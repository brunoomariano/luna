package fsm

import "testing"

// reviewing is a task in a review stage, ready to send work back.
func reviewing(t *testing.T) TaskState {
	t.Helper()

	state := atStage(t, KindFeature, "audit")
	state.Context.Artifacts["ci_green"] = true
	return state
}

// TestTwoRoundsDeliveringTheSameThingCountAsNoProgress is PRD node-0002's whole
// point, and the ceiling that was decorative until now.
//
// `Loop.NoProgress` has existed since the loop ceilings were designed and
// nothing ever incremented it — the counter that would catch a task circling
// without converging was the one counting nothing.
func TestTwoRoundsDeliveringTheSameThingCountAsNoProgress(t *testing.T) {
	state := reviewing(t)

	first, err := Reduce(state, ReviewFinding{Aligned: true, Progress: "abc123"})
	if err != nil {
		t.Fatalf("first round: %v", err)
	}
	if first.Loop.NoProgress != 0 {
		t.Errorf("the first round counted as no progress; it has nothing to compare against")
	}

	// Back to the review stage, delivering the same commit.
	second := first
	second.Stage = "audit"
	second.Status = StatusRunning

	after, err := Reduce(second, ReviewFinding{Aligned: true, Progress: "abc123"})
	if err != nil {
		t.Fatalf("second round: %v", err)
	}
	if after.Loop.NoProgress != 1 {
		t.Errorf("NoProgress = %d, want 1 — the same commit twice is the same work twice",
			after.Loop.NoProgress)
	}
}

// TestWorkThatChangedResetsTheStreak. The counter is for *consecutive* rounds
// that produced nothing: one round that moves the work forward means the loop is
// converging, however slowly.
func TestWorkThatChangedResetsTheStreak(t *testing.T) {
	state := reviewing(t)
	state.Loop.NoProgress = 1
	state.Loop.LastProgress = "abc123"

	after, err := Reduce(state, ReviewFinding{Aligned: true, Progress: "def456"})
	if err != nil {
		t.Fatalf("Reduce: %v", err)
	}

	if after.Loop.NoProgress != 0 {
		t.Errorf("NoProgress = %d, want 0 — the work changed", after.Loop.NoProgress)
	}
	if after.Loop.LastProgress != "def456" {
		t.Errorf("LastProgress = %q, want the round's own signal", after.Loop.LastProgress)
	}
}

// TestARoundThatObservedNothingLeavesTheStreakAlone is the error handling the
// PRD asks for: *"a detector that blocks work when it cannot observe is worse
// than one that stays quiet"*.
//
// Both alternatives are wrong in a way that matters. Counting silence would fire
// the ceiling on a node that could not look; clearing the streak would throw away
// a real one because a single round in the middle could not.
func TestARoundThatObservedNothingLeavesTheStreakAlone(t *testing.T) {
	state := reviewing(t)
	state.Loop.NoProgress = 1
	state.Loop.LastProgress = "abc123"

	after, err := Reduce(state, ReviewFinding{Aligned: true})
	if err != nil {
		t.Fatalf("Reduce: %v", err)
	}

	if after.Loop.NoProgress != 1 {
		t.Errorf("NoProgress = %d, want it left at 1", after.Loop.NoProgress)
	}
	// And the next round compares against the last thing actually seen.
	if after.Loop.LastProgress != "abc123" {
		t.Errorf("LastProgress = %q — a round that saw nothing overwrote what the "+
			"last one did see", after.Loop.LastProgress)
	}
}

// TestTheNoProgressCeilingFiresOnRepeatedWork walks the whole path: same commit
// every round until the ceiling the flow declares is spent.
//
// Before this, reaching that ceiling was impossible — which is why the watchdog
// was said not to cover a busy agent achieving nothing, on the grounds that the
// loop ceilings did.
func TestTheNoProgressCeilingFiresOnRepeatedWork(t *testing.T) {
	state := reviewing(t)
	limit := DefaultLoopLimits().NoProgress

	// One more round than the ceiling allows, all delivering the same thing.
	for round := 0; round <= limit; round++ {
		state.Stage = "audit"
		state.Status = StatusRunning

		var err error
		state, err = Reduce(state, ReviewFinding{Aligned: true, Progress: "same"})
		if err != nil {
			t.Fatalf("round %d: %v", round, err)
		}
		if state.Status == StatusAwaitingGate {
			break
		}
	}

	if state.Status != StatusAwaitingGate {
		t.Fatalf("status = %q after %d identical rounds — the ceiling never fired",
			state.Status, state.Loop.NoProgress)
	}
	if state.Gate == nil || state.Gate.Kind != GateLoopCeiling {
		t.Fatalf("gate = %+v, want a loop ceiling", state.Gate)
	}
	// Not converging is a decision to make with the history in view, not a node
	// failure — so a gate, and the reason says which ceiling.
	if state.Gate.Reason == "" {
		t.Error("the gate does not say why it opened")
	}
}

// TestWhatWasComparedIsRecorded is RF3. A person told two rounds made no
// progress wants to see what the machine looked at before believing it — a
// counter alone is an assertion.
func TestWhatWasComparedIsRecorded(t *testing.T) {
	state := reviewing(t)

	after, err := Reduce(state, ReviewFinding{Aligned: true, Progress: "d34db33f"})
	if err != nil {
		t.Fatalf("Reduce: %v", err)
	}

	if after.Loop.LastProgress != "d34db33f" {
		t.Errorf("LastProgress = %q — the state does not record what was compared",
			after.Loop.LastProgress)
	}
}

// TestAFindingOutOfScopeCountsNothing. `Aligned: false` is someone else's task:
// the code did not change, the flow carries on untouched, and a loop that never
// happened has no round to count.
func TestAFindingOutOfScopeCountsNothing(t *testing.T) {
	state := reviewing(t)
	state.Loop.LastProgress = "abc123"

	after, err := Reduce(state, ReviewFinding{Progress: "abc123"})
	if err != nil {
		t.Fatalf("Reduce: %v", err)
	}

	if after.Loop.NoProgress != 0 || after.Loop.Rounds != 0 {
		t.Errorf("an out-of-scope finding moved the loop counters: %+v", after.Loop)
	}
}
