package lead

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/brunoomariano/luna/src/internal/fsm"
)

// Autonomy is how far the lead may go before it needs a person.
//
// It is load-bearing rather than optional: once the lead is a model, "what does
// it do when something goes wrong" stops being a switch statement and starts
// being a decision somebody has to have bounded in advance.
type Autonomy string

const (
	// AutonomyAsk stops at every failure. The lead reports and waits.
	AutonomyAsk Autonomy = "ask"

	// AutonomyDecide lets the lead choose what to do about a failure, within the
	// carve-out the hybrid design already allows. It never widens to choosing a
	// stage.
	AutonomyDecide Autonomy = "decide"
)

// The set lost a third value and a parser when the knob absorbed this setting.
//
// `retry` was the old default, and retrying once is now what *every* setting
// does before anything else — so a name for "retries and then stops" described
// the floor rather than a choice. ParseAutonomy went with it: nothing outside
// this package may name an autonomy any more, because naming one was the second
// control the knob exists to remove — and what has no caller is either wired or
// gone.

// Agent is the lead as a model rather than a loop.
//
// It exists so that a person can talk to the thing running their task, which is
// the whole reason for making this change. Everything else about it is shaped
// against the risk that introduces: a model that can be told what is happening
// can also decide what happens next, and that is the one thing it must not do —
// the one thing Luna exists to prevent.
//
// The defence is not the brief. It is that the lead is never given a choice to
// make on the happy path — `luna next` returns an order, the lead executes it and
// reports, and the next order comes from the FSM having recorded the result. The
// brief below describes that arrangement; it does not create it.
type Agent struct {
	// Ask sends the lead a message and returns what it said. It is the whole
	// boundary between Luna and the model, and it is an interface because Luna
	// hosts no model of its own.
	Ask func(ctx context.Context, prompt string) (string, error)

	// Knob is the one control a person sets. What the lead may do about a failure
	// is derived from it, never configured beside it — two settings both called
	// autonomy is a worse product than one.
	Knob fsm.Knob

	// Budget is how long the lead has to conduct one stage.
	//
	// Not the question timeout, which is what this used to get by default and is
	// the wrong shape: `Ask` bounds a model answering a question, and two minutes
	// is right for judging a gate. Conducting a stage means starting an agent and
	// waiting for it to work, which is what `turn_budget` already describes and
	// why it defaults to hours. Measured on TALLY-4, where the run died with
	// "claude did not answer within 2m0s" while the agent was still working.
	//
	// Zero leaves it to the caller's context, which is the same thing the node
	// layer does with an unset budget.
	Budget time.Duration
}

// Autonomy is what this agent's knob means for a failure.
//
// Derived at the moment it is needed rather than stored, so there is no second
// field that could disagree with the knob. The knob changes in flight; a copy
// taken at construction would go stale the moment it did.
func (a *Agent) Autonomy() Autonomy {
	return Autonomy(a.Knob.Autonomy())
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
  3. run "luna done <task> --delivered <names> --commit <sha>"
  4. go back to 1

The <names> are the order's own "produces" list, comma-separated and verbatim.
It is a list of artifact names and not a description of the work: "briefing,kind"
is the answer, and a sentence about what you wrote is not. Luna checks the
delivery against the contract, so a name it did not ask for closes nothing.

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

	// Retrying once is what every setting does first, so it is stated once, above
	// the branch, rather than being a thing the strictest setting forbids. It is
	// the recovery whose bound is already in the state and it needs no judgement
	// to be safe — and a person woken for a failure a second attempt would have
	// cleared is attention spent for nothing.
	b.WriteString(`
When a stage fails:

  Retry it once. That is not a decision — it is what every setting does,
  because a failure that clears on a second attempt was never worth
  anyone's attention.

When the retry is spent:

`)
	if autonomy == AutonomyDecide {
		b.WriteString(`  Decide what to do about it — retry within the budget the task carries,
  or stop and escalate. That judgement is yours, and it is the only
  judgement about the flow that is. It is about the failure, never about
  which stage comes next.

  Tell the person what you decided and why.
`)
	} else {
		b.WriteString(`  Stop and tell the person what failed and what you would suggest.
  Do not work around it. Changing the approach because something did not
  work is a decision about the task, and that is not yours to make.
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
		return "", fmt.Errorf("no lead configured: Luna hosts no model of its own")
	}

	// No default to apply: the knob's zero value is KnobAsk, the most supervised
	// setting, and Autonomy derives from it. The old empty-means-retry fallback
	// was a second place deciding the same thing.
	if a.Budget > 0 {
		var stop context.CancelFunc
		ctx, stop = context.WithTimeout(ctx, a.Budget)
		defer stop()
	}

	said, err := a.Ask(ctx, Brief(a.Autonomy())+"\n\nYour order:\n\n"+order.Text())
	if err != nil {
		return "", fmt.Errorf("the lead did not answer: %w", err)
	}
	return said, nil
}
