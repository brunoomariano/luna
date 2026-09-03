// Package verify runs the checks that prove a phase delivered.
//
// Verification is deliberately not the agent's business: the agent is asked to
// deliver, and the check runs here, against a real exit code, so the evidence is
// the tool's answer rather than something the agent reported about itself.
//
// Nothing in here decides anything. It observes and reports, and what to do with
// the verdict belongs to whoever conducts.
package verify

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"
)

// gitTimeout bounds the git commands that build a delivery checkout. They are
// local and fast; a minute is generous and still bounded, because every external
// process here gets a deadline.
const gitTimeout = time.Minute

// Delivered is a checkout of what a phase committed, separate from the tree the
// agent worked in.
//
// It exists because a verdict has to describe the delivery. An agent's worktree
// holds uncommitted files, a local `.env`, a stale build artifact, a test edited
// and never committed — so a suite that passes there says nothing about what the
// phase actually handed over. Measured across coding agents, that incoherence is
// the dominant way a green check turns out to be wrong; deliberate sabotage is
// rarer and is not what this addresses.
//
// It is not containment. An agent that controls what it commits controls what is
// verified, and confining the process belongs to the layer a person chose before
// the agent started.
type Delivered struct {
	// Path is the checkout, valid until Close.
	Path string

	// Ref is what was checked out, kept for the evidence to name.
	Ref string

	cleanup func()
}

// Close removes the checkout. Safe to call more than once.
func (d *Delivered) Close() {
	if d == nil || d.cleanup == nil {
		return
	}
	d.cleanup()
	d.cleanup = nil
}

// CheckoutAt makes a throwaway worktree at a named commit, cut from a repository.
//
// An empty commit means "whatever this repository's HEAD is", which is what a
// caller with no handoff to name has.
//
// Naming the commit is what lets the checkout be cut from the repository rather
// than from the phase's worktree. The repository outlives every phase; a worktree
// is removed when its phase ends, and a verification that resolved HEAD from it
// failed on a missing directory the moment a phase was retried — measured on the
// swarm bench, on work that was itself green.
func CheckoutAt(ctx context.Context, repo, commit string) (*Delivered, error) {
	if commit == "" {
		commit = "HEAD"
	}

	resolved, err := git(ctx, repo, "rev-parse", "--verify", commit+"^{commit}")
	if err != nil {
		// Nothing to check out. Before the first delivery that is a state rather
		// than a failure, and the caller falls back to the working tree.
		return nil, nil //nolint:nilnil // "no delivery yet" is not an error
	}

	dir, err := os.MkdirTemp("", "luna-delivered-")
	if err != nil {
		return nil, fmt.Errorf("making room for a checkout of %s: %w", short(resolved), err)
	}
	path := filepath.Join(dir, "tree")

	// With the project's hooks off. This checkout exists to run one command in and
	// delete; a project's `post-checkout` firing on it is doing work nobody asked
	// for, against a tree that will not exist in a moment.
	//
	// It is also a real failure mode rather than a tidiness argument. Measured on
	// the swarm bench: a tracker's `init` set core.hooksPath and installed a
	// post-checkout calling a mise shim; inside a sandbox with a tmpfs $HOME mise
	// cannot resolve the shim, and `git worktree add` exits 1 having created the
	// worktree anyway. Reading that exit code blocked a phase that had delivered.
	//
	// --detach is what keeps it out of trouble: the checkout belongs to no branch,
	// so it cannot be mistaken for a phase's own worktree and cannot be committed
	// to by accident.
	if _, err := git(ctx, repo, "-c", "core.hooksPath=/dev/null",
		"worktree", "add", "--detach", path, resolved); err != nil {
		_ = os.RemoveAll(dir)
		return nil, fmt.Errorf("checking out %s: %w", short(resolved), err)
	}

	return &Delivered{
		Path: path,
		Ref:  short(resolved),
		cleanup: func() {
			// Remove the registration before the directory: a worktree deleted from
			// disk without git being told leaves a prunable entry in the repository
			// that outlives the run.
			//
			// Detached from the caller's context on purpose — cleanup has to run
			// even when the reason for cleaning up is that the context was
			// cancelled.
			done, stop := context.WithTimeout(context.Background(), gitTimeout)
			defer stop()
			_, _ = git(done, repo, "worktree", "remove", "--force", path)
			_ = os.RemoveAll(dir)
		},
	}, nil
}

// git runs one command in a repository and returns its trimmed output.
func git(ctx context.Context, dir string, args ...string) (string, error) {
	ctx, cancel := context.WithTimeout(ctx, gitTimeout)
	defer cancel()

	cmd := exec.CommandContext(ctx, "git", args...) //nolint:gosec // fixed subcommands
	cmd.Dir = dir

	out, err := cmd.CombinedOutput()
	if err != nil {
		return "", fmt.Errorf("git %s: %w: %s", strings.Join(args, " "), err, strings.TrimSpace(string(out)))
	}
	return strings.TrimSpace(string(out)), nil
}

// short abbreviates a commit for a message, keeping the full one out of prose
// nobody reads to the end.
func short(sha string) string {
	if len(sha) > 12 {
		return sha[:12]
	}
	return sha
}

// HeadOf resolves a repository's current commit.
//
// It is exported because a caller that did not name a commit still means "what
// is here now", and letting them pass "" instead would collide with the way an
// empty commit reads to Prove: nothing was delivered.
func HeadOf(ctx context.Context, repo string) (string, error) {
	return git(ctx, repo, "rev-parse", "--verify", "HEAD^{commit}")
}
