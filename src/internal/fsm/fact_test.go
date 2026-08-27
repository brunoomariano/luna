package fsm

import (
	"strings"
	"testing"
)

// factFlow is a stage that concludes something and a stage gated on it.
func factFlow() []Stage {
	return []Stage{
		{
			ID: "intake", Agent: "claude", Brief: "You read and classify.",
			Requires: []Artifact{TaskID}, Produces: []Artifact{"briefing"},
			Verifiers: map[Artifact]Verifier{"briefing": Existence{}},
		},
		{
			ID: "diagnose", Agent: "claude", Brief: "You find the cause.",
			When:     TriagedAsBug,
			Requires: []Artifact{"briefing"}, Produces: []Artifact{"root_cause"},
			Verifiers: map[Artifact]Verifier{"root_cause": Existence{}},
		},
	}
}

// TestADiscoveredFactEntersTheContext is the mechanism that had no way to fire.
//
// `TaskContext.Facts` was read by a condition and written by nothing, so the one
// stage gated on a fact could never enter. This is the write that closes it.
func TestADiscoveredFactEntersTheContext(t *testing.T) {
	state, err := Reduce(NewTaskState("F-1", KindFeature), Advance{Flow: factFlow()})
	if err != nil {
		t.Fatalf("entering: %v", err)
	}

	state, err = Reduce(state, FactDiscovered{
		Fact: TriagedBug, Why: "the averaging returns the sum; tally.sh:14",
	})
	if err != nil {
		t.Fatalf("recording the fact: %v", err)
	}

	if !state.Context.HasFact(TriagedBug) {
		t.Fatal("the fact did not reach the context")
	}
	// And the stage gated on it now enters, which is the whole point of recording
	// it rather than acting on it here.
	if !stageIn(factFlow(), "diagnose").AppliesTo(state.Context) {
		t.Error("the stage the fact gates still does not apply")
	}
}

// TestAFactRecordedByOneStateDoesNotLeakIntoAnother covers the copy.
//
// The context's map is shared with the state the caller is still holding, so a
// reducer writing through it would change the past for everybody looking at it —
// which is the one thing an append-only log is for (INV-2).
func TestAFactRecordedByOneStateDoesNotLeakIntoAnother(t *testing.T) {
	before, err := Reduce(NewTaskState("F-2", KindFeature), Advance{Flow: factFlow()})
	if err != nil {
		t.Fatalf("entering: %v", err)
	}

	if _, err := Reduce(before, FactDiscovered{Fact: TriagedBug}); err != nil {
		t.Fatalf("recording: %v", err)
	}

	if before.Context.HasFact(TriagedBug) {
		t.Error("the earlier state learned a fact recorded after it")
	}
}

// TestAnUnknownFactIsRefused covers the direction a typo fails in.
//
// It fails permissively: the condition reading a misspelled fact is simply never
// true, so the stage it gates never runs and nothing says why. That is the same
// shape as a misspelled capability, and it is refused for the same reason.
func TestAnUnknownFactIsRefused(t *testing.T) {
	state, err := Reduce(NewTaskState("F-3", KindFeature), Advance{Flow: factFlow()})
	if err != nil {
		t.Fatalf("entering: %v", err)
	}

	_, err = Reduce(state, FactDiscovered{Fact: "triaged_buggg"})
	if err == nil {
		t.Fatal("a fact nothing knows was recorded")
	}
	if !strings.Contains(err.Error(), "triaged_buggg") {
		t.Errorf("the refusal must name what was written, got %v", err)
	}
}

// TestAFactNeedsAStageToHaveConcludedIt. A fact is something a stage found, so
// one arriving with nothing running has no author — and the log would record a
// conclusion nobody reached.
func TestAFactNeedsAStageToHaveConcludedIt(t *testing.T) {
	if _, err := Reduce(NewTaskState("F-4", KindFeature), FactDiscovered{Fact: TriagedBug}); err == nil {
		t.Fatal("a fact was recorded with no stage running")
	}
}

// TestEveryKnownFactParses keeps the closed set and its parser in step: a fact
// added to one and not the other is refused at the moment it is first used.
func TestEveryKnownFactParses(t *testing.T) {
	for _, fact := range KnownFacts() {
		if _, err := ParseFact(string(fact)); err != nil {
			t.Errorf("%s is a known fact and does not parse: %v", fact, err)
		}
	}
}
