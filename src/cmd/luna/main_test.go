package main

import (
	"bytes"
	"errors"
	"fmt"
	"os"
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

	path, err := storePath()
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if path != "/tmp/somewhere/luna.db" {
		t.Errorf("want the environment's path, got %q", path)
	}
}

// TestStorePathDefaultsUnderTheWorkingDirectory covers the fallback.
//
// Per-directory rather than per-user: tasks belong to a project, and two projects
// sharing one log would list each other's gates.
func TestStorePathDefaultsUnderTheWorkingDirectory(t *testing.T) {
	t.Setenv("LUNA_STORE", "")

	path, err := storePath()
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
