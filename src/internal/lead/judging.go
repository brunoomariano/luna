package lead

import (
	"fmt"
	"strings"

	"github.com/brunoomariano/luna/src/internal/fsm"
)

// Evidence is everything a gate's judgement is allowed to rest on.
//
// A struct rather than three string arguments because they are all strings and
// the compiler would not catch a transposition — and because what belongs here
// is a design question that should be answered in one place: a criterion can
// only be settled by something in this struct.
type Evidence struct {
	// Artifact is what the gate is holding — the contract, the plan, whatever
	// the stage produced and the gate is reviewing.
	Artifact string

	// Checkout is the delivered commit on disk, when the gate has one. Empty
	// means the criteria are answered by reading rather than by running.
	Checkout string

	// Statement is what the task was opened with.
	//
	// It is here because a criterion can ask about the task rather than about
	// the artifact — "every acceptance criterion in the task appears as an
	// obligation" is the shipped flow's second criterion, and it asks whether
	// the contract covers what was actually requested. Without the statement
	// that question has no source to check against, so the honest answer is
	// UNSUPPORTED and the gate falls to a person every time, at every knob.
	//
	// Measured on TALLY-6: the lead answered the other two criteria on the
	// artifact's own lines and then wrote of this one, correctly, "the task was
	// not given to me". A criterion nothing can satisfy is worse than no
	// criterion, because it reads as a judgement about the artifact.
	Statement fsm.Statement
}

// writeStatement puts the task's own words in front of the judgement.
//
// Marked as the task rather than merged into the artifact, because the whole
// point of a criterion like "every acceptance criterion in the task appears as
// an obligation" is comparing two documents. Blurring them would leave the
// artifact vouching for itself, which is what rule 1 forbids.
func writeStatement(b *strings.Builder, statement fsm.Statement) {
	if statement.Description == "" && statement.Design == "" && statement.Acceptance == "" {
		return
	}

	b.WriteString("\nThe task this was written from, in the words it was opened with.\n")
	b.WriteString("A criterion that asks about the task is settled against this text,\n")
	b.WriteString("not against what the artifact says the task wanted:\n\n")

	if statement.Description != "" {
		fmt.Fprintf(b, "  About: %s\n", statement.Description)
	}
	if statement.Design != "" {
		fmt.Fprintf(b, "  Approach: %s\n", statement.Design)
	}
	if statement.Acceptance != "" {
		fmt.Fprintf(b, "  Done when: %s\n", statement.Acceptance)
	}
}

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
func JudgingBrief(gate *fsm.GateSpec, evidence Evidence) string {
	var b strings.Builder
	artifact, checkout := evidence.Artifact, evidence.Checkout

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
		// The exception the gate declared, and only the ones it named. Without
		// this the rule above is total, and a gate whose criteria are all
		// judgements about an attached artifact can never be approved by anybody
		// but a person — which is what the shipped gate was, at every autonomy.
		//
		// "Reading" is narrower than it sounds, and the next line is what keeps it
		// so: the artifact's own verdict about itself is still worth nothing. The
		// criterion is settled by what the text *says*, not by what it concludes
		// about whether it is good.
		if len(gate.ReadableJudge) > 0 {
			b.WriteString(`
     These criteria are answered by reading the artifact below, and are
     not held to that rule — the artifact is the evidence for them:
`)
			for _, criterion := range gate.ReadableJudge {
				fmt.Fprintf(&b, "       - %s\n", criterion)
			}
			b.WriteString(`     Rule 1 still holds for them: quote the line that settles each
     one. An artifact saying it satisfies a criterion is not that line.
`)
		}
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

	writeStatement(&b, evidence.Statement)

	if artifact != "" {
		b.WriteString("\nThe artifact:\n\n")
		b.WriteString(artifact)
		b.WriteString("\n")
	} else {
		// Said outright rather than left as an absence. A `confirm` gate carries no
		// payload — only `review-artifact` does — so criteria declared on one arrive
		// with nothing to read, and a model given criteria and no artifact will
		// reach for a verdict anyway. Measured: one opened with APPROVE before
		// working out that it had nothing, then corrected itself.
		//
		// Naming the situation is what turns that into the right answer on the
		// first line instead of the third paragraph.
		b.WriteString(`
There is no artifact attached to this gate. That is not an oversight you
should work around: with nothing to read, every criterion above is
UNSUPPORTED, and the answer is CANNOT-DECIDE. Say so on the first line.
`)
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

// String names the judgement in the vocabulary the brief asked for, so the
// recorded decision reads as the same word the model was told to answer with.
func (j Judgement) String() string {
	switch j {
	case JudgedApprove:
		return "approve"
	case JudgedReject:
		return "reject"
	default:
		return "cannot-decide"
	}
}

// ReadJudgement finds the verdict in what the lead said.
//
// The verdict is the first line, which is what the brief asks for — but an
// approval is only an approval if the working below does not take it back.
//
// That second half was measured, not imagined. Asked to judge a gate whose
// artifact never arrived, a real harness opened with `APPROVE`, worked both
// criteria to UNSUPPORTED, and then wrote: *"Correction to my first line: the
// answer is CANNOT-DECIDE, not APPROVE."* It went on to name the danger itself —
// *"if this gate's harness reads only the first line, it just got a false
// approval from me"*. It was right: this function did exactly that.
//
// So a leading APPROVE is checked against the rest of the reply, and anything
// that retracts it falls to a person. Only the approval is scrutinised, because
// only the approval is the outcome that keeps a person out — a REJECT or a defer
// already ends up in front of somebody, so re-reading the body to second-guess
// them would buy nothing.
//
// The asymmetry is the whole design in one function: the cheap direction is
// asking a person one more time, and the expensive one is approving something
// nobody looked at.
func ReadJudgement(said string) Judgement {
	trimmed := strings.TrimSpace(said)
	first, rest, _ := strings.Cut(trimmed, "\n")
	first = strings.ToUpper(strings.TrimSpace(first))

	// Checked before APPROVE because a reply like "CANNOT-DECIDE: I would approve
	// if..." contains both, and the verdict is the one the model led with.
	switch {
	case strings.HasPrefix(first, "CANNOT-DECIDE"), strings.HasPrefix(first, "CANNOT DECIDE"):
		return JudgedCannotDecide
	case strings.HasPrefix(first, "APPROVE"):
		if retracted(rest) {
			return JudgedCannotDecide
		}
		return JudgedApprove
	case strings.HasPrefix(first, "REJECT"):
		return JudgedReject
	default:
		return JudgedCannotDecide
	}
}

// retracted reports whether the body of a reply takes back a leading approval.
//
// It looks for the model saying it cannot decide, or correcting itself. The
// phrases are what a harness actually wrote rather than a guess at what one
// might, and the check is deliberately generous: a false retraction costs one
// question to a person, while a missed one costs an approval nobody made.
func retracted(body string) bool {
	lowered := strings.ToLower(body)

	for _, phrase := range []string{
		"cannot-decide",
		"cannot decide",
		"correction to my first line",
		"correcting my first line",
		"my first line was wrong",
		"disregard my first line",
	} {
		if strings.Contains(lowered, phrase) {
			return true
		}
	}
	return false
}
