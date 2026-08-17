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
	Kind    TaskKind `json:"kind"`
	Profile Profile  `json:"profile,omitempty"`

	// Flow identifies the flow this task was born under (ADR-0046).
	//
	// It is recorded, unlike Advance.Flow, and the difference is the same one
	// ADR-0026 draws for gates: which flow a task ran under is history, while the
	// flow's content is configuration. Storing the identity keeps the flow
	// editable and still lets a replay notice it is reading a log against a
	// contract that is not the one it was written under.
	//
	// Empty means a log written before this field existed, which replays as before.
	Flow FlowFingerprint `json:"flow,omitempty"`
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
	Delivered []Artifact            `json:"delivered"`
	Evidence  map[Artifact]Evidence `json:"evidence,omitempty"`
	Flow      []Stage               `json:"-"`

	// GateDecision is what was decided about the review gate this stage's closing
	// opens, or empty when the stage opens none — and also when the event
	// predates the field, which replays as the shipped policy.
	//
	// It is here for the same reason Advance carries one: a review gate opens on
	// the way *out* of the stage that produced its artifact (ADR-0064), so this
	// is the action that reaches it, and the decision is history while the policy
	// behind it is not (ADR-0026).
	GateDecision GateWaited `json:"gate_decision,omitempty"`

	// Commit is what the stage delivered, as a git object. It becomes the next
	// stage's base, which is what makes the handoff the artifact itself rather
	// than a description of it (INV-core-6, RFC-0002).
	//
	// Optional, and the omission is deliberate: a mechanical stage may produce no
	// commit at all, and a log written before the field existed still replays. An
	// empty commit leaves the base where it was rather than clearing it — losing
	// the base would send the next stage back to the repository's own HEAD and
	// silently discard every stage before it.
	Commit string `json:"commit,omitempty"`
}

// Fail reports that the node broke. Retry until the budget is spent, then block
// and notify (ADR-0011).
type Fail struct {
	Reason string `json:"reason"`
}

// GateApprove accepts what the gate was holding, as it is.
type GateApprove struct{}

// GateAdjust accepts a human-edited version. The replacement is what carries on,
// and the edit is recorded (ADR-0022).
type GateAdjust struct {
	Payload string `json:"payload"`
}

// GateReject refuses the artifact. It does not enter the context, and the stage
// that produced it runs again with the rejection in hand.
type GateReject struct {
	Reason string `json:"reason"`
}

// ReviewFinding is what a review stage found. Aligned sends the work back to
// build; out of scope becomes a separate task and the flow carries on. The
// distinction is a judgement call, which is why it arrives as a decision rather
// than being computed here.
type ReviewFinding struct {
	Aligned bool       `json:"aligned"`
	Summary string     `json:"summary,omitempty"`
	Limits  LoopLimits `json:"limits,omitempty"`

	// Flow is carried for the same reason Advance and Complete carry it: the
	// stage's own declaration says where a finding sends the work back and what
	// stops being true when it does, and a task may run a flow other than the
	// shipped one (ADR-0017, ADR-0049).
	Flow []Stage `json:"-"`

	// GateDecision is what the profile decided about the loop-ceiling gate, for
	// the same reason Advance carries one: the decision is history and the policy
	// behind it is not (ADR-0026). It is consulted only when a ceiling is actually
	// reached, so an ordinary round leaves it empty.
	GateDecision GateWaited `json:"gate_decision,omitempty"`

	// Progress is what the round produced, as an opaque signal to compare against
	// the last one. Two consecutive rounds with the same value made no functional
	// change, which is what `Loop.NoProgress` counts (PRD node-0002).
	//
	// It arrives in the action rather than being computed here, like every other
	// observation about the world: the reducer decides what it means, it does not
	// go looking (ADR-0024, RNF1).
	//
	// Empty means the round did not say — the first round, a node that could not
	// compute it, a log written before the field existed. All three are treated
	// as "no comparison available" rather than as "no progress", because a
	// detector that fires on missing data is one people turn off.
	Progress string `json:"progress,omitempty"`
}

// Block stops the task and notifies, without pretending an attempt was made.
//
// It exists because the lead used to reach a block by recording Fail until the
// retry budget ran out, which left three failures in the log where there had been
// one decision to escalate. The history is the audit trail (INV-core-2), and an
// audit that shows retries that never happened is a worse kind of wrong than a
// second path into the same state.
type Block struct {
	Reason string `json:"reason"`
}

// Unblock is a human clearing a block.
type Unblock struct{}

// Abandon ends a task by human decision, without it having finished (ADR-0046).
//
// It is the answer to a task that will not be completed and has no way out
// otherwise: one whose flow changed under it and no longer replays, or one that
// was simply superseded. Without it, such a task stays open forever — the store
// has no UPDATE and no DELETE (INV-core-2), so nothing else could end it.
//
// It is deliberately not a delete. The log keeps every event, and abandoning adds
// one more fact rather than removing any: the audit should show that a person
// ended this, and why.
//
// It is also not a reclassification of `blocked`. A block is an anomaly a person
// can clear with Unblock; abandoning is the explicit act of saying it is over.
type Abandon struct {
	Reason string `json:"reason"`
}

// SetKnob changes how autonomous a task is, mid-run.
//
// It is an action rather than configuration re-read at each Advance, and the
// difference is the audit. A run where the lead judged three gates has to be
// reviewable afterwards, and a setting that changed with no record turns "why
// was nobody asked here?" into a question the log cannot answer. The change is
// itself a decision, so it is history (ADR-0048).
//
// Changing it does not disturb a gate that is already open: that gate was
// answered — or is waiting to be — under whatever held when it opened, and taking
// a decision away from someone already looking at it would be worse than asking
// them once more (RFC-0006).
type SetKnob struct {
	Knob Knob `json:"knob"`

	// Why the person moved it. Not required, and worth having: the log already
	// says what changed, and this is the only place it can say what for.
	Reason string `json:"reason,omitempty"`
}

func (TaskCreated) isAction()   {}
func (SetKnob) isAction()       {}
func (Advance) isAction()       {}
func (Complete) isAction()      {}
func (Fail) isAction()          {}
func (GateApprove) isAction()   {}
func (GateAdjust) isAction()    {}
func (GateReject) isAction()    {}
func (ReviewFinding) isAction() {}
func (Block) isAction()         {}
func (Unblock) isAction()       {}
func (Abandon) isAction()       {}

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
	default:
		return reduceRunControl(state, action)
	}
}

// reduceRunControl handles the actions that say something about the run rather
// than about a stage: it stopped, it resumed, it ended, it changed how
// autonomous it is.
//
// Split from Reduce because the dispatch had grown past the complexity gate, and
// this is the seam that means something: everything above moves work through the
// flow, everything here is a person changing the terms the flow runs under.
func reduceRunControl(state TaskState, action Action) (TaskState, error) {
	switch a := action.(type) {
	case Block:
		return block(state, a)
	case Unblock:
		return unblock(state)
	case Abandon:
		return abandon(state, a)
	case SetKnob:
		return setKnob(state, a)
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
	// Carried into the state so a replay can compare it against the flow it was
	// handed. The reducer records it and never checks it: comparing is the store's
	// job, because the reducer sees one action at a time and the mismatch is a
	// property of the whole replay (ADR-0046).
	state.Flow = a.Flow
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

	// Only the gates that ask about work not yet done open here. A `confirm`
	// before a stage runs is a question about that stage; a `review-artifact`
	// asks about something the stage has to produce first, so it opens when the
	// stage closes (ADR-0064).
	//
	// The decision arrived in the action, and a gate that resolves on its own
	// still happened — it is just that nobody was asked (ADR-0026).
	if gate := gateFor(stage); asksAboutWorkAhead(gate) &&
		gateWaits(a.GateDecision, state.Profile, gate.Kind) {
		state.Status = StatusAwaitingGate
		state.Gate = withPayload(gate, state.Evidence)
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

	// Passing is not enough — it has to be the check the contract asked for. An
	// artifact declared with a command that comes back proven by existence alone
	// has not been verified, it has been delivered, and closing on that is the
	// laundering the scopes exist to prevent (INV-core-4).
	if weak := underProven(stage, owed, a.Evidence); len(weak) > 0 {
		state.Status = StatusBlocked
		state.Blocked = fmt.Sprintf("stage %q proved %v with a weaker check than its contract declared", state.Stage, weak)
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

	// The base advances only here, on the path where the stage actually closed.
	// A delivery that failed its verification is recorded above and returns
	// early, so work that did not pass never becomes the next stage's starting
	// point — which is the whole reason the base is a separate field rather than
	// "whatever the last commit was".
	if a.Commit != "" {
		state.Base = a.Commit
	}

	// The review gate opens here rather than on entry to the next stage, because
	// this is the first moment its artifact exists (ADR-0064). The evidence was
	// absorbed above, so the payload is a read of something real instead of the
	// blank line `luna gate show` used to print.
	//
	// After the base advances, so a task answering the gate resumes from what the
	// stage delivered — the gate is a pause in the handoff, not a step before it.
	if gate := gateFor(stage); asksAboutWorkDone(gate) &&
		gateWaits(a.GateDecision, state.Profile, gate.Kind) {
		state.Status = StatusAwaitingGate
		state.Gate = withPayload(gate, state.Evidence)
	}
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

	// Where the task resumes depends on which side of the stage the gate was on.
	// A gate that opened on the way *in* leaves a stage to run; one that opened
	// on the way *out* leaves a stage already closed, and saying `running` would
	// ask the node to run it a second time (ADR-0064).
	state.Status = StatusRunning
	if asksAboutWorkDone(gate) {
		state.Status = StatusStageDone
	}

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
		// The artifact leaves the context. A review gate now opens on the way out
		// of the stage that produced it (ADR-0064), so by the time a person can
		// reject it the exit check has already let it in — where the old timing
		// asked before it existed and there was nothing to take back.
		//
		// Its evidence goes too. What is being said is "this is not acceptable",
		// and leaving a passing record behind would let the next attempt's exit
		// check close on the rejected version's proof.
		delete(state.Context.Artifacts, gate.Artifact)
		delete(state.Evidence, gate.Artifact)

		// Back to the stage that produced it — which is this gate's own stage now,
		// and is the sentence ADR-0022 wrote. It *runs again*, so the status is
		// `running` even though the gate opened on the way out: `stage_done` is
		// what the other two answers resume to, and it would carry a rejected
		// stage forward as though it had closed.
		state.Stage = gate.Stage
		state.Status = StatusRunning
		state.Blocked = ""
	}

	return state, nil
}

func reviewFinding(state TaskState, a ReviewFinding) (TaskState, error) {
	flow := a.Flow
	if len(flow) == 0 {
		flow = DefaultFlow()
	}

	// Only a review stage may send work back. This is INV-core-7 inside the
	// engine: an implementer returning its own work would be reviewing itself, and
	// the refusal has to come from the stage's own declaration rather than a list
	// of names the engine keeps, or a renamed stage loses the protection silently.
	stage := stageIn(flow, state.Stage)
	if stage.Review == nil {
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

	// The third ceiling, finally fed (PRD node-0002).
	state.Loop = countProgress(state.Loop, a.Progress)

	// Going back invalidates what the work had proven: the green attested to code
	// that no longer exists (ADR-0020). Which artifacts those are is the stage's
	// to declare — a flow whose green is called something else keeps the behaviour.
	//
	// Only the flow's own products leave the context. An artifact a stage produces
	// for a human to read was never an input, so removing it would be removing
	// something that is not there (INV-core-11).
	for _, artifact := range stage.Review.Invalidates {
		delete(state.Context.Artifacts, artifact)
	}

	// The evidence for it does not disappear, it goes stale. Dropping the record
	// would leave an audit that cannot tell "never checked" from "checked, then
	// invalidated" — and the second is the interesting one (ADR-0032).
	stale(state.Evidence, stage.Review.Invalidates, state.Seq)
	state.Stage = stage.Review.SendsBackTo
	state.Status = StatusRunning

	// A spent ceiling opens a gate rather than blocking. Not converging is a
	// decision to make with the history in view, not an anomaly of the node
	// (ADR-0023).
	if reason := ceilingHit(state.Loop, limits); reason != "" {
		if gateWaits(a.GateDecision, state.Profile, GateLoopCeiling) {
			state.Status = StatusAwaitingGate
			state.Gate = &PendingGate{Kind: GateLoopCeiling, Stage: state.Stage, Reason: reason}
			return state, nil
		}

		// Nobody is waiting — and a ceiling that nobody answers must not simply
		// resolve. That is the infinite loop INV-core-8 names in as many words:
		// *"no infinite retry, which is the loop that does not converge and burns
		// tokens"*.
		//
		// So the run blocks instead, which is the ending that notifies. ADR-0023
		// preferred a gate to a block because a loop that stopped converging
		// leaves a decision worth taking with the history in view — that
		// reasoning holds wherever there is somebody to take it, and where there
		// is not, the choice is between blocking and looping forever (ADR-0059).
		//
		// Which of the two this is, is the lead's call now rather than a
		// profile's: it reads the history and decides whether a spent ceiling is
		// a block or a question, and the answer arrives here in the action
		// (ADR-0063). With no lead, the decision is absent and this is what
		// absent means.
		//
		// This was unreachable until a review could send work back: the ceilings
		// counted rounds that never happened, so the hole was real and invisible.
		state.Status = StatusBlocked
		state.Blocked = reason
		state.Gate = nil
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

// abandon ends a task a person decided not to finish (ADR-0046).
//
// It accepts any state that is not already terminal, which is wider than the
// other human actions on purpose: the cases it exists for are the ones nobody
// planned. A task can be running, waiting at a gate, or blocked, and the reason a
// person is calling it off is not the engine's business.
//
// Ending an already-ended task is refused rather than ignored: it would put a
// second ending in the log, and a history that shows a task finishing twice is
// worse than an error the caller has to read.
// setKnob moves the autonomy setting, leaving any open gate exactly as it is.
//
// The pending gate is deliberately untouched. A gate that opened needing a person
// keeps needing one: it was already put in front of somebody, and having a
// setting move it out from under them is worse than the cost of being asked once
// more. Only gates that open after this see the new value (RFC-0006).
func setKnob(state TaskState, a SetKnob) (TaskState, error) {
	if state.IsTerminal() {
		return state, fmt.Errorf("%w: the task already ended as %q", ErrIllegalTransition, state.Status)
	}
	if a.Knob < KnobAsk || a.Knob > KnobAll {
		return state, fmt.Errorf("%w: autonomy has to be 0-10, got %d", ErrIllegalTransition, a.Knob)
	}

	state.Knob = a.Knob
	return state, nil
}

func abandon(state TaskState, a Abandon) (TaskState, error) {
	if state.IsTerminal() {
		return state, fmt.Errorf("%w: the task already ended as %q", ErrIllegalTransition, state.Status)
	}
	if a.Reason == "" {
		return state, fmt.Errorf("%w: abandoning a task needs a reason", ErrIllegalTransition)
	}

	// Kept in Blocked rather than a field of its own: it is the same question —
	// why did this task stop — and a second field would mean two places to look
	// and one of them usually empty.
	state.Status = StatusAbandoned
	state.Blocked = a.Reason
	// The gate goes with it. A gate left pending on an ended task would keep it in
	// `luna gates`, waiting for a decision that no longer means anything
	// (INV-core-12).
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

// countProgress folds this round's signal into the no-progress counter.
//
// A round whose signal equals the last one produced the same thing twice: the
// reviewer is sending back work that is not changing, which is the loop that
// burns tokens without converging and which nothing detected until ADR-0061.
//
// A round that says nothing leaves the streak alone — it neither extends nor
// clears it. Silence is "no comparison available", and both alternatives are
// wrong in a way that matters: counting it would fire the ceiling on a node that
// could not observe, and clearing it would throw away a real streak because one
// round in the middle could not. The last signal is kept for the same reason —
// the next round should compare against the last thing actually seen.
func countProgress(loop LoopCounters, signal string) LoopCounters {
	switch signal {
	case "":
		return loop
	case loop.LastProgress:
		loop.NoProgress++
	default:
		loop.NoProgress = 0
	}
	loop.LastProgress = signal
	return loop
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

// GateClosing is the gate the running stage opens when it closes, or nil.
//
// The mirror of GateAhead, for the kind that asks about work already done: a
// `review-artifact` gate opens on the way out of the stage that produced its
// artifact (ADR-0064), so the caller that records the closing is the one that has
// to decide it.
//
// It answers for the stage that is running rather than the next one, and it does
// not check whether the stage will actually close — that is the exit check's
// answer, taken inside the reducer. A caller asks this to know whether a decision
// is owed at all.
func GateClosing(state TaskState, flow []Stage) *PendingGate {
	if state.Status != StatusRunning {
		return nil
	}

	gate := gateFor(stageIn(flow, state.Stage))
	if !asksAboutWorkDone(gate) {
		return nil
	}
	return gate
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
	if stage.Gate == nil {
		return nil
	}
	return &PendingGate{
		Kind:     stage.Gate.Kind,
		Stage:    stage.ID,
		Reason:   stage.Gate.Reason,
		Artifact: stage.Gate.Artifact,
	}
}

// asksAboutWorkAhead reports whether this gate belongs on the way *into* a stage.
//
// A `confirm` asks about work not yet done, so it opens before the stage runs. A
// `review-artifact` asks about something the stage has to produce first, so it
// opens when the stage closes — opening it on entry was asking a person to review
// a file that did not exist yet (ADR-0064).
//
// A nil gate is not a gate, which is the ordinary case for most stages.
func asksAboutWorkAhead(gate *PendingGate) bool {
	return gate != nil && gate.Kind != GateReviewArtifact
}

// asksAboutWorkDone is its mirror: the gate that opens on the way out.
func asksAboutWorkDone(gate *PendingGate) bool {
	return gate != nil && gate.Kind == GateReviewArtifact
}

// withPayload fills a review gate with the artifact the human is being asked to
// read, when there is one to fill it with.
//
// INV-core-12 requires the gate's artifact to be retrievable by command, and
// until this existed `luna gate show` printed the artifact's name and a blank
// line — the payload was declared, documented, and written by nothing.
//
// It fills from the evidence because that is where the delivered content lives.
// A gate whose artifact has not been produced yet keeps an empty payload rather
// than inventing one: the gate for `spec` opens on entry, before `spec` has
// written the contract, which is a timing bug of its own and is recorded in
// RFC-0001 rather than papered over here.
func withPayload(gate *PendingGate, evidence map[Artifact]Evidence) *PendingGate {
	if gate.Kind != GateReviewArtifact || gate.Payload != "" {
		return gate
	}
	// A copy: the caller's gate is shared with the state it came from, and the
	// reducer does not write through its inputs.
	filled := *gate
	filled.Payload = evidence[gate.Artifact].Detail
	return &filled
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

// underProven reports which owed artifacts passed a weaker check than the
// contract asked for.
//
// This is the other half of the exit check, and without it the scopes are
// decoration: evidence that only proves the file is on disk would close a stage
// whose contract declared a command, and the log would carry `existence` under a
// stage that promised the suite. Scope.Satisfies is one-directional precisely so
// that gap cannot be closed by reading the record generously (ADR-0032).
//
// It runs after notPassing, so everything here already passed — the question is
// no longer whether the check succeeded but whether it was the right check.
func underProven(stage Stage, owed []Artifact, evidence map[Artifact]Evidence) []Artifact {
	var weak []Artifact
	for _, artifact := range owed {
		wanted := VerifierFor(stage, artifact).Proves()
		if !evidence[artifact].Scope.Satisfies(wanted) {
			weak = append(weak, artifact)
		}
	}
	return weak
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
