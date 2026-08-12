package cli

import (
	"strings"
	"testing"

	"github.com/brunoomariano/luna/src/internal/fsm"
)

// TestFlowCheckReportsAnOpenTask covers the primary defence of ADR-0046.
//
// Changing the flow while a task is open rewrites how that task's history reads,
// so the rule is to stop and confirm nothing is in flight. This is the command
// that answers it.
func TestFlowCheckReportsAnOpenTask(t *testing.T) {
	h := newHarness(t)
	h.mustRun(t, "task", "new", "LUNA-1") // interactive: stops at the first gate

	out := h.mustRun(t, "flow", "check")

	if !strings.Contains(out, string(fsm.Fingerprint(fsm.DefaultFlow()))) {
		t.Errorf("the check should name the flow it is reporting on, got %q", out)
	}
	if !strings.Contains(out, "LUNA-1") {
		t.Errorf("an open task must be named, got %q", out)
	}
	if !strings.Contains(out, "still open") {
		t.Errorf("the answer to 'can I change the flow' must be legible, got %q", out)
	}
}

// TestFlowCheckIsClearWhenNothingIsOpen covers the other half of the answer.
func TestFlowCheckIsClearWhenNothingIsOpen(t *testing.T) {
	h := newHarness(t)
	h.mustRun(t, "task", "new", "LUNA-1")
	h.mustRun(t, "task", "abandon", "LUNA-1", "not doing this one")

	out := h.mustRun(t, "flow", "check")

	if !strings.Contains(out, "no task is open") {
		t.Errorf("want a clear all-clear, got %q", out)
	}
}

// TestFlowCheckNamesWhatNoLongerReplays is why the command exists rather than
// being a line in `luna gates`.
//
// A task written under another flow is skipped by the gate listing on purpose —
// one unreadable task must not hide every other task waiting on a person. This is
// where those surface, because somebody has to decide about them.
func TestFlowCheckNamesWhatNoLongerReplays(t *testing.T) {
	h := newHarness(t)

	// A task born under a flow this build does not have.
	stale := []fsm.Stage{{ID: "gone", Requires: []fsm.Artifact{fsm.TaskID}, Produces: []fsm.Artifact{"x"}}}
	if err := h.env.Store.AppendAction("LUNA-1", fsm.TaskCreated{
		Kind: fsm.KindChore, Flow: fsm.Fingerprint(stale),
	}); err != nil {
		t.Fatalf("seeding: %v", err)
	}

	out := h.mustRun(t, "flow", "check")

	if !strings.Contains(out, "no longer replay") {
		t.Errorf("an unreadable task must be reported, got %q", out)
	}
	if !strings.Contains(out, "abandon") {
		t.Errorf("the report must say what to do about it, got %q", out)
	}
}

// TestAbandonWorksOnATaskThatCannotBeReplayed is the reason abandon does not
// replay before acting.
//
// A task whose flow changed under it no longer replays at all, so a command that
// read the state first could never end the tasks that most need ending. This is
// the case the whole design turns on.
func TestAbandonWorksOnATaskThatCannotBeReplayed(t *testing.T) {
	h := newHarness(t)

	stale := []fsm.Stage{{ID: "gone", Requires: []fsm.Artifact{fsm.TaskID}, Produces: []fsm.Artifact{"x"}}}
	if err := h.env.Store.AppendAction("LUNA-1", fsm.TaskCreated{
		Kind: fsm.KindChore, Flow: fsm.Fingerprint(stale),
	}); err != nil {
		t.Fatalf("seeding: %v", err)
	}

	// Proof the task really is unreadable, so the test cannot pass by accident.
	if err := h.run(t, "task", "show", "LUNA-1"); err == nil {
		t.Fatal("setup: the task should not be replayable against this flow")
	}

	out := h.mustRun(t, "task", "abandon", "LUNA-1", "flow changed underneath")
	if !strings.Contains(out, "abandoned") {
		t.Errorf("want the ending confirmed, got %q", out)
	}
}

// TestAbandonNeedsAReasonOnTheCommandLine keeps the audit's point intact.
func TestAbandonNeedsAReasonOnTheCommandLine(t *testing.T) {
	h := newHarness(t)
	h.mustRun(t, "task", "new", "LUNA-1")

	if err := h.run(t, "task", "abandon", "LUNA-1"); err == nil {
		t.Error("abandoning without a reason must be refused")
	}
}

// TestAbandonRefusesAnUnknownTask keeps a typo from opening a log.
func TestAbandonRefusesAnUnknownTask(t *testing.T) {
	h := newHarness(t)

	if err := h.run(t, "task", "abandon", "LUNA-9", "never existed"); err == nil {
		t.Error("abandoning a task that does not exist must be refused")
	}
}

// TestAnAbandonedTaskShowsWhy covers the difference between abandoning and
// deleting: the log keeps everything, and the reason is the point.
func TestAnAbandonedTaskShowsWhy(t *testing.T) {
	h := newHarness(t)
	h.mustRun(t, "task", "new", "LUNA-1")
	h.mustRun(t, "task", "abandon", "LUNA-1", "superseded by LUNA-2")

	out := h.mustRun(t, "task", "show", "LUNA-1")

	if !strings.Contains(out, "abandoned") {
		t.Errorf("the state must say the task ended, got %q", out)
	}
	if !strings.Contains(out, "superseded by LUNA-2") {
		t.Errorf("the reason must survive in the log, got %q", out)
	}
}

// TestFlowRefusesAnUnknownSubcommand keeps a typo from looking like a no-op.
func TestFlowRefusesAnUnknownSubcommand(t *testing.T) {
	h := newHarness(t)

	if err := h.run(t, "flow"); err == nil {
		t.Error("flow with no subcommand must say what it takes")
	}
	if err := h.run(t, "flow", "frobnicate"); err == nil {
		t.Error("an unknown flow subcommand must be refused")
	}
	if err := h.run(t, "flow", "check", "extra"); err == nil {
		t.Error("flow check takes no arguments")
	}
}
