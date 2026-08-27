package cli

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/brunoomariano/luna/src/internal/agent"
	"github.com/brunoomariano/luna/src/internal/fsm"
	"github.com/brunoomariano/luna/src/internal/lead"
	"github.com/brunoomariano/luna/src/internal/node"
	"github.com/brunoomariano/luna/src/internal/store"
)

// driveTask runs one task to its next stopping point and returns where it landed.
//
// Split out of the command so the fleet can drive many of these at once without a
// second copy of the wiring. Everything about one task stays here; what the fleet
// adds is which tasks and how many at a time.
//
// The machinery breaking does not surface as an error, and the absence is the
// design rather than an omission: the lead records a block and the run ends
// normally, so the task appears in `luna gates` with a reason instead of the
// caller erroring and leaving nothing behind.
func driveTask(ctx context.Context, env Env, id string, opts runOptions) (fsm.TaskState, error) {
	events, err := env.Store.Events(id)
	if err != nil {
		return fsm.TaskState{}, err
	}
	if len(events) == 0 {
		return fsm.TaskState{}, fmt.Errorf("no task %q — create it with `luna task new %s`", id, id)
	}

	// The budget comes from the profile this task was created under, read from
	// its own log. Taking it from a fixed profile would give a nightly run the
	// supervised timeout — backwards, since the run with nobody watching is the
	// one whose watchdog is its only net.
	state, err := env.replay(id)
	if err != nil {
		return fsm.TaskState{}, err
	}
	flow, err := env.flowOf(id)
	if err != nil {
		return fsm.TaskState{}, err
	}

	if err := agreeOnSimulation(id, state, opts.Dry); err != nil {
		return fsm.TaskState{}, err
	}

	conductor, cleanup, err := conduct(env, opts, state.Profile, flow)
	if err != nil {
		return fsm.TaskState{}, err
	}
	defer cleanup()

	return conductor.Run(ctx, id)
}

// stageToWork resolves the task to a state with a stage actually open, or says
// why there is none.
//
// The opening is here because neither command in the lead's loop did it: its
// brief is `luna next` then `luna work`, `next` is a read, and `work` required a
// stage already running. So a task sat at `stage_done` while `next` named the
// stage that logically followed and `work` refused it — twice, byte-identically,
// because a retry cannot clear a disagreement.
//
// Measured on TALLY-6, and the stall was not the cost. Given two commands that
// contradicted each other and no third, the lead reached for
// a dry run to understand the mechanism, and that walked the task to
// `done` with `verify` and `review` recorded as passed. The gap did not block
// the run; it routed around the part that checks.
func stageToWork(env Env, id string, opts runOptions) (fsm.TaskState, error) {
	state, err := env.replay(id)
	if err != nil {
		return state, err
	}

	if err := agreeOnSimulation(id, state, opts.Dry); err != nil {
		return state, err
	}

	if state, err = openNextStage(env, id, state, opts.Repo); err != nil {
		return state, err
	}

	// Still not running means there was nothing to open: a gate waiting on a
	// person, a block, or a finished task. The status says which, and none of
	// them is `work`'s to push past.
	if state.Status != fsm.StatusRunning {
		// The status is known here, so the way out is too. It used to stop at the
		// state and leave the caller to work out the command — and a lead that
		// correctly refuses to guess a command that writes to the log has nowhere
		// to go, which is where one sat.
		return state, fmt.Errorf("task %q has no running stage to work (it is %s)%s",
			id, state.Status, wayOut(id, state))
	}
	return state, nil
}

// openNextStage opens the stage that follows a closed one, and returns the state
// that results.
//
// Only for a task already in flight. A `ready` task has no stage yet, and
// opening its first one would be `work` choosing where the flow starts — the one
// thing this command must not do, and what `luna lead` is for.
func openNextStage(env Env, id string, state fsm.TaskState, repo string) (fsm.TaskState, error) {
	if state.Status != fsm.StatusStageDone {
		return state, nil
	}

	// The task's own flow, never this build's default: a Lead with no Flow replays
	// against DefaultFlow, and every task on any other flow is then refused by its
	// own fingerprint before it can open a stage.
	flow, err := env.flowOf(id)
	if err != nil {
		return state, err
	}

	entering := &lead.Lead{
		Store:     env.Store,
		Flow:      flow,
		CheckGate: checkGateWith(env.Store, repo),
	}
	if err := entering.Enter(context.Background(), id); err != nil {
		return state, err
	}
	return env.replay(id)
}

// agreeOnSimulation refuses to mix a simulation with a real run.
//
// Both directions are refused, and neither is hypothetical. A dry run pointed at
// a real task is what happened on TALLY-6: the flag walked a task with four
// genuine stages to `done`, marking `verify` and `review` as passed without
// running either agent, and the log said the pipeline was green against code no
// command had seen. The other direction is the same damage read backwards — a
// real run continuing a simulated task would leave one history where some stages
// ran and some did not, with nothing saying which.
//
// The check is on the task rather than on each stage because that is the honest
// unit: a run that simulated any part of itself is a simulation.
func agreeOnSimulation(id string, state fsm.TaskState, dry bool) error {
	if state.Simulated == dry {
		return nil
	}
	if dry {
		return fmt.Errorf("%q is a real task with %d events: a dry run would record "+
			"stages as passed without running them. Open a separate task to exercise "+
			"the flow", id, state.Seq)
	}
	return fmt.Errorf("%q was created as a simulation, and its stages recorded "+
		"checks that never ran: it cannot be continued for real. Open a new task",
		id)
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

// parseRunOptions reads the flags the commands that start an agent accept.
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
func conduct(env Env, opts runOptions, profile fsm.Profile, flow []fsm.Stage) (*lead.Lead, func(), error) {
	// Applied once, here, so the lead and the node see the same flow: the two are
	// given it separately below, and an override landing on only one of them would
	// make the log disagree with what ran.
	flow = forceAgent(flow, opts.Agent)

	// The judge is what makes the retry budget real: without one the lead blocks on
	// the first failure and the retry budget is never spent. This one
	// carries no model — it reads the budget the task already has.
	conductor := &lead.Lead{
		Store: env.Store, Judge: lead.BudgetJudge{}, Flow: flow,
		// The mechanical half of a gate: what the task declared, run over what it
		// delivered.
		CheckGate: checkGateWith(env.Store, opts.Repo),
		// The judgement half, when the knob reaches a gate and a model is wired
		// in. Nil is the ordinary case for a dry run — and then a gate the knob
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

	stage, cleanup, err := env.Node(env, opts, flow)
	if err != nil {
		return nil, nil, err
	}
	conductor.Node = stage
	return conductor, cleanup, nil
}

// StageRunner is the real thing that runs a stage: a contained agent in its own
// worktree.
//
// Exported so the binary can wire it and a test can replace it. There is no
// server to dial and no session to keep alive — a stage that dies leaves nothing
// behind to reap, which is most of what the previous transport needed a
// connection for.
func StageRunner(env Env, opts runOptions, flow []fsm.Stage) (lead.Node, func(), error) {
	cfg := env.profiles()

	runner := &node.Runner{
		Repo: opts.Repo,
		// The project's step between `git clone` and "the tests run", run in every
		// fresh worktree. Without it a stage's checks fail on the machine rather
		// than on the work — two build stages and $10.36 of a $19.13 task, for code
		// that had been correct since the first attempt.
		Bootstrap: cfg.Bootstrap,
		// The flow, so a stage declaring `context = "live"` can find which session
		// its role was last in. The answer comes out of the task's log, and the
		// flow is what says which stage belongs to which role.
		Flow: flow,
		Agent: agent.Harness{
			// Which sandbox is deliberately not configurable: making it so would
			// move the containment boundary into the file where `editor` lives.
			Sandbox: node.Sandbox,
		},
		// The stage's role decides which agent runs it. --agent overrides every
		// role, which is what makes a run reproducible against one harness while
		// the roles are still being tuned.
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
	return runner, func() {}, nil
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
		state, err := s.ReplayOwnFlow(taskID)
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

// forceAgent pins every non-mechanical stage to one harness.
//
// The override exists for the same reason `--dry-run` does: pinning every stage
// to one harness makes a run reproducible while the briefs are still being tuned.
// It changes which agent runs, never what the stage names itself — so the flow
// and the log stay honest about who was supposed to do what.
//
// An empty override returns the flow untouched, and a mechanical stage is left
// alone: it starts no agent, so giving it one would invent a process.
func forceAgent(flow []fsm.Stage, override string) []fsm.Stage {
	if override == "" {
		return flow
	}
	forced := make([]fsm.Stage, len(flow))
	copy(forced, flow)
	for i := range forced {
		if !forced[i].Mechanical() {
			forced[i].Agent = override
		}
	}
	return forced
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

	state, err := env.replay(id)
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

	fmt.Fprintf(env.Out, "%s unblocked — conduct it again with `luna lead %s`\n", id, id)
	return nil
}

// workCommand runs the agent for the stage that is already open, and stops.
//
// It is the half of the driving loop the lead needed and Luna did not have. The
// lead's brief has always said "the agent you start does the work — you do not
// do it yourself", and there was no way to start one: `next` reads, `done`
// reports, and `run` drives the whole flow, which is the one thing the lead must
// not do. So on TALLY-4 the lead did three stages with its own tools, and every
// artifact was recorded as "reported by hand through `luna done`" — no spend, no
// blobs, no handover, and a review gate whose artifact had never been attached.
//
// It chooses no stage. There is exactly one open, the status says so, and a task
// with none is refused rather than advanced — otherwise this would be the driving loop
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

	state, err := stageToWork(env, id, opts)
	if err != nil {
		return err
	}
	flow, err := env.flowOf(id)
	if err != nil {
		return err
	}

	conductor, cleanup, err := conduct(env, opts, state.Profile, flow)
	if err != nil {
		return err
	}
	defer cleanup()

	result, err := conductor.Node.Run(context.Background(), state, stageIn(flow, state.Stage))
	if err != nil {
		// The machinery breaking is not this command failing. INV-5 says a task
		// ends in a commit, a gate or a *notified* block, and a sandbox that is
		// not installed is infrastructure: the task blocks with a reason a person
		// can act on, and the caller — the lead, usually — reads that from the
		// log rather than from an exit status it cannot record.
		//
		// This lived only in the loop that Luna used to drive with. When that
		// surface went and the lead became the only way work starts, `work`
		// erroring took the third ending with it: nothing blocked, nothing was
		// notified, and the task sat `running` for whoever looked next.
		if errors.Is(err, lead.ErrInfrastructure) || errors.Is(err, lead.ErrStalled) {
			return blockTask(env, id, err.Error())
		}
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
		Guarded:   result.Guarded,
		Flow:      flow,
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

// blockTask records a block and reports it, which is the third ending INV-5
// names.
//
// The retry budget is untouched on purpose: an attempt against a binary that is
// not installed will not find it installed on the second one, and spending the
// budget on that leaves nothing for the failure it was meant for.
func blockTask(env Env, id, reason string) error {
	if err := env.Store.AppendAction(id, fsm.Block{Reason: reason}); err != nil {
		return err
	}
	fmt.Fprintf(env.Out, "%s is blocked: %s\n", id, reason)
	fmt.Fprintf(env.Out, "  resume it with `luna unblock %s` once it is dealt with\n", id)
	notifyBlocked(env, id, reason)
	return nil
}

// stageIn finds a stage by id, or returns the zero stage.
//
// The zero value rather than an error because the caller reached here through
// NextOrder, which already refused a stage this flow does not have.
func stageIn(flow []fsm.Stage, id fsm.StageID) fsm.Stage {
	for _, candidate := range flow {
		if candidate.ID == id {
			return candidate
		}
	}
	return fsm.Stage{}
}

// wayOut names the command that moves a task on from where it stopped.
//
// Three of the four states `work` refuses have one, and saying it is the whole
// difference between a caller that continues and one that stops: the state is
// already in hand, so leaving it out is withholding half an answer.
func wayOut(id string, state fsm.TaskState) string {
	switch state.Status {
	case fsm.StatusBlocked:
		return fmt.Sprintf(" — deal with what stopped it, then `luna unblock %s`", id)
	case fsm.StatusAwaitingGate:
		return fmt.Sprintf(" — answer it with `luna gate show %s`", id)
	case fsm.StatusReady, fsm.StatusStageDone:
		return fmt.Sprintf(" — `luna lead %s` opens the next stage", id)
	default:
		// Done, called off: there is no next command, and inventing one would send
		// somebody to reopen a task that ended on purpose.
		return ""
	}
}
