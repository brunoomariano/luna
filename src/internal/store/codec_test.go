package store

import (
	"errors"
	"strings"
	"testing"

	"github.com/brunoomariano/luna/src/internal/fsm"
)

// TestEveryActionSurvivesARoundTrip covers the whole codec.
//
// An action that encodes but does not decode back to itself would replay a task
// into a state it was never in — and the state would look perfectly valid, which
// is what makes the bug expensive. Every action is checked rather than a sample.
func TestEveryActionSurvivesARoundTrip(t *testing.T) {
	cases := []struct {
		name   string
		action fsm.Action
		verify func(*testing.T, fsm.Action)
	}{
		{
			name:   "Advance",
			action: fsm.Advance{Flow: fsm.DefaultFlow(), GateDecision: fsm.GateDecisionPassed},
			verify: func(t *testing.T, got fsm.Action) {
				a, ok := got.(fsm.Advance)
				if !ok {
					t.Fatalf("want Advance, got %T", got)
				}
				// The flow is supplied at replay, not read from the log: recording
				// it would freeze a task to the flow it started under (ADR-0017).
				if len(a.Flow) != len(fsm.DefaultFlow()) {
					t.Errorf("replay supplies the current flow, got %d stages", len(a.Flow))
				}
				// The decision goes the other way: it is history, and recomputing
				// it would let an edited profile rewrite the past (ADR-0026).
				if a.GateDecision != fsm.GateDecisionPassed {
					t.Errorf("want the recorded gate decision, got %q", a.GateDecision)
				}
			},
		},
		{
			name:   "Complete",
			action: fsm.Complete{Delivered: []fsm.Artifact{"code", "tests_green"}},
			verify: func(t *testing.T, got fsm.Action) {
				a, ok := got.(fsm.Complete)
				if !ok {
					t.Fatalf("want Complete, got %T", got)
				}
				if len(a.Delivered) != 2 {
					t.Errorf("want the 2 delivered artifacts, got %v", a.Delivered)
				}
			},
		},
		{
			name: "Complete with evidence",
			action: fsm.Complete{
				Delivered: []fsm.Artifact{"code"},
				Evidence: map[fsm.Artifact]fsm.Evidence{"code": {
					Scope:    fsm.ScopeFull,
					Verdict:  fsm.VerdictPassed,
					Command:  "go test ./...",
					ExitCode: 0,
				}},
			},
			verify: func(t *testing.T, got fsm.Action) {
				a, ok := got.(fsm.Complete)
				if !ok {
					t.Fatalf("want Complete, got %T", got)
				}
				// Every field is checked rather than the map: evidence that comes
				// back with its scope or verdict lost would replay as a different
				// claim about the same delivery (ADR-0024, ADR-0028).
				code := a.Evidence["code"]
				if code.Scope != fsm.ScopeFull || code.Verdict != fsm.VerdictPassed || code.Command != "go test ./..." {
					t.Errorf("the evidence must survive the round trip (ADR-0024), got %+v", a.Evidence)
				}
			},
		},
		{
			name:   "Fail",
			action: fsm.Fail{Reason: "the compiler disagreed"},
			verify: func(t *testing.T, got fsm.Action) {
				a, ok := got.(fsm.Fail)
				if !ok {
					t.Fatalf("want Fail, got %T", got)
				}
				if a.Reason != "the compiler disagreed" {
					t.Errorf("the reason must survive: %+v", a)
				}
			},
		},
		{
			name:   "GateApprove",
			action: fsm.GateApprove{},
			verify: func(t *testing.T, got fsm.Action) {
				if _, ok := got.(fsm.GateApprove); !ok {
					t.Errorf("want GateApprove, got %T", got)
				}
			},
		},
		{
			name:   "GateAdjust",
			action: fsm.GateAdjust{Payload: "the contract a human fixed"},
			verify: func(t *testing.T, got fsm.Action) {
				a, ok := got.(fsm.GateAdjust)
				if !ok {
					t.Fatalf("want GateAdjust, got %T", got)
				}
				if a.Payload != "the contract a human fixed" {
					t.Errorf("the adjusted payload must survive: %+v", a)
				}
			},
		},
		{
			name:   "GateReject",
			action: fsm.GateReject{Reason: "the approach does not hold"},
			verify: func(t *testing.T, got fsm.Action) {
				a, ok := got.(fsm.GateReject)
				if !ok {
					t.Fatalf("want GateReject, got %T", got)
				}
				if a.Reason == "" {
					t.Error("the rejection reason must survive")
				}
			},
		},
		{
			name:   "ReviewFinding",
			action: fsm.ReviewFinding{Aligned: true, Summary: "wrong boundary", Limits: fsm.LoopLimits{MaxRounds: 7}},
			verify: func(t *testing.T, got fsm.Action) {
				a, ok := got.(fsm.ReviewFinding)
				if !ok {
					t.Fatalf("want ReviewFinding, got %T", got)
				}
				if !a.Aligned || a.Summary != "wrong boundary" {
					t.Errorf("the finding must survive: %+v", a)
				}
				if a.Limits.MaxRounds != 7 {
					t.Errorf("custom limits must survive, got %+v", a.Limits)
				}
			},
		},
		{
			name:   "SetKnob",
			action: fsm.SetKnob{Knob: 7, Reason: "trusting the checks here"},
			verify: func(t *testing.T, got fsm.Action) {
				a, ok := got.(fsm.SetKnob)
				if !ok {
					t.Fatalf("want SetKnob, got %T", got)
				}
				// The value is the whole point: a knob that decoded back as zero
				// would replay an autonomous run as a supervised one, and the log
				// would look right while saying the opposite of what happened.
				if a.Knob != 7 {
					t.Errorf("the knob must survive, got %d", a.Knob)
				}
				if a.Reason != "trusting the checks here" {
					t.Errorf("why a person moved it must survive, got %q", a.Reason)
				}
			},
		},
		{
			name:   "Unblock",
			action: fsm.Unblock{},
			verify: func(t *testing.T, got fsm.Action) {
				if _, ok := got.(fsm.Unblock); !ok {
					t.Errorf("want Unblock, got %T", got)
				}
			},
		},
		{
			// The statement rides in the log now rather than being read from a
			// registry on every stage (ADR-0067), so the round trip is what stands
			// between an agent's briefing and silence.
			name: "TaskCreated",
			action: fsm.TaskCreated{
				Kind:      fsm.KindBug,
				Profile:   fsm.ProfileInteractive,
				Statement: fsm.Statement{Description: "zsh-only glob", Acceptance: "bash -n exits 0"},
				Flow:      fsm.Fingerprint(fsm.DefaultFlow()),
			},
			verify: func(t *testing.T, got fsm.Action) {
				a, ok := got.(fsm.TaskCreated)
				if !ok {
					t.Fatalf("want TaskCreated, got %T", got)
				}
				if a.Kind != fsm.KindBug {
					t.Errorf("the kind must survive, got %q", a.Kind)
				}
				if a.Statement.Description != "zsh-only glob" {
					t.Errorf("what the task is about must survive, got %q", a.Statement.Description)
				}
				if a.Statement.Acceptance != "bash -n exits 0" {
					t.Errorf("how it will be judged must survive, got %q", a.Statement.Acceptance)
				}
			},
		},
		{
			// The empty-versus-absent distinction has to survive the log: one sends
			// the gate to judgement having been decided, the other never decided.
			name:   "GateChecksDeclared",
			action: fsm.GateChecksDeclared{Gate: fsm.GateConfirm, Checks: []string{"make ci", "make race"}},
			verify: func(t *testing.T, got fsm.Action) {
				a, ok := got.(fsm.GateChecksDeclared)
				if !ok {
					t.Fatalf("want GateChecksDeclared, got %T", got)
				}
				if a.Gate != fsm.GateConfirm {
					t.Errorf("the gate must survive, got %q", a.Gate)
				}
				if len(a.Checks) != 2 || a.Checks[0] != "make ci" {
					t.Errorf("the checks must survive in order, got %v", a.Checks)
				}
			},
		},
		{
			name: "StatementRevised",
			action: fsm.StatementRevised{
				Statement: fsm.Statement{Description: "corrected", Design: "a POSIX loop"},
			},
			verify: func(t *testing.T, got fsm.Action) {
				a, ok := got.(fsm.StatementRevised)
				if !ok {
					t.Fatalf("want StatementRevised, got %T", got)
				}
				if a.Statement.Description != "corrected" {
					t.Errorf("the revision must survive, got %q", a.Statement.Description)
				}
				if a.Statement.Design != "a POSIX loop" {
					t.Errorf("the design must survive, got %q", a.Statement.Design)
				}
			},
		},
	}

	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			name, payload, err := encodeAction(c.action)
			if err != nil {
				t.Fatalf("encoding: %v", err)
			}

			decoded, err := decodeAction(Event{Action: name, Payload: payload}, fsm.DefaultFlow())
			if err != nil {
				t.Fatalf("decoding: %v", err)
			}
			c.verify(t, decoded)
		})
	}
}

// TestEncodingAnUnknownActionIsRefused covers encodeAction's default branch.
//
// Writing an action the codec cannot read back would produce a log that only
// looks complete. Better to refuse at the point of writing.
func TestEncodingAnUnknownActionIsRefused(t *testing.T) {
	// A nil Action reaches the same branch without needing a type that satisfies
	// the closed interface from another package.
	_, _, err := encodeAction(nil)

	if !errors.Is(err, ErrUnknownAction) {
		t.Errorf("want ErrUnknownAction encoding something unknown, got %v", err)
	}
}

// TestDecodingAMalformedPayloadIsReported covers decodeJSON's error path.
//
// A corrupted row stops the replay and names the payload, rather than yielding a
// zero-valued action that would look like a legitimate transition.
func TestDecodingAMalformedPayloadIsReported(t *testing.T) {
	_, err := decodeAction(Event{Action: actionComplete, Payload: "{not json"}, fsm.DefaultFlow())

	if err == nil {
		t.Fatal("a malformed payload must be reported")
	}
	if !strings.Contains(err.Error(), "decoding") {
		t.Errorf("the error should say what it failed to decode, got %v", err)
	}
}

// TestAnEmptyPayloadDecodesToTheZeroAction covers the empty-payload shortcut.
//
// The three actions that carry no data store an empty payload; decoding one must
// not go through JSON at all.
func TestAnEmptyPayloadDecodesToTheZeroAction(t *testing.T) {
	got, err := decodeAction(Event{Action: actionComplete, Payload: ""}, fsm.DefaultFlow())
	if err != nil {
		t.Fatalf("an empty payload is not malformed: %v", err)
	}

	a, ok := got.(fsm.Complete)
	if !ok {
		t.Fatalf("want Complete, got %T", got)
	}
	if len(a.Delivered) != 0 {
		t.Errorf("want the zero action, got %+v", a)
	}
}

// TestAppendActionRefusesWhatItCannotRecord covers the error path in AppendAction.
func TestAppendActionRefusesWhatItCannotRecord(t *testing.T) {
	s := openTemp(t)

	if err := s.AppendAction("LUNA-1", nil); !errors.Is(err, ErrUnknownAction) {
		t.Errorf("want ErrUnknownAction, got %v", err)
	}

	events, err := s.Events("LUNA-1")
	if err != nil {
		t.Fatalf("reading: %v", err)
	}
	if len(events) != 0 {
		t.Error("a refused action must not reach the log")
	}
}
