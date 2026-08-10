package lead

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/brunoomariano/luna/src/internal/fsm"
	"github.com/brunoomariano/luna/src/internal/store"
)

// waitsFor is a policy that stops at the gate kinds it was given and nothing
// else. Named rather than inline so the tests read as what the profile does.
type waitsFor struct {
	kinds map[fsm.GateKind]bool
	asked int
}

func (p *waitsFor) Waits(_ fsm.Profile, gate fsm.GateKind) bool {
	p.asked++
	return p.kinds[gate]
}

// TestAConfiguredPolicyDecidesTheGate covers the lead consulting configuration.
//
// The profile in the log says nightly, which stops at nothing. The configured
// policy says this gate waits. The policy is what decided, because the profile
// name is only a name — what it means lives in configuration (ADR-0026).
func TestAConfiguredPolicyDecidesTheGate(t *testing.T) {
	s := newStore(t)
	nightly(t, s, "LUNA-1", fsm.KindFeature)

	policy := &waitsFor{kinds: map[fsm.GateKind]bool{fsm.GateConfirm: true}}
	l := &Lead{Store: s, Node: &deliveringNode{}, Gates: policy}

	state, err := l.Run(context.Background(), "LUNA-1")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if state.Status != fsm.StatusAwaitingGate {
		t.Fatalf("the configured policy waits at this gate, got %q", state.Status)
	}
	if state.Stage != "discovery" {
		t.Errorf("want it stopped at discovery, got %q", state.Stage)
	}
	if policy.asked == 0 {
		t.Error("the policy was never consulted")
	}
}

// TestTheDecisionReachesTheLog is what makes the replay independent of the
// policy.
//
// Recording it is the mechanism: without this the next replay would ask the
// policy again, and an edited profile would rewrite what already happened.
func TestTheDecisionReachesTheLog(t *testing.T) {
	s := newStore(t)
	nightly(t, s, "LUNA-1", fsm.KindFeature)

	l := &Lead{
		Store: s,
		Node:  &deliveringNode{},
		Gates: &waitsFor{kinds: map[fsm.GateKind]bool{fsm.GateConfirm: true}},
	}
	if _, err := l.Run(context.Background(), "LUNA-1"); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if got := decisionsIn(t, s, "LUNA-1"); len(got) != 1 || got[0] != fsm.GateDecisionWaited {
		t.Errorf("want one recorded wait, got %v", got)
	}
}

// TestAGatelessStageRecordsNoDecision covers the quiet case.
//
// Most stages open no gate. Recording "passed" for them would claim a gate was
// reached that never was, and the log is the audit trail (INV-core-2).
func TestAGatelessStageRecordsNoDecision(t *testing.T) {
	s := newStore(t)
	nightly(t, s, "LUNA-1", fsm.KindFeature)

	// A policy that waits for nothing, so the task runs the whole flow.
	l := &Lead{Store: s, Node: &deliveringNode{}, Gates: &waitsFor{kinds: map[fsm.GateKind]bool{}}}
	if _, err := l.Run(context.Background(), "LUNA-1"); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	decisions := decisionsIn(t, s, "LUNA-1")

	var gated, silent int
	for _, d := range decisions {
		if d == fsm.GateDecisionAbsent {
			silent++
			continue
		}
		gated++
		if d != fsm.GateDecisionPassed {
			t.Errorf("a policy that waits for nothing records passes, got %q", d)
		}
	}

	if gated == 0 {
		t.Error("the flow has gates; some advance should have recorded a decision")
	}
	if silent == 0 {
		t.Error("the flow has gateless stages; those record nothing")
	}
}

// TestWithoutAPolicyNothingIsRecorded covers the lead with no configuration.
//
// It falls back to the shipped policy at replay, which is the same behaviour as a
// log written before decisions existed — and the honest one, since nothing else
// decided.
func TestWithoutAPolicyNothingIsRecorded(t *testing.T) {
	s := newStore(t)
	nightly(t, s, "LUNA-1", fsm.KindFeature)

	l := &Lead{Store: s, Node: &deliveringNode{}}
	state, err := l.Run(context.Background(), "LUNA-1")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	for _, d := range decisionsIn(t, s, "LUNA-1") {
		if d != fsm.GateDecisionAbsent {
			t.Errorf("no policy means no decision recorded, got %q", d)
		}
	}

	// And the shipped nightly policy still carried it through.
	if state.Status != fsm.StatusDone {
		t.Errorf("want the task finished under the shipped nightly policy, got %q", state.Status)
	}
}

// decisionsIn reads the gate decision off every Advance in a task's log.
func decisionsIn(t *testing.T, s *store.Store, taskID string) []fsm.GateWaited {
	t.Helper()

	events, err := s.Events(taskID)
	if err != nil {
		t.Fatalf("reading the log: %v", err)
	}

	var decisions []fsm.GateWaited
	for _, e := range events {
		if e.Action != "Advance" {
			continue
		}
		decisions = append(decisions, decisionOf(t, e.Payload))
	}
	return decisions
}

// decisionOf reads the recorded decision out of an Advance payload, which is the
// shape the log actually stores rather than what the codec hands back.
func decisionOf(t *testing.T, payload string) fsm.GateWaited {
	t.Helper()

	if payload == "" || payload == "{}" {
		return fsm.GateDecisionAbsent
	}

	var body struct {
		GateDecision fsm.GateWaited `json:"gate_decision"`
	}
	if err := json.Unmarshal([]byte(payload), &body); err != nil {
		t.Fatalf("decoding %q: %v", payload, err)
	}
	return body.GateDecision
}
