package fsm

import (
	"strings"
	"testing"
)

// TestAGateDeclaringNothingIsTheMostCritical is what keeps this feature additive.
//
// Every stage in the shipped stock declares no criticality, so the undeclared
// case is the one that has to be safe: only the most autonomous knob reaches it,
// and the default knob of 0 reaches nothing at all.
func TestAGateDeclaringNothingIsTheMostCritical(t *testing.T) {
	gate := &GateSpec{Kind: GateConfirm, Reason: "approve the plan"}

	if got := gate.Resolved(); got != DefaultCriticality {
		t.Errorf("an undeclared criticality resolved to %d, want %d", got, DefaultCriticality)
	}

	for knob := 0; knob < DefaultCriticality; knob++ {
		if gate.AbsorbedBy(knob) {
			t.Errorf("knob %d absorbed a gate that declared no criticality", knob)
		}
	}
	if !gate.AbsorbedBy(DefaultCriticality) {
		t.Error("the highest knob did not absorb an undeclared gate")
	}
}

// TestTheKnobAbsorbsUpToTheDeclaredLevel covers the comparison the whole feature
// turns on.
func TestTheKnobAbsorbsUpToTheDeclaredLevel(t *testing.T) {
	gate := &GateSpec{Kind: GateReviewArtifact, Criticality: 7}

	for knob := 0; knob <= 6; knob++ {
		if gate.AbsorbedBy(knob) {
			t.Errorf("knob %d absorbed a gate of criticality 7", knob)
		}
	}
	for knob := 7; knob <= 10; knob++ {
		if !gate.AbsorbedBy(knob) {
			t.Errorf("knob %d did not absorb a gate of criticality 7", knob)
		}
	}
}

// TestKnobZeroJudgesNothing is the default, and the rollback.
//
// It has to hold for every gate at every declared level, including the lowest:
// zero is the floor of the knob's range precisely so that "judge nothing" is
// expressible, and a criticality of 1 absorbed by knob 0 would make it a lie.
func TestKnobZeroJudgesNothing(t *testing.T) {
	for level := 1; level <= DefaultCriticality; level++ {
		gate := &GateSpec{Kind: GateConfirm, Criticality: level}
		if gate.AbsorbedBy(0) {
			t.Errorf("knob 0 absorbed a gate of criticality %d", level)
		}
	}
}

// TestCriticalityIsParsedAndBounded covers the declaration in the stage file.
func TestCriticalityIsParsedAndBounded(t *testing.T) {
	accepted := map[string]int{"1": 1, "7": 7, "10": 10}
	for value, want := range accepted {
		stage, err := ParseStage(stageWithGate("criticality = "+value), "stage.toml")
		if err != nil {
			t.Fatalf("criticality %s was refused: %v", value, err)
		}
		if got := stage.Gate.Criticality; got != want {
			t.Errorf("criticality %s parsed as %d", value, got)
		}
	}

	// Zero is refused rather than read as "undeclared": writing it asks for a gate
	// every knob absorbs, including the one meant to judge nothing. Leaving the key
	// out is how a person says nothing, and that resolves to the opposite end.
	refused := []string{"0", "11", "-1", "high", "7.5", ""}
	for _, value := range refused {
		if _, err := ParseStage(stageWithGate("criticality = "+value), "stage.toml"); err == nil {
			t.Errorf("criticality %q was accepted", value)
		}
	}
}

// TestJudgementCriteriaSurviveTheirCommas is the bug an artifact-style list would
// have.
//
// A criterion is a sentence a person wrote, and sentences contain commas.
// Splitting on them — which is exactly what parseArtifacts does — would silently
// turn one criterion into two half-criteria, and the lead would be asked to
// judge against fragments.
func TestJudgementCriteriaSurviveTheirCommas(t *testing.T) {
	stage, err := ParseStage(stageWithGate(
		`judge = ["no test without a docstring, and no docstring without a test", "the second one"]`,
	), "stage.toml")
	if err != nil {
		t.Fatalf("parsing judgement criteria: %v", err)
	}

	if len(stage.Gate.Judge) != 2 {
		t.Fatalf("want 2 criteria, got %d: %q", len(stage.Gate.Judge), stage.Gate.Judge)
	}
	if !strings.Contains(stage.Gate.Judge[0], ", and no docstring") {
		t.Errorf("a criterion was cut at its comma: %q", stage.Gate.Judge[0])
	}
}

// TestAListMaySpanLines covers what the stage file needs to stay readable.
//
// Judgement criteria are prose, and a flow whose criteria are unreadable is one
// nobody maintains. The loader is otherwise line-oriented, so this is the one
// place it has to look further than the line it is on.
func TestAListMaySpanLines(t *testing.T) {
	stage, err := ParseStage(stageWithGate(`judge = [
  "The contract states what is required and what is forbidden",
  "Every acceptance criterion appears as an obligation",
]`), "stage.toml")
	if err != nil {
		t.Fatalf("parsing a multi-line list: %v", err)
	}

	if len(stage.Gate.Judge) != 2 {
		t.Fatalf("want 2 criteria across lines, got %d: %q",
			len(stage.Gate.Judge), stage.Gate.Judge)
	}
	if stage.Gate.Judge[1] != "Every acceptance criterion appears as an obligation" {
		t.Errorf("the second line did not survive: %q", stage.Gate.Judge[1])
	}
}

// TestAnUnclosedListIsRefused covers the half-loaded flow ParseStage refuses
// everywhere else.
//
// A judge block missing its ] would otherwise load with however many criteria
// preceded the mistake — a gate quietly judging against half its criteria, with
// nothing saying so.
func TestAnUnclosedListIsRefused(t *testing.T) {
	_, err := ParseStage(stageWithGate(`judge = [
  "the first criterion",
  "the second one"`), "stage.toml")

	if err == nil {
		t.Fatal("a list that never closed was accepted")
	}
	if !strings.Contains(err.Error(), "close with ]") {
		t.Errorf("the error does not say what is wrong: %v", err)
	}
}

// stageWithGate builds the smallest stage file that carries a gate, so each test
// above states only the line it is about.
func stageWithGate(gateLines string) string {
	return `id = "review"
role = "reviewer"
requires = ["task_id"]
produces_for_human = ["review_report"]

[gate]
kind = "confirm"
reason = "approve it"
` + gateLines + "\n"
}

// TestOneCriterionReadsAsSingular covers the single-entry list, which is the
// shape a person writes first.
func TestOneCriterionReadsAsSingular(t *testing.T) {
	stage, err := ParseStage(stageWithGate(`judge = ["the only criterion"]`), "stage.toml")
	if err != nil {
		t.Fatalf("parsing a single criterion: %v", err)
	}
	if len(stage.Gate.Judge) != 1 || stage.Gate.Judge[0] != "the only criterion" {
		t.Errorf("a single criterion did not survive: %q", stage.Gate.Judge)
	}
}

// TestAJudgeValueMustBeAList covers the value that is not a list at all.
//
// `judge = "one criterion"` is the mistake a person makes when they have exactly
// one, and accepting it would let the quotes decide the type — with the failure
// arriving later, at the gate, as criteria nobody can read.
func TestAJudgeValueMustBeAList(t *testing.T) {
	for _, value := range []string{`"a bare string"`, `7`} {
		if _, err := ParseStage(stageWithGate("judge = "+value), "stage.toml"); err == nil {
			t.Errorf("judge = %s was accepted as a list", value)
		}
	}

	// A list whose entries are not quoted declares nothing rather than being
	// malformed: there are no strings in it to find.
	stage, err := ParseStage(stageWithGate(`judge = [unquoted]`), "stage.toml")
	if err != nil {
		t.Fatalf("an unquoted list was refused: %v", err)
	}
	if len(stage.Gate.Judge) != 0 {
		t.Errorf("an unquoted list produced criteria: %q", stage.Gate.Judge)
	}
}
