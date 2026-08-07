package fsm

// Status is where a task stands. The five values are exhaustive: a task is always
// in exactly one of them.
type Status string

const (
	// StatusReady is a task that has not entered its first stage yet.
	StatusReady Status = "ready"

	// StatusRunning is a node working inside a stage.
	StatusRunning Status = "running"

	// StatusAwaitingGate is a planned pause. The profile foresaw it, and the slot
	// is released while it waits (INV-core-10) — this is not a failure, and it
	// must not be reported as one.
	StatusAwaitingGate Status = "awaiting_gate"

	// StatusBlocked is an anomaly: something failed or stalled and a human has to
	// look. Always notifies (INV-core-8).
	StatusBlocked Status = "blocked"

	// StatusDone is a task that reached the end of its flow.
	StatusDone Status = "done"
)

// GateKind names why a gate stopped the task, which decides what the human is
// being asked for (ADR-0022).
type GateKind string

const (
	// GateConfirm asks a yes or no. Nothing is attached.
	GateConfirm GateKind = "confirm"

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
}

// LoopLimits are the ceilings themselves, configurable per loop.
type LoopLimits struct {
	MaxRounds   int
	NoProgress  int
	Oscillation int
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

// TaskState is everything the engine knows about one task. It is rebuilt by
// replaying the append-only log, so it holds no pointer to anything live
// (INV-core-2).
type TaskState struct {
	ID      string
	Status  Status
	Stage   StageID
	Context TaskContext

	Gate  *PendingGate
	Loop  LoopCounters
	Retry Retry

	// Blocked is why the task stopped, and it is never empty while the status is
	// blocked: a task that halts without saying why is the silent failure
	// INV-core-8 forbids.
	Blocked string

	// Evidence records what the tool reported for each delivered artifact
	// (ADR-0024). An audit that says a stage closed but not on what grounds
	// answers half the question.
	Evidence map[Artifact]string
}

// NewTaskState starts a task that has not entered its first stage.
func NewTaskState(id string, kind TaskKind) TaskState {
	return TaskState{
		ID:       id,
		Status:   StatusReady,
		Context:  NewTaskContext(kind),
		Retry:    Retry{Max: 2},
		Evidence: map[Artifact]string{},
	}
}

// IsTerminal reports whether the task has finished and will not transition again
// on its own.
func (s TaskState) IsTerminal() bool {
	return s.Status == StatusDone
}

// NeedsHuman reports whether the task is waiting on a person — either planned
// (a gate) or not (a block). Both stop progress; only one is an anomaly.
func (s TaskState) NeedsHuman() bool {
	return s.Status == StatusAwaitingGate || s.Status == StatusBlocked
}
