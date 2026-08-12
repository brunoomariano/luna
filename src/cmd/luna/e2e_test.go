package main_test

import (
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"testing"
)

// End-to-end through the built binary.
//
// Every other test calls cli.Run in-process, which is the right default: it is
// fast and it isolates. What it cannot cover is the wiring in main — where the
// store lives, how the config is read, what an exit code means — and the flow
// fingerprint of ADR-0046 lives exactly there, because a task's flow is recorded
// at creation by the command and checked at replay by the store.
//
// This drives the real binary against a real SQLite file, so a mistake in that
// wiring shows up as a broken command rather than as a passing unit test.

// luna builds the binary once and returns a runner bound to a fresh store.
func luna(t *testing.T) func(args ...string) (string, error) {
	t.Helper()

	dir := t.TempDir()
	binary := filepath.Join(dir, "luna")

	build := exec.Command("go", "build", "-o", binary, ".")
	if out, err := build.CombinedOutput(); err != nil {
		t.Fatalf("building the binary: %v\n%s", err, out)
	}

	// LUNA_STORE rather than a working directory: the point is to exercise the
	// path main resolves, and a temp dir keeps the test off the developer's own
	// store.
	store := filepath.Join(dir, "luna.db")

	return func(args ...string) (string, error) {
		cmd := exec.Command(binary, args...)
		cmd.Env = append(os.Environ(), "LUNA_STORE="+store)
		out, err := cmd.CombinedOutput()
		return string(out), err
	}
}

// TestE2EATaskRecordsTheFlowItWasBornUnder is ADR-0046 through the real binary.
//
// The fingerprint has to survive the whole round trip — written by `task new`,
// serialised into SQLite, read back by replay — and the only way to know the
// wiring holds is to make the binary do it.
func TestE2EATaskRecordsTheFlowItWasBornUnder(t *testing.T) {
	run := luna(t)

	out, err := run("task", "new", "LUNA-1", "--kind", "chore")
	if err != nil {
		t.Fatalf("creating a task: %v\n%s", err, out)
	}
	if !strings.Contains(out, "flow=") {
		t.Errorf("creation should report the flow the task was born under, got %q", out)
	}

	// The same fingerprint has to come back out of `flow check`, or the two ends
	// of the round trip disagree.
	checked, err := run("flow", "check")
	if err != nil {
		t.Fatalf("checking the flow: %v\n%s", err, checked)
	}

	born := field(out, "flow=")
	if born == "" {
		t.Fatalf("could not read the fingerprint out of %q", out)
	}
	if !strings.Contains(checked, born) {
		t.Errorf("the flow a task was born under (%s) must be the one check reports, got %q", born, checked)
	}
	if !strings.Contains(checked, "LUNA-1") {
		t.Errorf("an open task must be named by the check, got %q", checked)
	}
}

// TestE2EAbandonEndsATaskAndClearsTheWay covers the full operational loop of
// ADR-0046: a task that will not be finished stops counting as open.
func TestE2EAbandonEndsATaskAndClearsTheWay(t *testing.T) {
	run := luna(t)

	if out, err := run("task", "new", "LUNA-1", "--kind", "chore"); err != nil {
		t.Fatalf("creating: %v\n%s", err, out)
	}

	// Open: the flow cannot change.
	before, err := run("flow", "check")
	if err != nil {
		t.Fatalf("checking: %v\n%s", err, before)
	}
	if !strings.Contains(before, "still open") {
		t.Fatalf("setup: the task should be open, got %q", before)
	}

	if out, err := run("task", "abandon", "LUNA-1", "superseded"); err != nil {
		t.Fatalf("abandoning: %v\n%s", err, out)
	}

	after, err := run("flow", "check")
	if err != nil {
		t.Fatalf("checking again: %v\n%s", err, after)
	}
	if !strings.Contains(after, "no task is open") {
		t.Errorf("an abandoned task must stop blocking the flow, got %q", after)
	}

	// The log keeps it, with the reason: abandoning adds a fact, it does not
	// remove any (INV-core-2).
	shown, err := run("task", "show", "LUNA-1")
	if err != nil {
		t.Fatalf("showing an abandoned task: %v\n%s", err, shown)
	}
	if !strings.Contains(shown, "abandoned") || !strings.Contains(shown, "superseded") {
		t.Errorf("the ending and its reason must survive, got %q", shown)
	}
}

// TestE2EUsageErrorsExitTwo covers the exit codes a script depends on: telling
// "you typed it wrong" from "it went wrong" is the whole reason main separates
// them.
func TestE2EUsageErrorsExitTwo(t *testing.T) {
	run := luna(t)

	cases := []struct {
		what string
		args []string
		code int
	}{
		{"a subcommand that does not exist", []string{"task", "frobnicate"}, 2},
		{"abandoning without a reason", []string{"task", "abandon", "LUNA-1"}, 2},
		{"abandoning a task that was never opened", []string{"task", "abandon", "LUNA-9", "why"}, 1},
	}

	for _, c := range cases {
		out, err := run(c.args...)
		var code int
		var exit *exec.ExitError
		if errors.As(err, &exit) {
			code = exit.ExitCode()
		}
		if code != c.code {
			t.Errorf("%s: want exit %d, got %d (%s)", c.what, c.code, code, out)
		}
	}
}

// field reads `name<value>` out of a line of output, up to the next space or
// closing paren.
func field(out, name string) string {
	i := strings.Index(out, name)
	if i < 0 {
		return ""
	}
	rest := out[i+len(name):]
	return strings.TrimRight(strings.Fields(rest)[0], ")\n")
}

// TestE2ETwoProcessesCannotCorruptOneLog is ADR-0047 through two real processes.
//
// The in-package test uses two store handles, which is close but not the thing:
// this is what actually happens when someone approves a gate in one terminal
// while a run advances the same task in another. Before the conditional append,
// the second write landed and the log stopped replaying for good.
func TestE2ETwoProcessesCannotCorruptOneLog(t *testing.T) {
	run := luna(t)

	if out, err := run("task", "new", "LUNA-1", "--kind", "chore"); err != nil {
		t.Fatalf("creating: %v\n%s", err, out)
	}

	// `task new` leaves the task at the first gate, so two approvals of the same
	// gate is the collision: both processes read a task waiting, both decide to
	// approve, and only one of them can be right.
	var wg sync.WaitGroup
	results := make([]error, 2)
	outputs := make([]string, 2)
	for i := range results {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			outputs[i], results[i] = run("run", "LUNA-1", "--dry-run")
		}(i)
	}
	wg.Wait()

	// Whatever the two processes did to each other, the log has to still be
	// readable — that is the property the conditional append protects, and the one
	// that had no recovery when it was lost.
	shown, err := run("task", "show", "LUNA-1")
	if err != nil {
		t.Fatalf("the log must still replay after a collision: %v\n%s\n%s\n%s",
			err, shown, outputs[0], outputs[1])
	}
}
