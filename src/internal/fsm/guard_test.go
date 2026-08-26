package fsm

import (
	"strings"
	"testing"
)

// guardedBuild drives a task to its build stage on the chore flow, which is the
// shortest path to a stage that declares a guard.
func guardedBuild(t *testing.T) ([]Stage, TaskState) {
	t.Helper()

	flow, err := FlowNamed("chore")
	if err != nil {
		t.Fatalf("loading the chore flow: %v", err)
	}

	state := NewTaskState("G-1", KindChore)
	if state, err = Reduce(state, TaskCreated{Kind: KindChore, FlowName: "chore"}); err != nil {
		t.Fatalf("creating: %v", err)
	}
	if state, err = Reduce(state, Advance{Flow: flow}); err != nil {
		t.Fatalf("entering setup: %v", err)
	}
	if state, err = Reduce(state, Complete{
		Flow:      flow,
		Delivered: []Artifact{"worktree"},
		Evidence:  map[Artifact]Evidence{"worktree": Exists(1)},
		Commit:    "base01",
	}); err != nil {
		t.Fatalf("closing setup: %v", err)
	}
	if state, err = Reduce(state, Advance{Flow: flow}); err != nil {
		t.Fatalf("entering build: %v", err)
	}
	return flow, state
}

// buildDelivery is everything the chore flow's build stage owes, proven.
func buildDelivery(flow []Stage) Complete {
	stage := stageIn(flow, "build")
	owed := append(append([]Artifact{}, stage.Produces...), stage.ProducesForHuman...)

	evidence := map[Artifact]Evidence{}
	for _, artifact := range owed {
		evidence[artifact] = Evidence{
			Scope:   VerifierFor(stage, artifact).Proves(),
			Verdict: VerdictPassed,
			Command: VerifierFor(stage, artifact).Describe(),
		}
	}
	return Complete{Flow: flow, Delivered: owed, Evidence: evidence, Commit: "cafe01"}
}

// TestAGuardedDeliveryStopsForAPerson is the mechanism: a delivery that touched
// something consequential suspends on the way out of the stage that made it.
func TestAGuardedDeliveryStopsForAPerson(t *testing.T) {
	flow, state := guardedBuild(t)

	action := buildDelivery(flow)
	action.Guarded = []string{"migrations/002_drop_sessions.sql"}

	state, err := Reduce(state, action)
	if err != nil {
		t.Fatalf("closing build: %v", err)
	}

	if state.Status != StatusAwaitingGate {
		t.Fatalf("a guarded delivery did not stop: %q", state.Status)
	}
	if state.Gate == nil || state.Gate.Kind != GateGuard {
		t.Fatalf("want a guard gate, got %+v", state.Gate)
	}
	// The paths, not only the wording: "the delivery touches something
	// consequential" is a sentence somebody approves without looking.
	if !strings.Contains(state.Gate.Reason, "002_drop_sessions.sql") {
		t.Errorf("the gate does not say what was touched: %s", state.Gate.Reason)
	}
	if !strings.Contains(state.Gate.Reason, "migrations") {
		t.Errorf("the gate does not carry the stage's own reason: %s", state.Gate.Reason)
	}
}

// TestAnUnguardedDeliveryCarriesOn. The guard has to be silent on the ordinary
// case, or it is a gate on every stage wearing a different name.
func TestAnUnguardedDeliveryCarriesOn(t *testing.T) {
	flow, state := guardedBuild(t)

	state, err := Reduce(state, buildDelivery(flow))
	if err != nil {
		t.Fatalf("closing build: %v", err)
	}

	if state.Status != StatusStageDone {
		t.Errorf("an ordinary delivery was stopped: %q — %s", state.Status, state.Blocked)
	}
	if state.Gate != nil {
		t.Errorf("an ordinary delivery opened %+v", state.Gate)
	}
}

// TestNoAutonomySettingGetsPastAGuard is what makes it worth having. The knob is
// how much the lead may decide on its own, and whether dropping a table was
// intended is not a thing it can weigh — so this gate carries no judgement
// criteria and reaches a person at every setting.
func TestNoAutonomySettingGetsPastAGuard(t *testing.T) {
	flow, state := guardedBuild(t)
	state.Knob = KnobAll

	action := buildDelivery(flow)
	action.Guarded = []string{".env.production"}
	// And even with the gate decision that waves every other gate through.
	action.Gate = GateAccount{Decision: GateDecisionPassed}

	state, err := Reduce(state, action)
	if err != nil {
		t.Fatalf("closing build: %v", err)
	}

	if state.Status != StatusAwaitingGate || state.Gate == nil || state.Gate.Kind != GateGuard {
		t.Errorf("the highest autonomy walked past a guard: %q %+v", state.Status, state.Gate)
	}
}

// TestApprovingAGuardCarriesOn. It is a gate like any other on the way out, so
// the person who looked releases it — otherwise a guard would be a block wearing
// a gate's name, and the task would need unblocking rather than answering.
func TestApprovingAGuardCarriesOn(t *testing.T) {
	flow, state := guardedBuild(t)

	action := buildDelivery(flow)
	action.Guarded = []string{"migrations/001.sql"}
	state, err := Reduce(state, action)
	if err != nil {
		t.Fatalf("closing build: %v", err)
	}

	if state, err = Reduce(state, GateApprove{}); err != nil {
		t.Fatalf("approving the guard: %v", err)
	}
	if state.Gate != nil {
		t.Errorf("the gate stayed open after approval: %+v", state.Gate)
	}
	if state.Base != "cafe01" {
		t.Errorf("the delivery was lost across the gate: base %q", state.Base)
	}
}

// TestAGuardIsPolicyAndNotHistory. The patterns are read by the node and never by
// the reducer, so editing the list changes what stops tomorrow and cannot rewrite
// what stopped last week — a replay reads whether a guard fired, from the log.
func TestAGuardIsPolicyAndNotHistory(t *testing.T) {
	flow, err := FlowNamed("chore")
	if err != nil {
		t.Fatalf("loading the chore flow: %v", err)
	}
	before := Fingerprint(flow)

	widened := append([]Stage(nil), flow...)
	for i := range widened {
		if widened[i].Guard == nil {
			continue
		}
		widened[i].Guard = &GuardSpec{Paths: []string{"anything", "at", "all"}, Reason: "changed"}
	}

	if got := Fingerprint(widened); got != before {
		t.Errorf("editing a guard changed the flow's identity: %s → %s", before, got)
	}
}
