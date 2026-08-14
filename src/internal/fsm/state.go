package fsm

// Status is where a task stands. The five values are exhaustive: a task is always
// in exactly one of them.
type Status string

const (
	// StatusReady is a task that has not entered its first stage yet.
	StatusReady Status = "ready"

	// StatusRunning is a node working inside a stage.
	StatusRunning Status = "running"

	// StatusStageDone is a stage that delivered and closed, with the next one not
	// yet entered.
	//
	// It exists because "the node has work to do" and "the node is finished" are
	// different situations, and collapsing them into `running` left the lead
	// unable to tell them apart — it would run the same node forever. Inferring
	// the difference from a side effect was the first fix and the wrong one: if a
	// stage finished, the status should say so.
	StatusStageDone Status = "stage_done"

	// StatusAwaitingGate is a planned pause. The profile foresaw it, and the slot
	// is released while it waits (INV-core-10) — this is not a failure, and it
	// must not be reported as one.
	StatusAwaitingGate Status = "awaiting_gate"

	// StatusBlocked is an anomaly: something failed or stalled and a human has to
	// look. Always notifies (INV-core-8).
	StatusBlocked Status = "blocked"

	// StatusDone is a task that reached the end of its flow.
	StatusDone Status = "done"

	// StatusAbandoned is a task a person ended before it finished (ADR-0046).
	//
	// Terminal like done, and deliberately not the same word: an audit that could
	// not tell a task that delivered from one that was called off would be missing
	// the more interesting of the two.
	StatusAbandoned Status = "abandoned"
)

// Profile names which gates actually wait for a human. It is chosen per task
// rather than per task type or per repository, because the type does not predict
// the risk — a critical bug can deserve more gating than a trivial feature
// (ADR-0013).
//
// The name is all the engine holds. What the name *means* is configuration, and
// it is resolved outside the reducer — the decision arrives in the action, the
// same way a verification verdict does (ADR-0024, ADR-0026). That is what lets a
// project define its own profiles without the engine growing a list of them.
type Profile string

const (
	// ProfileInteractive stops at every gate.
	ProfileInteractive Profile = "interactive"

	// ProfileTurbo stops only before writing: the commit gate waits, the rest
	// resolve on their own.
	ProfileTurbo Profile = "turbo"

	// ProfileNightly stops at nothing. It is what makes an unattended run
	// unattended — and what makes the watchdog (ADR-0019) load-bearing rather
	// than a nicety.
	ProfileNightly Profile = "nightly"
)

// GateWaited is whether a gate stopped the task, decided by the profile before
// the action was recorded.
//
// It is a tri-state rather than a bool because the log has to distinguish "the
// profile said carry on" from "this event predates the field". A bool would make
// those identical, and the older one has to fall back to recomputing while the
// newer one must not (ADR-0026).
type GateWaited string

const (
	// GateDecisionAbsent is an event written before the decision was recorded.
	// Replaying one falls back to the shipped policy, which is the best guess
	// available and the only behaviour that keeps old logs replaying.
	GateDecisionAbsent GateWaited = ""

	// GateDecisionWaited means the gate stopped the task and a human answered it.
	GateDecisionWaited GateWaited = "waited"

	// GateDecisionPassed means the gate was reached and the profile let it
	// through. It still happened — nobody was asked (ADR-0013).
	GateDecisionPassed GateWaited = "passed"

	// GateDecisionChecked means the commands declared for this gate ran over the
	// delivered commit and all exited 0. Nobody was asked, and the reason is a
	// verdict rather than a policy: someone declared that those commands answer
	// this gate (RFC-0006).
	GateDecisionChecked GateWaited = "checked"

	// GateDecisionJudged means the lead judged the gate against criteria declared
	// in advance, because the knob reached the gate's criticality (RFC-0006).
	//
	// It is separate from GateDecisionPassed for the reason the whole tri-state
	// exists: "nobody was asked because the profile said so" and "nobody was asked
	// because a model answered" are different claims about the same transition,
	// and an audit that could not tell them apart would be the weaker for it.
	GateDecisionJudged GateWaited = "judged"
)

// Waits reports what the recorded decision says, and whether it said anything at
// all. A caller that gets false must fall back to a policy.
//
// The three "did not wait" values collapse here on purpose: for the question
// *did the task stop*, a gate answered by a command and one answered by the lead
// are the same event. What separates them is who answered, which the value itself
// records and which the audit reads — not the transition.
func (d GateWaited) Waits() (waited, recorded bool) {
	switch d {
	case GateDecisionWaited:
		return true, true
	case GateDecisionPassed, GateDecisionChecked, GateDecisionJudged:
		return false, true
	default:
		return false, false
	}
}

// AnsweredBy names what settled the gate, for an audit reading the log.
//
// It exists so that "who answered this?" has one implementation rather than a
// switch at each reader, and so that adding a fourth answerer later has one place
// to change.
func (d GateWaited) AnsweredBy() string {
	switch d {
	case GateDecisionWaited:
		return "a person"
	case GateDecisionChecked:
		return "the declared checks"
	case GateDecisionJudged:
		return "the lead"
	case GateDecisionPassed:
		return "nobody: the profile let it through"
	default:
		return "unrecorded"
	}
}

// ShippedPolicy is what the three built-in profiles do. It is the fallback for an
// event recorded before the decision was part of the log, and the seed for the
// defaults a project inherits when its config names no profiles of its own.
//
// It is not consulted for a task whose events carry a decision: those replay from
// what was recorded, so editing a profile cannot rewrite them (ADR-0026).
func ShippedPolicy(p Profile, gate GateKind) bool {
	switch p {
	case ProfileNightly:
		return false
	case ProfileTurbo:
		// Only the write waits. A loop ceiling under turbo resolves by carrying
		// on, which is the point of the profile.
		return gate == GateConfirmWrite
	case ProfileInteractive:
		return true
	default:
		// A profile this build has no policy for is treated as the most cautious
		// one. Guessing the permissive answer would let a name that resolved to
		// nothing turn a supervised run into an unattended one.
		return true
	}
}

// GateKind names why a gate stopped the task, which decides what the human is
// being asked for (ADR-0022).
type GateKind string

const (
	// GateConfirm asks a yes or no. Nothing is attached.
	GateConfirm GateKind = "confirm"

	// GateConfirmWrite is the confirmation before the task writes — the commit.
	// It is a kind of its own rather than a plain confirm because it is the one
	// gate the turbo profile still waits for: everything before it is reversible,
	// and this is not.
	GateConfirmWrite GateKind = "confirm-write"

	// GateReviewArtifact carries what the stage produced. The human may approve
	// it, adjust it, or reject it, and the approved version is what the next
	// stage consumes.
	GateReviewArtifact GateKind = "review-artifact"

	// GateLoopCeiling is a loop that hit one of its ceilings (ADR-0023). Not
	// converging is a decision to make, not a node failure — hence a gate rather
	// than a block.
	GateLoopCeiling GateKind = "loop-ceiling"
)

// PendingGate is what a suspended task is waiting on. It is what `luna gates`
// lists, so a suspension is discoverable without anyone having watched it happen
// (INV-core-12).
type PendingGate struct {
	Kind   GateKind
	Stage  StageID
	Reason string

	// Artifact and Payload are set only for GateReviewArtifact: the thing the
	// human is reviewing, and its current content. An adjustment replaces Payload,
	// and the replacement is what enters the context.
	Artifact Artifact
	Payload  string
}

// LoopCounters tracks the three ceilings separately (ADR-0023). One counter would
// force a single limit to arbitrate three different pathologies: a loop that
// never ends, one that spins without producing, and one that undoes what it just
// did.
type LoopCounters struct {
	// Rounds is every trip through the loop.
	Rounds int

	// NoProgress counts consecutive rounds that produced no functional change.
	NoProgress int

	// Oscillation counts consecutive rounds that returned to a stage already
	// visited in the same loop.
	Oscillation int

	// Visited is the stage history of the current loop, which is what makes
	// oscillation detectable at all.
	Visited []StageID

	// LastProgress is what the previous round produced, kept so the next one has
	// something to compare against (PRD node-0002).
	//
	// It is the signal itself rather than a count, because the audit's question
	// is "what was compared" — a person told two rounds made no progress wants to
	// see what the machine looked at before believing it (RF3).
	LastProgress string
}

// LoopLimits are the ceilings themselves, configurable per loop.
type LoopLimits struct {
	MaxRounds   int `json:"max_rounds"`
	NoProgress  int `json:"no_progress"`
	Oscillation int `json:"oscillation"`
}

// DefaultLoopLimits are the values the design settled on.
func DefaultLoopLimits() LoopLimits {
	return LoopLimits{MaxRounds: 4, NoProgress: 2, Oscillation: 2}
}

// Retry counts attempts at the current stage after a node failure. Kept apart
// from LoopCounters on purpose (ADR-0011): a transient failure must not eat the
// convergence budget, and a loop that is not converging is not a failure.
type Retry struct {
	Attempts int
	Max      int
}

// Statement is what a person said the task is about, in their words.
//
// It exists because a task used to be an id, a kind and a profile, and nothing
// more: agents inferred the goal from the id string, and the stage that has to
// write a contract stopped to ask a person instead — every time.
//
// The three fields are the registry's own, kept apart rather than flattened into
// one blob so a stage can be told what it needs: `scenarios` and `spec` live on
// Acceptance, while `build` mostly needs Description.
type Statement struct {
	// Description is what to build.
	Description string

	// Design is the technical route, when one was decided before the work began.
	Design string

	// Acceptance is how the result will be judged.
	Acceptance string
}

// Stated reports whether anybody said anything about this task.
func (s Statement) Stated() bool {
	return s.Description != "" || s.Design != "" || s.Acceptance != ""
}

// TaskState is everything the engine knows about one task. It is rebuilt by
// replaying the append-only log, so it holds no pointer to anything live
// (INV-core-2).
type TaskState struct {
	ID      string
	Status  Status
	Stage   StageID
	Context TaskContext

	// Profile decides which gates wait. It lives in the state rather than in
	// configuration because a replay has to reproduce the run as it happened: a
	// task run overnight must not replay as though it had been supervised.
	Profile Profile

	// Knob is how far the lead may judge on its own, as it stands now. It moves
	// only through SetKnob, so replaying the log reproduces every value it held
	// and when — which is the whole reason the change is an action rather than
	// configuration re-read at each step (RFC-0006).
	//
	// The zero value is KnobAsk, so a task that never set one judges nothing.
	Knob Knob

	// Flow identifies the flow this task was born under, from its opening event
	// (ADR-0046). It is here for the same reason as Profile: a replay has to know
	// which contract the history was written against, and asking the caller for
	// what the log already holds would let the two disagree.
	//
	// Empty means a log written before the field existed.
	Flow FlowFingerprint

	// Base is the commit the last closed stage delivered, and the one the next
	// stage branches from. It is the handoff: the next agent starts from the
	// artifact rather than from a description of it (INV-core-6, RFC-0002).
	//
	// Empty on a task that has not closed a stage yet, which means the next
	// worktree branches from whatever the repository already is.
	Base string

	// Statement is what a person said the task is about. It is **not recorded**,
	// for the same reason `Advance.Flow` is not: it lives in the registry, a
	// person edits it there, and a copy frozen into the log would go quietly stale
	// while still looking authoritative (ADR-0026, ADR-0054).
	//
	// Empty is normal — a project with no registry states nothing, and every stage
	// still runs.
	Statement Statement

	Gate  *PendingGate
	Loop  LoopCounters
	Retry Retry

	// Blocked is why the task stopped, and it is never empty while the status is
	// blocked: a task that halts without saying why is the silent failure
	// INV-core-8 forbids.
	Blocked string

	// Seq counts transitions applied. It is the log position, and it is what lets
	// the staleness rule compare "when was this proven" against "when was this
	// touched" without a clock ever entering the reducer (ADR-0024, ADR-0032).
	Seq int

	// Evidence records what the tool reported for each delivered artifact
	// (ADR-0024). An audit that says a stage closed but not on what grounds
	// answers half the question.
	Evidence map[Artifact]Evidence
}

// NewTaskState starts a task that has not entered its first stage.
func NewTaskState(id string, kind TaskKind) TaskState {
	return TaskState{
		ID:       id,
		Status:   StatusReady,
		Context:  NewTaskContext(kind),
		Profile:  ProfileInteractive,
		Retry:    Retry{Max: 2},
		Evidence: map[Artifact]Evidence{},
	}
}

// IsTerminal reports whether the task has finished and will not transition again
// on its own.
//
// Blocked is deliberately absent: it is an anomaly a person clears with Unblock,
// and counting it as an ending would erase the difference between "this failed
// and someone should look" and "this is over" (ADR-0046).
func (s TaskState) IsTerminal() bool {
	return s.Status == StatusDone || s.Status == StatusAbandoned
}

// NeedsHuman reports whether the task is waiting on a person — either planned
// (a gate) or not (a block). Both stop progress; only one is an anomaly.
func (s TaskState) NeedsHuman() bool {
	return s.Status == StatusAwaitingGate || s.Status == StatusBlocked
}

// ShippedProfiles are the three names ShippedPolicy has an answer for.
//
// It is no longer where the defaults come from — those are files now
// (ADR-0060) — and it is not a list the engine validates against: a name it has
// never heard of is a profile someone defined, not an error (ADR-0026).
//
// What it still is: the domain of the replay fallback. `ShippedPolicy` answers
// for exactly these three and treats everything else as the most cautious
// reading, so this is the list that has to match `src/stock/profiles/`. A test
// walks it to compare the two, and without the list that comparison would have
// to hardcode the names it is checking.
func ShippedProfiles() []Profile {
	return []Profile{ProfileInteractive, ProfileTurbo, ProfileNightly}
}
