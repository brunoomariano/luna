package verify

import "github.com/brunoomariano/luna/src/internal/contract"

// Verdict is what a check concluded.
type Verdict string

const (
	// VerdictPassed means the check ran and the artifact is proven, to the extent
	// its scope claims.
	VerdictPassed Verdict = "passed"

	// VerdictFailed means the check ran and the artifact is not proven. It is a
	// verdict about the work, never about the machine — a check that could not run
	// at all comes back as an error instead, because recording it as failed would
	// tell whoever reads the record that the tests ran and lost.
	VerdictFailed Verdict = "failed"
)

// Evidence is what one check observed.
//
// It carries the scope so a reader knows how much was proven, and it never
// carries more than what happened: an existence check that found a file says so
// and claims nothing about whether the file is any good.
type Evidence struct {
	// Artifact is what was being proven.
	Artifact string

	// Scope is how much a passing verdict establishes.
	Scope contract.Scope

	// Verdict is passed or failed.
	Verdict Verdict

	// Command is what ran, or how the check describes itself when nothing ran.
	Command string

	// ExitCode is what the command returned. Meaningless when nothing ran.
	ExitCode int

	// Detail is a short account, carried only when it helps: the tail of a failing
	// command's output, or what was found under a declared path.
	Detail string
}

// Passed reports whether every piece of evidence is a pass.
func Passed(all []Evidence) bool {
	for _, e := range all {
		if e.Verdict != VerdictPassed {
			return false
		}
	}
	return true
}

// Failures returns only the evidence that did not pass, in the order given.
func Failures(all []Evidence) []Evidence {
	var failed []Evidence
	for _, e := range all {
		if e.Verdict != VerdictPassed {
			failed = append(failed, e)
		}
	}
	return failed
}
