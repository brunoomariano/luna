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

	// What a real lead does, and only that: report what the stage produced.
	//
	// This used to open the stage first, with `enterStage` — a test helper. That
	// made the suite prove `luna lead` worked while the step it depended on
	// existed nowhere but here: a real lead has no command that opens a stage,
	// and `luna next` changes nothing by design. So every run stalled on the
	// first stage with "no running stage to finish", and no test could see it.
	// Measured on TALLY-4. Entering is the loop's job now, which is what a lead
	// can actually rely on.
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
// saying so has to do nothing at all.
func TestALeadThatClaimsSuccessWithoutReportingMovesNothing(t *testing.T) {
	h := newHarness(t)
	h.mustRun(t, "task", "new", "LUNA-1", "--kind", "feature", "--profile", "nightly")

	h.env.Lead = func(context.Context, string) (string, error) {
		return "Done. I completed every stage and the task is finished.", nil
	}

	err := h.run(t, "lead", "LUNA-1")

	if err == nil {
		t.Fatal("a lead that only claimed to have finished was believed")
	}
	if !strings.Contains(err.Error(), "luna done") {
		t.Errorf("the refusal does not say what the lead failed to do: %v", err)
	}

	// The loop opens the first stage before asking — that is the engine's move,
	// and it is what `luna done` needs to have something to close. What must not
	// have happened is the flow going anywhere on the strength of what the lead
	// said: it claimed every stage was finished, so the test is that the task is
	// sitting in the *first* one, unfinished.
	after := mustState(t, h, "LUNA-1")
	if after.Status != fsm.StatusRunning {
		t.Errorf("want the first stage still open, got %q", after.Status)
	}
	if after.Stage != fsm.DefaultFlow()[0].ID {
		t.Errorf("the task moved past the first stage on the lead's word alone, to %q",
			after.Stage)
	}
	if len(after.Context.Artifacts) > 1 {
		t.Errorf("a lead's claim delivered artifacts: %v", after.Context.Artifacts)
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

	// Interactive stops at every gate; walk to the first one there is.
	seedAtFirstGate(t, h, "LUNA-1")

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

	if err := h.run(t, "lead", "LUNA-1", "--autonomy", "10"); err != nil {
		t.Fatalf("lead: %v", err)
	}

	if !strings.Contains(lead.saw[0], "That judgement is yours") {
		t.Errorf("--autonomy 10 did not reach the brief:\n%s", lead.saw[0])
	}
}

// TestTheKnobIsANumberAndTheOldNamesAreGone is the fold, from the surface a
// person types at.
//
// The three names are refused rather than aliased onto knob values: a flag
// meaning "knob 5" would authorise the lead to judge gates up to criticality 5
// without the word "gate" appearing anywhere.
func TestTheKnobIsANumberAndTheOldNamesAreGone(t *testing.T) {
	for _, value := range []string{"ask", "retry", "decide", "whatever", "11", "-1"} {
		h, _ := leadHarness(t)
		if err := h.run(t, "lead", "LUNA-1", "--autonomy", value); err == nil {
			t.Errorf("--autonomy %s was accepted", value)
		}
	}

	// And an absent flag is the most supervised setting, never the widest.
	h, lead := leadHarness(t)
	if err := h.run(t, "lead", "LUNA-1"); err != nil {
		t.Fatalf("lead with no knob: %v", err)
	}
	if strings.Contains(lead.saw[0], "That judgement is yours") {
		t.Error("an unset knob briefed the lead as though it could decide")
	}
}

// TestWithNoLeadTheCommandSaysSoAndNamesTheAlternative. Luna hosts no model
// , and a machine with none should be told what does work rather than
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

	if err := h.run(t, "lead", "LUNA-1"); err == nil {
		t.Fatal("a lead that never answered was treated as having conducted the stage")
	}

	// The loop opens the stage before asking, so the log has moved by one — what
	// must not have happened is the stage *closing*. A lead that said nothing
	// delivered nothing, and the stage it was handed is still open.
	after := mustState(t, h, "LUNA-1")
	if after.Status != fsm.StatusRunning {
		t.Errorf("want the stage still open, got %q", after.Status)
	}
	if len(after.Context.Artifacts) > 1 {
		t.Errorf("a lead that never answered delivered something: %v", after.Context.Artifacts)
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
		// Every turn now finds a stage already open, because the loop opens it.
		// Failing is the only thing this lead ever does, which is the point.
		_ = h.env.Store.AppendAction("LUNA-1", fsm.Fail{Reason: "not this time"})
		return "failed it", nil
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
// one prompt.
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

// TestTheLeadCanCloseTheStageItWasHandedIsTheWholeLoop is the fix for a
// conductor that could never conduct anything.
//
// The loop the lead is briefed on is `luna next` for the order, do the work,
// `luna done` to report. But `next` reads and changes nothing by design, so the
// task stayed `ready` and `done` answered "no running stage to finish" — every
// time, on the first stage, for any task.
//
// Measured on TALLY-4 under `--autonomy 9`. The lead diagnosed it exactly,
// declined to reach for `luna run` because choosing how far a task advances is
// not its call, and escalated instead of retrying a deterministic failure. It
// was right on all three counts, and there was nothing it could have done.
//
// Entering the stage is the engine's move, not the lead's: a task that is not
// running has one next step and the status says which, so nothing is being
// decided here that a model could get wrong.
func TestTheLeadCanCloseTheStageItWasHandedIsTheWholeLoop(t *testing.T) {
	h := newHarness(t)
	h.mustRun(t, "task", "new", "LUNA-1")

	state, err := h.env.replay("LUNA-1")
	if err != nil {
		t.Fatalf("replaying: %v", err)
	}
	if state.Status != fsm.StatusReady {
		t.Fatalf("a new task is ready, got %q", state.Status)
	}

	// A lead that can do nothing but report. It is what a real one is: `luna next`
	// changes nothing, and there is no command that opens a stage — so if the
	// loop does not open it, `luna done` has nothing to close and this fails the
	// way TALLY-4 did.
	reported := 0
	h.env.Lead = func(context.Context, string) (string, error) {
		reported++
		state := mustState(t, h, "LUNA-1")
		order, err := fsm.NextOrder(state, fsm.DefaultFlow(), h.env.profiles().Roles)
		if err != nil {
			return "", err
		}
		owed := strings.Join(artifactNames(order.Produces), ",")
		if err := Run(h.env, []string{"done", "LUNA-1", "--delivered", owed}); err != nil {
			return "", err
		}
		return "carried out " + string(order.Stage), nil
	}

	if err := h.run(t, "lead", "LUNA-1"); err != nil {
		t.Fatalf("a lead that reports what it produced must be able to conduct: %v", err)
	}
	if reported < 2 {
		t.Fatalf("the loop stopped after %d turn(s) — the lead never got past one stage", reported)
	}

	after := mustState(t, h, "LUNA-1")
	if after.Stage == "" {
		t.Error("the task never entered a stage")
	}
	if len(after.Context.Artifacts) < 2 {
		t.Errorf("nothing was delivered, so no stage ever closed: %v", after.Context.Artifacts)
	}
}
