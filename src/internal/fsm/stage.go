// Package fsm is Luna's engine: stages, contract and transitions.
//
// The FSM decides which stage comes next; the model works inside it. See
// docs/invariants/core.md (INV-core-1) and docs/ADRs/0001-flow-control-out-of-model.md.
package fsm

// Artifact identifies a product of the flow — what a stage requires to start or
// delivers when it finishes. It is a named string rather than a raw one so the
// compiler keeps "artifact name" apart from any other text travelling with it.
type Artifact string

// StageID identifies a stage within a flow.
type StageID string

// TaskKind is the nature of the task. It governs which conditional stages enter
// the flow — see docs/ADRs/0014-conditional-stages.md.
type TaskKind string

// The task kinds Luna ships with. They govern which conditional stages enter the
// flow: diagnose is bug-only, spec and harden skip a chore, code-review skips
// docs (see docs/architecture/stages.md).
const (
	KindFeature TaskKind = "feature"
	KindBug     TaskKind = "bug"
	KindChore   TaskKind = "chore"
	KindDocs    TaskKind = "docs"
)

// TaskID is the root of the artifact graph: the only input no stage produces,
// because the task already arrives carrying it.
const TaskID Artifact = "task_id"

// Fact is something discovered about the task **during** execution — unlike
// TaskKind, which it carries from intake onwards. It is what lets a conditional
// stage depend on what the work revealed, not only on what was known before it
// started.
type Fact string

// TouchesStructure marks that the change altered the system's structure. It is
// only knowable by looking at what build produced, and it is the entry condition
// for the architecture stage.
const TouchesStructure Fact = "touches_structure"

// TaskContext is what a stage condition consults to decide whether it enters the
// flow: the nature of the task, the artifacts produced so far, and the facts
// discovered along the way.
//
// It exists as a type instead of passing TaskKind alone because not every
// condition is about the nature of the task. diagnose depends on the kind, known
// at intake; architecture depends on the change having touched the structure,
// which is only known after build. A single parameter serves both without
// duplicating the mechanism.
type TaskContext struct {
	Kind      TaskKind
	Artifacts map[Artifact]bool
	Facts     map[Fact]bool
}

// NewTaskContext builds a context for a task that is just starting: the kind
// only, with no artifact produced and no fact discovered.
func NewTaskContext(kind TaskKind) TaskContext {
	return TaskContext{
		Kind:      kind,
		Artifacts: map[Artifact]bool{TaskID: true},
		Facts:     map[Fact]bool{},
	}
}

// HasFact reports whether the fact was discovered. Safe to call on a context with
// nil maps — a condition should not have to know whether someone initialized them.
func (c TaskContext) HasFact(f Fact) bool {
	return c.Facts[f]
}

// HasArtifact reports whether the artifact is already available in the context.
func (c TaskContext) HasArtifact(a Artifact) bool {
	return c.Artifacts[a]
}

// Stage is a stage's contract: what it requires to start and what it delivers
// when it finishes.
//
// Produces and ProducesForHuman are separate fields on purpose. The first is
// consumed by some later stage and takes part in the static check; the second is
// read by a person and is exempt from it — nobody consuming it is not a defect.
// See docs/ADRs/0021-produces-for-human-is-a-separate-contract-field.md.
type Stage struct {
	ID   StageID
	Role string

	// Requires is what must be in the context for the stage to start.
	Requires []Artifact

	// Produces is what the flow consumes. Checked on exit and by the static check.
	Produces []Artifact

	// ProducesForHuman is what only a person reads: reports, assessments,
	// diagnoses. Checked on exit like Produces, exempt from the static check.
	ProducesForHuman []Artifact

	// When decides whether the stage enters the flow. Nil means unconditional.
	//
	// It receives the whole context rather than just the kind: not every
	// condition is about the nature of the task. diagnose looks at the kind;
	// architecture looks at a fact discovered during execution.
	When func(TaskContext) bool
}

// ProducesArtifact reports whether the stage delivers the artifact for the flow
// to consume. ProducesForHuman does not count: an audit report satisfies nobody's
// Requires (INV-core-11).
func (s Stage) ProducesArtifact(a Artifact) bool {
	return containsArtifact(s.Produces, a)
}

// ProducesForHumanArtifact reports whether the stage delivers the artifact for a
// person to read. Separate from ProducesArtifact because the question differs:
// this one is not about availability to the flow, but about whether the stage
// committed to delivering an assessment.
func (s Stage) ProducesForHumanArtifact(a Artifact) bool {
	return containsArtifact(s.ProducesForHuman, a)
}

// containsArtifact is a linear scan because a stage contract's lists hold half a
// dozen items — an index would cost more in allocation than it saves in
// comparison.
func containsArtifact(list []Artifact, want Artifact) bool {
	for _, a := range list {
		if a == want {
			return true
		}
	}
	return false
}

// AppliesTo reports whether the stage enters the flow in this context.
func (s Stage) AppliesTo(ctx TaskContext) bool {
	if s.When == nil {
		return true
	}
	return s.When(ctx)
}
