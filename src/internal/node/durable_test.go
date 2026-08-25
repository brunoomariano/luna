package node_test

import (
	"errors"
	"os"
	"path/filepath"
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
		t.Fatalf("the second run must recognise the directory, got %v", err)
	}
}

// TestAnExistingStoreIsAdopted is the regression for the guard's second false
// positive: "a database with nothing beside it" describes every store written
// before the guard existed — measured on this project's own log — and refusing
// that refuses adoption itself. What decides is the git directory, never the
// presence of the database.
func TestAnExistingStoreIsAdopted(t *testing.T) {
	dir := filepath.Join(t.TempDir(), ".luna")
	if err := os.MkdirAll(dir, 0o750); err != nil {
		t.Fatalf("setting up: %v", err)
	}
	if err := os.WriteFile(filepath.Join(dir, "luna.db"), []byte("real history"), 0o600); err != nil {
		t.Fatalf("setting up the old store: %v", err)
	}

	if err := node.EnsureDurable(dir); err != nil {
		t.Fatalf("an existing store must be adopted, got %v", err)
	}
}

// TestNothingIsLeftBesideTheLog. The guard used to drop a marker next to
// `luna.db` as a shortcut past its own check. It caught nothing on its own — the
// git directory is what detects the failure — so it was a file in every project
// for no guarantee.
func TestNothingIsLeftBesideTheLog(t *testing.T) {
	root := t.TempDir()
	if err := os.Mkdir(filepath.Join(root, ".git"), 0o750); err != nil {
		t.Fatalf("setting up the repository: %v", err)
	}
	dir := filepath.Join(root, ".luna")

	if err := node.EnsureDurable(dir); err != nil {
		t.Fatalf("the first run: %v", err)
	}

	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatalf("reading the log directory: %v", err)
	}
	for _, entry := range entries {
		t.Errorf("the guard left %q beside the log", entry.Name())
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

// TestABrokenDirectoryIsReportedAsItself pins the distinction between "this log
// is unreachable" and "this directory is broken". Blaming containment for a
// permission fault would send someone looking in the wrong place.
func TestABrokenDirectoryIsReportedAsItself(t *testing.T) {
	root := t.TempDir()
	if err := os.Chmod(root, 0o500); err != nil {
		t.Skipf("cannot drop permissions here: %v", err)
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

// TestRecordedAndVisibleIsTheOrdinaryCase covers the path every real run takes on
// its second command: the git directory names this log and the log is there.
//
// It reaches the comparison on every command now rather than skipping past it,
// which is the whole cost of removing the shortcut — one stat and one read.
func TestRecordedAndVisibleIsTheOrdinaryCase(t *testing.T) {
	root := t.TempDir()
	if err := os.Mkdir(filepath.Join(root, ".git"), 0o750); err != nil {
		t.Fatalf("setting up the repository: %v", err)
	}
	dir := filepath.Join(root, ".luna")

	if err := node.EnsureDurable(dir); err != nil {
		t.Fatalf("the first run: %v", err)
	}
	if err := node.EnsureDurable(dir); err != nil {
		t.Fatalf("a recorded directory that is visible must pass, got %v", err)
	}
}
