package fsm

import (
	"fmt"
	"strconv"
)

// Knob is how much of the flow runs without a person: the criticality up to
// which the lead may answer a gate on its own (RFC-0006).
//
// One setting on the task, never per gate. What differs between gates is not the
// setting but the criticality they declare, and a per-gate knob would be a second
// place deciding which gates matter — competing with the profile's `waits`.
type Knob int

const (
	// KnobAsk judges nothing. Every gate with judgement criteria goes to a person,
	// which is today's behaviour, the default, and the rollback.
	KnobAsk Knob = 0

	// KnobAll judges everything. It does not mean "never asks a person": the lead
	// may judge every gate, and a lead judging honestly will sometimes conclude it
	// cannot — which falls to a person, by design.
	KnobAll Knob = 10
)

// ParseKnob reads a configured value, refusing what is out of range.
//
// The asymmetry is the same one ParseAutonomy reasoned about, and it now guards
// one setting instead of two: an absent value is KnobAsk, the most supervised, so
// neither a typo nor an unset field can quietly turn a watched run into an
// unwatched one.
func ParseKnob(value string) (Knob, error) {
	if value == "" {
		return KnobAsk, nil
	}

	// Atoi rather than Sscanf, which was the first attempt and was wrong: Sscanf
	// stops at the first character it cannot read, so "5.5" parsed as 5 and a
	// person who meant something else got a setting they did not type.
	level, err := strconv.Atoi(value)
	if err != nil {
		return KnobAsk, fmt.Errorf("autonomy has to be a number 0-10, got %q", value)
	}
	if level < int(KnobAsk) || level > int(KnobAll) {
		return KnobAsk, fmt.Errorf("autonomy has to be 0-10, got %d", level)
	}
	return Knob(level), nil
}

// Judges reports whether the lead may judge a gate at this criticality.
func (k Knob) Judges(criticality int) bool { return int(k) >= criticality }

// Autonomy is what this knob means for a failure, rather than for a gate.
//
// Derived rather than configured: two controls both called autonomy — one an enum
// over failures, one a scale over gates — is a worse product than one, and an
// alias mapping the old names onto knob values would hide the gate consequences
// of the value it set (RFC-0006).
//
// Retrying once comes first at every setting, including zero. It is the recovery
// whose bound is already in the state, it needs no judgement to be safe, and
// spending a person's attention on a failure a second attempt would have cleared
// is the cheapest thing this design can stop doing. What the knob changes is what
// happens when that retry is spent.
func (k Knob) Autonomy() string {
	if k > KnobAll/2 {
		return "decide"
	}
	return "ask"
}

// GateAnswer is who answers a gate that is opening now.
//
// It is resolved outside the reducer and travels inward inside the action, the
// same way a verification verdict does (ADR-0024) — which is what keeps the
// transition reproducible from the log and testable without a model.
type GateAnswer int

const (
	// AnswerPerson is the floor and the fallback. Every path that cannot conclude
	// safely ends here.
	AnswerPerson GateAnswer = iota

	// AnswerChecks means the declared commands ran and all passed.
	AnswerChecks

	// AnswerRejected means a declared command failed. Nothing is judged after it:
	// a failing command is an objective answer, and asking a model to weigh it
	// would be inviting it to argue with an exit code.
	AnswerRejected

	// AnswerLead means the lead judges, against the criteria the stage declares.
	AnswerLead
)

// ResolveGate decides who answers a gate, given what ran and what is declared.
//
// This is RFC-0006's decision flow, in one place and with no side effects, so the
// order it applies in is testable rather than emergent:
//
//	MECHANICAL — the checks declared for this gate, from the registry
//	   ├── none declared       → nothing to run; straight to judgement
//	   ├── any exit ≠ 0        → reject
//	   ├── a check cannot run  → a person answers, results attached
//	   └── all exit 0          ↓
//	JUDGEMENT — the criteria declared for this stage
//	   ├── no criteria declared → checks ran and passed? approve — they were the
//	   │                          answer. nothing ran either? a person answers.
//	   └── criteria present     → the knob decides who judges
func ResolveGate(gate *GateSpec, checks GateChecksOutcome, knob Knob) GateAnswer {
	if checks.Unrunnable {
		return AnswerPerson
	}
	if checks.Rejected {
		return AnswerRejected
	}

	if gate == nil || len(gate.Judge) == 0 {
		// Declaring checks for a gate *is* the statement that those commands answer
		// it. Running them and then asking anyway would make the declaration
		// meaningless — so a gate whose checks all passed, with no criteria beside
		// them, is answered and nobody is asked.
		if checks.Passed {
			return AnswerChecks
		}
		return AnswerPerson
	}

	if knob.Judges(gate.Resolved()) {
		return AnswerLead
	}
	return AnswerPerson
}

// GateSpecIn is the gate a named stage declares, or nil.
//
// It exists so a caller outside this package can reach a stage's declared
// criticality and criteria without reaching for the stage itself — the gate is
// what they need and the rest of the contract is not theirs to read.
//
// A stage the flow does not contain has no gate rather than being an error: the
// caller is asking about a gate that is opening, so a flow that does not describe
// it is a flow that declares nothing about it.
func GateSpecIn(flow []Stage, id StageID) *GateSpec {
	return stageIn(flow, id).Gate
}

// GateChecksOutcome is what the mechanical half concluded, flattened to what the
// decision needs.
//
// The three booleans are not redundant: "nothing was declared" and "everything
// declared passed" are different inputs to the rule above, and a single verdict
// value would have to invent a name for their difference.
type GateChecksOutcome struct {
	// Passed is true when at least one check ran and none failed.
	Passed bool

	// Rejected is true when a check ran and did not pass.
	Rejected bool

	// Unrunnable is true when a check never produced an exit code at all.
	Unrunnable bool
}
