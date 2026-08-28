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

// TestALoopIsReadFromItsOwnBlock covers the four keys a converging stage
// declares, and the shapes each refuses.
func TestALoopIsReadFromItsOwnBlock(t *testing.T) {
	stage, err := ParseStage(`
id       = "forge"
agent    = "claude"
brief    = "You build and judge."
produces = ["code", "ci_green"]

[loop]
converges_on = ["ci_green"]
invalidates  = ["ci_green", "tests_green"]
max_rounds   = 6
no_progress  = 3
oscillation  = 2

[verify.code]
kind = "existence"

[verify.ci_green]
run   = "make ci"
scope = "full"
`, "forge.toml")
	if err != nil {
		t.Fatalf("ParseStage: %v", err)
	}

	if stage.Loop == nil {
		t.Fatal("a stage declaring a loop did not get one")
	}
	if len(stage.Loop.ConvergesOn) != 1 || stage.Loop.ConvergesOn[0] != "ci_green" {
		t.Errorf("converges_on = %v", stage.Loop.ConvergesOn)
	}
	if len(stage.Loop.Invalidates) != 2 {
		t.Errorf("invalidates = %v", stage.Loop.Invalidates)
	}
	want := LoopLimits{MaxRounds: 6, NoProgress: 3, Oscillation: 2}
	if stage.Loop.Limits != want {
		t.Errorf("limits = %+v, want %+v", stage.Loop.Limits, want)
	}
}

// TestALoopThatConvergesOnNothingIsNotALoop. A block declaring only ceilings is
// one whose exit nothing proves, and reading it as a loop would let the model's
// verdict close it unchecked.
func TestALoopThatConvergesOnNothingIsNotALoop(t *testing.T) {
	stage, err := ParseStage("id = \"forge\"\n\n[loop]\nmax_rounds = 4\n", "forge.toml")
	if err != nil {
		t.Fatalf("ParseStage: %v", err)
	}
	if stage.Loop != nil {
		t.Error("a block with no convergence was read as a loop")
	}
}

// TestALoopCeilingRefusesWhatCannotBoundAnything.
//
// Zero is either a loop that stops before its first round or one that never
// stops, depending on which comparison reads it, and neither is what somebody
// typing it meant.
func TestALoopCeilingRefusesWhatCannotBoundAnything(t *testing.T) {
	for _, written := range []string{"0", "-1", "many"} {
		_, err := ParseStage("id = \"forge\"\n\n[loop]\nconverges_on = [\"ci_green\"]\n"+
			"max_rounds = "+written+"\n", "forge.toml")
		if err == nil {
			t.Errorf("`max_rounds = %s` was accepted", written)
		}
	}
}

// TestAnUnknownLoopKeyIsRefused, for the reason every other unknown key is: a
// misspelled ceiling would leave the loop on its default and nothing would say so.
func TestAnUnknownLoopKeyIsRefused(t *testing.T) {
	_, err := ParseStage("id = \"forge\"\n\n[loop]\nmax_round = 4\n", "forge.toml")

	if err == nil {
		t.Fatal("a misspelled loop key was accepted")
	}
	if !strings.Contains(err.Error(), "max_round") {
		t.Errorf("the refusal does not name what was written: %v", err)
	}
}

// TestARedRoundIsARoundAndNotAShortfall is what lets the loop work at all.
//
// A converging stage ends its round without what it converges on — that is the
// ordinary case, and it is why there is a loop. Treating it as a delivery that
// fell short would spend the retry budget on it and then block the task at the
// first red pipeline, which is the failure the loop exists to work through.
func TestARedRoundIsARoundAndNotAShortfall(t *testing.T) {
	state := running(t)

	// Everything but the artifact it converges on.
	state, err := Reduce(state, Complete{
		Delivered: []Artifact{"code"},
		Evidence:  map[Artifact]Evidence{"code": Exists(0)},
		Flow:      loopFlow(),
	})
	if err != nil {
		t.Fatalf("closing a red round: %v", err)
	}

	if state.Status != StatusStageDone {
		t.Errorf("a red round did not close its stage, got %q", state.Status)
	}
	if len(state.StillOwed) != 1 || state.StillOwed[0] != "ci_green" {
		t.Errorf("the round does not say what it is still working towards: %v", state.StillOwed)
	}
	if state.Retry.Attempts != 0 {
		t.Errorf("a round spent the retry budget (%d attempts)", state.Retry.Attempts)
	}
}

// TestMissingAnythingElseIsStillAShortfall is the other side, and the reason the
// exemption names artifacts rather than applying to the whole stage: the loop is
// not a way out of the contract.
func TestMissingAnythingElseIsStillAShortfall(t *testing.T) {
	flow := loopFlow()
	flow[0].Produces = append(flow[0].Produces, "commit_plan")
	flow[0].Verifiers["commit_plan"] = Existence{}

	state := NewTaskState("L-3", KindFeature)
	state, err := Reduce(state, Advance{Flow: flow})
	if err != nil {
		t.Fatalf("entering: %v", err)
	}

	// The convergence artifact *and* something the loop has no claim on.
	state, err = Reduce(state, Complete{
		Delivered: []Artifact{"code"},
		Evidence:  map[Artifact]Evidence{"code": Exists(0)},
		Flow:      flow,
	})
	if err != nil {
		t.Fatalf("closing: %v", err)
	}

	if state.Status == StatusStageDone {
		t.Error("a stage that did not deliver what it owed closed as a round")
	}
}
