package node

import (
	"context"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// readFile reads a file from the repository, for the assertions about what
// survived a merge.
func readFile(t *testing.T, dir, name string) string {
	t.Helper()

	body, err := os.ReadFile(filepath.Join(dir, name))
	if err != nil {
		t.Fatalf("reading %s: %v", name, err)
	}
	return string(body)
}

// gitTry runs git and returns whether it succeeded. Unlike `run`, a failure is
// the answer rather than a fatal — asking whether MERGE_HEAD exists is a
// question, and the expected answer is no.
func gitTry(t *testing.T, dir string, args ...string) error {
	t.Helper()

	cmd := exec.Command("git", args...)
	cmd.Dir = dir
	return cmd.Run()
}

// branchWith makes a branch off main carrying one change, and returns its
// commit. Real git throughout: the subject is what git does with a conflict, and
// a stub of git would be testing the stub.
func branchWith(t *testing.T, dir, branch, file, content string) string {
	t.Helper()

	run(t, dir, "git", "checkout", "-q", "-b", branch, "main")
	write(t, dir, file, content)
	run(t, dir, "git", "add", ".")
	run(t, dir, "git", "commit", "-q", "-m", "work on "+branch)

	sha := strings.TrimSpace(output(t, dir, "git", "rev-parse", "HEAD"))
	run(t, dir, "git", "checkout", "-q", "main")
	return sha
}

func TestACleanBranchIsReadyToMerge(t *testing.T) {
	dir := repo(t)
	commit := branchWith(t, dir, "feature", "new.txt", "added")

	got, err := Merger{Repo: dir}.DryRun(context.Background(), commit)
	if err != nil {
		t.Fatalf("DryRun: %v", err)
	}

	if got.Verdict != MergeReady {
		t.Errorf("verdict = %q, want %q (%s)", got.Verdict, MergeReady, got.Detail)
	}
}

// TestAConflictIsAVerdictNotAnError is the shape the whole design rests on. A
// conflict is an ordinary outcome once several agents share a base, and a system
// that errors on one is a system somebody babysits.
func TestAConflictIsAVerdictNotAnError(t *testing.T) {
	dir := repo(t)

	// Both branches change the same line of the same file.
	conflicting := branchWith(t, dir, "theirs", "delivered.txt", "their version")
	run(t, dir, "git", "checkout", "-q", "main")
	write(t, dir, "delivered.txt", "our version")
	run(t, dir, "git", "add", ".")
	run(t, dir, "git", "commit", "-q", "-m", "our change")

	got, err := Merger{Repo: dir}.DryRun(context.Background(), conflicting)
	if err != nil {
		t.Fatalf("a conflict came back as an error rather than a verdict: %v", err)
	}

	if got.Verdict != MergeBlocked {
		t.Fatalf("verdict = %q, want %q", got.Verdict, MergeBlocked)
	}
	// Naming the file is the point. swarm-forge's own bugs.md records a
	// multi-hour stall where the dashboard never surfaced which file conflicted.
	if len(got.Conflicts) != 1 || got.Conflicts[0] != "delivered.txt" {
		t.Errorf("conflicts = %v, want the file that actually conflicted", got.Conflicts)
	}
	if !strings.Contains(got.Detail, "delivered.txt") {
		t.Errorf("detail = %q, want it to name the file", got.Detail)
	}
}

// TestTheDryRunLeavesTheRepositoryAlone is why the check is separate from the
// merge. Finding out by attempting it leaves the shared repository mid-conflict,
// and something then has to unwind it at the worst possible moment.
func TestTheDryRunLeavesTheRepositoryAlone(t *testing.T) {
	dir := repo(t)

	conflicting := branchWith(t, dir, "theirs", "delivered.txt", "their version")
	run(t, dir, "git", "checkout", "-q", "main")
	write(t, dir, "delivered.txt", "our version")
	run(t, dir, "git", "add", ".")
	run(t, dir, "git", "commit", "-q", "-m", "our change")

	before := strings.TrimSpace(output(t, dir, "git", "rev-parse", "HEAD"))

	if _, err := (Merger{Repo: dir}).DryRun(context.Background(), conflicting); err != nil {
		t.Fatalf("DryRun: %v", err)
	}

	if after := strings.TrimSpace(output(t, dir, "git", "rev-parse", "HEAD")); after != before {
		t.Errorf("the dry run moved HEAD from %s to %s", short(before), short(after))
	}
	// A repository left mid-merge has this file. Its absence is the assertion.
	if status := output(t, dir, "git", "status", "--porcelain"); strings.TrimSpace(status) != "" {
		t.Errorf("the dry run left the working tree dirty:\n%s", status)
	}
	if err := gitTry(t, dir, "rev-parse", "--verify", "MERGE_HEAD"); err == nil {
		t.Error("the dry run left the repository mid-merge")
	}
}

// TestTheDryRunCleansUpAfterItself guards the failure that outlives the task: a
// worktree removed from disk without git being told leaves a prunable entry in
// the repository forever.
func TestTheDryRunCleansUpAfterItself(t *testing.T) {
	dir := repo(t)
	commit := branchWith(t, dir, "feature", "new.txt", "added")

	if _, err := (Merger{Repo: dir}).DryRun(context.Background(), commit); err != nil {
		t.Fatalf("DryRun: %v", err)
	}

	list := output(t, dir, "git", "worktree", "list", "--porcelain")
	if strings.Contains(list, "prunable") {
		t.Errorf("a prunable worktree was left behind:\n%s", list)
	}
	if strings.Contains(list, "luna-merge-check") {
		t.Errorf("the merge-check worktree outlived the check:\n%s", list)
	}
}

// TestMergingIntegratesTheWork is the other half: a clean branch does not just
// report ready, it lands.
func TestMergingIntegratesTheWork(t *testing.T) {
	dir := repo(t)
	commit := branchWith(t, dir, "feature", "new.txt", "added")

	got, err := Merger{Repo: dir, As: LunaOwnsTheMerge}.Merge(context.Background(), commit, "merge feature")
	if err != nil {
		t.Fatalf("Merge: %v", err)
	}

	if got.Verdict != MergeReady {
		t.Fatalf("verdict = %q, want %q (%s)", got.Verdict, MergeReady, got.Detail)
	}
	if got.Commit == "" {
		t.Error("a completed merge reported no commit — it is the next stage's base")
	}
	if files := output(t, dir, "git", "ls-tree", "--name-only", "HEAD"); !strings.Contains(files, "new.txt") {
		t.Errorf("the merged file is not in the tree:\n%s", files)
	}
}

// TestAConflictingMergeChangesNothing is the guarantee that matters most. The
// caller asked to merge, it did not merge, and the repository is untouched — no
// half-applied state, and above all nothing resolved on the caller's behalf.
func TestAConflictingMergeChangesNothing(t *testing.T) {
	dir := repo(t)

	conflicting := branchWith(t, dir, "theirs", "delivered.txt", "their version")
	run(t, dir, "git", "checkout", "-q", "main")
	write(t, dir, "delivered.txt", "our version")
	run(t, dir, "git", "add", ".")
	run(t, dir, "git", "commit", "-q", "-m", "our change")

	before := strings.TrimSpace(output(t, dir, "git", "rev-parse", "HEAD"))

	got, err := Merger{Repo: dir, As: LunaOwnsTheMerge}.Merge(context.Background(), conflicting, "merge theirs")
	if err != nil {
		t.Fatalf("Merge: %v", err)
	}

	if got.Verdict != MergeBlocked {
		t.Fatalf("verdict = %q, want %q", got.Verdict, MergeBlocked)
	}
	if after := strings.TrimSpace(output(t, dir, "git", "rev-parse", "HEAD")); after != before {
		t.Errorf("a blocked merge still moved HEAD, from %s to %s", short(before), short(after))
	}

	// And our version survived. This is the failure a fork of swarm-forge
	// documented: `-X theirs`, and an agent silently discarding its own work.
	if content := readFile(t, dir, "delivered.txt"); content != "our version" {
		t.Errorf("the file reads %q — a blocked merge resolved a conflict on its own, "+
			"which is the one thing it must never do", content)
	}
}

// TestNothingIsResolvedAutomatically is the control for the test above: it
// asserts on the strategy rather than the outcome, so a future `-X theirs` fails
// here even if the conflict it silences never reaches the other test.
func TestNothingIsResolvedAutomatically(t *testing.T) {
	dir := repo(t)

	conflicting := branchWith(t, dir, "theirs", "delivered.txt", "their version")
	run(t, dir, "git", "checkout", "-q", "main")
	write(t, dir, "delivered.txt", "our version")
	run(t, dir, "git", "add", ".")
	run(t, dir, "git", "commit", "-q", "-m", "our change")

	got, _ := Merger{Repo: dir}.DryRun(context.Background(), conflicting)

	if got.Verdict == MergeReady {
		t.Fatal("a genuine conflict came back ready — something resolved it, and " +
			"the only correct answer to a conflict is to stop and say where it is")
	}
}

func TestMergingAnUnknownCommitIsAnError(t *testing.T) {
	dir := repo(t)

	got, err := Merger{Repo: dir}.DryRun(context.Background(), "0000000000000000000000000000000000000000")
	if err == nil && got.Verdict == MergeReady {
		t.Fatal("a commit that does not exist reported ready to merge")
	}
}

// TestOnlyLunaMerges is the ownership rule, enforced rather than asked for.
//
// swarm-forge learned this the expensive way and now gates it with an exit code.
// The failure it prevents is not malice: it is two things merging into the same
// branch at once, each having checked a state the other has already changed.
func TestOnlyLunaMerges(t *testing.T) {
	dir := repo(t)
	commit := branchWith(t, dir, "feature", "new.txt", "added")
	before := strings.TrimSpace(output(t, dir, "git", "rev-parse", "HEAD"))

	for _, as := range []Owner{"", "implementer", "reviewer"} {
		t.Run(string(as), func(t *testing.T) {
			_, err := Merger{Repo: dir, As: as}.Merge(context.Background(), commit, "merge feature")
			if !errors.Is(err, ErrNotTheOwner) {
				t.Fatalf("%q was allowed to merge (err = %v)", as, err)
			}
		})
	}

	if after := strings.TrimSpace(output(t, dir, "git", "rev-parse", "HEAD")); after != before {
		t.Errorf("a refused merge still moved HEAD, from %s to %s", short(before), short(after))
	}
}

// TestAnyoneMayAskWhetherItWouldMerge is the deliberate asymmetry. The dry run
// touches nothing, so gating it would buy no safety and would stop a reviewer
// from checking its own work before handing it over.
func TestAnyoneMayAskWhetherItWouldMerge(t *testing.T) {
	dir := repo(t)
	commit := branchWith(t, dir, "feature", "new.txt", "added")

	got, err := Merger{Repo: dir, As: "implementer"}.DryRun(context.Background(), commit)
	if err != nil {
		t.Fatalf("DryRun: %v", err)
	}
	if got.Verdict != MergeReady {
		t.Errorf("verdict = %q, want %q", got.Verdict, MergeReady)
	}
}

// TestSeveralConflictsAreSummarisedWithoutHidingTheCount. A person reading a
// notification needs to know it is not one file, and a wall of paths in a
// banner is a banner nobody finishes reading.
func TestSeveralConflictsAreSummarisedWithoutHidingTheCount(t *testing.T) {
	dir := repo(t)

	write(t, dir, "second.txt", "base")
	run(t, dir, "git", "add", ".")
	run(t, dir, "git", "commit", "-q", "-m", "a second file")

	run(t, dir, "git", "checkout", "-q", "-b", "theirs", "main")
	write(t, dir, "delivered.txt", "their one")
	write(t, dir, "second.txt", "their two")
	run(t, dir, "git", "add", ".")
	run(t, dir, "git", "commit", "-q", "-m", "their changes")
	conflicting := strings.TrimSpace(output(t, dir, "git", "rev-parse", "HEAD"))

	run(t, dir, "git", "checkout", "-q", "main")
	write(t, dir, "delivered.txt", "our one")
	write(t, dir, "second.txt", "our two")
	run(t, dir, "git", "add", ".")
	run(t, dir, "git", "commit", "-q", "-m", "our changes")

	got, err := Merger{Repo: dir}.DryRun(context.Background(), conflicting)
	if err != nil {
		t.Fatalf("DryRun: %v", err)
	}

	if len(got.Conflicts) != 2 {
		t.Fatalf("conflicts = %v, want both files", got.Conflicts)
	}
	if !strings.Contains(got.Detail, "1 more") {
		t.Errorf("detail = %q, want it to say how many others there are", got.Detail)
	}
}

// TestAFastForwardStillCountsAsAMerge covers the branch with nothing to
// reconcile — --no-ff makes it a real merge commit, so the base a stage hands on
// is always a commit Luna made.
func TestAFastForwardStillCountsAsAMerge(t *testing.T) {
	dir := repo(t)
	commit := branchWith(t, dir, "feature", "new.txt", "added")

	got, err := Merger{Repo: dir, As: LunaOwnsTheMerge}.Merge(context.Background(), commit, "merge feature")
	if err != nil {
		t.Fatalf("Merge: %v", err)
	}

	parents := output(t, dir, "git", "rev-list", "--parents", "-n", "1", "HEAD")
	if len(strings.Fields(parents)) != 3 {
		t.Errorf("HEAD has %d parents, want a merge commit with two: %q",
			len(strings.Fields(parents))-1, parents)
	}
	if got.Commit == "" {
		t.Error("no commit reported")
	}
}

// TestMergingIntoANamedBranchChecksThatBranch. The default is HEAD, but a merge
// aimed at a branch that is not checked out must be checked against that branch
// rather than against whatever the repository happens to be on.
func TestMergingIntoANamedBranchChecksThatBranch(t *testing.T) {
	dir := repo(t)
	commit := branchWith(t, dir, "feature", "new.txt", "added")

	// Leave the repository on a branch that is not the merge target.
	run(t, dir, "git", "checkout", "-q", "-b", "elsewhere", "main")

	got, err := Merger{Repo: dir, Branch: "main"}.DryRun(context.Background(), commit)
	if err != nil {
		t.Fatalf("DryRun against main: %v", err)
	}
	if got.Verdict != MergeReady {
		t.Errorf("verdict = %q, want %q", got.Verdict, MergeReady)
	}
}

// TestAMergeThatFailsWithoutAConflictedFileStillBlocks. Not every refusal
// produces conflicted paths — unrelated histories is one — and the verdict has
// to be blocked rather than an error with no explanation.
func TestAMergeThatFailsWithoutAConflictedFileStillBlocks(t *testing.T) {
	dir := repo(t)

	// An orphan branch shares no history with main, which git refuses to merge
	// without --allow-unrelated-histories. Nothing conflicts; it simply will not.
	run(t, dir, "git", "checkout", "-q", "--orphan", "unrelated")
	run(t, dir, "git", "rm", "-q", "-rf", ".")
	write(t, dir, "alone.txt", "no shared history")
	run(t, dir, "git", "add", ".")
	run(t, dir, "git", "commit", "-q", "-m", "an unrelated root")
	orphan := strings.TrimSpace(output(t, dir, "git", "rev-parse", "HEAD"))
	run(t, dir, "git", "checkout", "-q", "main")

	got, err := Merger{Repo: dir}.DryRun(context.Background(), orphan)
	if err != nil {
		t.Fatalf("DryRun: %v", err)
	}
	if got.Verdict != MergeBlocked {
		t.Fatalf("verdict = %q, want %q", got.Verdict, MergeBlocked)
	}
	if got.Detail == "" {
		t.Error("a blocked merge said nothing about why")
	}
}

func TestAMergerNeedsARepository(t *testing.T) {
	if _, err := (Merger{Repo: t.TempDir()}).DryRun(context.Background(), "HEAD"); err == nil {
		t.Fatal("a directory that is not a repository produced a merge verdict")
	}

	// And the same through the merge path, which has its own authorisation check
	// in front of it: the failure must survive being authorised.
	if _, err := (Merger{Repo: t.TempDir(), As: LunaOwnsTheMerge}).Merge(
		context.Background(), "HEAD", "merge",
	); err == nil {
		t.Fatal("merging in a directory that is not a repository reported success")
	}
}

// TestAnEmptyRepositoryHasNothingToMergeInto covers a repository with no commit
// yet — the very first task, before anything was delivered.
func TestAnEmptyRepositoryHasNothingToMergeInto(t *testing.T) {
	dir := t.TempDir()
	run(t, dir, "git", "init", "--initial-branch=main")

	if _, err := (Merger{Repo: dir}).DryRun(context.Background(), "HEAD"); err == nil {
		t.Fatal("a repository with no commits produced a merge verdict")
	}
}

// TestABranchThatMovedAfterTheCheckBlocks is the race the two calls create. The
// dry run passed against a state that no longer holds, and reporting ready would
// claim a merge that did not happen.
func TestABranchThatMovedAfterTheCheckBlocks(t *testing.T) {
	dir := repo(t)
	commit := branchWith(t, dir, "feature", "new.txt", "added")

	// Leave the repository mid-merge so the real merge cannot start. This is what
	// a branch that moved underneath the check looks like from git's side: the
	// dry run succeeds in its own worktree, the real merge refuses.
	run(t, dir, "git", "checkout", "-q", "main")
	write(t, dir, "uncommitted.txt", "in the way")
	run(t, dir, "git", "add", ".")

	got, err := Merger{Repo: dir, As: LunaOwnsTheMerge}.Merge(context.Background(), commit, "merge feature")
	if err != nil {
		t.Fatalf("Merge: %v", err)
	}
	if got.Verdict != MergeBlocked {
		t.Fatalf("verdict = %q, want %q — the merge did not happen", got.Verdict, MergeBlocked)
	}
	if !strings.Contains(got.Detail, "moved") {
		t.Errorf("detail = %q, want it to say the state changed under the check", got.Detail)
	}
}
