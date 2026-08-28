// Package fsm is Luna's engine: stages, contract and transitions.
//
// The FSM decides which stage comes next; the model works inside it. See
// docs/architecture.md.
package fsm

import (
	"fmt"
	"strings"
)

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

const (
	// TouchesStructure marks that the change altered the system's structure. It is
	// only knowable by looking at what build produced, which is what makes it a
	// Fact rather than part of TaskKind.
	TouchesStructure Fact = "touches_structure"

	// TriagedBug marks that the intake read the task and the code and concluded
	// something is broken, rather than missing.
	//
	// A fact and not the task's kind, and the difference is the whole reason it
	// exists: the kind is what a person declared when opening the task, before
	// anyone read the code. This is what the intake concluded after reading it.
	// The kind stays where it is — one is a statement of intent, the other a
	// finding, and collapsing them would lose which of the two a later reader is
	// looking at.
	TriagedBug Fact = "triaged_bug"
)

// KnownFacts are the facts a stage may be discovered to have.
//
// A closed set rather than free strings, for the reason the capabilities are one:
// a typo in a fact fails silent and in the permissive direction — the condition
// reading it is simply never true, and the stage it gates never runs with nothing
// saying why.
func KnownFacts() []Fact { return []Fact{TouchesStructure, TriagedBug} }

// ParseFact turns a recorded name into a fact, refusing what it does not know.
func ParseFact(name string) (Fact, error) {
	for _, known := range KnownFacts() {
		if Fact(name) == known {
			return known, nil
		}
	}

	names := make([]string, 0, len(KnownFacts()))
	for _, known := range KnownFacts() {
		names = append(names, string(known))
	}
	return "", fmt.Errorf("unknown fact %q (%s)", name, strings.Join(names, ", "))
}

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
	ID StageID

	// Agent is the harness kind that runs this stage, travelling to the node
	// layer as agent.Call.Kind. Empty on a mechanical stage, which starts none —
	// and that emptiness is what Mechanical reads, so it is the field that
	// decides whether anything runs at all.
	Agent string

	// Brief is what the agent is told: what happens here, what it owes, and what
	// its delivery is held against.
	//
	// It lives on the stage rather than in a shared role file because a brief is
	// about the work, and the work is what a stage is. The cost is real and was
	// weighed: two stages doing the same kind of judging now each carry their own
	// text, and keeping them consistent is a person's job rather than a file's.
	// What it buys is that one file answers what a stage is for, and that a stage
	// briefed for its own contract can say things no shared text could.
	//
	// It is instruction, not enforcement: a restriction that lives only here is
	// the violation INV-4 names, and closing that gap needs ToolsDeny, which the
	// harness applies before the agent starts.
	Brief string

	// Skills are the capability bundles this stage loads.
	Skills []string

	// Discovers names the facts this stage may conclude about the task.
	//
	// Declared rather than open, and the difference is the whole guard: a stage
	// that could record any fact could switch on a conditional stage from anywhere
	// in the flow. What a stage is allowed to find out is part of its contract,
	// like what it is allowed to produce.
	Discovers []Fact

	// Uncontained starts this stage's agent outside the sandbox.
	//
	// INV-4 names the one stage this is for and asks that no second one inherit it
	// by accident, which `AuditContainment` is. Declared rather than inferred: an
	// exemption from containment is the last thing that should be derived from
	// something else happening to be true.
	Uncontained bool

	// ToolsDeny names capabilities this stage must not have — `Edit`, `Write`.
	//
	// It names what the stage cannot do, never how a harness spells it: claude
	// says `Edit`, pi says `edit`, codex takes no names at all and denies writing
	// with a sandbox mode. Keeping the vocabulary out of here is what lets a
	// stage move from one agent to another without being rewritten, and what
	// stops it from silently ceasing to deny anything when its agent changes.
	ToolsDeny []Capability

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

	// Loop declares that this stage converges rather than simply finishing: it
	// runs, judges the round, and either leaves or goes round again.
	//
	// Nil means the stage runs once, which is every other stage.
	Loop *LoopSpec

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

	// AutonomyFloor is the lowest autonomy setting that absorbs this gate, 1–10.
	// The lead may answer the gate on its own when `knob >= AutonomyFloor`.
	//
	// Named after the knob it is compared against. The floor is the autonomy the
	// gate needs before nobody is asked.
	//
	// The range starts at 1 rather than 0 because a gate exists precisely because
	// something about it matters: a floor of zero would be a gate every knob
	// setting absorbs, including the most conservative one. Zero here means the
	// stage declared nothing, and DefaultAutonomyFloor is what that resolves to.
	AutonomyFloor int

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
		g.AutonomyFloor != 0 ||
		len(g.Judge) > 0
}

// DefaultAutonomyFloor is what a gate that declares none is treated as.
//
// The highest value, so only the most autonomous setting absorbs it. Refusing to
// load such a gate was the louder alternative and is wrong for this feature
// specifically: every shipped stage declares no floor today, so refusing would
// break every existing flow the moment the field arrived. Defaulting to the
// safest value is what keeps "declare nothing, change nothing" true.
const DefaultAutonomyFloor = 10

// Resolved is the autonomy floor the stage declared, or the default when it
// declared nothing.
//
// A method rather than a value filled in at load time: the zero value has to keep
// meaning "undeclared" for a GateSpec built in a test or decoded from anywhere
// else, and a loader that normalised it would make that indistinguishable from a
// deliberate 10.
func (g *GateSpec) Resolved() int {
	if g == nil || g.AutonomyFloor == 0 {
		return DefaultAutonomyFloor
	}
	return g.AutonomyFloor
}

// LoopSpec is how a converging stage decides whether to go round again.
//
// The stage runs build, then hygiene, then acceptance, and asks what the round
// produced. Four answers: it converged, it progressed, it regressed, it is stuck.
// The first leaves the loop; the middle two go round again; the last asks a
// person.
type LoopSpec struct {
	// ConvergesOn names the artifacts whose evidence has to be passing before the
	// loop may be declared converged.
	//
	// This is the boundary between what the model decides and what it may not.
	// The model reads the round and judges it — that is the point of putting a
	// model here at all, and a scoreboard of exit codes cannot tell "progressed"
	// from "swapped one problem for another". What it cannot do is declare the
	// work finished over a command that says otherwise: leaving the loop is the
	// one outcome that ends the judging, so it is the one the engine checks.
	//
	// Empty means nothing is checked, which the static check warns about — a loop
	// whose exit nothing proves is a loop that closes on an opinion.
	ConvergesOn []Artifact

	// Limits are the ceilings for this loop. Zero takes DefaultLoopLimits.
	Limits LoopLimits

	// Invalidates names what stops being true when a round is judged a regression
	// and the work goes round again. The green attested to code being reverted.
	Invalidates []Artifact
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
