package lead

import (
	"fmt"
	"strings"

	"github.com/brunoomariano/luna/src/internal/fsm"
)

// CeilingBrief is what the lead is told about a loop that stopped converging.
//
// It carries the counters rather than a summary, because the counters are the
// history and the decision turns on reading them: three rounds with no functional
// change is a different situation from three rounds that each moved something,
// and only the numbers separate them (ADR-0023, ADR-0063).
//
// It asks for one of two answers and nothing else. The lead is not being asked
// what to do about the work — that would be choosing a stage, which it may never
// do (INV-core-1). It is being asked which of two endings this loop has earned.
func CeilingBrief(loop fsm.LoopCounters) string {
	var b strings.Builder

	b.WriteString(`A loop in this task hit one of its ceilings. You decide what that
means, and there are exactly two answers.

The history:

`)
	fmt.Fprintf(&b, "  rounds:      %d\n", loop.Rounds)
	fmt.Fprintf(&b, "  no progress: %d consecutive round(s) that changed nothing\n", loop.NoProgress)
	fmt.Fprintf(&b, "  oscillation: %d consecutive round(s) returning to a stage already visited\n",
		loop.Oscillation)

	if len(loop.Visited) > 0 {
		b.WriteString("  path:        ")
		for i, stage := range loop.Visited {
			if i > 0 {
				b.WriteString(" → ")
			}
			b.WriteString(string(stage))
		}
		b.WriteString("\n")
	}
	if loop.LastProgress != "" {
		fmt.Fprintf(&b, "  last change: %s\n", loop.LastProgress)
	}

	b.WriteString(`
Answer with one word on the first line:

  BLOCK — this loop was not going to converge. Stop the task and notify.
          Rounds that changed nothing, or that keep returning to the same
          stage, are this.

  ASK   — this loop was making progress and ran out of room. A person
          should see it and decide, with the history above in view.

Then say why in a sentence or two.

You are not deciding what happens to the work, and you may not choose a
stage. You are deciding which of those two endings this loop earned.
`)

	return b.String()
}

// CeilingVerdict is what the lead concluded about a spent ceiling.
type CeilingVerdict int

const (
	// CeilingBlock stops the task and notifies. It is the zero value, and that is
	// deliberate: anything unreadable ends here, and a run that cannot conclude
	// must not carry on looping (INV-core-8).
	CeilingBlock CeilingVerdict = iota

	// CeilingAsk puts the loop in front of a person, with the history in view.
	CeilingAsk
)

// ReadCeiling finds the verdict in what the lead said.
//
// The first line only, the same shape ReadJudgement uses and for the same reason:
// the word "block" appears in reasoning about blocking, and a parser scanning the
// whole reply would find it in a paragraph arguing the opposite.
//
// Anything it cannot read is BLOCK. The asymmetry is inverted from a gate's on
// purpose — there, the cheap direction is asking a person; here, asking is what
// costs, because a loop that nobody assessed and nobody stops is the one that
// burns tokens forever.
func ReadCeiling(said string) CeilingVerdict {
	first, _, _ := strings.Cut(strings.TrimSpace(said), "\n")

	if strings.HasPrefix(strings.ToUpper(strings.TrimSpace(first)), "ASK") {
		return CeilingAsk
	}
	return CeilingBlock
}
