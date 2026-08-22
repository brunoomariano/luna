package fsm

import "testing"

// TestASimulationIsNeverVerified. `--dry-run` already told this lie once: it
// recorded the scope each contract declared with the truth in a Detail field the
// renderer never printed, so `luna task show` displayed a passing `make ci` for a
// command nothing ran. A simulation is its own verdict rather than `verified`
// with a note beside it, because a reader sees the word and not the note.
func TestASimulationIsNeverVerified(t *testing.T) {
	state := TaskState{
		Status:    StatusDone,
		Simulated: true,
		Evidence: map[Artifact]Evidence{
			"ci_green": {Scope: ScopeFull, Verdict: VerdictPassed, Command: "make ci"},
		},
	}

	if got := state.Product(); got != ProductSimulated {
		t.Errorf("a simulation reports %q", got)
	}
}

// TestAFailedCheckOutranksEverythingThatPassed. One verdict that ran and said no
// is the answer, whatever else is green — the alternative counts checks, and a
// count lets a failure be outvoted.
func TestAFailedCheckOutranksEverythingThatPassed(t *testing.T) {
	state := TaskState{
		Status: StatusDone,
		Evidence: map[Artifact]Evidence{
			"code":        {Scope: ScopeExistence, Verdict: VerdictPassed},
			"tests_green": {Scope: ScopeTargeted, Verdict: VerdictPassed},
			"ci_green":    {Scope: ScopeFull, Verdict: VerdictFailed},
		},
	}

	if got := state.Product(); got != ProductFailed {
		t.Errorf("a task holding a failed check reports %q", got)
	}
}

// TestTheTwoVerdictsAreIndependent is the whole reason there are two of them: the
// case that motivated this is a run that delivered correct code and stopped on a
// cosmetic artifact, which one column would have to call either a success or a
// failure and both readings are wrong.
func TestTheTwoVerdictsAreIndependent(t *testing.T) {
	stopped := TaskState{
		Status:    StatusBlocked,
		BlockedBy: BlockContract,
		Evidence: map[Artifact]Evidence{
			"ci_green": {Scope: ScopeFull, Verdict: VerdictPassed, Command: "make ci"},
		},
	}

	if got := stopped.Product(); got != ProductPartial {
		t.Errorf("work that was proven reports %q", got)
	}
	if got := stopped.Operation(); got != OperationStopped {
		t.Errorf("a blocked task reports %q", got)
	}
}

// TestEveryEndingHasAnOperationalVerdict. A status this does not name would fall
// through to "running", which reads as progress — the silent ending INV-5 forbids,
// arriving through a report rather than through the engine.
func TestEveryEndingHasAnOperationalVerdict(t *testing.T) {
	for status, want := range map[Status]Operation{
		StatusDone:         OperationClean,
		StatusAbandoned:    OperationCalledOff,
		StatusAwaitingGate: OperationWaiting,
		StatusBlocked:      OperationStopped,
		StatusRunning:      OperationRunning,
		StatusStageDone:    OperationRunning,
		StatusReady:        OperationRunning,
	} {
		if got := (TaskState{Status: status}).Operation(); got != want {
			t.Errorf("status %q reports operation %q, want %q", status, got, want)
		}
	}
}

// TestATaskThatProvedNothingSaysSo. "none" and "verified" have to be tellable
// apart by a reader who only has the verdict, or an unattended run that did
// nothing reads like one that did everything.
func TestATaskThatProvedNothingSaysSo(t *testing.T) {
	if got := (TaskState{Status: StatusReady}).Product(); got != ProductNone {
		t.Errorf("a task that has proven nothing reports %q", got)
	}
}

// TestEveryBlockCarriesItsKind walks the reducer's blocking paths, because the
// field is only useful if it is set everywhere — one path that blocks without a
// kind is a task the morning report cannot sort, and the empty value would read
// as a category rather than as an omission.
func TestEveryBlockCarriesItsKind(t *testing.T) {
	flow, err := FlowNamed("chore")
	if err != nil {
		t.Fatalf("loading the chore flow: %v", err)
	}

	// The budget path: a stage closed, the ceiling is spent.
	state := NewTaskState("V-1", KindChore)
	if state, err = Reduce(state, TaskCreated{Kind: KindChore, FlowName: "chore", BudgetUSD: 1}); err != nil {
		t.Fatalf("creating: %v", err)
	}
	if state, err = Reduce(state, Advance{Flow: flow}); err != nil {
		t.Fatalf("advancing: %v", err)
	}
	if state, err = Reduce(state, Complete{
		Flow:      flow,
		Delivered: []Artifact{"worktree"},
		Evidence:  map[Artifact]Evidence{"worktree": Exists(1)},
		Spent:     Spend{CostUSD: 9},
		Commit:    "abc",
	}); err != nil {
		t.Fatalf("completing: %v", err)
	}
	if state, err = Reduce(state, Advance{Flow: flow}); err != nil {
		t.Fatalf("advancing past the ceiling: %v", err)
	}
	if state.BlockedBy != BlockBudget {
		t.Errorf("a budget block is typed %q", state.BlockedBy)
	}

	// And clearing it clears the kind too, or a resumed task keeps a label for a
	// block that is over.
	if state, err = Reduce(state, Unblock{}); err != nil {
		t.Fatalf("unblocking: %v", err)
	}
	if state.BlockedBy != "" {
		t.Errorf("an unblocked task still reports %q", state.BlockedBy)
	}

	// The tooling path, which arrives as its own action from the node layer.
	tooling, err := Reduce(state, Block{Reason: "git is not installed"})
	if err != nil {
		t.Fatalf("blocking: %v", err)
	}
	if tooling.BlockedBy != BlockTooling {
		t.Errorf("an infrastructure block is typed %q", tooling.BlockedBy)
	}
}
