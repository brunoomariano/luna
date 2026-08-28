package cli

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/brunoomariano/luna/src/internal/fsm"
	"github.com/brunoomariano/luna/src/internal/lead"
	"github.com/brunoomariano/luna/src/internal/store"
)

// TestFlowCheckReportsAnOpenTask covers the primary defence of the flow fingerprint.
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
// Everything after this treats the id as safe: it becomes a directory name and a
// branch name, and neither checked. The path risk was named in the convention and
// the guard left for later; this is later.
//
// The length case is one character past MaxTaskIDLen rather than a round number,
// so the test fails if the limit moves and nobody revisits the boundary.
func TestTaskNewRefusesAnIdThatBreaksDownstream(t *testing.T) {
	tooLong := strings.Repeat("x", fsm.MaxTaskIDLen+1)
	for _, id := range []string{"../../etc", "a/b", "LUNA 1", tooLong} {
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

// TestABlockedTaskNotifies is the third ending INV-5 names.
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

	blocked := fsm.TaskState{ID: "LUNA-1", Status: fsm.StatusBlocked, Blocked: "the node broke"}
	reportEnding(h.env, fsm.Order{Kind: fsm.OrderBlocked}, blocked)

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

		state.ID = "LUNA-1"
		reportEnding(h.env, fsm.Order{Kind: fsm.OrderWait}, state)
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

	blocked := fsm.TaskState{ID: "LUNA-1", Status: fsm.StatusBlocked, Blocked: "the node broke"}
	reportEnding(h.env, fsm.Order{Kind: fsm.OrderBlocked, Reason: "the node broke"}, blocked)
	if !strings.Contains(h.out.String(), "blocked") {
		t.Error("the block is still reported to the terminal")
	}
}

// TestNoNotifierIsNotAnError covers the machine with nothing to notify through.
func TestNoNotifierIsNotAnError(t *testing.T) {
	h := newHarness(t)
	h.env.Notify = nil

	blocked := fsm.TaskState{ID: "LUNA-1", Status: fsm.StatusBlocked, Blocked: "the node broke"}
	reportEnding(h.env, fsm.Order{Kind: fsm.OrderBlocked, Reason: "the node broke"}, blocked)

	if !strings.Contains(h.out.String(), "the node broke") {
		t.Error("no notifier is a machine without one, not a reason to say nothing")
	}
}

// TestRetryExhaustionBlocksAndNotifies is INV-5's first acceptance
// criterion: the budget is spent, the task ends `blocked`, **and** somebody is
// told.
//
// It drives the real lead against a node that always fails, rather than handing
// the ending a TaskState with `Retry{Attempts: 3}` written into it. The
// fabricated version passed identically with `Attempts: 0` — the retry was
// decoration, and a refactor that broke exhaustion in the lead would have left
// this green. The criterion asks for both halves in one test because the seam
// between them is what has no other cover: `lead` exhausts, `cli` notifies, and
// nothing crossed from one to the other.
func TestRetryExhaustionBlocksAndNotifies(t *testing.T) {
	h := newHarness(t)

	notified := 0
	h.env.Notify = func(context.Context, string, string) error {
		notified++
		return nil
	}

	h.mustRun(t, "task", "new", "LUNA-1", "--kind", "chore")

	// A node that never succeeds, and a judge that keeps asking for another go:
	// the budget is the only thing that can end this, which is the point.
	node := &alwaysFailingNode{}
	conductor := &lead.Lead{
		Store: h.env.Store,
		Node:  node,
		Judge: retryingJudge{},
		Ask:   func(context.Context, string) (string, error) { return "APPROVE", nil },
	}

	state, err := conductor.Run(context.Background(), "LUNA-1")
	if err != nil {
		t.Fatalf("running: %v", err)
	}
	state.ID = "LUNA-1"
	reportEnding(h.env, fsm.Order{Kind: fsm.OrderBlocked, Reason: state.Blocked}, state)

	if state.Status != fsm.StatusBlocked {
		t.Fatalf("an exhausted budget must end blocked, got %q", state.Status)
	}
	// Reached rather than declared: the state got here by failing until the
	// reducer stopped allowing it.
	if state.Retry.Attempts == 0 {
		t.Error("the budget was never spent, so the exhaustion path was not exercised")
	}
	if notified != 1 {
		t.Errorf("an exhausted budget must notify exactly once, got %d", notified)
	}
	if !strings.Contains(h.out.String(), "blocked") {
		t.Errorf("and say so in the terminal too, got %q", h.out.String())
	}
}

// alwaysFailingNode is a stage that never delivers, so the retry budget is the
// only thing that can end the run.
type alwaysFailingNode struct{ calls int }

func (n *alwaysFailingNode) Run(context.Context, fsm.TaskState, fsm.Stage) (lead.Result, error) {
	n.calls++
	return lead.Result{}, errors.New("the node broke")
}

// retryingJudge always asks for another attempt, so nothing but the ceiling stops
// the loop — which is what makes this a test of the ceiling.
type retryingJudge struct{}

func (retryingJudge) OnFailure(context.Context, fsm.TaskState, string) lead.Decision {
	return lead.DecideRetry
}

// TestFlowCheckSaysWhichKnobReachesEachGate is what makes the knob choosable.
//
// The setting is a number compared against numbers written beside each gate, and
// picking one by reading every stage file is how it gets chosen by guess instead.
//
// It asserts on a flow this test controls rather than on the shipped stock: what
// the stock happens to declare is a product decision that will change, and a test
// that read it would fail every time somebody tuned an autonomy floor.
func TestFlowCheckSaysWhichKnobReachesEachGate(t *testing.T) {
	h := newHarness(t)

	out := h.mustRun(t, "flow", "check")

	if !strings.Contains(out, "gate(s), and the knob that reaches each") {
		t.Fatalf("the gate listing is missing:\n%s", out)
	}
	// Every gate the shipped flow opens declares criteria now — that is what makes
	// it wait at all — so each line has to say what reaches it.
	if !strings.Contains(out, "→ knob") {
		t.Errorf("the listing does not say which knob reaches a gate:\n%s", out)
	}
	if !strings.Contains(out, "criteri") {
		t.Errorf("the listing does not say what there is to judge:\n%s", out)
	}
	// The mechanical half is per task rather than per flow, and the listing has to
	// name the command that declares it — otherwise a person edits the stage file
	// looking for somewhere to put a check that does not belong there.
	if !strings.Contains(out, "luna gate checks") {
		t.Errorf("the listing does not say how checks are declared:\n%s", out)
	}
}

// TestAGateDeclaringNoAutonomyFloorReportsTheDefault covers the rendering of an
// undeclared value, which is the one that must not read as a zero.
func TestAGateDeclaringNoAutonomyFloorReportsTheDefault(t *testing.T) {
	gate := &fsm.GateSpec{Kind: fsm.GateConfirm, Reason: "approve it"}

	if got := gate.Resolved(); got != fsm.DefaultAutonomyFloor {
		t.Errorf("an undeclared autonomy floor resolved to %d, want %d",
			got, fsm.DefaultAutonomyFloor)
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
			{ID: "build", Requires: []fsm.Artifact{"a_spec_nobody_wrote"}, Produces: []fsm.Artifact{"code"}, Agent: "claude", Brief: "You build."},
		},
		"a stage with no agent": {
			{ID: "build", Requires: []fsm.Artifact{fsm.TaskID}, Produces: []fsm.Artifact{"code"}},
		},
		"a name too long for an agent": {
			{
				ID:       fsm.StageID(strings.Repeat("verylongstagename", 8)),
				Requires: []fsm.Artifact{fsm.TaskID},
				Produces: []fsm.Artifact{"code"},
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
// `luna autonomy 6` a decision rather than a guess.
func TestFlowGatesNamesTheKnobThatReachesEach(t *testing.T) {
	h := newHarness(t)

	reportGates(h.env, []fsm.Stage{
		{ID: "plan", Gate: &fsm.GateSpec{Kind: fsm.GateConfirm, AutonomyFloor: 3, Judge: []string{"is it in scope"}}},
		{ID: "spec", Gate: &fsm.GateSpec{Kind: fsm.GateReviewArtifact}},
	})

	out := h.out.String()
	for _, want := range []string{"plan", "confirm", "knob 3+", "1 criterion", "spec", "nothing to judge"} {
		if !strings.Contains(out, want) {
			t.Errorf("the gate listing does not carry %q:\n%s", want, out)
		}
	}
	// An undeclared autonomy floor is the highest, and saying so is what stops a
	// person reading 10 as a deliberate choice somebody made.
	if !strings.Contains(out, "undeclared") {
		t.Errorf("an undeclared autonomy floor was not flagged:\n%s", out)
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

// TestFlowCheckNamesAStageThatWouldInheritTheWrongSession covers the one rule
// that survived when fresh context stopped being one.
//
// A reviewer continuing the implementer's session reads its own reasoning
// instead of the delivery, and a review that confirms is not a review. The gap
// is static because the alternative is finding out from a code-review stage that
// agreed with everything — which reads exactly like a stage that went well, and
// so is never investigated.
//
// Two shapes, and they need different sentences. A stage in the middle names the
// stage whose session it would inherit and the role that ran it; the first stage
// has nothing before it at all, and pointing at an absent stage would print an
// empty name.
func TestFlowCheckNamesAStageThatWouldInheritTheWrongSession(t *testing.T) {
	for name, tc := range map[string]struct {
		flow []fsm.Stage
		says []string
	}{
		"a reviewer continuing the implementer": {
			flow: []fsm.Stage{
				{ID: "build", Requires: []fsm.Artifact{fsm.TaskID}, Produces: []fsm.Artifact{"code"}, Agent: "claude", Brief: "You build."},
				{
					ID: "code-review", Requires: []fsm.Artifact{"code"}, Produces: []fsm.Artifact{"review"},
					Agent: "claude", Brief: "You judge.", Context: fsm.ContextLive,
				},
			},
			says: []string{"code-review", "build", "briefed differently"},
		},
		"the first stage having nothing to continue": {
			flow: []fsm.Stage{{
				ID: "build", Requires: []fsm.Artifact{fsm.TaskID}, Produces: []fsm.Artifact{"code"},
				Agent: "claude", Brief: "You build.", Context: fsm.ContextLive,
			}},
			says: []string{"build", "there is none to continue"},
		},
	} {
		t.Run(name, func(t *testing.T) {
			h := newHarness(t)

			reportFlowGaps(h.env, tc.flow)

			out := h.out.String()
			if strings.Contains(out, "the contract holds") {
				t.Fatalf("a stage inheriting the wrong session was reported as sound:\n%s", out)
			}
			for _, want := range tc.says {
				if !strings.Contains(out, want) {
					t.Errorf("the gap does not name %q:\n%s", want, out)
				}
			}
		})
	}
}

// TestFlowCheckStopsOnALogItCannotDecode is the other side of the line this
// command already draws.
//
// A task born under a retired flow is listed by name — that is the whole reason
// the command exists, and skipping it in the loop is right because it is
// reported afterwards. A log holding an action this build cannot decode is a
// different thing: it is not a flow that moved on, it is a log that is damaged.
// Folding it into the "unreadable" list would tell the person their flow changed
// when it did not, and send them re-recording a fingerprint that was never the
// problem.
func TestFlowCheckStopsOnALogItCannotDecode(t *testing.T) {
	h := newHarness(t)

	if err := h.env.Store.Append("LUNA-1", store.Event{Action: "TimeTravel"}); err != nil {
		t.Fatalf("seeding: %v", err)
	}

	err := h.run(t, "flow", "check")

	if err == nil {
		t.Fatal("a log that cannot be decoded must stop the check rather than be listed as a flow change")
	}
	if strings.Contains(h.out.String(), "no longer replay") {
		t.Errorf("a damaged log was reported as a flow change:\n%s", h.out.String())
	}
}

// TestFlowCheckSaysWhatThisFlowHasCostHere is the number that lived in prose.
//
// `--flow` cannot change after a task opens, and the three flows differ by a
// factor nobody can guess — so it is the most expensive decision available and
// the one made with the least information. The skill carried figures; the tool
// carried none, and the decision is made in the terminal.
func TestFlowCheckSaysWhatThisFlowHasCostHere(t *testing.T) {
	h := newHarness(t)
	h.mustRun(t, "task", "new", "C-1", "--kind", "chore", "--flow", "chore")

	openStage(t, h, "C-1")
	if err := h.env.Store.AppendAction("C-1", fsm.Complete{
		Delivered: []fsm.Artifact{"worktree"},
		Evidence: map[fsm.Artifact]fsm.Evidence{
			"worktree": {Scope: fsm.ScopeExistence, Verdict: fsm.VerdictPassed},
		},
		Spent: fsm.Spend{CostUSD: 0.6681, Turns: 8},
		Flow:  fsm.DefaultFlow(),
	}); err != nil {
		t.Fatalf("closing the stage: %v", err)
	}

	out := h.mustRun(t, "flow", "check", "--flow", "chore")

	if !strings.Contains(out, "$0.6681") {
		t.Errorf("flow check does not say what this flow has cost here:\n%s", out)
	}
	if !strings.Contains(out, "setup") {
		t.Errorf("the cost is not attributed to a stage:\n%s", out)
	}
	// How many observations it rests on, because one is not an estimate.
	if !strings.Contains(out, "1 run") {
		t.Errorf("the report does not say how much it is standing on:\n%s", out)
	}

	// A second task on the same flow: the count moves, and the estimate is now
	// standing on two observations rather than one.
	h.mustRun(t, "task", "new", "C-2", "--kind", "chore", "--flow", "chore")
	openStage(t, h, "C-2")
	if err := h.env.Store.AppendAction("C-2", fsm.Complete{
		Delivered: []fsm.Artifact{"worktree"},
		Evidence: map[fsm.Artifact]fsm.Evidence{
			"worktree": {Scope: fsm.ScopeExistence, Verdict: fsm.VerdictPassed},
		},
		Spent: fsm.Spend{CostUSD: 0.9, Turns: 11},
		Flow:  fsm.DefaultFlow(),
	}); err != nil {
		t.Fatalf("closing the second stage: %v", err)
	}

	if out := h.mustRun(t, "flow", "check", "--flow", "chore"); !strings.Contains(out, "2 runs") {
		t.Errorf("the second observation did not move the count:\n%s", out)
	}
}

// TestAFlowNobodyHasRunReportsNoCost. An estimate from no observations is a
// number somebody would plan against.
func TestAFlowNobodyHasRunReportsNoCost(t *testing.T) {
	h := newHarness(t)

	out := h.mustRun(t, "flow", "check", "--flow", "full")

	if strings.Contains(out, "what it has cost") {
		t.Errorf("a flow nobody has run was given a price:\n%s", out)
	}
}

// TestTheMedianSurvivesOneRunawayStage. A mean would follow the outlier and say
// nothing about the next run — which is the run somebody is deciding about.
func TestTheMedianSurvivesOneRunawayStage(t *testing.T) {
	ordinary := []float64{1, 1, 1, 1, 40}

	if got := medianOf(ordinary); got != 1 {
		t.Errorf("one runaway moved the estimate to %v", got)
	}
	// An even count takes the middle pair, so two observations do not silently
	// report the cheaper one.
	if got := medianOf([]float64{2, 4}); got != 3 {
		t.Errorf("an even count reported %v", got)
	}
	// And the source is not reordered: the caller may still be using it.
	if ordinary[4] != 40 {
		t.Error("taking the median sorted the caller's slice")
	}
}

// TestFlowCheckNamesAStageThatEscapesTheSandbox. The static check exists so an
// exemption cannot spread quietly, and `luna flow check` is where a person meets
// it — a check nothing reports is one nobody acts on.
func TestFlowCheckNamesAStageThatEscapesTheSandbox(t *testing.T) {
	h := newHarness(t)

	reportFlowGaps(h.env, []fsm.Stage{
		{
			ID: "build", Agent: "claude", Brief: "You build.", Uncontained: true,
			Requires: []fsm.Artifact{fsm.TaskID}, Produces: []fsm.Artifact{"code"},
			Verifiers: map[fsm.Artifact]fsm.Verifier{"code": fsm.Existence{}},
		},
	})

	out := h.out.String()
	for _, want := range []string{"build", "outside the sandbox", "INV-4"} {
		if !strings.Contains(out, want) {
			t.Errorf("the report does not carry %q:\n%s", want, out)
		}
	}
}
