package cli

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/brunoomariano/luna/src/internal/lead"
	"github.com/brunoomariano/luna/src/internal/store"

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
	order, err := fsm.NextOrder(state, fsm.DefaultFlow())
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

	if err := h.run(t, "fleet", "run", "LUNA-1"); err != nil {
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

	err := h.run(t, "fleet", "run", "LUNA-1")

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

	out := h.mustRun(t, "fleet", "run", "LUNA-1")

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

	if err := h.run(t, "fleet", "run", "LUNA-1", "--autonomy", "10"); err != nil {
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
		if err := h.run(t, "fleet", "run", "LUNA-1", "--autonomy", value); err == nil {
			t.Errorf("--autonomy %s was accepted", value)
		}
	}

	// And an absent flag is the most supervised setting, never the widest.
	h, lead := leadHarness(t)
	if err := h.run(t, "fleet", "run", "LUNA-1"); err != nil {
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

	err := h.run(t, "fleet", "run", "LUNA-1")
	if err == nil {
		t.Fatal("a lead command with no model reported success")
	}
	if !strings.Contains(err.Error(), "luna lead") {
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

	if err := h.run(t, "fleet", "run", "LUNA-1"); err == nil {
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

	out := h.mustRun(t, "fleet", "run", "LUNA-1")

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

	if err := h.run(t, "fleet", "run", "LUNA-1"); err != nil {
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
		{"fleet", "run", "LUNA-1", "--nonsense", "x"},
		{"fleet", "run", "LUNA-1", "--autonomy"},
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
		order, err := fsm.NextOrder(state, fsm.DefaultFlow())
		if err != nil {
			return "", err
		}
		owed := strings.Join(artifactNames(order.Produces), ",")
		if err := Run(h.env, []string{"done", "LUNA-1", "--delivered", owed}); err != nil {
			return "", err
		}
		return "carried out " + string(order.Stage), nil
	}

	if err := h.run(t, "fleet", "run", "LUNA-1"); err != nil {
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

// TestWhatTheLeadConcludedAboutAGateIsKept covers a judgement that cost a model
// call and was thrown away.
//
// A gate the lead does not approve stays open, which is right. What was wrong is
// that nothing recorded why: the reasoning went to the model, came back, was
// printed to a terminal and left no event at all. Measured on TALLY-6, where the
// lead found a real contradiction in a contract — an obligation requiring six
// test cases against a permitted five — and a person opening that gate the next
// day would have seen "waiting for review" and none of it.
func TestWhatTheLeadConcludedAboutAGateIsKept(t *testing.T) {
	h, obedient := leadHarness(t)
	h.mustRun(t, "autonomy", "LUNA-1", "9", "measuring")

	// The obedient lead carries out the stages, so the task actually reaches a
	// gate; only the judging question is answered by this test.
	const finding = "obligation 7 wants six cases and the contract permits five"
	h.env.Lead = func(ctx context.Context, prompt string) (string, error) {
		if strings.Contains(prompt, "You are answering a gate") {
			return "REJECT\n\n" + finding, nil
		}
		return obedient.ask(ctx, prompt)
	}

	if err := h.run(t, "fleet", "run", "LUNA-1", "--autonomy", "9"); err != nil {
		t.Fatalf("lead: %v", err)
	}

	state := mustState(t, h, "LUNA-1")
	// Not a skip: a flow that reaches no gate makes every assertion below
	// vacuous, and a test that passes by never arriving is the kind that gets
	// trusted without having held.
	if state.Gate == nil {
		t.Fatal("the task never reached a gate, so nothing here was exercised")
	}

	if state.Gate.Judged != "reject" {
		t.Errorf("the gate does not carry what the lead concluded, got %q", state.Gate.Judged)
	}
	if !strings.Contains(state.Gate.Reasoning, "six cases") {
		t.Errorf("the reasoning was not kept: %q", state.Gate.Reasoning)
	}

	// And a person opening the gate is shown it, because a verdict without the
	// working asks them to take a model's word for it.
	h.out.Reset()
	h.mustRun(t, "gate", "show", "LUNA-1")
	if !strings.Contains(h.out.String(), finding) {
		t.Errorf("`gate show` does not show what the lead concluded:\n%s", h.out.String())
	}
}

// TestTheLeadIsWiredToLandAndToWarn covers two fields that were simply absent.
//
// `luna run` built its lead with Land and Warn; `luna lead` built a different
// one without them. So a task conducted by the lead finished with its work
// reachable only through the stage branches, `luna status` printed a landing ref
// nothing had created, and the warning that would have said so had nowhere to
// go — the silent failure INV-5 forbids, arriving through a struct literal.
//
// Measured on TALLY-7: six real commits, `done`, and no `luna/TALLY-7/done`.
//
// Asserted against the command's own wiring rather than by running a task,
// because what was wrong is which fields the command sets.
// TestConductTaskLandsAFinishedTask covers the call that was missing, not the
// field that was set.
//
// `luna lead` runs its own loop — `conductTask`, not `Lead.Run` — and only
// `Lead.Run` landed. A previous fix wired the Land field on this path and
// nothing invoked it, so TALLY-8 finished six stages with its work reachable
// only through the stage branches while `luna status` printed
// `luna/TALLY-8/done` for a ref that did not exist.
//
// The earlier test asserted the field was non-nil, and passed throughout.
func TestConductTaskLandsAFinishedTask(t *testing.T) {
	var landed struct{ task, commit string }

	conductor := leadFor(newHarness(t).env, ".", fsm.DefaultFlow())
	conductor.Land = func(_ context.Context, taskID, commit string) error {
		landed.task, landed.commit = taskID, commit
		return nil
	}

	conductor.PointBranchIfDone(context.Background(), fsm.TaskState{
		ID: "LUNA-1", Status: fsm.StatusDone, Base: "abc123",
	})

	if landed.task != "LUNA-1" || landed.commit != "abc123" {
		t.Errorf("a finished task was not landed, got %+v", landed)
	}

	// And a task that has not finished is not landed, so a caller can hand any
	// ending state to it.
	landed.task = ""
	conductor.PointBranchIfDone(context.Background(), fsm.TaskState{
		ID: "LUNA-2", Status: fsm.StatusAwaitingGate, Base: "abc123",
	})
	if landed.task != "" {
		t.Errorf("a task waiting at a gate was landed: %+v", landed)
	}
}

// TestConductTaskLandsWhatTheLoopEndsOn is the same guarantee through the
// command's own loop, which is where it broke.
//
// The test above holds with the call site removed — that is exactly the state
// TALLY-8 shipped in, and why it shipped. This one runs `luna lead` against a
// task that is already done and asks whether the loop lands it on the way out.
// A done task gives the loop nothing to conduct, so it takes the ending branch
// immediately, which is the branch that was missing the call.
func TestConductTaskLandsWhatTheLoopEndsOn(t *testing.T) {
	h := newHarness(t)
	h.mustRun(t, "task", "new", "LUNA-1", "--simulated", "--kind", "chore", "--profile", "nightly")
	h.mustRun(t, "autonomy", "LUNA-1", "10")

	// Driven to done by the dry runner, which needs no agent.
	_ = Run(h.env, []string{"fleet", "run", "LUNA-1", "--dry-run"})
	if state := mustState(t, h, "LUNA-1"); !state.IsTerminal() {
		t.Skipf("the dry run did not finish the task (it is %q)", state.Status)
	}

	var landed string
	h.env.Land = func(_ context.Context, taskID, _ string) error {
		landed = taskID
		return nil
	}
	h.env.Lead = func(context.Context, string) (string, error) {
		return "", errors.New("the loop asked a model about a finished task")
	}

	if err := Run(h.env, []string{"fleet", "run", "LUNA-1", "--autonomy", "10"}); err != nil {
		t.Fatalf("conducting a finished task: %v", err)
	}
	if landed != "LUNA-1" {
		t.Error("`luna lead` ended on a finished task and never pointed its branch")
	}
}

func TestTheLeadIsWiredToLandAndToWarn(t *testing.T) {
	h, _ := leadHarness(t)
	h.env.Lead = func(context.Context, string) (string, error) { return "stopping", nil }

	// The command builds its lead and runs it; a task that goes nowhere is fine
	// here, since the assertion is about the fields.
	_ = Run(h.env, []string{"fleet", "run", "LUNA-1"})

	// Rebuilt the same way the command does, which is the thing under test: if
	// leadCommand stops setting these, this constructor has to stop too or the
	// test is asserting about a struct nothing uses.
	built := leadFor(h.env, ".", fsm.DefaultFlow())
	if built.Land == nil {
		t.Error("the lead cannot land: a finished task's work stays on the stage branches")
	}
	if built.Warn == nil {
		t.Error("the lead has nowhere to report a landing that failed")
	}
}

// TestTheAccountIsInTheJSONAndNotOnlyInTheProse covers the reader that is not a
// person.
//
// `gate show` printed what the lead concluded and `task show --json` did not, so
// anything reading Luna through its machine surface — a dashboard, a fleet, a
// script deciding which gate to put in front of somebody — saw a gate with
// nothing said about it, and the two surfaces disagreed about the same gate.
func TestTheAccountIsInTheJSONAndNotOnlyInTheProse(t *testing.T) {
	state := fsm.TaskState{
		ID:     "LUNA-1",
		Status: fsm.StatusAwaitingGate,
		Gate: &fsm.PendingGate{
			Kind: fsm.GateReviewArtifact, Stage: "plan", Artifact: "contract",
			Judged:    "reject",
			Reasoning: "obligation 7 wants six cases and the contract permits five",
		},
	}

	report := taskReport(Config{}, state, 0, fsm.DefaultFlow())
	if report.Gate == nil {
		t.Fatal("an open gate is missing from the report")
	}
	if report.Gate.Judged != "reject" {
		t.Errorf("the verdict is not in the JSON: %q", report.Gate.Judged)
	}
	if !strings.Contains(report.Gate.Reasoning, "six cases") {
		t.Errorf("the reasoning is not in the JSON: %q", report.Gate.Reasoning)
	}
}

// soloNode reports that every stage delivered exactly what it declared, and
// remembers the role each one ran under.
//
// A named fake rather than an inline stub, and it records the role because that
// is the whole difference between the two modes: a solo run resolves one role for
// every stage, a pack resolves the one each stage declares.
type soloNode struct{ roles []string }

func (n *soloNode) Run(_ context.Context, state fsm.TaskState, stage fsm.Stage) (lead.Result, error) {
	n.roles = append(n.roles, stage.Role)

	owed := append(append([]fsm.Artifact{}, stage.Produces...), stage.ProducesForHuman...)
	evidence := map[fsm.Artifact]fsm.Evidence{}
	for _, artifact := range owed {
		evidence[artifact] = fsm.Evidence{
			Scope: fsm.ScopeExistence, Verdict: fsm.VerdictPassed, RecordedAt: state.Seq,
		}
	}
	return lead.Result{Delivered: owed, Evidence: evidence, Commit: state.Base}, nil
}

// TestASoloRunCarriesEveryStageUnderOneRole is the mode `luna lead` runs.
//
// One agent for the whole task, which is what a person asking for a single agent
// means — and the role is how it is one: the worktree and the session are both
// keyed by role, so collapsing them is what makes them survive from stage to
// stage. A run that resolved the flow's declared roles would open a worktree per
// specialism and start a cold agent in each, which is the pack and is the other
// command.
func TestASoloRunCarriesEveryStageUnderOneRole(t *testing.T) {
	h := newHarness(t)
	h.mustRun(t, "task", "new", "S-1", "--kind", "chore", "--flow", "chore")

	node := &soloNode{}
	h.env.Node = func(Env, runOptions, []fsm.Stage) (lead.Node, func(), error) {
		return node, func() {}, nil
	}
	// No model: a solo run needs none, and that is a property worth holding.
	h.env.Lead = nil

	if err := h.run(t, "lead", "S-1"); err != nil {
		t.Fatalf("lead: %v", err)
	}

	if len(node.roles) == 0 {
		t.Fatal("no stage ran, so this test measures nothing")
	}
	for _, role := range node.roles {
		// A mechanical stage has none, and keeps none: a stage that starts no agent
		// has nobody to be.
		if role != "" && role != fsm.SoloRole {
			t.Errorf("a solo run started a stage under %q rather than the one role", role)
		}
	}

	// And the flow it ran still declares its pack, untouched: the collapse is a
	// reading of the flow for one run, never an edit to it.
	if stageIn(fsm.DefaultFlow(), "build").Role != "coder" {
		t.Error("a solo run rewrote the flow's declared roles")
	}
}

// TestAPackRunResolvesTheRolesTheFlowDeclares is the other half, and the two
// together are the whole difference between the modes.
func TestAPackRunResolvesTheRolesTheFlowDeclares(t *testing.T) {
	h := newHarness(t)
	h.mustRun(t, "task", "new", "P-1", "--kind", "bug", "--flow", "fix")

	node := &soloNode{}
	h.env.Node = func(Env, runOptions, []fsm.Stage) (lead.Node, func(), error) {
		return node, func() {}, nil
	}

	// The pack is conducted, so it needs a model; this one carries out whatever
	// order it is given by running the stage through `luna work`.
	h.env.Lead = func(_ context.Context, prompt string) (string, error) {
		id := ""
		for _, line := range strings.Split(prompt, "\n") {
			if rest, found := strings.CutPrefix(strings.TrimSpace(line), "task="); found {
				id = rest
				break
			}
		}
		if id == "" {
			return "", errors.New("the order named no task")
		}
		return "carried it out", Run(h.env, []string{"work", id})
	}

	if err := h.run(t, "fleet", "run", "P-1"); err != nil {
		t.Fatalf("fleet run: %v", err)
	}

	var sawSpecialist bool
	for _, role := range node.roles {
		if role == fsm.SoloRole {
			t.Errorf("a pack run collapsed a stage onto the solo role")
		}
		if role != "" {
			sawSpecialist = true
		}
	}
	if !sawSpecialist {
		t.Fatal("no stage with a role ran, so this test measures nothing")
	}
}

// TestASoloRunThatStopsAtAGateShowsWhatWasConcluded. A run that ends at a gate is
// the ordinary ending, and the person who has to answer it should not have to run
// a second command to see that a model already looked.
func TestASoloRunThatStopsAtAGateShowsWhatWasConcluded(t *testing.T) {
	h := newHarness(t)

	gated := fsm.TaskState{
		ID: "S-2", Status: fsm.StatusAwaitingGate, Stage: "plan",
		Gate: &fsm.PendingGate{
			Kind: fsm.GateReviewArtifact, Stage: "plan", Reason: "review the contract",
			Judged: "reject", Reasoning: "obligation 7 wants six cases and the contract permits five",
		},
	}
	reportSoloEnding(h.env, "S-2", gated)

	out := h.out.String()
	for _, want := range []string{"waiting", "review the contract", "reject", "six cases"} {
		if !strings.Contains(out, want) {
			t.Errorf("the ending does not carry %q:\n%s", want, out)
		}
	}
}

// TestASoloRunOnAnUnexpectedStatusSaysWhereItStopped covers the branch no ordinary
// run reaches. A fourth way out of the loop must not finish in silence.
func TestASoloRunOnAnUnexpectedStatusSaysWhereItStopped(t *testing.T) {
	h := newHarness(t)

	reportSoloEnding(h.env, "S-3", fsm.TaskState{
		ID: "S-3", Status: fsm.StatusRunning, Stage: "build",
	})

	out := h.out.String()
	if !strings.Contains(out, "build") || !strings.Contains(out, string(fsm.StatusRunning)) {
		t.Errorf("an unexpected ending said neither the stage nor the status:\n%s", out)
	}
}

// TestASoloRunOnAGateWithNoDetailDoesNotPanic. A gate normally carries a reason,
// and the ending reads it defensively: printing a blank line beats dying on the
// command a person ran to find out what happened.
func TestASoloRunOnAGateWithNoDetailDoesNotPanic(t *testing.T) {
	h := newHarness(t)

	reportSoloEnding(h.env, "S-4", fsm.TaskState{
		ID: "S-4", Status: fsm.StatusAwaitingGate, Stage: "plan",
	})

	if !strings.Contains(h.out.String(), "S-4") {
		t.Errorf("a gate with no detail said nothing at all:\n%s", h.out.String())
	}
}

// TestBothModesRefuseAFlagTheyDoNotHave. A typo that runs is worse than one that
// stops: the run behaves as though nobody had asked for anything, and the person
// finds out from the bill.
func TestBothModesRefuseAFlagTheyDoNotHave(t *testing.T) {
	h := newHarness(t)
	h.mustRun(t, "task", "new", "F-1", "--kind", "chore", "--flow", "chore")

	if err := h.run(t, "lead", "F-1", "--nonsense", "1"); !errors.Is(err, ErrUsage) {
		t.Errorf("`luna lead` accepted a flag it does not have: %v", err)
	}
	if err := h.run(t, "fleet", "run", "F-1", "--nonsense", "1"); !errors.Is(err, ErrUsage) {
		t.Errorf("`luna fleet run` accepted a flag it does not have: %v", err)
	}
}

// TestAModeRefusesATaskWhoseFlowThisBuildCannotRead. The order is computed from
// the flow, so a flow that is gone is not a run that goes wrong halfway — it is
// one that must not start.
func TestAModeRefusesATaskWhoseFlowThisBuildCannotRead(t *testing.T) {
	h := newHarness(t)
	if err := h.env.Store.AppendAction("GONE-2", fsm.TaskCreated{
		Kind: fsm.KindChore, FlowName: "a-flow-nobody-ships",
	}); err != nil {
		t.Fatalf("creating the task: %v", err)
	}

	if err := h.run(t, "lead", "GONE-2"); err == nil {
		t.Error("a solo run started against a flow this build cannot read")
	}
	if err := h.run(t, "fleet", "run", "GONE-2"); err == nil {
		t.Error("a pack run started against a flow this build cannot read")
	}
}

// TestAStageLookupOnAFlowThatDoesNotHaveItIsEmpty. The caller reached the lookup
// through NextOrder, which already refused a stage the flow does not have — so the
// zero stage is the honest answer and not a case anybody has to handle.
func TestAStageLookupOnAFlowThatDoesNotHaveItIsEmpty(t *testing.T) {
	if got := stageIn(fsm.DefaultFlow(), "nowhere"); got.ID != "" {
		t.Errorf("a stage that is not in the flow resolved to %q", got.ID)
	}
}

// TestASoloRunIsRefusedOnASimulatedTask. Its stages recorded checks that never
// ran, so continuing it for real would build on proof nobody produced — and the
// guard has to be on both modes, because it is a fact about the task rather than
// about how it is being carried out.
func TestASoloRunIsRefusedOnASimulatedTask(t *testing.T) {
	h := newHarness(t)
	h.mustRun(t, "task", "new", "S-5", "--kind", "chore", "--flow", "chore", "--simulated")

	err := h.run(t, "lead", "S-5")
	if err == nil {
		t.Fatal("a simulated task was carried out for real")
	}
	if !strings.Contains(err.Error(), "simulation") {
		t.Errorf("the refusal does not say why: %v", err)
	}
}

// TestTheReportWindowRefusesADurationItCannotRead. `--since yesterday` silently
// read as "everything" would answer a different question from the one asked.
func TestTheReportWindowRefusesADurationItCannotRead(t *testing.T) {
	h := newHarness(t)

	if err := h.run(t, "fleet", "report", "--since", "yesterday"); !errors.Is(err, ErrUsage) {
		t.Errorf("a window nobody can parse was accepted: %v", err)
	}
}

// TestTheLeadIsHandedNothingRatherThanTheWrongThing. The artifact lookup answers
// the gate's judge, and an artifact that was never handed over has no body to
// read. Answering with an empty string and "found" would put a blank document in
// front of a model and ask it to judge one.
func TestTheLeadIsHandedNothingRatherThanTheWrongThing(t *testing.T) {
	h := newHarness(t)
	built := leadFor(h.env, ".", fsm.DefaultFlow())

	if built.Artifact == nil {
		t.Fatal("the lead cannot fetch the artifact a gate is about")
	}
	if _, found := built.Artifact("NOBODY-1", "contract"); found {
		t.Error("an artifact nobody handed over was reported as fetched")
	}

	// And the one that was handed over comes back whole. A gate that asks the lead
	// to judge a contract has to hand it the contract, not the evidence line that
	// names it — `gate show` printed a name and a blank line until this existed.
	h.mustRun(t, "task", "new", "A-1", "--kind", "chore", "--flow", "chore")
	if err := h.env.Store.PutBlob(store.Blob{
		TaskID: "A-1", Stage: "plan", Artifact: "contract", Body: []byte("# contract\n"),
	}); err != nil {
		t.Fatalf("handing an artifact over: %v", err)
	}
	body, found := built.Artifact("A-1", "contract")
	if !found || !strings.Contains(body, "# contract") {
		t.Errorf("the artifact came back as %q (found=%v)", body, found)
	}
}

// TestTheRealLandingIsUsedWhenNoneIsInjected. The injected one wins so a test can
// watch a landing without a repository; with none, the command has to supply the
// real thing rather than leaving the field nil and landing nothing.
func TestTheRealLandingIsUsedWhenNoneIsInjected(t *testing.T) {
	h := newHarness(t)
	h.env.Land = nil

	if landingFor(h.env, ".") == nil {
		t.Error("with nothing injected there is no landing at all, so `done` means nothing")
	}
}

// TestReadingAnOrderCarriesTheReasonItCouldNotBeRead. The order is computed from
// the task and its flow, and each of the three reads can fail — a task nobody
// created, a flow this build does not carry, an order the flow cannot produce.
// The loop stops on any of them, so the reason has to survive the return.
func TestReadingAnOrderCarriesTheReasonItCouldNotBeRead(t *testing.T) {
	h := newHarness(t)

	if _, _, err := orderFor(h.env, "NOBODY-2"); err == nil {
		t.Error("an order was produced for a task nobody created")
	}

	if err := h.env.Store.AppendAction("GONE-3", fsm.TaskCreated{
		Kind: fsm.KindChore, FlowName: "a-flow-nobody-ships",
	}); err != nil {
		t.Fatalf("creating the task: %v", err)
	}
	_, _, err := orderFor(h.env, "GONE-3")
	if err == nil {
		t.Fatal("an order was produced against a flow this build cannot read")
	}
	if !strings.Contains(err.Error(), "a-flow-nobody-ships") {
		t.Errorf("the refusal does not name the flow it could not find: %v", err)
	}
}

// TestAnInjectedLandingWinsOverTheRealOne is what lets a test watch a landing
// without a repository — and the landing is the half of `done` that broke twice
// with nothing seeing it.
func TestAnInjectedLandingWinsOverTheRealOne(t *testing.T) {
	h := newHarness(t)

	var landed string
	h.env.Land = func(_ context.Context, taskID, _ string) error {
		landed = taskID
		return nil
	}

	if err := landingFor(h.env, ".")(context.Background(), "L-1", "c0ffee"); err != nil {
		t.Fatalf("landing: %v", err)
	}
	if landed != "L-1" {
		t.Error("the injected landing was not the one called")
	}
}
