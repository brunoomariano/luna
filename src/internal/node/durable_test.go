package node_test

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/brunoomariano/luna/src/internal/node"
)

// TestAFirstRunAdoptsTheDirectory covers the ordinary case: nobody has been here,
// so there is nothing to be suspicious of.
func TestAFirstRunAdoptsTheDirectory(t *testing.T) {
	dir := filepath.Join(t.TempDir(), ".luna")

	if err := node.EnsureDurable(dir); err != nil {
		t.Fatalf("a directory Luna has never seen must be adopted, got %v", err)
	}
	if err := node.EnsureDurable(dir); err != nil {
		t.Fatalf("the second run must recognise its own anchor, got %v", err)
	}
}

// TestAGhostStoreIsRefused is the regression for the failure measured against
// ai-jail 1.17.0: a contained process cannot see the real log, creates its own on
// a tmpfs, reports success, and loses everything.
//
// The jail is reproduced by its observable consequence rather than by running one:
// from inside, the directory holds a database and *not* the anchor Luna wrote,
// because the anchor is on the filesystem the container cannot reach.
func TestAGhostStoreIsRefused(t *testing.T) {
	dir := filepath.Join(t.TempDir(), ".luna")

	// What the real run does, outside the jail.
	if err := node.EnsureDurable(dir); err != nil {
		t.Fatalf("setting up the real log: %v", err)
	}

	// What the contained process sees: its own database, and no anchor.
	if err := os.Remove(filepath.Join(dir, ".luna-anchor")); err != nil {
		t.Fatalf("simulating the invisible anchor: %v", err)
	}
	if err := os.WriteFile(filepath.Join(dir, "luna.db"), []byte("invented"), 0o600); err != nil {
		t.Fatalf("simulating the store the container invented: %v", err)
	}

	err := node.EnsureDurable(dir)
	if !errors.Is(err, node.ErrGhostStore) {
		t.Fatalf("a log with no anchor must be refused as a ghost, got %v", err)
	}
	// The message has to name the directory, because the person reading it is
	// looking for which path lied to them.
	if !strings.Contains(err.Error(), dir) {
		t.Errorf("the refusal must name the directory it refused, got %q", err)
	}
}

// TestALegitimateTmpfsLogIsNotRefused pins the distinction the guard is built on.
//
// Refusing tmpfs was the obvious implementation and the wrong one: this project's
// own fixtures live on one, and so does any CI runner with the workspace in RAM.
// What is refused is invisibility, not volatility.
func TestALegitimateTmpfsLogIsNotRefused(t *testing.T) {
	// t.TempDir() is under /tmp, which is tmpfs on the machine this was measured
	// on. A guard that keyed on the filesystem type would refuse this.
	dir := filepath.Join(t.TempDir(), ".luna")

	if err := node.EnsureDurable(dir); err != nil {
		t.Fatalf("a volatile but visible log is legitimate, got %v", err)
	}
	if err := os.WriteFile(filepath.Join(dir, "luna.db"), []byte("real"), 0o600); err != nil {
		t.Fatalf("writing the store: %v", err)
	}
	if err := node.EnsureDurable(dir); err != nil {
		t.Fatalf("a store beside its own anchor must pass, got %v", err)
	}
}

// TestAHollowRepositoryIsRefused is the second half of the measured failure, and
// the half the anchor alone cannot catch.
//
// Inside the jail the log directory does not exist at all, so there is no anchor
// to be missing and nothing looks suspicious. What gives it away is the
// repository: `git` answers from the worktree's own `.git`, so HEAD resolves,
// while the main checkout's files are not there.
func TestAHollowRepositoryIsRefused(t *testing.T) {
	root := t.TempDir()
	if err := os.Mkdir(filepath.Join(root, ".git"), 0o750); err != nil {
		t.Fatalf("setting up the repository: %v", err)
	}

	// What a real run leaves behind: the anchor in the git directory, naming the
	// log. The jail exposes the git directory, so a contained process reads this.
	real := filepath.Join(root, ".luna")
	if err := node.EnsureDurable(real); err != nil {
		t.Fatalf("the real run: %v", err)
	}

	// What the contained process sees: the anchor still readable, and the directory
	// it names gone, because that path is outside its reach.
	if err := os.RemoveAll(real); err != nil {
		t.Fatalf("simulating the unreachable log: %v", err)
	}

	err := node.EnsureDurable(real)
	if !errors.Is(err, node.ErrGhostStore) {
		t.Fatalf("a repository with no working tree must be refused, got %v", err)
	}
}

// TestARealCheckoutIsNotRefused pins the other side, and is the regression for
// the guard's first version: a real repository with an empty working tree is
// ordinary, and refusing it stopped `luna` from running at all.
func TestARealCheckoutIsNotRefused(t *testing.T) {
	root := t.TempDir()
	// Deliberately with no files in the working tree: `git init` plus an empty
	// commit is a legitimate checkout, and the guard's first version refused it —
	// the project's own suite builds exactly this shape.
	if err := os.MkdirAll(filepath.Join(root, ".git", "objects"), 0o750); err != nil {
		t.Fatalf("setting up the repository: %v", err)
	}

	if err := node.EnsureDurable(filepath.Join(root, ".luna")); err != nil {
		t.Fatalf("a checkout with files is a real one, got %v", err)
	}
}

// TestAPlainDirectoryIsNotRefused covers Luna running outside a checkout, which
// Root() deliberately supports.
func TestAPlainDirectoryIsNotRefused(t *testing.T) {
	if err := node.EnsureDurable(filepath.Join(t.TempDir(), ".luna")); err != nil {
		t.Fatalf("a directory that is not a repository is not this failure, got %v", err)
	}
}

// TestAnUnreadableAnchorIsReportedAsItself pins the distinction between "this log
// is unreachable" and "this directory is broken". Blaming containment for a
// permission fault would send someone looking in the wrong place.
func TestAnUnreadableAnchorIsReportedAsItself(t *testing.T) {
	dir := filepath.Join(t.TempDir(), ".luna")
	if err := os.MkdirAll(dir, 0o750); err != nil {
		t.Fatalf("setting up: %v", err)
	}
	// Unsearchable, so stating the anchor inside fails with a permission error
	// rather than with "not there".
	if err := os.Chmod(dir, 0o000); err != nil {
		t.Skipf("cannot drop permissions here: %v", err)
	}
	t.Cleanup(func() { _ = os.Chmod(dir, 0o700) })

	err := node.EnsureDurable(dir)
	if err == nil {
		t.Fatal("an unreadable anchor must be reported")
	}
	if errors.Is(err, node.ErrGhostStore) {
		t.Errorf("a permission fault is not a ghost store, got %v", err)
	}
}

// TestAStaleAnchorIsIgnored covers an anchor naming some other directory: it is
// stale or was tampered with, and either way says nothing about this log.
func TestAStaleAnchorIsIgnored(t *testing.T) {
	root := t.TempDir()
	gitDir := filepath.Join(root, ".git")
	if err := os.Mkdir(gitDir, 0o750); err != nil {
		t.Fatalf("setting up the repository: %v", err)
	}
	if err := os.WriteFile(filepath.Join(gitDir, "luna-log-location"),
		[]byte("/somewhere/else\n"), 0o600); err != nil {
		t.Fatalf("writing the stale anchor: %v", err)
	}

	if err := node.EnsureDurable(filepath.Join(root, ".luna")); err != nil {
		t.Fatalf("an anchor naming another directory must be ignored, got %v", err)
	}
}

// TestAnUnwritableDirectoryFailsPlainly covers the path where Luna cannot create
// the log directory at all.
func TestAnUnwritableDirectoryFailsPlainly(t *testing.T) {
	root := t.TempDir()
	if err := os.Chmod(root, 0o500); err != nil {
		t.Skipf("cannot make the directory read-only here: %v", err)
	}
	t.Cleanup(func() { _ = os.Chmod(root, 0o700) })

	err := node.EnsureDurable(filepath.Join(root, ".luna"))
	if err == nil {
		t.Fatal("a directory that cannot be created must be reported")
	}
	if errors.Is(err, node.ErrGhostStore) {
		t.Errorf("a permission fault is not a ghost store, got %v", err)
	}
}

// TestAWorktreeGitFileIsNotARepositoryRoot covers `.git` as a file rather than a
// directory, which is what a linked worktree has. It is not the main checkout, so
// there is no anchor to look for there.
func TestAWorktreeGitFileIsNotARepositoryRoot(t *testing.T) {
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, ".git"),
		[]byte("gitdir: /elsewhere/.git/worktrees/w\n"), 0o600); err != nil {
		t.Fatalf("setting up the worktree marker: %v", err)
	}

	if err := node.EnsureDurable(filepath.Join(root, ".luna")); err != nil {
		t.Fatalf("a worktree's .git file is not this failure, got %v", err)
	}
}

// TestTheAnchorSurvivesAnUnwritableGitDirectory covers the best-effort half: a
// repository Luna cannot write to still gets a working log.
func TestTheAnchorSurvivesAnUnwritableGitDirectory(t *testing.T) {
	root := t.TempDir()
	gitDir := filepath.Join(root, ".git")
	if err := os.Mkdir(gitDir, 0o500); err != nil {
		t.Fatalf("setting up the read-only repository: %v", err)
	}
	t.Cleanup(func() { _ = os.Chmod(gitDir, 0o700) })

	if err := node.EnsureDurable(filepath.Join(root, ".luna")); err != nil {
		t.Fatalf("a log must still work beside a read-only .git, got %v", err)
	}
}

// TestAnchoredAndVisibleIsTheOrdinaryCase covers the path every real run takes on
// its second command: the anchor names this directory and the directory is there.
func TestAnchoredAndVisibleIsTheOrdinaryCase(t *testing.T) {
	root := t.TempDir()
	if err := os.Mkdir(filepath.Join(root, ".git"), 0o750); err != nil {
		t.Fatalf("setting up the repository: %v", err)
	}
	dir := filepath.Join(root, ".luna")

	if err := node.EnsureDurable(dir); err != nil {
		t.Fatalf("the first run: %v", err)
	}
	// The anchor beside the log short-circuits, so remove it to reach the
	// git-directory comparison deliberately. The log directory is still there and
	// the git anchor still names it, which is the visible-and-anchored case.
	if err := os.Remove(filepath.Join(dir, ".luna-anchor")); err != nil {
		t.Fatalf("setting up the second run: %v", err)
	}

	if err := node.EnsureDurable(dir); err != nil {
		t.Fatalf("an anchored directory that is visible must pass, got %v", err)
	}
}
