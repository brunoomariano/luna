package fsm

import (
	"errors"
	"strings"
	"testing"
)

// TestARecordedDecisionOverridesTheShippedPolicy is the point of recording the
// gate decision rather than the policy that produced it.
//
// The profile in the state says one thing and the recorded decision says another.
// The recorded one wins, because it is what happened — and that is exactly what a
// task replays as after someone edits the profile it ran under.
func TestARecordedDecisionOverridesTheShippedPolicy(t *testing.T) {
	// interactive stops at every gate, and discovery opens one.
	state := start(t, ProfileInteractive)

	state, err := Reduce(state, Advance{Flow: gatedFlow(), Gate: GateAccount{Decision: GateDecisionPassed}})
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

	state, err := Reduce(state, Advance{Flow: gatedFlow(), Gate: GateAccount{Decision: GateDecisionWaited}})
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

// TestAnEventWithNoDecisionWaitsWhateverTheProfile covers what an unrecorded
// decision means, and that the profile no longer changes it.
//
// It used to fall back to the profile's own policy, so the same event replayed
// two ways depending on a name — which recording the decision rules out: a policy read
// at replay time means editing a profile rewrites how past tasks read. What
// replaced it is the conservative reading, the same one `KnobAsk` takes: a gate
// nobody recorded an answer for is a gate to ask about.
func TestAnEventWithNoDecisionWaitsWhateverTheProfile(t *testing.T) {
	for _, profile := range []Profile{ProfileInteractive, ProfileNightly} {
		state, err := Reduce(start(t, profile), Advance{Flow: gatedFlow()})
		if err != nil {
			t.Fatalf("%s: advancing: %v", profile, err)
		}
		if state.Status != StatusAwaitingGate {
			t.Errorf("%s with no recorded decision: want %q, got %q",
				profile, StatusAwaitingGate, state.Status)
		}
	}
}

// TestASpentCeilingStopsTheTaskWhicheverWayItIsAnswered covers the second gate
// the profile decides, which is not reached through an Advance.
//
// This asserted the opposite until 2026-08-13: a recorded `passed` let the
// ceiling through and the loop carried on. Wiring the ReviewFinding emitter made
// that reachable for the first time, and it ran forever — 8 rounds against a
// ceiling of 4, with the counters climbing and nothing firing. INV-5 names
// that case in as many words, so the ceiling now stops the task whichever way
// the gate was answered; only *how* it stops depends on the profile.
func TestASpentCeilingStopsTheTaskWhicheverWayItIsAnswered(t *testing.T) {
	// A loop already at its ceiling, on a profile that stops at everything.
	spent := TaskState{
		Status:   StatusRunning,
		Stage:    "audit",
		Profile:  ProfileInteractive,
		Loop:     LoopCounters{Rounds: 9},
		Context:  NewTaskContext(KindFeature),
		Evidence: map[Artifact]Evidence{},
	}

	// Nobody is waiting: it blocks, which is the ending that notifies.
	passed, err := Reduce(spent, ReviewFinding{Aligned: true, Gate: GateAccount{Decision: GateDecisionPassed}})
	if err != nil {
		t.Fatalf("reviewing: %v", err)
	}
	if passed.Status != StatusBlocked {
		t.Errorf("a ceiling nobody answers must not resolve, got %q", passed.Status)
	}
	if passed.Blocked == "" {
		t.Error("the block does not say why — a task that halts without a reason is " +
			"the silent failure INV-5 forbids")
	}

	// The same state with no decision recorded waits, because an unrecorded
	// decision is a gate nobody answered rather than one nobody needs.
	unrecorded, err := Reduce(spent, ReviewFinding{Aligned: true})
	if err != nil {
		t.Fatalf("reviewing: %v", err)
	}
	if unrecorded.Status != StatusAwaitingGate {
		t.Errorf("an unrecorded decision asks rather than resolving, got %q", unrecorded.Status)
	}
}

// TestAnUnattendedRunStopsAtTheCeiling is the rule that matters most about a
// spent ceiling: a run with nobody watching cannot answer a gate, so the ceiling
// that exists to stop a loop burning tokens has to end the task rather than
// suspend it.
//
// The decision arrives in the action rather than being read off the profile — a
// run nobody is supervising is one whose lead let the gate through.
func TestAnUnattendedRunStopsAtTheCeiling(t *testing.T) {
	spent := TaskState{
		Status:   StatusRunning,
		Stage:    "audit",
		Profile:  ProfileNightly,
		Loop:     LoopCounters{Rounds: 9},
		Context:  NewTaskContext(KindFeature),
		Evidence: map[Artifact]Evidence{},
	}

	after, err := Reduce(spent, ReviewFinding{Aligned: true, Gate: GateAccount{Decision: GateDecisionPassed}})
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
		// Both new answers are recorded decisions that did not stop the task. The
		// value that matters here is `recorded`: a gate the checks answered must
		// not fall back to the profile on replay, which is what a missing case
		// would silently do.
		{GateDecisionChecked, false, true},
		{GateDecisionJudged, false, true},
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

// TestAReviewFindingMakesTheGreenStale covers the audit half of an aligned
// finding invalidating the green.
//
// The rollback already removes ci_green from the context so nothing downstream
// consumes it. The evidence is a different question: dropping it would leave an
// audit unable to tell "never checked" from "checked, then invalidated by a
// finding", and the second is the one worth seeing.
func TestAReviewFindingMakesTheGreenStale(t *testing.T) {
	state := TaskState{
		Status:  StatusRunning,
		Stage:   "audit",
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
// flow's shape — which changed when integration left Luna's scope, removing
// `commit`, and `discovery` went with it, leaving the mechanical `setup` first
// and gateless.
func gatedFlow() []Stage {
	return []Stage{{
		ID: "gated", Requires: []Artifact{TaskID},
		Produces: []Artifact{"thing"},
		Gate:     &GateSpec{Kind: GateConfirm, Reason: "confirm it"},
	}}
}

// TestAJudgedGateIsNeverRecordedAsHuman is the falsification this design must
// not commit.
//
// Recording the lead's judgement as ScopeHuman would make the audit say a person
// looked when none did. The scopes are separate values and `judged` must not
// satisfy a requirement for `human` — an inverted implementation that aliased
// them would pass every other test in this file.
func TestAJudgedGateIsNeverRecordedAsHuman(t *testing.T) {
	if ScopeJudged == ScopeHuman {
		t.Fatal("the lead's judgement and a person's are the same scope")
	}

	if ScopeJudged.Satisfies(ScopeHuman) {
		t.Error("a lead's judgement stood in for a person's")
	}
}

// TestTheAccountTravelsWithTheDecisionThatOpensTheGate is what replaced a
// separate `GateJudged` action.
//
// That action could never land. `judge()` runs while *computing* the decision
// that opens the gate, so at that moment there is no gate for the reducer to
// attach an account to, and it refused every one. Measured at knob 9: the lead
// judged, concluded `cannot-decide`, and left nothing behind but a warning on a
// stream an unattended run has nobody to read.
func TestTheAccountTravelsWithTheDecisionThatOpensTheGate(t *testing.T) {
	state := atStage(t, KindFeature, "plan")
	stage := stageIn(DefaultFlow(), "plan")

	owed := append(append([]Artifact{}, stage.Produces...), stage.ProducesForHuman...)
	state, err := Reduce(state, Complete{
		Delivered: owed,
		Evidence:  passing(stage, owed),
		Gate: GateAccount{
			Decision:  GateDecisionWaited,
			Judgement: "cannot-decide",
			Excerpt:   "criterion 3 turns on the arithmetic, which I cannot settle from the document",
		},
	})
	if err != nil {
		t.Fatalf("closing plan: %v", err)
	}

	if state.Status != StatusAwaitingGate || state.Gate == nil {
		t.Fatalf("a waited decision did not leave the gate open: %q", state.Status)
	}
	if state.Gate.Judged != "cannot-decide" {
		t.Errorf("the verdict did not reach the gate: %q", state.Gate.Judged)
	}
	if state.Gate.Reasoning == "" {
		t.Error("the gate carries a verdict and no account of it")
	}
}

// TestAGateNobodyJudgedCarriesNoAccount. An empty verdict beside an open gate has
// to mean "nobody was asked" and not "asked, said nothing" — a person answering
// it reads the difference.
func TestAGateNobodyJudgedCarriesNoAccount(t *testing.T) {
	state := atStage(t, KindFeature, "plan")
	stage := stageIn(DefaultFlow(), "plan")

	owed := append(append([]Artifact{}, stage.Produces...), stage.ProducesForHuman...)
	state, err := Reduce(state, Complete{Delivered: owed, Evidence: passing(stage, owed)})
	if err != nil {
		t.Fatalf("closing plan: %v", err)
	}

	if state.Gate == nil {
		t.Fatal("the gate did not open")
	}
	if state.Gate.Judged != "" || state.Gate.Reasoning != "" {
		t.Errorf("a gate nobody judged carries an account: %q / %q",
			state.Gate.Judged, state.Gate.Reasoning)
	}
}

// TestAJudgementIsRecordedAgainstTheGateItIsAbout. It used to be possible to file
// reasoning against nothing, which the reducer refused — and that refusal was the
// bug: the only caller filed it before the gate existed, so every account was
// refused and none was ever kept. Carrying it inside the action removes the
// question: there is exactly one gate the action can be about, and the reducer
// attaches the account to that one as it opens it.
func TestAJudgementIsRecordedAgainstTheGateItIsAbout(t *testing.T) {
	state := start(t, ProfileNightly)

	state, err := Reduce(state, Advance{
		Flow: gatedFlow(),
		Gate: GateAccount{
			Decision:  GateDecisionWaited,
			Judgement: "reject",
			Excerpt:   "obligation 7 wants six cases and the contract permits five",
		},
	})
	if err != nil {
		t.Fatal(err)
	}

	if state.Gate == nil {
		t.Fatal("the flow under test opens no gate")
	}
	if state.Gate.Judged != "reject" {
		t.Errorf("the verdict is filed against the wrong thing: %q", state.Gate.Judged)
	}
	if state.Gate.Reasoning == "" {
		t.Error("the gate carries a verdict with no account of it")
	}
}

// TestAJudgedGateRecordsWhatTheLeadConcluded covers the second of the two
// positions a gate can be judged from — the one where the gate is already open.
//
// `luna lead` has the agent close its own stage through `luna done`, so the gate
// reaches this point with nothing decided and there is no action left to carry an
// account. Filing it here changes nothing else: the gate stays exactly where it
// was, with the reasoning beside it.
func TestAJudgedGateRecordsWhatTheLeadConcluded(t *testing.T) {
	state := start(t, ProfileNightly)
	state = mustReduce(t, state, Advance{Flow: gatedFlow(), Gate: GateAccount{Decision: GateDecisionWaited}})
	if state.Gate == nil {
		t.Fatal("the flow under test opens no gate")
	}

	before := state.Status
	judged, err := Reduce(state, GateJudged{Gate: GateAccount{
		Judgement: "reject",
		Excerpt:   "obligation 7 wants six cases and the contract permits five",
	}})
	if err != nil {
		t.Fatalf("filing a judgement: %v", err)
	}

	if judged.Status != before {
		t.Errorf("an account is not a transition: %q became %q", before, judged.Status)
	}
	if judged.Gate == nil {
		t.Fatal("the gate closed on an action that only describes it")
	}
	if judged.Gate.Judged != "reject" || !strings.Contains(judged.Gate.Reasoning, "six cases") {
		t.Errorf("the account did not reach the gate: %+v", judged.Gate)
	}

	// The state it was built from keeps its own gate: a replay produced that one,
	// and writing through the pointer would edit history.
	if state.Gate.Judged != "" {
		t.Error("the reducer wrote through its input")
	}
}

// TestGateJudgedRefusesWithNoGateOpen keeps reasoning from being filed against
// nothing. The next person to read the log would have to work out which gate it
// meant — and this guard is why the *other* position carries its account inside
// the action that opens the gate instead of calling this.
func TestGateJudgedRefusesWithNoGateOpen(t *testing.T) {
	state := start(t, ProfileNightly)

	_, err := Reduce(state, GateJudged{Gate: GateAccount{Judgement: "approve"}})
	if !errors.Is(err, ErrIllegalTransition) {
		t.Fatalf("a judgement about no gate must be refused, got %v", err)
	}
}
