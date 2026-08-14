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

	// Verifiers declares how each produced artifact is proven (ADR-0032). An
	// artifact absent from the map is verified by existence alone, which the
	// static check warns about so the floor stays a choice.
	Verifiers map[Artifact]Verifier

	// When decides whether the stage enters the flow. The zero value is
	// unconditional.
	//
	// It receives the whole context rather than just the kind: not every
	// condition is about the nature of the task. diagnose looks at the kind;
	// architecture looks at a fact discovered during execution.
	//
	// It carries a name because it is history — it decides which stages a task
	// should have walked through — and a bare function has no identity a
	// fingerprint could record (ADR-0048).
	When Condition

	// Gate is the decision a person makes on entering this stage, when there is
	// one (ADR-0049).
	//
	// It lives on the stage rather than in a switch over stage ids because a flow
	// is meant to be replaceable (ADR-0017), and a custom flow with no gates at
	// all was not a flow anyone would want — it was what the code did.
	Gate *GateSpec

	// Review declares that this stage judges work rather than doing it, and what
	// its verdict costs when it sends the work back.
	//
	// Nil means the stage produces nothing to review. Only a review stage may
	// emit a ReviewFinding, which is INV-core-7 in the engine: an implementer
	// sending its own work back would be reviewing itself.
	Review *ReviewSpec
}

// GateSpec is a gate a stage opens, declared as part of the contract.
type GateSpec struct {
	Kind   GateKind
	Reason string

	// Artifact is what the human reads, for a review-artifact gate. Empty for the
	// kinds that only ask yes or no.
	Artifact Artifact

	// Criticality is how much this gate matters, 1–10, higher being more critical.
	// The knob absorbs every gate whose criticality is at or below it (RFC-0006).
	//
	// The range starts at 1 rather than 0 because a gate exists precisely because
	// something about it matters: a criticality of zero would be a gate every knob
	// setting absorbs, including the most conservative one. Zero here means the
	// stage declared nothing, and DefaultCriticality is what that resolves to.
	Criticality int

	// Judge is what the lead is asked to decide, one criterion per entry.
	//
	// Empty means this gate has no judgement half: the declared checks are the
	// whole answer, and where there are none either, a person is asked. That is
	// every gate in the shipped stock today, which is what makes this additive.
	Judge []string
}

// declared reports whether the file said anything about a gate at all.
//
// It replaces comparing against the zero value, which stopped compiling once
// Judge made the struct uncomparable — and is clearer for it: what this asks is
// whether a person wrote a [gate] block, and listing the fields says so where
// `!= (GateSpec{})` only implied it.
func (g GateSpec) declared() bool {
	return g.Kind != "" ||
		g.Reason != "" ||
		g.Artifact != "" ||
		g.Criticality != 0 ||
		len(g.Judge) > 0
}

// DefaultCriticality is what a gate that declares none is treated as.
//
// The highest value, so only the most autonomous setting absorbs it. Refusing to
// load such a gate was the louder alternative and is wrong for this feature
// specifically: every shipped stage declares no criticality today, so refusing
// would break every existing flow the moment the field arrived. Defaulting to the
// safest value is what keeps "declare nothing, change nothing" true (RFC-0006).
const DefaultCriticality = 10

// Resolved is the criticality the stage declared, or the default when it
// declared nothing.
//
// A method rather than a value filled in at load time: the zero value has to keep
// meaning "undeclared" for a GateSpec built in a test or decoded from anywhere
// else, and a loader that normalised it would make that indistinguishable from a
// deliberate 10.
func (g *GateSpec) Resolved() int {
	if g == nil || g.Criticality == 0 {
		return DefaultCriticality
	}
	return g.Criticality
}

// AbsorbedBy reports whether a knob at this setting lets the lead judge this
// gate.
//
// The comparison is the whole knob: both scales are the same numbers, so a
// project writing `criticality = 7` beside a gate knows exactly which setting
// reaches it.
func (g *GateSpec) AbsorbedBy(knob int) bool {
	return knob >= g.Resolved()
}

// ReviewSpec is what a review stage does when its finding lands.
//
// Both fields were constants in the reducer, which meant a project could rename
// `build` or invalidate a differently-named green and lose the behaviour without
// anything saying so (ADR-0049).
type ReviewSpec struct {
	// SendsBackTo is the stage the work returns to when a finding is aligned.
	SendsBackTo StageID

	// Invalidates are the artifacts that stop being true once the work goes back —
	// the green attested to code that no longer exists (ADR-0020).
	Invalidates []Artifact
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
	return s.When.Met(ctx)
}
