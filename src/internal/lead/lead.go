// Package lead conducts a task through the flow, in either of two shapes.
//
// Lead is the loop the design calls hybrid (ADR-0002): code decides the next
// stage, calls the node, checks the delivery and records the transition —
// deterministic, zero tokens. When something goes off the rails, a model decides
// what to do with it, and that judgement arrives through the Judge interface
// rather than being wired in here. It is what `luna run` uses, and it needs no
// model at all.
//
// Agent is the same task conducted by a model, so a person can talk to the thing
// running it (ADR-0056). What does not change is who decides the stage: the
// agent is handed one closed order at a time and its answer is never parsed, so
// there is no path from anything it says to a transition. It is what `luna lead`
// uses.
//
// The lead owns no state. Everything it knows it read from the store, and
// everything it decides it writes back before acting on it — so a process killed
// mid-task loses nothing but the work in flight.
package lead

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/brunoomariano/luna/src/internal/fsm"
	"github.com/brunoomariano/luna/src/internal/store"
)

// ErrStalled is returned when a task stops making progress without failing. It is
// the failure mode the design calls the most expensive one: a fleet that stops
// talking stops in silence (ADR-0019).
var ErrStalled = errors.New("the task stopped making progress")

// ErrInfrastructure is returned when the machinery around the task broke rather
// than the work in it — herdr went away, a socket died, a worktree vanished.
//
// It is kept apart from an ordinary failure because the retry budget is for a
// stage that failed, and infrastructure says nothing about the stage (ADR-0011,
// ADR-0033). Retrying it would also be retrying the wrong thing: a herdr that is
// not running will not be running on the second attempt either.
//
// The node layer wraps whatever its own transport reported, so the lead learns the
// distinction without importing the transport (ADR-0030).
var ErrInfrastructure = errors.New("the machinery around the task broke")

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

	// Commit is what the stage delivered, and it is the handoff: the next stage
	// branches from it (ADR-0055, INV-core-6).
	//
	// Empty means the stage committed nothing, and then the base does not move —
	// which is right for a mechanical stage, and is a stage that produced nothing
	// durable for any other. It is not an error here: the contract check decides
	// whether the stage closes, and this only decides where the next one starts.
	Commit string
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

// Lead conducts one task. One per task, never shared: the parallelism is between
// tasks, not inside them (ADR-0003).
// There is no watchdog field, and the absence is deliberate. ADR-0019 imagined one
// polling the state between transitions; ADR-0034 replaced that with delegation,
// and delegation is what shipped — herdr bounds the wait and a stall arrives as
// ErrStalled from Node.Run, which the loop below already handles. A second
// interface asking a replayed TaskState whether it looks stuck could only answer
// from a clock the state does not carry, which is why nothing but a test fake ever
// implemented it (ADR-0051).
type Lead struct {
	Store *store.Store
	Node  Node
	Judge Judge

	// Gates is optional. Without one the lead records no gate decision, and the
	// replay falls back to the shipped policy — the same behaviour as a log
	// written before decisions were recorded.
	Gates GatePolicy

	// Flow defaults to the shipped one when empty.
	Flow []fsm.Stage

	// CheckGate runs the commands a person declared as the answer to one gate,
	// over what the task delivered, and reports what they concluded.
	//
	// A function rather than an interface pair because it is the seam between two
	// things the lead deliberately does not own: which commands answer a gate
	// lives in the registry, and running them belongs to the node layer. The lead
	// only needs the verdict, which is the ADR-0024 shape — the observation
	// arrives, the decision is taken here.
	//
	// Nil means no mechanical half at all, which is what a run with no registry
	// configured has. Then every gate is decided by the judgement half alone,
	// which is exactly today's behaviour.
	CheckGate func(ctx context.Context, taskID string, gate fsm.GateKind) fsm.GateChecksOutcome

	// Ask is how the lead judges a gate the knob reached. It is the same boundary
	// Agent uses and the same one ADR-0043 draws: Luna hosts no model of its own.
	//
	// Nil is the ordinary case — `luna run` needs no model, and a run with none
	// sends every judgement to a person rather than approving what nobody looked
	// at.
	Ask func(ctx context.Context, prompt string) (string, error)
}

// Run drives a task until it needs a person or reaches the end.
//
// It returns the state it stopped at rather than an error for the ordinary
// endings: done, waiting at a gate, and blocked are all outcomes, not failures.
// An error means the lead itself could not continue — the store would not answer,
// or the log will not replay.
func (l *Lead) Run(ctx context.Context, taskID string) (fsm.TaskState, error) {
	flow := l.flow()

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
			GateDecision: l.decideGate(ctx, state, fsm.GateAhead(state, flow)),
		})
	}

	stage := stageIn(flow, state.Stage)

	result, err := l.Node.Run(ctx, state, stage)
	if err != nil {
		// A stall is a decision, not a judgement call: the node observed that the
		// agent is alive and doing nothing, and there is nothing for a model to
		// weigh (ADR-0034). It also must not spend the retry budget — that budget
		// is for a stage that failed, and a stall says nothing about the stage.
		if errors.Is(err, ErrStalled) {
			return l.stall(taskID, err.Error())
		}
		// Infrastructure blocks without consulting anyone and without spending the
		// budget: there is no judgement to make about a herdr that went away, and
		// the budget belongs to the stage (ADR-0033).
		if errors.Is(err, ErrInfrastructure) {
			return l.record(taskID, fsm.Block{Reason: err.Error()})
		}
		return l.handleFailure(ctx, taskID, state, err.Error())
	}

	// The verdict came from outside; the reducer decides what it means (ADR-0024).
	// A stage that delivered less than it promised will not close, and the lead
	// does not argue with that — it records the attempt and lets the next pass see
	// a blocked task.
	if err := l.record(taskID, fsm.Complete{
		Delivered: result.Delivered,
		Evidence:  result.Evidence,
		Commit:    result.Commit,
		Flow:      flow,
	}); err != nil {
		return err
	}

	// Read back rather than reusing the state from before the Complete: the base
	// only moves on the path where the stage actually closed, and that is exactly
	// the signal the round is judged by.
	after, err := l.Store.Replay(taskID, flow)
	if err != nil {
		return err
	}
	return l.readReview(taskID, stage, after, result)
}

// readReview turns a review stage's report into a transition, when it carries
// one.
//
// This is what ADR-0041 specified and nothing implemented: the reviewer produces
// a report like any other artifact, and **Luna reads it**. A `[BLOCKING]`
// finding sends the work back; anything else lets the flow carry on.
//
// The agent never emits the action, and that is the point. Handing the reviewer
// a `luna review-finding --aligned` would be the direct route and would put a
// transition in a model's hands — with nothing able to stop a false one, and
// nothing able to detect it. So the model reports and the code decides, which is
// INV-core-1 applied to the one place where letting the model decide would look
// most reasonable: it has just finished forming an opinion, and acting on one is
// the obvious next step.
//
// A stage that is not a review reads nothing. A review whose report has no
// recognisable finding reads nothing either, which is the honest outcome — the
// report is in the context, and a person can see what was written.
func (l *Lead) readReview(taskID string, stage fsm.Stage, state fsm.TaskState, result Result) error {
	artifact, ok := fsm.ReviewedArtifact(stage)
	if !ok {
		return nil
	}

	// The delivered content travels in the evidence's detail, which is where the
	// gate payload reads it from too (ADR-0024: the node ran the tool, and what it
	// saw arrives in the action).
	findings := fsm.ReadReport(result.Evidence[artifact].Detail)
	if !fsm.Blocks(findings) {
		return nil
	}

	return l.record(taskID, fsm.ReviewFinding{
		Aligned:  true,
		Summary:  summarise(findings),
		Progress: progressOf(state, result),
		Flow:     l.flow(),
	})
}

// progressOf is what this round produced, for the next one to compare against
// (PRD node-0002).
//
// It is the delivered commit. That is the closest thing Luna has to "did the
// work change", and it is exact rather than approximate: the handoff *is* the
// commit (INV-core-6), so two rounds delivering the same sha delivered the same
// work — no hashing, no diff, no guessing which parts of a diff are meaningful.
//
// The PRD's open question asked what to hash, listing artifacts, the worktree
// diff and the evidence, and worried about meaningless variation — a timestamp
// in a diff that never matches itself. The commit sidesteps that entirely, and
// only because the handoff moved to git first (ADR-0055): hashing a worktree
// would have had exactly the problem the question describes.
//
// A round that delivered no commit says nothing rather than guessing, which the
// reducer reads as "no comparison available".
func progressOf(state fsm.TaskState, result Result) string {
	if state.Base != "" {
		return state.Base
	}
	// Nothing was committed. The evidence is what the tools reported, and an
	// empty signal is the honest answer — a detector that invents one fires on
	// its own invention.
	_ = result
	return ""
}

// summarise names the blocking findings, so the log says what sent the work back
// rather than that something did.
func summarise(findings []fsm.Finding) string {
	var blocking []string
	for _, f := range findings {
		if f.Severity != fsm.SeverityBlocking {
			continue
		}
		if f.ID != "" {
			blocking = append(blocking, f.ID+": "+f.Text)
			continue
		}
		blocking = append(blocking, f.Text)
	}
	return strings.Join(blocking, "; ")
}

// flow is what this lead runs, defaulting to the shipped one.
func (l *Lead) flow() []fsm.Stage {
	if len(l.Flow) == 0 {
		return fsm.DefaultFlow()
	}
	return l.Flow
}

// decideGate asks the policy about a gate and turns the answer into the value the
// log will carry.
//
// A stage that opens no gate records no decision: there was nothing to decide, and
// writing "passed" would claim a gate was reached that never was. With no policy
// configured it also records nothing, which leaves replay on the shipped defaults
// — the honest reading, since nothing else decided.
func (l *Lead) decideGate(ctx context.Context, state fsm.TaskState, gate *fsm.PendingGate) fsm.GateWaited {
	if gate == nil || l.Gates == nil {
		return fsm.GateDecisionAbsent
	}
	if !l.Gates.Waits(state.Profile, gate.Kind) {
		return fsm.GateDecisionPassed
	}

	// The profile said this gate waits. That is where the question used to end;
	// now it is where the two halves get their turn — a gate that waits may still
	// be answered by its declared checks or by the lead, and only then does it
	// reach a person (RFC-0006).
	return l.answerWaitingGate(ctx, state, gate)
}

// answerWaitingGate is who answers a gate the profile stopped for.
//
// The mechanical half runs through CheckGate rather than here: reading the
// registry and running commands is work for the node layer, and the verdict
// arrives as an observation the way every other one does (ADR-0024). What this
// function owns is the decision made from it, which is why the rule itself lives
// in fsm.ResolveGate and is testable without a registry or a shell.
func (l *Lead) answerWaitingGate(
	ctx context.Context, state fsm.TaskState, gate *fsm.PendingGate,
) fsm.GateWaited {
	spec := fsm.GateSpecIn(l.flow(), gate.Stage)

	checks := fsm.GateChecksOutcome{}
	if l.CheckGate != nil {
		checks = l.CheckGate(ctx, state.ID, gate.Kind)
	}

	switch fsm.ResolveGate(spec, checks, state.Knob) {
	case fsm.AnswerChecks:
		return fsm.GateDecisionChecked
	case fsm.AnswerLead:
		return l.judge(ctx, spec, gate)
	case fsm.AnswerRejected, fsm.AnswerPerson:
		return fsm.GateDecisionWaited
	default:
		// A resolution this build does not recognise falls to a person, which is
		// the direction every uncertain path in this design takes.
		return fsm.GateDecisionWaited
	}
}

// judge asks the lead to answer a gate against its declared criteria.
//
// The knob having reached this gate is permission to judge, not a judgement:
// recording `judged` without anyone having judged would put a fact in the log
// that nothing produced, which is the worst of both designs — a model's authority
// with no model involved.
//
// Everything that is not a clear approval falls to a person. A rejection does
// too, deliberately: the reducer answers a gate at the moment it opens, and there
// is no path from here to a rejection that sends work back. Recording a person's
// wait is honest about that — the gate is still open, and what the lead concluded
// belongs in front of whoever answers it.
func (l *Lead) judge(ctx context.Context, spec *fsm.GateSpec, gate *fsm.PendingGate) fsm.GateWaited {
	if l.Ask == nil {
		// The knob authorised a judgement and there is nobody to make it. Asking a
		// person is the only honest answer: the alternative is approving a gate
		// because no model was configured to look at it.
		return fsm.GateDecisionWaited
	}

	said, err := l.Ask(ctx, JudgingBrief(spec, gate.Payload, ""))
	if err != nil {
		return fsm.GateDecisionWaited
	}

	if ReadJudgement(said) == JudgedApprove {
		return fsm.GateDecisionJudged
	}
	return fsm.GateDecisionWaited
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
	flow := l.flow()

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
