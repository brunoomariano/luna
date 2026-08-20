package node

import (
	"context"
	"fmt"
)

// TaskBranch is the ref a task ends on: `luna/<task>/done`.
//
// Land creates it, and pointing it at the last stage's commit is what turns it
// into the answer to "where is the work".
//
// The trailing segment is what makes it creatable at all. It was `luna/<task>`,
// and git stores refs as directories: a task whose stages worked on
// `luna/<task>/maker` cannot then have `luna/<task>`, because the name is
// already a directory holding them. `git branch -f` answers "cannot lock ref
// 'refs/heads/luna/T-1': 'refs/heads/luna/T-1/critic' exists".
//
// The same collision was found and fixed for the stage branches, one segment
// deeper — see stageBranch, whose comment describes it. The landing ref was left
// on the colliding shape, and the failure was invisible for a second reason:
// `luna lead` wired up neither Land nor Warn, so nothing tried and nothing said
// so. Measured on TALLY-7, which reported `branch luna/TALLY-7` in its status
// for a ref that was never created.
func TaskBranch(taskID string) string { return "luna/" + taskID + "/done" }

// Land points a task's branch at the commit its last stage delivered.
//
// It is the one write Luna keeps now that it does not integrate: `git branch -f`
// on a ref under `luna/`, which is a namespace Luna owns entirely. Nothing
// outside it is touched, and no merge is attempted — moving the work anywhere
// else is a manual act, deliberately.
//
// `done` is what makes this run, and running it is what makes `done` mean *ready
// to integrate*. Measured before it existed: a task finished all twelve stages,
// recorded the right commit as its base, and left `luna/<task>` on the seed —
// the work was on a role branch and nothing said which.
//
// An empty commit is not an error. A task can reach the end having committed
// nothing — every stage mechanical, or a flow that produces no code — and there
// is nothing to point at then. Forcing the branch to an empty ref would be worse
// than leaving it where it is.
func Land(ctx context.Context, repo, taskID, commit string) error {
	if commit == "" {
		return nil
	}

	branch := TaskBranch(taskID)

	// Checked before writing, so a commit nobody has is reported as itself rather
	// than as git's branch-point complaint — the person reading it needs to know
	// the sha is wrong, not that a branch would not move.
	if _, err := git(ctx, repo, "rev-parse", "--verify", commit+"^{commit}"); err != nil {
		return fmt.Errorf("the commit %s a task ended on does not resolve: %w", short(commit), err)
	}

	if _, err := git(ctx, repo, "branch", "-f", branch, commit); err != nil {
		return fmt.Errorf("pointing %s at %s: %w", branch, short(commit), err)
	}
	return nil
}
