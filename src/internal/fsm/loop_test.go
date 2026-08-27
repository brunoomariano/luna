package fsm

import (
	"strings"
	"testing"
)

// loopFlow is a converging stage with everything the reducer reads: what it
// converges on, what a regression invalidates, and ceilings small enough that a
// test can reach them.
func loopFlow() []Stage {
	return []Stage{{
		ID:       "forge",
		Agent:    "claude",
		Brief:    "You build and judge the round.",
		Requires: []Artifact{TaskID},
		Produces: []Artifact{"code", "ci_green"},
		Verifiers: map[Artifact]Verifier{
			"code":     Existence{},
			"ci_green": Command{Run: "make ci", Scope: ScopeFull},
		},
		Loop: &LoopSpec{
			ConvergesOn: []Artifact{"ci_green"},
			Invalidates: []Artifact{"ci_green"},
			Limits:      LoopLimits{MaxRounds: 2, NoProgress: 2, Oscillation: 2},
		},
	}}
}

// running puts a task inside the converging stage, which is where every round is
// judged from.
func running(t *testing.T) TaskState {
	t.Helper()

	state := NewTaskState("L-1", KindFeature)
	state, err := Reduce(state, Advance{Flow: loopFlow()})
	if err != nil {
		t.Fatalf("entering the loop: %v", err)
	}
	return state
}

// TestAProgressedRoundGoesRoundAgain is the ordinary outcome: the stage stays
// where it is and the counters move.
//
// Staying is what makes this a loop rather than a jump. Nothing is sent anywhere;
// the same stage runs again with one more round behind it.
func TestAProgressedRoundGoesRoundAgain(t *testing.T) {
	state := running(t)

	state, err := Reduce(state, RoundJudged{
		Outcome: OutcomeProgressed, Progress: "c0ffee1", Flow: loopFlow(),
	})
	if err != nil {
		t.Fatalf("judging the round: %v", err)
	}

	if state.Stage != "forge" || state.Status != StatusRunning {
		t.Errorf("want the loop still in its stage, got %q %q", state.Stage, state.Status)
	}
	if state.Loop.Rounds != 1 {
		t.Errorf("the round was not counted, got %d", state.Loop.Rounds)
	}
	if state.Loop.Oscillation != 0 {
		t.Error("progress is not oscillation")
	}
}

// TestARegressedRoundCountsAsOscillationAndUndoesTheGreen covers the outcome that
// separates a productive loop from a thrashing one.
//
// A regression is counted apart from a round, because the two ceilings ask
// different questions: one is "is this going anywhere", the other "is this going
// back and forth". And the green has to go — it attested to code the round is
// undoing.
func TestARegressedRoundCountsAsOscillationAndUndoesTheGreen(t *testing.T) {
	state := running(t)
	state.Context.Artifacts["ci_green"] = true
	state.Evidence["ci_green"] = Evidence{Scope: ScopeFull, Verdict: VerdictPassed}

	state, err := Reduce(state, RoundJudged{
		Outcome: OutcomeRegressed, Progress: "c0ffee2", Flow: loopFlow(),
	})
	if err != nil {
		t.Fatalf("judging the round: %v", err)
	}

	if state.Loop.Oscillation != 1 {
		t.Errorf("a regression counts against oscillation, got %d", state.Loop.Oscillation)
	}
	if state.Context.HasArtifact("ci_green") {
		t.Error("the green attested to code the round undid, and must not survive it")
	}
}

// TestAConvergedRoundLeavesTheLoop is the exit, and the only outcome that closes
// the stage.
func TestAConvergedRoundLeavesTheLoop(t *testing.T) {
	state := running(t)
	state.Evidence["ci_green"] = Evidence{Scope: ScopeFull, Verdict: VerdictPassed}

	state, err := Reduce(state, RoundJudged{Outcome: OutcomeConverged, Flow: loopFlow()})
	if err != nil {
		t.Fatalf("converging: %v", err)
	}

	if state.Status != StatusStageDone {
		t.Errorf("a converged loop closes its stage, got %q", state.Status)
	}
}

// TestAConvergedRoundIsRefusedWithoutTheEvidence is the line between what the
// model decides and what it may not.
//
// The verdict is the model's — no exit code tells "progressed" from "traded one
// failure for another". Leaving the loop is different: it ends the judging, so it
// is the one the engine checks. A loop that could close over a red command would
// be a loop closing on an opinion.
func TestAConvergedRoundIsRefusedWithoutTheEvidence(t *testing.T) {
	for name, evidence := range map[string]Evidence{
		"never proven": {},
		"proven failed": {
			Scope: ScopeFull, Verdict: VerdictFailed, Command: "make ci", ExitCode: 1,
		},
	} {
		t.Run(name, func(t *testing.T) {
			state := running(t)
			if evidence.Verdict != "" {
				state.Evidence["ci_green"] = evidence
			}

			_, err := Reduce(state, RoundJudged{Outcome: OutcomeConverged, Flow: loopFlow()})
			if err == nil {
				t.Fatal("a loop closed on an opinion")
			}
			if !strings.Contains(err.Error(), "ci_green") {
				t.Errorf("the refusal must name what is unproven, got %v", err)
			}
		})
	}
}

// TestAStuckRoundStopsWithoutSpendingTheCeiling covers the outcome that exists so
// the remaining rounds are not paid to rediscover something already known.
func TestAStuckRoundStopsWithoutSpendingTheCeiling(t *testing.T) {
	state := running(t)

	state, err := Reduce(state, RoundJudged{
		Outcome: OutcomeStuck,
		Summary: "the acceptance criteria contradict each other",
		Flow:    loopFlow(),
		Gate:    GateAccount{Decision: GateDecisionWaited},
	})
	if err != nil {
		t.Fatalf("judging the round: %v", err)
	}

	if state.Status != StatusAwaitingGate {
		t.Fatalf("a stuck loop asks a person, got %q", state.Status)
	}
	if state.Gate == nil || !strings.Contains(state.Gate.Reason, "contradict") {
		t.Errorf("the gate must carry why it stopped, got %+v", state.Gate)
	}
	if state.Loop.Rounds != 1 {
		t.Errorf("a stuck round is still a round, got %d", state.Loop.Rounds)
	}
}

// TestTheCeilingStopsTheLoop is INV-5 on the loop: no infinite retry.
//
// Both endings, because the difference is the whole reason a ceiling is not
// simply a failure. With somebody waiting it opens a gate — not converging is a
// decision to make with the history in view. With nobody, it blocks, which is the
// ending that notifies; a ceiling that resolved itself would be the infinite loop
// the invariant names.
func TestTheCeilingStopsTheLoop(t *testing.T) {
	for name, c := range map[string]struct {
		gate GateAccount
		want Status
	}{
		"somebody is waiting": {gate: GateAccount{}, want: StatusAwaitingGate},
		"nobody is":           {gate: GateAccount{Decision: GateDecisionPassed}, want: StatusBlocked},
	} {
		t.Run(name, func(t *testing.T) {
			state := running(t)

			var err error
			for round := 1; round <= 3; round++ {
				state, err = Reduce(state, RoundJudged{
					Outcome: OutcomeProgressed, Progress: "round", Flow: loopFlow(), Gate: c.gate,
				})
				if err != nil {
					t.Fatalf("round %d: %v", round, err)
				}
			}

			if state.Status != c.want {
				t.Fatalf("want %q at the ceiling, got %q", c.want, state.Status)
			}
			if c.want == StatusBlocked && state.Blocked == "" {
				t.Error("the block must say which ceiling was reached")
			}
		})
	}
}

// TestOnlyAConvergingStageJudgesRounds is the guard that keeps the loop's
// machinery out of the stages that do not loop.
func TestOnlyAConvergingStageJudgesRounds(t *testing.T) {
	flat := []Stage{{
		ID: "shipping", Agent: "claude", Brief: "You write the commits.",
		Requires: []Artifact{TaskID}, Produces: []Artifact{"shipped"},
		Verifiers: map[Artifact]Verifier{"shipped": Existence{}},
	}}
	state, err := Reduce(NewTaskState("L-2", KindFeature), Advance{Flow: flat})
	if err != nil {
		t.Fatalf("entering: %v", err)
	}

	if _, err := Reduce(state, RoundJudged{Outcome: OutcomeProgressed, Flow: flat}); err == nil {
		t.Fatal("a stage that does not converge judged a round")
	}
}

// TestAnUnknownOutcomeIsRefused covers the direction a typo fails in. A verdict
// nothing recognises would otherwise fall through to whatever the switch does
// last.
func TestAnUnknownOutcomeIsRefused(t *testing.T) {
	state := running(t)

	_, err := Reduce(state, RoundJudged{Outcome: "nearly", Flow: loopFlow()})
	if err == nil {
		t.Fatal("an outcome nothing knows was accepted")
	}
	if !strings.Contains(err.Error(), "nearly") {
		t.Errorf("the refusal must name what was written, got %v", err)
	}
}
