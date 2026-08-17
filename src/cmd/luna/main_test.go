package main

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/brunoomariano/luna/src/internal/cli"
)

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

	path, err := storePath(context.Background())
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if path != "/tmp/somewhere/luna.db" {
		t.Errorf("want the environment's path, got %q", path)
	}
}

// TestStorePathDefaultsToTheRepositoryRoot covers the fallback.
//
// Per-repository rather than per-directory: a stage runs in an ephemeral
// worktree, so resolving from the cwd would put the log somewhere that is about
// to be deleted (ADR-0057).
func TestStorePathDefaultsToTheRepositoryRoot(t *testing.T) {
	t.Setenv("LUNA_STORE", "")

	path, err := storePath(context.Background())
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if filepath.Base(path) != "luna.db" {
		t.Errorf("want the store file, got %q", path)
	}
	if !strings.HasSuffix(filepath.Dir(path), ".luna") {
		t.Errorf("want it under .luna, got %q", path)
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

// TestRunFromAWorktreeUsesTheMainRepositorysLog is the defect ADR-0057 fixes,
// exercised end to end through `run` rather than through the resolver alone.
//
// Before it, running from a worktree created a second `.luna/luna.db` inside a
// checkout that is deleted when the stage ends — so the task went with it.
func TestRunFromAWorktreeUsesTheMainRepositorysLog(t *testing.T) {
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

	// LUNA_STORE would win, and this is testing what happens without it.
	t.Setenv("LUNA_STORE", "")

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
// machine, and a knob raised past a gate's criticality quietly sent it to a
// person. Both are the correct behaviour for a missing model — which is exactly
// why nothing noticed for two commits.
//
// So this asserts on the assembled environment rather than on any one command:
// a dependency added to Env and forgotten here is one the binary does not have.
func TestEveryInjectedDependencyIsWired(t *testing.T) {
	env := environment(nil, "/tmp/stock", cli.Config{}, "/tmp/root")

	missing := map[string]bool{
		"Out":       env.Out == nil,
		"Err":       env.Err == nil,
		"In":        env.In == nil,
		"Edit":      env.Edit == nil,
		"Interpret": env.Interpret == nil,
		"Lead":      env.Lead == nil,
		"Notify":    env.Notify == nil,
	}

	for field, absent := range missing {
		if absent {
			t.Errorf("Env.%s is nil in the real binary — the feature that reads it "+
				"is off on every machine, and it fails safe so nothing says so", field)
		}
	}
}
