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
// The empty value means a log written before fingerprints existed, and it
// matches everything: an old log keeps replaying rather than becoming
// unreadable because a field was added.
type FlowFingerprint string

// Fingerprint reduces a flow to what changes how its history reads.
//
// Covered: each stage's id, in order, with the artifacts it requires, produces,
// and produces for a human. Those decide whether a past Advance entered and a
// past Complete closed, so changing them rewrites what the log means.
//
// Not covered: Role, Verifiers, and the body of a When condition. Those are read
// at the moment of use — changing them alters what happens next, not what already
// happened. A fingerprint that covered them would refuse a replay because someone
// edited a brief, and a check that fires on changes that do not matter is one
// people learn to route around.
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
		b.WriteString("(")
		writeArtifacts(&b, stage.Requires)
		b.WriteString("->")
		writeArtifacts(&b, stage.Produces)
		// Kept apart from Produces rather than concatenated: moving an artifact
		// between the two changes whether the static check demands a consumer for
		// it (ADR-0021), and that is a different flow.
		b.WriteString("+")
		writeArtifacts(&b, stage.ProducesForHuman)
		b.WriteString(")")
	}

	sum := sha256.Sum256([]byte(b.String()))
	// Half the digest: this distinguishes flows a person wrote, not adversarial
	// collisions, and a short value is one someone can compare by eye in a log.
	return FlowFingerprint(hex.EncodeToString(sum[:8]))
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
// An empty fingerprint matches anything: it means the log predates the field, and
// refusing those would make adding the field a breaking change for every task
// already in the store.
func (f FlowFingerprint) Matches(flow []Stage) bool {
	return f == "" || f == Fingerprint(flow)
}
