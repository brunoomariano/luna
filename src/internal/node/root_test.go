package node

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// repoWithWorktree builds a real repository with a linked worktree beside it,
// and returns both paths.
//
// Real git rather than a fake: the subject is which directory git calls the root
// from inside a worktree, and a stub would be testing the stub. This is the same
// reasoning the node package's own tests already follow.
func repoWithWorktree(t *testing.T) (main, worktree string) {
	t.Helper()

	base := t.TempDir()
	main = filepath.Join(base, "app")

	runGit(t, base, "init", "-q", "app")
	runGit(t, main, "config", "user.email", "test@example.invalid")
	runGit(t, main, "config", "user.name", "Test")
	writeFileAt(t, main, "f.txt", "first")
	runGit(t, main, "add", ".")
	runGit(t, main, "commit", "-qm", "init")

	worktree = filepath.Join(base, "wt-app-LUNA-1-reviewer")
	runGit(t, main, "worktree", "add", "-q", "--detach", worktree, "HEAD")
	return main, worktree
}

func runGit(t *testing.T, dir string, args ...string) {
	t.Helper()

	cmd := exec.Command("git", args...)
	cmd.Dir = dir
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("git %s: %v: %s", strings.Join(args, " "), err, out)
	}
}

func writeFileAt(t *testing.T, dir, name, body string) {
	t.Helper()

	if err := os.WriteFile(filepath.Join(dir, name), []byte(body), 0o600); err != nil {
		t.Fatalf("writing %s: %v", name, err)
	}
}

// TestTheCentralLogDoesNotFollowAWorktree is the defect this fixes.
//
// A stage runs in an ephemeral worktree that is deleted when it ends.
// Resolving the log from the working directory put a second database inside that
// worktree — measured, not theorised: two `.luna/luna.db` files, and the task in
// the main repository invisible from the other.
func TestTheCentralLogDoesNotFollowAWorktree(t *testing.T) {
	main, worktree := repoWithWorktree(t)

	fromMain, err := DefaultPath(context.Background(), main)
	if err != nil {
		t.Fatalf("resolving from the main checkout: %v", err)
	}
	fromWorktree, err := DefaultPath(context.Background(), worktree)
	if err != nil {
		t.Fatalf("resolving from the worktree: %v", err)
	}

	if fromMain != fromWorktree {
		t.Errorf("the worktree got its own log:\n  main:     %s\n  worktree: %s\n"+
			"a log inside an ephemeral checkout is deleted with it", fromMain, fromWorktree)
	}
	// And it is outside both, which is the move: a log inside any checkout is a
	// log an agent working in that checkout can reach.
	if strings.HasPrefix(fromMain, main) || strings.HasPrefix(fromMain, worktree) {
		t.Errorf("the log is inside a checkout: %s", fromMain)
	}
}

// TestShowToplevelWouldHaveBeenWrong records why `--git-common-dir` is the call.
//
// `--show-toplevel` is the obvious one and it answers a different question —
// "which checkout am I in" — which is exactly what must not decide where shared
// state lives. This asserts the two genuinely disagree, so the choice is checked
// rather than remembered.
func TestShowToplevelWouldHaveBeenWrong(t *testing.T) {
	main, worktree := repoWithWorktree(t)

	top, err := git(context.Background(), worktree, "rev-parse", "--path-format=absolute", "--show-toplevel")
	if err != nil {
		t.Fatalf("rev-parse: %v", err)
	}
	root, err := Root(context.Background(), worktree)
	if err != nil {
		t.Fatalf("Root: %v", err)
	}

	if filepath.Clean(top) == filepath.Clean(root) {
		t.Skip("git reports the same path for both here; the distinction this guards does not exist")
	}
	if filepath.Clean(root) != filepath.Clean(main) {
		t.Errorf("Root = %s, want the main repository %s", root, main)
	}
}

// TestAWorktreeIsRecognisedAsOne is the signal Luna can actually get. No
// environment variable: an agent inherits and can unset the environment, and it
// cannot make git lie about where it is standing.
func TestAWorktreeIsRecognisedAsOne(t *testing.T) {
	main, worktree := repoWithWorktree(t)

	if !InsideAWorktree(context.Background(), worktree) {
		t.Error("a linked worktree was not recognised as one")
	}
	if InsideAWorktree(context.Background(), main) {
		t.Error("the main checkout was mistaken for a worktree")
	}
	if InsideAWorktree(context.Background(), t.TempDir()) {
		t.Error("a plain directory was mistaken for a worktree")
	}
}

// TestADirectoryWithNoRepositoryStillWorks. Luna runs outside a checkout too,
// and refusing there would stop it working anywhere that is not a repository.
func TestADirectoryWithNoRepositoryStillWorks(t *testing.T) {
	dir := t.TempDir()

	got, err := DefaultPath(context.Background(), dir)
	if err != nil {
		t.Fatalf("DefaultPath outside a repository: %v", err)
	}
	if filepath.Base(got) != "luna.db" || filepath.Base(filepath.Dir(got)) != "luna" {
		t.Errorf("path = %s, want the central Luna database", got)
	}
}

// TestTheLogSitsUnderTheDataHome pins the layout rather than leaving it implied.
//
// Where the log lives is a fact other things depend on — a person looking for it
// looks there, and the daemon that will own it has to find the same place.
func TestTheLogSitsUnderTheDataHome(t *testing.T) {
	t.Setenv("XDG_DATA_HOME", t.TempDir())
	main, _ := repoWithWorktree(t)

	got, err := DefaultPath(context.Background(), main)
	if err != nil {
		t.Fatalf("DefaultPath: %v", err)
	}

	home, err := DataHome()
	if err != nil {
		t.Fatalf("DataHome: %v", err)
	}
	if got != filepath.Join(home, "luna.db") {
		t.Errorf("path = %s, want %s", got, filepath.Join(home, "luna.db"))
	}
	if filepath.Base(got) != "luna.db" {
		t.Errorf("path = %s, want the store file", got)
	}
}

// TestARelativeDataHomeIsRefused. The point of this path is that it does not
// depend on where a command was run, so a relative value is the one thing it
// cannot be — resolving it against the working directory would put a project's
// log somewhere different for every caller.
func TestARelativeDataHomeIsRefused(t *testing.T) {
	t.Setenv("XDG_DATA_HOME", "relative/share")

	if _, err := DataHome(); err == nil {
		t.Fatal("a relative XDG_DATA_HOME was accepted")
	}
}

// TestAPathBelowTheRootStillFindsIt. Luna is run from wherever a person happens
// to be standing, which is usually not the top of the repository.
func TestAPathBelowTheRootStillFindsIt(t *testing.T) {
	main, _ := repoWithWorktree(t)

	deep := filepath.Join(main, "a", "b")
	if err := os.MkdirAll(deep, 0o750); err != nil {
		t.Fatalf("making a subdirectory: %v", err)
	}

	got, err := Root(context.Background(), deep)
	if err != nil {
		t.Fatalf("Root: %v", err)
	}
	if got != main {
		t.Errorf("Root = %s, want the repository root %s", got, main)
	}
}

// TestTheDataHomeFollowsTheEnvironmentThenTheDefault covers both branches, and
// the reason there are two: XDG says where a person's data lives, and the
// fallback is what every machine has when they have not said.
func TestTheDataHomeFollowsTheEnvironmentThenTheDefault(t *testing.T) {
	set := t.TempDir()
	t.Setenv("XDG_DATA_HOME", set)

	home, err := DataHome()
	if err != nil {
		t.Fatalf("DataHome: %v", err)
	}
	if home != filepath.Join(set, "luna") {
		t.Errorf("home = %s, want it under %s", home, set)
	}

	// Unset, it is the conventional path rather than a refusal — a machine with
	// no XDG_DATA_HOME is the ordinary machine.
	t.Setenv("XDG_DATA_HOME", "")
	home, err = DataHome()
	if err != nil {
		t.Fatalf("DataHome with nothing set: %v", err)
	}
	if !strings.HasSuffix(home, filepath.Join(".local", "share", "luna")) {
		t.Errorf("home = %s, want the conventional path", home)
	}
}

// TestARelativeDataHomeStopsThePathFromResolving is the same refusal seen from
// the caller that matters: nothing should get a log path at all if the location
// would depend on where the command was run.
func TestARelativeDataHomeStopsThePathFromResolving(t *testing.T) {
	t.Setenv("XDG_DATA_HOME", "share")

	if _, err := DefaultPath(context.Background(), t.TempDir()); err == nil {
		t.Fatal("a log path resolved under a relative data home")
	}
}
