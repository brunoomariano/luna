// Package node runs the checks that prove a stage delivered.
//
// Verification is deliberately not the agent's business: the agent is asked to
// deliver, and the check runs here, against a real exit code, so the evidence is
// the tool's answer rather than something the agent reported about itself.
//
// Nothing in here decides a transition. It observes, and the verdict travels
// inward inside an action.
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
// Every external process gets a deadline. The study found a tool that bounded its
// agent calls and forgot its own git subprocesses, where one hang wedges a
// path permanently — the same mistake is available here, and this is the guard
// against it.
const DefaultTimeout = 10 * time.Minute

// Shell runs verification commands over what a stage delivered.
type Shell struct {
	// Dir is where the delivery is checked out from. It is the repository, not
	// the stage's worktree, and the difference is the bug this field's meaning
	// changed to fix.
	//
	// Measured on the swarm bench: a stage stalled, its worktree was removed with
	// it — correctly, since a stage that ended has no tree — and every
	// retry then failed on `chdir ...: no such file` rather than on the work. The
	// delivery was fine and `make ci` was green on the agent's own commit.
	//
	// A commit outlives the tree that produced it, and the repository outlives
	// every stage. That is the same correction already made for the handoff and
	// for gate checks; this is the third caller to make it.
	Dir string

	// Commit is what to verify. Empty means the delivery is whatever the
	// repository's HEAD is, which is what a caller with no handoff to name has.
	//
	// It is named rather than discovered because discovering it meant reading
	// HEAD from the stage's worktree, and that is the tree that goes away.
	Commit string

	// Timeout bounds one command. Zero means DefaultTimeout.
	Timeout time.Duration

	// OverWorkingTree runs the commands in Dir itself rather than over a checkout
	// of what was committed.
	//
	// It exists for the callers that have no delivery to check out — a test with a
	// directory that is not a repository, mainly — and never for a real run: a
	// verdict about the working tree is a verdict about uncommitted files, local
	// configuration and stale build output, which is the incoherence INV-1
	// exists to keep out of the log.
	OverWorkingTree bool
}

// Prove runs a verifier and reports what it observed.
//
// A verifier that executes nothing and names no path produces evidence without
// touching anything: the artifact was delivered and that is the entire claim.
// An existence check *with* a path asks git instead — see provePath.
func (s Shell) Prove(ctx context.Context, v fsm.Verifier, seq int) (fsm.Evidence, error) {
	if existence, ok := v.(fsm.Existence); ok {
		if existence.Path == "" {
			return fsm.Evidence{Scope: v.Proves(), Verdict: fsm.VerdictPassed, RecordedAt: seq}, nil
		}
		return s.provePath(ctx, existence, seq)
	}

	command, ok := v.(fsm.Command)
	if !ok {
		return fsm.Evidence{Scope: v.Proves(), Verdict: fsm.VerdictPassed, RecordedAt: seq}, nil
	}

	// The command runs over what was delivered, not over the tree the agent worked
	// in. A stage with nothing committed yet has no delivery to check
	// out, and then the working tree is all there is to verify.
	where := s.Dir
	if !s.OverWorkingTree {
		delivered, err := CheckoutAt(ctx, s.Dir, s.Commit)
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

// provePath asks git whether the stage committed anything under the declared
// directory.
//
// The delivered commit rather than the worktree, for the reason every other check
// here uses it: the tree the agent worked in is removed when the stage ends, and
// verifying it is the incoherence INV-1 exists to prevent. `git ls-tree`
// reads the commit directly, so nothing has to be checked out to answer.
//
// A stage with no commit is not a failure. It is the first stage of the first
// task, before anything was delivered, and there is no tree to look in — the
// verdict falls back to what an undeclared path would have said, which is the
// agent's word.
func (s Shell) provePath(ctx context.Context, v fsm.Existence, seq int) (fsm.Evidence, error) {
	evidence := fsm.Evidence{
		Scope:      v.Proves(),
		Command:    v.Describe(),
		RecordedAt: seq,
	}
	if s.Commit == "" {
		evidence.Verdict = fsm.VerdictPassed
		evidence.Detail = "nothing delivered yet, so nothing to look in"
		return evidence, nil
	}

	found, err := git(ctx, s.Dir, "ls-tree", "-r", "--name-only", s.Commit, "--", v.Path)
	if err != nil {
		// The tree could not be read at all, which is not the artifact being
		// absent. Reporting it as absent would blame the agent for a broken
		// repository.
		return fsm.Evidence{}, fmt.Errorf("looking for %s in %s: %w", v.Path, short(s.Commit), err)
	}

	if strings.TrimSpace(found) == "" {
		evidence.Verdict = fsm.VerdictFailed
		evidence.Detail = fmt.Sprintf("%s committed nothing under %s", short(s.Commit), v.Path)
		return evidence, nil
	}

	evidence.Verdict = fsm.VerdictPassed
	evidence.Detail = strings.TrimSpace(found)
	return evidence, nil
}

// run executes one command line and reports how it exited.
//
// `sh -c` rather than an argv: a contract will want pipes and `&&`, and the
// command comes from the project's own configuration rather than from a model
// .
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
