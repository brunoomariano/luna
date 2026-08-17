package cli

import (
	"context"
	"errors"
	"fmt"

	"github.com/brunoomariano/luna/src/internal/fsm"
	"github.com/brunoomariano/luna/src/internal/herdr"
	"github.com/brunoomariano/luna/src/internal/lead"
	"github.com/brunoomariano/luna/src/internal/node"
	"github.com/brunoomariano/luna/src/internal/store"
)

// runTask drives a task until it needs a person or reaches the end.
//
// This is where the two systems meet: herdr hosts the agent, Luna decides and
// verifies (ADR-0027). Everything it assembles is an implementation of an
// interface the lead already declared, so none of the wiring reaches the engine.
func runTaskCommand(env Env, args []string) error {
	if len(args) == 0 {
		return fmt.Errorf("%w: run needs a task id", ErrUsage)
	}
	id := args[0]

	opts, err := parseRunOptions(args[1:])
	if err != nil {
		return err
	}

	events, err := env.Store.Events(id)
	if err != nil {
		return err
	}
	if len(events) == 0 {
		return fmt.Errorf("no task %q — create it with `luna task new %s`", id, id)
	}

	// The budget comes from the profile this task was created under, read from
	// its own log. Taking it from a fixed profile would give a nightly run the
	// supervised timeout — backwards, since the run with nobody watching is the
	// one whose watchdog is its only net (ADR-0034).
	state, err := env.Store.Replay(id, fsm.DefaultFlow())
	if err != nil {
		return err
	}

	conductor, cleanup, err := conduct(env, opts, state.Profile)
	if err != nil {
		return err
	}
	defer cleanup()

	state, err = conductor.Run(context.Background(), id)
	if err != nil {
		// Losing herdr is not a task failure, and the message says which it was
		// so nobody goes looking for a bug in the flow (ADR-0033).
		if errors.Is(err, herdr.ErrGone) {
			return fmt.Errorf("herdr went away while %s was running: %w", id, err)
		}
		return err
	}

	return reportRun(env, id, state)
}

// runOptions is how this run is driven.
type runOptions struct {
	// Agent overrides every role's agent. Empty means each role decides, which is
	// the ordinary case (ADR-0040).
	Agent string

	// Socket overrides where herdr listens; empty resolves the usual way.
	Socket string

	// Repo is the checkout worktrees are cut from.
	Repo string

	// Dry runs with no herdr and no agent: the engine, the log and the gates
	// exercised end to end. It is what tells a broken flow apart from a broken
	// integration.
	Dry bool
}

// parseRunOptions reads the flags `luna run` accepts.
func parseRunOptions(args []string) (runOptions, error) {
	opts := runOptions{Repo: "."}

	flags, err := parseFlags(args)
	if err != nil {
		return opts, err
	}

	for name, value := range flags {
		switch name {
		case "agent":
			opts.Agent = value
		case "socket":
			opts.Socket = value
		case "repo":
			opts.Repo = value
		case "dry-run":
			opts.Dry = true
		default:
			return opts, fmt.Errorf("%w: unknown flag --%s", ErrUsage, name)
		}
	}
	return opts, nil
}

// conduct assembles the lead for this run.
//
// The node is chosen here and nowhere else: swapping herdr for something else is
// one more branch in this function, not a change to the lead or the engine
// (ADR-0030).
func conduct(env Env, opts runOptions, profile fsm.Profile) (*lead.Lead, func(), error) {
	cfg := env.profiles()
	// The judge is what makes the retry budget real: without one the lead blocks on
	// the first failure and ADR-0011's budget is never spent (ADR-0051). This one
	// carries no model — it reads the budget the task already has.
	conductor := &lead.Lead{
		Store: env.Store, Judge: lead.BudgetJudge{},
		// The mechanical half of a gate: what the task declared, run over what it
		// delivered (RFC-0006).
		CheckGate: checkGateWith(env.Store, opts.Repo),
		// The judgement half, when the knob reaches a gate and a model is wired
		// in. Nil is the ordinary case for `luna run` — and then a gate the knob
		// reached still goes to a person, because authority to judge is not a
		// judgement (ADR-0043).
		Ask: env.Lead,
		// `done` means ready to integrate, and this is what makes it true: the
		// task's own branch is pointed at what it delivered (ADR-0062).
		Land: func(ctx context.Context, taskID, commit string) error {
			return node.Land(ctx, opts.Repo, taskID, commit)
		},
		Warn: func(format string, args ...any) {
			fmt.Fprintf(env.Err, format+"\n", args...)
		},
	}

	if opts.Dry {
		conductor.Node = dryNode{}
		return conductor, func() {}, nil
	}

	client, err := herdr.Dial(opts.Socket)
	if err != nil {
		return nil, nil, fmt.Errorf("%w — is herdr running?", err)
	}

	conductor.Node = &herdr.Node{
		Runner: herdr.NewRunner(client, opts.Repo, cfg.Budgets(profile).Resolve().Turn),
		// The stage's role decides which agent runs it (ADR-0040). --agent
		// overrides every role, which is what makes a run reproducible against one
		// harness while the roles are still being tuned.
		Roles: rolesFor(cfg, opts.Agent),
		Prove: func(commit string) herdr.Prover {
			// Cut from the repository at the delivered commit, executed by Luna
			// rather than through a pane (ADR-0035).
			//
			// The repository and not the stage's worktree: the tree is removed when
			// the stage ends (ADR-0055), so a retry verified against a directory
			// nobody had. This is the same `Dir: repo` the gate checks below already
			// use — the two halves now agree about what outlives a stage.
			return node.Shell{Dir: opts.Repo, Commit: commit}
		},
		// A worktree that would not go away does not fail the stage, but it does
		// accumulate: a checkout left behind on every run eventually fills a disk,
		// and the first anyone would hear of it is that.
		Warn: func(format string, args ...any) {
			fmt.Fprintf(env.Err, format+"\n", args...)
		},
		// Whether a sandbox is holding the boundary, which decides how much the
		// agent is trusted with (INV-core-7). Luna does not contain anything
		// itself; it asks whether something else is.
		Contained: node.Contained,
		// What the stage committed, which is what the next one branches from.
		Delivered: node.Handover,
	}
	return conductor, func() { _ = client.Close() }, nil
}

// checkGateWith runs the commands a task declared for one gate, over what it
// delivered.
//
// This is the seam between the two halves of RFC-0006: the task's log says which
// commands answer a gate, the node layer runs them in a checkout of the delivered
// commit, and the lead gets a verdict rather than a shell.
//
// The declaration used to be read from the registry's metadata (ADR-0054); it is
// replayed from the task's own log now, which is what let the registry go
// (ADR-0067). Nothing else about the seam changed — the outcome the lead sees is
// the same three-way answer it always was.
func checkGateWith(s *store.Store, repo string) func(context.Context, string, fsm.GateKind) fsm.GateChecksOutcome {
	return func(ctx context.Context, taskID string, gate fsm.GateKind) fsm.GateChecksOutcome {
		state, err := s.Replay(taskID, fsm.DefaultFlow())
		if err != nil {
			// The log could not be read, so nothing is known about what should have
			// run. That is not "no checks declared" — it is not knowing, and the two
			// must not collapse: one approves a gate and the other asks a person.
			return fsm.GateChecksOutcome{Unrunnable: true}
		}

		checks, declared := state.GateChecks[gate]
		if !declared || len(checks) == 0 {
			// Nothing declared, or declared empty on purpose: either way there is no
			// command to run, so the gate goes to the judgement half.
			return fsm.GateChecksOutcome{}
		}

		verdict := node.Shell{Dir: repo}.CheckGate(ctx, checks)
		return fsm.GateChecksOutcome{
			Passed:     verdict.Approves(),
			Rejected:   verdict.Rejected(),
			Unrunnable: verdict.Unrunnable != nil,
		}
	}
}

// rolesFor resolves roles from the config, optionally forcing one agent.
//
// The override exists for the same reason `--dry-run` does: pinning every stage
// to one harness makes a run reproducible while the roles are still being tuned.
// It changes which agent runs, never which role the stage names — so the flow and
// the log stay honest about who was supposed to do what.
func rolesFor(cfg Config, override string) func(fsm.RoleName) (fsm.Role, bool) {
	return func(name fsm.RoleName) (fsm.Role, bool) {
		role, ok := cfg.Role(name)
		if ok && override != "" {
			role.Agent = override
		}
		return role, ok
	}
}

// dryNode delivers whatever the contract asks for, without running anything.
//
// It exists so the machine can be exercised without the world: the flow, the
// contract checks, the gates and the replay all run, and no agent starts.
//
// The evidence it produces claims the scope the contract declared, and says in
// its detail that nothing ran. That is a deliberate lie of scope with the truth
// beside it, and it is confined to this type: recording `existence` instead
// would be honest but would stop every stage whose contract declares a command,
// which is exactly the machinery a dry run exists to exercise. Nothing outside
// `--dry` may construct evidence this way — a node that cannot prove something
// says so and lets the stage block (ADR-0028).
type dryNode struct{}

func (dryNode) Run(_ context.Context, state fsm.TaskState, stage fsm.Stage) (lead.Result, error) {
	owed := append(append([]fsm.Artifact{}, stage.Produces...), stage.ProducesForHuman...)

	evidence := make(map[fsm.Artifact]fsm.Evidence, len(owed))
	for _, artifact := range owed {
		verifier := fsm.VerifierFor(stage, artifact)
		evidence[artifact] = fsm.Evidence{
			Scope:      verifier.Proves(),
			Verdict:    fsm.VerdictPassed,
			Command:    verifier.Describe(),
			Detail:     "dry run: nothing was executed",
			RecordedAt: state.Seq,
		}
	}
	return lead.Result{Delivered: owed, Evidence: evidence}, nil
}

// reportRun prints where the task stopped and what to do about it.
func reportRun(env Env, id string, state fsm.TaskState) error {
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
		notifyBlocked(env, id, state.Blocked)
	default:
		fmt.Fprintf(env.Out, "%s stopped at %s (%s)\n", id, state.Stage, state.Status)
	}
	return nil
}

// notifyBlocked tells a person a task stopped, which is what INV-core-8 means by
// a block being *notified*.
//
// Printing to stdout is not notifying: the run that most needs it is the
// unattended one, where nobody is reading the terminal. This is the third ending
// the invariant names, and it was the one with nothing behind it.
//
// A notification that fails is reported and does not fail the run. The block is
// the fact worth keeping; the banner is only how it was announced, and losing the
// announcement must not lose the task.
func notifyBlocked(env Env, id, reason string) {
	if env.Notify == nil {
		return
	}
	if err := env.Notify(context.Background(), id, reason); err != nil {
		fmt.Fprintf(env.Err, "  (could not notify: %v)\n", err)
	}
}

// unblockCommand clears a block so the task can be run again.
//
// Every path into a block ends here: a stall, a lost herdr, a stage whose
// verification failed. The person decides the situation is dealt with, and the
// retry budget resets because the block was the escalation.
func unblockCommand(env Env, args []string) error {
	if len(args) == 0 {
		return fmt.Errorf("%w: unblock needs a task id", ErrUsage)
	}
	id := args[0]

	// Replaying an empty log yields a healthy zero state, so a task that was
	// never created would be reported as "ready, not blocked" — which tells the
	// person it exists. The other commands guard this the same way.
	events, err := env.Store.Events(id)
	if err != nil {
		return err
	}
	if len(events) == 0 {
		return fmt.Errorf("no task %q", id)
	}

	state, err := env.Store.Replay(id, fsm.DefaultFlow())
	if err != nil {
		return err
	}
	if state.Status != fsm.StatusBlocked {
		return fmt.Errorf("%s is %s, not blocked", id, state.Status)
	}

	// Conditional on the log not having moved since the status was read: whoever
	// clears a block is rarely the process that set it, so the check that the task
	// is still blocked has to hold at the moment of writing, not only at the moment
	// of asking (ADR-0047).
	if err := env.Store.AppendActionAt(id, state.Seq, fsm.Unblock{}); err != nil {
		return err
	}

	fmt.Fprintf(env.Out, "%s unblocked — run it again with `luna run %s`\n", id, id)
	return nil
}
