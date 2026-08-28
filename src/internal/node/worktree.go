package node

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/brunoomariano/luna/src/internal/lead"
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
	path, err := WorktreePath(repo, taskID, role)
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

	// And a registration whose directory is already gone, which the check above
	// cannot see: a stage that is killed never closes its worktree, so git goes
	// on holding that branch checked out at a path that no longer exists, and the
	// `add` below fails with "already used by worktree at …".
	//
	// It needed the roles to collapse before it could bite. A branch is named for
	// the task and the role, so with twelve roles a killed stage stranded a name
	// nothing asked for again; with one `maker` across plan, build and refactor,
	// the next stage asks for exactly that name. Measured on TALLY-2 — killed
	// during build, blocked at refactor.
	if _, err := git(ctx, repo, "worktree", "prune"); err != nil {
		return Worktree{}, fmt.Errorf("clearing worktrees git still holds for %s: %w", repo, err)
	}

	from := base
	if from == "" {
		from = "HEAD"
	}

	// -B rather than -b: a branch left behind by an earlier attempt is moved to
	// the new base rather than refusing the stage. The branch name identifies the
	// work, not one particular attempt at it.
	if _, err := git(ctx, repo, "worktree", "add", "-B", branch, path, from); err != nil {
		if held := strings.Contains(err.Error(), "already used by worktree"); held {
			return Worktree{}, fmt.Errorf(
				"%w: the branch %s is checked out somewhere else, so this stage cannot open its worktree.\n"+
					"  `git worktree list` says where. Remove it, or — to read the work without holding\n"+
					"  the branch — `git worktree add --detach <path> <sha>`, which collides with nobody: %w",
				lead.ErrInfrastructure, branch, err,
			)
		}
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

// WorktreePath is `../wt-<repo>-<task>-<stage>`, absolute.
//
// Exported so `luna status` can say where the work is without opening anything:
// "which worktree is this in" is asked while a stage is running, and answering it
// by guessing the naming convention is how somebody looks in the wrong directory.
func WorktreePath(repo, taskID, stage string) (string, error) {
	absolute, err := filepath.Abs(repo)
	if err != nil {
		return "", fmt.Errorf("resolving the repository path %q: %w", repo, err)
	}
	name := fmt.Sprintf("wt-%s-%s-%s", filepath.Base(absolute), taskID, stage)
	if stage == "" {
		// A trailing separator makes the directory read as though a part went
		// missing. Every stage has an id, so nothing reaches this from the flow —
		// it is here for a caller that asks about a task rather than a stage.
		name = fmt.Sprintf("wt-%s-%s", filepath.Base(absolute), taskID)
	}
	return filepath.Join(filepath.Dir(absolute), name), nil
}

// stageBranch is where one stage's work lands: `luna/<task>/<stage>`.
//
// The two halves are what makes a checkout self-describing, which is what lets
// `Identify` answer "which task and stage is this" from a directory alone. Both
// are required, and the empty case is refused rather than defaulted: two bugs
// sat behind defaulting it, both found by running it.
//
// `luna/<task>/` with nothing after the slash is not a valid ref at all. And
// `luna/<task>` *is* valid, which is worse — git stores refs as directories, so
// a task that took `luna/T-1` could no longer create `luna/T-1/build`: "cannot
// lock ref … 'refs/heads/luna/T-1' exists".
func stageBranch(taskID, stage string) string {
	if stage == "" {
		// Unreachable from a flow, where every stage has an id. Named rather than
		// left to compose an invalid ref, because the ref it would compose is the
		// one that broke every task after it.
		stage = "unnamed"
	}
	return fmt.Sprintf("luna/%s/%s", taskID, strings.ToLower(stage))
}
