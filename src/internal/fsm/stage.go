// Package fsm is Luna's engine: stages, contract and transitions.
//
// The FSM decides which stage comes next; the model works inside it. See
// docs/architecture.md.
package fsm

import "fmt"

// Artifact identifies a product of the flow — what a stage requires to start or
// delivers when it finishes. It is a named string rather than a raw one so the
// compiler keeps "artifact name" apart from any other text travelling with it.
type Artifact string

// StageID identifies a stage within a flow.
type StageID string

// TaskKind is the nature of the task. It governs which conditional stages enter
// the flow — see docs/architecture.md.
type TaskKind string

// The task kinds Luna ships with. They govern which conditional stages enter the
// flow: diagnose is bug-only, and review skips a chore (see docs/architecture.md).
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
// only knowable by looking at what build produced, which is what makes it a Fact
// rather than part of TaskKind. No shipped stage is gated on it since the review
// merge; it stays because a project flow can gate one on it, and the mechanism
// for a mid-run condition has no other example.
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
// See docs/decisions.md.
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

	// Verifiers declares how each produced artifact is proven. An
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
	// fingerprint could record.
	When Condition

	// Gate is the decision a person makes on entering this stage, when there is
	// one.
	//
	// It lives on the stage rather than in a switch over stage ids because a flow
	// is meant to be replaceable, and a custom flow with no gates at
	// all was not a flow anyone would want — it was what the code did.
	Gate *GateSpec

	// Review declares that this stage judges work rather than doing it, and what
	// its verdict costs when it sends the work back.
	//
	// Nil means the stage produces nothing to review. Only a review stage may
	// emit a ReviewFinding, which is whoever-writes-does-not-review in the engine:
	// an implementer sending its own work back would be reviewing itself.
	Review *ReviewSpec

	// Context says whether this stage continues the previous stage's session or
	// starts a clean one. The zero value is ContextFresh.
	//
	// Starting fresh everywhere used to be a rule, on the argument that roles
	// erode in long sessions. The concern is real and the mechanism was wrong:
	// what protects the flow is the check at the exit, which catches a drifted
	// agent and a merely bad one alike. So this is a setting, and which value
	// serves is a measurement rather than a belief — a resumed call costs an
	// order of magnitude less, and whether it delivers as well is the question.
	//
	// It is deliberately NOT part of the flow fingerprint. A fingerprint records
	// what delivering means, so that a replay against a changed flow is refused;
	// this changes what a stage costs and how it is briefed, and a task halfway
	// through should be able to switch without its log becoming unreplayable.
	// The same reasoning that keeps an artifact's path out of it.
	Context StageContext

	// Memory says whether this stage runs inside the project's durable memory.
	// The zero value is MemoryOff.
	//
	// Policy like Context, and out of the fingerprint for the same reason: it
	// changes what the agent knows walking in, not what the stage owes walking
	// out. A task halfway through must be able to gain memory without its log
	// becoming unreplayable.
	//
	// Off by default, and deliberately: a shared project memory that every stage
	// of every task writes to is one that fills with the transient. Reading is
	// cheap and safe; writing is the part worth declaring.
	Memory StageMemory
}

// StageMemory is whether a stage's agent sees the project's durable memory.
type StageMemory string

const (
	// MemoryOff runs the harness directly. The default.
	MemoryOff StageMemory = "off"

	// MemoryOn wraps the call so the agent starts knowing what the project
	// already decided, and its session is consolidated on the way out.
	MemoryOn StageMemory = "on"
)

// Enabled answers whether the stage runs with memory. The zero value reads as
// off, so a flow that never mentions it behaves as every flow did before.
func (m StageMemory) Enabled() bool { return m == MemoryOn }

// ParseStageMemory reads the `memory` key, refusing what it does not know for
// the reason ParseStageContext does: a typo silently meaning "off" looks like a
// setting that was applied.
func ParseStageMemory(value, at string) (StageMemory, error) {
	switch StageMemory(value) {
	case MemoryOn, MemoryOff:
		return StageMemory(value), nil
	case "":
		return MemoryOff, nil
	}
	return "", fmt.Errorf("%s: memory is %q, and the choices are %q and %q",
		at, value, MemoryOff, MemoryOn)
}

// StageContext is how a stage's agent starts: clean, or continuing.
type StageContext string

const (
	// ContextFresh starts a new session. The default, and the only thing the
	// first stage of a task can do.
	ContextFresh StageContext = "fresh"

	// ContextLive continues the previous stage's session.
	//
	// It is refused across a change of role. A reviewer that inherited the
	// implementer's session would be reading its own reasoning rather than the
	// delivery, and independence of review is the one part of the old rule that
	// verification cannot stand in for.
	ContextLive StageContext = "live"
)

// Fresh answers whether this stage starts clean. The zero value reads as fresh,
// so a flow that never mentions context behaves the way every flow did before
// the setting existed.
func (c StageContext) Fresh() bool { return c != ContextLive }

// ParseStageContext reads the `context` key. An unknown value is refused rather
// than defaulted: a typo silently meaning "fresh" would look like a setting that
// was applied and cost the measurement it was written for.
func ParseStageContext(value, at string) (StageContext, error) {
	switch StageContext(value) {
	case ContextFresh, ContextLive:
		return StageContext(value), nil
	case "":
		return ContextFresh, nil
	}
	return "", fmt.Errorf("%s: context is %q, and the choices are %q and %q",
		at, value, ContextFresh, ContextLive)
}

// GateSpec is a gate a stage opens, declared as part of the contract.
type GateSpec struct {
	Kind   GateKind
	Reason string

	// Artifact is what the human reads, for a review-artifact gate. Empty for the
	// kinds that only ask yes or no.
	Artifact Artifact

	// Criticality is how much this gate matters, 1–10, higher being more critical.
	// The knob absorbs every gate whose criticality is at or below it.
	//
	// The range starts at 1 rather than 0 because a gate exists precisely because
	// something about it matters: a criticality of zero would be a gate every knob
	// setting absorbs, including the most conservative one. Zero here means the
	// stage declared nothing, and DefaultCriticality is what that resolves to.
	Criticality int

	// Judge is what the lead is asked to decide, one criterion per entry.
	//
	// Empty means this gate has no judgement half: the declared checks are the
	// whole answer, and where there are none either, a person is asked.
	Judge []string

	// ReadableJudge names the criteria of Judge that are settled by reading the
	// artifact, rather than by running something over a delivery.
	//
	// It exists because the lead could not approve the one gate Luna ships. The
	// judging brief tells it not to believe a claim — right, and the reason that
	// rule exists is a stage that once approved a defect it had itself named —
	// but with no checkout it made *every* criterion unsupported. The shipped
	// gate's criteria are all judgements about the text of an artifact that is
	// attached to the brief, so all of them came back unsupported and `nightly`
	// stopped at the only gate in the flow.
	//
	// Declared per criterion rather than inferred, because "this can be settled
	// by reading" is exactly the judgement a model must not make about its own
	// task. An entry naming a criterion Judge does not have is refused by the
	// static check: a typo would silently narrow what the lead may approve on.
	//
	// Empty leaves the old rule exactly as it was, which is what keeps this
	// additive for a gate that says nothing.
	ReadableJudge []string
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
// safest value is what keeps "declare nothing, change nothing" true.
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
// anything saying so.
type ReviewSpec struct {
	// SendsBackTo is the stage the work returns to when a finding is aligned.
	SendsBackTo StageID

	// Invalidates are the artifacts that stop being true once the work goes back —
	// the green attested to code that no longer exists.
	Invalidates []Artifact
}

// ProducesArtifact reports whether the stage delivers the artifact for the flow
// to consume. ProducesForHuman does not count: an audit report satisfies nobody's
// Requires (INV-3).
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
