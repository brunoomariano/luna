package lead

import (
	"context"
	"strings"
	"testing"

	"github.com/brunoomariano/luna/src/internal/fsm"
)

// triaging is a flow whose first stage may conclude one thing and whose second
// only runs when it did.
func triaging() []fsm.Stage {
	return []fsm.Stage{
		{
			ID: "intake", Agent: "claude", Brief: "You read and classify.",
			Requires: []fsm.Artifact{fsm.TaskID}, Produces: []fsm.Artifact{"briefing"},
			Discovers: []fsm.Fact{fsm.TriagedBug},
			Verifiers: map[fsm.Artifact]fsm.Verifier{"briefing": fsm.Existence{}},
		},
		{
			ID: "diagnose", Agent: "claude", Brief: "You find the cause.",
			When:     fsm.TriagedAsBug,
			Requires: []fsm.Artifact{"briefing"}, Produces: []fsm.Artifact{"root_cause"},
			Verifiers: map[fsm.Artifact]fsm.Verifier{"root_cause": fsm.Existence{}},
		},
	}
}

// TestAStagesConclusionTurnsOnTheStageItGates is the whole of the triage, end to
// end: a report says one word, Luna records the fact, and the conditional stage
// enters.
//
// Before this, the fact existed and nothing could write it — so the stage it
// gates could never run, whatever the intake concluded.
func TestAStagesConclusionTurnsOnTheStageItGates(t *testing.T) {
	s := newStore(t)
	nightlyUnder(t, s, "T-1", fsm.KindFeature, triaging())

	l := &Lead{
		Store: s, Flow: triaging(), Judge: &alwaysBlocks{},
		Node: reportingNode{report: "something returns the wrong value\n\nFACT: triaged_bug"},
	}
	if _, err := l.Run(context.Background(), "T-1"); err != nil {
		t.Fatalf("Run: %v", err)
	}

	state, err := s.Replay("T-1", triaging())
	if err != nil {
		t.Fatalf("replaying: %v", err)
	}
	if !state.Context.HasFact(fsm.TriagedBug) {
		t.Fatal("the conclusion in the report was not recorded")
	}
	if !stageIn(triaging(), "diagnose").AppliesTo(state.Context) {
		t.Error("the stage the conclusion gates still does not apply")
	}
}

// TestAReportThatConcludesNothingLeavesTheShorterPath. Something merely missing
// is the ordinary case, and there is no fact for it — inventing one would pay for
// an investigation with nothing to investigate.
func TestAReportThatConcludesNothingLeavesTheShorterPath(t *testing.T) {
	s := newStore(t)
	nightlyUnder(t, s, "T-2", fsm.KindFeature, triaging())

	l := &Lead{
		Store: s, Flow: triaging(), Judge: &alwaysBlocks{},
		Node: reportingNode{report: "the behaviour is not there yet; nothing is broken"},
	}
	if _, err := l.Run(context.Background(), "T-2"); err != nil {
		t.Fatalf("Run: %v", err)
	}

	state, err := s.Replay("T-2", triaging())
	if err != nil {
		t.Fatalf("replaying: %v", err)
	}
	if state.Context.HasFact(fsm.TriagedBug) {
		t.Error("a report concluding nothing recorded a conclusion")
	}
}

// converging is a stage that loops, with a ceiling a test can reach.
func converging() []fsm.Stage {
	return []fsm.Stage{{
		ID: "forge", Agent: "claude", Brief: "You build and judge the round.",
		Requires: []fsm.Artifact{fsm.TaskID}, Produces: []fsm.Artifact{"code"},
		Verifiers: map[fsm.Artifact]fsm.Verifier{"code": fsm.Existence{}},
		Loop: &fsm.LoopSpec{
			ConvergesOn: []fsm.Artifact{"code"},
			Limits:      fsm.LoopLimits{MaxRounds: 2, NoProgress: 2, Oscillation: 2},
		},
	}}
}

// TestARoundsVerdictMovesTheLoop is the other half: the model says what the round
// produced, and the engine turns the word into a transition.
//
// Progressed keeps the stage where it is, which is what makes it a loop — nothing
// is sent anywhere, the same stage runs again with the counters further along.
func TestARoundsVerdictMovesTheLoop(t *testing.T) {
	s := newStore(t)
	nightlyUnder(t, s, "T-3", fsm.KindFeature, converging())

	l := &Lead{
		Store: s, Flow: converging(), Judge: &alwaysBlocks{},
		Node: reportingNode{report: "the suite is closer\n\nVERDICT: progressed"},
	}
	if _, err := l.Run(context.Background(), "T-3"); err != nil {
		t.Fatalf("Run: %v", err)
	}

	state, err := s.Replay("T-3", converging())
	if err != nil {
		t.Fatalf("replaying: %v", err)
	}
	if state.Loop.Rounds == 0 {
		t.Error("a judged round was not counted")
	}
}

// TestAReportWithNoVerdictLeavesTheStageClosed. A loop that went round again on
// an unreadable report would spend its ceiling on something nobody decided.
func TestAReportWithNoVerdictLeavesTheStageClosed(t *testing.T) {
	s := newStore(t)
	nightlyUnder(t, s, "T-4", fsm.KindFeature, converging())

	l := &Lead{
		Store: s, Flow: converging(), Judge: &alwaysBlocks{},
		Node: reportingNode{report: "I believe this is all working now"},
	}
	if _, err := l.Run(context.Background(), "T-4"); err != nil {
		t.Fatalf("Run: %v", err)
	}

	state, err := s.Replay("T-4", converging())
	if err != nil {
		t.Fatalf("replaying: %v", err)
	}
	if state.Loop.Rounds != 0 {
		t.Errorf("a report with no verdict moved the loop (%d rounds)", state.Loop.Rounds)
	}
}

// TestAnUnreadableVerdictIsNotAConclusion. `converged` is the one outcome that
// ends the judging, so a word nothing recognises must not be read as the nearest
// one — a typo would decide where the flow goes.
func TestAnUnreadableVerdictIsNotAConclusion(t *testing.T) {
	if got := fsm.ReadVerdict("VERDICT: converge"); got != "" {
		t.Errorf("a near miss was read as %q", got)
	}
	if !strings.Contains(string(fsm.OutcomeConverged), "converged") {
		t.Error("the fixture no longer matches the outcome it is a near miss of")
	}
}
