package node

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// identifiable builds a repository with one commit, which is the least a
// worktree can be added to.
func identifiable(t *testing.T) string {
	t.Helper()

	repo := t.TempDir()
	for _, args := range [][]string{
		{"init", "-q", "-b", "main"},
		{"config", "user.email", "t@t"},
		{"config", "user.name", "t"},
	} {
		if _, err := git(context.Background(), repo, args...); err != nil {
			t.Fatalf("git %v: %v", args, err)
		}
	}
	if err := os.WriteFile(filepath.Join(repo, "f"), []byte("x"), 0o600); err != nil {
		t.Fatalf("writing: %v", err)
	}
	for _, args := range [][]string{{"add", "-A"}, {"commit", "-qm", "first"}} {
		if _, err := git(context.Background(), repo, args...); err != nil {
			t.Fatalf("git %v: %v", args, err)
		}
	}
	return repo
}

// TestTheMainCheckoutIdentifiesItself is the ordinary case: a person standing in
// their repository, with no task in sight.
func TestTheMainCheckoutIdentifiesItself(t *testing.T) {
	repo := identifiable(t)

	id, err := Identify(context.Background(), repo)
	if err != nil {
		t.Fatalf("Identify: %v", err)
	}

	if id.Repo != id.Worktree {
		t.Errorf("the main checkout is its own worktree, got repo=%s worktree=%s", id.Repo, id.Worktree)
	}
	if id.Linked {
		t.Error("the main checkout is not a linked worktree")
	}
	if id.TaskID != "" || id.Stage != "" {
		t.Errorf("a branch naming no task named one: %q %q", id.TaskID, id.Stage)
	}
}

// TestAStageWorktreeSaysWhichTaskAndStageItIs is the whole point: standing in a
// checkout Luna made, the task and the stage are already there to be read.
func TestAStageWorktreeSaysWhichTaskAndStageItIs(t *testing.T) {
	repo := identifiable(t)

	wt, err := OpenWorktree(context.Background(), repo, "LUNA-1", "forge", "")
	if err != nil {
		t.Fatalf("opening the worktree: %v", err)
	}
	t.Cleanup(func() { _ = CloseWorktree(context.Background(), repo, wt) })

	id, err := Identify(context.Background(), wt.Path)
	if err != nil {
		t.Fatalf("Identify: %v", err)
	}

	if id.TaskID != "LUNA-1" || id.Stage != "forge" {
		t.Errorf("want LUNA-1/forge, got %q/%q from branch %q", id.TaskID, id.Stage, id.Branch)
	}
	if !id.Linked {
		t.Error("a stage's checkout is a linked worktree")
	}
	// And the repository is the main one, not the worktree — the distinction the
	// whole store resolution rests on.
	if filepath.Clean(id.Repo) != filepath.Clean(repo) {
		t.Errorf("repo = %s, want the main checkout %s", id.Repo, repo)
	}
}

// TestSomebodyElsesBranchNamesNoTask. Luna runs in repositories people also work
// in normally, and a branch that is not `luna/<task>/<stage>` is theirs.
func TestSomebodyElsesBranchNamesNoTask(t *testing.T) {
	for _, branch := range []string{"main", "feature/x", "luna", "luna/only-two", "luna//empty"} {
		task, stage := fromBranch(branch)
		if task != "" || stage != "" {
			t.Errorf("%q was read as task %q stage %q", branch, task, stage)
		}
	}
}

// TestTheDirectoryCrossChecksTheBranch is the second source, and it is there on
// purpose.
//
// The branch is the authority because it travels with the work; the directory is
// what catches a worktree that was moved, or one reused for another task. Two
// sources for one fact is usually a defect — here a disagreement means something
// moved that should not have, and the caller is told rather than guessed at.
func TestTheDirectoryCrossChecksTheBranch(t *testing.T) {
	repo := identifiable(t)

	wt, err := OpenWorktree(context.Background(), repo, "LUNA-2", "plan", "")
	if err != nil {
		t.Fatalf("opening the worktree: %v", err)
	}
	t.Cleanup(func() { _ = CloseWorktree(context.Background(), repo, wt) })

	id, err := Identify(context.Background(), wt.Path)
	if err != nil {
		t.Fatalf("Identify: %v", err)
	}
	if agrees, why := id.Agrees(); !agrees {
		t.Errorf("a worktree Luna made disagrees with its own branch: %s", why)
	}

	// The same branch read from somewhere it does not belong.
	moved := id
	moved.Worktree = filepath.Join(filepath.Dir(id.Worktree), "somewhere-else")
	agrees, why := moved.Agrees()
	if agrees {
		t.Fatal("a worktree at the wrong path agreed with its branch")
	}
	for _, want := range []string{"LUNA-2", "plan", "somewhere-else"} {
		if !strings.Contains(why, want) {
			t.Errorf("the disagreement does not name %q: %s", want, why)
		}
	}
}

// TestADirectoryThatIsNotARepositoryIsNotAnError. Luna runs in plain directories
// too, and a resolver that refused them would make every command that wants to be
// helpful about the current directory fail everywhere else.
func TestADirectoryThatIsNotARepositoryIsNotAnError(t *testing.T) {
	dir := t.TempDir()

	id, err := Identify(context.Background(), dir)
	if err != nil {
		t.Fatalf("a plain directory was refused: %v", err)
	}
	if id.TaskID != "" {
		t.Errorf("a plain directory named a task: %q", id.TaskID)
	}
}
