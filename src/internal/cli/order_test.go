package cli

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"

	"github.com/brunoomariano/luna/src/internal/node"

	"github.com/brunoomariano/luna/src/internal/fsm"
)

// TestNextIssuesAnOrderForANewTask is the first half of phase 1a: a task that
// exists, an order that says what to run.
func TestNextIssuesAnOrderForANewTask(t *testing.T) {
	h := newHarness(t)
	h.mustRun(t, "task", "new", "LUNA-1", "--kind", "feature")

	out := h.mustRun(t, "next", "LUNA-1")

	if !strings.Contains(out, "kind=run") {
		t.Errorf("no run order in:\n%s", out)
	}
	if !strings.Contains(out, "task=LUNA-1") {
		t.Errorf("the order does not name its task:\n%s", out)
	}
	if !strings.Contains(out, "worktree=luna-LUNA-1") {
		t.Errorf("the order does not say where to work:\n%s", out)
	}
}

// TestNextIsAReadNotAStep is what makes the order safe for a caller that
// crashed. Asking twice must produce the same instruction and leave the log
// exactly as long as it was.
func TestNextIsAReadNotAStep(t *testing.T) {
	h := newHarness(t)
	h.mustRun(t, "task", "new", "LUNA-1", "--kind", "feature")

	before, err := h.env.Store.Events("LUNA-1")
	if err != nil {
		t.Fatalf("events: %v", err)
	}

	first := h.mustRun(t, "next", "LUNA-1")
	second := h.mustRun(t, "next", "LUNA-1")

	if first != second {
		t.Errorf("two reads gave two orders:\n%s\n---\n%s", first, second)
	}

	after, _ := h.env.Store.Events("LUNA-1")
	if len(after) != len(before) {
		t.Errorf("reading the order wrote %d event(s) — an order that advances the "+
			"flow by being read loses a stage whenever a caller retries",
			len(after)-len(before))
	}
}

func TestNextRefusesATaskThatDoesNotExist(t *testing.T) {
	h := newHarness(t)

	if err := h.run(t, "next", "NOPE-1"); err == nil {
		t.Fatal("an order was issued for a task that was never opened")
	}
}

// TestTheOrderComesInBothShapes is the decision that the order comes in two shapes: text for a
// person driving by hand, JSON for the lead once one is driving.
func TestTheOrderComesInBothShapes(t *testing.T) {
	h := newHarness(t)
	h.mustRun(t, "task", "new", "LUNA-1", "--kind", "feature")

	text := h.mustRun(t, "next", "LUNA-1")
	raw := h.mustRun(t, "next", "LUNA-1", "--json")

	var order fsm.Order
	if err := json.Unmarshal([]byte(raw), &order); err != nil {
		t.Fatalf("the JSON shape does not parse: %v\n%s", err, raw)
	}

	if order.Kind != fsm.OrderRun {
		t.Errorf("kind = %q, want run", order.Kind)
	}
	if !strings.Contains(text, "stage="+string(order.Stage)) {
		t.Errorf("the two shapes disagree about the stage:\n%s\n%s", text, raw)
	}
}

// TestOneTurnOfTheLoopByHand walks the cycle phase 1a exists to make usable:
// ask for the order, enter the stage, report what it delivered, ask again.
//
// It is driven entirely through the commands rather than by reaching into the
// store, because the point of phase 1a is that a person can run the machine
// without an agent — and a test that took a shortcut would not prove that.
func TestOneTurnOfTheLoopByHand(t *testing.T) {
	h := newHarness(t)
	h.mustRun(t, "task", "new", "LUNA-1", "--kind", "feature", "--profile", "nightly")

	first := mustOrderFrom(t, h, "LUNA-1")
	if first.Kind != fsm.OrderRun {
		t.Fatalf("kind = %q, want an order to run", first.Kind)
	}

	// Entering the stage is the reducer's job, not this command's: `next` reads
	// and `done` reports, and something has to open the stage between them.
	enterStage(t, h, "LUNA-1")

	owed := strings.Join(artifactNames(first.Produces), ",")
	handed := aRealCommit(t)
	out := h.mustRun(t, "done", "LUNA-1", "--delivered", owed, "--commit", handed)

	if !strings.Contains(out, "closed") {
		t.Fatalf("the stage did not close after delivering everything it owed: %s", out)
	}

	// The commit became the base, which is the handoff: the next stage starts
	// from what the last one produced.
	second := mustOrderFrom(t, h, "LUNA-1")
	if second.Base != handed {
		t.Errorf("base = %q, want the commit just handed in", second.Base)
	}
	if second.Stage == first.Stage {
		t.Errorf("the flow did not move past %q", first.Stage)
	}
}

// TestAStageThatDeliveredLessThanItOwedDoesNotClose is the contract check
// reaching the hand-driven path. No flag on `done` can talk the reducer into
// closing a stage that came up short.
func TestAStageThatDeliveredLessThanItOwedDoesNotClose(t *testing.T) {
	h := newHarness(t)
	h.mustRun(t, "task", "new", "LUNA-1", "--kind", "feature", "--profile", "nightly")

	enterStage(t, h, "LUNA-1")

	// An artifact the stage does not owe, and nothing it does. Reporting the
	// wrong thing is the same shortfall as reporting too little, and it is the
	// mistake a caller driving by hand actually makes.
	out := h.mustRun(t, "done", "LUNA-1", "--delivered", "something-else")

	if !strings.Contains(out, "did not close") {
		t.Fatalf("a short delivery closed the stage: %s", out)
	}
	if !strings.Contains(out, "still owed") {
		t.Errorf("the caller is not told what is outstanding: %s", out)
	}
	if state := mustState(t, h, "LUNA-1"); state.Status == fsm.StatusStageDone {
		t.Error("a stage that delivered the wrong artifact closed")
	}

	// Repeating it spends the retry budget, and then the task stops. No flag on
	// `done` closes a stage that never completed its contract.
	h.mustRun(t, "done", "LUNA-1", "--delivered", "something-else")
	last := h.mustRun(t, "done", "LUNA-1", "--delivered", "something-else")
	if !strings.Contains(last, "did not close") {
		t.Errorf("a stage that never delivered was allowed through: %s", last)
	}
	if state := mustState(t, h, "LUNA-1"); state.Status != fsm.StatusBlocked {
		t.Errorf("status = %q, want blocked once the budget is spent", state.Status)
	}
}

// enterStage opens the stage the order named. `next` is a read and `done`
// reports a finish, so the transition between them belongs to the engine.
//
// The decision is recorded rather than left empty, because an `Advance` that
// records nothing is a gate nobody answered and the reducer stops at it. The one
// `Advance` in the product always carries a decision (lead.go); leaving it off
// here was the harness taking a shortcut the real caller cannot, and it only went
// unnoticed while an unrecorded decision fell back to the profile.
//
// `passed` is what these tests mean: they drive a task whose gates are not the
// subject, and a gate the profile let through is exactly that.
func enterStage(t *testing.T, h *harness, id string) {
	t.Helper()

	state := mustState(t, h, id)
	advance := fsm.Advance{Flow: fsm.DefaultFlow(), Gate: fsm.GateAccount{Decision: fsm.GateDecisionPassed}}
	if err := h.env.Store.AppendActionAt(id, state.Seq, advance); err != nil {
		t.Fatalf("entering the stage: %v", err)
	}
}

func artifactNames(list []fsm.Artifact) []string {
	names := make([]string, 0, len(list))
	for _, a := range list {
		names = append(names, string(a))
	}
	return names
}

// TestDoneRefusesATaskWithNoRunningStage keeps the report honest. Reporting a
// stage finished when none is open is a caller that has lost track, and
// answering "ok" would let it carry on believing something happened.
func TestDoneRefusesATaskWithNoRunningStage(t *testing.T) {
	h := newHarness(t)
	h.mustRun(t, "task", "new", "LUNA-1", "--kind", "feature")

	// Strict on purpose, and `luna start` is the other half: `done` refusing a task
	// with no running stage is what makes a mistyped id a clean error instead of
	// two events in the log of a task nobody meant to touch.
	err := h.run(t, "done", "LUNA-1", "--delivered", "repos")
	if err == nil {
		t.Fatal("a stage was reported done on a task that had not started one")
	}
	if !strings.Contains(err.Error(), "ready") {
		t.Errorf("the refusal does not say what the task is instead: %v", err)
	}
}

// TestDoneNeedsToBeToldWhatWasDelivered guards the one argument that carries
// the whole meaning of the command.
//
// `done` with no `--delivered` is a caller saying "the stage finished" and
// nothing else, and there is no safe reading of it: taking the contract's list
// as implied would let a stage that produced none of it close on the say-so, and
// taking the empty list literally would block every stage with a message about
// artifacts nobody mentioned. Refusing at the edge is what makes the delivery
// the thing being recorded rather than the claim.
//
// Driven against a task with a stage actually open, because the earlier guards —
// no such task, nothing running — would otherwise answer first and this
// refusal would never be reached.
func TestDoneNeedsToBeToldWhatWasDelivered(t *testing.T) {
	h := newHarness(t)
	h.mustRun(t, "task", "new", "LUNA-1", "--kind", "feature", "--profile", "nightly")
	enterStage(t, h, "LUNA-1")

	for _, args := range [][]string{
		{"done", "LUNA-1"},
		{"done", "LUNA-1", "--delivered", ""},
		{"done", "LUNA-1", "--delivered", "  "},
	} {
		err := h.run(t, args...)

		if !errors.Is(err, ErrUsage) {
			t.Errorf("%v: a stage closed without saying what it produced, got %v", args, err)
			continue
		}
		if !strings.Contains(err.Error(), "--delivered") {
			t.Errorf("%v: the refusal must name the argument to add, got %v", args, err)
		}
	}

	// And nothing was recorded: a refusal that still appended would leave the
	// stage looking closed on the next replay.
	if state := mustState(t, h, "LUNA-1"); state.Status != fsm.StatusRunning {
		t.Errorf("a refused `done` moved the task to %q", state.Status)
	}
}

// TestDeliveredNamesAreTrimmedAndBlanksIgnored. `--delivered "repos, code"` is
// what a person types, and a leading space must not become part of an artifact
// name that then fails to match the contract.
func TestDeliveredNamesAreTrimmedAndBlanksIgnored(t *testing.T) {
	got, err := deliveredArtifacts(" repos , , code ")
	if err != nil {
		t.Fatalf("deliveredArtifacts: %v", err)
	}
	if len(got) != 2 || got[0] != "repos" || got[1] != "code" {
		t.Errorf("got %v, want [repos code] with the blank dropped", got)
	}
}

// TestAnAbandonedTaskHasNoOrderToGive closes the loop on the terminal statuses:
// the commands must refuse rather than issue an order for a task that is over.
func TestAnAbandonedTaskHasNoOrderToGive(t *testing.T) {
	h := newHarness(t)
	h.mustRun(t, "task", "new", "LUNA-1", "--kind", "feature")
	h.mustRun(t, "task", "abandon", "LUNA-1", "changed our minds")

	out := h.mustRun(t, "next", "LUNA-1")
	if !strings.Contains(out, "kind=done") {
		t.Errorf("an abandoned task got something other than a done order:\n%s", out)
	}
	if !strings.Contains(out, "called off") {
		t.Errorf("the order does not distinguish abandoned from finished:\n%s", out)
	}

	if err := h.run(t, "done", "LUNA-1", "--delivered", "repos"); err == nil {
		t.Error("a stage was reported done on an abandoned task")
	}
}

// TestStatusShowsTheWholeFlowAndNextDoesNot is the pair that keeps flow control
// where the decision put it: the panorama exists, and asking for it is a
// separate act from receiving an instruction.
func TestStatusShowsTheWholeFlowAndNextDoesNot(t *testing.T) {
	h := newHarness(t)
	h.mustRun(t, "task", "new", "LUNA-1", "--kind", "feature")

	status := h.mustRun(t, "status", "LUNA-1")
	order := h.mustRun(t, "next", "LUNA-1")

	stages := 0
	for _, stage := range fsm.DefaultFlow() {
		if strings.Contains(status, string(stage.ID)) {
			stages++
		}
	}
	if stages < len(fsm.DefaultFlow()) {
		t.Errorf("status showed %d of %d stages", stages, len(fsm.DefaultFlow()))
	}

	// The order names its own stage and nothing beyond it.
	named := 0
	for _, stage := range fsm.DefaultFlow() {
		if strings.Contains(order, string(stage.ID)) {
			named++
		}
	}
	if named > 1 {
		t.Errorf("the order mentions %d stages — it should name only the one it "+
			"orders, or the lead can decide to run ahead:\n%s", named, order)
	}
}

// TestStatusMarksSkippedStagesRatherThanHidingThem — "qa does not apply to a
// chore" is an answer, and a flow that dropped the row would read as a flow that
// forgot it.
func TestStatusMarksSkippedStagesRatherThanHidingThem(t *testing.T) {
	h := newHarness(t)
	h.mustRun(t, "task", "new", "LUNA-1", "--kind", "chore")

	raw := h.mustRun(t, "status", "LUNA-1", "--json")

	var report StatusReport
	if err := json.Unmarshal([]byte(raw), &report); err != nil {
		t.Fatalf("status --json does not parse: %v", err)
	}
	if len(report.Stages) != len(fsm.DefaultFlow()) {
		t.Errorf("status listed %d stages, the flow has %d — a skipped stage is "+
			"reported as skipped, not omitted", len(report.Stages), len(fsm.DefaultFlow()))
	}

	skipped := 0
	for _, stage := range report.Stages {
		if stage.State == "skipped" {
			skipped++
		}
	}
	if skipped == 0 {
		t.Error("a chore skips spec, qa and harden, and none was marked skipped")
	}
}

// TestTheOrderCommandsRefuseWhatTheyCannotAnswer covers the ways a caller gets
// it wrong. Each one is a refusal rather than a default, because every default
// available here would be a confident answer about a task that does not exist or
// a flag nobody typed.
func TestTheOrderCommandsRefuseWhatTheyCannotAnswer(t *testing.T) {
	for _, tc := range []struct {
		name string
		args []string
	}{
		{"next with no id", []string{"next"}},
		{"next with an unparseable flag", []string{"next", "LUNA-1", "--nonsense"}},
		{"done with no id", []string{"done"}},
		{"done on a task nobody opened", []string{"done", "GHOST-1", "--delivered", "repos"}},
		{"done with an empty delivery", []string{"done", "LUNA-1", "--delivered", " , "}},
		{"status with no id", []string{"status"}},
		{"status on a task nobody opened", []string{"status", "GHOST-1"}},
		{"status with an unparseable flag", []string{"status", "LUNA-1", "--nonsense"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			h := newHarness(t)
			h.mustRun(t, "task", "new", "LUNA-1", "--kind", "feature")

			if err := h.run(t, tc.args...); err == nil {
				t.Errorf("%v was accepted", tc.args)
			}
		})
	}
}

// TestStatusFollowsATaskAsItMoves is what the command is for: the marks have to
// mean something, or the panorama is decoration.
func TestStatusFollowsATaskAsItMoves(t *testing.T) {
	h := newHarness(t)
	h.mustRun(t, "task", "new", "LUNA-1", "--kind", "feature", "--profile", "nightly")

	first := mustOrderFrom(t, h, "LUNA-1")
	enterStage(t, h, "LUNA-1")
	handed := aRealCommit(t)
	h.mustRun(t, "done", "LUNA-1",
		"--delivered", strings.Join(artifactNames(first.Produces), ","),
		"--commit", handed)

	raw := h.mustRun(t, "status", "LUNA-1", "--json")
	var report StatusReport
	if err := json.Unmarshal([]byte(raw), &report); err != nil {
		t.Fatalf("status --json: %v", err)
	}

	if report.Base != handed {
		t.Errorf("base = %q, want the commit the closed stage delivered", report.Base)
	}
	if report.Stages[0].State != "current" {
		t.Errorf("the closed stage is %q; the task has not left it yet, so it is "+
			"still where the task stands", report.Stages[0].State)
	}

	// And the text shape says the same thing.
	if text := h.mustRun(t, "status", "LUNA-1"); !strings.Contains(text, handed) {
		t.Errorf("the text shape does not show the base:\n%s", text)
	}
}

func mustOrderFrom(t *testing.T, h *harness, id string) fsm.Order {
	t.Helper()
	raw := h.mustRun(t, "next", id, "--json")

	var order fsm.Order
	if err := json.Unmarshal([]byte(raw), &order); err != nil {
		t.Fatalf("next --json: %v", err)
	}
	return order
}

func mustState(t *testing.T, h *harness, id string) fsm.TaskState {
	t.Helper()
	state, err := h.env.Store.ReplayOwnFlow(id)
	if err != nil {
		t.Fatalf("replay: %v", err)
	}
	return state
}

// seedAtFirstGate walks a task to the first stage that opens a gate, and leaves
// it waiting there.
//
// Tests about gates want a task at a gate, not a task at a particular stage. They
// used to reach one with a single Advance because `discovery` was stage 1 and
// gated; integration left Luna's scope along with `commit`, so the first gate is now three
// stages in. Walking until a gate opens says what the test means and survives the
// next change to the flow.
func seedAtFirstGate(t *testing.T, h *harness, id string) fsm.StageID {
	t.Helper()

	flow := fsm.DefaultFlow()
	for range flow {
		state := mustState(t, h, id)
		if state.Gate != nil {
			return state.Stage
		}

		if state.Status == fsm.StatusRunning {
			// Close the stage it is in, delivering whatever the contract asks for,
			// so the walk can move on to the next one.
			stage := stageByID(flow, state.Stage)
			owed := append(append([]fsm.Artifact{}, stage.Produces...), stage.ProducesForHuman...)
			evidence := map[fsm.Artifact]fsm.Evidence{}
			for _, a := range owed {
				evidence[a] = fsm.Evidence{
					Scope:   fsm.VerifierFor(stage, a).Proves(),
					Verdict: fsm.VerdictPassed,
				}
			}
			if err := h.env.Store.AppendActionAt(id, state.Seq, fsm.Complete{
				Delivered: owed, Evidence: evidence, Flow: flow, Commit: "c0ffee" + string(state.Stage),
			}); err != nil {
				t.Fatalf("closing %s: %v", state.Stage, err)
			}
			continue
		}

		if err := h.env.Store.AppendActionAt(id, state.Seq, fsm.Advance{
			Flow: flow,
			Gate: fsm.GateAccount{Decision: fsm.GateDecisionWaited},
		}); err != nil {
			t.Fatalf("walking to the first gate: %v", err)
		}
	}
	t.Fatalf("no stage in the flow opens a gate")
	return ""
}

// stageByID finds a stage in a flow, for tests that walk it.
func stageByID(flow []fsm.Stage, id fsm.StageID) fsm.Stage {
	for _, s := range flow {
		if s.ID == id {
			return s
		}
	}
	return fsm.Stage{ID: id}
}

// TestDoneRefusesACommitThatDoesNotResolve closes a hole the lead found by
// reading the design rather than by hitting it.
//
// It said: "a stage can self-report a commit SHA that Luna never verifies
// exists. The delivery check caught the weak proof, but a fabricated SHA would
// sail past a stage whose check *was* strong enough." Probed, and it was right —
// forty hex characters became the task's `base`, which is the commit the next
// stage branches from.
//
// SwarmForge's lesson is the one this restores: validate the commit by running
// git, not by checking that the text looks like a SHA.
func TestDoneRefusesACommitThatDoesNotResolve(t *testing.T) {
	h := newHarness(t)
	h.mustRun(t, "task", "new", "LUNA-1", "--kind", "feature", "--profile", "nightly")
	enterStage(t, h, "LUNA-1")

	err := Run(h.env, []string{
		"done", "LUNA-1", "--delivered", "worktree",
		"--commit", "deadbeefdeadbeefdeadbeefdeadbeefdeadbeef",
	})

	if err == nil {
		t.Fatal("a commit that resolves to nothing was accepted as the delivery")
	}
	if !strings.Contains(err.Error(), "deadbeef") {
		t.Errorf("the refusal must name the value it refused, got %q", err)
	}

	if state := mustState(t, h, "LUNA-1"); strings.Contains(state.Base, "deadbeef") {
		t.Error("the fabricated commit became the base the next stage branches from")
	}
}

// aRealCommit is a commit that resolves, for the tests that hand one to `luna
// done`.
//
// `done` checks the commit against git now, because a fabricated SHA used to
// become the base the next stage branches from. So a test that means "a
// delivery happened" has to name a delivery that exists — HEAD of the
// repository the test runs in.
func aRealCommit(t *testing.T) string {
	t.Helper()
	sha, err := node.ResolveCommit(context.Background(), ".", "HEAD")
	if err != nil {
		t.Fatalf("reading a commit to hand over: %v", err)
	}
	return sha
}

// TestTheOrderSaysHowEachArtifactIsProven. The order named what to deliver and
// never what delivering would be measured by — fine for an agent Luna briefs
// itself, and wrong for the reader driving by hand, who was told to produce
// `ci_green` and left to guess that the check is `make ci` at full scope.
func TestTheOrderSaysHowEachArtifactIsProven(t *testing.T) {
	h := newHarness(t)
	h.mustRun(t, "task", "new", "H-4", "--kind", "chore", "--flow", "chore")
	openStage(t, h, "H-4")
	h.mustRun(t, "done", "H-4", "--delivered", "worktree")

	out := h.mustRun(t, "next", "H-4")
	if !strings.Contains(out, "make test") {
		t.Errorf("the order does not say what proves tests_green:\n%s", out)
	}
	if !strings.Contains(out, "targeted") {
		t.Errorf("the order does not say what scope that earns:\n%s", out)
	}

	// And the brief is the stage's, not the role's: a person driving by hand got
	// strictly less than the agent Luna starts for the same stage.
	if !strings.Contains(out, "What you owe") {
		t.Errorf("the order carries the role's brief rather than the stage's:\n%s", out)
	}
}

// TestStartOnAnAlreadyOpenStageChangesNothing. The command has to be safe to
// repeat, for the same reason `next` is: whoever is driving by hand may have lost
// track of whether they ran it, and a second run must not advance the flow.
func TestStartOnAnAlreadyOpenStageChangesNothing(t *testing.T) {
	h := newHarness(t)
	h.mustRun(t, "task", "new", "H-5", "--kind", "chore", "--flow", "chore")
	openStage(t, h, "H-5")

	before, err := h.env.Store.Events("H-5")
	if err != nil {
		t.Fatalf("reading the log: %v", err)
	}

	openStage(t, h, "H-5")

	after, _ := h.env.Store.Events("H-5")
	if len(after) != len(before) {
		t.Errorf("starting an open stage wrote %d event(s)", len(after)-len(before))
	}
}

// TestStartSaysWhenAGateIsInTheWay rather than reporting a stage it did not open.
// A gate on the way in is the ordinary reason nothing opens, and "opened at" would
// be a lie a person acts on.
func TestStartSaysWhenAGateIsInTheWay(t *testing.T) {
	h := newHarness(t)
	h.mustRun(t, "task", "new", "H-6", "--kind", "feature")

	// Drive to the stage whose closing opens the shipped flow's one gate.
	for range 4 {
		state := mustState(t, h, "H-6")
		if state.Status == fsm.StatusAwaitingGate {
			break
		}
		openStage(t, h, "H-6")
		stage := stageIn(fsm.DefaultFlow(), mustState(t, h, "H-6").Stage)
		owed := append(append([]fsm.Artifact{}, stage.Produces...), stage.ProducesForHuman...)
		if len(owed) == 0 {
			break
		}
		if err := h.run(t, "done", "H-6", "--delivered", strings.Join(artifactNames(owed), ",")); err != nil {
			break
		}
	}

	if state := mustState(t, h, "H-6"); state.Status == fsm.StatusAwaitingGate {
		// The opener leaves an open gate exactly where it is: walking past one is
		// not the same act as deciding it.
		openStage(t, h, "H-6")
		if after := mustState(t, h, "H-6"); after.Status != fsm.StatusAwaitingGate {
			t.Errorf("opening a stage walked past a gate: %q", after.Status)
		}
	}
}

// TestAMechanicalStageKeepsTheEmptyBrief. There is no agent to address, and a
// brief written for nobody is noise in the one command a person reads closely.
func TestAMechanicalStageKeepsTheEmptyBrief(t *testing.T) {
	h := newHarness(t)
	h.mustRun(t, "task", "new", "H-7", "--kind", "chore", "--flow", "chore")

	out := h.mustRun(t, "next", "H-7")
	if strings.Contains(out, "brief=") {
		t.Errorf("a mechanical stage carries a brief:\n%s", out)
	}
	if !strings.Contains(out, "worktree: delivered") {
		t.Errorf("it lost the proof line with it:\n%s", out)
	}
}

// TestAnOrderThatIsNotWorkCarriesNoBrief. Three of the four order kinds are
// endings — waiting, blocked, done — and briefing somebody about work that is not
// theirs to start would read as an instruction.
func TestAnOrderThatIsNotWorkCarriesNoBrief(t *testing.T) {
	h := newHarness(t)
	h.mustRun(t, "task", "new", "H-8", "--kind", "chore", "--flow", "chore")
	h.mustRun(t, "task", "abandon", "H-8", "superseded")

	out := h.mustRun(t, "next", "H-8")
	if strings.Contains(out, "brief=") {
		t.Errorf("an ended task was briefed:\n%s", out)
	}
	if !strings.Contains(out, "kind=done") {
		t.Errorf("the order does not report the ending:\n%s", out)
	}
}

// TestTheHandDrivenLoopRefusesATaskWhoseFlowIsGone, the same way every other
// reader does — the remedy is `task abandon`, and start pretending otherwise
// would open a stage against a contract this build cannot read.
func TestTheHandDrivenLoopRefusesATaskWhoseFlowIsGone(t *testing.T) {
	h := newHarness(t)
	if err := h.env.Store.AppendAction("H-9", fsm.TaskCreated{
		Kind: fsm.KindFeature, FlowName: "a-flow-nobody-ships",
	}); err != nil {
		t.Fatalf("creating the task: %v", err)
	}

	if err := h.run(t, "start", "H-9"); err == nil {
		t.Error("a task whose flow is gone was opened")
	}
	if err := h.run(t, "next", "H-9"); err == nil {
		t.Error("an order was issued against a flow this build does not have")
	}
}

// openStage opens the stage a task's order names, the way `luna lead` does.
//
// The hand-driven opener is gone with its mode, so a test that needs a running
// stage goes through the lead's own entry — which is also the only opener left,
// and therefore the one worth exercising.
func openStage(t *testing.T, h *harness, id string) {
	t.Helper()

	flow, err := h.env.flowOf(id)
	if err != nil {
		t.Fatalf("resolving %s's flow: %v", id, err)
	}
	if err := leadFor(h.env, ".", flow).Enter(context.Background(), id); err != nil {
		t.Fatalf("opening a stage for %s: %v", id, err)
	}
}

// TestTheLeadOpensAgainstTheTasksOwnFlow. A Lead built without a flow replays
// against this build's default, so every task on any other flow was refused by
// its own fingerprint before it could start. The hand-driven opener had a test
// for this and the lead's did not — and the lead's is the one that survived.
func TestTheLeadOpensAgainstTheTasksOwnFlow(t *testing.T) {
	h := newHarness(t)
	h.mustRun(t, "task", "new", "H-2", "--kind", "bug", "--flow", "fix")

	openStage(t, h, "H-2")

	if state := mustState(t, h, "H-2"); state.Stage != "setup" {
		t.Errorf("a task on a non-default flow opened at %q", state.Stage)
	}
}
