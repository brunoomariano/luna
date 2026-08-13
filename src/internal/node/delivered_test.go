package node

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/brunoomariano/luna/src/internal/fsm"
)

// repo makes a git repository with one commit, and returns its path.
//
// A real repository rather than a fake: the whole subject is what `git worktree
// add` produces, and a stub of git would be testing the stub.
func repo(t *testing.T) string {
	t.Helper()

	dir := t.TempDir()
	for _, args := range [][]string{
		{"init", "--initial-branch=main"},
		{"config", "user.email", "test@example.invalid"},
		{"config", "user.name", "Test"},
	} {
		run(t, dir, "git", args...)
	}

	write(t, dir, "delivered.txt", "committed")
	run(t, dir, "git", "add", ".")
	run(t, dir, "git", "commit", "-m", "the delivery")
	return dir
}

// TestVerificationDoesNotSeeUncommittedWork is INV-core-4's acceptance criterion.
//
// A file left in the working tree and never delivered must not reach the check.
// This is the dominant way a green verdict turns out to be wrong — not sabotage
// but incoherence: an uncommitted file, a local .env, a test edited and never
// committed. A suite that passes on that tree says nothing about the delivery.
func TestVerificationDoesNotSeeUncommittedWork(t *testing.T) {
	dir := repo(t)

	// The agent leaves something behind that it never committed.
	write(t, dir, "left-behind.txt", "not delivered")

	shell := Shell{Dir: dir}
	evidence, err := shell.Prove(
		context.Background(),
		fsm.Command{Run: "test -f left-behind.txt", Scope: fsm.ScopeTargeted},
		1,
	)
	if err != nil {
		t.Fatalf("proving: %v", err)
	}

	if evidence.Verdict != fsm.VerdictFailed {
		t.Errorf("a file that was never delivered must not be visible to the check, got %q",
			evidence.Verdict)
	}

	// And the delivery itself must be there, or the check is running over nothing.
	evidence, err = shell.Prove(
		context.Background(),
		fsm.Command{Run: "test -f delivered.txt", Scope: fsm.ScopeTargeted},
		2,
	)
	if err != nil {
		t.Fatalf("proving: %v", err)
	}
	if evidence.Verdict != fsm.VerdictPassed {
		t.Errorf("what was committed must be visible to the check, got %q", evidence.Verdict)
	}
}

// TestVerificationSeesTheCommittedVersionOfAnEditedFile is the sharper case.
//
// The file exists in both trees, with different content. A check reading it in
// the working tree would see the edit; reading the delivery it sees what was
// handed over.
func TestVerificationSeesTheCommittedVersionOfAnEditedFile(t *testing.T) {
	dir := repo(t)
	write(t, dir, "delivered.txt", "edited after the commit")

	evidence, err := Shell{Dir: dir}.Prove(
		context.Background(),
		fsm.Command{Run: "grep -q committed delivered.txt", Scope: fsm.ScopeTargeted},
		1,
	)
	if err != nil {
		t.Fatalf("proving: %v", err)
	}
	if evidence.Verdict != fsm.VerdictPassed {
		t.Errorf("the check must read the committed version, got %q: %s",
			evidence.Verdict, evidence.Detail)
	}
}

// TestARepositoryWithNoCommitVerifiesTheWorkingTree covers the first stage of the
// first task, where there is no delivery yet.
//
// Refusing to verify would block a task for not having committed something it was
// never asked to commit; the working tree is all there is, and saying so is more
// honest than pretending otherwise.
func TestARepositoryWithNoCommitVerifiesTheWorkingTree(t *testing.T) {
	dir := t.TempDir()
	run(t, dir, "git", "init", "--initial-branch=main")
	write(t, dir, "in-progress.txt", "nothing committed yet")

	evidence, err := Shell{Dir: dir}.Prove(
		context.Background(),
		fsm.Command{Run: "test -f in-progress.txt", Scope: fsm.ScopeTargeted},
		1,
	)
	if err != nil {
		t.Fatalf("proving: %v", err)
	}
	if evidence.Verdict != fsm.VerdictPassed {
		t.Errorf("with no delivery the working tree is what there is, got %q", evidence.Verdict)
	}
}

// TestTheDeliveryCheckoutIsCleanedUp keeps a verification from leaving a worktree
// behind on every run.
//
// Both halves matter. The directory has to go, and git has to be told — a
// worktree removed from disk without `git worktree remove` leaves a prunable
// entry in the repository that outlives the task, which is exactly the leak the
// study measured in other projects (5.26 GiB across 17 worktrees in one).
func TestTheDeliveryCheckoutIsCleanedUp(t *testing.T) {
	dir := repo(t)

	delivered, err := CheckoutDelivered(context.Background(), dir)
	if err != nil {
		t.Fatalf("checking out: %v", err)
	}
	if delivered == nil {
		t.Fatal("a repository with a commit has a delivery to check out")
	}

	path := delivered.Path
	if _, err := os.Stat(path); err != nil {
		t.Fatalf("the checkout should exist while it is in use: %v", err)
	}

	delivered.Close()

	if _, err := os.Stat(path); !os.IsNotExist(err) {
		t.Errorf("the checkout must be removed, got %v", err)
	}
	if listed := output(t, dir, "git", "worktree", "list"); strings.Contains(listed, path) {
		t.Errorf("git must be told the worktree is gone, still listed:\n%s", listed)
	}

	// Closing twice is what a deferred Close plus an explicit one looks like.
	delivered.Close()
}

// TestOverWorkingTreeSkipsTheCheckout covers the escape hatch, so the tests that
// use a plain directory keep working.
func TestOverWorkingTreeSkipsTheCheckout(t *testing.T) {
	dir := t.TempDir() // not a repository at all
	write(t, dir, "here.txt", "x")

	evidence, err := Shell{Dir: dir, OverWorkingTree: true}.Prove(
		context.Background(),
		fsm.Command{Run: "test -f here.txt", Scope: fsm.ScopeTargeted},
		1,
	)
	if err != nil {
		t.Fatalf("proving: %v", err)
	}
	if evidence.Verdict != fsm.VerdictPassed {
		t.Errorf("the working tree is what was asked for, got %q", evidence.Verdict)
	}
}

func run(t *testing.T, dir, name string, args ...string) {
	t.Helper()

	cmd := exec.Command(name, args...) //nolint:gosec // fixed arguments from the test
	cmd.Dir = dir
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("%s %s: %v\n%s", name, strings.Join(args, " "), err, out)
	}
}

func output(t *testing.T, dir, name string, args ...string) string {
	t.Helper()

	cmd := exec.Command(name, args...) //nolint:gosec // fixed arguments from the test
	cmd.Dir = dir
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("%s %s: %v\n%s", name, strings.Join(args, " "), err, out)
	}
	return string(out)
}

func write(t *testing.T, dir, name, body string) {
	t.Helper()

	if err := os.WriteFile(filepath.Join(dir, name), []byte(body), 0o600); err != nil {
		t.Fatalf("writing %s: %v", name, err)
	}
}

// TestHandoverReadsTheCommitAndItsMessage covers the pair the node needs from a
// finished stage: the sha the next one branches from, and the message the agent
// declared its delivery in.
//
// Both from the same commit deliberately — reading them separately would let a
// commit land between the two calls and pair a sha with the wrong declaration.
func TestHandoverReadsTheCommitAndItsMessage(t *testing.T) {
	dir := repo(t)
	write(t, dir, "artifact.txt", "the work")
	run(t, dir, "git", "add", ".")
	run(t, dir, "git", "commit", "-m", "chore: the stage\n\nDelivered: repos")

	commit, message := Handover(context.Background(), dir)

	if commit == "" {
		t.Error("no commit came back from a repository that has one")
	}
	if !strings.Contains(message, "Delivered: repos") {
		t.Errorf("the declaration did not survive: %q", message)
	}
	// Head is the same call, narrowed.
	if got := Head(context.Background(), dir); got != commit {
		t.Errorf("Head = %q, Handover = %q — they must agree", got, commit)
	}
}

// TestHandoverOnARepositoryWithNoCommit. The first stage of the first task has
// nothing behind it, and that is a state rather than a failure.
func TestHandoverOnARepositoryWithNoCommit(t *testing.T) {
	dir := t.TempDir()
	run(t, dir, "git", "init", "--initial-branch=main")

	commit, message := Handover(context.Background(), dir)

	if commit != "" || message != "" {
		t.Errorf("got %q/%q, want nothing from a repository with no commit", commit, message)
	}
}
