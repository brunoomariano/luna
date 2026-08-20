package agent

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"
)

// TestAskSendsThePromptAndReadsTheAnswer is the whole contract of Ask: a
// question in, what the model said out.
func TestAskSendsThePromptAndReadsTheAnswer(t *testing.T) {
	fake := newFakeHarness(t, "the gate should pass", 0)
	h := Harness{Binary: fake.path()}

	answer, err := h.Ask(context.Background(), "does this contract cover the criteria?")
	if err != nil {
		t.Fatalf("asking: %v", err)
	}
	if !strings.Contains(answer, "the gate should pass") {
		t.Errorf("the answer did not come back: %q", answer)
	}
	if stdin := fake.stdin(t); !strings.Contains(stdin, "does this contract cover the criteria?") {
		t.Errorf("the prompt did not reach the harness on stdin, got %q", stdin)
	}
}

// TestAskDoesNotGoThroughTheSandbox is the difference between Ask and Run, as a
// test rather than a comment.
//
// Run refuses to start without a sandbox because it is starting an agent that
// will write code. Ask is the lead reading state and answering a question; it
// runs where Luna runs. If this ever starts requiring one, the lead stops
// working on every machine at once and INV-4 gains a caller it was not written
// for.
func TestAskDoesNotGoThroughTheSandbox(t *testing.T) {
	fake := newFakeHarness(t, "answered", 0)
	h := Harness{Binary: fake.path()} // no Sandbox

	if _, err := h.Ask(context.Background(), "anything"); err != nil {
		t.Fatalf("Ask must not require a sandbox: %v", err)
	}

	// And the other half: the same empty Sandbox refuses a Run.
	if _, err := h.Run(context.Background(), Call{Kind: "claude", Prompt: "x"}); err == nil {
		t.Error("Run without a sandbox must be refused — that is INV-4's floor")
	}
}

// TestAHarnessThatExitsZeroSayingNothingIsAnError is the measurement this guard
// was written from.
//
// Against opencode 1.17.7, `--print` is not a flag it has: rather than refusing,
// it printed its banner and exited 0. The empty answer read as a successful turn
// and whoever asked got a blank line. Exit code zero is not proof that a turn
// happened.
func TestAHarnessThatExitsZeroSayingNothingIsAnError(t *testing.T) {
	fake := newFakeHarness(t, "", 0)
	h := Harness{Binary: fake.path()}

	_, err := h.Ask(context.Background(), "anything")
	if err == nil {
		t.Fatal("a harness that exits 0 and says nothing must be an error, not an empty answer")
	}
	if !strings.Contains(err.Error(), "without answering") {
		t.Errorf("the error must say what happened, got %q", err)
	}
}

// TestAskIsBounded keeps the deadline real. A model that has not answered is a
// turn that ends, not a command line that waits forever.
func TestAskIsBounded(t *testing.T) {
	fake := newSlowHarness(t, 30*time.Second)
	h := Harness{Binary: fake, Deadline: 100 * time.Millisecond}

	started := time.Now()
	_, err := h.Ask(context.Background(), "anything")
	if err == nil {
		t.Fatal("a harness that never answers must time out")
	}
	if elapsed := time.Since(started); elapsed > 5*time.Second {
		t.Errorf("the deadline did not bind: waited %s", elapsed)
	}
	if !strings.Contains(err.Error(), "did not answer within") {
		t.Errorf("the error must name the timeout, got %q", err)
	}
}

// TestACancelledAskIsNotReportedAsASlowModel keeps the two cancellations apart.
//
// "Someone stopped the run" and "the model ran out of time" are different facts,
// and reporting one as the other sends whoever reads it looking for a slow
// harness that was never slow.
func TestACancelledAskIsNotReportedAsASlowModel(t *testing.T) {
	fake := newSlowHarness(t, 30*time.Second)
	h := Harness{Binary: fake}

	ctx, stop := context.WithCancel(context.Background())
	go func() {
		time.Sleep(50 * time.Millisecond)
		stop()
	}()

	_, err := h.Ask(ctx, "anything")
	if err == nil {
		t.Fatal("a cancelled ask must report the cancellation")
	}
	if !strings.Contains(err.Error(), "stopped before it answered") {
		t.Errorf("a cancellation must not read as a timeout, got %q", err)
	}
}

// TestAskRefusesAnUnknownHarnessRatherThanGuessing keeps the table closed. A
// harness Luna guesses at fails in a way nobody sees until somebody is waiting
// on an answer.
func TestAskRefusesAnUnknownHarnessRatherThanGuessing(t *testing.T) {
	h := Harness{Kind: "gpt-whatever"}

	_, err := h.Ask(context.Background(), "anything")
	if !errors.Is(err, ErrNoHarness) {
		t.Fatalf("an unlisted harness must be refused as one, got %v", err)
	}
	if !strings.Contains(err.Error(), "gpt-whatever") {
		t.Errorf("the refusal must name the invalid value, got %q", err)
	}
}

// TestAnAskedHarnessErrorIsReadable. A failing harness reports why on stderr, and the
// end of it is where the reason is.
func TestAnAskedHarnessErrorIsReadable(t *testing.T) {
	fake := newFailingHarness(t, "not logged in")
	h := Harness{Binary: fake}

	_, err := h.Ask(context.Background(), "anything")
	if err == nil {
		t.Fatal("a harness that exits non-zero must be reported")
	}
	if !strings.Contains(err.Error(), "not logged in") {
		t.Errorf("the reason must survive into the error, got %q", err)
	}
}

// newSlowHarness is a harness that never answers within the test's patience: it
// sleeps and prints nothing.
//
// A script rather than a stub for the same reason as fakeHarness — what is under
// test is a real process being started and stopped, and a fake that skipped the
// process would prove nothing about either deadline.
func newSlowHarness(t *testing.T, sleep time.Duration) string {
	t.Helper()
	return writeScript(t, "slow-harness",
		"#!/bin/sh\nsleep "+strconv.Itoa(int(sleep.Seconds()))+"\necho too late\n")
}

// newFailingHarness is a harness that refuses, the way one that is not logged in
// refuses: a reason on stderr and a non-zero exit.
func newFailingHarness(t *testing.T, reason string) string {
	t.Helper()
	return writeScript(t, "failing-harness",
		"#!/bin/sh\necho '"+reason+"' >&2\nexit 1\n")
}

func writeScript(t *testing.T, name, body string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), name)
	if err := os.WriteFile(path, []byte(body), 0o755); err != nil {
		t.Fatalf("writing %s: %v", name, err)
	}
	return path
}

// TestAskCanRunTheCommandsItIsToldToRun is what makes an unattended lead
// possible at all.
//
// The lead's whole loop is Luna commands: `luna next` for the order, `luna done`
// to report. Without this the harness asks a person to approve each one, and
// there is no person — measured on TALLY-4, where the lead answered "the call
// needs your approval before it can run" and the task never left `setup`. Luna's
// own guard caught the stall correctly, which is how it was seen at all.
//
// What bounds the lead is not the harness's prompt. It is that no order gives it
// more than one stage, and nothing it says moves the flow: a transition happens
// because the reducer recorded one, never because the lead reported it. The
// prompt was protecting against a decision the lead cannot make.
func TestAskCanRunTheCommandsItIsToldToRun(t *testing.T) {
	fake := newFakeHarness(t, "answered", 0)
	h := Harness{Binary: fake.path()}

	if _, err := h.Ask(context.Background(), "anything"); err != nil {
		t.Fatalf("asking: %v", err)
	}

	argv := fake.argv(t)
	if !strings.Contains(argv, "bypassPermissions") {
		t.Errorf("a lead that cannot run `luna next` cannot conduct anything. argv was:\n%s", argv)
	}
}
