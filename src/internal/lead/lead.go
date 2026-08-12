// Package lead conducts a task through the flow.
//
// It is the loop the design calls hybrid (ADR-0002): code decides the next stage,
// calls the node, checks the delivery and records the transition — deterministic,
// zero tokens. When something goes off the rails, a model decides what to do with
// it, and that judgement arrives through the Judge interface rather than being
// wired in here.
//
// The lead owns no state. Everything it knows it read from the store, and
// everything it decides it writes back before acting on it — so a process killed
// mid-task loses nothing but the work in flight.
package lead

import (
	"context"
	"errors"
	"fmt"

	"github.com/brunoomariano/luna/src/internal/fsm"
	"github.com/brunoomariano/luna/src/internal/store"
)

// ErrStalled is returned when a task stops making progress without failing. It is
// the failure mode the design calls the most expensive one: a fleet that stops
// talking stops in silence (ADR-0019).
var ErrStalled = errors.New("the task stopped making progress")

// Node runs one stage and reports what came back.
//
// This is the boundary between the engine and the world. The implementation
// arrives with wave 5; until then the interface is what lets the lead be built
// and tested without a network, a process, or an agent.
type Node interface {
	// Run executes the stage and returns what it delivered, with the evidence for
	// each artifact. The evidence comes from running the real tool — the test
	// passed, the file exists, the commit resolves (ADR-0024, INV-core-4).
	Run(ctx context.Context, state fsm.TaskState, stage fsm.Stage) (Result, error)
}

// Result is what a node reports. It is deliberately not a fsm.Action: turning it
// into one is the lead's job, and keeping them apart means a node cannot decide a
// transition.
type Result struct {
	Delivered []fsm.Artifact
	Evidence  map[fsm.Artifact]fsm.Evidence
}

// Decision is what the model chose to do about a failure.
type Decision string

const (
	// DecideRetry runs the stage again with the error in context.
	DecideRetry Decision = "retry"

	// DecideBlock stops and notifies. Every blocked task notifies (INV-core-8).
	DecideBlock Decision = "block"
)

// Judge is the judgement layer of the hybrid lead. It is consulted only when
// something has already gone wrong — never on the happy path, where a model in
// the loop would cost tokens and determinism for nothing.
//
// The reason it exists as an interface rather than a call to an LLM is
// testability, but there is a second reason worth naming: the design's own
// argument for a hybrid lead comes from a case where the machine was wrong and
// the model caught it (ADR-0002). A judgement layer that cannot be swapped cannot
// be studied.
type Judge interface {
	OnFailure(ctx context.Context, state fsm.TaskState, reason string) Decision
}

// GatePolicy decides whether a gate stops the task.
//
// It is an interface rather than a map because the answer comes from
// configuration, and the reducer may not read configuration (ADR-0024). The lead
// asks it once, and what it answered goes into the log — so replaying the task
// never asks again, and editing a profile cannot rewrite what already happened
// (ADR-0026).
type GatePolicy interface {
	// Waits reports whether a gate of this kind stops a task on this profile.
	Waits(profile fsm.Profile, gate fsm.GateKind) bool
}

// Watchdog reports whether a task has stopped making progress.
//
// Separate from Node because the question is different: a node that returns an
// error failed, and a node that returns nothing at all may simply be slow. Only
// something outside the call can tell those apart (ADR-0019).
type Watchdog interface {
	// Stalled is consulted after each transition. Returning true turns the task
	// into the same decision path as a failure — the model chooses what to do.
	Stalled(state fsm.TaskState) bool
}

// Lead conducts one task. One per task, never shared: the parallelism is between
// tasks, not inside them (ADR-0003).
type Lead struct {
	Store *store.Store
	Node  Node
	Judge Judge

	// Watchdog is optional. Without one, a node that hangs hangs the lead — which
	// is the honest behaviour until wave 5 gives it something to measure.
	Watchdog Watchdog

	// Gates is optional. Without one the lead records no gate decision, and the
	// replay falls back to the shipped policy — the same behaviour as a log
	// written before decisions were recorded.
	Gates GatePolicy

	// Flow defaults to the shipped one when empty.
	Flow []fsm.Stage
}

// Run drives a task until it needs a person or reaches the end.
//
// It returns the state it stopped at rather than an error for the ordinary
// endings: done, waiting at a gate, and blocked are all outcomes, not failures.
// An error means the lead itself could not continue — the store would not answer,
// or the log will not replay.
func (l *Lead) Run(ctx context.Context, taskID string) (fsm.TaskState, error) {
	flow := l.Flow
	if len(flow) == 0 {
		flow = fsm.DefaultFlow()
	}

	for {
		state, err := l.Store.Replay(taskID, flow)
		if err != nil {
			return fsm.TaskState{}, err
		}

		// Three endings, none of them the lead's to push past. A gate is waiting on
		// a person, a block is waiting on a person, and done is done.
		if state.IsTerminal() || state.NeedsHuman() {
			return state, nil
		}

		// Cancellation is checked before doing anything rather than after: the point
		// of stopping is not to start the next thing.
		if err := ctx.Err(); err != nil {
			return state, err
		}

		if err := l.step(ctx, taskID, state, flow); err != nil {
			return fsm.TaskState{}, err
		}
	}
}

// step performs exactly one transition and records it. Splitting it out keeps Run
// a loop over outcomes rather than a loop with a body.
func (l *Lead) step(ctx context.Context, taskID string, state fsm.TaskState, flow []fsm.Stage) error {
	// Anything but a running node means the next move is to enter a stage — a task
	// that has not started, or one whose stage just closed. The status says which,
	// so the lead never has to infer it.
	if state.Status != fsm.StatusRunning {
		return l.record(taskID, fsm.Advance{
			Flow:         flow,
			GateDecision: l.decideGate(state, fsm.GateAhead(state, flow)),
		})
	}

	stage := stageIn(flow, state.Stage)

	if l.Watchdog != nil && l.Watchdog.Stalled(state) {
		return l.stall(taskID, fmt.Sprintf("%s at stage %q", ErrStalled, state.Stage))
	}

	result, err := l.Node.Run(ctx, state, stage)
	if err != nil {
		// A stall is a decision, not a judgement call: the node observed that the
		// agent is alive and doing nothing, and there is nothing for a model to
		// weigh (ADR-0034). It also must not spend the retry budget — that budget
		// is for a stage that failed, and a stall says nothing about the stage.
		if errors.Is(err, ErrStalled) {
			return l.stall(taskID, err.Error())
		}
		return l.handleFailure(ctx, taskID, state, err.Error())
	}

	// The verdict came from outside; the reducer decides what it means (ADR-0024).
	// A stage that delivered less than it promised will not close, and the lead
	// does not argue with that — it records the attempt and lets the next pass see
	// a blocked task.
	return l.record(taskID, fsm.Complete{
		Delivered: result.Delivered,
		Evidence:  result.Evidence,
		Flow:      flow,
	})
}

// decideGate asks the policy about a gate and turns the answer into the value the
// log will carry.
//
// A stage that opens no gate records no decision: there was nothing to decide, and
// writing "passed" would claim a gate was reached that never was. With no policy
// configured it also records nothing, which leaves replay on the shipped defaults
// — the honest reading, since nothing else decided.
func (l *Lead) decideGate(state fsm.TaskState, gate *fsm.PendingGate) fsm.GateWaited {
	if gate == nil || l.Gates == nil {
		return fsm.GateDecisionAbsent
	}
	if l.Gates.Waits(state.Profile, gate.Kind) {
		return fsm.GateDecisionWaited
	}
	return fsm.GateDecisionPassed
}

// stall records a task that stopped making progress.
//
// It is a block rather than a failure, and the distinction is load-bearing: a
// failure says the stage went wrong, a stall says nothing about the stage at all.
// Collapsing them would spend the retry budget on an agent that is not going to
// react, and would leave the audit unable to tell the two apart at exactly the
// moment someone needs to know which happened (ADR-0034, ADR-0011).
func (l *Lead) stall(taskID, reason string) error {
	return l.record(taskID, fsm.Block{Reason: reason})
}

// handleFailure asks the judge what to do and records the answer.
//
// The retry budget is the reducer's to spend: recording a Fail is what increments
// it, and what turns the third one into a block. The judge choosing retry does not
// override that — it only means the lead is not escalating early.
func (l *Lead) handleFailure(ctx context.Context, taskID string, state fsm.TaskState, reason string) error {
	decision := DecideBlock
	if l.Judge != nil {
		decision = l.Judge.OnFailure(ctx, state, reason)
	}

	switch decision {
	case DecideRetry:
		return l.record(taskID, fsm.Fail{Reason: reason})
	case DecideBlock:
		// One decision, one event. Spending the retry budget to reach a block would
		// leave three failures in the log where there was one choice to escalate,
		// and the history is the audit trail (INV-core-2).
		return l.record(taskID, fsm.Block{Reason: reason})
	default:
		return fmt.Errorf("the judge returned an unknown decision %q", decision)
	}
}

// record appends a transition after checking the reducer accepts it.
//
// Checking first matters: an action the reducer would refuse must not reach the
// log, because a log that will not replay is worse than a task that refused to
// move.
func (l *Lead) record(taskID string, action fsm.Action) error {
	flow := l.Flow
	if len(flow) == 0 {
		flow = fsm.DefaultFlow()
	}

	state, err := l.Store.Replay(taskID, flow)
	if err != nil {
		return err
	}
	if _, err := fsm.Reduce(state, action); err != nil {
		return fmt.Errorf("refusing to record %T on %s: %w", action, taskID, err)
	}

	// Conditional on the log still ending where it was read. The dry run above
	// validated this action against `state`, and between reading it and writing
	// there is a window: something landing in it means the decision was made
	// against a task that has since moved, and appending anyway would put two
	// decisions taken from one state into a log that cannot be repaired
	// (ADR-0047).
	return l.Store.AppendActionAt(taskID, state.Seq, action)
}

func stageIn(flow []fsm.Stage, id fsm.StageID) fsm.Stage {
	for _, s := range flow {
		if s.ID == id {
			return s
		}
	}
	return fsm.Stage{ID: id}
}
