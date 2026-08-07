package lead

import (
	"context"
	"errors"
	"path/filepath"
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

	evidence := map[fsm.Artifact]string{}
	for _, a := range delivered {
		evidence[a] = "the tool confirmed " + string(a)
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

// stalledWatchdog reports a task as stuck the moment it is asked.
type stalledWatchdog struct{ asked int }

func (w *stalledWatchdog) Stalled(fsm.TaskState) bool {
	w.asked++
	return true
}

func newStore(t *testing.T) *store.Store {
	t.Helper()

	s, err := store.Open(filepath.Join(t.TempDir(), "luna.db"))
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

	if err := s.AppendAction(id, fsm.TaskCreated{Kind: kind, Profile: fsm.ProfileNightly}); err != nil {
		t.Fatalf("creating %s: %v", id, err)
	}
}

// ── the happy path ───────────────────────────────────────────────────────────

// TestTheLeadDrivesATaskToTheEnd is the scenario the wave exists for.
//
// Given a node that always delivers and a profile that stops at nothing, the lead
// walks the whole flow and the task finishes — with no model consulted anywhere,
// because nothing went wrong (ADR-0002).
func TestTheLeadDrivesATaskToTheEnd(t *testing.T) {
	s := newStore(t)
	nightly(t, s, "LUNA-1", fsm.KindChore)

	node := &deliveringNode{}
	judge := &alwaysRetries{}
	l := &Lead{Store: s, Node: node, Judge: judge}

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
	if err := s.AppendAction("LUNA-1", fsm.TaskCreated{Kind: fsm.KindFeature}); err != nil {
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
	if state.Stage != "discovery" {
		t.Errorf("want it stopped at the first gate, got %q", state.Stage)
	}
}

// TestTheLeadResumesFromWhereItStopped covers what the store buys.
//
// A second lead, with no memory of the first, picks the task up from the log and
// carries on. This is the property that makes killing the process survivable
// (INV-core-2).
func TestTheLeadResumesFromWhereItStopped(t *testing.T) {
	s := newStore(t)
	if err := s.AppendAction("LUNA-1", fsm.TaskCreated{Kind: fsm.KindChore}); err != nil {
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

	if state.Stage == "discovery" && state.Status == fsm.StatusAwaitingGate {
		t.Error("the second lead should have moved past the answered gate")
	}
}

// ── failure and judgement ────────────────────────────────────────────────────

// TestAFailingNodeConsultsTheJudge covers the hybrid boundary.
//
// The model is asked only once something has gone wrong. That is the whole shape
// of ADR-0002: deterministic on the happy path, judgement where the machine has
// nothing to go on.
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
	// A blocked task that does not say why is the silent failure INV-core-8
	// forbids.
	if state.Blocked == "" {
		t.Error("a blocked task must carry its reason")
	}
}

// TestRetryIsBoundedEvenWhenTheJudgeKeepsSayingRetry covers the budget.
//
// The judgement layer chooses, but it does not get to choose forever: the retry
// ceiling belongs to the reducer, and a model that always says "try again" still
// ends at a block (ADR-0011). Without this, a confident judge is an infinite loop.
func TestRetryIsBoundedEvenWhenTheJudgeKeepsSayingRetry(t *testing.T) {
	s := newStore(t)
	nightly(t, s, "LUNA-1", fsm.KindChore)

	node := &failingNode{reason: "still broken"}
	judge := &alwaysRetries{}
	l := &Lead{Store: s, Node: node, Judge: judge}

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

// TestEvidenceReachesTheLog covers ADR-0024 from the lead's side.
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
	if state.Evidence["repos"] == "" {
		t.Errorf("want evidence for what discovery produced, got %v", state.Evidence)
	}
}

// ── the watchdog ─────────────────────────────────────────────────────────────

// TestAStalledTaskIsTreatedAsAFailure covers ADR-0019.
//
// A node that returns nothing is not a node that failed — only something outside
// the call can tell those apart. When the watchdog says the task stopped moving,
// it goes down the same judgement path a failure would.
func TestAStalledTaskIsTreatedAsAFailure(t *testing.T) {
	s := newStore(t)
	nightly(t, s, "LUNA-1", fsm.KindChore)

	watchdog := &stalledWatchdog{}
	judge := &alwaysBlocks{}
	l := &Lead{Store: s, Node: &deliveringNode{}, Judge: judge, Watchdog: watchdog}

	state, err := l.Run(context.Background(), "LUNA-1")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if watchdog.asked == 0 {
		t.Error("the watchdog must be consulted while a stage is running")
	}
	if judge.asked == 0 {
		t.Error("a stall goes to the judgement layer, like a failure")
	}
	if state.Status != fsm.StatusBlocked {
		t.Errorf("want the stalled task blocked, got %q", state.Status)
	}
}

// TestWithoutAWatchdogNothingIsChecked covers the optional field.
//
// A lead with no watchdog runs exactly as before. That is the honest behaviour
// until wave 5 provides something that can actually measure progress.
func TestWithoutAWatchdogNothingIsChecked(t *testing.T) {
	s := newStore(t)
	nightly(t, s, "LUNA-1", fsm.KindChore)

	l := &Lead{Store: s, Node: &deliveringNode{}, Judge: &alwaysBlocks{}}

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
// Flows are meant to be editable (ADR-0017), so the lead must drive the one it
// was given rather than the shipped one.
func TestACustomFlowIsHonoured(t *testing.T) {
	s := newStore(t)
	nightly(t, s, "LUNA-1", fsm.KindChore)

	flow := []fsm.Stage{
		{ID: "only", Requires: []fsm.Artifact{fsm.TaskID}, Produces: []fsm.Artifact{"something"}},
	}
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
// editable (ADR-0017). The lookup returns a stage that declares nothing rather
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
