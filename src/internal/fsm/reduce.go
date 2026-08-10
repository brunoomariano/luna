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

// TaskCreated opens a task's log. It is always the first event, and it is what
// makes the log self-describing: the kind and the profile come out of the history
// rather than having to be supplied alongside it.
type TaskCreated struct {
	Kind    TaskKind
	Profile Profile
}

// Advance moves to the next stage of the flow, applying the contract's entry
// check on the way in.
//
// The two fields are recorded on opposite rules, and the difference is the whole
// point of ADR-0026:
//
//   - Flow is configuration, so it is not recorded. Storing it would freeze a task
//     to the flow it started under, and flows are meant to be editable (ADR-0017).
//   - GateDecision is history, so it is recorded. The profile that produced it is
//     configuration too — and if replay re-derived the decision from it, editing a
//     profile would rewrite how past tasks replay.
//
// Both come from outside the reducer, which reads a profile no more than it reads
// a clock (ADR-0024).
type Advance struct {
	Flow []Stage `json:"-"`

	// GateDecision is what the profile decided about the gate this advance walks
	// into, or empty when the stage opens no gate — and also when the event
	// predates the field, which is why replay still needs a fallback.
	GateDecision GateWaited `json:"gate_decision,omitempty"`
}

// Complete closes the running stage.
//
// Delivered and Evidence come from outside: the lead runs the real tool and hands
// the verdict in (ADR-0024). The reducer decides what it means, it does not go
// looking.
//
// Flow is carried for the same reason Advance carries it: a task may run a flow
// other than the shipped one (ADR-0017), and the exit check has to compare the
// delivery against the contract that task is actually running. It defaults to the
// shipped flow when empty, so a log written before this field existed still
// replays.
type Complete struct {
	Delivered []Artifact
	Evidence  map[Artifact]Evidence
	Flow      []Stage `json:"-"`
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

	// GateDecision is what the profile decided about the loop-ceiling gate, for
	// the same reason Advance carries one: the decision is history and the policy
	// behind it is not (ADR-0026). It is consulted only when a ceiling is actually
	// reached, so an ordinary round leaves it empty.
	GateDecision GateWaited `json:"gate_decision,omitempty"`
}

// Block stops the task and notifies, without pretending an attempt was made.
//
// It exists because the lead used to reach a block by recording Fail until the
// retry budget ran out, which left three failures in the log where there had been
// one decision to escalate. The history is the audit trail (INV-core-2), and an
// audit that shows retries that never happened is a worse kind of wrong than a
// second path into the same state.
type Block struct{ Reason string }

// Unblock is a human clearing a block.
type Unblock struct{}

func (TaskCreated) isAction()   {}
func (Advance) isAction()       {}
func (Complete) isAction()      {}
func (Fail) isAction()          {}
func (GateApprove) isAction()   {}
func (GateAdjust) isAction()    {}
func (GateReject) isAction()    {}
func (ReviewFinding) isAction() {}
func (Block) isAction()         {}
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
	// Every applied action advances the log position. It is incremented before
	// the transition so anything recorded during it carries the sequence of the
	// event that produced it, which is what the staleness rule compares against
	// (ADR-0032). A refused action returns the state untouched, sequence
	// included — it never entered the log.
	state.Seq++

	switch a := action.(type) {
	case TaskCreated:
		return created(state, a)
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
	case Block:
		return block(state, a)
	case Unblock:
		return unblock(state)
	default:
		return state, fmt.Errorf("%w: unknown action %T", ErrIllegalTransition, action)
	}
}

// canAdvance reports whether the task is in a position to enter a stage.
//
// Every status is named rather than relying on a default: when a seventh one is
// added, this is the place that has to decide about it, instead of silently
// letting it advance.
func canAdvance(state TaskState) error {
	switch state.Status {
	case StatusReady, StatusStageDone:
		// The two that may advance: a task that has not started, and one whose
		// stage just closed.
		return nil
	case StatusRunning:
		return fmt.Errorf("%w: %q is still running", ErrIllegalTransition, state.Stage)
	case StatusAwaitingGate:
		return fmt.Errorf("%w: a gate is pending on %q", ErrIllegalTransition, state.Stage)
	case StatusBlocked:
		return fmt.Errorf("%w: the task is blocked", ErrIllegalTransition)
	case StatusDone:
		return fmt.Errorf("%w: the task is finished", ErrIllegalTransition)
	default:
		return fmt.Errorf("%w: unknown status %q", ErrIllegalTransition, state.Status)
	}
}

// created stamps a task's kind and profile from its opening event.
func created(state TaskState, a TaskCreated) (TaskState, error) {
	if state.Status != StatusReady || state.Stage != "" {
		return state, fmt.Errorf("%w: a task is created once, before anything else", ErrIllegalTransition)
	}

	state.Context.Kind = a.Kind
	state.Profile = a.Profile
	if state.Profile == "" {
		state.Profile = ProfileInteractive
	}
	return state, nil
}

func advance(state TaskState, a Advance) (TaskState, error) {
	if err := canAdvance(state); err != nil {
		return state, err
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

	// The profile decided whether this gate stops the task, and the decision
	// arrived in the action. A gate that resolves on its own still happened — it
	// is just that nobody was asked (ADR-0013, ADR-0026).
	if gate := gateFor(stage); gate != nil && gateWaits(a.GateDecision, state.Profile, gate.Kind) {
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

	flow := a.Flow
	if len(flow) == 0 {
		flow = DefaultFlow()
	}
	stage := stageIn(flow, state.Stage)

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

	// The verdict decides, not the delivery. A stage that produced an artifact
	// whose check failed does not close: the node ran the real tool and it said
	// no, and closing anyway is the self-reported completion ADR-0028 rejects.
	if failed := notPassing(owed, a.Evidence); len(failed) > 0 {
		state.Status = StatusBlocked
		state.Blocked = fmt.Sprintf("stage %q delivered %v but its verification did not pass", state.Stage, failed)
		// The evidence is recorded even so: the audit needs to show what failed,
		// not just that something did.
		absorb(state.Evidence, a.Evidence)
		return state, nil
	}

	// Only flow products enter the context. Letting an audit report in would make
	// it satisfy some stage's requires, which is what ADR-0021 separates the two
	// fields to prevent.
	for _, produced := range stage.Produces {
		state.Context.Artifacts[produced] = true
	}
	absorb(state.Evidence, a.Evidence)

	// The status says the stage finished, rather than leaving the caller to infer
	// it from what landed in the context. A closed stage and a stage about to
	// start were both `running`, which is a distinction the state should make
	// itself.
	state.Status = StatusStageDone
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
			state.Evidence[gate.Artifact] = Approved(gate.Payload, state.Seq)
		}
	case GateAdjust:
		// The edited version is what carries on. Recording it is what keeps the
		// handoff describing what the next stage actually consumed.
		//
		// It is human-scoped evidence rather than a command's verdict: someone
		// looked and accepted, which is a different fact from a check that ran.
		state.Evidence[gate.Artifact] = Approved(a.Payload, state.Seq)
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

	// The evidence for it does not disappear, it goes stale. Dropping the record
	// would leave an audit that cannot tell "never checked" from "checked, then
	// invalidated" — and the second is the interesting one (ADR-0032).
	stale(state.Evidence, []Artifact{"ci_green", "tests_green"}, state.Seq)
	state.Stage = "build"
	state.Status = StatusRunning

	// A spent ceiling opens a gate rather than blocking. Not converging is a
	// decision to make with the history in view, not an anomaly of the node
	// (ADR-0023).
	if reason := ceilingHit(state.Loop, limits); reason != "" && gateWaits(a.GateDecision, state.Profile, GateLoopCeiling) {
		state.Status = StatusAwaitingGate
		state.Gate = &PendingGate{Kind: GateLoopCeiling, Stage: state.Stage, Reason: reason}
	}

	return state, nil
}

// block stops the task with the reason it will notify with.
func block(state TaskState, a Block) (TaskState, error) {
	if state.Status == StatusDone || state.Status == StatusBlocked {
		return state, fmt.Errorf("%w: the task is already %s", ErrIllegalTransition, state.Status)
	}
	if a.Reason == "" {
		// A block that does not say why is the silent failure INV-core-8 forbids,
		// so the reason is required rather than defaulted.
		return state, fmt.Errorf("%w: a block must carry a reason", ErrIllegalTransition)
	}

	state.Status = StatusBlocked
	state.Blocked = a.Reason
	state.Gate = nil
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

// GateAhead reports the gate an Advance would walk into, or nil.
//
// It exists so the caller can ask the profile about that gate *before* recording
// the action, which is what lets the decision be written into the log instead of
// recomputed at replay (ADR-0026). It walks the same path advance does and
// changes nothing, so asking is free of consequences.
func GateAhead(state TaskState, flow []Stage) *PendingGate {
	if err := canAdvance(state); err != nil {
		return nil
	}

	next, ok, err := NextStage(flow, state.Stage, state.Context)
	if err != nil || !ok {
		return nil
	}

	stage := stageIn(flow, next)
	if len(MissingFor(stage, state.Context)) > 0 {
		// The entry check blocks before any gate is reached, so there is no
		// decision to make here.
		return nil
	}
	return gateFor(stage)
}

// gateWaits reports whether a gate stops the task, preferring the decision the
// log recorded over anything this build would compute.
//
// The fallback is not a second policy: it is what an event written before the
// decision existed replays as. Those logs recorded a name and nothing else, so
// the shipped policy is the only reading of them available — and it is the same
// one that produced them (ADR-0026).
func gateWaits(decision GateWaited, profile Profile, gate GateKind) bool {
	if waited, recorded := decision.Waits(); recorded {
		return waited
	}
	return ShippedPolicy(profile, gate)
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
		return &PendingGate{Kind: GateConfirmWrite, Stage: stage.ID, Reason: "confirm the write"}
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

// notPassing reports which owed artifacts arrived without passing evidence.
//
// Missing evidence counts as not passing: a delivery the node said nothing about
// is not proof, and treating silence as success is exactly how a status becomes a
// verdict (ADR-0028).
func notPassing(owed []Artifact, evidence map[Artifact]Evidence) []Artifact {
	var failed []Artifact
	for _, artifact := range owed {
		if !evidence[artifact].Passing() {
			failed = append(failed, artifact)
		}
	}
	return failed
}

// absorb copies observed evidence into the state, newest wins.
func absorb(into, from map[Artifact]Evidence) {
	for artifact, e := range from {
		into[artifact] = e
	}
}

// stale marks evidence that stopped being true because the work moved under it.
//
// One comparison of two sequence numbers is the whole mechanism that stops "I
// tested it" from surviving a later change (ADR-0032). It is a pure function of
// the log, which is why it belongs here and not in the node layer.
//
// The comparison is `>` rather than `>=`: evidence recorded at the very event
// that invalidates it is still invalidated. Only a check proven *after* the
// change survives it, which is the whole point.
func stale(evidence map[Artifact]Evidence, touched []Artifact, at int) {
	for _, artifact := range touched {
		e, ok := evidence[artifact]
		if !ok || e.Verdict != VerdictPassed || e.RecordedAt > at {
			continue
		}
		// Existence is not invalidated by an edit: the thing still exists, and
		// claiming otherwise would block a stage for rewriting its own prose.
		if e.Scope == ScopeExistence {
			continue
		}
		e.Verdict = VerdictStale
		evidence[artifact] = e
	}
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
