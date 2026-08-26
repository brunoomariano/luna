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
// The loop is the lead's rather than Go's: instead of Go deciding when to call
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

	if err := onlyLeadFlags(flags); err != nil {
		return err
	}

	repo := flags["repo"]
	if repo == "" {
		repo = "."
	}

	if _, dry := flags["dry-run"]; dry {
		return dryRun(env, id, repo, flags["agent"])
	}

	// Solo: one agent carries the task end to end. Luna advances and starts that
	// agent per stage, in the sandbox and in a worktree, under the one role a solo
	// run has — so there is never a conductor and a worker alive at once, and the
	// session and the worktree survive from stage to stage because the role does.
	//
	// The pack is the other mode and it is `luna fleet run`: there the lead
	// conducts from outside and the flow's declared roles do the work.
	return soloRun(env, id, repo, flags["agent"])
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
// of these fields were missing here while the other surface had them, and the
// absence was invisible: a task finished, its work stayed on the stage branches,
// and the warning that would have said so had nowhere to go.
//
// The flow is passed rather than defaulted for the same class of reason. A Lead
// built without one replays against this build's default, so every task on any
// other flow was refused by its own fingerprint before it could start — the hole
// a test caught on the hand-driven opener and nothing watched here.
func leadFor(env Env, repo string, flow []fsm.Stage) *lead.Lead {
	return &lead.Lead{
		Store:     env.Store,
		Flow:      flow,
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
		// here while the driving loop had it, so a task conducted by the lead finished
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

	// A block is the ending INV-5 says has to be *notified*, and printing is not
	// notifying: the run that most needs it is the unattended one, where nobody
	// is reading the terminal. This lived only on the ending of the loop Luna used
	// to drive with, so when that surface went and the fleet moved onto this one,
	// every nightly block would have gone out in silence.
	if state.Status == fsm.StatusBlocked {
		fmt.Fprintf(env.Out, "  resume it with `luna unblock %s` once it is dealt with\n", state.ID)
		notifyBlocked(env, state.ID, state.Blocked)
	}

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
func conductTask(env Env, id string, conductor *lead.Agent, entering *lead.Lead) (fsm.TaskState, error) {
	for {
		// The stage is opened before the order is read, because `next` is a read
		// and `done` reports a finish — the transition between them is the
		// engine's, and without it the task never leaves `ready` and every `done`
		// answers "no running stage to finish". A gate on the way in is answered
		// here, through the knob.
		if err := entering.Enter(context.Background(), id); err != nil {
			return fsm.TaskState{}, err
		}

		state, order, err := orderFor(env, id)
		if err != nil {
			return fsm.TaskState{}, err
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
			return state, nil
		}

		// A task created as a simulation recorded checks that never ran, so it
		// cannot be continued for real. Refused after the ending branch rather
		// than before the loop: a simulated task that already finished still has
		// to be reported and its branch pointed, and it is only the order to
		// actually work that must not happen.
		if err := agreeOnSimulation(id, state, false); err != nil {
			return state, err
		}

		said, err := conductor.Conduct(context.Background(), order)
		if err != nil {
			return state, err
		}
		fmt.Fprintf(env.Out, "[%s] %s\n", order.Stage, said)

		// What the lead said is printed and nothing else. Whether the stage
		// closed is read back from the registry on the next pass, because the
		// lead reporting through `luna done` is what records it — and a lead that
		// merely claims to have finished has not.
		after, err := env.replay(id)
		if err != nil {
			return fsm.TaskState{}, err
		}
		// This is what keeps the loop finite, and it is a stronger guard than a
		// turn count: a lead that does nothing stops the run on its first turn
		// rather than a hundred turns later.
		if after.Seq == state.Seq {
			return after, fmt.Errorf("the lead returned on %s without the task moving: "+
				"it has to report through `luna done` (or the stage has to fail) for "+
				"anything to happen", order.Stage)
		}
	}
}

// reportDryEnding says where a dry run landed.
//
// Its own function rather than reportEnding's, because that one takes an order
// and notifies a block — and a block a dry run reached is a fact about the flow,
// not about a task anybody has to be woken for.
func reportDryEnding(env Env, id string, state fsm.TaskState) {
	switch state.Status {
	case fsm.StatusDone:
		fmt.Fprintf(env.Out, "%s finished\n", id)
	case fsm.StatusAwaitingGate:
		reason := ""
		if state.Gate != nil {
			reason = state.Gate.Reason
		}
		fmt.Fprintf(env.Out, "%s is waiting at %s: %s\n", id, state.Stage, reason)
		fmt.Fprintf(env.Out, "  answer it with `luna gate show %s`\n", id)
	case fsm.StatusBlocked:
		fmt.Fprintf(env.Out, "%s is blocked: %s\n", id, state.Blocked)
		fmt.Fprintf(env.Out, "  resume it with `luna unblock %s` once it is dealt with\n", id)
	default:
		fmt.Fprintf(env.Out, "%s stopped at %s (%s)\n", id, state.Stage, state.Status)
	}
}

// onlyLeadFlags refuses a flag this command does not have, rather than ignoring
// it. A typo that runs is worse than one that stops: the run behaves as though
// nobody had asked for anything.
func onlyLeadFlags(flags map[string]string) error {
	for name := range flags {
		switch name {
		case "autonomy", "dry-run", "agent", "repo":
		default:
			return fmt.Errorf("%w: unknown flag --%s", ErrUsage, name)
		}
	}
	return nil
}

// dryRun exercises the flow with no agent, no worktree and no model.
//
// It is the one shape a lead cannot conduct — there is nobody to conduct with —
// so it takes the node-driven loop, exactly as the fleet's dry run does. That
// keeps it a flag on a mode rather than a third mode: the same task, run free.
func dryRun(env Env, id, repo, agent string) error {
	state, err := driveTask(context.Background(), env, id, runOptions{
		Agent: agent, Repo: repo, Dry: true,
	})
	if err != nil {
		return err
	}
	reportDryEnding(env, id, state)
	return nil
}

// orderFor reads the task, its flow and the order that follows from both.
//
// The three go together everywhere they are needed, and the flow is read per
// pass rather than once: a task's flow is fixed, but replaying against this
// build's default instead of the task's own is the mistake this makes
// impossible to write.
func orderFor(env Env, id string) (fsm.TaskState, fsm.Order, error) {
	state, err := env.replay(id)
	if err != nil {
		return fsm.TaskState{}, fsm.Order{}, err
	}
	flow, err := env.flowOf(id)
	if err != nil {
		return fsm.TaskState{}, fsm.Order{}, err
	}
	order, err := fsm.NextOrder(state, flow, env.profiles().Roles)
	if err != nil {
		return fsm.TaskState{}, fsm.Order{}, err
	}
	return state, order, nil
}

// soloRun is the mode where one agent carries the task from end to end.
//
// Luna advances and starts that agent per stage — sandboxed, in a worktree —
// under the single role `fsm.Solo` collapses the flow onto. One agent alive at a
// time, and the worktree and the session survive from stage to stage because the
// role does.
//
// It is deliberately not the pack's loop with a smaller number. A conductor
// dispatching to one worker is two agents to buy what one can do, and the
// conductor is billed every turn.
func soloRun(env Env, id, repo, agent string) error {
	state, err := env.replay(id)
	if err != nil {
		return err
	}
	if err := agreeOnSimulation(id, state, false); err != nil {
		return err
	}

	flow, err := env.flowOf(id)
	if err != nil {
		return err
	}

	conductor, cleanup, err := conduct(env, runOptions{Agent: agent, Repo: repo},
		state.Profile, fsm.Solo(flow))
	if err != nil {
		return err
	}
	defer cleanup()

	landed, err := conductor.Run(context.Background(), id)
	if err != nil {
		return err
	}
	reportSoloEnding(env, id, landed)
	return nil
}

// reportSoloEnding says where a solo run landed, and notifies a block.
//
// A block is the ending INV-5 says has to be notified, and printing is not
// notifying: the run that most needs it is the unattended one.
func reportSoloEnding(env Env, id string, state fsm.TaskState) {
	switch state.Status {
	case fsm.StatusDone:
		fmt.Fprintf(env.Out, "%s finished\n", id)
	case fsm.StatusAwaitingGate:
		reason := ""
		if state.Gate != nil {
			reason = state.Gate.Reason
		}
		fmt.Fprintf(env.Out, "%s is waiting at %s: %s\n", id, state.Stage, reason)
		fmt.Fprintf(env.Out, "  answer it with `luna gate show %s`\n", id)
		if state.Gate != nil && state.Gate.Reasoning != "" {
			fmt.Fprintf(env.Out, "\n  the lead judged this %s:\n  %s\n",
				state.Gate.Judged, state.Gate.Reasoning)
		}
	case fsm.StatusBlocked:
		fmt.Fprintf(env.Out, "%s is blocked: %s\n", id, state.Blocked)
		fmt.Fprintf(env.Out, "  resume it with `luna unblock %s` once it is dealt with\n", id)
		notifyBlocked(env, id, state.Blocked)
	default:
		fmt.Fprintf(env.Out, "%s stopped at %s (%s)\n", id, state.Stage, state.Status)
	}
}

// packRun is the mode where the lead conducts and the flow's declared roles work.
//
// The lead stays outside the sandbox here, which is the `Ask`/`Run` split holding:
// it reads state and answers questions, and it starts no worktree of its own. What
// writes code is a role agent, contained, one per stage — and each role keeps a
// worktree and a session across the stages it owns, which is what the pack buys
// and a solo run cannot have.
func packRun(env Env, id, repo string, knob fsm.Knob) (fsm.TaskState, error) {
	if env.Lead == nil {
		return fsm.TaskState{}, errors.New("no lead is configured: Luna hosts no " +
			"model of its own, so a pack needs one to conduct it. `luna lead` runs " +
			"the same flow with a single agent and no conductor")
	}

	state, err := env.replay(id)
	if err != nil {
		return fsm.TaskState{}, err
	}

	flow, err := env.flowOf(id)
	if err != nil {
		return fsm.TaskState{}, err
	}

	// The task's own knob unless the caller named one. A flag over a whole pack
	// must not quietly overrule what `luna autonomy` recorded on the task.
	if knob == fsm.KnobAsk {
		knob = state.Knob
	}

	// The stage budget, not the question timeout: conducting a stage means
	// starting an agent and waiting for it to work, which is the shape
	// `turn_budget` describes. `Ask`'s ceiling is for a model answering a question.
	conductor := &lead.Agent{Ask: env.Lead, Knob: knob, Budget: env.profiles().Turn()}

	return conductTask(env, id, conductor, leadFor(env, repo, flow))
}
