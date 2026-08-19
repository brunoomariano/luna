package store

import (
	"testing"

	"github.com/brunoomariano/luna/src/internal/fsm"
)

// TestAnEditedProfileDoesNotRewriteThePast is the guarantee recording the gate
// decision, rather than the policy that produced it, exists for.
//
// A task ran overnight and walked past its gates. Someone later tightens what
// that profile means. Replaying the task must still show a run that walked past
// its gates, because that is what happened — the log records the decision, not
// the policy that produced it.
//
// The test fakes the edit the only way the log can see it: the same profile name,
// replayed with decisions recorded against it. If replay consulted a policy
// instead, the assertion below would flip the moment the policy did.
func TestAnEditedProfileDoesNotRewriteThePast(t *testing.T) {
	s := openTemp(t)

	// A task created as nightly, whose first gate was recorded as passed.
	appendAll(
		t, s, "LUNA-1",
		fsm.TaskCreated{Kind: fsm.KindFeature, Profile: fsm.ProfileNightly, Flow: fsm.Fingerprint(gatedFlow())},
		fsm.Advance{GateDecision: fsm.GateDecisionPassed},
	)

	state, err := s.Replay("LUNA-1", gatedFlow())
	if err != nil {
		t.Fatalf("replaying: %v", err)
	}
	if state.Status != fsm.StatusRunning {
		t.Errorf("the recorded run walked past the gate, got %q", state.Status)
	}

	// The same log, with the decision recorded the other way — which is what a
	// supervised run of the same profile would have written. Replay follows the
	// log, not the name.
	appendAll(
		t, s, "LUNA-2",
		fsm.TaskCreated{Kind: fsm.KindFeature, Profile: fsm.ProfileNightly, Flow: fsm.Fingerprint(gatedFlow())},
		fsm.Advance{GateDecision: fsm.GateDecisionWaited},
	)

	waited, err := s.Replay("LUNA-2", gatedFlow())
	if err != nil {
		t.Fatalf("replaying: %v", err)
	}
	if waited.Status != fsm.StatusAwaitingGate {
		t.Errorf("a nightly task whose log says the gate waited replays as waiting, got %q", waited.Status)
	}
}

// TestALogWithoutDecisionsStillReplays covers the events written before the field
// existed.
//
// They carry a profile name and nothing else, so replay falls back to the shipped
// policy — the same one that produced them.
func TestALogWithoutDecisionsStillReplays(t *testing.T) {
	s := openTemp(t)

	appendAll(
		t, s, "LUNA-1",
		fsm.TaskCreated{Kind: fsm.KindFeature, Profile: fsm.ProfileInteractive, Flow: fsm.Fingerprint(gatedFlow())},
		fsm.Advance{},
	)

	state, err := s.Replay("LUNA-1", gatedFlow())
	if err != nil {
		t.Fatalf("replaying: %v", err)
	}
	if state.Status != fsm.StatusAwaitingGate {
		t.Errorf("an old interactive log still stops at its gate, got %q", state.Status)
	}
}

// TestAProfileNoLongerDefinedStillReplays covers the deleted-profile case.
//
// Replay never asks the configuration anything, so a name that resolves to
// nothing is not an error: the task's own decisions carry it.
func TestAProfileNoLongerDefinedStillReplays(t *testing.T) {
	s := openTemp(t)

	appendAll(
		t, s, "LUNA-1",
		fsm.TaskCreated{Kind: fsm.KindFeature, Profile: fsm.Profile("deleted-last-week"), Flow: fsm.Fingerprint(gatedFlow())},
		fsm.Advance{GateDecision: fsm.GateDecisionPassed},
	)

	state, err := s.Replay("LUNA-1", gatedFlow())
	if err != nil {
		t.Fatalf("a profile the config no longer defines must still replay: %v", err)
	}
	if state.Profile != fsm.Profile("deleted-last-week") {
		t.Errorf("want the profile as recorded, got %q", state.Profile)
	}
	if state.Status != fsm.StatusRunning {
		t.Errorf("its recorded decision still governs, got %q", state.Status)
	}
}

func appendAll(t *testing.T, s *Store, taskID string, actions ...fsm.Action) {
	t.Helper()

	for _, action := range actions {
		if err := s.AppendAction(taskID, action); err != nil {
			t.Fatalf("appending %T: %v", action, err)
		}
	}
}

// gatedFlow is a one-stage flow whose stage opens a confirm.
//
// These tests are about what a recorded decision does on replay, not about the
// shipped flow's shape — which changed when integration left Luna's scope,
// removing `commit`, and `discovery` went with it, leaving the mechanical
// `setup` first and gateless.
func gatedFlow() []fsm.Stage {
	return []fsm.Stage{{
		ID: "gated", Role: "someone", Requires: []fsm.Artifact{fsm.TaskID},
		Produces: []fsm.Artifact{"thing"},
		Gate:     &fsm.GateSpec{Kind: fsm.GateConfirm, Reason: "confirm it"},
	}}
}
