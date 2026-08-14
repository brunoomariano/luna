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
	if state.Stage != "scenarios" {
		t.Errorf("want it stopped at the first gate, got %q", state.Stage)
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

	// The run stops at the first confirm, so exactly one decision says "waited";
	// the advances before it opened no gate and recorded nothing.
	got := decisionsIn(t, s, "LUNA-1")
	waited := 0
	for _, d := range got {
		if d == fsm.GateDecisionWaited {
			waited++
		}
	}
	if waited != 1 {
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

// flowWithCriticality is the shipped flow with one gate declared, so a test can
// say which knob reaches it without depending on the stock's own values.
func flowWithCriticality(t *testing.T, stage fsm.StageID, level int, judge ...string) []fsm.Stage {
	t.Helper()

	flow := fsm.DefaultFlow()
	for i := range flow {
		if flow[i].ID != stage {
			continue
		}
		if flow[i].Gate == nil {
			t.Fatalf("%s opens no gate to declare criticality on", stage)
		}
		declared := *flow[i].Gate
		declared.Criticality = level
		declared.Judge = judge
		flow[i].Gate = &declared
		return flow
	}
	t.Fatalf("no stage %q in the flow", stage)
	return nil
}

// TestTheKnobDecidesWhetherTheLeadAnswersAWaitingGate is what the whole feature
// comes to: the same flow, the same policy, two knob settings, two different
// facts in the log.
func TestTheKnobDecidesWhetherTheLeadAnswersAWaitingGate(t *testing.T) {
	flow := flowWithCriticality(t, "scenarios", 5, "the plan covers the acceptance criteria")

	cases := map[string]struct {
		knob fsm.Knob
		want fsm.GateWaited
	}{
		"below the gate's criticality, a person answers": {knob: 4, want: fsm.GateDecisionWaited},
		"at it, the lead judges":                         {knob: 5, want: fsm.GateDecisionJudged},
		"above it, the lead judges":                      {knob: 10, want: fsm.GateDecisionJudged},
		"the default judges nothing":                     {knob: fsm.KnobAsk, want: fsm.GateDecisionWaited},
	}

	for name, c := range cases {
		t.Run(name, func(t *testing.T) {
			s := newStore(t)
			nightly(t, s, "LUNA-1", fsm.KindFeature)
			if err := s.AppendAction("LUNA-1", fsm.SetKnob{Knob: c.knob}); err != nil {
				t.Fatalf("setting the knob: %v", err)
			}

			l := &Lead{
				Store: s, Node: &deliveringNode{}, Flow: flow,
				Gates: &waitsFor{kinds: map[fsm.GateKind]bool{fsm.GateConfirm: true}},
			}
			if _, err := l.Run(context.Background(), "LUNA-1"); err != nil {
				t.Fatalf("Run: %v", err)
			}

			if got := firstGateDecision(t, s, "LUNA-1"); got != c.want {
				t.Errorf("knob %d recorded %q, want %q", c.knob, got, c.want)
			}
		})
	}
}

// firstGateDecision reads the decision the earliest gate recorded.
//
// The first, not the last: the run walks past several gates, and reading the
// final one answers a question about whichever gate happened to come last. That
// was the first version of this helper and it made a passing implementation look
// broken — the log said `judged` for the gate under test and `passed` for a later
// one the profile let through.
func firstGateDecision(t *testing.T, s *store.Store, id string) fsm.GateWaited {
	t.Helper()

	events, err := s.Events(id)
	if err != nil {
		t.Fatalf("reading the log: %v", err)
	}

	for _, e := range events {
		if e.Action != "Advance" {
			continue
		}
		var advance struct {
			GateDecision fsm.GateWaited `json:"gate_decision"`
		}
		if err := json.Unmarshal([]byte(e.Payload), &advance); err != nil {
			t.Fatalf("decoding an advance: %v", err)
		}
		if advance.GateDecision != fsm.GateDecisionAbsent {
			return advance.GateDecision
		}
	}
	t.Fatal("no advance recorded a gate decision")
	return fsm.GateDecisionAbsent
}
