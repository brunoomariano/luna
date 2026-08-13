package cli

import (
	"context"
	"strings"
	"testing"

	"github.com/brunoomariano/luna/src/internal/fsm"
)

// obedientLead carries out the order it is given by driving the same commands a
// real lead would: it enters the stage and reports through `luna done`.
//
// A named fake rather than a canned string, because what is being tested is a
// loop, and a lead that never moves the task would make every one of these tests
// pass by stopping on the first turn.
type obedientLead struct {
	h     *harness
	t     *testing.T
	saw   []string
	turns int
}

func (o *obedientLead) ask(_ context.Context, prompt string) (string, error) {
	o.saw = append(o.saw, prompt)
	o.turns++

	state := mustState(o.t, o.h, "LUNA-1")
	order, err := fsm.NextOrder(state, fsm.DefaultFlow(), o.h.env.profiles().Roles)
	if err != nil {
		return "", err
	}

	// What a real lead does: open the stage, then report what it produced.
	enterStage(o.t, o.h, "LUNA-1")
	owed := strings.Join(artifactNames(order.Produces), ",")
	if err := Run(o.h.env, []string{"done", "LUNA-1", "--delivered", owed}); err != nil {
		return "", err
	}
	return "carried out " + string(order.Stage), nil
}

func leadHarness(t *testing.T) (*harness, *obedientLead) {
	t.Helper()

	h := newHarness(t)
	h.mustRun(t, "task", "new", "LUNA-1", "--kind", "feature", "--profile", "nightly")

	lead := &obedientLead{h: h, t: t}
	h.env.Lead = lead.ask
	return h, lead
}

// TestTheLeadDrivesTheTaskThroughItsStages is phase 4 working: Luna hands out
// orders, the lead carries them out, and the task walks its flow.
func TestTheLeadDrivesTheTaskThroughItsStages(t *testing.T) {
	h, lead := leadHarness(t)

	if err := h.run(t, "lead", "LUNA-1"); err != nil {
		t.Fatalf("lead: %v", err)
	}

	if lead.turns < 2 {
		t.Fatalf("the lead was asked %d times — the loop did not run", lead.turns)
	}

	state := mustState(t, h, "LUNA-1")
	if state.Stage == "" {
		t.Error("the task never entered a stage")
	}
}

// TestALeadThatClaimsSuccessWithoutReportingMovesNothing is the guarantee this
// phase turns on. A model can say anything, including that it finished — and
// saying so has to do nothing at all (INV-core-1).
func TestALeadThatClaimsSuccessWithoutReportingMovesNothing(t *testing.T) {
	h := newHarness(t)
	h.mustRun(t, "task", "new", "LUNA-1", "--kind", "feature", "--profile", "nightly")

	h.env.Lead = func(context.Context, string) (string, error) {
		return "Done. I completed every stage and the task is finished.", nil
	}

	before := mustState(t, h, "LUNA-1")
	err := h.run(t, "lead", "LUNA-1")

	if err == nil {
		t.Fatal("a lead that only claimed to have finished was believed")
	}
	if !strings.Contains(err.Error(), "luna done") {
		t.Errorf("the refusal does not say what the lead failed to do: %v", err)
	}

	after := mustState(t, h, "LUNA-1")
	if after.Seq != before.Seq || after.Stage != before.Stage {
		t.Errorf("the task moved on the lead's word alone: %+v then %+v", before, after)
	}
}

// TestTheLoopStopsWhenSomethingNeedsAPerson. A gate and a block are both endings
// the lead does not get to push past.
func TestTheLoopStopsWhenSomethingNeedsAPerson(t *testing.T) {
	h := newHarness(t)
	h.mustRun(t, "task", "new", "LUNA-1", "--kind", "feature", "--profile", "interactive")

	asked := 0
	h.env.Lead = func(context.Context, string) (string, error) {
		asked++
		return "carried on", nil
	}

	// Interactive stops at the first gate, which the first stage opens.
	if err := h.env.Store.AppendAction("LUNA-1", fsm.Advance{
		Flow:         fsm.DefaultFlow(),
		GateDecision: fsm.GateDecisionWaited,
	}); err != nil {
		t.Fatalf("advancing to the gate: %v", err)
	}

	out := h.mustRun(t, "lead", "LUNA-1")

	if asked != 0 {
		t.Errorf("the lead was asked to act on a task waiting for a person (%d times)", asked)
	}
	if !strings.Contains(out, string(fsm.OrderWait)) {
		t.Errorf("the ending was not reported: %s", out)
	}
}

// TestTheAutonomyKnobReachesTheLead. It is what bounds the one judgement the
// lead has, and a knob that never reaches the prompt bounds nothing.
func TestTheAutonomyKnobReachesTheLead(t *testing.T) {
	h, lead := leadHarness(t)

	if err := h.run(t, "lead", "LUNA-1", "--autonomy", "ask"); err != nil {
		t.Fatalf("lead: %v", err)
	}

	if !strings.Contains(lead.saw[0], "Do not retry") {
		t.Errorf("--autonomy ask did not reach the brief:\n%s", lead.saw[0])
	}
}

func TestAnUnknownAutonomyIsRefusedAtTheCommandLine(t *testing.T) {
	h, _ := leadHarness(t)

	if err := h.run(t, "lead", "LUNA-1", "--autonomy", "whatever"); err == nil {
		t.Fatal("an unknown autonomy was accepted")
	}
}

// TestWithNoLeadTheCommandSaysSoAndNamesTheAlternative. Luna hosts no model
// (ADR-0043), and a machine with none should be told what does work rather than
// what does not.
func TestWithNoLeadTheCommandSaysSoAndNamesTheAlternative(t *testing.T) {
	h := newHarness(t)
	h.mustRun(t, "task", "new", "LUNA-1", "--kind", "feature")
	h.env.Lead = nil

	err := h.run(t, "lead", "LUNA-1")
	if err == nil {
		t.Fatal("a lead command with no model reported success")
	}
	if !strings.Contains(err.Error(), "luna run") {
		t.Errorf("the refusal does not name the alternative: %v", err)
	}
}

func TestLeadNeedsATaskThatExists(t *testing.T) {
	h := newHarness(t)
	h.env.Lead = func(context.Context, string) (string, error) { return "", nil }

	if err := h.run(t, "lead"); err == nil {
		t.Fatal("lead with no id was accepted")
	}
	if err := h.run(t, "lead", "GHOST-1"); err == nil {
		t.Fatal("a task nobody opened was conducted")
	}
}

// TestALeadThatWillNotAnswerStopsTheRun. A model that errors mid-flow is not a
// stage failure and must not be recorded as one: nothing about the task changed.
func TestALeadThatWillNotAnswerStopsTheRun(t *testing.T) {
	h := newHarness(t)
	h.mustRun(t, "task", "new", "LUNA-1", "--kind", "feature", "--profile", "nightly")
	h.env.Lead = func(context.Context, string) (string, error) {
		return "", context.DeadlineExceeded
	}

	before := mustState(t, h, "LUNA-1")
	if err := h.run(t, "lead", "LUNA-1"); err == nil {
		t.Fatal("a lead that never answered was treated as having conducted the stage")
	}

	if after := mustState(t, h, "LUNA-1"); after.Seq != before.Seq {
		t.Error("the task moved even though the lead never answered")
	}
}

// TestALeadThatCannotFinishAStageStillTerminates is why there is no turn cap.
//
// A cap was written first, on the reasoning that a model can talk itself into a
// loop. Nothing could reach it: the lead cannot keep a stage open, because
// failing it spends the retry budget and the third failure blocks — and a
// blocked task returns an order that is not OrderRun, which ends the loop.
//
// This walks that path and insists it ends, so the reasoning for the cap's
// absence is checked rather than asserted.
func TestALeadThatCannotFinishAStageStillTerminates(t *testing.T) {
	h := newHarness(t)
	h.mustRun(t, "task", "new", "LUNA-1", "--kind", "feature", "--profile", "nightly")

	turns := 0
	h.env.Lead = func(context.Context, string) (string, error) {
		turns++
		if mustState(t, h, "LUNA-1").Status == fsm.StatusRunning {
			_ = h.env.Store.AppendAction("LUNA-1", fsm.Fail{Reason: "not this time"})
			return "failed it", nil
		}
		enterStage(t, h, "LUNA-1")
		return "entered it", nil
	}

	out := h.mustRun(t, "lead", "LUNA-1")

	if !strings.Contains(out, string(fsm.OrderBlocked)) {
		t.Errorf("the run did not end at a block:\n%s", out)
	}
	if turns > 10 {
		t.Errorf("it took %d turns to give up on one stage — the retry budget "+
			"is meant to bound this", turns)
	}
	if state := mustState(t, h, "LUNA-1"); state.Status != fsm.StatusBlocked {
		t.Errorf("status = %q, want blocked", state.Status)
	}
}

// TestTheLeadIsGivenOneOrderAtATime — never the flow, and never two stages in
// one prompt (ADR-0052).
func TestTheLeadIsGivenOneOrderAtATime(t *testing.T) {
	h, lead := leadHarness(t)

	if err := h.run(t, "lead", "LUNA-1"); err != nil {
		t.Fatalf("lead: %v", err)
	}

	for i, prompt := range lead.saw {
		stages := 0
		for _, stage := range fsm.DefaultFlow() {
			if strings.Contains(prompt, "stage="+string(stage.ID)) {
				stages++
			}
		}
		if stages > 1 {
			t.Errorf("turn %d gave the lead %d stages at once", i+1, stages)
		}
	}
}

// TestLeadRefusesACommandLineItCannotParse. A flag nobody recognises is a
// caller who meant something, and guessing which setting they wanted is how an
// unattended run happens by accident.
func TestLeadRefusesACommandLineItCannotParse(t *testing.T) {
	h, _ := leadHarness(t)

	for _, args := range [][]string{
		{"lead", "LUNA-1", "--nonsense", "x"},
		{"lead", "LUNA-1", "--autonomy"},
	} {
		if err := h.run(t, args...); err == nil {
			t.Errorf("%v was accepted", args)
		}
	}
}
