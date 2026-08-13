package fsm

import "testing"

// TestABlockingFindingIsRead is the shape ADR-0041 specifies, in the form a
// reviewer actually writes it.
func TestABlockingFindingIsRead(t *testing.T) {
	report := `
# Review of LUNA-1

- [BLOCKING] B1: the retry budget is spent before the judge is consulted
- [SHOULD-FIX] S1: this name is misleading
- [NIT] N1: trailing whitespace
`

	findings := ReadReport(report)

	if len(findings) != 3 {
		t.Fatalf("read %d findings, want 3: %+v", len(findings), findings)
	}
	if findings[0].Severity != SeverityBlocking || findings[0].ID != "B1" {
		t.Errorf("first finding = %+v, want a blocking B1", findings[0])
	}
	if !Blocks(findings) {
		t.Error("a report with a blocking finding did not block")
	}
}

// TestAReportWithNothingBlockingLetsTheFlowCarryOn. `[SHOULD-FIX]` and `[NIT]`
// are worth doing and are not worth sending a task backwards for.
func TestAReportWithNothingBlockingLetsTheFlowCarryOn(t *testing.T) {
	findings := ReadReport(`
- [SHOULD-FIX] S1: extract this
- [NIT] N1: spelling
- [SHOULD-FIX] S2: and this
`)

	if len(findings) != 3 {
		t.Fatalf("read %d findings, want 3", len(findings))
	}
	if Blocks(findings) {
		t.Error("three non-blocking findings blocked — severity is the reviewer's " +
			"judgement per finding, and summing them overrules it by arithmetic")
	}
}

// TestUncertainDoesNotBlock. `[UNCERTAIN]` is a statement about confidence, not
// a severity between two others (ADR-0041). A reviewer that is unsure has not
// found a defect, and blocking on doubt would fire the loop ceilings on it.
func TestUncertainDoesNotBlock(t *testing.T) {
	findings := ReadReport("- [UNCERTAIN] U1: this may leak under load; a soak test would confirm")

	if len(findings) != 1 {
		t.Fatalf("read %d findings, want 1", len(findings))
	}
	if findings[0].Severity != SeverityUncertain {
		t.Errorf("severity = %q, want UNCERTAIN", findings[0].Severity)
	}
	if Blocks(findings) {
		t.Error("uncertainty blocked the flow")
	}
}

// TestTheTagSurvivesTheProseAroundIt. A reviewer writes prose, and prose arrives
// with bullets, numbering, indentation and bold. What must not be loose is the
// set of severities.
func TestTheTagSurvivesTheProseAroundIt(t *testing.T) {
	for _, line := range []string{
		"[BLOCKING] B1: it panics",
		"- [BLOCKING] B1: it panics",
		"  * [BLOCKING] B1 — it panics",
		"1. [BLOCKING] B1: it panics",
		"**[BLOCKING]** B1: it panics",
	} {
		findings := ReadReport(line)
		if len(findings) != 1 {
			t.Errorf("%q produced %d findings, want 1", line, len(findings))
			continue
		}
		if !Blocks(findings) {
			t.Errorf("%q did not block", line)
		}
	}
}

// TestAnUnknownTagIsNotAFinding is the direction that matters. A tag this cannot
// read must not become "not blocking" quietly — it becomes nothing, and the
// report stays in the context where a person can see what was written.
func TestAnUnknownTagIsNotAFinding(t *testing.T) {
	findings := ReadReport(`
- [CRITICAL] C1: this tag does not exist in the contract
- [blocking] b1: lowercase is a different tag
`)

	if len(findings) != 0 {
		t.Errorf("an unrecognised tag was read as a finding: %+v", findings)
	}
}

// TestAReportWithNoFindingsIsNotAnError. A reviewer that found nothing worth
// tagging says so in prose, and that is a passing review.
func TestAReportWithNoFindingsIsNotAnError(t *testing.T) {
	for _, report := range []string{
		"",
		"Looks good to me. Nothing to report.",
		"# Review\n\nI read the diff and the tests. No concerns.",
	} {
		findings := ReadReport(report)
		if len(findings) != 0 {
			t.Errorf("%q produced findings: %+v", report, findings)
		}
		if Blocks(findings) {
			t.Errorf("%q blocked", report)
		}
	}
}

// TestAFindingKeepsItsHandle. The id is what the conversation afterwards refers
// to, so it has to survive the parse (ADR-0041).
func TestAFindingKeepsItsHandle(t *testing.T) {
	findings := ReadReport("- [BLOCKING] B7: the emitter is missing")

	if len(findings) != 1 {
		t.Fatalf("read %d findings, want 1", len(findings))
	}
	if findings[0].ID != "B7" {
		t.Errorf("id = %q, want B7", findings[0].ID)
	}
	if findings[0].Text != "the emitter is missing" {
		t.Errorf("text = %q, want the finding without its id", findings[0].Text)
	}
}

// TestAFindingWithNoIdStillCounts. The id is a convenience for talking about it
// afterwards; the severity is what decides the transition, and a reviewer that
// omitted the handle has still found the defect.
func TestAFindingWithNoIdStillCounts(t *testing.T) {
	findings := ReadReport("- [BLOCKING] the tests do not cover the failure path")

	if len(findings) != 1 {
		t.Fatalf("read %d findings, want 1", len(findings))
	}
	if !Blocks(findings) {
		t.Error("a blocking finding with no id did not block")
	}
	if findings[0].Text != "the tests do not cover the failure path" {
		t.Errorf("text = %q — the finding lost its body", findings[0].Text)
	}
}

// TestTheReportIsFoundFromTheStagesOwnDeclaration. The four shipped review
// stages produce four differently named reports, so a hardcoded name would work
// for one and silently skip the rest (ADR-0049).
func TestTheReportIsFoundFromTheStagesOwnDeclaration(t *testing.T) {
	for _, id := range []StageID{"qa", "code-review", "harden", "architecture"} {
		stage := stageIn(DefaultFlow(), id)

		artifact, ok := ReviewedArtifact(stage)
		if !ok {
			t.Errorf("%s is a review stage with no readable report", id)
			continue
		}
		if artifact == "" {
			t.Errorf("%s named an empty artifact", id)
		}
	}
}

// TestAStageThatReviewsNothingHasNoReport. Only a review stage may send work
// back, so only a review stage has a report to read (INV-core-7).
func TestAStageThatReviewsNothingHasNoReport(t *testing.T) {
	build := stageIn(DefaultFlow(), "build")

	if _, ok := ReviewedArtifact(build); ok {
		t.Error("a stage that does not review reported a readable report")
	}
}
