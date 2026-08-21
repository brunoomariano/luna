package fsm

import (
	"regexp"
	"strings"
)

// Severity is the tag a review finding carries. Exactly one per finding.
type Severity string

const (
	// SeverityBlocking is the only one that sends work back.
	SeverityBlocking Severity = "BLOCKING"

	// SeverityShouldFix is worth doing and does not stop the flow.
	SeverityShouldFix Severity = "SHOULD-FIX"

	// SeverityNit is a preference.
	SeverityNit Severity = "NIT"

	// SeverityUncertain is a statement about confidence rather than a severity
	// between two others, and it must say what would confirm it.
	//
	// It does not send work back. A reviewer that is unsure has not found a
	// defect, and treating "I might be wrong" as blocking would make the loop
	// ceilings fire on doubt.
	SeverityUncertain Severity = "UNCERTAIN"
)

// KnownSeverities is every tag a finding may carry.
//
// It exists so a caller can ask rather than repeat the list. The one that made
// it necessary is the check that the critic's brief teaches all of them: written
// with the names inline, that test would keep passing when a severity is added
// and never taught — which is the exact failure it was written for, one level up.
func KnownSeverities() []Severity {
	return []Severity{SeverityBlocking, SeverityShouldFix, SeverityNit, SeverityUncertain}
}

// Finding is one entry in a review report.
type Finding struct {
	// ID is the handle for the conversation afterwards — B1, S2, N1.
	ID string

	Severity Severity

	// Text is what the finding says, for a person to read.
	Text string
}

// findingLine matches a tagged finding: an optional id, a tag in brackets, and
// the rest of the line.
//
// Deliberately permissive about what surrounds the tag and strict about the tag
// itself. A reviewer writes prose, and prose arrives with bullets, numbering and
// indentation; what must not be loose is which severities exist, because an
// unrecognised tag that fell through to "not blocking" would be a defect the
// flow never hears about.
var findingLine = regexp.MustCompile(`(?m)^[\s\-\*\d\.\)]*(?:\*\*)?\[(BLOCKING|SHOULD-FIX|NIT|UNCERTAIN)\](?:\*\*)?\s*(?:(\w+\d+)[\s:\-]+)?(.*)$`)

// ReadReport pulls the findings out of a review report.
//
// This is the piece the review contract specifies and nothing implemented: the
// reviewer produces a report like any other artifact, and **Luna reads it**. The
// agent never emits the transition — it reports, and the code decides. That is
// flow control staying out of the model, applied to the one place where letting
// the model decide would look most reasonable, because the model has just
// finished forming an opinion and the obvious next step is to let it act on one.
//
// A report with no recognisable finding yields nothing, which is the honest
// answer: it means the reviewer found nothing worth tagging, or wrote something
// this cannot read. Both let the flow carry on, and the second is visible
// because the report is in the context for a person to look at.
func ReadReport(report string) []Finding {
	matches := findingLine.FindAllStringSubmatch(report, -1)
	findings := make([]Finding, 0, len(matches))

	for _, match := range matches {
		findings = append(findings, Finding{
			ID:       match[2],
			Severity: Severity(match[1]),
			Text:     strings.TrimSpace(match[3]),
		})
	}
	return findings
}

// Blocks reports whether a set of findings sends the work back.
//
// One blocking finding is enough, and nothing else counts. A pile of
// `[SHOULD-FIX]` is not a `[BLOCKING]`: severity is the reviewer's judgement
// expressed once per finding, and summing them would let the engine overrule it
// by arithmetic.
func Blocks(findings []Finding) bool {
	for _, f := range findings {
		if f.Severity == SeverityBlocking {
			return true
		}
	}
	return false
}

// ReviewedArtifact is the report a review stage produces.
//
// Read from the stage's own declaration rather than a name the engine keeps: the
// four shipped review stages produce `qa_report`, `review_report`,
// `mutation_report` and an architecture assessment, and a project's flow may
// name its own. A hardcoded name would work for one stage and silently skip the
// rest — which is the shape of bug that moving gates and reviews onto the stage
// avoids.
//
// The report is the stage's human-facing product. That is what makes it the
// right field: a review's output is read by a person and by this, and satisfies
// nobody's Requires (INV-3).
func ReviewedArtifact(stage Stage) (Artifact, bool) {
	if stage.Review == nil || len(stage.ProducesForHuman) == 0 {
		return "", false
	}
	return stage.ProducesForHuman[0], true
}
