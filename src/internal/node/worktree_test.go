package node

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// TestAWorktreeIsASiblingOfTheRepository covers the layout rule.
//
// A checkout nested inside the repository gets caught by every recursive walk
// the repository does to itself, and by the cleanup of whatever tool made it.
// One convention regardless of what created it, so `ls ../wt-*` finds every one.
func TestAWorktreeIsASiblingOfTheRepository(t *testing.T) {
	repo := repoWithCommit(t)

	wt, err := OpenWorktree(context.Background(), repo, "T-1", "implementer", "")
	if err != nil {
		t.Fatalf("OpenWorktree: %v", err)
	}
	defer func() { _ = CloseWorktree(context.Background(), repo, wt) }()

	if strings.HasPrefix(wt.Path, repo+string(os.PathSeparator)) {
		t.Errorf("the worktree is inside the repository: %s", wt.Path)
	}
	if filepath.Dir(wt.Path) != filepath.Dir(repo) {
		t.Errorf("want a sibling of %s, got %s", repo, wt.Path)
	}
	if base := filepath.Base(wt.Path); !strings.HasPrefix(base, "wt-") {
		t.Errorf("want the house naming convention, got %q", base)
	}
}

// TestTwoRolesOnOneTaskGetDifferentWorktrees is the isolation that makes the
// write/review separation structural rather than a matter of the brief.
//
// A shared directory lets a reviewer read uncommitted files, and then it reviews
// something other than what was delivered.
func TestTwoRolesOnOneTaskGetDifferentWorktrees(t *testing.T) {
	repo := repoWithCommit(t)
	ctx := context.Background()

	build, err := OpenWorktree(ctx, repo, "T-2", "implementer", "")
	if err != nil {
		t.Fatalf("opening the implementer's worktree: %v", err)
	}
	defer func() { _ = CloseWorktree(ctx, repo, build) }()

	review, err := OpenWorktree(ctx, repo, "T-2", "reviewer", "")
	if err != nil {
		t.Fatalf("opening the reviewer's worktree: %v", err)
	}
	defer func() { _ = CloseWorktree(ctx, repo, review) }()

	if build.Path == review.Path {
		t.Errorf("both roles share a directory: %s", build.Path)
	}
	if build.Branch == review.Branch {
		t.Errorf("both roles share a branch: %s", build.Branch)
	}
}

// TestAMechanicalStageGetsAUsableBranchName covers two bugs that only appear by
// running it.
//
// `luna/<task>/` with nothing after the slash is not a valid ref at all. And
// `luna/<task>` *is* valid, which is worse — git stores refs as directories, so
// a mechanical stage taking that name makes `luna/<task>/<role>` impossible
// afterwards. The first stage of every task is mechanical, so that broke every
// task with a role after it.
func TestAMechanicalStageGetsAUsableBranchName(t *testing.T) {
	repo := repoWithCommit(t)
	ctx := context.Background()

	mechanical, err := OpenWorktree(ctx, repo, "T-3", "", "")
	if err != nil {
		t.Fatalf("opening a mechanical stage's worktree: %v", err)
	}
	defer func() { _ = CloseWorktree(ctx, repo, mechanical) }()

	if strings.HasSuffix(mechanical.Branch, "/") {
		t.Errorf("the branch name ends in a separator: %q", mechanical.Branch)
	}

	// The stage that follows it must still be able to have a branch.
	next, err := OpenWorktree(ctx, repo, "T-3", "implementer", "")
	if err != nil {
		t.Fatalf("a role after a mechanical stage could not open a worktree: %v", err)
	}
	defer func() { _ = CloseWorktree(ctx, repo, next) }()
}

// TestAWorktreeBranchesFromTheDeliveryRatherThanTheRepository is the handoff.
//
// The next stage starts from what the previous one delivered. Branching from the
// repository's own head instead would silently discard every stage before it —
// which is exactly how twelve stages once verified against the wrong tree and
// passed.
func TestAWorktreeBranchesFromTheDeliveryRatherThanTheRepository(t *testing.T) {
	repo := repoWithCommit(t)
	ctx := context.Background()

	// A second commit that is not on the repository's checked-out branch, so
	// "branched from HEAD" and "branched from the delivery" differ observably.
	first, err := OpenWorktree(ctx, repo, "T-4", "implementer", "")
	if err != nil {
		t.Fatalf("opening the first worktree: %v", err)
	}
	if err := os.WriteFile(filepath.Join(first.Path, "delivered.txt"), []byte("work"), 0o600); err != nil {
		t.Fatalf("writing the delivery: %v", err)
	}
	run(t, first.Path, "git", "add", "-A")
	run(t, first.Path, "git", "commit", "-m", "delivered")
	delivered := strings.TrimSpace(output(t, first.Path, "git", "rev-parse", "HEAD"))
	if err := CloseWorktree(ctx, repo, first); err != nil {
		t.Fatalf("closing the first worktree: %v", err)
	}

	second, err := OpenWorktree(ctx, repo, "T-4", "reviewer", delivered)
	if err != nil {
		t.Fatalf("opening the second worktree: %v", err)
	}
	defer func() { _ = CloseWorktree(ctx, repo, second) }()

	if _, err := os.Stat(filepath.Join(second.Path, "delivered.txt")); err != nil {
		t.Errorf("the next stage did not start from the delivery: %v", err)
	}
}

// TestALeftoverWorktreeIsClearedRatherThanReused covers the directory an earlier
// attempt left behind. Reusing one starts the stage on top of work nobody
// accounted for, and it reads as the agent having produced it.
func TestALeftoverWorktreeIsClearedRatherThanReused(t *testing.T) {
	repo := repoWithCommit(t)
	ctx := context.Background()

	first, err := OpenWorktree(ctx, repo, "T-5", "implementer", "")
	if err != nil {
		t.Fatalf("opening the worktree: %v", err)
	}
	stray := filepath.Join(first.Path, "left-behind.txt")
	if err := os.WriteFile(stray, []byte("uncommitted"), 0o600); err != nil {
		t.Fatalf("writing the leftover: %v", err)
	}

	// Opening the same stage again, as a retry would.
	second, err := OpenWorktree(ctx, repo, "T-5", "implementer", "")
	if err != nil {
		t.Fatalf("reopening the worktree: %v", err)
	}
	defer func() { _ = CloseWorktree(ctx, repo, second) }()

	if _, err := os.Stat(filepath.Join(second.Path, "left-behind.txt")); err == nil {
		t.Error("the retry inherited uncommitted work from the previous attempt")
	}
}

// TestClosingAWorktreeTwiceIsNotAnError covers the deferred cleanup running
// after something else already removed the directory. A stage that delivered
// must not be turned into a stage that failed by a tidy-up.
func TestClosingAWorktreeTwiceIsNotAnError(t *testing.T) {
	repo := repoWithCommit(t)
	ctx := context.Background()

	wt, err := OpenWorktree(ctx, repo, "T-6", "implementer", "")
	if err != nil {
		t.Fatalf("OpenWorktree: %v", err)
	}
	if err := CloseWorktree(ctx, repo, wt); err != nil {
		t.Fatalf("first close: %v", err)
	}
	if err := CloseWorktree(ctx, repo, wt); err != nil {
		t.Errorf("closing an already-removed worktree reported an error: %v", err)
	}
}

// TestClosingNothingIsNothing covers the deferred cleanup on a path that never
// opened one — the failure that happens before the worktree exists.
func TestClosingNothingIsNothing(t *testing.T) {
	if err := CloseWorktree(context.Background(), t.TempDir(), Worktree{}); err != nil {
		t.Errorf("closing an unopened worktree reported an error: %v", err)
	}
}

// TestAnUnreadableBaseStopsTheStage covers the fallback that must not exist.
//
// Falling back to HEAD when the named delivery cannot be read is how twelve
// stages verified against the repository's own head and passed, having discarded
// every stage before them.
func TestAnUnreadableBaseStopsTheStage(t *testing.T) {
	repo := repoWithCommit(t)

	_, err := OpenWorktree(context.Background(), repo, "T-7", "implementer", "0000000000000000000000000000000000000000")
	if err == nil {
		t.Fatal("want a refusal for a base that cannot be read, got a worktree")
	}
	if !strings.Contains(err.Error(), "T-7") {
		t.Errorf("want the error to name the task, got %q", err)
	}
}
