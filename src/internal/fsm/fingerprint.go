package fsm

import (
	"crypto/sha256"
	"encoding/hex"
	"strings"
)

// FlowFingerprint identifies the flow a task was born under (ADR-0046).
//
// It is the flow's *identity*, never its content. The flow itself stays in
// configuration and stays editable — what the log records is enough to notice
// that the flow a task is being replayed against is not the one it ran under.
//
// The empty value is the fingerprint of a flow with no stages, and nothing more.
// Every task Luna creates is stamped with the flow it was born under, so a task
// carrying no fingerprint is one created against no flow — which agrees with
// nothing a build actually runs.
type FlowFingerprint string

// Fingerprint reduces a flow to what changes how its history reads.
//
// The rule for what belongs, from the field's own behaviour rather than from
// taste: a field read *inside the reducer* decides what a past event means, so it
// is history; a field only the node layer reads decides what happens next, so it
// is policy (ADR-0048).
//
// Covered:
//
//   - the stage's id and position — NextStage resolves by both;
//   - Requires — the entry check decides whether a past Advance entered;
//   - Produces and ProducesForHuman — the exit check decides whether a past
//     Complete closed;
//   - the *name* of the When condition — AppliesTo decides which stages a task
//     should have walked through at all;
//   - the *scope* each verifier declares — since the exit check compares the
//     recorded scope against the declared one, lowering a requirement makes a
//     past Complete that blocked start closing.
//
// Not covered:
//
//   - Role, which only internal/herdr reads. Verified by grep, not by assumption.
//   - The verifier's command. `make test` becoming `go test ./...` changes how an
//     artifact is proven, not how much was proven, and evidence records what
//     actually ran (ADR-0024). Including it would refuse a replay because someone
//     renamed a Makefile target, and a check that fires on changes that do not
//     matter is one people learn to route around.
//   - The body of a condition, which no fingerprint can see. The name is the
//     handle a person maintains deliberately, the same discipline the log's
//     hand-written action names already rely on.
//
// The empty flow has an empty fingerprint rather than the hash of nothing, so a
// caller that never had a flow is not told it disagrees with one.
func Fingerprint(flow []Stage) FlowFingerprint {
	if len(flow) == 0 {
		return ""
	}

	// A plain-text rendering rather than hashing struct memory: the digest has to
	// be stable across builds and Go versions, and it is worth being able to read
	// what went into it when two of them disagree.
	var b strings.Builder
	for _, stage := range flow {
		b.WriteString(string(stage.ID))
		// The condition's name, empty when the stage is unconditional — so a flow
		// that never used conditions renders the same as one that spells the
		// default out.
		b.WriteString("?")
		b.WriteString(stage.When.Name)
		b.WriteString("(")
		writeArtifacts(&b, stage.Requires)
		b.WriteString("->")
		writeArtifacts(&b, stage.Produces)
		// Kept apart from Produces rather than concatenated: moving an artifact
		// between the two changes whether the static check demands a consumer for
		// it (ADR-0021), and that is a different flow.
		b.WriteString("+")
		writeArtifacts(&b, stage.ProducesForHuman)
		b.WriteString("|")
		writeProofs(&b, stage)
		b.WriteString("|")
		writeGate(&b, stage.Gate)
		b.WriteString("|")
		writeReview(&b, stage.Review)
		b.WriteString(")")
	}

	sum := sha256.Sum256([]byte(b.String()))
	// Half the digest: this distinguishes flows a person wrote, not adversarial
	// collisions, and a short value is one someone can compare by eye in a log.
	return FlowFingerprint(hex.EncodeToString(sum[:8]))
}

// writeGate renders the gate a stage opens, if it opens one.
//
// The kind and the artifact only. The reason is prose a person reads at the
// moment they are asked — rewording "confirm the repositories" cannot change
// whether a past Advance suspended, and refusing a replay over it would be the
// noise ADR-0048 keeps out.
//
// `criticality` and `judge` are out for the same reason, and it is worth stating
// because the intuition runs the other way. They decide *who is asked* at a gate
// that is opening now; they cannot change whether a past Advance suspended,
// because what the reducer replays is the recorded GateDecision and not the
// policy that produced it (ADR-0026). A gate answered by the lead last week
// replays as `judged` whatever the stage file says today.
//
// This is the ADR-0048 rule applied literally — a field the reducer reads is
// history, a field only the layer above reads is policy — and it lands opposite
// to what RFC-0006 assumed. The RFC expected populating the stock to stop every
// open task replaying, and planned around that; measured against the rule, the
// two fields are policy, so a project can declare criticality on a running flow
// without stranding a single task.
func writeGate(b *strings.Builder, gate *GateSpec) {
	if gate == nil {
		return
	}
	b.WriteString(string(gate.Kind))
	if gate.Artifact != "" {
		b.WriteString(":")
		b.WriteString(string(gate.Artifact))
	}
}

// writeReview renders what a review stage's finding costs.
//
// Both fields count: where the work goes back decides the stage a past finding
// moved the task to, and what it invalidates decides which artifacts left the
// context — so a replay under a changed spec would rebuild a different state.
func writeReview(b *strings.Builder, review *ReviewSpec) {
	if review == nil {
		return
	}
	b.WriteString(string(review.SendsBackTo))
	b.WriteString("<")
	writeArtifacts(b, review.Invalidates)
}

// writeProofs renders how much each owed artifact has to be proven.
//
// The scope only, never the command: what a past Complete had to satisfy is the
// requirement, and lowering it is what makes a blocked stage start closing.
//
// It walks the owed artifacts in declaration order rather than ranging over the
// Verifiers map, because Go randomises map iteration and a fingerprint that
// changed between two runs of the same binary would refuse every replay. That is
// the kind of nondeterminism the reducer's purity rules out by design, and it
// would have entered here through the back door.
func writeProofs(b *strings.Builder, stage Stage) {
	owed := append(append([]Artifact{}, stage.Produces...), stage.ProducesForHuman...)

	for i, artifact := range owed {
		if i > 0 {
			b.WriteString(",")
		}
		b.WriteString(string(artifact))
		b.WriteString(":")
		b.WriteString(string(VerifierFor(stage, artifact).Proves()))
	}
}

// writeArtifacts renders a stage's artifact list in declaration order.
//
// Order is preserved rather than sorted, because the order artifacts are declared
// in is part of how a person reads the contract, and reordering them is an edit
// worth noticing.
func writeArtifacts(b *strings.Builder, artifacts []Artifact) {
	for i, a := range artifacts {
		if i > 0 {
			b.WriteString(",")
		}
		b.WriteString(string(a))
	}
}

// Matches reports whether a log written under this fingerprint can be replayed
// against the given flow.
//
// The comparison is plain equality, and the empty value needs no special case:
// `Fingerprint` answers empty for a flow with no stages, so an empty fingerprint
// agrees with an empty flow and disagrees with every real one. That is the right
// answer to both readings — a task created with no flow, and a caller replaying
// one against nothing.
func (f FlowFingerprint) Matches(flow []Stage) bool {
	return f == Fingerprint(flow)
}
