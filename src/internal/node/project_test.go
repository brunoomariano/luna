package node

import (
	"context"
	"path/filepath"
	"strings"
	"testing"
)

// TestTheFormsOfOneRemoteAreOneProject is the claim the whole scheme rests on.
//
// A worktree opened to review a task has to see that task, and a second clone
// made to work on a branch is the same work. What both agree on is the remote —
// but they may spell it differently, and every one of those differences is
// punctuation rather than identity.
func TestTheFormsOfOneRemoteAreOneProject(t *testing.T) {
	same := []string{
		"git@github.com:brunoomariano/luna.git",
		"git@github.com:brunoomariano/luna",
		"https://github.com/brunoomariano/luna.git",
		"https://github.com/brunoomariano/luna",
		"https://github.com/brunoomariano/luna/",
		"ssh://git@github.com/brunoomariano/luna.git",
		"https://someone@github.com/brunoomariano/luna.git",
		"git@github.com:brunoomariano/Luna.git",
	}

	want := NormalizeRemote(same[0])
	for _, url := range same[1:] {
		if got := NormalizeRemote(url); got != want {
			t.Errorf("%q normalises to %q, want %q", url, got, want)
		}
	}
	if want != "github.com/brunoomariano/luna" {
		t.Errorf("the normal form is %q", want)
	}
}

// TestTwoProjectsThatShareANameStayApart is the other direction, and it is the
// one that costs something to get wrong: merging them would put one project's
// tasks in another's log.
func TestTwoProjectsThatShareANameStayApart(t *testing.T) {
	apart := []string{
		"git@github.com:me/app.git",
		"git@gitlab.com:me/app.git",
		"git@github.com:someone-else/app.git",
		"git@github.com:me/app-two.git",
	}

	seen := map[string]string{}
	for _, url := range apart {
		normal := NormalizeRemote(url)
		key := keyOf(normal, normal)
		if first, clash := seen[key]; clash {
			t.Errorf("%q and %q share the key %q", first, url, key)
		}
		seen[key] = url
	}
}

// TestTheKeyIsReadableAndStillUnique. A key nobody can recognise makes the
// projects directory useless to a person; a key that is only readable lets two
// projects collide. It is both, which is what the hash is for.
func TestTheKeyIsReadableAndStillUnique(t *testing.T) {
	normal := NormalizeRemote("git@github.com:brunoomariano/luna.git")
	key := keyOf(normal, normal)

	if !strings.Contains(key, "github.com-brunoomariano-luna") {
		t.Errorf("the key is not recognisable: %q", key)
	}
	if key == "github.com-brunoomariano-luna" {
		t.Error("the key carries no hash, so two flattening to it would collide")
	}

	// A remote long enough to be truncated still gets a key of its own, because
	// the hash is taken over what identifies rather than over what is printed.
	long := strings.Repeat("verylongsegment/", 8)
	if keyOf(long+"one", long+"one") == keyOf(long+"two", long+"two") {
		t.Error("two remotes that truncate to the same prefix share a key")
	}
}

// TestARepositoryWithNoRemoteIsKeyedByItsPath, and two of them do not collide
// just because they are called the same thing.
//
// Hashing the base name alone was the first attempt: two checkouts both called
// `app` would have shared a log, which is the failure this scheme exists to
// prevent between projects and must not introduce within them.
func TestARepositoryWithNoRemoteIsKeyedByItsPath(t *testing.T) {
	one := filepath.Join(t.TempDir(), "app")
	two := filepath.Join(t.TempDir(), "app")

	if pathKey(one) == pathKey(two) {
		t.Errorf("two repositories called app share the key %q", pathKey(one))
	}
	if !strings.HasPrefix(pathKey(one), "app-") {
		t.Errorf("the key is not recognisable: %q", pathKey(one))
	}
}

// TestAWorktreeBelongsToTheProjectItWasMadeFrom is the property review depends
// on: a linked worktree resolves to the same project as its main checkout.
func TestAWorktreeBelongsToTheProjectItWasMadeFrom(t *testing.T) {
	repo := identifiable(t)
	if _, err := git(context.Background(), repo, "remote", "add", "origin",
		"git@github.com:me/app.git"); err != nil {
		t.Fatalf("adding a remote: %v", err)
	}

	wt, err := OpenWorktree(context.Background(), repo, "LUNA-1", "review", "")
	if err != nil {
		t.Fatalf("opening the worktree: %v", err)
	}
	t.Cleanup(func() { _ = CloseWorktree(context.Background(), repo, wt) })

	main, err := IdentifyProject(context.Background(), repo)
	if err != nil {
		t.Fatalf("identifying the main checkout: %v", err)
	}
	linked, err := IdentifyProject(context.Background(), wt.Path)
	if err != nil {
		t.Fatalf("identifying the worktree: %v", err)
	}

	if main.Key != linked.Key {
		t.Errorf("a worktree landed in another project: %q against %q", linked.Key, main.Key)
	}
	if !main.Remote {
		t.Error("a repository with a remote is keyed by it")
	}
}

// TestTwoClonesOfOneRepositoryShareAProject is the same claim across checkouts
// rather than worktrees — the second half of what "by URL" buys.
func TestTwoClonesOfOneRepositoryShareAProject(t *testing.T) {
	var keys []string
	for _, spelling := range []string{
		"git@github.com:me/app.git",
		"https://github.com/me/app",
	} {
		repo := t.TempDir()
		for _, args := range [][]string{
			{"init", "-q", "-b", "main"},
			{"remote", "add", "origin", spelling},
		} {
			if _, err := git(context.Background(), repo, args...); err != nil {
				t.Fatalf("git %v: %v", args, err)
			}
		}
		p, err := IdentifyProject(context.Background(), repo)
		if err != nil {
			t.Fatalf("identifying: %v", err)
		}
		keys = append(keys, p.Key)
	}

	if keys[0] != keys[1] {
		t.Errorf("two clones of one repository landed in %q and %q", keys[0], keys[1])
	}
}

// TestADirectoryThatIsNotARepositoryStillHasAProject. Luna runs in plain
// directories, and the honest answer there is one project for that directory.
func TestADirectoryThatIsNotARepositoryStillHasAProject(t *testing.T) {
	p, err := IdentifyProject(context.Background(), t.TempDir())
	if err != nil {
		t.Fatalf("a plain directory was refused: %v", err)
	}
	if p.Remote {
		t.Error("a directory with no repository reported a remote")
	}
	if !strings.Contains(p.String(), "no remote") {
		t.Errorf("the description hides that there is no remote: %q", p.String())
	}
}

// TestAProjectNamesItselfByItsRemote covers the description a listing prints:
// with a remote it is the remote, and without one it says so rather than showing
// a path that looks like one.
func TestAProjectNamesItselfByItsRemote(t *testing.T) {
	remote := Project{Key: "k", From: "github.com/me/app", Remote: true}
	if remote.String() != "github.com/me/app" {
		t.Errorf("a project with a remote names it, got %q", remote.String())
	}

	local := Project{Key: "k", From: "/repos/app"}
	if !strings.Contains(local.String(), "/repos/app") {
		t.Errorf("a project with no remote names its path, got %q", local.String())
	}
}

// TestAKeyIsNeverEmpty. A remote that flattens to nothing — punctuation only —
// would otherwise produce a directory named by its hash alone, which is a
// listing nobody can read.
func TestAKeyIsNeverEmpty(t *testing.T) {
	key := keyOf("///", "///")

	if !strings.HasPrefix(key, "project-") {
		t.Errorf("a key with no readable half is %q", key)
	}
}
