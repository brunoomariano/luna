package cli

import (
	"context"
	"errors"
	"fmt"

	"github.com/brunoomariano/luna/src/internal/fsm"
	"github.com/brunoomariano/luna/src/internal/lead"
)

// leadCommand hands a task to the lead agent.
//
// This is `luna run` with the loop moved: instead of Go deciding when to call
// the node, Luna hands the lead an order and the lead carries it out. What did
// not move is which order — that is still `fsm.NextOrder`, and the lead never
// sees a choice (ADR-0052, ADR-0056).
//
// The loop lives here rather than in the model. Luna asks for the order, gives
// it over, records what came back, and asks again. A lead that answered with a
// decision about the flow would find nothing listening: there is no path from
// what it says to a transition.
func leadCommand(env Env, args []string) error {
	if len(args) == 0 {
		return fmt.Errorf("%w: lead needs a task id", ErrUsage)
	}
	id := args[0]

	flags, err := parseFlags(args[1:])
	if err != nil {
		return err
	}

	for name := range flags {
		if name != "autonomy" {
			return fmt.Errorf("%w: unknown flag --%s", ErrUsage, name)
		}
	}

	// The knob is the only control, and its state is what decides behaviour on a
	// failure (RFC-0006). The three names this flag used to take are gone rather
	// than aliased: a flag meaning "knob 5" would hide the gate consequences of
	// the value it set, authorising the lead to judge gates up to criticality 5
	// without the word "gate" appearing anywhere.
	knob, err := fsm.ParseKnob(flags["autonomy"])
	if err != nil {
		return fmt.Errorf("%w: %w", ErrUsage, err)
	}

	if env.Lead == nil {
		return errors.New("no lead is configured: Luna hosts no model of its own, so " +
			"`luna lead` needs one wired in (ADR-0043). `luna run` drives the same " +
			"flow without a model")
	}

	return conductTask(env, id, &lead.Agent{Ask: env.Lead, Knob: knob})
}

// conductTask is the loop: ask Luna for the order, give it to the lead, check
// the task actually moved.
//
// Split from the command so the setup and the loop are separately readable —
// and because this is the part worth reading. Everything the design promises
// about the lead is visible in these thirty lines.
//
// There is no turn cap, and its absence was measured rather than assumed. A cap
// was written first, on the reasoning that a model can talk itself into a loop;
// no test could reach it, because every way out of this loop is the engine's.
// The lead cannot keep a stage open — failing it spends the retry budget and the
// third failure blocks (ADR-0011) — and a task that stops for any reason returns
// an order that is not OrderRun, which ends the loop above. A ceiling nothing can
// reach is one that gets trusted without ever having held.
func conductTask(env Env, id string, conductor *lead.Agent) error {
	for {
		state, err := env.replay(id)
		if err != nil {
			return err
		}

		order, err := fsm.NextOrder(state, fsm.DefaultFlow(), env.profiles().Roles)
		if err != nil {
			return err
		}

		// Three of the four kinds end the loop, and none of them is the lead's to
		// push past. A gate is waiting on a person, a block is waiting on a
		// person, and done is done.
		if order.Kind != fsm.OrderRun {
			fmt.Fprintf(env.Out, "%s: %s\n", order.Kind, order.Reason)
			return nil
		}

		said, err := conductor.Conduct(context.Background(), order)
		if err != nil {
			return err
		}
		fmt.Fprintf(env.Out, "[%s] %s\n", order.Stage, said)

		// What the lead said is printed and nothing else. Whether the stage
		// closed is read back from the registry on the next pass, because the
		// lead reporting through `luna done` is what records it — and a lead that
		// merely claims to have finished has not.
		after, err := env.replay(id)
		if err != nil {
			return err
		}
		// This is what keeps the loop finite, and it is a stronger guard than a
		// turn count: a lead that does nothing stops the run on its first turn
		// rather than a hundred turns later.
		if after.Seq == state.Seq {
			return fmt.Errorf("the lead returned on %s without the task moving: "+
				"it has to report through `luna done` (or the stage has to fail) for "+
				"anything to happen", order.Stage)
		}
	}
}
