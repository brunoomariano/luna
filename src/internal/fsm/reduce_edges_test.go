package fsm

import (
	"errors"
	"strings"
	"testing"
)

// TestActionsSatisfyTheClosedSet guards the marker method.
//
// Action is a closed set: the unexported isAction keeps a type outside this
// package from joining it, so Reduce's switch cannot meet something it has never
// heard of. The assertions are the whole point — if a type stops implementing it,
// this file stops compiling, which is the earliest possible warning.
func TestActionsSatisfyTheClosedSet(t *testing.T) {
	actions := []Action{
		Advance{},
		Complete{},
		Fail{},
		GateApprove{},
		GateAdjust{},
		GateReject{},
		ReviewFinding{},
		Unblock{},
	}

	if len(actions) != 8 {
		t.Errorf("want the 8 known actions, got %d", len(actions))
	}
	for _, a := range actions {
		a.isAction() // compiles only while the type stays in the set
	}
}

// unknownAction is a type from inside the package that satisfies Action without
// Reduce knowing it — the only way to reach the default branch, and the reason
// that branch exists.
type unknownAction struct{}

func (unknownAction) isAction() {}

// TestUnknownActionIsRefused covers Reduce's default branch.
func TestUnknownActionIsRefused(t *testing.T) {
	_, err := Reduce(NewTaskState("LUNA-1", KindFeature), unknownAction{})

	if !errors.Is(err, ErrIllegalTransition) {
		t.Errorf("an action Reduce does not know must be refused, got %v", err)
	}
}

// TestUnblockRefusesATaskThatIsNotBlocked covers the guard in unblock.
func TestUnblockRefusesATaskThatIsNotBlocked(t *testing.T) {
	running := TaskState{Status: StatusRunning, Stage: "build"}

	if _, err := Reduce(running, Unblock{}); !errors.Is(err, ErrIllegalTransition) {
		t.Errorf("want ErrIllegalTransition unblocking a running task, got %v", err)
	}
}

// TestEachLoopCeilingIsReportedByName covers every branch of ceilingHit.
//
// The three ceilings exist because they detect different pathologies;
// a message that did not distinguish them would collapse the distinction the
// separate counters were introduced to preserve.
func TestEachLoopCeilingIsReportedByName(t *testing.T) {
	limits := DefaultLoopLimits()

	cases := []struct {
		name  string
		loop  LoopCounters
		hit   bool
		about string
	}{
		{"under every ceiling", LoopCounters{Rounds: 1}, false, ""},
		{"too many rounds", LoopCounters{Rounds: limits.MaxRounds + 1}, true, "rounds"},
		{"no functional change", LoopCounters{NoProgress: limits.NoProgress}, true, "no functional change"},
		{"back to the same stages", LoopCounters{Oscillation: limits.Oscillation}, true, "same stages"},
	}

	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			reason := ceilingHit(c.loop, limits)
			if c.hit && reason == "" {
				t.Error("a spent ceiling must say which one")
			}
			if !c.hit && reason != "" {
				t.Errorf("nothing was spent, got %q", reason)
			}
			if c.hit && !strings.Contains(reason, c.about) {
				t.Errorf("want the reason to mention %q, got %q", c.about, reason)
			}
		})
	}
}

// TestNoProgressCeilingOpensAGate covers the NoProgress path end to end.
//
// A loop can run few rounds and still be stuck: same stages, no functional
// change. Counting only rounds would let it spin to the round limit before anyone
// noticed.
func TestNoProgressCeilingOpensAGate(t *testing.T) {
	state := atStage(t, KindFeature, "code-review")
	state.Context.Artifacts["ci_green"] = true
	state.Loop.NoProgress = DefaultLoopLimits().NoProgress

	state, err := Reduce(state, ReviewFinding{Aligned: true, Summary: "still nothing"})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if state.Status != StatusAwaitingGate {
		t.Errorf("a spent no-progress ceiling opens a gate, got %q", state.Status)
	}
}

// TestCustomLoopLimitsAreHonoured covers the non-default branch of the limits.
func TestCustomLoopLimitsAreHonoured(t *testing.T) {
	state := atStage(t, KindFeature, "code-review")
	state.Context.Artifacts["ci_green"] = true

	// A single round is enough under a ceiling of zero.
	state, err := Reduce(state, ReviewFinding{
		Aligned: true,
		Summary: "first trip",
		Limits:  LoopLimits{MaxRounds: 0, NoProgress: 9, Oscillation: 9},
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if state.Status != StatusAwaitingGate {
		t.Errorf("a custom ceiling of 0 is spent on the first round, got %q", state.Status)
	}
}

// TestGateApproveKeepsTheReviewedArtifact covers the approve-with-payload branch.
//
// Approving without editing still records what was approved: the handoff has to
// say which version the next stage received, and "the one that was there" is only
// meaningful if it was written down.
func TestGateApproveKeepsTheReviewedArtifact(t *testing.T) {
	state := NewTaskState("LUNA-1", KindFeature)
	state.Status = StatusAwaitingGate
	state.Stage = "spec"
	state.Gate = &PendingGate{
		Kind:     GateReviewArtifact,
		Stage:    "spec",
		Artifact: "contract",
		Payload:  "the generated contract",
	}

	state, err := Reduce(state, GateApprove{})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if state.Evidence["contract"].Detail != "the generated contract" {
		t.Errorf("an approved artifact is recorded as approved, got %+v", state.Evidence["contract"])
	}
}

// TestUnknownStageStopsTheAdvance covers the error path out of NextStage.
func TestUnknownStageStopsTheAdvance(t *testing.T) {
	state := NewTaskState("LUNA-1", KindFeature)
	state.Status = StatusStageDone
	state.Stage = "buld" // a typo, not a stage

	if _, err := Reduce(state, Advance{Flow: DefaultFlow()}); !errors.Is(err, ErrUnknownStage) {
		t.Errorf("want ErrUnknownStage, got %v", err)
	}
}

// TestTaskStateAnswersAboutItself covers the two predicates.
func TestTaskStateAnswersAboutItself(t *testing.T) {
	cases := []struct {
		status     Status
		terminal   bool
		needsHuman bool
	}{
		{StatusReady, false, false},
		{StatusRunning, false, false},
		{StatusAwaitingGate, false, true},
		{StatusBlocked, false, true},
		{StatusDone, true, false},
	}

	for _, c := range cases {
		state := TaskState{Status: c.status}
		if got := state.IsTerminal(); got != c.terminal {
			t.Errorf("%q IsTerminal: want %v, got %v", c.status, c.terminal, got)
		}
		if got := state.NeedsHuman(); got != c.needsHuman {
			t.Errorf("%q NeedsHuman: want %v, got %v", c.status, c.needsHuman, got)
		}
	}
}

// TestStageLookupOnAnAbsentStage covers the fallback in stageIn.
func TestStageLookupOnAnAbsentStage(t *testing.T) {
	stage := stageIn(DefaultFlow(), "nowhere")

	if stage.ID != "nowhere" {
		t.Errorf("the fallback keeps the id it was asked about, got %q", stage.ID)
	}
	if len(stage.Requires) != 0 || len(stage.Produces) != 0 {
		t.Error("a stage that is not in the flow declares nothing")
	}
}
