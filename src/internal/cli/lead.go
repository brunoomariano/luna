package cli

import (
	"context"
	"errors"
	"fmt"

	"github.com/brunoomariano/luna/src/internal/fsm"
	"github.com/brunoomariano/luna/src/internal/lead"
	"github.com/brunoomariano/luna/src/internal/node"
)

// leadCommand hands a task to the lead agent.
//
// This is `luna run` with the loop moved: instead of Go deciding when to call
// the node, Luna hands the lead an order and the lead carries it out. What did
// not move is which order — that is still `fsm.NextOrder`, and the lead never
// sees a choice.
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
	// failure. The three names this flag used to take are gone rather than
	// aliased: a flag meaning "knob 5" would hide the gate consequences of the
	// value it set, authorising the lead to judge gates up to criticality 5
	// without the word "gate" appearing anywhere.
	knob, err := fsm.ParseKnob(flags["autonomy"])
	if err != nil {
		return fmt.Errorf("%w: %w", ErrUsage, err)
	}

	if env.Lead == nil {
		return errors.New("no lead is configured: Luna hosts no model of its own, so " +
			"`luna lead` needs one wired in. `luna run` drives the same " +
			"flow without a model")
	}

	// The gate on the way into a stage goes through the same knob-aware path
	// `luna run` uses, rather than a second copy of it. The knob it consults is
	// the task's own, from the state where `luna autonomy` put it.
	//
	// So `--autonomy` here governs what the lead may decide about a *failure* and
	// not which gates it may answer, which is a seam worth naming rather than
	// leaving to be discovered: a flag and a recorded setting sharing one word.
	// The recorded one wins for gates because a gate decision is history — it is
	// replayed as a fact, and a flag on one invocation must not rewrite how a
	// past run reads.
	entering := leadFor(env, ".")

	// The stage budget, not the question timeout: conducting a stage means
	// starting an agent and waiting for it to work, which is the shape
	// `turn_budget` describes. The default is hours; `Ask`'s two minutes are for
	// a model answering a question.
	conductor := &lead.Agent{Ask: env.Lead, Knob: knob, Budget: env.profiles().Turn()}

	return conductTask(env, id, conductor, entering)
}

// landingFor is how a finished task's branch gets pointed, with the injected one
// winning so a test can watch without a repository.
func landingFor(env Env, repo string) func(context.Context, string, string) error {
	if env.Land != nil {
		return env.Land
	}
	return func(ctx context.Context, taskID, commit string) error {
		return node.Land(ctx, repo, taskID, commit)
	}
}

// leadFor builds the lead `luna lead` conducts with.
//
// Extracted so a test can assert on the same construction the command uses. Two
// of these fields were missing here while `luna run` had them, and the absence
// was invisible: a task finished, its work stayed on the stage branches, and the
// warning that would have said so had nowhere to go.
func leadFor(env Env, repo string) *lead.Lead {
	return &lead.Lead{
		Store:     env.Store,
		Ask:       env.Lead,
		CheckGate: checkGateWith(env.Store, repo),
		// The artifact itself rather than the evidence line naming it: a gate that
		// asks the lead to judge a contract has to hand it the contract.
		Artifact: func(taskID, artifact string) (string, bool) {
			blob, err := env.Store.LatestBlob(taskID, "", artifact)
			if err != nil {
				return "", false
			}
			return string(blob.Body), true
		},

		// `done` means ready to integrate, and this is what makes it true. Absent
		// here while `luna run` had it, so a task conducted by the lead finished
		// with its work reachable only through the stage branches — and `luna
		// status` printed the landing ref it had not created. Measured on TALLY-7.
		Land: landingFor(env, repo),

		// And somewhere for that to be said. Without it the landing could fail
		// and the run would end clean, which is the silent failure INV-5 forbids —
		// the warning existed and had nowhere to go.
		Warn: func(format string, args ...any) {
			fmt.Fprintf(env.Err, format+"\n", args...)
		},
	}
}

// reportEnding says why the loop stopped, and what the lead made of it.
//
// The reasoning is printed as well as recorded because a run that ends at a gate
// otherwise puts one line on the terminal and takes the analysis with it —
// measured on TALLY-6, where the lead found a real contradiction in a contract
// and the whole visible output was `wait: review the plan and its contract`.
func reportEnding(env Env, order fsm.Order, state fsm.TaskState) {
	fmt.Fprintf(env.Out, "%s: %s\n", order.Kind, order.Reason)

	if state.Gate == nil || state.Gate.Reasoning == "" {
		return
	}
	fmt.Fprintf(env.Out, "\nthe lead judged this %s:\n\n%s\n",
		state.Gate.Judged, state.Gate.Reasoning)
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
// third failure blocks — and a task that stops for any reason returns an order
// that is not OrderRun, which ends the loop above. A ceiling nothing can reach is
// one that gets trusted without ever having held.
func conductTask(env Env, id string, conductor *lead.Agent, entering *lead.Lead) error {
	for {
		// The stage is opened before the order is read, because `next` is a read
		// and `done` reports a finish — the transition between them is the
		// engine's, and without it the task never leaves `ready` and every `done`
		// answers "no running stage to finish". A gate on the way in is answered
		// here, through the knob.
		if err := entering.Enter(context.Background(), id); err != nil {
			return err
		}

		state, err := env.replay(id)
		if err != nil {
			return err
		}

		flow, err := env.flowOf(id)
		if err != nil {
			return err
		}

		order, err := fsm.NextOrder(state, flow, env.profiles().Roles)
		if err != nil {
			return err
		}

		// Three of the four kinds end the loop, and none of them is the lead's to
		// push past. A gate is waiting on a person, a block is waiting on a
		// person, and done is done.
		if order.Kind != fsm.OrderRun {
			// A finished task's branch is pointed here, because this loop is not
			// `Lead.Run` and does not go through its ending. The field was wired on
			// this path and nothing called it: TALLY-8 finished six stages and left
			// its work reachable only through the stage branches.
			entering.PointBranchIfDone(context.Background(), state)
			reportEnding(env, order, state)
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
