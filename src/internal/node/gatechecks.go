package node

import (
	"context"
	"fmt"

	"github.com/brunoomariano/luna/src/internal/fsm"
)

// CheckOutcome is what running one declared check concluded.
type CheckOutcome struct {
	// Command is what ran, verbatim. An audit that says a gate was answered but
	// not by what answers half the question.
	Command string

	// ExitCode is the answer. Zero passes; anything else rejects.
	ExitCode int

	// Output is what the command printed, for a person reading the rejection.
	Output string
}

// Passed reports whether this check answered yes.
func (c CheckOutcome) Passed() bool { return c.ExitCode == 0 }

// GateVerdict is what the mechanical half of a gate concluded.
//
// The three states are distinct because they go three different places
// (RFC-0006): passing approves the gate outright, a failure rejects it, and a
// check that could not run at all goes to a person with the results attached.
type GateVerdict struct {
	// Ran is every check that produced an exit code, in the order declared.
	Ran []CheckOutcome

	// Unrunnable is the error from a check that never produced an exit code — no
	// shell, a deadline, a cancelled run.
	//
	// It is kept apart from a non-zero exit for the reason INV-core-4 exists: a
	// command that was killed exits non-zero, and reading that as "the checks
	// failed" would tell the audit they ran and lost when nobody ever asked them.
	Unrunnable error
}

// Rejected reports whether any check that ran answered no.
func (v GateVerdict) Rejected() bool {
	for _, outcome := range v.Ran {
		if !outcome.Passed() {
			return true
		}
	}
	return false
}

// Approves reports whether the mechanical half answered the gate on its own.
//
// It requires at least one check: an empty declaration has approved nothing, and
// treating "nothing ran" as "everything passed" is the vacuous-truth bug that
// would let a typo in a metadata key silently open every gate.
func (v GateVerdict) Approves() bool {
	return v.Unrunnable == nil && len(v.Ran) > 0 && !v.Rejected()
}

// Failures is the checks that answered no, for the message a person reads.
func (v GateVerdict) Failures() []CheckOutcome {
	var failed []CheckOutcome
	for _, outcome := range v.Ran {
		if !outcome.Passed() {
			failed = append(failed, outcome)
		}
	}
	return failed
}

// CheckGate runs the commands declared for a gate over what was delivered.
//
// The checkout is the delivered commit, never the tree an agent worked in — the
// same rule verification already follows (INV-core-4), for the same reason: a
// verdict about the working tree is a verdict about uncommitted files and stale
// build output. The stage's own worktree is gone by now anyway; what survives a
// stage is the commit (ADR-0055), and the commit is reachable from the
// repository whatever tree produced it.
//
// It stops at the first failure. A gate is rejected by one failing command, so
// running the rest would spend minutes to reach a conclusion already reached —
// and the audit records what answered, which the first failure already is.
func (s Shell) CheckGate(ctx context.Context, checks []string) GateVerdict {
	if len(checks) == 0 {
		return GateVerdict{}
	}

	where, cleanup, err := s.gateCheckout(ctx)
	if err != nil {
		return GateVerdict{Unrunnable: err}
	}
	defer cleanup()

	verdict := GateVerdict{}
	for _, command := range checks {
		exit, output, err := s.runIn(ctx, where, command)
		if err != nil {
			verdict.Unrunnable = err
			return verdict
		}

		verdict.Ran = append(verdict.Ran, CheckOutcome{
			Command:  command,
			ExitCode: exit,
			Output:   output,
		})

		if exit != 0 {
			return verdict
		}
	}
	return verdict
}

// gateCheckout resolves where the checks run, and how to clean it up.
//
// A repository with nothing committed falls back to the working tree, which is
// the same fallback Prove makes and for the same reason: before the first
// delivery there is nothing else to check.
func (s Shell) gateCheckout(ctx context.Context) (where string, cleanup func(), err error) {
	if s.OverWorkingTree {
		return s.Dir, func() {}, nil
	}

	delivered, err := CheckoutDelivered(ctx, s.Dir)
	if err != nil {
		return "", nil, fmt.Errorf("checking out what was delivered: %w", err)
	}
	if delivered == nil {
		return s.Dir, func() {}, nil
	}
	return delivered.Path, delivered.Close, nil
}

// Evidence turns the mechanical verdict into what the log records.
//
// The scope is the command's, not the gate's: these are real commands with real
// exit codes, so they carry the same weight as any other check that ran. Which
// of full or targeted is the caller's to say — a gate declaring `make ci` proved
// more than one declaring `go test ./internal/fsm`, and only the person who
// wrote them knows which.
func (v GateVerdict) Evidence(scope fsm.Scope, seq int) fsm.Evidence {
	evidence := fsm.Evidence{Scope: scope, Verdict: fsm.VerdictPassed, RecordedAt: seq}

	if failed := v.Failures(); len(failed) > 0 {
		evidence.Verdict = fsm.VerdictFailed
		evidence.Command = failed[0].Command
		evidence.ExitCode = failed[0].ExitCode
		evidence.Detail = failed[0].Output
		return evidence
	}

	if len(v.Ran) > 0 {
		last := v.Ran[len(v.Ran)-1]
		evidence.Command = last.Command
		evidence.ExitCode = last.ExitCode
	}
	return evidence
}
