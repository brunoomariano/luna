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
	return CheckoutAt(ctx, worktree, "")
}

// CheckoutAt makes a throwaway worktree at a named commit, cut from a repository.
//
// An empty commit means "whatever this repository's HEAD is", which is what
// CheckoutDelivered asks for and what a caller with no handoff to name has.
//
// Naming the commit is what lets the checkout be cut from the repository rather
// than from the stage's worktree. The repository outlives every stage; the
// worktree is removed when the stage ends (ADR-0055), and a verification that
// resolved HEAD from it failed on a missing directory the moment a stage was
// retried — measured on the swarm bench, on work that was itself green.
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

	return throwawayCheckout(ctx, repo, resolved, "luna-delivered-")
}

// throwawayCheckout makes a detached worktree at a commit, and knows how to
// remove it again.
//
// Shared by the delivery check and the merge check because they want exactly the
// same thing: a tree at a known commit, belonging to no branch, gone afterwards
// whatever happened. The two grew independently and were identical but for the
// prefix, which is a duplicate of the cleanup rule — the part it is expensive to
// get wrong twice.
//
// --detach is what keeps it out of trouble: the checkout belongs to no branch,
// so it cannot be mistaken for a task's own worktree and cannot be committed to
// by accident.
func throwawayCheckout(ctx context.Context, repo, commit, prefix string) (*Delivered, error) {
	dir, err := os.MkdirTemp("", prefix)
	if err != nil {
		return nil, fmt.Errorf("making room for a checkout of %s: %w", short(commit), err)
	}
	path := filepath.Join(dir, "tree")

	// With the project's hooks off. This checkout exists for Luna to run one
	// command in and delete; a project's `post-checkout` firing on it is doing
	// work nobody asked for, against a tree that will not exist in a moment.
	//
	// It is also a real failure mode rather than a tidiness argument. Measured on
	// the swarm bench: `bd init` sets core.hooksPath and installs a post-checkout
	// that calls `bd`, which is a mise shim; inside ai-jail $HOME is tmpfs, mise
	// cannot resolve the shim, and `git worktree add` exits 1 having created the
	// worktree anyway. Luna read the exit code and blocked a task that had
	// delivered.
	if _, err := git(ctx, repo, "-c", "core.hooksPath=/dev/null",
		"worktree", "add", "--detach", path, commit); err != nil {
		_ = os.RemoveAll(dir)
		return nil, fmt.Errorf("checking out %s: %w", short(commit), err)
	}

	return &Delivered{
		Path: path,
		Ref:  short(commit),
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

// Handover is the commit a stage delivered and the message it left with it.
//
// Both come from the same commit deliberately: the sha is what the next stage
// branches from, and the message is where the agent says which artifacts it
// produced. Reading them separately would let a commit land between the two
// calls and pair a sha with the wrong declaration.
//
// The message is the channel because committing is already mandatory — the brief
// says so — and it needs no new protocol between Luna and the harness. What it
// is not is proof: the agent is reporting, and the contract check decides
// (INV-core-1).
//
// Empty for both when there is no commit, which is the first stage of the first
// task and not an error.
//
// An unreadable worktree **is** an error, and the two must not collapse. They did:
// a sandbox that cannot see a sibling directory made every `rev-parse` fail, every
// stage report no commit, and the base never move — so twelve stages each branched
// from the same commit and the work ended up on twelve sibling branches, each with
// a third of the answer. Nothing said anything, because "no commit" is a legitimate
// state and this returned it for both cases.
//
// The empty commit is worse than it looks downstream: `CheckoutAt` reads it as
// "verify HEAD", so the stage is checked against the repository's own head rather
// than against what it delivered — and passes.
func Handover(ctx context.Context, worktree string) (commit, message string, err error) {
	// Asked first and separately from HEAD, because the failure that matters is
	// not being able to read the tree at all, and `rev-parse HEAD` cannot tell
	// that apart from a branch with no commits on it.
	if _, err := git(ctx, worktree, "rev-parse", "--git-dir"); err != nil {
		return "", "", fmt.Errorf("reading what %s delivered: %w", worktree, err)
	}

	head, err := git(ctx, worktree, "rev-parse", "--verify", "HEAD^{commit}")
	if err != nil {
		// The tree is readable and has no commit: the first stage of the first
		// task, which is a state rather than a failure.
		return "", "", nil
	}
	// %B is the raw subject and body, which is what the declaration sits in.
	// A message that cannot be read still leaves a usable commit.
	body, err := git(ctx, worktree, "log", "-1", "--format=%B", head)
	if err != nil {
		return head, "", nil
	}
	return head, body, nil
}
