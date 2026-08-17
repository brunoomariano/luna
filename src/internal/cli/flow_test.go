package cli

import (
	"context"
	"errors"
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

// TestTaskNewRefusesAnIdThatBreaksDownstream guards the only door an id comes
// through.
//
// Everything after this treats the id as safe: it becomes `wt-<repo>-<id>` on
// disk and part of an agent name in herdr, and neither checked. ADR-0037 named
// the path risk and left the guard for later; this is later.
func TestTaskNewRefusesAnIdThatBreaksDownstream(t *testing.T) {
	for _, id := range []string{"../../etc", "a/b", "LUNA 1", strings.Repeat("x", 40)} {
		h := newHarness(t)
		if err := h.run(t, "task", "new", id); err == nil {
			t.Errorf("%q should not open a log", id)
		}
	}
}

// TestTaskNewStillTakesOrdinaryIds keeps the rule from breaking the normal case.
func TestTaskNewStillTakesOrdinaryIds(t *testing.T) {
	for _, id := range []string{"LUNA-1", "PROJ-4823", "fix_login"} {
		h := newHarness(t)
		if err := h.run(t, "task", "new", id); err != nil {
			t.Errorf("%q is an ordinary id: %v", id, err)
		}
	}
}

// TestABlockedTaskNotifies is the third ending INV-core-8 names.
//
// "Every task ends in a commit, a gate or a notified block" — and the third had
// nothing behind it. Printing to stdout is not notifying: the run that most needs
// it is the unattended one, where nobody is reading the terminal.
func TestABlockedTaskNotifies(t *testing.T) {
	h := newHarness(t)

	var gotTask, gotReason string
	h.env.Notify = func(_ context.Context, taskID, reason string) error {
		gotTask, gotReason = taskID, reason
		return nil
	}

	blocked := fsm.TaskState{Status: fsm.StatusBlocked, Blocked: "the node broke"}
	if err := reportRun(h.env, "LUNA-1", blocked); err != nil {
		t.Fatalf("reporting: %v", err)
	}

	if gotTask != "LUNA-1" {
		t.Errorf("the notification must name the task, got %q", gotTask)
	}
	if gotReason != "the node broke" {
		t.Errorf("the notification must carry the reason, got %q", gotReason)
	}
}

// TestOnlyABlockNotifies keeps the banner for the ending that needs a person now.
//
// A gate is a planned pause and is discoverable with `luna gates`; a finished task
// needs nobody. Notifying on either would train people to ignore the notification
// that matters.
func TestOnlyABlockNotifies(t *testing.T) {
	for _, state := range []fsm.TaskState{
		{Status: fsm.StatusDone},
		{Status: fsm.StatusAwaitingGate, Gate: &fsm.PendingGate{Reason: "confirm"}},
	} {
		h := newHarness(t)
		notified := false
		h.env.Notify = func(context.Context, string, string) error {
			notified = true
			return nil
		}

		if err := reportRun(h.env, "LUNA-1", state); err != nil {
			t.Fatalf("reporting: %v", err)
		}
		if notified {
			t.Errorf("%q needs no banner", state.Status)
		}
	}
}

// TestAFailedNotificationDoesNotFailTheRun covers the direction that matters.
//
// The block is the fact worth keeping; the banner is only how it was announced.
// Losing the announcement must not lose the task — and the person still has the
// printed line and the log.
func TestAFailedNotificationDoesNotFailTheRun(t *testing.T) {
	h := newHarness(t)
	h.env.Notify = func(context.Context, string, string) error {
		return errors.New("no herdr running")
	}

	blocked := fsm.TaskState{Status: fsm.StatusBlocked, Blocked: "the node broke"}
	if err := reportRun(h.env, "LUNA-1", blocked); err != nil {
		t.Errorf("a notifier that failed must not fail the run: %v", err)
	}
	if !strings.Contains(h.out.String(), "blocked") {
		t.Error("the block is still reported to the terminal")
	}
}

// TestNoNotifierIsNotAnError covers the machine with nothing to notify through.
func TestNoNotifierIsNotAnError(t *testing.T) {
	h := newHarness(t)
	h.env.Notify = nil

	blocked := fsm.TaskState{Status: fsm.StatusBlocked, Blocked: "the node broke"}
	if err := reportRun(h.env, "LUNA-1", blocked); err != nil {
		t.Errorf("no notifier is a machine without one, not a failure: %v", err)
	}
}

// TestRetryExhaustionBlocksAndNotifies is INV-core-8's first acceptance criterion,
// and it was unreachable until this round.
//
// Both halves were missing: with no Judge the lead blocked on the first failure,
// so the budget was never exhausted, and with no notifier there was nothing to
// emit. The criterion asked for a test of behaviour that had no code path.
func TestRetryExhaustionBlocksAndNotifies(t *testing.T) {
	h := newHarness(t)

	notified := 0
	h.env.Notify = func(context.Context, string, string) error {
		notified++
		return nil
	}

	// A task whose budget is spent: the next failure is the one that escalates.
	if err := h.env.Store.AppendAction("LUNA-1", fsm.TaskCreated{Kind: fsm.KindChore}); err != nil {
		t.Fatalf("creating: %v", err)
	}

	blocked := fsm.TaskState{
		Status:  fsm.StatusBlocked,
		Blocked: `stage "build" failed 3 times: the node broke`,
		Retry:   fsm.Retry{Attempts: 3, Max: 2},
	}
	if err := reportRun(h.env, "LUNA-1", blocked); err != nil {
		t.Fatalf("reporting: %v", err)
	}

	if notified != 1 {
		t.Errorf("an exhausted budget must notify exactly once, got %d", notified)
	}
	if !strings.Contains(h.out.String(), "blocked") {
		t.Errorf("and say so in the terminal too, got %q", h.out.String())
	}
}

// TestFlowCheckSaysWhichKnobReachesEachGate is what makes the knob choosable.
//
// The setting is a number compared against numbers written beside each gate, and
// picking one by reading every stage file is how it gets chosen by guess instead.
//
// It asserts on a flow this test controls rather than on the shipped stock: what
// the stock happens to declare is a product decision that will change, and a test
// that read it would fail every time somebody tuned a criticality.
func TestFlowCheckSaysWhichKnobReachesEachGate(t *testing.T) {
	h := newHarness(t)

	out := h.mustRun(t, "flow", "check")

	if !strings.Contains(out, "gate(s), and the knob that reaches each") {
		t.Fatalf("the gate listing is missing:\n%s", out)
	}
	// Every gate the shipped flow opens declares criteria now — that is what makes
	// it wait at all (ADR-0063) — so each line has to say what reaches it.
	if !strings.Contains(out, "→ knob") {
		t.Errorf("the listing does not say which knob reaches a gate:\n%s", out)
	}
	if !strings.Contains(out, "criteri") {
		t.Errorf("the listing does not say what there is to judge:\n%s", out)
	}
	// The mechanical half is per task rather than per flow, and the listing has to
	// name the command that declares it — otherwise a person edits the stage file
	// looking for somewhere to put a check that does not belong there (ADR-0067).
	if !strings.Contains(out, "luna gate checks") {
		t.Errorf("the listing does not say how checks are declared:\n%s", out)
	}
}

// TestAGateDeclaringNoCriticalityReportsTheDefault covers the rendering of an
// undeclared value, which is the one that must not read as a zero.
func TestAGateDeclaringNoCriticalityReportsTheDefault(t *testing.T) {
	gate := &fsm.GateSpec{Kind: fsm.GateConfirm, Reason: "approve it"}

	if got := gate.Resolved(); got != fsm.DefaultCriticality {
		t.Errorf("an undeclared criticality resolved to %d, want %d",
			got, fsm.DefaultCriticality)
	}
}

// TestFlowCheckReportsEachKindOfGap covers what `luna flow check` is for: a stock
// somebody edited into something that cannot run.
//
// Each gap is a different way an edit goes wrong, and each has to be named — a
// check that reports "there are problems" sends a person reading TOML by hand.
func TestFlowCheckReportsEachKindOfGap(t *testing.T) {
	for name, flow := range map[string][]fsm.Stage{
		"an input nothing produces": {
			{ID: "build", Requires: []fsm.Artifact{"a_spec_nobody_wrote"}, Produces: []fsm.Artifact{"code"}, Role: "implementer"},
		},
		"a stage with no role": {
			{ID: "build", Requires: []fsm.Artifact{fsm.TaskID}, Produces: []fsm.Artifact{"code"}},
		},
		"a name too long for an agent": {
			{
				ID:       fsm.StageID(strings.Repeat("verylongstagename", 8)),
				Requires: []fsm.Artifact{fsm.TaskID},
				Produces: []fsm.Artifact{"code"},
				Role:     "implementer",
			},
		},
	} {
		t.Run(name, func(t *testing.T) {
			h := newHarness(t)

			reportFlowGaps(h.env, flow)

			out := h.out.String()
			if strings.Contains(out, "the contract holds") {
				t.Fatalf("a broken flow was reported as sound:\n%s", out)
			}
			if !strings.Contains(out, "build") && !strings.Contains(out, "verylongstagename") {
				t.Errorf("the gap does not name the stage it is in:\n%s", out)
			}
		})
	}
}

// TestFlowCheckSaysSoWhenTheContractHolds. The shipped flow is sound, and a check
// that only ever spoke up about problems would leave a person unsure whether it
// ran at all.
func TestFlowCheckSaysSoWhenTheContractHolds(t *testing.T) {
	h := newHarness(t)

	reportFlowGaps(h.env, fsm.DefaultFlow())

	if !strings.Contains(h.out.String(), "the contract holds") {
		t.Errorf("a sound flow said nothing:\n%s", h.out.String())
	}
}

// TestFlowGatesNamesTheKnobThatReachesEach. The knob is a number, and a number is
// meaningless without the list of what it reaches — this listing is what makes
// `luna autonomy 6` a decision rather than a guess (RFC-0006).
func TestFlowGatesNamesTheKnobThatReachesEach(t *testing.T) {
	h := newHarness(t)

	reportGates(h.env, []fsm.Stage{
		{ID: "plan", Gate: &fsm.GateSpec{Kind: fsm.GateConfirm, Criticality: 3, Judge: []string{"is it in scope"}}},
		{ID: "spec", Gate: &fsm.GateSpec{Kind: fsm.GateReviewArtifact}},
	})

	out := h.out.String()
	for _, want := range []string{"plan", "confirm", "knob 3+", "1 criterion", "spec", "nothing to judge"} {
		if !strings.Contains(out, want) {
			t.Errorf("the gate listing does not carry %q:\n%s", want, out)
		}
	}
	// An undeclared criticality is the highest, and saying so is what stops a
	// person reading 10 as a deliberate choice somebody made.
	if !strings.Contains(out, "undeclared") {
		t.Errorf("an undeclared criticality was not flagged:\n%s", out)
	}
}

// TestFlowGatesSaysSoWhenNothingStops. A flow with no gate runs start to finish
// unattended, which is worth stating rather than showing an empty list.
func TestFlowGatesSaysSoWhenNothingStops(t *testing.T) {
	h := newHarness(t)

	reportGates(h.env, []fsm.Stage{{ID: "build"}})

	if !strings.Contains(h.out.String(), "nothing stops for a person") {
		t.Errorf("a gateless flow said nothing:\n%s", h.out.String())
	}
}
