package verify

import (
	"context"
	"errors"
	"fmt"
	"os/exec"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/brunoomariano/luna/src/internal/contract"
)

// DefaultTimeout bounds a single verification command.
//
// Every external process gets a deadline. A study of comparable tools found one
// that bounded its agent calls and forgot its own git subprocesses, where a
// single hang wedges a path permanently — the same mistake is available here, and
// this is the guard against it.
const DefaultTimeout = 10 * time.Minute

// Shell runs a contract's checks over what a phase delivered.
type Shell struct {
	// Dir is the repository the delivery is checked out from. It is the
	// repository, not the phase's worktree, and the difference is a bug this
	// field's meaning changed to fix.
	//
	// Measured on the swarm bench: a phase stalled, its worktree was removed with
	// it — correctly, since a phase that ended has no tree — and every retry then
	// failed on `chdir ...: no such file` rather than on the work. The delivery
	// was fine and the gate was green on the agent's own commit.
	//
	// A commit outlives the tree that produced it, and the repository outlives
	// every phase.
	Dir string

	// Commit is what to verify. Empty means the delivery is whatever the
	// repository's HEAD is, which is what a caller with no handoff to name has.
	//
	// It is named rather than discovered because discovering it meant reading HEAD
	// from the phase's worktree, and that is the tree that goes away.
	Commit string

	// Base is the commit the phase started from, when the caller knows it.
	//
	// It is what tells "delivered nothing" apart from "delivered something": a
	// phase that adds no commit hands back the base, which exists and resolves, so
	// a check over it passes on code the phase was given. Empty leaves that
	// distinction unavailable and the check runs, which is the cautious direction.
	Base string

	// Timeout bounds one command. Zero means DefaultTimeout.
	Timeout time.Duration

	// OverWorkingTree runs the commands in Dir itself rather than over a checkout
	// of what was committed.
	//
	// It is for a caller with no delivery to check out — a directory that is not a
	// repository — and never for a real run: a verdict about the working tree is a
	// verdict about uncommitted files, local configuration and stale build output,
	// which is the incoherence INV-1 exists to keep out.
	OverWorkingTree bool
}

// Prove runs every check a contract declares and reports what each observed.
//
// The evidence comes back in the contract's own order so two runs of one contract
// report identically, and every owed artifact appears — including the ones that
// passed, because a report listing only failures cannot be told from one where
// nothing ran.
func (s Shell) Prove(ctx context.Context, c contract.Contract) ([]Evidence, error) {
	owed := c.Owed()
	all := make([]Evidence, 0, len(owed))

	// One checkout for the whole contract rather than one per command: cutting a
	// worktree per artifact would pay the same checkout several times to look at
	// the same delivery.
	where, undelivered, delivery, err := s.whereToRun(ctx)
	if err != nil {
		return nil, err
	}
	if delivery != nil {
		defer delivery.Close()
	}

	for _, artifact := range owed {
		verifier, _ := c.VerifierFor(artifact)
		evidence, err := s.proveOne(ctx, artifact, verifier, where, undelivered)
		if err != nil {
			return nil, err
		}
		all = append(all, evidence)
	}
	return all, nil
}

// proveOne runs a single verifier.
//
// A verifier that executes nothing and names no path produces evidence without
// touching anything: the artifact was delivered and that is the entire claim. An
// existence check *with* a path asks git instead.
func (s Shell) proveOne(ctx context.Context, artifact string, v contract.Verifier, where string, undelivered bool) (Evidence, error) {
	if existence, ok := v.(contract.Existence); ok {
		if existence.Path == "" {
			return Evidence{
				Artifact: artifact,
				Scope:    v.Proves(),
				Verdict:  VerdictPassed,
				Command:  v.Describe(),
			}, nil
		}
		return s.provePath(ctx, artifact, existence)
	}

	command, ok := v.(contract.Command)
	if !ok {
		return Evidence{
			Artifact: artifact,
			Scope:    v.Proves(),
			Verdict:  VerdictPassed,
			Command:  v.Describe(),
		}, nil
	}

	// A phase that owes a command-proven artifact and committed nothing has not
	// delivered, and the honest verdict is that it failed.
	//
	// This used to fall through to the checkout, which reads "" as "HEAD" and
	// resolves it against the main repository. So the command ran over whatever
	// was already committed there and exited zero. Measured on TALLY-3: a build
	// phase was billed $0.56 and recorded `make test → 0` while its branch sat on
	// the base commit with a clean worktree. The green was true, and it was true
	// about code the phase did not write.
	//
	// The existence path has always drawn this line; the command path did not, and
	// the asymmetry between two verifiers looking at the same missing commit was
	// the whole defect. Existence answers "passed" there because an artifact
	// nobody delivered yet has nowhere to be looked for — no claim is made. Here a
	// claim would be made, about a tree that is not the delivery.
	if undelivered {
		return Evidence{
			Artifact: artifact,
			Scope:    command.Proves(),
			Verdict:  VerdictFailed,
			Command:  command.Run,
			Detail:   s.undeliveredDetail(),
		}, nil
	}

	exit, output, err := s.runIn(ctx, where, command.Run)
	if err != nil {
		// The command could not be run at all — no shell, no such directory, the
		// deadline hit. That is not a failing check, and recording it as one would
		// tell whoever reads the record that the tests ran and lost.
		return Evidence{}, err
	}

	verdict := VerdictPassed
	if exit != 0 {
		verdict = VerdictFailed
	}
	return Evidence{
		Artifact: artifact,
		Scope:    command.Proves(),
		Verdict:  verdict,
		Command:  command.Run,
		ExitCode: exit,
		Detail:   summarise(output, verdict),
	}, nil
}

// whereToRun answers where commands should run, and whether there is anywhere at
// all.
//
// `undelivered` means the phase delivered nothing on top of a base it had, which
// is a failed delivery rather than a broken machine. The returned checkout, when
// there is one, is the caller's to close.
//
// Two situations answer "no commit" and only one of them is the phase's fault. A
// repository with no commit at all is the first phase of the first run: there is
// nothing to have delivered, the working tree is all there is, and refusing would
// block work for not committing what it was never asked to. A repository that
// *has* commits and a phase that added none is the defect.
//
// A Dir that is not a repository is neither: that caller is checking a plain
// directory on purpose, and refusing would report a broken machine as a failed
// delivery.
func (s Shell) whereToRun(ctx context.Context) (where string, undelivered bool, checkout *Delivered, err error) {
	if s.OverWorkingTree {
		return s.Dir, false, nil, nil
	}
	if s.Commit == "" {
		if s.hasSomethingToBuildOn(ctx) {
			return "", true, nil, nil
		}
		return s.Dir, false, nil, nil
	}

	// A delivery that is the base is not a delivery. The phase was handed that
	// commit and handed it straight back, so running the check over it proves what
	// the *previous* phase delivered — and passes, because that code was already
	// green when this phase received it. Measured on TALLY-5: a build phase was
	// billed $0.64 over 17 turns, ended clean at its base with none of the feature
	// written, and closed its check as passed.
	if s.Base != "" && s.Commit == s.Base {
		return "", true, nil, nil
	}

	delivered, err := CheckoutAt(ctx, s.Dir, s.Commit)
	if err != nil {
		return "", false, nil, err
	}
	if delivered == nil {
		return s.Dir, false, nil, nil
	}
	return delivered.Path, false, delivered, nil
}

// hasSomethingToBuildOn reports whether Dir is a repository that already has a
// commit.
//
// Anything that is not a clear yes answers no, because the cautious direction is
// to run the command rather than to refuse it: a false yes blocks work that did
// nothing wrong, while a false no only costs the check it would have refused.
func (s Shell) hasSomethingToBuildOn(ctx context.Context) bool {
	_, err := git(ctx, s.Dir, "rev-parse", "--verify", "HEAD^{commit}")
	return err == nil
}

// provePath asks git whether the phase committed anything under the declared
// directory.
//
// The delivered commit rather than the worktree, for the reason every other check
// here uses it. `git ls-tree` reads the commit directly, so nothing has to be
// checked out to answer.
//
// A phase with no commit is not a failure. It is the first phase of the first
// run, before anything was delivered, and there is no tree to look in — the
// verdict falls back to what an undeclared path would have said.
func (s Shell) provePath(ctx context.Context, artifact string, v contract.Existence) (Evidence, error) {
	evidence := Evidence{
		Artifact: artifact,
		Scope:    v.Proves(),
		Command:  v.Describe(),
	}
	if s.Commit == "" {
		evidence.Verdict = VerdictPassed
		evidence.Detail = "nothing delivered yet, so nothing to look in"
		return evidence, nil
	}

	found, err := git(ctx, s.Dir, "ls-tree", "-r", "--name-only", s.Commit, "--", v.Path)
	if err != nil {
		// The tree could not be read at all, which is not the artifact being
		// absent. Reporting it as absent would blame the agent for a broken
		// repository.
		return Evidence{}, fmt.Errorf("looking for %s in %s: %w", v.Path, short(s.Commit), err)
	}

	if strings.TrimSpace(found) == "" {
		evidence.Verdict = VerdictFailed
		evidence.Detail = fmt.Sprintf("%s committed nothing under %s", short(s.Commit), v.Path)
		return evidence, nil
	}

	evidence.Verdict = VerdictPassed
	evidence.Detail = strings.TrimSpace(found)
	return evidence, nil
}

// runIn executes one command line and reports how it exited.
//
// `sh -c` rather than an argv: a contract will want pipes and `&&`, and the
// command comes from the project's own configuration rather than from a model.
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
	// failed" would tell the record they ran and lost.
	//
	// The two are kept apart so the error says which happened: "the check ran out
	// of time" and "someone stopped the run" are different facts.
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
// and its banner is at the start. Bounded because a build log in a ledger line
// makes the whole record unreadable.
const (
	detailLines = 3
	detailChars = 300
)

// summarise keeps evidence readable.
//
// A passing check needs no detail — the exit code says it. A failing one is where
// someone will look, so it carries the tail of the output rather than the head.
func summarise(output string, verdict Verdict) string {
	trimmed := strings.TrimSpace(output)
	if trimmed == "" || verdict == VerdictPassed {
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
	// in the record forever.
	cut := detailChars
	for cut > 0 && !utf8.RuneStart(detail[cut]) {
		cut--
	}
	return detail[:cut]
}

// undeliveredDetail says which way the delivery was missing.
//
// The two read differently to whoever finds them: a phase with no commit at all
// never wrote anything down, while a phase whose commit is its own base ran,
// spent, and added nothing on top.
func (s Shell) undeliveredDetail() string {
	if s.Commit == "" {
		return "the phase committed nothing, so there is no delivery to run it over"
	}
	return "the phase committed nothing on top of " + short(s.Base) + ", so there is no delivery to run it over"
}
