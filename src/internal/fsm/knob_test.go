package fsm

import "testing"

// TestPassingChecksApproveWithoutAsking is the rule the design turns on.
//
// Declaring checks for a gate *is* the statement that those commands answer it.
// A design that ran them and then asked a person anyway would have made the
// declaration meaningless, and the person who wrote it would learn not to bother.
func TestPassingChecksApproveWithoutAsking(t *testing.T) {
	gate := &GateSpec{Kind: GateConfirm, Criticality: 3}
	passed := GateChecksOutcome{Passed: true}

	// True at every knob setting, including the one that judges nothing: the knob
	// governs judgement, and this gate never reached the judgement half.
	for knob := KnobAsk; knob <= KnobAll; knob++ {
		if got := ResolveGate(gate, passed, knob); got != AnswerChecks {
			t.Errorf("knob %d: passing checks did not answer the gate, got %v", knob, got)
		}
	}
}

// TestAFailingCheckRejectsAndNothingIsJudged covers the order of the two halves.
//
// The mechanical half runs first and can only reject, which is the second of the
// three differences RFC-0006 claims against the shape that rubber-stamped: the
// model is never the thing standing between a failing test and an approval.
func TestAFailingCheckRejectsAndNothingIsJudged(t *testing.T) {
	gate := &GateSpec{
		Kind:        GateReviewArtifact,
		Criticality: 1,
		Judge:       []string{"the contract states what is forbidden"},
	}

	// Criticality 1 with the knob at 10: the lead would judge this gate if it ever
	// reached judgement. It must not.
	if got := ResolveGate(gate, GateChecksOutcome{Rejected: true}, KnobAll); got != AnswerRejected {
		t.Errorf("a failing check was not the answer, got %v", got)
	}
}

// TestACheckThatCannotRunGoesToAPerson is the direction this design fails in.
//
// Nobody got an answer, so there is nothing to conclude — and RF4's rule is that
// the fallback is always the human.
func TestACheckThatCannotRunGoesToAPerson(t *testing.T) {
	gate := &GateSpec{Kind: GateConfirm, Criticality: 1, Judge: []string{"anything"}}

	if got := ResolveGate(gate, GateChecksOutcome{Unrunnable: true}, KnobAll); got != AnswerPerson {
		t.Errorf("an unrunnable check did not fall to a person, got %v", got)
	}
}

// TestAGateWithNothingDeclaredAsksAPerson is what makes this additive.
//
// It is every gate in the shipped stock: no checks, no criteria. A project that
// declares nothing keeps being asked about everything.
func TestAGateWithNothingDeclaredAsksAPerson(t *testing.T) {
	bare := &GateSpec{Kind: GateConfirm, Reason: "approve the plan"}

	for knob := KnobAsk; knob <= KnobAll; knob++ {
		if got := ResolveGate(bare, GateChecksOutcome{}, knob); got != AnswerPerson {
			t.Errorf("knob %d: a gate declaring nothing did not ask a person, got %v", knob, got)
		}
	}
}

// TestTheKnobDecidesWhoJudges covers the comparison, at the boundary where it
// changes.
func TestTheKnobDecidesWhoJudges(t *testing.T) {
	gate := &GateSpec{
		Kind:        GateReviewArtifact,
		Criticality: 7,
		Judge:       []string{"every acceptance criterion appears as an obligation"},
	}

	for knob := KnobAsk; knob <= KnobAll; knob++ {
		got := ResolveGate(gate, GateChecksOutcome{}, knob)

		want := AnswerPerson
		if knob >= 7 {
			want = AnswerLead
		}
		if got != want {
			t.Errorf("knob %d against criticality 7: want %v, got %v", knob, want, got)
		}
	}
}

// TestKnobZeroJudgesNothingAnywhere is the default and the rollback, asserted
// across every declared level rather than at one.
func TestKnobZeroJudgesNothingAnywhere(t *testing.T) {
	for level := 1; level <= int(KnobAll); level++ {
		gate := &GateSpec{Kind: GateConfirm, Criticality: level, Judge: []string{"a criterion"}}

		if got := ResolveGate(gate, GateChecksOutcome{}, KnobAsk); got != AnswerPerson {
			t.Errorf("knob 0 did not ask a person about criticality %d, got %v", level, got)
		}
	}
}

// TestChecksAndCriteriaCoexist is the case the two halves exist for.
//
// A gate may declare both. The checks run first and pass; the criteria are still
// there, so the knob decides who weighs them — passing checks do not skip a
// judgement somebody asked for.
func TestChecksAndCriteriaCoexist(t *testing.T) {
	gate := &GateSpec{
		Kind:        GateReviewArtifact,
		Criticality: 5,
		Judge:       []string{"the contract states what is forbidden"},
	}
	passed := GateChecksOutcome{Passed: true}

	if got := ResolveGate(gate, passed, KnobAsk); got != AnswerPerson {
		t.Errorf("knob 0 with criteria present: want a person, got %v", got)
	}
	if got := ResolveGate(gate, passed, KnobAll); got != AnswerLead {
		t.Errorf("knob 10 with criteria present: want the lead, got %v", got)
	}
}

// TestAnUndeclaredCriticalityIsReachedOnlyByTheHighestKnob covers the default
// where it matters — in the decision, not just in the accessor.
func TestAnUndeclaredCriticalityIsReachedOnlyByTheHighestKnob(t *testing.T) {
	gate := &GateSpec{Kind: GateConfirm, Judge: []string{"a criterion"}}

	for knob := KnobAsk; knob < KnobAll; knob++ {
		if got := ResolveGate(gate, GateChecksOutcome{}, knob); got != AnswerPerson {
			t.Errorf("knob %d reached a gate that declared no criticality, got %v", knob, got)
		}
	}
	if got := ResolveGate(gate, GateChecksOutcome{}, KnobAll); got != AnswerLead {
		t.Errorf("knob 10 did not reach an undeclared gate, got %v", got)
	}
}

// TestTheKnobIsParsedAndBounded covers the setting a person types.
func TestTheKnobIsParsedAndBounded(t *testing.T) {
	accepted := map[string]Knob{"": KnobAsk, "0": KnobAsk, "5": 5, "10": KnobAll}
	for value, want := range accepted {
		got, err := ParseKnob(value)
		if err != nil {
			t.Errorf("autonomy %q was refused: %v", value, err)
		}
		if got != want {
			t.Errorf("autonomy %q parsed as %d, want %d", value, got, want)
		}
	}

	for _, value := range []string{"-1", "11", "high", "decide", "5.5"} {
		got, err := ParseKnob(value)
		if err == nil {
			t.Errorf("autonomy %q was accepted as %d", value, got)
		}
		// A refused value must not leave a permissive setting behind: the whole
		// asymmetry is that a mistake fails towards supervision.
		if got != KnobAsk {
			t.Errorf("autonomy %q was refused but left the knob at %d", value, got)
		}
	}
}

// TestAutonomyIsDerivedFromTheKnob is the fold: one control, and its state is
// what decides failure behaviour.
func TestAutonomyIsDerivedFromTheKnob(t *testing.T) {
	for knob := KnobAsk; knob <= KnobAll; knob++ {
		want := "ask"
		if knob > 5 {
			want = "decide"
		}
		if got := knob.Autonomy(); got != want {
			t.Errorf("knob %d derives autonomy %q, want %q", knob, got, want)
		}
	}
}

// TestSetKnobRecordsTheChangeAndLeavesTheGate covers the reducer directly, where
// the two promises live.
func TestSetKnobRecordsTheChangeAndLeavesTheGate(t *testing.T) {
	running := TaskState{ID: "LUNA-1", Status: StatusRunning}

	moved, err := Reduce(running, SetKnob{Knob: 7, Reason: "the checks cover this one"})
	if err != nil {
		t.Fatalf("moving the knob: %v", err)
	}
	if moved.Knob != 7 {
		t.Errorf("the knob did not move: %d", moved.Knob)
	}
	// Moving the setting is not a transition: the task keeps doing what it was.
	if moved.Status != StatusRunning {
		t.Errorf("moving the knob changed the status to %q", moved.Status)
	}

	waiting := TaskState{
		ID:     "LUNA-1",
		Status: StatusAwaitingGate,
		Gate:   &PendingGate{Kind: GateConfirm, Stage: "scenarios"},
	}
	stillWaiting, err := Reduce(waiting, SetKnob{Knob: KnobAll})
	if err != nil {
		t.Fatalf("moving the knob with a gate open: %v", err)
	}
	if stillWaiting.Gate == nil || stillWaiting.Status != StatusAwaitingGate {
		t.Error("raising the knob took an open gate away from the person looking at it")
	}
}

// TestSetKnobRefusesWhatCannotBeHonoured covers the two guards.
//
// A terminal task cannot become more autonomous — there is nothing left to be
// autonomous about, and recording the change would put a fact in the log that
// never affected anything. An out-of-range value is refused for the reason the
// whole scale is asymmetric: nothing may widen autonomy by accident.
func TestSetKnobRefusesWhatCannotBeHonoured(t *testing.T) {
	for _, status := range []Status{StatusDone, StatusAbandoned} {
		ended := TaskState{ID: "LUNA-1", Status: status}
		if _, err := Reduce(ended, SetKnob{Knob: KnobAll}); err == nil {
			t.Errorf("a %s task accepted a knob change", status)
		}
	}

	running := TaskState{ID: "LUNA-1", Status: StatusRunning}
	for _, knob := range []Knob{-1, 11, 99} {
		if _, err := Reduce(running, SetKnob{Knob: knob}); err == nil {
			t.Errorf("knob %d was accepted by the reducer", knob)
		}
	}
}

// TestGateSpecInFindsWhatTheStageDeclared covers the accessor the lead reads a
// gate's declaration through.
//
// It is what decides whether a gate waits at all now (ADR-0063), so a stage the
// flow does not contain has to answer nil rather than panic: the caller is asking
// about a gate that is opening, and a flow that does not describe it declares
// nothing about it.
func TestGateSpecInFindsWhatTheStageDeclared(t *testing.T) {
	flow := []Stage{
		{ID: "plain"},
		{ID: "gated", Gate: &GateSpec{Kind: GateConfirm, Criticality: 4, Judge: []string{"a criterion"}}},
	}

	spec := GateSpecIn(flow, "gated")
	if spec == nil {
		t.Fatal("the declared gate was not found")
	}
	if spec.Criticality != 4 || len(spec.Judge) != 1 {
		t.Errorf("the declaration did not survive: %+v", spec)
	}

	if GateSpecIn(flow, "plain") != nil {
		t.Error("a stage with no gate reported one")
	}
	if GateSpecIn(flow, "no-such-stage") != nil {
		t.Error("a stage the flow does not contain reported a gate")
	}
}
