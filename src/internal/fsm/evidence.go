package fsm

import "fmt"

// Scope is how much an evidence record actually proves.
//
// It exists because "one test passed" and "the suite is green" are different
// claims, and a log that cannot tell them apart lets the first quietly become the
// second (ADR-0028). The study found that laundering in every system that
// recorded evidence at all, and the one project that refused it said so in its
// own source: it "never upgrades targeted checks into repo green".
type Scope string

const (
	// ScopeFull is the whole check the artifact names: the suite, the build, the
	// pipeline.
	ScopeFull Scope = "full"

	// ScopeTargeted is a narrower run — one package, one test, one file. It is
	// real evidence, and it does not satisfy a requirement for the full one.
	ScopeTargeted Scope = "targeted"

	// ScopeExistence is the honest floor: the artifact was delivered and is in
	// the store, and nothing was verified about it. Prose has no exit code
	// (ADR-0032), and recording that as a passing check would be a lie the log
	// tells forever.
	ScopeExistence Scope = "existence"

	// ScopeHuman is a person's judgement, from a gate. It outranks any command
	// because someone looked, and it is kept distinct because who decided is
	// part of the audit.
	ScopeHuman Scope = "human"
)

// Satisfies reports whether evidence at this scope meets a requirement for the
// wanted one.
//
// The rule is deliberately one-directional: stronger evidence answers a weaker
// requirement, never the reverse. An existence record cannot stand in for a check
// that was supposed to run, which is the laundering the scopes exist to prevent —
// but a stage that ran the whole suite where only delivery was asked has proven
// more than it had to, and refusing that would be refusing good news.
//
// The ordering is human > full > targeted > existence. `human` outranks a command
// because a person looked; `existence` is the floor because it verified nothing.
// An unknown scope on either side refuses: evidence this build cannot rank
// proves nothing, and a requirement it cannot rank cannot be shown to be met.
// That is the same refusal the store makes for an unknown action — a log written
// by a newer version stops the replay rather than being read generously.
func (s Scope) Satisfies(wanted Scope) bool {
	have, known := scopeRank(s)
	if !known {
		return false
	}
	asked, known := scopeRank(wanted)
	if !known {
		return false
	}
	return have >= asked
}

// scopeRank orders the scopes by how much they prove, and reports whether the
// scope is one this build knows at all.
func scopeRank(s Scope) (int, bool) {
	switch s {
	case ScopeHuman:
		return 4, true
	case ScopeFull:
		return 3, true
	case ScopeTargeted:
		return 2, true
	case ScopeExistence:
		return 1, true
	default:
		return 0, false
	}
}

// Verdict is what the verification concluded.
type Verdict string

const (
	// VerdictPassed is a check that ran and succeeded.
	VerdictPassed Verdict = "passed"

	// VerdictFailed is a check that ran and did not succeed.
	VerdictFailed Verdict = "failed"

	// VerdictStale is a check that passed and was then invalidated by a later
	// edit to what it covered (ADR-0032).
	//
	// It is a status rather than a deletion because the audit should show that
	// the check ran and stopped counting, not that it never happened.
	VerdictStale Verdict = "stale"
)

// Evidence is what was observed about one delivered artifact.
//
// It is the verdict that arrives inside the action (ADR-0024): the node layer
// runs the real tool and reports, and the reducer decides what it means without
// running anything itself.
type Evidence struct {
	// Scope is how much this proves. Never upgraded after the fact.
	Scope Scope

	// Verdict is what the check concluded.
	Verdict Verdict

	// Command is what was run, verbatim, or empty for evidence that ran nothing.
	// An audit that says a stage closed but not on what grounds answers half the
	// question.
	Command string

	// ExitCode is what the command returned. Meaningless when Command is empty,
	// which is why Scope carries the real distinction.
	ExitCode int

	// Detail is a short human-readable summary: the failing test, the missing
	// file, the person's note on a gate adjustment.
	Detail string

	// RecordedAt is a monotonic sequence, not a clock — the position in the log
	// at which this was observed. It is what makes the staleness rule a pure
	// comparison rather than a call to time.Now (ADR-0024).
	RecordedAt int
}

// Passing reports whether this evidence currently counts as proof.
//
// Stale is not passing: that is the whole point of recording it separately
// instead of dropping it.
func (e Evidence) Passing() bool { return e.Verdict == VerdictPassed }

// Delivered reports whether the artifact exists at all, whatever the verdict.
//
// A failed check still means something was produced — that distinction is what
// lets a stage report "it built and the tests failed" rather than "nothing
// happened".
func (e Evidence) Delivered() bool { return e.Scope != "" }

// Exists is the evidence for an artifact that was delivered with nothing checked.
func Exists(seq int) Evidence {
	return Evidence{Scope: ScopeExistence, Verdict: VerdictPassed, RecordedAt: seq}
}

// Approved is the evidence for an artifact a person accepted at a gate.
//
// It is kept apart from a command's verdict because a human approval is a
// different kind of fact, and collapsing the two would let the audit claim a
// check ran where someone simply agreed.
func Approved(payload string, seq int) Evidence {
	return Evidence{
		Scope:      ScopeHuman,
		Verdict:    VerdictPassed,
		Detail:     payload,
		RecordedAt: seq,
	}
}

// String renders evidence for the CLI in one line.
func (e Evidence) String() string {
	if e.Command == "" {
		return fmt.Sprintf("%s (%s)", e.Verdict, e.Scope)
	}
	return fmt.Sprintf("%s (%s) %s → %d", e.Verdict, e.Scope, e.Command, e.ExitCode)
}
