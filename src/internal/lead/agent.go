package lead

import (
	"context"
	"fmt"
	"strings"

	"github.com/brunoomariano/luna/src/internal/fsm"
)

// Autonomy is how far the lead may go before it needs a person.
//
// It is the knob PRD gate-0001 asked for, and RFC-0002 makes it load-bearing
// rather than optional: once the lead is a model, "what does it do when
// something goes wrong" stops being a switch statement and starts being a
// decision somebody has to have bounded in advance.
type Autonomy string

const (
	// AutonomyAsk stops at every failure. The lead reports and waits.
	AutonomyAsk Autonomy = "ask"

	// AutonomyRetry lets the lead spend the retry budget the task already has,
	// and stops when it is gone. This is the default, and it is deliberately the
	// narrowest thing that is still useful: retrying is the one recovery whose
	// bound is already in the state.
	AutonomyRetry Autonomy = "retry"

	// AutonomyDecide lets the lead choose what to do about a failure, within the
	// carve-out ADR-0002 already allows. It never widens to choosing a stage.
	AutonomyDecide Autonomy = "decide"
)

// ParseAutonomy reads a configured value, refusing what it does not know.
//
// A closed set because the failure is asymmetric: a typo that fell back to the
// permissive value would turn a supervised run into an unattended one, and
// nothing would say so.
func ParseAutonomy(name string) (Autonomy, error) {
	switch Autonomy(name) {
	case AutonomyAsk, AutonomyRetry, AutonomyDecide:
		return Autonomy(name), nil
	case "":
		return AutonomyRetry, nil
	default:
		return "", fmt.Errorf("unknown autonomy %q (ask, retry, decide)", name)
	}
}

// Agent is the lead as a model rather than a loop.
//
// It exists so that a person can talk to the thing running their task, which is
// the whole reason RFC-0002 makes this change. Everything else about it is
// shaped against the risk that introduces: a model that can be told what is
// happening can also decide what happens next, and that is the one thing it must
// not do (INV-core-1).
//
// The defence is not the brief. It is that the lead is never given a choice to
// make on the happy path — `luna next` returns an order, the lead executes it and
// reports, and the next order comes from the FSM having recorded the result
// (ADR-0052). The brief below describes that arrangement; it does not create it.
type Agent struct {
	// Ask sends the lead a message and returns what it said. It is the whole
	// boundary between Luna and the model, and it is an interface because Luna
	// hosts no model of its own (ADR-0043).
	Ask func(ctx context.Context, prompt string) (string, error)

	// Autonomy bounds what it may do about a failure.
	Autonomy Autonomy
}

// Brief is what the lead is told about being the lead.
//
// It is written as a description of a mechanism rather than a list of
// prohibitions, and that is deliberate. "Do not skip stages" invites a model to
// weigh whether this is one of the times; "the order you are given is the only
// stage that exists for you" leaves nothing to weigh. The parts that actually
// enforce this are the closed order and the fact that Luna records the
// transition — the brief exists so the lead understands the shape it is in
// rather than fighting it.
func Brief(autonomy Autonomy) string {
	var b strings.Builder

	b.WriteString(`You conduct one task through Luna.

You do not decide what happens next. Luna does. Your loop is:

  1. run "luna next <task> --json" — it returns an order
  2. carry out exactly that order
  3. run "luna done <task> --delivered <what it produced> --commit <sha>"
  4. go back to 1

The order names one stage, one role, one worktree and one base commit. It is
the only stage that exists for you. There is no list of what comes after it,
and asking for one is not how this works — if you want to see the whole flow
for a person you are talking to, "luna status <task>" is the command, and what
it shows you is for telling them, never for working ahead.

You never run two stages because they looked small. You never skip a stage
because it looked unnecessary. If a stage looks wrong, that is worth saying to
the person — and then you carry out the order anyway, because you may be
wrong and Luna's record is what everyone else reads.

The agent you start does the work. You do not do it yourself: you are the
conductor, and a conductor that picks up an instrument has stopped conducting.

When a person asks you something, answer them. That is why you are a model
and not a loop.
`)

	b.WriteString("\nWhen a stage fails:\n\n")
	switch autonomy {
	case AutonomyAsk:
		b.WriteString(`  Stop and tell the person what failed and what you would suggest.
  Do not retry. Do not work around it. Wait.
`)
	case AutonomyDecide:
		b.WriteString(`  Decide what to do about it — retry it, or stop and escalate. That
  judgement is yours, and it is the only judgement that is. It is about
  the failure, never about which stage comes next.

  Tell the person what you decided and why.
`)
	default:
		b.WriteString(`  Retry it, up to the budget the task carries. When the budget is
  spent, stop and tell the person.

  You do not work around a failure. Changing the approach because
  something did not work is a decision about the task, and that is not
  yours to make.
`)
	}

	return b.String()
}

// Conduct drives one task by talking to the lead.
//
// The loop is here rather than in the model, and that is the point: Luna asks
// for the order, hands it over, and records what came back. The lead's
// contribution is carrying out the order and explaining itself to a person — not
// deciding when the loop ends.
//
// A lead that answers with something Luna did not ask for is not corrected into
// obedience; the loop simply never acts on it. There is no path from anything
// the model says to a stage transition, which is what makes the guarantee
// structural rather than a matter of the brief being persuasive.
func (a *Agent) Conduct(ctx context.Context, order fsm.Order) (string, error) {
	if a.Ask == nil {
		return "", fmt.Errorf("no lead configured: Luna hosts no model of its own (ADR-0043)")
	}

	autonomy := a.Autonomy
	if autonomy == "" {
		autonomy = AutonomyRetry
	}

	said, err := a.Ask(ctx, Brief(autonomy)+"\n\nYour order:\n\n"+order.Text())
	if err != nil {
		return "", fmt.Errorf("the lead did not answer: %w", err)
	}
	return said, nil
}
