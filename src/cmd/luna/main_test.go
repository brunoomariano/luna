package main

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/brunoomariano/luna/src/internal/node"
	"github.com/brunoomariano/luna/src/internal/store"

	"github.com/brunoomariano/luna/src/internal/cli"
	"github.com/brunoomariano/luna/src/internal/daemon"
)

func TestMain(m *testing.M) {
	original := spawnDaemon
	var servers []*daemon.Server
	spawnDaemon = func(socket, database, legacyRoot string) error {
		server, err := daemon.Listen(daemon.Options{
			Socket: socket, Store: database, LegacyRoot: legacyRoot,
		})
		if err == nil {
			servers = append(servers, server)
		}
		return err
	}
	code := m.Run()
	for _, server := range servers {
		_ = server.Close()
	}
	spawnDaemon = original
	os.Exit(code)
}

// TestExitCodeSeparatesUsageFromFailure covers what main does with an error.
//
// A script driving luna needs to tell "you typed it wrong" from "it went wrong":
// the first is worth retrying with different arguments, the second is not.
func TestExitCodeSeparatesUsageFromFailure(t *testing.T) {
	cases := []struct {
		name string
		err  error
		want int
	}{
		{"success", nil, 0},
		{"usage", fmt.Errorf("%w: unknown command", cli.ErrUsage), 2},
		{"failure", errors.New("the disk is full"), 1},
	}

	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			var stderr bytes.Buffer

			if got := exitCode(c.err, &stderr); got != c.want {
				t.Errorf("want exit %d, got %d", c.want, got)
			}

			// Success prints nothing; anything else has to say what happened, or
			// the exit code is all the user gets.
			if c.err == nil && stderr.Len() != 0 {
				t.Errorf("success should be quiet, got %q", stderr.String())
			}
			if c.err != nil && stderr.Len() == 0 {
				t.Error("a failure must say what went wrong")
			}
		})
	}
}

// TestStorePathPrefersTheEnvironment covers the override.
//
// It is what lets a test — or a person with several projects — point at a
// specific log rather than whatever the working directory implies.
func TestStorePathPrefersTheEnvironment(t *testing.T) {
	t.Setenv("LUNA_STORE", "/tmp/somewhere/luna.db")

	path, chosen, err := storePath(context.Background())
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if path != "/tmp/somewhere/luna.db" {
		t.Errorf("want the environment's path, got %q", path)
	}
	// And it says the path was named rather than derived, which is what stops a
	// log somewhere else from being moved into it.
	if !chosen {
		t.Error("a path from the environment is a chosen one")
	}
}

func TestStorePathMakesARelativeOverrideStableForTheDaemon(t *testing.T) {
	t.Setenv("LUNA_STORE", "state/luna.db")
	cwd, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	path, chosen, err := storePath(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if !chosen || path != filepath.Join(cwd, "state", "luna.db") {
		t.Fatalf("relative LUNA_STORE resolved to %q, chosen=%v", path, chosen)
	}
}

// TestStorePathDefaultsToTheCentralDatabase covers the fallback.
//
// The daemon owns one database for every project. Project identity scopes rows
// inside it rather than choosing another file, so a command from any checkout
// resolves the same path.
func TestStorePathDefaultsToTheCentralDatabase(t *testing.T) {
	t.Setenv("LUNA_STORE", "")
	t.Setenv("XDG_DATA_HOME", t.TempDir())

	path, chosen, err := storePath(context.Background())
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if chosen {
		t.Error("a derived path is not a chosen one")
	}

	if filepath.Base(path) != "luna.db" {
		t.Errorf("want the store file, got %q", path)
	}
	home, err := node.DataHome()
	if err != nil {
		t.Fatalf("resolving the data home: %v", err)
	}
	if path != filepath.Join(home, "luna.db") {
		t.Errorf("want the central store, got %q", path)
	}
	// And outside the checkout it was resolved from, which is the whole move.
	if strings.Contains(path, string(filepath.Separator)+".luna"+string(filepath.Separator)) {
		t.Errorf("the log is still inside a checkout: %q", path)
	}
	if !filepath.IsAbs(path) {
		t.Errorf("want an absolute path, got %q", path)
	}
}

// TestRunOpensTheStoreAndDispatches covers the wiring end to end.
//
// This is the one test that exercises main's actual job: find the store, open it,
// hand over the arguments. Everything below it is already covered in the cli
// package.
func TestRunOpensTheStoreAndDispatches(t *testing.T) {
	store := filepath.Join(t.TempDir(), "luna.db")
	t.Setenv("LUNA_STORE", store)

	if err := run([]string{"task", "new", "LUNA-1"}); err != nil {
		t.Fatalf("creating a task: %v", err)
	}

	if _, err := os.Stat(store); err != nil {
		t.Errorf("the store should exist after a write: %v", err)
	}

	// A second run against the same file sees what the first wrote — which is the
	// whole point of a store that outlives the process.
	if err := run([]string{"task", "show", "LUNA-1"}); err != nil {
		t.Errorf("reading it back: %v", err)
	}
}

// TestRunReportsAUsageError covers the error path main turns into an exit code.
func TestRunReportsAUsageError(t *testing.T) {
	t.Setenv("LUNA_STORE", filepath.Join(t.TempDir(), "luna.db"))

	if err := run([]string{"fly"}); err == nil {
		t.Error("an unknown command must be reported")
	}
}

// TestRunReportsAnUnopenableStore covers the failure before dispatch.
func TestRunReportsAnUnopenableStore(t *testing.T) {
	if os.Geteuid() == 0 {
		t.Skip("root ignores directory permissions, so this cannot be provoked")
	}

	locked := filepath.Join(t.TempDir(), "locked")
	if err := os.Mkdir(locked, 0o500); err != nil {
		t.Fatalf("preparing the fixture: %v", err)
	}
	t.Setenv("LUNA_STORE", filepath.Join(locked, "nested", "luna.db"))

	if err := run([]string{"gates"}); err == nil {
		t.Error("a store that cannot be opened must be reported")
	}
}

// TestRunFromAWorktreeUsesTheCentralLog is the defect the shared log location fixes,
// exercised end to end through `run` rather than through the resolver alone.
//
// Before it, running from a worktree created a second `.luna/luna.db` inside a
// checkout that is deleted when the stage ends — so the task went with it.
func TestRunFromAWorktreeUsesTheCentralLog(t *testing.T) {
	base := t.TempDir()
	main := filepath.Join(base, "app")
	worktree := filepath.Join(base, "wt-app-LUNA-1-reviewer")

	for _, args := range [][]string{
		{"init", "-q", "app"},
	} {
		cmd := exec.Command("git", args...)
		cmd.Dir = base
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Skipf("git is not usable here: %v: %s", err, out)
		}
	}
	for _, args := range [][]string{
		{"config", "user.email", "test@example.invalid"},
		{"config", "user.name", "Test"},
		{"commit", "-q", "--allow-empty", "-m", "init"},
		{"worktree", "add", "-q", "--detach", worktree, "HEAD"},
	} {
		cmd := exec.Command("git", args...)
		cmd.Dir = main
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v: %s", args, err, out)
		}
	}

	// LUNA_STORE would win, and this is testing what happens without it — which
	// means the log resolves to the data home, so the data home has to be this
	// test's. Without that a test run writes a project directory into whoever ran
	// it, which is how this was found.
	t.Setenv("LUNA_STORE", "")
	t.Setenv("XDG_DATA_HOME", t.TempDir())

	inDir(t, main, func() {
		if err := run([]string{"task", "new", "LUNA-1", "--kind", "feature"}); err != nil {
			t.Fatalf("creating the task from the main checkout: %v", err)
		}
	})

	inDir(t, worktree, func() {
		if err := run([]string{"task", "show", "LUNA-1"}); err != nil {
			t.Errorf("the task created in the main checkout is invisible from the "+
				"worktree — the log followed the working directory: %v", err)
		}
	})

	if _, err := os.Stat(filepath.Join(worktree, ".luna", "luna.db")); err == nil {
		t.Error("a second log was created inside the worktree, which is deleted " +
			"when the stage ends")
	}
}

// inDir runs fn with the working directory changed, and puts it back.
func inDir(t *testing.T, dir string, fn func()) {
	t.Helper()

	was, err := os.Getwd()
	if err != nil {
		t.Fatalf("reading the working directory: %v", err)
	}
	if err := os.Chdir(dir); err != nil {
		t.Fatalf("entering %s: %v", dir, err)
	}
	defer func() {
		if err := os.Chdir(was); err != nil {
			t.Fatalf("returning to %s: %v", was, err)
		}
	}()

	fn()
}

// TestEveryInjectedDependencyIsWired is the test that was missing.
//
// `Env` is a struct of injected functions, and a field left nil fails *safe*
// rather than loudly: `luna lead` reported "no lead is configured" on every real
// machine, and a knob raised past a gate's autonomy floor quietly sent it to a
// person. Both are the correct behaviour for a missing model — which is exactly
// why nothing noticed for two commits.
//
// So this asserts on the assembled environment rather than on any one command:
// a dependency added to Env and forgotten here is one the binary does not have.
func TestEveryInjectedDependencyIsWired(t *testing.T) {
	// `Edit` is the one field whose wiring depends on the machine: `cli.Editor`
	// answers nil when no editor is configured anywhere, which is deliberate —
	// the command names what to set instead of launching nothing. So a bare
	// assertion here passed on a developer's machine, where $EDITOR is set, and
	// failed on CI, where it is not. Configuring one makes this ask what it means
	// to ask: is the field wired, rather than is this machine set up.
	t.Setenv("EDITOR", "true")

	env := environment(nil, cli.Config{}, "/tmp/root")

	// Reflected rather than listed, because a hand-written list is the drift this
	// test exists to catch: it would have to be updated by whoever adds a field,
	// which is exactly the person who just forgot to wire one. It held a stale
	// `Interpret` entry after that field was removed, and would have gone on
	// passing while a newly added field sat nil.
	//
	// Only nilable kinds are asked about. A string or a struct has no nil to be,
	// and `Store` is passed in by the caller rather than built here.
	value := reflect.ValueOf(env)
	for i := range value.NumField() {
		field := value.Type().Field(i)
		switch field.Type.Kind() {
		case reflect.Func, reflect.Interface, reflect.Pointer, reflect.Map, reflect.Slice:
		default:
			continue
		}
		if field.Name == "Store" || field.Name == "GlobalStore" {
			continue
		}
		if value.Field(i).IsNil() {
			t.Errorf("Env.%s is nil in the real binary — the feature that reads it "+
				"is off on every machine, and it fails safe so nothing says so", field.Name)
		}
	}
}

// TestWithNoEditorConfiguredEditIsAbsent is the other half of the field above.
//
// The wiring test sets an editor, so on its own it would pass just as well if
// `cli.Editor` always answered a function. What makes nil the right answer for an
// unconfigured machine is that `luna gate adjust` can then say which variable to
// set, instead of launching nothing and reporting a crash — and that only holds
// while something proves the nil is real.
func TestWithNoEditorConfiguredEditIsAbsent(t *testing.T) {
	// All four sources `resolveEditor` consults, emptied: this is a machine where
	// nobody ever set one.
	for _, name := range []string{"LUNA_EDITOR", "EDITOR", "VISUAL"} {
		t.Setenv(name, "")
	}

	env := environment(nil, cli.Config{}, "/tmp/root")

	if env.Edit != nil {
		t.Error("Env.Edit is wired with nothing configured — `gate adjust` would " +
			"launch nothing instead of naming what to set")
	}
}

// TestArtifactCommandsDoNotNeedTheLog is the regression for a bug a real agent
// found and diagnosed better than any test had.
//
// `luna artifact put` runs inside a stage's sandbox, where the log is
// deliberately out of reach: the agent hands its work to Luna through a socket
// and Luna is the only writer. Resolving the store first made every artifact
// command exit 1 from inside the jail, so a stage produced its contract, could
// not deliver it, and blocked — with the guard that refused working exactly as
// designed, aimed at the wrong command.
//
// The assertion is that the failure is about the socket rather than about the
// log: reaching the socket check at all means the store was never consulted.
func TestArtifactCommandsDoNotNeedTheLog(t *testing.T) {
	// A directory that is not a repository, so anything touching the store fails.
	dir := t.TempDir()
	t.Chdir(dir)
	t.Setenv("LUNA_ARTIFACT_SOCKET", "")

	err := run([]string{"artifact", "put", "contract"})
	if err == nil {
		t.Fatal("want a refusal with no socket set, got success")
	}
	if !strings.Contains(err.Error(), "LUNA_ARTIFACT_SOCKET") {
		t.Errorf("want the failure to be about the socket, got %q", err)
	}
}

// TestALogLeftInACheckoutIsAdoptedOnTheNextRun is phase one end to end.
//
// The log used to live inside the checkout. A build that simply looked in the new
// place would find nothing and start an empty log beside work somebody has open,
// and every command would then answer "no such task" about a task that exists.
func TestALogLeftInACheckoutIsAdoptedOnTheNextRun(t *testing.T) {
	t.Setenv("LUNA_STORE", "")
	data := t.TempDir()
	t.Setenv("XDG_DATA_HOME", data)

	repo := t.TempDir()
	for _, args := range [][]string{
		{"init", "-q", "-b", "main"},
		{"config", "user.email", "t@t"},
		{"config", "user.name", "t"},
	} {
		cmd := exec.Command("git", args...)
		cmd.Dir = repo
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v: %s", args, err, out)
		}
	}

	// A task written where an older build would have put it.
	old := filepath.Join(repo, ".luna", "luna.db")
	if err := os.MkdirAll(filepath.Dir(old), 0o750); err != nil {
		t.Fatalf("making the old log directory: %v", err)
	}
	inDir(t, repo, func() {
		t.Setenv("LUNA_STORE", old)
		if err := run([]string{"task", "new", "OLD-1", "--kind", "chore"}); err != nil {
			t.Fatalf("seeding the old log: %v", err)
		}
	})

	// And now a run that resolves the location itself.
	inDir(t, repo, func() {
		t.Setenv("LUNA_STORE", "")
		if err := run([]string{"task", "show", "OLD-1"}); err != nil {
			t.Errorf("the task in the adopted log is invisible: %v", err)
		}
	})

	if _, err := os.Stat(old); err == nil {
		t.Error("the log is still in the checkout")
	}
	if _, err := os.Stat(filepath.Join(repo, ".luna", ".gitignore")); err != nil {
		t.Error("what Luna still leaves in the checkout is not ignored")
	}
}

// TestAnUnreadableLegacyLogStopsTheMigration. The daemon cannot archive a file
// until it has proved that its history reached the central store.
func TestAnUnreadableLegacyLogStopsTheMigration(t *testing.T) {
	t.Setenv("LUNA_STORE", "")
	t.Setenv("XDG_DATA_HOME", t.TempDir())

	repo := t.TempDir()
	cmd := exec.Command("git", "init", "-q", "-b", "main")
	cmd.Dir = repo
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("git init: %v: %s", err, out)
	}

	// The new log first, so it is already there — adoption with only an old log
	// moves it, and the two-log case is the one where both exist.
	inDir(t, repo, func() {
		if err := run([]string{"task", "new", "NEW-1", "--kind", "chore"}); err != nil {
			t.Fatalf("seeding the new log: %v", err)
		}
	})

	old := filepath.Join(repo, ".luna", "luna.db")
	if err := os.WriteFile(old, []byte("an older build's log"), 0o600); err != nil {
		t.Fatalf("writing the old log: %v", err)
	}

	inDir(t, repo, func() {
		err := run([]string{"task", "show", "NEW-1"})
		if err == nil {
			t.Fatal("a checkout with two logs was resolved anyway")
		}
		for _, want := range []string{old, "not a database"} {
			if !strings.Contains(err.Error(), want) {
				t.Errorf("the refusal does not carry %q: %v", want, err)
			}
		}
	})
}

// TestACommandGetsAStoreItDoesNotOwn is phase two's central claim.
//
// The ownership rule was a constant checked inside one process. It is a process
// boundary now: a command opens the log read-only and forwards its appends to the
// daemon, so exactly one process holds the file open for writing. A command that
// owned the file would be the old arrangement wearing the new name.
func TestACommandGetsAStoreItDoesNotOwn(t *testing.T) {
	t.Setenv("LUNA_STORE", filepath.Join(t.TempDir(), "luna.db"))
	t.Setenv("XDG_DATA_HOME", t.TempDir())

	s, _, _, err := openStore(context.Background())
	if err != nil {
		t.Fatalf("openStore: %v", err)
	}
	t.Cleanup(func() { _ = s.Close() })

	if s.As == store.LunaOwnsTheLog {
		t.Error("a command opened the log as its owner")
	}
	if s.Via == nil {
		t.Fatal("a command that does not own the log has nowhere to send an append")
	}
}

// TestTheDaemonDoesNotForwardToItself. It is the owner, so going through the
// ordinary prologue would hand it a read-only store pointing at a daemon — which
// is the process asking itself to do the thing it was asked to do.
func TestTheDaemonDoesNotForwardToItself(t *testing.T) {
	dir, err := os.MkdirTemp("/tmp", "luna-dm-")
	if err != nil {
		t.Fatalf("making a directory: %v", err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(dir) })

	// A socket path of its own, so this does not meet a daemon somebody else left
	// running.
	socket := filepath.Join(dir, "daemon.sock")

	done := make(chan error, 1)
	go func() { done <- run([]string{"daemon", "--socket", socket}) }()

	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		if _, err := os.Stat(socket); err == nil {
			break
		}
		time.Sleep(20 * time.Millisecond)
	}
	if _, err := os.Stat(socket); err != nil {
		t.Fatalf("the daemon never bound its socket: %v", err)
	}

	if err := syscall.Kill(syscall.Getpid(), syscall.SIGTERM); err != nil {
		t.Fatalf("signalling: %v", err)
	}
	select {
	case err := <-done:
		if err != nil {
			t.Errorf("the daemon did not stop cleanly: %v", err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("the daemon did not stop when told")
	}
}

// TestTheEnvironmentWiresWhatTheCommandsNeed. Every field here is something a
// command calls and none of them is checked anywhere else: a nil `Where` is a
// command that cannot infer a task, a nil `Lead` is a gate nothing can judge, and
// both fail at the point of use rather than at startup.
func TestTheEnvironmentWiresWhatTheCommandsNeed(t *testing.T) {
	t.Setenv("XDG_DATA_HOME", t.TempDir())

	s, err := store.Open(filepath.Join(t.TempDir(), "luna.db"))
	if err != nil {
		t.Fatalf("opening: %v", err)
	}
	t.Cleanup(func() { _ = s.Close() })

	env := environment(s, cli.Config{}, t.TempDir())

	if env.Store == nil {
		t.Error("the environment carries no store")
	}
	if env.Where == nil {
		t.Error("no resolver: a command could not infer the task from the directory")
	}
	if env.Lead == nil {
		t.Error("no lead: a gate the knob reaches could not be judged")
	}
	if env.Node == nil {
		t.Error("no node: nothing could run a stage")
	}

	// And the resolver answers about somewhere real rather than panicking on a
	// directory it cannot read.
	if _, err := env.Where(); err != nil {
		t.Errorf("the resolver does not work where it was built: %v", err)
	}
}

func TestStoreResolutionReportsStartupAndCheckoutFailures(t *testing.T) {
	t.Setenv("XDG_RUNTIME_DIR", t.TempDir())
	original := spawnDaemon
	spawnDaemon = func(_, _, _ string) error { return errors.New("cannot start") }
	t.Cleanup(func() { spawnDaemon = original })

	_, err := connectDaemon(storeOpening{
		path: filepath.Join(t.TempDir(), "central.db"), chosen: true,
	})
	if err == nil || !strings.Contains(err.Error(), "cannot start") {
		t.Fatalf("daemon startup error = %v", err)
	}

	repoFile := filepath.Join(t.TempDir(), "repo-is-a-file")
	if err := os.WriteFile(repoFile, []byte("x"), 0o600); err != nil {
		t.Fatal(err)
	}
	err = importCheckoutStore(daemon.Client{}, storeOpening{repo: repoFile})
	if err == nil || !strings.Contains(err.Error(), "checking legacy store") {
		t.Fatalf("checkout store lookup error = %v", err)
	}
}

func TestStoreResolutionReportsConfigurationErrors(t *testing.T) {
	t.Setenv("LUNA_STORE", "")
	t.Setenv("XDG_DATA_HOME", "relative")
	if _, err := resolveStoreOpening(context.Background()); err == nil {
		t.Fatal("a relative data home was accepted")
	}

	repo := t.TempDir()
	cmd := exec.Command("git", "init", "-q", "-b", "main")
	cmd.Dir = repo
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("git init: %v: %s", err, out)
	}
	t.Setenv("XDG_DATA_HOME", t.TempDir())
	configPath := cli.ConfigPath(repo)
	if err := os.MkdirAll(filepath.Dir(configPath), 0o750); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(configPath, []byte("not = valid = toml"), 0o600); err != nil {
		t.Fatal(err)
	}
	inDir(t, repo, func() {
		if _, err := resolveStoreOpening(context.Background()); err == nil {
			t.Fatal("a malformed project configuration was accepted")
		}
	})
}

func TestCommandsReportARemovedWorkingDirectory(t *testing.T) {
	was, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	parent := t.TempDir()
	gone := filepath.Join(parent, "gone")
	if err := os.Mkdir(gone, 0o750); err != nil {
		t.Fatal(err)
	}
	if err := os.Chdir(gone); err != nil {
		t.Fatal(err)
	}
	if err := os.Remove(gone); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Chdir(was) })
	t.Setenv("LUNA_STORE", "")

	if _, _, err := storePath(context.Background()); err == nil {
		t.Error("storePath accepted a removed working directory")
	}
	if _, err := resolveStoreOpening(context.Background()); err == nil {
		t.Error("resolveStoreOpening accepted a removed working directory")
	}
	if _, err := currentRoot(context.Background(), &store.Store{}, "central.db"); err == nil {
		t.Error("currentRoot accepted a removed working directory")
	}
	if _, err := environment(nil, cli.Config{}, "").Where(); err == nil {
		t.Error("the environment resolver accepted a removed working directory")
	}
}

func TestRunWithoutStoreLeavesAnEmptyCommandForUsageHandling(t *testing.T) {
	handled, err := runWithoutStore(nil)
	if handled || err != nil {
		t.Fatalf("empty command bypass = %v, %v", handled, err)
	}
}
