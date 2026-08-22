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

	// StatusAwaitingGate is a planned pause. The stage declared it, and the slot
	// is released while it waits — this is not a failure, and it
	// must not be reported as one.
	StatusAwaitingGate Status = "awaiting_gate"

	// StatusBlocked is an anomaly: something failed or stalled and a human has to
	// look. Always notifies (INV-5).
	StatusBlocked Status = "blocked"

	// StatusDone is a task that reached the end of its flow.
	StatusDone Status = "done"

	// StatusAbandoned is a task a person ended before it finished.
	//
	// Terminal like done, and deliberately not the same word: an audit that could
	// not tell a task that delivered from one that was called off would be missing
	// the more interesting of the two.
	StatusAbandoned Status = "abandoned"
)

// Profile names which gates actually wait for a human. It is chosen per task
// rather than per task type or per repository, because the type does not predict
// the risk — a critical bug can deserve more gating than a trivial feature.
//
// The name is all the engine holds. What the name *means* is configuration, and
// it is resolved outside the reducer — the decision arrives in the action, the
// same way a verification verdict does. That is what lets a
// project define its own profiles without the engine growing a list of them.
type Profile string

// The three shipped names. They no longer decide which gates wait — that is the
// stage's declaration and the knob — and what they still carry is the
// watchdog budget.
//
// A task records the profile it ran under, so the name stays part of the log's
// vocabulary even where it decides nothing.
const (
	ProfileInteractive Profile = "interactive"
	ProfileTurbo       Profile = "turbo"
	ProfileNightly     Profile = "nightly"
)

// GateWaited is whether a gate stopped the task, decided before the action was
// recorded and never recomputed at replay.
//
// It is a tri-state rather than a bool because the log has to distinguish "it was
// decided that nobody would be asked" from "nothing was decided here". A bool
// would make those identical, and they are not: the first is a gate that
// resolved, the second is one still owed an answer.
type GateWaited string

const (
	// GateDecisionAbsent is a gate nobody decided about — most often a stage that
	// opens none at all. Where a stage does open one, it is read as waiting: no
	// unrecorded value may turn a supervised run into an unattended one.
	GateDecisionAbsent GateWaited = ""

	// GateDecisionWaited means the gate stopped the task and a human answered it.
	GateDecisionWaited GateWaited = "waited"

	// GateDecisionPassed means the gate was reached and let through. It still
	// happened — nobody was asked.
	GateDecisionPassed GateWaited = "passed"

	// GateDecisionChecked means the commands declared for this gate ran over the
	// delivered commit and all exited 0. Nobody was asked, and the reason is a
	// verdict rather than a policy: someone declared that those commands answer
	// this gate.
	GateDecisionChecked GateWaited = "checked"

	// GateDecisionJudged means the lead judged the gate against criteria declared
	// in advance, because the knob reached the gate's criticality.
	//
	// It is separate from GateDecisionPassed for the reason the whole tri-state
	// exists: "nobody was asked because nothing was declared to ask about" and
	// "nobody was asked because a model answered" are different claims about the
	// same transition, and an audit that could not tell them apart would be the
	// weaker for it.
	GateDecisionJudged GateWaited = "judged"
)

// Waits reports what the recorded decision says, and whether it said anything at
// all. A caller that gets recorded=false has a gate nothing answered.
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
		return "nobody: the gate declared nothing to answer it with"
	default:
		return "unrecorded"
	}
}

// GateKind names why a gate stopped the task, which decides what the human is
// being asked for.
type GateKind string

const (
	// GateConfirm asks a yes or no. Nothing is attached.
	GateConfirm GateKind = "confirm"

	// GateReviewArtifact carries what the stage produced. The human may approve
	// it, adjust it, or reject it, and the approved version is what the next
	// stage consumes.
	GateReviewArtifact GateKind = "review-artifact"

	// GateLoopCeiling is a loop that hit one of its ceilings. Not
	// converging is a decision to make, not a node failure — hence a gate rather
	// than a block.
	GateLoopCeiling GateKind = "loop-ceiling"
)

// PendingGate is what a suspended task is waiting on. It is what `luna gates`
// lists, so a suspension is discoverable without anyone having watched it happen
// (INV-5).
type PendingGate struct {
	Kind   GateKind
	Stage  StageID
	Reason string

	// Artifact and Payload are set only for GateReviewArtifact: the thing the
	// human is reviewing, and its current content. An adjustment replaces Payload,
	// and the replacement is what enters the context.
	Artifact Artifact
	Payload  string

	// Judged and Reasoning are what the lead concluded about this gate, when it
	// was asked. Empty means nobody has looked — a knob too low to reach it, or a
	// gate that opened and has not been judged yet.
	//
	// They are on the pending gate rather than in the log's tail because this is
	// what a person opening the gate needs in front of them, and reaching back
	// through the events to find it is what nobody does. A judgement about a
	// *previous* opening of the same gate would be worse than none, so a
	// rejection that sends the stage round again clears them with the gate.
	Judged    string
	Reasoning string
}

// LoopCounters tracks the three ceilings separately. One counter would
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
// from LoopCounters on purpose: a transient failure must not eat the
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
// one blob so a stage can be told what it needs: `plan` lives on Acceptance,
// while `build` mostly needs Description.
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
// (INV-2).
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
	// configuration re-read at each step.
	//
	// The zero value is KnobAsk, so a task that never set one judges nothing.
	Knob Knob

	// Flow identifies the flow this task was born under, from its opening event.
	// It is here for the same reason as Profile: a replay has to know
	// which contract the history was written against, and asking the caller for
	// what the log already holds would let the two disagree.
	//
	// Empty is a task opened against no flow, which no build's flow agrees with.
	Flow FlowFingerprint

	// BudgetUSD is the most this task may spend, or zero for no ceiling.
	//
	// It is here rather than in configuration for the reason the knob is: the
	// ceiling changes mid-run, the change is a decision, and a value re-read from a
	// file at each step could not say when it moved or why.
	BudgetUSD float64

	// FlowName is which flow the fingerprint above identifies. A build running
	// several flows needs the name to load one; the fingerprint stays the thing
	// that says whether the loaded one is the one the task ran under.
	FlowName string

	// Base is the commit the last closed stage delivered, and the one the next
	// stage branches from. It is the handoff: the next agent starts from the
	// artifact rather than from a description of it.
	//
	// Empty on a task that has not closed a stage yet, which means the next
	// worktree branches from whatever the repository already is.
	Base string

	// Spent is what each stage's agent cost, as its harness reported it.
	//
	// Nothing reads it to decide anything — a transition that depended on a price
	// would replay differently when the price changed. It is here so the question
	// the whole design rests on has an answer: whether driving work through
	// stages beats doing it in one session is a measurement, and for a long time
	// there was no number to measure it with.
	//
	// Keyed by stage rather than summed, because the sum cannot answer which
	// stage is expensive, and that is the part worth acting on. A stage that ran
	// twice keeps the total of both — a retry is not free, and hiding it would
	// make the cheapest-looking flow the one that fails most.
	Spent map[StageID]Spend

	// Statement is what a person said the task is about, rebuilt from the log:
	// `TaskCreated` carries the first one and `StatementRevised` every edit after
	// it.
	//
	// It used to be read from beads and deliberately left unrecorded, on the
	// argument that a frozen copy would go stale while still looking
	// authoritative. Recording the *revision* is what answers that — the log holds
	// the current statement and how it got there.
	//
	// Empty is normal — a task nobody described still runs every stage.
	Statement Statement

	// Simulated marks a task whose stages ran no agent — a `--dry-run`. It is set
	// once, when the task is created, and never cleared: a run that simulated any
	// part of itself is a simulation, and letting it become real later is how a
	// simulated result gets read as a measured one.
	Simulated bool

	// GateChecks are the commands this task declared as the mechanical answer to
	// each gate, keyed by gate kind.
	//
	// A gate absent from the map declared nothing and goes to judgement. A gate
	// present with an empty slice is a person saying it has no mechanical answer,
	// which is a different statement and must stay sayable.
	//
	// Nil until something is declared, which is every task today.
	GateChecks map[GateKind][]string

	Gate  *PendingGate
	Loop  LoopCounters
	Retry Retry

	// Blocked is why the task stopped, and it is never empty while the status is
	// blocked: a task that halts without saying why is the silent failure
	// INV-5 forbids.
	Blocked string

	// Seq counts transitions applied. It is the log position, and it is what lets
	// the staleness rule compare "when was this proven" against "when was this
	// touched" without a clock ever entering the reducer.
	Seq int

	// Evidence records what the tool reported for each delivered artifact.
	// An audit that says a stage closed but not on what grounds
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
// and someone should look" and "this is over".
func (s TaskState) IsTerminal() bool {
	return s.Status == StatusDone || s.Status == StatusAbandoned
}

// NeedsHuman reports whether the task is waiting on a person — either planned
// (a gate) or not (a block). Both stop progress; only one is an anomaly.
func (s TaskState) NeedsHuman() bool {
	return s.Status == StatusAwaitingGate || s.Status == StatusBlocked
}

// ShippedProfiles are the three names Luna comes with.
//
// It is no longer where the defaults come from — those are files now
// — and it is not a list the engine validates against: a name it has
// never heard of is a profile someone defined, not an error.
//
// What it still is: the list that has to match `src/stock/profiles/`, so that a
// name shipped in Go and a name shipped as a file cannot drift apart. A test
// walks it to compare the two, and without the list that comparison would have
// to hardcode the names it is checking.
func ShippedProfiles() []Profile {
	return []Profile{ProfileInteractive, ProfileTurbo, ProfileNightly}
}
