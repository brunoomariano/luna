package lead

import (
	"context"
	"errors"
	"fmt"
	"path/filepath"
	"strings"
	"testing"

	"github.com/brunoomariano/luna/src/internal/fsm"
	"github.com/brunoomariano/luna/src/internal/store"
)

// deliveringNode reports that every stage delivered exactly what it declared. It
// stands in for a world where the agent always succeeds, which is the case the
// happy path is about.
type deliveringNode struct{ calls int }

func (n *deliveringNode) Run(_ context.Context, _ fsm.TaskState, stage fsm.Stage) (Result, error) {
	n.calls++

	delivered := make([]fsm.Artifact, 0, len(stage.Produces)+len(stage.ProducesForHuman))
	delivered = append(delivered, stage.Produces...)
	delivered = append(delivered, stage.ProducesForHuman...)

	// Passing evidence for everything it owes: a stage only closes when every owed
	// artifact has a verdict that passed, so a node that always succeeds has to say
	// so artifact by artifact.
	evidence := map[fsm.Artifact]fsm.Evidence{}
	for _, a := range delivered {
		evidence[a] = fsm.Evidence{
			Scope:   fsm.ScopeFull,
			Verdict: fsm.VerdictPassed,
			Command: "check " + string(a),
			Detail:  "the tool confirmed " + string(a),
		}
	}
	return Result{Delivered: delivered, Evidence: evidence}, nil
}

// failingNode always breaks, which is what exercises the judgement layer.
type failingNode struct {
	calls  int
	reason string
}

func (n *failingNode) Run(context.Context, fsm.TaskState, fsm.Stage) (Result, error) {
	n.calls++
	return Result{}, errors.New(n.reason)
}

// partialNode delivers less than the stage promised — the case the contract's
// exit check exists for.
type partialNode struct{}

func (partialNode) Run(_ context.Context, _ fsm.TaskState, stage fsm.Stage) (Result, error) {
	if len(stage.Produces) == 0 {
		return Result{Delivered: stage.ProducesForHuman}, nil
	}
	// Everything but the last thing it owed.
	return Result{Delivered: stage.Produces[:len(stage.Produces)-1]}, nil
}

// alwaysRetries and alwaysBlocks stand in for the model's judgement.
type alwaysRetries struct{ asked int }

func (j *alwaysRetries) OnFailure(context.Context, fsm.TaskState, string) Decision {
	j.asked++
	return DecideRetry
}

type alwaysBlocks struct{ asked int }

func (j *alwaysBlocks) OnFailure(context.Context, fsm.TaskState, string) Decision {
	j.asked++
	return DecideBlock
}

// stallingNode reports the stall the node layer observes — an agent that is alive
// and producing nothing. Named rather than inline because it stands in for a
// real external condition.
//
// This is the only way a stall reaches the lead. There is no watchdog interface
// beside it, because nothing in a replayed TaskState says "stuck" — the state
// carries no clock, so only the node, which was there, can tell.
type stallingNode struct{}

func (stallingNode) Run(context.Context, fsm.TaskState, fsm.Stage) (Result, error) {
	return Result{}, fmt.Errorf("%w: the agent did not react", ErrStalled)
}

func newStore(t *testing.T) *store.Store {
	t.Helper()

	s, err := store.OpenAs(filepath.Join(t.TempDir(), "luna.db"), store.LunaOwnsTheLog)
	if err != nil {
		t.Fatalf("opening the store: %v", err)
	}
	t.Cleanup(func() { _ = s.Close() })
	return s
}

// nightly opens a task that stops at no gate, so a test can watch the lead drive
// a whole flow without a person in the loop.
func nightly(t *testing.T, s *store.Store, id string, kind fsm.TaskKind) {
	t.Helper()

	nightlyUnder(t, s, id, kind, fsm.DefaultFlow())
}

// nightlyUnder is the same, for a test that drives a flow of its own.
//
// A task records the flow it was born under, and a replay refuses a log written
// against a different one — so a test that hands the lead a custom flow has to
// open its task under that flow rather than the shipped one. The stamp is not
// incidental to what those tests check: it is the mechanism that makes a flow
// swapped underneath an open task an error instead of a silent re-run.
func nightlyUnder(t *testing.T, s *store.Store, id string, kind fsm.TaskKind, flow []fsm.Stage) {
	t.Helper()

	created := fsm.TaskCreated{Kind: kind, Profile: fsm.ProfileNightly, Flow: fsm.Fingerprint(flow)}
	if err := s.AppendAction(id, created); err != nil {
		t.Fatalf("creating %s: %v", id, err)
	}
}

// ── the happy path ───────────────────────────────────────────────────────────

// TestTheLeadDrivesATaskToTheEnd is the scenario the wave exists for.
//
// Given a node that always delivers and a profile that stops at nothing, the lead
// walks the whole flow and the task finishes — with no model consulted anywhere,
// because nothing went wrong.
// approvingLead answers every gate the knob reaches with an approval.
//
// It exists because a gate waits when the stage declared criteria, so a test that
// wants a task to run to the end has to say who answers them. Naming it here keeps
// that intent visible instead of repeating a closure whose meaning is easy to miss.
func approvingLead() func(context.Context, string) (string, error) {
	return func(context.Context, string) (string, error) {
		return "APPROVE\n\nevery criterion is met", nil
	}
}

func TestTheLeadDrivesATaskToTheEnd(t *testing.T) {
	s := newStore(t)
	nightly(t, s, "LUNA-1", fsm.KindChore)
	// Unattended is the knob's job now rather than a profile's, and it is task
	// state, so it is set the way every decision is: through the log.
	if err := s.AppendAction("LUNA-1", fsm.SetKnob{Knob: fsm.KnobAll}); err != nil {
		t.Fatalf("setting the knob: %v", err)
	}

	node := &deliveringNode{}
	judge := &alwaysRetries{}
	// Knob 10 with a lead that approves: unattended is the knob's job now
	// rather than a profile's.
	l := &Lead{Store: s, Node: node, Judge: judge, Ask: approvingLead()}

	state, err := l.Run(context.Background(), "LUNA-1")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if state.Status != fsm.StatusDone {
		t.Errorf("want the task finished, got %q (blocked: %s)", state.Status, state.Blocked)
	}
	if node.calls == 0 {
		t.Error("the lead must have called the node")
	}
	// The judgement layer costs tokens. On a run where nothing failed it should
	// never have been asked.
	if judge.asked != 0 {
		t.Errorf("the judge was consulted %d times on a clean run", judge.asked)
	}
}

// TestTheLeadStopsAtAGate covers the ending that is not an ending.
//
// A gate is a person's decision, and the lead has no business answering it. It
// returns the suspended state and leaves.
func TestTheLeadStopsAtAGate(t *testing.T) {
	s := newStore(t)
	// Interactive: every gate waits.
	if err := s.AppendAction("LUNA-1", fsm.TaskCreated{Kind: fsm.KindFeature, Flow: fsm.Fingerprint(fsm.DefaultFlow())}); err != nil {
		t.Fatalf("creating: %v", err)
	}

	l := &Lead{Store: s, Node: &deliveringNode{}, Judge: &alwaysBlocks{}}

	state, err := l.Run(context.Background(), "LUNA-1")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if state.Status != fsm.StatusAwaitingGate {
		t.Fatalf("want the task waiting, got %q", state.Status)
	}
	if state.Stage != "plan" {
		t.Errorf("want it stopped at the first gate, got %q", state.Stage)
	}
}

// TestTheLeadResumesFromWhereItStopped covers what the store buys.
//
// A second lead, with no memory of the first, picks the task up from the log and
// carries on. This is the property that makes killing the process survivable
// (INV-2).
func TestTheLeadResumesFromWhereItStopped(t *testing.T) {
	s := newStore(t)
	if err := s.AppendAction("LUNA-1", fsm.TaskCreated{Kind: fsm.KindChore, Flow: fsm.Fingerprint(fsm.DefaultFlow())}); err != nil {
		t.Fatalf("creating: %v", err)
	}

	first := &Lead{Store: s, Node: &deliveringNode{}, Judge: &alwaysBlocks{}}
	state, err := first.Run(context.Background(), "LUNA-1")
	if err != nil {
		t.Fatalf("first run: %v", err)
	}
	if state.Status != fsm.StatusAwaitingGate {
		t.Fatalf("expected the run to stop at a gate, got %q", state.Status)
	}

	// A person answers, and a different lead takes over.
	if err := s.AppendAction("LUNA-1", fsm.GateApprove{}); err != nil {
		t.Fatalf("approving: %v", err)
	}

	second := &Lead{Store: s, Node: &deliveringNode{}, Judge: &alwaysBlocks{}}
	state, err = second.Run(context.Background(), "LUNA-1")
	if err != nil {
		t.Fatalf("second run: %v", err)
	}

	if state.Stage == "plan" && state.Status == fsm.StatusAwaitingGate {
		t.Error("the second lead should have moved past the answered gate")
	}
}

// ── failure and judgement ────────────────────────────────────────────────────

// TestAFailingNodeConsultsTheJudge covers the hybrid boundary.
//
// The model is asked only once something has gone wrong. That is the whole shape
// of the hybrid lead: deterministic on the happy path, judgement where the machine
// has nothing to go on.
func TestAFailingNodeConsultsTheJudge(t *testing.T) {
	s := newStore(t)
	nightly(t, s, "LUNA-1", fsm.KindChore)

	judge := &alwaysBlocks{}
	l := &Lead{Store: s, Node: &failingNode{reason: "the compiler disagreed"}, Judge: judge}

	state, err := l.Run(context.Background(), "LUNA-1")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if judge.asked == 0 {
		t.Error("a failure must reach the judgement layer")
	}
	if state.Status != fsm.StatusBlocked {
		t.Errorf("want the task blocked, got %q", state.Status)
	}
	// A blocked task that does not say why is the silent failure INV-5 forbids.
	if state.Blocked == "" {
		t.Error("a blocked task must carry its reason")
	}
}

// TestRetryIsBoundedEvenWhenTheJudgeKeepsSayingRetry covers the budget.
//
// The judgement layer chooses, but it does not get to choose forever: the retry
// ceiling belongs to the reducer, and a model that always says "try again" still
// ends at a block. Without this, a confident judge is an infinite loop.
func TestRetryIsBoundedEvenWhenTheJudgeKeepsSayingRetry(t *testing.T) {
	s := newStore(t)
	nightly(t, s, "LUNA-1", fsm.KindChore)

	node := &failingNode{reason: "still broken"}
	judge := &alwaysRetries{}
	// Knob 10 with a lead that approves: unattended is the knob's job now
	// rather than a profile's.
	l := &Lead{Store: s, Node: node, Judge: judge, Ask: approvingLead()}

	state, err := l.Run(context.Background(), "LUNA-1")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if state.Status != fsm.StatusBlocked {
		t.Fatalf("an unbounded retry loop is the failure this prevents; got %q", state.Status)
	}
	// The budget is two attempts plus the one that spends it.
	if node.calls > 4 {
		t.Errorf("the node was called %d times; the budget should have stopped it sooner", node.calls)
	}
}

// TestWithoutAJudgeAFailureBlocks covers the default.
//
// A lead with no judgement layer is not a lead that guesses — it escalates. The
// cautious default matters because the alternative is retrying blind.
func TestWithoutAJudgeAFailureBlocks(t *testing.T) {
	s := newStore(t)
	nightly(t, s, "LUNA-1", fsm.KindChore)

	l := &Lead{Store: s, Node: &failingNode{reason: "no judge here"}}

	state, err := l.Run(context.Background(), "LUNA-1")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if state.Status != fsm.StatusBlocked {
		t.Errorf("want a block when there is nobody to judge, got %q", state.Status)
	}
}

// TestAnUnknownDecisionIsAnError covers the judge contract.
//
// A judgement the lead cannot act on is a programming error, not a transition to
// guess at.
func TestAnUnknownDecisionIsAnError(t *testing.T) {
	s := newStore(t)
	nightly(t, s, "LUNA-1", fsm.KindChore)

	l := &Lead{
		Store: s,
		Node:  &failingNode{reason: "broken"},
		Judge: judgeFunc(func(context.Context, fsm.TaskState, string) Decision {
			return Decision("ponder")
		}),
	}

	if _, err := l.Run(context.Background(), "LUNA-1"); err == nil {
		t.Error("a decision the lead cannot act on must be reported")
	}
}

// judgeFunc adapts a function to the Judge interface, for the cases where a named
// fake would say less than the function itself.
type judgeFunc func(context.Context, fsm.TaskState, string) Decision

func (f judgeFunc) OnFailure(ctx context.Context, s fsm.TaskState, reason string) Decision {
	return f(ctx, s, reason)
}

// ── the contract, from the lead's side ───────────────────────────────────────

// TestAPartialDeliveryBlocksTheTask covers the exit check end to end.
//
// The lead records what the node reported and does not argue with the verdict. A
// stage that promised two artifacts and delivered one does not close — and the
// lead finds a blocked task on its next pass rather than pushing through.
func TestAPartialDeliveryBlocksTheTask(t *testing.T) {
	s := newStore(t)
	nightly(t, s, "LUNA-1", fsm.KindChore)

	l := &Lead{Store: s, Node: partialNode{}, Judge: &alwaysBlocks{}}

	state, err := l.Run(context.Background(), "LUNA-1")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if state.Status != fsm.StatusBlocked {
		t.Fatalf("want the task blocked on a partial delivery, got %q", state.Status)
	}
	if state.Blocked == "" {
		t.Error("the block must name what was not delivered")
	}
}

// TestEvidenceReachesTheLog covers the verification verdict from the lead's side.
//
// What the tool reported has to survive into the history, or the audit says a
// stage closed without saying on what grounds.
func TestEvidenceReachesTheLog(t *testing.T) {
	s := newStore(t)
	nightly(t, s, "LUNA-1", fsm.KindChore)

	l := &Lead{Store: s, Node: &deliveringNode{}, Judge: &alwaysBlocks{}}
	if _, err := l.Run(context.Background(), "LUNA-1"); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	state, err := s.Replay("LUNA-1", fsm.DefaultFlow())
	if err != nil {
		t.Fatalf("replaying: %v", err)
	}

	if len(state.Evidence) == 0 {
		t.Fatal("the evidence must reach the log")
	}
	if !state.Evidence["worktree"].Delivered() {
		t.Errorf("want evidence for what the flow produced, got %v", state.Evidence)
	}
}

// ── the watchdog ─────────────────────────────────────────────────────────────

// TestAStalledTaskBlocksWithoutConsultingTheJudge covers the stall path.
//
// A node that returns nothing is not a node that failed — only something outside
// the call can tell those apart. When the watchdog says the task stopped moving,
// that is a decision, not a judgement call: there is nothing for a model to weigh
// about an agent that is alive and doing nothing, and asking one would spend
// tokens to restate what the log already says.
func TestAStalledTaskBlocksWithoutConsultingTheJudge(t *testing.T) {
	s := newStore(t)
	nightly(t, s, "LUNA-1", fsm.KindChore)

	judge := &alwaysBlocks{}
	l := &Lead{Store: s, Node: stallingNode{}, Judge: judge}

	state, err := l.Run(context.Background(), "LUNA-1")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if judge.asked != 0 {
		t.Errorf("a stall is decided in code, not by the model (got %d calls)", judge.asked)
	}
	if state.Status != fsm.StatusBlocked {
		t.Errorf("want the stalled task blocked, got %q", state.Status)
	}
	if !strings.Contains(state.Blocked, "progress") {
		t.Errorf("the reason must say the task stopped progressing, got %q", state.Blocked)
	}
}

// TestAStallDoesNotSpendTheRetryBudget covers the other half of the stall path.
//
// The retry budget is for a stage that failed. A stall says nothing about the
// stage — burning attempts on an agent that is not going to react would reach a
// block through three recorded failures that never happened (INV-2).
func TestAStallDoesNotSpendTheRetryBudget(t *testing.T) {
	s := newStore(t)
	nightly(t, s, "LUNA-1", fsm.KindChore)

	l := &Lead{Store: s, Node: stallingNode{}}

	state, err := l.Run(context.Background(), "LUNA-1")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if state.Retry.Attempts != 0 {
		t.Errorf("a stall costs no retries, got %d spent", state.Retry.Attempts)
	}

	// One decision, one event: the log must not show failures nobody observed.
	events, err := s.Events("LUNA-1")
	if err != nil {
		t.Fatalf("reading the log: %v", err)
	}
	for _, e := range events {
		if e.Action == "Fail" {
			t.Error("a stall is recorded as a block, never as a failure")
		}
	}
}

// TestAStalledNodeBlocksToo covers the stall reported by the node rather than by
// the watchdog — the only path a stall reaches the lead by.
func TestAStalledNodeBlocksToo(t *testing.T) {
	s := newStore(t)
	nightly(t, s, "LUNA-1", fsm.KindChore)

	judge := &alwaysRetries{}
	l := &Lead{Store: s, Node: &stallingNode{}, Judge: judge}

	state, err := l.Run(context.Background(), "LUNA-1")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if judge.asked != 0 {
		t.Errorf("a stall from the node is not a failure either (got %d calls)", judge.asked)
	}
	if state.Status != fsm.StatusBlocked {
		t.Errorf("want the task blocked, got %q", state.Status)
	}
}

// TestANodeThatAnswersIsNotStalled is the other side: a stall comes from the node
// reporting one, so a node that answers normally is never treated as stuck.
func TestANodeThatAnswersIsNotStalled(t *testing.T) {
	s := newStore(t)
	nightly(t, s, "LUNA-1", fsm.KindChore)

	// The knob is what carries it past the gates; this test is about the node
	// answering, not about who answers a gate.
	if err := s.AppendAction("LUNA-1", fsm.SetKnob{Knob: fsm.KnobAll}); err != nil {
		t.Fatalf("setting the knob: %v", err)
	}

	l := &Lead{Store: s, Node: &deliveringNode{}, Judge: &alwaysBlocks{}, Ask: approvingLead()}

	state, err := l.Run(context.Background(), "LUNA-1")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if state.Status != fsm.StatusDone {
		t.Errorf("want the task finished, got %q", state.Status)
	}
}

// ── cancellation and failure of the lead itself ──────────────────────────────

// TestACancelledContextStopsTheLead covers the stop signal.
//
// Cancellation is checked before starting the next thing rather than after: the
// point of stopping is not to begin more work.
func TestACancelledContextStopsTheLead(t *testing.T) {
	s := newStore(t)
	nightly(t, s, "LUNA-1", fsm.KindChore)

	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	l := &Lead{Store: s, Node: &deliveringNode{}, Judge: &alwaysBlocks{}}

	if _, err := l.Run(ctx, "LUNA-1"); !errors.Is(err, context.Canceled) {
		t.Errorf("want the cancellation reported, got %v", err)
	}
}

// TestABrokenStoreStopsTheLead covers the error path that is not a task outcome.
//
// A store that will not answer is the lead's failure, not the task's — so it
// comes back as an error rather than as a blocked state.
func TestABrokenStoreStopsTheLead(t *testing.T) {
	s := newStore(t)
	nightly(t, s, "LUNA-1", fsm.KindChore)
	if err := s.Close(); err != nil {
		t.Fatalf("closing: %v", err)
	}

	l := &Lead{Store: s, Node: &deliveringNode{}, Judge: &alwaysBlocks{}}

	if _, err := l.Run(context.Background(), "LUNA-1"); err == nil {
		t.Error("a store that will not answer must stop the lead")
	}
}

// TestACustomFlowIsHonoured covers the Flow field.
//
// Flows are meant to be editable, so the lead must drive the one it was given
// rather than the shipped one.
func TestACustomFlowIsHonoured(t *testing.T) {
	s := newStore(t)

	flow := []fsm.Stage{
		{ID: "only", Requires: []fsm.Artifact{fsm.TaskID}, Produces: []fsm.Artifact{"something"}},
	}
	nightlyUnder(t, s, "LUNA-1", fsm.KindChore, flow)
	l := &Lead{Store: s, Node: &deliveringNode{}, Judge: &alwaysBlocks{}, Flow: flow}

	state, err := l.Run(context.Background(), "LUNA-1")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if state.Status != fsm.StatusDone {
		t.Errorf("a one-stage flow finishes in one stage, got %q", state.Status)
	}
	if !state.Context.HasArtifact("something") {
		t.Error("the custom stage's product should be in the context")
	}
}

// TestARefusedActionNeverReachesTheLog covers the guard in record.
//
// The lead checks the reducer before appending. A log that will not replay is
// worse than a task that refused to move: the first loses the task, the second
// only pauses it.
func TestARefusedActionNeverReachesTheLog(t *testing.T) {
	s := newStore(t)
	nightly(t, s, "LUNA-1", fsm.KindChore)

	l := &Lead{Store: s, Node: &deliveringNode{}, Judge: &alwaysBlocks{}}

	// Approving a gate that was never opened is illegal for the reducer.
	err := l.record("LUNA-1", fsm.GateApprove{})
	if err == nil {
		t.Fatal("an action the reducer refuses must not be recorded")
	}

	if _, err := s.Replay("LUNA-1", fsm.DefaultFlow()); err != nil {
		t.Errorf("the log must still replay after a refusal: %v", err)
	}
}

// TestAStageOutsideTheFlowIsHandledGracefully covers the fallback in stageIn.
//
// A log naming a stage the current flow no longer has is possible once flows are
// editable. The lookup returns a stage that declares nothing rather
// than panicking, so the contract's exit check is what reports the problem.
func TestAStageOutsideTheFlowIsHandledGracefully(t *testing.T) {
	stage := stageIn(fsm.DefaultFlow(), "a-stage-that-was-removed")

	if stage.ID != "a-stage-that-was-removed" {
		t.Errorf("the fallback keeps the id it was asked about, got %q", stage.ID)
	}
	if len(stage.Requires) != 0 || len(stage.Produces) != 0 {
		t.Error("a stage that is not in the flow declares nothing")
	}
}

// ── the judge that ships ──────────────────────────────────────────

// TestTheBudgetJudgeSpendsTheBudgetBeforeBlocking is why it exists.
//
// Without a Judge the lead blocked on the first failure, so the retry
// budget was never spent and the hybrid lead was, in production, a
// purely deterministic one. That was a behaviour nobody chose — it was the zero
// value of an optional field.
func TestTheBudgetJudgeSpendsTheBudgetBeforeBlocking(t *testing.T) {
	cases := []struct {
		attempts int
		max      int
		want     Decision
	}{
		{attempts: 0, max: 2, want: DecideRetry},
		{attempts: 1, max: 2, want: DecideRetry},
		{attempts: 2, max: 2, want: DecideBlock},
		{attempts: 3, max: 2, want: DecideBlock},
		// A task with no budget at all blocks immediately, which is the honest
		// reading of "no attempts allowed".
		{attempts: 0, max: 0, want: DecideBlock},
	}

	for _, c := range cases {
		state := fsm.TaskState{Retry: fsm.Retry{Attempts: c.attempts, Max: c.max}}
		got := BudgetJudge{}.OnFailure(context.Background(), state, "the node broke")
		if got != c.want {
			t.Errorf("%d of %d attempts spent: want %q, got %q", c.attempts, c.max, c.want, got)
		}
	}
}

// TestTheJudgeReadsTheBudgetFromTheState keeps it from holding a second copy.
//
// A counter kept here would disagree with the log after the first restart, and
// the log is the state. This is the same reason the reducer owns
// every other count.
func TestTheJudgeReadsTheBudgetFromTheState(t *testing.T) {
	generous := fsm.TaskState{Retry: fsm.Retry{Attempts: 4, Max: 9}}
	if got := (BudgetJudge{}).OnFailure(context.Background(), generous, ""); got != DecideRetry {
		t.Errorf("a task with a larger budget keeps retrying, got %q", got)
	}
}

// TestInfrastructureBlocksWithoutSpendingTheBudget covers losing the runner through the
// path that now exists.
//
// A harness that is not installed is not a stage that failed, and it will not be
// installed on the second attempt. Wiring the judge is what made this visible: with the lead
// blocking on everything, nothing distinguished the two.
func TestInfrastructureBlocksWithoutSpendingTheBudget(t *testing.T) {
	s := newStore(t)
	nightly(t, s, "LUNA-1", fsm.KindChore)

	judge := &countingJudge{}
	l := &Lead{Store: s, Node: brokenMachineryNode{}, Judge: judge}

	state, err := l.Run(context.Background(), "LUNA-1")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if state.Status != fsm.StatusBlocked {
		t.Errorf("want the task blocked, got %q", state.Status)
	}
	if state.Retry.Attempts != 0 {
		t.Errorf("infrastructure costs no retries, got %d spent", state.Retry.Attempts)
	}
	if judge.asked != 0 {
		t.Errorf("there is nothing to judge about a binary that is missing, got %d calls", judge.asked)
	}
}

// brokenMachineryNode stands in for the transport failing rather than the stage.
type brokenMachineryNode struct{}

func (brokenMachineryNode) Run(context.Context, fsm.TaskState, fsm.Stage) (Result, error) {
	return Result{}, fmt.Errorf("%w: the agent harness is not installed", ErrInfrastructure)
}

// countingJudge records whether it was consulted at all.
type countingJudge struct{ asked int }

func (j *countingJudge) OnFailure(context.Context, fsm.TaskState, string) Decision {
	j.asked++
	return DecideRetry
}

// committingNode is a node whose stages actually commit, which is what every
// real one does: the handoff is the commit.
type committingNode struct{ commits []string }

func (n *committingNode) Run(_ context.Context, _ fsm.TaskState, stage fsm.Stage) (Result, error) {
	sha := fmt.Sprintf("c0ffee%d", len(n.commits))
	n.commits = append(n.commits, sha)

	delivered := append(append([]fsm.Artifact{}, stage.Produces...), stage.ProducesForHuman...)
	evidence := map[fsm.Artifact]fsm.Evidence{}
	for _, a := range delivered {
		evidence[a] = fsm.Evidence{Scope: fsm.ScopeFull, Verdict: fsm.VerdictPassed}
	}
	return Result{Delivered: delivered, Evidence: evidence, Commit: sha}, nil
}

// TestTheBaseFollowsWhatTheStageCommitted is the handoff working end to end.
//
// The reducer has always advanced the base from `Complete.Commit`, and `luna
// done --commit` filled it in — but the node never reported one, so every stage
// driven by `luna run` branched from wherever the task started. Measured on a
// real 14-stage run: nine stages closed and seven of their commits had the
// pre-task commit as their parent, so the reviewer reviewed a tree with none of
// the implementer's work in it.
func TestTheBaseFollowsWhatTheStageCommitted(t *testing.T) {
	s := newStore(t)
	nightly(t, s, "LUNA-1", fsm.KindChore)

	node := &committingNode{}
	conductor := &Lead{Store: s, Node: node, Judge: &alwaysRetries{}}

	state, err := conductor.Run(context.Background(), "LUNA-1")
	if err != nil {
		t.Fatalf("Run: %v", err)
	}

	if len(node.commits) < 2 {
		t.Fatalf("want several stages to have run, got %d", len(node.commits))
	}
	if want := node.commits[len(node.commits)-1]; state.Base != want {
		t.Errorf("base = %q, want %q — the stage's commit never became the handoff", state.Base, want)
	}
}

// TestAFailureToLandDoesNotFailTheTask covers the direction that matters when a
// ref will not move.
//
// The work is committed by then and the state records the commit. Turning "it
// delivered twelve stages" into "it failed" because a branch would not move
// would lose the more important of the two — the same reasoning the worktree
// cleanup already follows. It is reported rather than swallowed.
func TestAFailureToLandDoesNotFailTheTask(t *testing.T) {
	s := newStore(t)
	nightly(t, s, "LUNA-1", fsm.KindChore)
	if err := s.AppendAction("LUNA-1", fsm.SetKnob{Knob: fsm.KnobAll}); err != nil {
		t.Fatalf("setting the knob: %v", err)
	}

	var warned string
	l := &Lead{
		Store: s, Node: &deliveringNode{}, Judge: &alwaysRetries{}, Ask: approvingLead(),
		Land: func(context.Context, string, string) error {
			return errors.New("the ref is checked out somewhere")
		},
		Warn: func(format string, args ...any) { warned = fmt.Sprintf(format, args...) },
	}

	state, err := l.Run(context.Background(), "LUNA-1")
	if err != nil {
		t.Fatalf("a branch that would not move failed the whole task: %v", err)
	}
	if state.Status != fsm.StatusDone {
		t.Errorf("the task did not finish, got %q", state.Status)
	}

	// Reported rather than swallowed: a task whose branch is stranded and says
	// nothing is the bug this whole feature exists to close.
	if !strings.Contains(warned, "LUNA-1") || !strings.Contains(warned, "checked out") {
		t.Errorf("the failure was not reported: %q", warned)
	}
}

// TestEnterAnswersAGateTheKnobReaches is the fix for an autonomy setting that
// could not reach the only gate the flow has.
//
// A review gate opens on the way *out* of the stage that produced its artifact,
// so the task sits at `awaiting_gate` and the decision is taken on the next
// Advance. Enter refused to advance from that status — reasoning that a gate is
// waiting for a person — which is true only when nobody is authorised to answer
// it. With the knob at or above the gate's criticality, somebody is.
//
// Measured on TALLY-5: knob 9, gate criticality 9, `judge_by_reading` declared,
// and the loop still ended at `wait: review the plan and its contract`. `luna
// run` never had this — its own step advances from any non-running status.
func TestEnterAnswersAGateTheKnobReaches(t *testing.T) {
	s := newStore(t)
	flow := fsm.DefaultFlow()

	// Driven to the gate the way a run reaches it: stages deliver, and the review
	// gate opens when the stage that produced its artifact closes.
	if err := s.AppendAction("LUNA-1", fsm.TaskCreated{
		Kind: fsm.KindFeature, Flow: fsm.Fingerprint(flow),
	}); err != nil {
		t.Fatalf("creating: %v", err)
	}
	if err := s.AppendAction("LUNA-1", fsm.SetKnob{Knob: fsm.Knob(9)}); err != nil {
		t.Fatalf("setting the knob: %v", err)
	}

	driver := &Lead{Store: s, Flow: flow, Node: &deliveringNode{}, Judge: &alwaysBlocks{}}
	if _, err := driver.Run(context.Background(), "LUNA-1"); err != nil {
		t.Fatalf("driving to the gate: %v", err)
	}
	if at, _ := s.Replay("LUNA-1", flow); at.Status != fsm.StatusAwaitingGate {
		t.Fatalf("this test needs a task at a gate, got %q", at.Status)
	}

	asked := 0
	l := &Lead{
		Store: s,
		Flow:  flow,
		Ask: func(context.Context, string) (string, error) {
			asked++
			return "APPROVE\n\nevery criterion is met", nil
		},
	}

	if err := l.Enter(context.Background(), "LUNA-1"); err != nil {
		t.Fatalf("entering past an answered gate: %v", err)
	}

	if asked == 0 {
		t.Error("the knob reached the gate and no judgement was asked for")
	}

	after, err := s.Replay("LUNA-1", flow)
	if err != nil {
		t.Fatalf("replaying: %v", err)
	}
	if after.Status == fsm.StatusAwaitingGate {
		t.Error("the task is still waiting at a gate the knob authorised the lead to answer")
	}
}

// TestTheLeadJudgesTheArtifactRatherThanItsHash is the other half of a fix that
// `luna gate show` already had and the lead did not.
//
// An artifact handed to Luna is in the store; the gate's payload carries the
// evidence line that names it — "handed over to Luna, 9e727…". That is the right
// thing for an audit record and useless to read: a hash contains no obligations,
// so a lead asked to judge it says so, correctly, every time.
//
// Measured on TALLY-5, where the knob and the criticality both said the lead
// should answer and the loop ended at "wait" anyway. `gate show` fetches the
// blob for a person with a comment saying exactly this — "not something a person
// can review". Neither is a model.
func TestTheLeadJudgesTheArtifactRatherThanItsHash(t *testing.T) {
	var judged string
	l := &Lead{
		Ask: func(_ context.Context, prompt string) (string, error) {
			judged = prompt
			return "APPROVE", nil
		},
		Artifact: func(string, string) (string, bool) {
			return "# contract\n\n- `--avg` MUST print the mean.\n", true
		},
	}

	spec := &fsm.GateSpec{
		Kind: fsm.GateReviewArtifact, Artifact: "contract",
		Judge: []string{"the contract states what is required"},
	}
	gate := &fsm.PendingGate{
		Kind: fsm.GateReviewArtifact, Stage: "plan",
		Artifact: "contract", Payload: "handed over to Luna, 9e72757f0fdc",
	}

	state := fsm.TaskState{ID: "LUNA-1"}
	if got := l.judge(context.Background(), state, spec, gate); got != fsm.GateDecisionJudged {
		t.Fatalf("the lead could not judge an artifact it was given, got %q", got)
	}
	if !strings.Contains(judged, "--avg` MUST print the mean") {
		t.Errorf("the lead was asked to judge something other than the artifact:\n%s", judged)
	}
	if strings.Contains(judged, "handed over to Luna") {
		t.Error("the evidence line reached the model as though it were the artifact")
	}
}

// TestTheLeadIsGivenTheTaskWhenItJudges covers the wiring rather than the brief.
//
// JudgingBrief can carry a statement and still never receive one: the caller had
// only the task's id, and the statement lives on the state beside it. A test of
// the brief alone passes either way, which is why this one goes through judge.
func TestTheLeadIsGivenTheTaskWhenItJudges(t *testing.T) {
	var judged string
	l := &Lead{
		Ask: func(_ context.Context, prompt string) (string, error) {
			judged = prompt
			return "APPROVE", nil
		},
		Artifact: func(string, string) (string, bool) {
			return "# contract\n\n- `--avg` MUST print the mean.\n", true
		},
	}

	spec := &fsm.GateSpec{
		Kind: fsm.GateReviewArtifact, Artifact: "contract",
		Judge: []string{"every acceptance criterion in the task appears as an obligation"},
	}
	gate := &fsm.PendingGate{
		Kind: fsm.GateReviewArtifact, Stage: "plan", Artifact: "contract",
	}
	state := fsm.TaskState{
		ID: "LUNA-2",
		Statement: fsm.Statement{
			Description: "add an --avg flag to tally.sh",
			Acceptance:  "tally.sh --avg 1 2 3 prints 2",
		},
	}

	if got := l.judge(context.Background(), state, spec, gate); got != fsm.GateDecisionJudged {
		t.Fatalf("judging: got %q", got)
	}
	if !strings.Contains(judged, "tally.sh --avg 1 2 3 prints 2") {
		t.Errorf("the lead was asked about the task's acceptance without being given it:\n%s", judged)
	}
}

// TestFindingsThatDoNotBlockAreStillPutInFrontOfAPerson covers the other half of
// a narrow blocking rule.
//
// Only a defect this change introduced sends the work back. That is deliberate:
// blocking on inherited ones turns every task into an audit of the repository,
// and the work reopens to fix legacy nobody asked about. But an inherited defect
// is still a defect somebody verified against a named input, and leaving it in a
// report nobody was told to open is how it is never read.
//
// Measured on TALLY-7: four real defects, every one of them true, and the only
// place any of them existed was the report.
func TestFindingsThatDoNotBlockAreStillPutInFrontOfAPerson(t *testing.T) {
	// Driven through a real review rather than by calling the reporter: what was
	// missing is that nothing on the path invoked it, and a test that calls it
	// directly passes with the call site removed.
	var warned []string
	report := strings.Join([]string{
		"[SHOULD-FIX] B1 tally.sh --avg -- 1 2 3 prints 0 at exit 0",
		"[NIT] N1 the header comment could name the flag",
		"[BLOCKING] B2 the acceptance criterion does not hold",
	}, "\n")

	runReviewWatching(t, report, func(format string, args ...any) {
		warned = append(warned, fmt.Sprintf(format, args...))
	})

	if !anyContains(warned, "prints 0 at exit 0") {
		t.Errorf("a should-fix finding never reached a person: %v", warned)
	}
	if !anyContains(warned, "could name the flag") {
		t.Errorf("a nit never reached a person: %v", warned)
	}

	// The blocking one is not repeated here: it reopens the stage, and that is
	// where it is read. Saying it twice trains a reader to skim both.
	if anyContains(warned, "acceptance criterion does not hold") {
		t.Errorf("a blocking finding was reported as though it were not: %v", warned)
	}
}

func anyContains(list []string, want string) bool {
	for _, got := range list {
		if strings.Contains(got, want) {
			return true
		}
	}
	return false
}
