package cli

import (
	"encoding/json"
	"strings"
	"testing"

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

// TestTheOrderComesInBothShapes is the decision recorded in RFC-0002: text for a
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
	out := h.mustRun(t, "done", "LUNA-1", "--delivered", owed, "--commit", "abc1234")

	if !strings.Contains(out, "closed") {
		t.Fatalf("the stage did not close after delivering everything it owed: %s", out)
	}

	// The commit became the base, which is the handoff: the next stage starts
	// from what the last one produced (INV-core-6).
	second := mustOrderFrom(t, h, "LUNA-1")
	if second.Base != "abc1234" {
		t.Errorf("base = %q, want the commit just handed in", second.Base)
	}
	if second.Stage == first.Stage {
		t.Errorf("the flow did not move past %q", first.Stage)
	}
}

// TestAStageThatDeliveredLessThanItOwedDoesNotClose is the contract check
// reaching the hand-driven path. No flag on `done` can talk the reducer into
// closing a stage that came up short (INV-core-3).
func TestAStageThatDeliveredLessThanItOwedDoesNotClose(t *testing.T) {
	h := newHarness(t)
	h.mustRun(t, "task", "new", "LUNA-1", "--kind", "feature", "--profile", "nightly")

	order := mustOrderFrom(t, h, "LUNA-1")
	if len(order.Produces) < 2 {
		t.Skipf("the first stage owes %d artifact(s); this test needs a stage that owes more than one",
			len(order.Produces))
	}
	enterStage(t, h, "LUNA-1")

	// Everything but the last one.
	short := artifactNames(order.Produces)[:len(order.Produces)-1]
	out := h.mustRun(t, "done", "LUNA-1", "--delivered", strings.Join(short, ","))

	if !strings.Contains(out, "did not close") {
		t.Fatalf("a short delivery closed the stage: %s", out)
	}
	if state := mustState(t, h, "LUNA-1"); state.Status != fsm.StatusBlocked {
		t.Errorf("status = %q, want blocked", state.Status)
	}
}

// enterStage opens the stage the order named. `next` is a read and `done`
// reports a finish, so the transition between them belongs to the engine.
func enterStage(t *testing.T, h *harness, id string) {
	t.Helper()

	state := mustState(t, h, id)
	if err := h.env.Store.AppendActionAt(id, state.Seq, fsm.Advance{Flow: fsm.DefaultFlow()}); err != nil {
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

	err := h.run(t, "done", "LUNA-1", "--delivered", "repos")
	if err == nil {
		t.Fatal("a stage was reported done on a task that had not started one")
	}
	if !strings.Contains(err.Error(), "ready") {
		t.Errorf("the refusal does not say what the task is instead: %v", err)
	}
}

func TestDoneNeedsToBeToldWhatWasDelivered(t *testing.T) {
	h := newHarness(t)
	h.mustRun(t, "task", "new", "LUNA-1", "--kind", "feature")

	err := h.run(t, "done", "LUNA-1")
	if err == nil {
		t.Fatal("a stage closed without saying what it produced")
	}
}

// TestStatusShowsTheWholeFlowAndNextDoesNot is the pair that keeps INV-core-1
// where the decision put it: the panorama exists, and asking for it is a
// separate act from receiving an instruction (ADR-0052).
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
// forgot it (ADR-0014).
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
	h.mustRun(t, "done", "LUNA-1",
		"--delivered", strings.Join(artifactNames(first.Produces), ","),
		"--commit", "c0ffee")

	raw := h.mustRun(t, "status", "LUNA-1", "--json")
	var report StatusReport
	if err := json.Unmarshal([]byte(raw), &report); err != nil {
		t.Fatalf("status --json: %v", err)
	}

	if report.Base != "c0ffee" {
		t.Errorf("base = %q, want the commit the closed stage delivered", report.Base)
	}
	if report.Stages[0].State != "current" {
		t.Errorf("the closed stage is %q; the task has not left it yet, so it is "+
			"still where the task stands", report.Stages[0].State)
	}

	// And the text shape says the same thing.
	if text := h.mustRun(t, "status", "LUNA-1"); !strings.Contains(text, "c0ffee") {
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
	state, err := h.env.Store.Replay(id, fsm.DefaultFlow())
	if err != nil {
		t.Fatalf("replay: %v", err)
	}
	return state
}
