package fsm

import (
	"errors"
	"fmt"
)

// ErrIllegalTransition is returned when an action makes no sense for the current
// status — completing a stage that is not running, approving a gate that is not
// open. It is an error rather than a silent no-op because a no-op would let the
// caller believe a transition happened.
var ErrIllegalTransition = errors.New("illegal transition")

// Action is something that happens to a task. The set is closed: these are all
// the ways a task can move.
type Action interface{ isAction() }

// Advance moves to the next stage of the flow, applying the contract's entry
// check on the way in.
type Advance struct{ Flow []Stage }

// Complete closes the running stage.
//
// Delivered and Evidence come from outside: the lead runs the real tool and hands
// the verdict in (ADR-0024). The reducer decides what it means, it does not go
// looking.
type Complete struct {
	Delivered []Artifact
	Evidence  map[Artifact]string
}

// Fail reports that the node broke. Retry until the budget is spent, then block
// and notify (ADR-0011).
type Fail struct{ Reason string }

// GateApprove accepts what the gate was holding, as it is.
type GateApprove struct{}

// GateAdjust accepts a human-edited version. The replacement is what carries on,
// and the edit is recorded (ADR-0022).
type GateAdjust struct{ Payload string }

// GateReject refuses the artifact. It does not enter the context, and the stage
// that produced it runs again with the rejection in hand.
type GateReject struct{ Reason string }

// ReviewFinding is what a review stage found. Aligned sends the work back to
// build; out of scope becomes a separate task and the flow carries on. The
// distinction is a judgement call, which is why it arrives as a decision rather
// than being computed here.
type ReviewFinding struct {
	Aligned bool
	Summary string
	Limits  LoopLimits
}

// Unblock is a human clearing a block.
type Unblock struct{}

func (Advance) isAction()       {}
func (Complete) isAction()      {}
func (Fail) isAction()          {}
func (GateApprove) isAction()   {}
func (GateAdjust) isAction()    {}
func (GateReject) isAction()    {}
func (ReviewFinding) isAction() {}
func (Unblock) isAction()       {}

// reviewStages are the only ones allowed to produce a finding. An implementer
// sending its own work back would be the self-review INV-core-7 rules out.
var reviewStages = map[StageID]bool{
	"qa": true, "code-review": true, "harden": true, "architecture": true,
}

// Reduce applies an action to a task and returns the resulting state.
//
// It is pure: no clock, no filesystem, no process. Everything it needs to decide
// arrives in the action (ADR-0024), which is what makes a transition reproducible
// from the append-only log and testable without infrastructure.
//
// An error means the action was illegal for this state. A task that stops for a
// legitimate reason — a missing input, a spent retry budget — comes back as a
// blocked state, not an error: that is a fact about the task, not a bug in the
// caller.
func Reduce(state TaskState, action Action) (TaskState, error) {
	switch a := action.(type) {
	case Advance:
		return advance(state, a)
	case Complete:
		return complete(state, a)
	case Fail:
		return fail(state, a)
	case GateApprove, GateAdjust, GateReject:
		return answerGate(state, action)
	case ReviewFinding:
		return reviewFinding(state, a)
	case Unblock:
		return unblock(state)
	default:
		return state, fmt.Errorf("%w: unknown action %T", ErrIllegalTransition, action)
	}
}

func advance(state TaskState, a Advance) (TaskState, error) {
	// Every status is named rather than relying on a default: when a sixth one is
	// added, the compiler-adjacent check makes this switch the place that has to
	// decide about it, instead of silently letting it advance.
	switch state.Status {
	case StatusReady, StatusRunning:
		// The two that may advance: a task that has not started, and one whose
		// stage just closed.
	case StatusAwaitingGate:
		return state, fmt.Errorf("%w: a gate is pending on %q", ErrIllegalTransition, state.Stage)
	case StatusBlocked:
		return state, fmt.Errorf("%w: the task is blocked", ErrIllegalTransition)
	case StatusDone:
		return state, fmt.Errorf("%w: the task is finished", ErrIllegalTransition)
	}

	next, ok, err := NextStage(a.Flow, state.Stage, state.Context)
	if err != nil {
		return state, err
	}
	if !ok {
		state.Status = StatusDone
		state.Stage = ""
		return state, nil
	}

	stage := stageIn(a.Flow, next)

	// The contract's entry check: the FSM does not call an agent for a stage whose
	// inputs are not there. Without it the agent would start blind, and the
	// failure would look like the model being dumb (INV-core-3).
	if missing := MissingFor(stage, state.Context); len(missing) > 0 {
		state.Status = StatusBlocked
		state.Blocked = fmt.Sprintf("stage %q requires %v, which the context does not hold", next, missing)
		return state, nil
	}

	state.Stage = next
	state.Retry.Attempts = 0

	if gate := gateFor(stage); gate != nil {
		state.Status = StatusAwaitingGate
		state.Gate = gate
		return state, nil
	}

	state.Status = StatusRunning
	return state, nil
}

func complete(state TaskState, a Complete) (TaskState, error) {
	if state.Status != StatusRunning {
		return state, fmt.Errorf("%w: no stage is running", ErrIllegalTransition)
	}

	stage := stageIn(DefaultFlow(), state.Stage)

	// The exit check, and the one that catches the most: a stage that promised two
	// artifacts and delivered one does not close. Both fields count — an audit
	// report has no consumer downstream, so nothing would ever miss it
	// (INV-core-11).
	owed := append(append([]Artifact{}, stage.Produces...), stage.ProducesForHuman...)
	if missing := missingFromList(owed, a.Delivered); len(missing) > 0 {
		state.Status = StatusBlocked
		state.Blocked = fmt.Sprintf("stage %q declared %v but did not deliver %v", state.Stage, owed, missing)
		return state, nil
	}

	// Only flow products enter the context. Letting an audit report in would make
	// it satisfy some stage's requires, which is what ADR-0021 separates the two
	// fields to prevent.
	for _, produced := range stage.Produces {
		state.Context.Artifacts[produced] = true
	}
	for artifact, evidence := range a.Evidence {
		state.Evidence[artifact] = evidence
	}

	state.Retry.Attempts = 0
	return state, nil
}

func fail(state TaskState, a Fail) (TaskState, error) {
	if state.Status != StatusRunning {
		return state, fmt.Errorf("%w: no stage is running", ErrIllegalTransition)
	}

	state.Retry.Attempts++
	if state.Retry.Attempts <= state.Retry.Max {
		return state, nil
	}

	state.Status = StatusBlocked
	state.Blocked = fmt.Sprintf("stage %q failed %d times: %s", state.Stage, state.Retry.Attempts, a.Reason)
	return state, nil
}

func answerGate(state TaskState, action Action) (TaskState, error) {
	if state.Status != StatusAwaitingGate || state.Gate == nil {
		return state, fmt.Errorf("%w: no gate is open", ErrIllegalTransition)
	}

	gate := state.Gate
	state.Gate = nil
	state.Status = StatusRunning

	switch a := action.(type) {
	case GateApprove:
		if gate.Kind == GateReviewArtifact && gate.Payload != "" {
			state.Evidence[gate.Artifact] = gate.Payload
		}
	case GateAdjust:
		// The edited version is what carries on. Recording it is what keeps the
		// handoff describing what the next stage actually consumed.
		state.Evidence[gate.Artifact] = a.Payload
	case GateReject:
		// Nothing enters the context: unlike the review rollback, this happens
		// before the artifact was ever accepted, so there is no green to
		// invalidate (ADR-0022).
		state.Stage = gate.Stage
		state.Blocked = ""
	}

	return state, nil
}

func reviewFinding(state TaskState, a ReviewFinding) (TaskState, error) {
	if !reviewStages[state.Stage] {
		return state, fmt.Errorf("%w: %q is not a review stage", ErrIllegalTransition, state.Stage)
	}

	if !a.Aligned {
		// Out of scope: someone else's task. The code did not change, so the green
		// still holds and the flow carries on untouched.
		return state, nil
	}

	limits := a.Limits
	if limits == (LoopLimits{}) {
		limits = DefaultLoopLimits()
	}

	state.Loop.Rounds++
	if visitedIn(state.Loop.Visited, state.Stage) {
		state.Loop.Oscillation++
	}
	state.Loop.Visited = append(state.Loop.Visited, state.Stage)

	// Going back invalidates the green: ci_green attested to code that no longer
	// exists, and qa, code-review and commit all consume it (ADR-0020).
	delete(state.Context.Artifacts, "ci_green")
	state.Stage = "build"
	state.Status = StatusRunning

	// A spent ceiling opens a gate rather than blocking. Not converging is a
	// decision to make with the history in view, not an anomaly of the node
	// (ADR-0023).
	if reason := ceilingHit(state.Loop, limits); reason != "" {
		state.Status = StatusAwaitingGate
		state.Gate = &PendingGate{Kind: GateLoopCeiling, Stage: state.Stage, Reason: reason}
	}

	return state, nil
}

func unblock(state TaskState) (TaskState, error) {
	if state.Status != StatusBlocked {
		return state, fmt.Errorf("%w: the task is not blocked", ErrIllegalTransition)
	}

	// The retry budget resets: the block was the escalation, and resuming with the
	// old count spent would burn it again on the first attempt.
	state.Retry.Attempts = 0
	state.Blocked = ""
	state.Status = StatusRunning
	return state, nil
}

// ceilingHit names the ceiling that was reached, or returns empty.
func ceilingHit(loop LoopCounters, limits LoopLimits) string {
	switch {
	case loop.Rounds > limits.MaxRounds:
		return fmt.Sprintf("the loop ran %d rounds, past the %d it is allowed", loop.Rounds, limits.MaxRounds)
	case loop.NoProgress >= limits.NoProgress:
		return fmt.Sprintf("%d rounds produced no functional change", loop.NoProgress)
	case loop.Oscillation >= limits.Oscillation:
		return fmt.Sprintf("the loop returned to the same stages %d times", loop.Oscillation)
	default:
		return ""
	}
}

// gateFor returns the gate a stage opens, or nil. The profile decides whether it
// actually waits for a human; this only says one exists.
func gateFor(stage Stage) *PendingGate {
	switch stage.ID {
	case "discovery":
		return &PendingGate{Kind: GateConfirm, Stage: stage.ID, Reason: "confirm the repositories"}
	case "scenarios":
		return &PendingGate{Kind: GateConfirm, Stage: stage.ID, Reason: "approve the plan"}
	case "spec":
		return &PendingGate{Kind: GateReviewArtifact, Stage: stage.ID, Artifact: "contract", Reason: "review the contract"}
	case "commit":
		return &PendingGate{Kind: GateConfirm, Stage: stage.ID, Reason: "confirm the write"}
	default:
		return nil
	}
}

func stageIn(flow []Stage, id StageID) Stage {
	for _, s := range flow {
		if s.ID == id {
			return s
		}
	}
	return Stage{ID: id}
}

// missingFromList reports which of the owed artifacts are absent from delivered.
func missingFromList(owed, delivered []Artifact) []Artifact {
	var missing []Artifact
	for _, want := range owed {
		if !containsArtifact(delivered, want) {
			missing = append(missing, want)
		}
	}
	return missing
}

func visitedIn(visited []StageID, id StageID) bool {
	for _, v := range visited {
		if v == id {
			return true
		}
	}
	return false
}
