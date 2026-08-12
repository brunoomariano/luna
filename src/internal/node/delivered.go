package node

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

// Delivered is a checkout of what a stage committed, separate from the tree the
// agent worked in.
//
// It exists because a verdict has to describe the delivery. An agent's worktree
// holds uncommitted files, a local `.env`, a stale build artifact, a test edited
// and never committed — so a suite that passes there says nothing about what the
// stage actually handed over. Measured across coding agents, that incoherence is
// the dominant way a green check turns out to be wrong; deliberate sabotage is
// rarer and is not what this addresses (INV-core-4).
//
// It is not containment. An agent that controls what it commits controls what is
// verified, and confining the process is a sandbox's job — Luna delegates that
// rather than building it (INV-core-7).
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

// CheckoutDelivered makes a throwaway worktree at the stage's current commit.
//
// `git worktree add --detach` rather than a clone: it shares the object database,
// so it costs a checkout rather than a copy of the history, and it is the same
// mechanism Luna already uses for a task's own tree (ADR-0037).
//
// A repository with no commit yet is not an error — it is the first stage of the
// first task, before anything was delivered. The caller gets nil and falls back to
// verifying the working tree, which is all there is to verify.
func CheckoutDelivered(ctx context.Context, worktree string) (*Delivered, error) {
	head, err := git(ctx, worktree, "rev-parse", "--verify", "HEAD^{commit}")
	if err != nil {
		// No commit means nothing has been delivered yet, which is a state rather
		// than a failure.
		return nil, nil //nolint:nilnil // "no delivery yet" is not an error
	}

	dir, err := os.MkdirTemp("", "luna-delivered-")
	if err != nil {
		return nil, fmt.Errorf("making room for the delivery checkout: %w", err)
	}
	path := filepath.Join(dir, "tree")

	// --detach: this checkout belongs to no branch, so it cannot be confused for
	// the task's own worktree and cannot be committed to by accident.
	if _, err := git(ctx, worktree, "worktree", "add", "--detach", path, head); err != nil {
		_ = os.RemoveAll(dir)
		return nil, fmt.Errorf("checking out the delivery at %s: %w", short(head), err)
	}

	return &Delivered{
		Path: path,
		Ref:  short(head),
		cleanup: func() {
			// Remove the registration before the directory: a worktree deleted from
			// disk without git being told leaves a prunable entry in the repository
			// that outlives the task.
			//
			// Detached from the caller's context on purpose — cleanup has to run
			// even when the reason for cleaning up is that the context was
			// cancelled.
			done, stop := context.WithTimeout(context.Background(), gitTimeout)
			defer stop()
			_, _ = git(done, worktree, "worktree", "remove", "--force", path)
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
