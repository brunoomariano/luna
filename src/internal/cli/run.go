package cli

import (
	"context"
	"fmt"
	"strings"

	"github.com/brunoomariano/luna/src/internal/agent"
	"github.com/brunoomariano/luna/src/internal/fsm"
	"github.com/brunoomariano/luna/src/internal/lead"
	"github.com/brunoomariano/luna/src/internal/node"
	"github.com/brunoomariano/luna/src/internal/store"
)

// runTask drives a task until it needs a person or reaches the end.
//
// This is where the two halves meet: the node runs the agent and verifies, the
// engine decides. Everything it assembles is an implementation of an
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
	// one whose watchdog is its only net.
	state, err := env.Store.Replay(id, fsm.DefaultFlow())
	if err != nil {
		return err
	}

	conductor, cleanup, err := conduct(env, opts, state.Profile)
	if err != nil {
		return err
	}
	defer cleanup()

	// The machinery breaking does not surface here, and the absence is the
	// design rather than an omission: the lead records a block and the run ends
	// normally, so the task appears in `luna gates` with a reason instead of the
	// command erroring and leaving nothing behind.
	//
	// A branch here that reported ErrInfrastructure was removed as unreachable —
	// the lead absorbs it, and a message that can never print is one somebody
	// eventually maintains for nothing.
	state, err = conductor.Run(context.Background(), id)
	if err != nil {
		return err
	}

	return reportRun(env, id, state)
}

// runOptions is how this run is driven.
type runOptions struct {
	// Agent overrides every role's agent. Empty means each role decides, which is
	// the ordinary case.
	Agent string

	// Repo is the checkout worktrees are cut from.
	Repo string

	// Dry runs with no agent and no worktree: the engine, the log and the gates
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
// The node is chosen here and nowhere else: swapping how a stage is run is one
// more branch in this function, not a change to the lead or the engine.
func conduct(env Env, opts runOptions, profile fsm.Profile) (*lead.Lead, func(), error) {
	cfg := env.profiles()
	// The judge is what makes the retry budget real: without one the lead blocks on
	// the first failure and the retry budget is never spent. This one
	// carries no model — it reads the budget the task already has.
	conductor := &lead.Lead{
		Store: env.Store, Judge: lead.BudgetJudge{},
		// The mechanical half of a gate: what the task declared, run over what it
		// delivered.
		CheckGate: checkGateWith(env.Store, opts.Repo),
		// The judgement half, when the knob reaches a gate and a model is wired
		// in. Nil is the ordinary case for `luna run` — and then a gate the knob
		// reached still goes to a person, because authority to judge is not a
		// judgement.
		Ask: env.Lead,
		// The artifact itself rather than the evidence line naming it: a gate that
		// asks the lead to judge a contract has to hand it the contract.
		Artifact: func(taskID, artifact string) (string, bool) {
			blob, err := env.Store.LatestBlob(taskID, "", artifact)
			if err != nil {
				return "", false
			}
			return string(blob.Body), true
		},

		// `done` means ready to integrate, and this is what makes it true: the
		// task's own branch is pointed at what it delivered.
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

	// The agent runs as a subprocess in its own worktree. There is no server to
	// dial and no session to keep alive: a stage that dies leaves nothing behind
	// to reap, which is most of what the previous transport needed a connection
	// for.
	conductor.Node = &node.Runner{
		Repo: opts.Repo,
		// The flow, so a stage declaring `context = "live"` can find which session
		// its role was last in. The answer comes out of the task's log, and the
		// flow is what says which stage belongs to which role.
		Flow: fsm.DefaultFlow(),
		Agent: agent.Harness{
			// Which sandbox is deliberately not configurable: making it so would
			// move the containment boundary into the file where `editor` lives.
			Sandbox: node.Sandbox,
		},
		// The stage's role decides which agent runs it. --agent overrides every
		// role, which is what makes a run reproducible against one harness while
		// the roles are still being tuned.
		Roles:  rolesFor(cfg, opts.Agent),
		Budget: cfg.Turn(),

		// The socket a contained agent hands artifacts over through, opened inside
		// the stage's worktree — the only place the agent can reach, measured
		// against ai-jail 1.17.0, where every other position answers ENOENT.
		Artifacts: func(taskID string) node.ArtifactStore {
			return NewTaskArtifacts(env.Store, taskID, 0)
		},

		// What answers "was it handed over?" for an artifact that is not in the
		// commit. The store is the witness, and the hash it returns is what the
		// evidence carries.
		Stored: func(id, stage, artifact string) (string, error) {
			blob, err := env.Store.LatestBlob(id, stage, artifact)
			if err != nil {
				return "", err
			}
			return blob.Hash, nil
		},

		// A worktree that would not go away does not fail the stage, but it does
		// accumulate: a checkout left behind on every run eventually fills a disk,
		// and the first anyone would hear of it is that.
		Warn: func(format string, args ...any) {
			fmt.Fprintf(env.Err, format+"\n", args...)
		},
	}
	return conductor, func() {}, nil
}

// checkGateWith runs the commands a task declared for one gate, over what it
// delivered.
//
// This is the seam between the two halves of the gate contract: the task's log says which
// commands answer a gate, the node layer runs them in a checkout of the delivered
// commit, and the lead gets a verdict rather than a shell.
//
// The declaration used to be read from the registry's metadata; it is
// replayed from the task's own log now, which is what let the registry go.
// Nothing else about the seam changed — the outcome the lead sees is
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
// says so and lets the stage block.
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

// notifyBlocked tells a person a task stopped, which is what INV-5 means by
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
// Every path into a block ends here: a stall, broken machinery, a stage whose
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
	// of asking.
	if err := env.Store.AppendActionAt(id, state.Seq, fsm.Unblock{}); err != nil {
		return err
	}

	fmt.Fprintf(env.Out, "%s unblocked — run it again with `luna run %s`\n", id, id)
	return nil
}

// workCommand runs the agent for the stage that is already open, and stops.
//
// It is the half of `luna run` the lead needed and Luna did not have. The
// lead's brief has always said "the agent you start does the work — you do not
// do it yourself", and there was no way to start one: `next` reads, `done`
// reports, and `run` drives the whole flow, which is the one thing the lead must
// not do. So on TALLY-4 the lead did three stages with its own tools, and every
// artifact was recorded as "reported by hand through `luna done`" — no spend, no
// blobs, no handover, and a review gate whose artifact had never been attached.
//
// It chooses no stage. There is exactly one open, the status says so, and a task
// with none is refused rather than advanced — otherwise this would be `luna run`
// under another name and the lead would have a path to flow control.
//
// It does not close the stage either. Running the agent and reporting what it
// delivered stay apart, because `luna done` is where the lead's report meets the
// contract check, and that seam is what makes the lead's word cost nothing.
func workCommand(env Env, args []string) error {
	if len(args) == 0 {
		return fmt.Errorf("%w: work needs a task id", ErrUsage)
	}
	id := args[0]

	opts, err := parseRunOptions(args[1:])
	if err != nil {
		return err
	}

	state, err := env.replay(id)
	if err != nil {
		return err
	}
	if state.Status != fsm.StatusRunning {
		return fmt.Errorf("task %q has no running stage to work (it is %s)", id, state.Status)
	}

	conductor, cleanup, err := conduct(env, opts, state.Profile)
	if err != nil {
		return err
	}
	defer cleanup()

	var stage fsm.Stage
	for _, candidate := range fsm.DefaultFlow() {
		if candidate.ID == state.Stage {
			stage = candidate
		}
	}
	result, err := conductor.Node.Run(context.Background(), state, stage)
	if err != nil {
		return fmt.Errorf("working %s: %w", state.Stage, err)
	}

	// The evidence is recorded here, by the command whose verifiers produced it.
	//
	// The first version printed a `luna done` line instead and threw the verdict
	// away, on the reasoning that closing a stage belongs to `done`. That was
	// backwards: `done` records `existence` for everything, always and
	// deliberately, so a stage reported by hand cannot launder a verdict nobody
	// produced. Which makes it the wrong command to close a stage whose contract
	// declares `make test` — every one of them blocked with "proved [tests_green]
	// with a weaker check than its contract declared", measured on TALLY-4, where
	// the agent had genuinely run the tests.
	//
	// The lead's word still costs nothing. What closes the stage is what the tool
	// returned; the lead only chose to start it, and the reducer refuses a
	// delivery that falls short whoever hands it in.
	if err := env.Store.AppendActionAt(id, state.Seq, fsm.Complete{
		Delivered: result.Delivered,
		Evidence:  result.Evidence,
		Commit:    result.Commit,
		Spent:     result.Spent,
		Flow:      fsm.DefaultFlow(),
	}); err != nil {
		return err
	}

	reportWork(env, id, state.Stage, result)
	return nil
}

// reportWork prints what the agent did and what it cost.
//
// It reports rather than instructs: the stage is already closed by the time this
// runs, so there is no `luna done` line to copy. What the reader wants to know is
// what the tool concluded and what the call cost.
func reportWork(env Env, id string, stage fsm.StageID, result lead.Result) {
	// "closed", not "ran": the lead read "ran setup / delivered worktree" as a
	// report and went looking for the command that would close it. It said so
	// itself — "reads like a closure but isn't one" — which was the right
	// observation about output that was by then out of date.
	fmt.Fprintf(env.Out, "%s closed %s\n", id, stage)
	if result.Commit != "" {
		fmt.Fprintf(env.Out, "  commit    %s\n", result.Commit)
	}
	if len(result.Delivered) > 0 {
		fmt.Fprintf(env.Out, "  delivered %s\n", joinArtifactNames(result.Delivered))
	}
	if !result.Spent.Zero() {
		fmt.Fprintf(env.Out, "  spent     %d tokens  $%.4f  %d turns\n",
			result.Spent.Tokens(), result.Spent.CostUSD, result.Spent.Turns)
	}
}

// joinArtifactNames renders a delivery for a person to copy into `luna done`.
func joinArtifactNames(list []fsm.Artifact) string {
	names := make([]string, 0, len(list))
	for _, a := range list {
		names = append(names, string(a))
	}
	return strings.Join(names, ",")
}
