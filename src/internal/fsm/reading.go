package fsm

import (
	"regexp"
	"strings"
)

// verdictLine is how a converging stage says what its round produced.
//
// One word on a line of its own, in the same shape a finding's severity takes —
// there is one way to say a thing in a report rather than two.
var verdictLine = regexp.MustCompile(`(?m)^\s*VERDICT:\s*([A-Za-z-]+)\s*$`)

// factLine is how a stage says what it concluded about the task.
var factLine = regexp.MustCompile(`(?m)^\s*FACT:\s*([a-z_]+)\s*$`)

// ReadVerdict pulls a round's outcome out of a report.
//
// The same division as ReadReport, and for the same reason: the agent reports and
// **Luna decides**. What the model is uniquely able to say is whether the round
// attacked the cause or traded one problem for another — no exit code tells those
// apart. What it does not get to do is act on that: it writes a word, and the
// engine turns the word into a transition or refuses to.
//
// The last verdict wins. An agent that reconsiders mid-report has said two
// things, and the one it ended on is the one it meant — where taking the first
// would freeze a conclusion it walked back.
//
// An empty outcome is a report with nothing recognisable in it, which the caller
// treats as "no round was judged" rather than guessing at one.
func ReadVerdict(report string) LoopOutcome {
	matches := verdictLine.FindAllStringSubmatch(report, -1)
	if len(matches) == 0 {
		return ""
	}

	written := strings.ToLower(strings.TrimSpace(matches[len(matches)-1][1]))
	for _, known := range KnownOutcomes() {
		if LoopOutcome(written) == known {
			return known
		}
	}
	// A word nothing recognises is not a verdict. Guessing the nearest one would
	// let a typo decide where the flow goes.
	return ""
}

// ReadFacts pulls the conclusions out of a report, keeping only those the stage
// was allowed to reach.
//
// Bounded by `allowed` rather than by the closed set of facts, because those are
// two different questions: `ParseFact` asks whether Luna knows the name, and this
// asks whether *this stage* was the one to decide it. A stage that could record
// any fact could turn a conditional stage on from anywhere in the flow.
//
// Named once each, in the order the report says them, so a report repeating
// itself does not record the same conclusion twice.
func ReadFacts(report string, allowed []Fact) []Fact {
	permitted := make(map[Fact]bool, len(allowed))
	for _, fact := range allowed {
		permitted[fact] = true
	}

	seen := map[Fact]bool{}
	var found []Fact
	for _, match := range factLine.FindAllStringSubmatch(report, -1) {
		fact := Fact(strings.TrimSpace(match[1]))
		if !permitted[fact] || seen[fact] {
			continue
		}
		seen[fact] = true
		found = append(found, fact)
	}
	return found
}
