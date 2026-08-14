package fsm

import "testing"

// TestARecordedDecisionOverridesTheShippedPolicy is the point of ADR-0026.
//
// The profile in the state says one thing and the recorded decision says another.
// The recorded one wins, because it is what happened — and that is exactly what a
// task replays as after someone edits the profile it ran under.
func TestARecordedDecisionOverridesTheShippedPolicy(t *testing.T) {
	// interactive stops at every gate, and discovery opens one.
	state := start(t, ProfileInteractive)

	state, err := Reduce(state, Advance{Flow: gatedFlow(), GateDecision: GateDecisionPassed})
	if err != nil {
		t.Fatalf("advancing: %v", err)
	}

	if state.Status != StatusRunning {
		t.Errorf("the recorded decision said the gate passed, got %q", state.Status)
	}
	if state.Gate != nil {
		t.Errorf("no gate should be pending when the log says it passed, got %+v", state.Gate)
	}
}

// TestARecordedWaitHoldsAgainstAPermissiveProfile is the same guarantee in the
// other direction.
//
// Loosening a profile must not erase a pause that happened. A nightly task whose
// log says a gate waited replays as having waited, because someone was asked.
func TestARecordedWaitHoldsAgainstAPermissiveProfile(t *testing.T) {
	state := start(t, ProfileNightly)

	state, err := Reduce(state, Advance{Flow: gatedFlow(), GateDecision: GateDecisionWaited})
	if err != nil {
		t.Fatalf("advancing: %v", err)
	}

	if state.Status != StatusAwaitingGate {
		t.Errorf("the recorded decision said the gate waited, got %q", state.Status)
	}
	if state.Gate == nil {
		t.Fatal("a gate that waited must be pending")
	}
	if state.Gate.Stage != "gated" {
		t.Errorf("want the gated stage, got %q", state.Gate.Stage)
	}
}

// TestAnEventWithNoDecisionFallsBackToTheShippedPolicy covers the old log.
//
// Every Advance written before this field existed carries nothing. Those tasks
// have to keep replaying, and the shipped policy is the only reading of them
// available — it is also the one that produced them.
func TestAnEventWithNoDecisionFallsBackToTheShippedPolicy(t *testing.T) {
	cases := []struct {
		profile Profile
		want    Status
	}{
		{ProfileInteractive, StatusAwaitingGate},
		{ProfileNightly, StatusRunning},
	}

	for _, c := range cases {
		state, err := Reduce(start(t, c.profile), Advance{Flow: gatedFlow()})
		if err != nil {
			t.Fatalf("%s: advancing: %v", c.profile, err)
		}
		if state.Status != c.want {
			t.Errorf("%s with no recorded decision: want %q, got %q", c.profile, c.want, state.Status)
		}
	}
}

// TestASpentCeilingStopsTheTaskWhicheverWayItIsAnswered covers the second gate
// the profile decides, which is not reached through an Advance.
//
// This asserted the opposite until 2026-08-13: a recorded `passed` let the
// ceiling through and the loop carried on. Wiring the ReviewFinding emitter made
// that reachable for the first time, and it ran forever — 8 rounds against a
// ceiling of 4, with the counters climbing and nothing firing. INV-core-8 names
// that case in as many words, so the ceiling now stops the task whichever way
// the gate was answered; only *how* it stops depends on the profile (ADR-0059).
func TestASpentCeilingStopsTheTaskWhicheverWayItIsAnswered(t *testing.T) {
	// A loop already at its ceiling, on a profile that stops at everything.
	spent := TaskState{
		Status:   StatusRunning,
		Stage:    "qa",
		Profile:  ProfileInteractive,
		Loop:     LoopCounters{Rounds: 9},
		Context:  NewTaskContext(KindFeature),
		Evidence: map[Artifact]Evidence{},
	}

	// Nobody is waiting: it blocks, which is the ending that notifies.
	passed, err := Reduce(spent, ReviewFinding{Aligned: true, GateDecision: GateDecisionPassed})
	if err != nil {
		t.Fatalf("reviewing: %v", err)
	}
	if passed.Status != StatusBlocked {
		t.Errorf("a ceiling nobody answers must not resolve, got %q", passed.Status)
	}
	if passed.Blocked == "" {
		t.Error("the block does not say why — a task that halts without a reason is " +
			"the silent failure INV-core-8 forbids")
	}

	// The same state with no decision recorded falls back, and interactive waits.
	fellBack, err := Reduce(spent, ReviewFinding{Aligned: true})
	if err != nil {
		t.Fatalf("reviewing: %v", err)
	}
	if fellBack.Status != StatusAwaitingGate {
		t.Errorf("with no decision recorded the shipped policy waits, got %q", fellBack.Status)
	}
}

// TestAnUnattendedRunStopsAtTheCeiling is the same rule through the profile that
// makes it matter. `nightly` stops at nothing, and "nothing" cannot include the
// ceiling that exists to stop a loop burning tokens.
func TestAnUnattendedRunStopsAtTheCeiling(t *testing.T) {
	spent := TaskState{
		Status:   StatusRunning,
		Stage:    "qa",
		Profile:  ProfileNightly,
		Loop:     LoopCounters{Rounds: 9},
		Context:  NewTaskContext(KindFeature),
		Evidence: map[Artifact]Evidence{},
	}

	after, err := Reduce(spent, ReviewFinding{Aligned: true})
	if err != nil {
		t.Fatalf("reviewing: %v", err)
	}
	if after.Status != StatusBlocked {
		t.Errorf("status = %q — an unattended run that stops converging has to stop", after.Status)
	}
	if after.Gate != nil {
		t.Error("a blocked task is not waiting at a gate; carrying one would make " +
			"`luna gates` list something nobody can answer")
	}
}

// TestGateAheadNamesTheGateAnAdvanceWouldHit covers the lookahead the lead uses
// to decide before recording.
//
// It has to answer without moving the task: asking what is coming must not be the
// same as going there.
func TestGateAheadNamesTheGateAnAdvanceWouldHit(t *testing.T) {
	state := start(t, ProfileInteractive)

	gate := GateAhead(state, gatedFlow())
	if gate == nil {
		t.Fatal("the stage ahead opens a gate; want it named")
	}
	if gate.Kind != GateConfirm || gate.Stage != "gated" {
		t.Errorf("want the confirm, got %+v", gate)
	}

	if state.Stage != "" || state.Status != StatusReady {
		t.Errorf("looking ahead must not move the task, got stage=%q status=%q", state.Stage, state.Status)
	}
}

// TestGateAheadIsSilentWhenNothingIsComing covers the cases with no decision to
// make: a task that cannot advance, and a stage that opens no gate.
func TestGateAheadIsSilentWhenNothingIsComing(t *testing.T) {
	blocked := TaskState{Status: StatusBlocked, Profile: ProfileInteractive, Context: NewTaskContext(KindFeature)}
	if gate := GateAhead(blocked, DefaultFlow()); gate != nil {
		t.Errorf("a blocked task advances nowhere, got %+v", gate)
	}

	// discovery opens a gate; what follows it does not.
	state, err := Reduce(start(t, ProfileNightly), Advance{Flow: DefaultFlow()})
	if err != nil {
		t.Fatalf("advancing: %v", err)
	}
	state.Status = StatusStageDone
	state.Context.Artifacts["repo_map"] = true

	if gate := GateAhead(state, DefaultFlow()); gate != nil && gate.Stage == "discovery" {
		t.Errorf("discovery is behind us, got %+v", gate)
	}
}

// TestWaitsReportsWhetherAnythingWasRecorded covers the tri-state itself.
//
// The distinction a plain bool would lose: "the profile said carry on" and "no
// decision was recorded" produce the same false, and only one of them may skip
// the fallback.
func TestWaitsReportsWhetherAnythingWasRecorded(t *testing.T) {
	cases := []struct {
		decision GateWaited
		waited   bool
		recorded bool
	}{
		{GateDecisionWaited, true, true},
		{GateDecisionPassed, false, true},
		{GateDecisionAbsent, false, false},
	}

	for _, c := range cases {
		waited, recorded := c.decision.Waits()
		if waited != c.waited || recorded != c.recorded {
			t.Errorf("%q: want waited=%v recorded=%v, got %v and %v",
				c.decision, c.waited, c.recorded, waited, recorded)
		}
	}
}

// TestGateAheadIsSilentWhenTheFlowCannotAnswer covers the error path.
//
// A flow whose next stage cannot be resolved has no gate to ask about. Returning
// nil rather than guessing means the advance that follows reports the real
// problem, instead of a decision recorded against a stage that does not exist.
func TestGateAheadIsSilentWhenTheFlowCannotAnswer(t *testing.T) {
	state := start(t, ProfileInteractive)

	// An empty flow: there is no next stage at all.
	if gate := GateAhead(state, nil); gate != nil {
		t.Errorf("an empty flow opens no gate, got %+v", gate)
	}

	// A task already at the end of its flow has nothing ahead either.
	done := TaskState{Status: StatusDone, Profile: ProfileInteractive, Context: NewTaskContext(KindFeature)}
	if gate := GateAhead(done, DefaultFlow()); gate != nil {
		t.Errorf("a finished task advances nowhere, got %+v", gate)
	}
}

// start opens a task on a profile, ready to advance into discovery.
func start(t *testing.T, profile Profile) TaskState {
	t.Helper()

	state, err := Reduce(NewTaskState("LUNA-1", ""), TaskCreated{Kind: KindFeature, Profile: profile})
	if err != nil {
		t.Fatalf("creating: %v", err)
	}
	return state
}

// TestAReviewFindingMakesTheGreenStale covers the audit half of ADR-0020.
//
// The rollback already removes ci_green from the context so nothing downstream
// consumes it. The evidence is a different question: dropping it would leave an
// audit unable to tell "never checked" from "checked, then invalidated by a
// finding", and the second is the one worth seeing (ADR-0032).
func TestAReviewFindingMakesTheGreenStale(t *testing.T) {
	state := TaskState{
		Status:  StatusRunning,
		Stage:   "qa",
		Profile: ProfileNightly,
		Context: NewTaskContext(KindFeature),
		Evidence: map[Artifact]Evidence{
			"ci_green": {Scope: ScopeFull, Verdict: VerdictPassed, Command: "make ci", RecordedAt: 1},
			"contract": Exists(1),
		},
	}
	state.Context.Artifacts["ci_green"] = true

	after, err := Reduce(state, ReviewFinding{Aligned: true})
	if err != nil {
		t.Fatalf("reviewing: %v", err)
	}

	green := after.Evidence["ci_green"]
	if green.Verdict != VerdictStale {
		t.Errorf("a finding invalidates the green, got %q", green.Verdict)
	}
	if green.Command != "make ci" {
		t.Error("the record stays: the audit needs to see what was invalidated")
	}
	if green.Passing() {
		t.Error("stale is not passing")
	}

	// Prose is not invalidated by a code change: the contract still exists, and
	// blocking a stage for rewriting its own briefing would be nonsense.
	if after.Evidence["contract"].Verdict != VerdictPassed {
		t.Error("existence evidence survives a rollback")
	}
}

// gatedFlow is a one-stage flow whose stage opens a confirm.
//
// These tests are about what a recorded decision does, not about the shipped
// flow's shape — which changed when ADR-0062 removed `commit` and `discovery`
// went with it, leaving the mechanical `setup` first and gateless.
func gatedFlow() []Stage {
	return []Stage{{
		ID: "gated", Role: "someone", Requires: []Artifact{TaskID},
		Produces: []Artifact{"thing"},
		Gate:     &GateSpec{Kind: GateConfirm, Reason: "confirm it"},
	}}
}
