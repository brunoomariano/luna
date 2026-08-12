// Package node runs the checks that prove a stage delivered.
//
// It is separate from the package that talks to herdr on purpose (ADR-0035):
// verification is not a herdr operation. herdr creates the worktree and hosts the
// agent; the check runs here, against a real exit code, so the evidence is the
// tool's answer rather than something recovered from a screen (INV-core-4).
//
// Nothing in here decides a transition. It observes, and the verdict travels
// inward inside an action (ADR-0024).
package node

import (
	"context"
	"errors"
	"fmt"
	"os/exec"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/brunoomariano/luna/src/internal/fsm"
)

// DefaultTimeout bounds a single verification command.
//
// Every external process gets a deadline. The study found herdr bounding its
// agent calls and forgetting its own git subprocesses, where one hang wedges a
// path permanently — the same mistake is available here, and this is the guard
// against it.
const DefaultTimeout = 10 * time.Minute

// Shell runs verification commands over what a stage delivered.
type Shell struct {
	// Dir is the task's worktree — where the agent worked, and the repository the
	// delivery is checked out from.
	Dir string

	// Timeout bounds one command. Zero means DefaultTimeout.
	Timeout time.Duration

	// OverWorkingTree runs the commands in Dir itself rather than over a checkout
	// of what was committed.
	//
	// It exists for the callers that have no delivery to check out — a test with a
	// directory that is not a repository, mainly — and never for a real run: a
	// verdict about the working tree is a verdict about uncommitted files, local
	// configuration and stale build output, which is the incoherence INV-core-4
	// exists to keep out of the log.
	OverWorkingTree bool
}

// Prove runs a verifier and reports what it observed.
//
// A verifier that executes nothing — existence, and anything like it — produces
// evidence without touching the filesystem: the artifact was delivered and that
// is the entire claim (ADR-0032).
func (s Shell) Prove(ctx context.Context, v fsm.Verifier, seq int) (fsm.Evidence, error) {
	command, ok := v.(fsm.Command)
	if !ok {
		return fsm.Evidence{Scope: v.Proves(), Verdict: fsm.VerdictPassed, RecordedAt: seq}, nil
	}

	// The command runs over what was delivered, not over the tree the agent worked
	// in (INV-core-4). A stage with nothing committed yet has no delivery to check
	// out, and then the working tree is all there is to verify.
	where := s.Dir
	if !s.OverWorkingTree {
		delivered, err := CheckoutDelivered(ctx, s.Dir)
		if err != nil {
			return fsm.Evidence{}, err
		}
		if delivered != nil {
			defer delivered.Close()
			where = delivered.Path
		}
	}

	exit, output, err := s.runIn(ctx, where, command.Run)
	if err != nil {
		// The command could not be run at all — no shell, no such directory, the
		// deadline hit. That is not a failing check, and recording it as one would
		// tell the audit the tests ran and lost.
		return fsm.Evidence{}, err
	}

	verdict := fsm.VerdictPassed
	if exit != 0 {
		verdict = fsm.VerdictFailed
	}
	return fsm.Evidence{
		Scope:      command.Proves(),
		Verdict:    verdict,
		Command:    command.Run,
		ExitCode:   exit,
		Detail:     summarise(output, verdict),
		RecordedAt: seq,
	}, nil
}

// run executes one command line and reports how it exited.
//
// `sh -c` rather than an argv: a contract will want pipes and `&&`, and the
// command comes from the project's own configuration rather than from a model
// (ADR-0035).
func (s Shell) runIn(ctx context.Context, dir, command string) (int, string, error) {
	timeout := s.Timeout
	if timeout <= 0 {
		timeout = DefaultTimeout
	}

	parent := ctx
	ctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	cmd := exec.CommandContext(ctx, "sh", "-c", command) //nolint:gosec // the command is project configuration
	cmd.Dir = dir

	out, err := cmd.CombinedOutput()

	// Neither cancellation is a verdict: the check did not finish, so there is
	// nothing to conclude about the work. Both are checked before the exit code,
	// because a killed command exits non-zero and reading that as "the tests
	// failed" would tell the audit they ran and lost.
	//
	// The two are kept apart so the error says which happened: "the check ran out
	// of time" and "someone stopped the run" are different facts, and one reported
	// as the other misleads whoever reads the block.
	if parent.Err() != nil {
		return 0, "", fmt.Errorf("%q was cancelled before it finished: %w", command, parent.Err())
	}
	if ctx.Err() != nil {
		return 0, "", fmt.Errorf("%q did not finish within %s", command, timeout)
	}

	// A non-zero exit is the answer, not a failure to ask.
	var exitErr *exec.ExitError
	if errors.As(err, &exitErr) {
		return exitErr.ExitCode(), string(out), nil
	}
	if err != nil {
		return 0, "", fmt.Errorf("running %q: %w", command, err)
	}
	return 0, string(out), nil
}

// How much of a failing command's output travels with the evidence.
//
// The tail rather than the head, because the reason a build failed is at the end
// and its banner is at the start. Bounded because a build log in an evidence
// record makes `luna task show` unreadable and bloats every replay of the task.
const (
	detailLines = 3
	detailChars = 300
)

// summarise keeps evidence readable.
//
// A passing check needs no detail — the exit code says it. A failing one is where
// someone will look, so it carries the tail of the output rather than the head.
func summarise(output string, verdict fsm.Verdict) string {
	trimmed := strings.TrimSpace(output)
	if trimmed == "" || verdict == fsm.VerdictPassed {
		return ""
	}

	lines := strings.Split(trimmed, "\n")
	if len(lines) > detailLines {
		lines = lines[len(lines)-detailLines:]
	}

	detail := strings.TrimSpace(strings.Join(lines, " · "))
	if len(detail) <= detailChars {
		return detail
	}

	// Cut on a rune boundary: slicing a string by byte index splits multi-byte
	// characters in half, and non-ASCII compiler output would leave broken UTF-8
	// in the log forever.
	cut := detailChars
	for cut > 0 && !utf8.RuneStart(detail[cut]) {
		cut--
	}
	return detail[:cut]
}
