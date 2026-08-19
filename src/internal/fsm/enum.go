package fsm

import (
	"encoding/json"
	"fmt"
)

// ErrUnknownValue is returned when the log holds a value this build does not
// recognise for a domain enum.
//
// It is the third refusal, after ErrUnknownAction for an action the build cannot
// read and ErrFlowChanged for a flow it was not written under. All three say the
// same thing: a log this build cannot interpret stops the replay rather than being
// read generously.
var ErrUnknownValue = fmt.Errorf("unknown value in the log")

// Every domain enum below decodes through the same shape: accept the values this
// build knows, refuse anything else.
//
// The alternative is what Go does by default, and it is worse than it looks. An
// unrecognised string unmarshals fine and lands in the field, so a `Verdict` from
// a newer version reaches the reducer as a value no switch matches — and the
// branch taken is whatever the `default` happens to be. The decision comes out
// wrong with nothing anywhere saying why.
//
// This is the failure neither journalling nor replay catches, and both Temporal
// and Restate document it. Temporal compares commands and never payloads;
// Restate protects the sequence and not the meaning, and their own example is a
// number on a 0-10 scale reinterpreted as 0-100. The engine cannot check that a
// value still *means* what it did, but it can refuse one it never meant anything
// by.
//
// Not every enum needs this, and the exclusions are as deliberate as the list.
//
// Status and GateKind are derived during replay rather than written to the log,
// so there is no stored value of either to misread.
//
// Profile is deliberately open. ShippedProfiles names the three Luna comes with
// and its own doc comment says the engine "does not validate against it: a name
// it has never heard of is a profile someone defined, not an error" — a project
// brings its own flow, and the log records the decision rather than the policy.
// Closing it here would break the extension point on the way past.
//
// The distinction worth keeping: these five are closed because the *engine*
// decides what they mean. A profile's meaning lives in configuration, so a value
// the engine does not recognise is somebody else's vocabulary rather than a log
// it cannot read.

// UnmarshalJSON refuses a task kind this build does not know.
func (k *TaskKind) UnmarshalJSON(b []byte) error {
	return decodeEnum(b, k, KnownTaskKinds(), "task kind")
}

// UnmarshalJSON refuses a gate decision this build does not know.
//
// GateDecisionAbsent is one of the known values rather than an exception: it is what
// an event predating the field decodes to, and it is what tells replay to fall
// back to the profile.
func (g *GateWaited) UnmarshalJSON(b []byte) error {
	return decodeEnum(b, g, KnownGateDecisions(), "gate decision")
}

// UnmarshalJSON refuses an evidence scope this build does not know.
//
// Scope had half of this already: scopeRank returns false for an unrecognised
// value, so Satisfies refuses it and a stage blocks rather than closing on a proof
// nobody can size. That was the right instinct and the wrong layer — it made the
// task block with a message about verification when the real problem was a log
// this build cannot read.
func (s *Scope) UnmarshalJSON(b []byte) error {
	return decodeEnum(b, s, KnownScopes(), "evidence scope")
}

// UnmarshalJSON refuses a verdict this build does not know.
//
// The most dangerous of the five: Passing() asks only whether the verdict equals
// VerdictPassed, so an unknown value silently means "did not pass" and the task
// blocks claiming verification failed — when nobody could read what it said.
func (v *Verdict) UnmarshalJSON(b []byte) error {
	return decodeEnum(b, v, KnownVerdicts(), "verdict")
}

// KnownTaskKinds is every kind this build understands.
func KnownTaskKinds() []TaskKind {
	return []TaskKind{KindFeature, KindBug, KindChore, KindDocs}
}

// KnownVerdicts is every verdict this build understands.
func KnownVerdicts() []Verdict {
	return []Verdict{VerdictPassed, VerdictFailed, VerdictStale}
}

// KnownScopes is every evidence scope this build understands.
func KnownScopes() []Scope {
	return []Scope{ScopeFull, ScopeTargeted, ScopeExistence, ScopeJudged, ScopeHuman}
}

// KnownGateDecisions is every gate decision this build understands.
func KnownGateDecisions() []GateWaited {
	return []GateWaited{
		GateDecisionAbsent,
		GateDecisionWaited,
		GateDecisionPassed,
		GateDecisionChecked,
		GateDecisionJudged,
	}
}

// decodeEnum unmarshals a string enum and refuses a value outside the known set.
//
// The error names the value and what was expected, which the AGENTS.md convention
// asks for and which matters more here than usual: the person reading it is
// looking at a log written by software they may not have.
func decodeEnum[T ~string](b []byte, into *T, known []T, what string) error {
	var raw string
	if err := unmarshalString(b, &raw); err != nil {
		return fmt.Errorf("decoding a %s: %w", what, err)
	}

	for _, value := range known {
		if T(raw) == value {
			*into = value
			return nil
		}
	}
	return fmt.Errorf("%w: %q is not a %s this build knows (%v) — "+
		"the log was most likely written by a newer version",
		ErrUnknownValue, raw, what, known)
}

// unmarshalString reads the JSON string an enum is stored as.
//
// It decodes into a plain string rather than into the enum type, which is not a
// detail: unmarshalling into the enum would call its own UnmarshalJSON and
// recurse forever. Every method above goes through here for that reason.
func unmarshalString(b []byte, into *string) error {
	return json.Unmarshal(b, into)
}
