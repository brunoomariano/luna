package lead

import (
	"fmt"
	"strings"

	"github.com/brunoomariano/luna/src/internal/fsm"
)

// JudgingBrief is what the lead is told when it judges a gate.
//
// The shape is the design's, not the caller's, and that is the measurement's
// doing rather than taste. Asked to "read this and decide", the same model
// approved a delivery that violated an acceptance criterion three times out of
// three — twice while naming the contradiction in its own reasoning. Asked to
// walk the criteria one at a time and quote the line that settles each, it
// rejected the same artifact three times out of three. Same model, same
// artifact, same criteria: the failure was the question (PRD gate-0001).
//
// So the three rules below are not advice to a prompt writer. Each one is a
// measured failure that the instruction closed, and leaving their wording to
// whoever wires this up would leave the result to chance.
func JudgingBrief(gate *fsm.GateSpec, artifact, checkout string) string {
	var b strings.Builder

	b.WriteString(`You are answering a gate. A person set this run's autonomy high
enough that this decision is yours, and it is recorded as yours.

Three rules, in order:

  1. Do not trust the artifact's own verdict. A criterion the artifact
     contradicts is VIOLATED no matter what its summary, its conclusion or
     its own review says about itself.

  2. Answer criterion by criterion. For each one, quote the line of the
     artifact that settles it, and mark it met, violated or unsupported.

  3. Verify rather than believe. `)

	if checkout == "" {
		b.WriteString(`You have no checkout for this one, so any
     criterion whose evidence is a claim rather than a check is
     UNSUPPORTED — not met.
`)
	} else {
		fmt.Fprintf(&b, `The delivered commit is checked out at
     %s. Run what settles a criterion instead of believing a claim about
     it. A criterion proven by a command that does not actually test it is
     UNSUPPORTED, not met.
`, checkout)
	}

	b.WriteString("\nThe criteria:\n\n")
	for i, criterion := range gate.Judge {
		fmt.Fprintf(&b, "  %d. %s\n", i+1, criterion)
	}

	if artifact != "" {
		b.WriteString("\nThe artifact:\n\n")
		b.WriteString(artifact)
		b.WriteString("\n")
	}

	b.WriteString(`
Answer with one line first — APPROVE, REJECT or CANNOT-DECIDE — and then
your criterion-by-criterion working.

CANNOT-DECIDE is a correct answer, not a failure. If the artifact does not
give you what a criterion needs, say so and it goes to a person. Deciding
anyway on a criterion you could not check is the one thing that would make
this worse than asking.
`)

	return b.String()
}

// Judgement is what the lead concluded about a gate.
type Judgement int

const (
	// JudgedCannotDecide is the default, and it is deliberate: anything this
	// package cannot read as a clear answer falls to a person. A judgement that
	// defaulted to approving would turn an unparseable reply into an approval.
	JudgedCannotDecide Judgement = iota

	// JudgedApprove accepts what the gate was holding.
	JudgedApprove

	// JudgedReject refuses it.
	JudgedReject
)

// ReadJudgement finds the verdict in what the lead said.
//
// It reads the first line only, which is what the brief asks for. Scanning the
// whole reply would find the word "approve" in the working — "criterion 2 is met,
// so I would approve if 3 held" is not an approval, and a parser that took it for
// one would approve on reasoning that concluded the opposite.
//
// Anything it cannot read is CANNOT-DECIDE, which falls to a person.
func ReadJudgement(said string) Judgement {
	first, _, _ := strings.Cut(strings.TrimSpace(said), "\n")
	first = strings.ToUpper(strings.TrimSpace(first))

	// Checked before APPROVE because "CANNOT-DECIDE" contains neither, but a reply
	// like "CANNOT-DECIDE: I would approve if..." contains both — and the verdict
	// is the one the model led with.
	switch {
	case strings.HasPrefix(first, "CANNOT-DECIDE"), strings.HasPrefix(first, "CANNOT DECIDE"):
		return JudgedCannotDecide
	case strings.HasPrefix(first, "APPROVE"):
		return JudgedApprove
	case strings.HasPrefix(first, "REJECT"):
		return JudgedReject
	default:
		return JudgedCannotDecide
	}
}
