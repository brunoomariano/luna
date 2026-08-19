package node

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// Worktree is a stage's checkout: where its agent works and what its delivery is
// read from.
type Worktree struct {
	// Path is the directory. It is also the agent's working directory and the
	// only place the sandbox lets it write.
	Path string

	// Branch is where the work lands, named after the task and the role so a
	// checkout is findable without asking Luna, and so two roles on one task
	// cannot collide.
	Branch string
}

// OpenWorktree creates a stage's checkout, branched from base.
//
// A sibling of the repository (`../wt-<repo>-<task>-<role>`) rather than a child:
// a checkout nested inside the repository gets caught by every recursive walk the
// repository does to itself, and by the cleanup of whatever tool made it. One
// convention regardless of what created it, so `ls ../wt-*` finds every one.
//
// Base empty means the repository's own head, which is the first stage of a task.
// A base that is present and unreadable is an error rather than a fallback —
// falling back to HEAD is how twelve stages once verified against the
// repository's own head and passed, having discarded every stage before them.
func OpenWorktree(ctx context.Context, repo, taskID, role, base string) (Worktree, error) {
	path, err := worktreePath(repo, taskID, role)
	if err != nil {
		return Worktree{}, err
	}
	branch := stageBranch(taskID, role)

	// A leftover from a previous run is removed rather than reused: reusing one
	// means the stage starts on top of work nobody accounted for, which reads as
	// the agent having produced it.
	if _, err := os.Stat(path); err == nil {
		if err := CloseWorktree(ctx, repo, Worktree{Path: path, Branch: branch}); err != nil {
			return Worktree{}, fmt.Errorf("clearing the worktree left at %s: %w", path, err)
		}
	}

	from := base
	if from == "" {
		from = "HEAD"
	}

	// -B rather than -b: a branch left behind by an earlier attempt is moved to
	// the new base rather than refusing the stage. The branch name identifies the
	// work, not one particular attempt at it.
	if _, err := git(ctx, repo, "worktree", "add", "-B", branch, path, from); err != nil {
		return Worktree{}, fmt.Errorf("opening a worktree for %s at %s from %s: %w", taskID, role, from, err)
	}
	return Worktree{Path: path, Branch: branch}, nil
}

// CloseWorktree removes a stage's checkout.
//
// What survives a stage is the commit, which is the handoff — so the next role
// starts from the artifact and never from a directory somebody else was working
// in. The directory going away is the point rather than a tidy-up.
func CloseWorktree(ctx context.Context, repo string, wt Worktree) error {
	if wt.Path == "" {
		return nil
	}
	if _, err := git(ctx, repo, "worktree", "remove", "--force", wt.Path); err != nil {
		// A worktree git has already forgotten is not an error worth failing on,
		// but the directory may still be there.
		if removeErr := os.RemoveAll(wt.Path); removeErr != nil {
			return fmt.Errorf("removing %s: %w", wt.Path, removeErr)
		}
	}
	return nil
}

// worktreePath is `../wt-<repo>-<task>-<role>`, absolute.
func worktreePath(repo, taskID, role string) (string, error) {
	absolute, err := filepath.Abs(repo)
	if err != nil {
		return "", fmt.Errorf("resolving the repository path %q: %w", repo, err)
	}
	name := fmt.Sprintf("wt-%s-%s-%s", filepath.Base(absolute), taskID, role)
	if role == "" {
		// A mechanical stage has no role, and a trailing separator makes the
		// directory read as though one went missing.
		name = fmt.Sprintf("wt-%s-%s", filepath.Base(absolute), taskID)
	}
	return filepath.Join(filepath.Dir(absolute), name), nil
}

// stageBranch is where one stage's work lands.
//
// Named after the task and the role rather than the stage: two stages run by the
// same role continue on one branch, which is what makes a base handed forward
// mean the same thing whether or not the role changed.
//
// A mechanical stage has no role, and it gets a named one rather than the task's
// own branch. Two bugs sit behind that, both found by running it:
//
// `luna/<task>/` with nothing after the slash is not a valid ref at all. And
// `luna/<task>` *is* valid, which is worse — git stores refs as directories, so
// a task whose mechanical stage took `luna/T-1` could no longer create
// `luna/T-1/analyst`: "cannot lock ref … 'refs/heads/luna/T-1' exists". The
// first stage of every task is mechanical, so that broke every task with a role
// after it.
func stageBranch(taskID, role string) string {
	if role == "" {
		role = "mechanical"
	}
	return fmt.Sprintf("luna/%s/%s", taskID, strings.ToLower(role))
}
