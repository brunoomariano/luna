package fsm

import "testing"

// TestAVerdictIsAWordAndNotAParagraph. The agent reports and Luna decides, so
// what carries the decision has to be something a parser can be sure of — a
// paragraph concluding that the work is done decides nothing.
func TestAVerdictIsAWordAndNotAParagraph(t *testing.T) {
	for report, want := range map[string]LoopOutcome{
		"VERDICT: converged":                          OutcomeConverged,
		"lots of prose\n\nVERDICT: progressed\nmore":  OutcomeProgressed,
		"  VERDICT:  regressed  ":                     OutcomeRegressed,
		"VERDICT: STUCK":                              OutcomeStuck,
		"I judge this round to have converged nicely": "",
		"VERDICT: nearly":                             "",
		"":                                            "",
	} {
		if got := ReadVerdict(report); got != want {
			t.Errorf("%q read as %q, want %q", report, got, want)
		}
	}
}

// TestTheLastVerdictIsTheOneItMeant. An agent that reconsiders mid-report has
// said two things, and taking the first would freeze a conclusion it walked back.
func TestTheLastVerdictIsTheOneItMeant(t *testing.T) {
	report := "VERDICT: progressed\n\non reflection the suite is green\n\nVERDICT: converged"

	if got := ReadVerdict(report); got != OutcomeConverged {
		t.Errorf("the report was read as %q, want the verdict it ended on", got)
	}
}

// TestAStageOnlyRecordsWhatItsContractAllows is the guard on facts.
//
// A stage that could record any fact could switch on a conditional stage from
// anywhere in the flow — so what a stage may find out is part of its contract,
// like what it may produce.
func TestAStageOnlyRecordsWhatItsContractAllows(t *testing.T) {
	report := "FACT: triaged_bug\nFACT: touches_structure"

	allowed := ReadFacts(report, []Fact{TriagedBug})

	if len(allowed) != 1 || allowed[0] != TriagedBug {
		t.Errorf("a stage recorded a fact it was not allowed to: %v", allowed)
	}
}

// TestAFactIsRecordedOnceHoweverOftenItIsSaid. A report that repeats itself is
// still one conclusion.
func TestAFactIsRecordedOnceHoweverOftenItIsSaid(t *testing.T) {
	report := "FACT: triaged_bug\n\nto be clear:\n\nFACT: triaged_bug"

	if found := ReadFacts(report, []Fact{TriagedBug}); len(found) != 1 {
		t.Errorf("one conclusion was read %d times", len(found))
	}
}

// TestAReportWithNoConclusionRecordsNothing, which is the honest answer both for
// a stage that concluded nothing and for one that wrote something unreadable. The
// second is visible, because the report is in the context.
func TestAReportWithNoConclusionRecordsNothing(t *testing.T) {
	for _, report := range []string{
		"",
		"nothing is broken here, it is simply missing",
		"FACT: something_else",
		"FACT:triaged_bug and more on the line",
	} {
		if found := ReadFacts(report, []Fact{TriagedBug}); len(found) != 0 {
			t.Errorf("%q recorded %v", report, found)
		}
	}
}
