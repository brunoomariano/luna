package fsm

import (
	"strings"
	"testing"
)

// budgeted is a task that has closed a stage and is about to open another, with a
// ceiling and a bill against it.
func budgeted(t *testing.T, ceiling, spent float64) TaskState {
	t.Helper()

	state := NewTaskState("B-1", "")
	state, err := Reduce(state, TaskCreated{Kind: KindChore, FlowName: "chore", BudgetUSD: ceiling})
	if err != nil {
		t.Fatalf("creating the task: %v", err)
	}

	flow, err := FlowNamed("chore")
	if err != nil {
		t.Fatalf("loading the chore flow: %v", err)
	}
	if state, err = Reduce(state, Advance{Flow: flow}); err != nil {
		t.Fatalf("opening the first stage: %v", err)
	}
	state, err = Reduce(state, Complete{
		Flow:      flow,
		Delivered: []Artifact{"worktree"},
		Evidence:  map[Artifact]Evidence{"worktree": Exists(state.Seq)},
		Spent:     Spend{CostUSD: spent},
		Commit:    "abc123",
	})
	if err != nil {
		t.Fatalf("closing the first stage: %v", err)
	}
	return state
}

// chore is the flow the tests above walk.
func chore(t *testing.T) []Stage {
	t.Helper()
	flow, err := FlowNamed("chore")
	if err != nil {
		t.Fatalf("loading the chore flow: %v", err)
	}
	return flow
}

// TestATaskOverItsBudgetOpensNoFurtherStage is the whole mechanism. Before this,
// CostUSD was recorded in the log, printed by two commands, and compared against
// nothing — so an unattended run had no ceiling at all.
func TestATaskOverItsBudgetOpensNoFurtherStage(t *testing.T) {
	state := budgeted(t, 1.00, 1.50)

	after, err := Reduce(state, Advance{Flow: chore(t)})
	if err != nil {
		t.Fatalf("advancing: %v", err)
	}

	if after.Status != StatusBlocked {
		t.Fatalf("a task $0.50 over its ceiling opened another stage (%s)", after.Status)
	}
	for _, want := range []string{"over budget", "1.50", "1.00"} {
		if !strings.Contains(after.Blocked, want) {
			t.Errorf("the block does not say %q: %s", want, after.Blocked)
		}
	}
}

// TestTheBudgetStopsTheNextStageAndNotTheOneThatPaid is what makes the ceiling
// recoverable rather than destructive.
//
// The stage that went over has already closed: its commit is the base, its
// artifacts are in the context. Blocking where the money was spent would have
// thrown that away and re-billed the same work on the way back.
func TestTheBudgetStopsTheNextStageAndNotTheOneThatPaid(t *testing.T) {
	state := budgeted(t, 1.00, 1.50)

	after, err := Reduce(state, Advance{Flow: chore(t)})
	if err != nil {
		t.Fatalf("advancing: %v", err)
	}

	if after.Base != "abc123" {
		t.Errorf("the delivery that was paid for was lost: base %q", after.Base)
	}
	if !after.Context.Artifacts["worktree"] {
		t.Error("the artifact that was paid for did not stay in the context")
	}
	if after.Stage != "build" {
		t.Errorf("the task is parked at %q, not at the stage that was about to open", after.Stage)
	}
}

// TestRaisingTheBudgetAndUnblockingCarriesOn. A limit with no way past it makes
// the cheapest failure the one nobody can recover from, so the ceiling moves.
func TestRaisingTheBudgetAndUnblockingCarriesOn(t *testing.T) {
	state := budgeted(t, 1.00, 1.50)
	state, err := Reduce(state, Advance{Flow: chore(t)})
	if err != nil {
		t.Fatalf("advancing: %v", err)
	}

	if state, err = Reduce(state, SetBudget{BudgetUSD: 5, Reason: "worth finishing"}); err != nil {
		t.Fatalf("raising the ceiling: %v", err)
	}
	if state.Status != StatusBlocked {
		t.Error("raising the ceiling resumed the task by itself")
	}

	if state, err = Reduce(state, Unblock{}); err != nil {
		t.Fatalf("unblocking: %v", err)
	}
	if state.Status != StatusRunning {
		t.Fatalf("the task did not resume: %s", state.Status)
	}
	if state.Stage != "build" {
		t.Errorf("it resumed at %q rather than the stage it was parked at", state.Stage)
	}
}

// TestNoBudgetIsNoCeiling. Zero has to mean unbounded and not "spend nothing":
// every task opened before budgets existed carries zero, and reading that as a
// ceiling would stop all of them at their first stage.
func TestNoBudgetIsNoCeiling(t *testing.T) {
	state := budgeted(t, 0, 999)

	after, err := Reduce(state, Advance{Flow: chore(t)})
	if err != nil {
		t.Fatalf("advancing: %v", err)
	}
	if after.Status != StatusRunning {
		t.Errorf("a task with no ceiling was stopped by one (%s): %s", after.Status, after.Blocked)
	}
}

// TestSpendingExactlyTheBudgetIsNotOverIt. The boundary is stated rather than
// left to a reader of the comparison: the ceiling is what may be spent, not what
// may be approached.
func TestSpendingExactlyTheBudgetIsNotOverIt(t *testing.T) {
	state := budgeted(t, 1.00, 1.00)

	after, err := Reduce(state, Advance{Flow: chore(t)})
	if err != nil {
		t.Fatalf("advancing: %v", err)
	}
	if after.Status != StatusRunning {
		t.Errorf("spending exactly the ceiling was treated as over it: %s", after.Blocked)
	}
}

// TestANegativeBudgetIsRefused. Clamping it to zero would remove the ceiling at
// exactly the moment somebody was trying to impose one.
func TestANegativeBudgetIsRefused(t *testing.T) {
	state := budgeted(t, 1.00, 0.10)

	if _, err := Reduce(state, SetBudget{BudgetUSD: -1}); err == nil {
		t.Fatal("a negative ceiling was accepted")
	}
}

func TestAUSDBudgetCannotBeSetAfterUnpricedUsage(t *testing.T) {
	state := NewTaskState("B-codex", "")
	state.Spent = map[StageID]Spend{
		"build": {Agent: "codex", InputTokens: 100, OutputTokens: 20},
	}

	_, err := Reduce(state, SetBudget{BudgetUSD: 5})
	if err == nil {
		t.Fatal("a USD ceiling was accepted after usage whose price is unknown")
	}
	for _, want := range []string{"USD budget", "unpriced"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("the refusal does not name %q: %v", want, err)
		}
	}
}

// TestABudgetIsNotMovedOnATaskThatEnded. The terms a run happens under mean
// nothing once there is no run, and accepting it would put an event in the log
// that describes a decision nobody could act on.
func TestABudgetIsNotMovedOnATaskThatEnded(t *testing.T) {
	state := budgeted(t, 1.00, 0.10)
	state, err := Reduce(state, Abandon{Reason: "superseded"})
	if err != nil {
		t.Fatalf("abandoning: %v", err)
	}

	if _, err := Reduce(state, SetBudget{BudgetUSD: 5}); err == nil {
		t.Fatal("the ceiling moved on a task that had ended")
	}
}

// TestParseBudgetUSDRefusesWhatItCannotRead. The value becomes part of an
// append-only opening event, so a ceiling nobody meant is one a task stops on in a
// way nobody expects.
func TestParseBudgetUSDRefusesWhatItCannotRead(t *testing.T) {
	for _, bad := range []string{"lots", "5 dollars", "-0.01", "1e"} {
		if _, err := ParseBudgetUSD(bad); err == nil {
			t.Errorf("ParseBudgetUSD(%q) was accepted", bad)
		}
	}

	// Absent is no ceiling, which is what every task had before there were any.
	if usd, err := ParseBudgetUSD(""); err != nil || usd != 0 {
		t.Errorf("an absent budget parsed as %v (%v)", usd, err)
	}
	for value, want := range map[string]float64{"5": 5, "2.50": 2.5, "0": 0} {
		got, err := ParseBudgetUSD(value)
		if err != nil || got != want {
			t.Errorf("ParseBudgetUSD(%q) = %v (%v), want %v", value, got, err, want)
		}
	}
}
