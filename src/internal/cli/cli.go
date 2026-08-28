// Package cli is the surface a person uses.
//
// It is deliberately thin: every command reads or writes the store and prints.
// Nothing here decides flow — that is the engine's job — and nothing here runs an
// agent, which belongs to the lead. What it does own is making a suspended task
// discoverable, which is the half of INV-5 that no amount of engine work can
// provide.
package cli

import (
	"context"
	"errors"
	"fmt"
	"io"
	"sort"
	"strings"

	"github.com/brunoomariano/luna/src/internal/fsm"
	"github.com/brunoomariano/luna/src/internal/lead"
	"github.com/brunoomariano/luna/src/internal/node"
	"github.com/brunoomariano/luna/src/internal/store"
)

// ErrUsage is returned when the command line does not parse. It is separated from
// a runtime failure so main can print usage for one and only the message for the
// other.
var ErrUsage = errors.New("usage")

// Env is what a command needs from the outside world. It exists so tests can run
// the real commands against a temporary store and captured output, rather than
// asserting on a mock of themselves.
type Env struct {
	Store *store.Store

	// Config is the project's settings, including the profiles it defines. A zero
	// value carries no profiles at all, so commands that need one fall back to the
	// shipped set rather than refusing every name.
	Config Config

	Out io.Writer
	Err io.Writer

	// In is where `--stdin` reads a replacement from. Nil means nothing is
	// connected, which `--stdin` reports rather than silently treating as empty.
	In io.Reader

	// Edit opens content for a person to change and returns what they left. It is
	// injected rather than called directly so a test does not need $EDITOR — and
	// so that a headless run can fail loudly instead of hanging on a terminal.
	Edit func(current string) (string, error)

	// Where says what the working directory is already working on, so a command
	// does not have to be told what the caller is standing in.
	//
	// Injected rather than called directly because resolving it runs git, and a
	// test that wanted to check "the id came from the directory" would otherwise
	// need a repository to stand in. Nil means nothing can be inferred, which is
	// the honest answer outside a checkout and the one every caller has to handle
	// anyway.
	Where func() (node.Identity, error)

	// Notify tells a person a task stopped. Injected for the same reason as Edit:
	// a test must not draw a banner, and a machine with no notifier should print
	// and carry on rather than fail the run (INV-5).
	Notify func(ctx context.Context, taskID, reason string) error

	// Lead is the model that judges a gate for `luna lead`. Injected because
	// Luna hosts no model of its own, and nil because a dry run exercises the same
	// flow without one — a machine with no harness still runs every task whose
	// gates a person answers.
	Lead func(ctx context.Context, prompt string) (string, error)

	// Land points a finished task's branch at what it delivered. Injected so a
	// test can observe the landing without a repository, and nil means the real
	// one — the command supplies `node.Land` when this is unset.
	//
	// It exists because the landing broke twice in ways no test could see: once
	// with the field unwired, once with it wired and no loop calling it. Both
	// times the suite was green and `luna status` promised a branch that was
	// never created.
	Land func(ctx context.Context, taskID, commit string) error

	// Node builds the thing that runs one stage. Injected so a test can drive a
	// solo run without starting a contained agent and waiting out the turn budget.
	//
	// A constructor rather than a value because the real one is per-run: it needs
	// the repository, the flow and the role table before it can exist. That also
	// keeps it wired in the binary rather than nil-means-real, which is the shape
	// TestEveryInjectedDependencyIsWired exists to hold — a field that is nil on
	// every machine is a feature that is off on every machine.
	//
	// It exists because solo mode has no other seam. The pack's conductor is a
	// model and arrives through `Lead`, so a fake there drives it; a solo run has
	// no conductor at all.
	Node func(env Env, opts runOptions, flow []fsm.Stage) (lead.Node, func(), error)
}

// Run dispatches a command line. args excludes the program name.
func Run(env Env, args []string) error {
	if len(args) == 0 {
		return fmt.Errorf("%w: %s", ErrUsage, Usage())
	}

	switch args[0] {
	case "help", "-h", "--help":
		fmt.Fprintln(env.Out, Usage())
		return nil
	}

	// A table rather than a switch arm each: adding a command is adding a row, and
	// the dispatch was the most complex function in the package without deciding
	// anything.
	//
	// Built here rather than as a package variable because an entry that referred
	// back to this function would be an initialisation cycle — which one of them
	// once did.
	commands := map[string]func(Env, []string) error{
		"task":     runTask,
		"work":     workCommand,
		"unblock":  unblockCommand,
		"gates":    runGates,
		"gate":     runGate,
		"flow":     runFlow,
		"next":     nextCommand,
		"done":     doneCommand,
		"status":   statusCommand,
		"stuck":    stuckCommand,
		"lead":     leadCommand,
		"autonomy": autonomyCommand,
		"budget":   budgetCommand,
		"fleet":    runFleet,
		"artifact": artifactCommand,
		"trust":    trustCommand,
		"version":  versionCommand,
		"where":    whereCommand,
	}

	command, ok := commands[args[0]]
	if !ok {
		return fmt.Errorf("%w: unknown command %q\n%s", ErrUsage, args[0], Usage())
	}
	return command(env, args[1:])
}

// Usage is the help text. It is a function rather than a constant so the command
// list has one home.
// Usage is the help text. It is a function rather than a constant so the command
// list has one home.
//
// Grouped by who acts rather than alphabetically, because the two modes are the
// product and everything else exists to watch them or to correct them. A reader
// looking for "how do I start work" should not have to find that among the ways
// to read a gate.
func Usage() string {
	return strings.TrimSpace(`
luna — deterministic orchestration for AI agents

Two modes. Luna picks the stage in both; the knob picks who answers a gate.

  luna lead <id> [--autonomy 0-10] [--agent <kind>] [--dry-run]
        solo: one agent carries the task end to end. Luna starts it per
        stage, contained and in a worktree, under the single role a solo
        run collapses the flow onto — so the worktree and the session
        survive from stage to stage, and there is never a conductor and a
        worker alive at once. It never chooses a stage.
        --dry-run exercises the flow with no agent, no worktree and no
        model — it is what tells a broken flow from a broken integration.

  luna fleet run <id> [--autonomy 0-10] [--agent <kind>] [--dry-run]
        pack: the lead conducts, and the roles the flow declares do the
        work — a worktree and a session each, kept across the stages that
        role owns. That is what buys an independent audit: a role that
        judges can be denied the tools to edit, which one agent doing
        everything cannot be.
        The size of the pack is the flow's, not a flag's — luna flow
        check says what each one names. A pack is inside one task.

how a stage is carried out — the pack's lead runs these, and so can you

  luna next <id> [--json]
        the order for this task: which stage, which role, which worktree,
        which base commit, what is denied. It is an instruction, not
        advice, and reading it changes nothing.

  luna work <id> [--agent <kind>] [--dry-run]
        run the agent for the stage the order names, and close that stage
        with whatever its checks observed. It chooses no stage.

  luna done <id> --delivered <a,b> [--commit <sha>]
        report a stage carried out by hand, and hand in the commit it
        produced. Luna checks the delivery against the contract — a stage
        that owed more than it delivered does not close. A stage whose
        contract names a command will not close this way.

  luna artifact put <artifact> [--stage <stage>]
  luna artifact get <artifact> [--stage <stage>]
        hand a document to Luna, or read one back. This is how a stage
        delivers something that is not a commit — a contract, a briefing —
        without leaving it in the tree for everyone downstream.

opening and correcting a task

  luna task new <id> --kind <kind> [--flow <flow>] [--profile <profile>]
        [--workstream <name> | --new-workstream <name>] [--simulated]
        [--about <what>] [--design <how>] [--acceptance <done when>]
        open a task's log, with what the task is about. --flow picks which
        flow it runs and cannot change afterwards: the flow's identity goes
        into the opening event, so a task that switched flows mid-run would
        be a log no replay could read. luna flow check lists them.
        Every agent this task starts writes to one durable workstream, so
        what one stage learned is there for the next. It is the project's
        unless the task names another; --new-workstream is the only way
        Luna opens one, because inferring that from an unknown name would
        make a typo write to a second ledger instead of stopping.

  luna task statement <id> [--about ...] [--design ...] [--acceptance ...]
        correct what a task is about. The previous wording stays in the
        log — a revision is an event, not an overwrite.

  luna budget <id> [<usd> [reason]]
        show what the task may spend, or move the ceiling. No ceiling by
        default. A task that stops on its budget carries on by raising it
        and unblocking — a limit with no way past it makes the cheapest
        failure the one you cannot recover from. Moving it writes an
        event, so the log says when it changed and why.

  luna autonomy <id> [<0-10> [reason]]
        show the knob, or move it mid-run. Moving it writes an event, so
        the log says when it changed and why. A gate already open still
        goes to a person; only later gates see the new value.

  luna task abandon <id> <reason>
        end a task that will not be finished. The log keeps everything —
        abandoning records that a person called it off, and why.

  luna task forget <id>
        drop the documents a finished task handed over. The log is
        untouched, hashes included, so what was produced outlives the
        content. Only a task that has ended may be forgotten.

answering a gate

  luna gate show <id>
        what a suspended task is waiting for, and what the lead already
        concluded about it if the knob let it look

  luna gate approve <id>
        accept and carry on

  luna gate reject <id> [reason]
        refuse the artifact; the stage that produced it runs again

  luna gate adjust <id> [--append <text> | --replace <text> | --stdin]
        change the artifact under review, then accept the changed version.
        With no flag, opens the editor. The flags exist so an agent or a
        script can answer a gate without a terminal.

  luna gate checks <id> --on <gate> [--run <command>]...
        declare the commands that answer a gate mechanically. They run
        against what the stage delivered, and the first failure is the
        answer. With no --run, the gate is declared to have no mechanical
        answer and goes to judgement.

watching

  luna status <id> [--json]
        the whole flow and where the task stands in it — which flow and
        its fingerprint, the pack it keeps, the workstream it writes to,
        what the knob means for these gates, the ceiling and what is
        left, and per stage the model that answered, the turns and the
        cost. Plus where each role's worktree is, and which one is open.
        Separate from next on purpose: an order carries no view of what
        comes after it.

  luna task show <id> [--json]
        the task's current state and what it has produced

  luna gates [--json]
        every task waiting on a person

  luna stuck [--for <duration>] [--notify] [--json]
        what has been stopped for too long — a blocked merge, a gate
        nobody answered. Defaults to an hour. --notify tells a person
        instead of only whoever ran the command.

  luna fleet report [--since <duration>] [--json]
        what every task is, grouped by what has to happen to it next.
        This is the morning's product rather than a side effect of it.

  luna unblock <id>
        clear a block once whatever caused it is dealt with

  luna flow check [--flow <flow>]
        what flows this build carries, whether anything is open, and
        what each has cost here before — median by stage, from this
        project's own log. --flow cannot change once a task opens, so it
        is the most expensive decision available and was the one made
        with the least information.
        They come from the binary and a project cannot override them —
        one build, one set of flows, every repository the same.
        Every flow by default — one that is never audited is one whose
        contract nobody checked. Changing a flow under an open task stops
        it replaying.

setting the machine up

  luna version
        which build this is, and the flows it carries with their
        fingerprints. A skill or a runbook written against one surface and
        run against another fails at the first unknown flag with no way to
        tell which of the two is behind — this is the one comparison that
        answers it.

  luna trust
        tell the harness it trusts the directory Luna makes worktrees in,
        so its agents start at a prompt instead of at a folder dialog.

config:   .luna/config.toml — editor, lead_harness, turn_budget,
          workstream (the project's default), profiles

kinds:    feature, bug, chore, docs
profiles: interactive (default), turbo, nightly, plus any the project
          defines in .luna/config.toml
`)
}

func runTask(env Env, args []string) error {
	if len(args) == 0 {
		return fmt.Errorf("%w: task needs a subcommand (new, show, statement, abandon, forget)", ErrUsage)
	}

	switch args[0] {
	case "new":
		return taskNew(env, args[1:])
	case "show":
		return taskShow(env, args[1:])
	case "statement":
		return taskStatement(env, args[1:])
	case "abandon":
		return taskAbandon(env, args[1:])
	case "forget":
		return taskForget(env, args[1:])
	default:
		return fmt.Errorf("%w: unknown task subcommand %q", ErrUsage, args[0])
	}
}

// taskAbandon ends a task a person decided not to finish.
//
// It reads the state first like every other command, and then makes one
// exception: a task whose flow changed under it no longer replays at all, and
// those are exactly the tasks that most need ending. So `ErrFlowChanged` is not
// a refusal here — it is the reason this command exists.
//
// Every other replay failure is. It used to skip the read entirely, on the
// reasoning that the reducer would refuse on the next read and that abandoning a
// finished task could not happen by accident. Both halves were wrong. The
// reducer refusing *afterwards* is not a refusal, it is a corrupted log: the
// event is already written, append-only, and the whole task becomes unreadable —
// `task show`, `status` and `forget` all replay, so a task in that state can
// neither be read nor be got rid of. And it happened by accident on the first
// occasion anyone tried, abandoning TALLY-6 to note why its run was superseded,
// three minutes after it had finished. Nineteen events of a completed run,
// including what it cost, are unreachable behind the twentieth.
func taskAbandon(env Env, args []string) error {
	if len(args) < 2 {
		return fmt.Errorf("%w: task abandon needs a task and a reason", ErrUsage)
	}
	id, reason := args[0], strings.Join(args[1:], " ")

	events, err := env.Store.Events(id)
	if err != nil {
		return err
	}
	if len(events) == 0 {
		return fmt.Errorf("no task %q", id)
	}

	if err := abandonable(env, id); err != nil {
		return err
	}

	if err := env.Store.AppendAction(id, fsm.Abandon{Reason: reason}); err != nil {
		return err
	}

	fmt.Fprintf(env.Out, "%s abandoned: %s\n", id, reason)
	return nil
}

// abandonable refuses to write an Abandon the reducer will reject on the way
// back in.
//
// A task that no longer replays against this build is the case this command is
// for, so that failure is passed over rather than reported. Anything else — and
// a task that has already ended is the one that matters — is refused before the
// write, because after it there is no way back: the log is append-only and every
// command that reads the task replays it.
func abandonable(env Env, id string) error {
	state, err := env.Store.ReplayOwnFlow(id)
	if errors.Is(err, store.ErrFlowChanged) {
		return nil
	}
	if err != nil {
		return err
	}
	if state.IsTerminal() {
		return fmt.Errorf("%s already ended as %s — abandoning it would write an event "+
			"that cannot be replayed, and the whole task would stop being readable. "+
			"`luna task forget %s` removes a task that has ended", id, state.Status, id)
	}
	return nil
}

func taskNew(env Env, args []string) error {
	if len(args) == 0 {
		return fmt.Errorf("%w: task new needs an id", ErrUsage)
	}

	id := args[0]

	// Validated here, at the only place an id enters the system. Everything
	// downstream treats it as safe: it becomes a directory name and part of an
	// agent name, and neither checked — the directory name was flagged first and
	// the guard was left unbuilt.
	if err := fsm.ValidateTaskID(id); err != nil {
		return fmt.Errorf("%w: %w", ErrUsage, err)
	}

	opts, err := parseTaskOptions(env.profiles(), args[1:])
	if err != nil {
		return err
	}

	// A task is created once. Refusing here rather than letting the reducer catch
	// it at replay means the message names the command that was wrong.
	events, err := env.Store.Events(id)
	if err != nil {
		return err
	}
	if len(events) > 0 {
		return fmt.Errorf("task %q already exists, with %d events", id, len(events))
	}

	// The flow the task is born under is recorded with it — by name, so a later
	// replay can load it, and by fingerprint, so it can tell it is being read
	// against a different one.
	stages, err := fsm.FlowNamed(opts.flow)
	if err != nil {
		return err
	}
	// The project's workstream unless the task named one. Resolved here, at
	// creation, and written into the log — so a replay reads which ledger the work
	// actually went to rather than which one the config names today.
	memory := opts.memory
	if memory.Workstream == "" {
		memory.Workstream = env.profiles().Memory()
	}

	fingerprint := fsm.Fingerprint(stages)
	created := fsm.TaskCreated{
		Kind:      opts.kind,
		Profile:   opts.profile,
		Flow:      fingerprint,
		FlowName:  opts.flow,
		BudgetUSD: opts.budgetUSD,
		Memory:    memory,
		Statement: opts.stated,
		Simulated: opts.simulated,
	}
	if err := env.Store.AppendAction(id, created); err != nil {
		return err
	}

	fmt.Fprintf(env.Out, "created %s (kind=%s profile=%s flow=%s/%s)\n",
		id, opts.kind, opts.profile, opts.flow, fingerprint)
	if opts.stated.Stated() {
		fmt.Fprintf(env.Out, "  about: %s\n", opts.stated.Description)
	}
	return nil
}

// taskStatement records a new statement of work for a task that already exists.
//
// Separate from `task new` because the two answer different questions — "open
// this" and "here is what it turned out to be" — and because a revision is an
// action of its own, so the previous statement stays in the log.
func taskStatement(env Env, args []string) error {
	if len(args) == 0 {
		return fmt.Errorf("%w: luna task statement <id> [--about ...] [--design ...] [--acceptance ...]", ErrUsage)
	}

	id := args[0]
	if err := fsm.ValidateTaskID(id); err != nil {
		return fmt.Errorf("%w: %w", ErrUsage, err)
	}

	opts, err := parseTaskOptions(env.profiles(), args[1:])
	if err != nil {
		return err
	}
	if !opts.stated.Stated() {
		return fmt.Errorf("%w: say something — --about, --design or --acceptance", ErrUsage)
	}

	// A task with no log replays to a zero state rather than an error, so its
	// absence is checked here — otherwise describing a task nobody created would
	// append an event and look like it worked.
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

	if err := env.Store.AppendActionAt(id, state.Seq, fsm.StatementRevised{Statement: opts.stated}); err != nil {
		return err
	}

	fmt.Fprintf(env.Out, "%s is about: %s\n", id, opts.stated.Description)
	return nil
}

// parseTaskOptions reads --kind and --profile, defaulting to the cautious pair:
// a feature, supervised. An unstated profile must not run unattended.
//
// The valid names come from the project's configuration rather than a list in the
// engine, because a project defines its own profiles. The rejection is still
// worth doing here: someone who meant `nightly` hears about the typo, instead of
// getting a supervised run with nothing saying why.
// taskOptions is everything `task new` was told: how to run the task, and what
// the task is about.
type taskOptions struct {
	kind    fsm.TaskKind
	profile fsm.Profile

	// memory is the workstream this task's agents write to. The zero value takes
	// the project's, which is what almost every task wants.
	memory fsm.TaskMemory

	// budgetUSD is the most the task may spend before it stops. Zero is no
	// ceiling, which is what a caller that never named one gets.
	budgetUSD float64

	// flow is which flow the task runs, by name. It is fixed at creation and
	// never changes: the flow's identity goes into the opening event, and a task
	// that could switch flows mid-run would be a log no replay could read.
	flow string

	// stated is what the task is about, as it was given on the command line. It
	// rides into the log with the task rather than into a registry.
	stated fsm.Statement

	// simulated opens a task for `--dry-run` to exercise. Declared here rather
	// than inferred at run time because it is a property of the task, and a task
	// cannot become a simulation after real stages have run in it.
	simulated bool
}

// howToRun reads the flags that decide how the task is conducted, as opposed to
// what it is about. An unknown flag lands here because this is the last of the
// three, and it is the one that can say what the valid names are.
func howToRun(opts *taskOptions, cfg Config, name, value string) error {
	if done, err := whichWorkstream(opts, name, value); done {
		return err
	}

	switch name {
	case "kind":
		kind, err := parseKind(value)
		if err != nil {
			return err
		}
		opts.kind = kind
	case "profile":
		if !cfg.Defines(fsm.Profile(value)) {
			return fmt.Errorf("%w: unknown profile %q (%s)",
				ErrUsage, value, strings.Join(cfg.ProfileNames(), ", "))
		}
		opts.profile = fsm.Profile(value)
	case "budget-usd":
		budget, err := fsm.ParseBudgetUSD(value)
		if err != nil {
			return fmt.Errorf("%w: %w", ErrUsage, err)
		}
		opts.budgetUSD = budget
	case "flow":
		return namedFlow(opts, value)
	case "simulated":
		opts.simulated = true
	default:
		return fmt.Errorf("%w: unknown flag --%s", ErrUsage, name)
	}
	return nil
}

// statedBy fills in the flag if it is one of the statement's, and reports whether
// it was. Separate from the switch above so that adding a field to the statement
// of work does not make the option parser harder to read.
//
// `--about` rather than `--description`: it is what a person answers when asked
// what the task is.
func statedBy(stated *fsm.Statement, name, value string) bool {
	switch name {
	case "about":
		stated.Description = value
	case "design":
		stated.Design = value
	case "acceptance":
		stated.Acceptance = value
	default:
		return false
	}
	return true
}

func parseTaskOptions(cfg Config, args []string) (taskOptions, error) {
	opts := taskOptions{kind: fsm.KindFeature, profile: fsm.ProfileInteractive, flow: fsm.DefaultFlowName}

	flags, err := parseFlags(args)
	if err != nil {
		return taskOptions{}, err
	}

	for name, value := range flags {
		if statedBy(&opts.stated, name, value) {
			continue
		}
		if err := howToRun(&opts, cfg, name, value); err != nil {
			return taskOptions{}, err
		}
	}
	return opts, nil
}

// profiles is the configuration a command should resolve names against, filling
// in the shipped profiles and roles when nothing was loaded.
//
// The fallback is for tests and for a zero Env, not for a real run: main always
// loads a config, and a missing file already yields the shipped set.
//
// It fills in rather than rebuilds, and the difference is a bug this had: the
// earlier version returned a fresh Config carrying only the editor, so a project
// that set `turn_budget` or `lead_harness` but named no profile silently lost
// both — its watchdog ran on the shipped default and nothing said so. Every
// setting that is not a profile has to survive a project that has none.
func (e Env) profiles() Config {
	cfg := e.Config
	if len(cfg.Profiles) == 0 {
		cfg.Profiles = ShippedProfiles()
	}
	return cfg
}

func taskShow(env Env, args []string) error {
	if len(args) == 0 {
		return fmt.Errorf("%w: task show needs an id", ErrUsage)
	}
	id := args[0]

	asJSON, err := wantsJSON(args[1:])
	if err != nil {
		return err
	}

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

	if asJSON {
		flow, err := env.flowOf(id)
		if err != nil {
			return err
		}
		return writeJSON(env.Out, taskReport(env.profiles(), state, len(events), flow))
	}

	printTask(env, state, len(events))
	spendFlow, err := env.flowOf(id)
	if err != nil {
		return err
	}
	printSpend(env, state, spendFlow)
	printHandedOver(env, id)
	return nil
}

// printHandedOver lists what the task's stages handed to Luna rather than to the
// commit.
//
// This is the listing INV-5 asks for: an artifact produced for a person is
// discoverable by command, without anyone having watched it scroll by. Before it,
// `qa_report` was verified, had evidence in the log, and appeared nowhere — the
// produced list above reads Context.Artifacts, which human-facing artifacts never
// enter by design.
func printHandedOver(env Env, id string) {
	blobs, err := env.Store.Blobs(id)
	if err != nil || len(blobs) == 0 {
		// An unreadable store already failed louder above; an empty one is every
		// task that ran before the handover, and silence is the right shape for it.
		return
	}

	fmt.Fprintf(env.Out, "\nhanded over\n")
	for _, blob := range blobs {
		fmt.Fprintf(env.Out, "  %s\n", artifactLine(blob))
	}
	fmt.Fprintf(env.Out, "  read one with `luna artifact get <name>` inside a stage\n")
}

// gateChecks declares the commands that answer one of a task's gates.
//
// It exists because the declaration used to be hand-written JSON in the
// registry's metadata (`bd update --metadata '{"luna_gates":...}'`), which is why
// almost no task ever carried one. A gate that can be answered by a command
// should not need a second tool and a schema to say so.
//
// `--run` may be repeated, and the order is kept: the checks run in the order
// they were declared, and the first failure is the answer.
func gateChecks(env Env, id string, args []string) error {
	gate, checks, err := parseGateChecks(args)
	if err != nil {
		return err
	}

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

	declared := fsm.GateChecksDeclared{Gate: gate, Checks: checks}
	if err := env.Store.AppendActionAt(id, state.Seq, declared); err != nil {
		return err
	}

	if len(checks) == 0 {
		fmt.Fprintf(env.Out, "%s: %s has no mechanical answer, so it goes to judgement\n", id, gate)
		return nil
	}
	fmt.Fprintf(env.Out, "%s: %s is answered by %s\n", id, gate, strings.Join(checks, ", "))
	return nil
}

// parseGateChecks reads `--on <gate>` and any number of `--run <command>`.
//
// Hand-rolled rather than going through parseFlags because --run repeats, and a
// map keyed by flag name would silently keep only the last one — which would look
// like the declaration worked and run a third of what was asked.
func parseGateChecks(args []string) (fsm.GateKind, []string, error) {
	var gate fsm.GateKind
	var checks []string

	for i := 0; i < len(args); i++ {
		if i+1 >= len(args) {
			return "", nil, fmt.Errorf("%w: %s needs a value", ErrUsage, args[i])
		}
		value := args[i+1]
		i++

		switch args[i-1] {
		case "--on":
			// Checked against the closed set rather than stored as typed: a gate kind
			// that does not exist would sit in the log answering nothing, and the
			// declaration would look like it took.
			parsed, err := fsm.ParseGateKind(value)
			if err != nil {
				return "", nil, fmt.Errorf("%w: %w", ErrUsage, err)
			}
			gate = parsed
		case "--run":
			checks = append(checks, value)
		default:
			return "", nil, fmt.Errorf("%w: unknown flag %s", ErrUsage, args[i-1])
		}
	}

	if gate == "" {
		return "", nil, fmt.Errorf("%w: luna gate checks <id> --on <gate> [--run <command>]", ErrUsage)
	}
	return gate, checks, nil
}

// blockNote names which kind of block stopped the task, in brackets after the
// operational verdict.
//
// The kind rather than the prose, because this line is the one a person scans: a
// listing where every stopped task says "stopped" answers nothing, and the four
// shapes sort a morning's work — one needs a person, one needs the environment
// fixed, one needs a bigger ceiling, one needs the code to change.
func blockNote(state fsm.TaskState) string {
	if state.BlockedBy == "" {
		return ""
	}
	return fmt.Sprintf(" (%s)", state.BlockedBy)
}

// knobNote says what a knob setting means, because a bare number does not.
//
// The two ends are the ones worth naming: 0 is the default and sends every gate
// to a person, and 10 lets the lead judge all of them. In between, the number is
// only meaningful against `luna flow` — which lists each gate's autonomy floor — so
// that is what it points at.
func knobNote(knob fsm.Knob) string {
	switch knob {
	case fsm.KnobAsk:
		return "  (every gate goes to a person)"
	case fsm.KnobAll:
		return "  (the lead may judge every gate)"
	default:
		return "  (the lead judges gates needing this much autonomy or less — see `luna flow`)"
	}
}

// printLoop reports where a convergence loop stands, and says nothing when there
// is no loop.
//
// The counters existed and were invisible: a task circling without converging
// counted rounds in silence until a ceiling fired, and the first anyone heard of
// it was the gate it opened. Three separate ceilings mean three separate
// pathologies, so they are named separately rather than summed.
//
// `LastProgress` is shown because the audit's question is what was compared — a
// person told two rounds made no progress wants to see what the machine looked at
// before believing it (PRD node-0002, RF3).
func printLoop(env Env, loop fsm.LoopCounters) {
	if loop.Rounds == 0 {
		return
	}

	fmt.Fprintf(env.Out, "  loop     round %d", loop.Rounds)
	if loop.NoProgress > 0 {
		fmt.Fprintf(env.Out, ", %d with no change", loop.NoProgress)
	}
	if loop.Oscillation > 0 {
		fmt.Fprintf(env.Out, ", %d back to a stage already visited", loop.Oscillation)
	}
	fmt.Fprintln(env.Out)

	if loop.LastProgress != "" {
		fmt.Fprintf(env.Out, "  compared %s\n", loop.LastProgress)
	}
}

// printTask writes the form a person reads.
//
// Split from taskShow so the two output shapes stay separable: the structured one
// is a contract and this one is prose, and mixing their construction
// is how they drift.
func printTask(env Env, state fsm.TaskState, events int) {
	fmt.Fprintf(env.Out, "%s  %s%s\n", state.ID, state.Status, simulationNote(state))

	// Two verdicts, side by side and never summed. A task can deliver code that
	// passes every check and still stop badly, and for a run nobody watched that
	// is a failure of the machinery with a good product inside it — which is
	// exactly the distinction one column would hide.
	fmt.Fprintf(env.Out, "  product  %s\n", state.Product())
	fmt.Fprintf(env.Out, "  flow     %s%s\n", state.Operation(), blockNote(state))
	fmt.Fprintf(env.Out, "  kind     %s\n", state.Context.Kind)
	// The knob rather than the profile: the profile decides nothing since
	// retired with the profiles and is kept only so old logs replay, while the knob is what bounds
	// who answers a gate — and it moves through the log, so a replay reproduces
	// every value it held.
	fmt.Fprintf(env.Out, "  autonomy %d%s\n", state.Knob, knobNote(state.Knob))
	if state.Stage != "" {
		fmt.Fprintf(env.Out, "  stage    %s\n", state.Stage)
	}
	if state.Blocked != "" {
		fmt.Fprintf(env.Out, "  blocked  %s\n", state.Blocked)
	}
	fmt.Fprintf(env.Out, "  events   %d\n", events)
	printLoop(env, state.Loop)

	// What the task is about comes out of the log now rather than the registry,
	// so the command that shows a task can show it without a second lookup — and
	// a person can check what the agents were told.
	for _, line := range []struct{ label, value string }{
		{"about", state.Statement.Description},
		{"design", state.Statement.Design},
		{"done", state.Statement.Acceptance},
	} {
		if line.value != "" {
			fmt.Fprintf(env.Out, "  %-8s %s\n", line.label, line.value)
		}
	}

	artifacts := sortedArtifacts(state.Context.Artifacts)
	if len(artifacts) == 0 {
		return
	}

	fmt.Fprintf(env.Out, "\nproduced\n")
	for _, a := range artifacts {
		// Evidence is what the tool reported. Showing it is the
		// difference between knowing a stage closed and knowing on what grounds —
		// and the scope is what separates a green suite from a file that merely
		// exists.
		if evidence := state.Evidence[a]; evidence.Delivered() {
			fmt.Fprintf(env.Out, "  %-16s %s\n", a, evidence)
			continue
		}
		fmt.Fprintf(env.Out, "  %s\n", a)
	}
}

func runGates(env Env, args []string) error {
	asJSON, err := wantsJSON(args)
	if err != nil {
		return err
	}

	waiting, err := env.Store.AwaitingGate()
	if err != nil {
		return err
	}

	if asJSON {
		return writeJSON(env.Out, gatesReport(env.profiles(), waiting))
	}

	if len(waiting) == 0 {
		fmt.Fprintln(env.Out, "nothing waiting")
		return nil
	}

	for _, w := range waiting {
		fmt.Fprintf(env.Out, "%-16s %-14s %s\n", w.TaskID, w.Stage, w.Reason)
		if note := undefinedProfileNote(env.profiles(), w.Profile); note != "" {
			fmt.Fprintf(env.Out, "%-16s %s\n", "", strings.TrimSpace(note))
		}
	}
	return nil
}

// undefinedProfileNote flags a profile the configuration no longer defines.
//
// It happens when a profile is deleted or renamed after tasks have run under it.
// Those tasks replay exactly as they ran — every gate decision they took is in
// their log — but the ones still moving have no policy left to decide
// their next gate, and that is worth saying before someone watches a nightly run
// start stopping at everything.
func undefinedProfileNote(cfg Config, p fsm.Profile) string {
	if p == "" {
		return ""
	}
	if cfg.Defines(p) {
		return ""
	}
	return fmt.Sprintf("  ⚠ no longer defined — remaining gates treated as %s", fsm.ProfileInteractive)
}

func runGate(env Env, args []string) error {
	if len(args) < 2 {
		return fmt.Errorf("%w: gate needs a subcommand and an id", ErrUsage)
	}

	sub, id := args[0], args[1]

	// Declaring checks is about a gate that has not opened yet, so it takes the
	// branch before the one that insists on an open gate.
	if sub == "checks" {
		return gateChecks(env, id, args[2:])
	}

	// The subcommand is checked before the task: a typo in the verb is a usage
	// error whatever state the task is in, and reporting the task's state instead
	// would send someone looking in the wrong place.
	switch sub {
	case "show", "approve", "reject", "adjust":
	default:
		return fmt.Errorf("%w: unknown gate subcommand %q", ErrUsage, sub)
	}

	return answerOpenGate(env, sub, id, args[2:])
}

// answerOpenGate handles the four subcommands that need a gate already waiting.
//
// Split from runGate so the one subcommand that works on a *closed* gate —
// declaring what will answer it — does not have to thread past a guard written
// for the others.
func answerOpenGate(env Env, sub, id string, rest []string) error {
	state, err := env.replay(id)
	if err != nil {
		return err
	}
	if state.Status != fsm.StatusAwaitingGate || state.Gate == nil {
		return fmt.Errorf("task %q is not waiting at a gate (it is %s)", id, state.Status)
	}

	switch sub {
	case "show":
		flow, err := env.flowOf(id)
		if err != nil {
			return err
		}
		return gateShow(env, state, flow)
	case "approve":
		return answer(env, id, fsm.GateApprove{}, "approved")
	case "reject":
		return answer(env, id, fsm.GateReject{Reason: strings.Join(rest, " ")}, "rejected")
	case "adjust":
		return gateAdjust(env, id, state, rest)
	default:
		// Unreachable: runGate already rejected anything else.
		return fmt.Errorf("%w: unknown gate subcommand %q", ErrUsage, sub)
	}
}

// simulationNote marks a task whose stages ran no agent.
//
// On the first line, next to the status, because that is the part a person
// reads. The evidence below it is real — the declared commands did run — but
// nothing was built for them to run against, so a green pipeline here says the
// machinery works and nothing about any code.
//
// Measured on TALLY-6, where a dry run walked a real task to `done` and every
// rendered line was indistinguishable from a genuine run.
func simulationNote(state fsm.TaskState) string {
	if !state.Simulated {
		return ""
	}
	return "  [simulation — no agent ran]"
}

// gateShow is what a person reads before answering a gate.
//
// It shows the criteria on purpose. A `confirm` gate carries no artifact — it
// asks about work that has not run yet — so without them the whole prompt was
// three lines and "approve the plan", and there was nothing on screen to decide
// against. Measured on the first real run in somebody else's repository: the
// gate was approved blind, because approving blind was the only option offered.
//
// What it does not do is fetch the previous stage's delivery. That is a commit
// and possibly a large one, and printing it here would bury the question. The
// command to read it is named instead.
func gateShow(env Env, state fsm.TaskState, flow []fsm.Stage) error {
	gate := state.Gate

	fmt.Fprintf(env.Out, "%s  %s\n", state.ID, gate.Kind)
	fmt.Fprintf(env.Out, "  stage   %s\n", gate.Stage)
	fmt.Fprintf(env.Out, "  waiting %s\n", gate.Reason)

	printGateCriteria(env, state, flow)
	printGateJudgement(env, state)

	if gate.Kind == fsm.GateReviewArtifact {
		fmt.Fprintf(env.Out, "\n%s\n%s\n", gate.Artifact, gate.Payload)

		// An artifact handed to Luna is in the store, so the person deciding gets
		// the thing itself rather than a hash naming it. Before this,
		// the answer to "where is what I should be looking at?" was a line of
		// evidence — measured on a real gate, and it is not something a person can
		// review.
		if blob, err := env.Store.LatestBlob(state.ID, "", string(gate.Artifact)); err == nil {
			fmt.Fprintf(env.Out, "\n%s\n", blob.Body)
		}
		return nil
	}

	// A gate about work ahead has nothing of its own to read, so it points at the
	// last thing that was delivered — which is what the decision is actually made
	// against.
	if state.Base != "" {
		// The whole sha rather than an abbreviation, because the line below is meant
		// to be copied — the same reason `luna next` prints the base in full.
		fmt.Fprintf(env.Out, "\nwhat came before is commit %s\n", state.Base)
		fmt.Fprintf(env.Out, "  git show %s --stat\n", state.Base)
	}
	return nil
}

// printGateCriteria lists what this gate is judged on and what answers it
// mechanically, so a person can see both halves before deciding.
func printGateCriteria(env Env, state fsm.TaskState, flow []fsm.Stage) {
	spec := gateSpecFor(flow, state.Gate.Stage)
	if spec != nil && len(spec.Judge) > 0 {
		fmt.Fprintf(env.Out, "\njudged on\n")
		for _, criterion := range spec.Judge {
			fmt.Fprintf(env.Out, "  - %s\n", criterion)
		}
	}

	checks, declared := state.GateChecks[state.Gate.Kind]
	switch {
	case declared && len(checks) > 0:
		fmt.Fprintf(env.Out, "\nanswered mechanically by\n")
		for _, check := range checks {
			fmt.Fprintf(env.Out, "  $ %s\n", check)
		}
	case declared:
		fmt.Fprintf(env.Out, "\nthis task declared no mechanical answer for %s\n", state.Gate.Kind)
	default:
		fmt.Fprintf(env.Out, "\nnothing declared to answer this mechanically\n")
		fmt.Fprintf(env.Out, "  luna gate checks %s --on %s --run <command>\n", state.ID, state.Gate.Kind)
	}
}

// printGateJudgement shows what the lead concluded, when it was asked.
//
// The verdict alone would be worse than nothing here: a person who reads
// "reject" and not why is being asked to take a model's word for it, which is
// the opposite of what the criterion-by-criterion brief is for. So the working
// is printed whole, and whoever is answering the gate can check it.
func printGateJudgement(env Env, state fsm.TaskState) {
	if state.Gate.Judged == "" {
		return
	}
	fmt.Fprintf(env.Out, "\nthe lead judged this %s\n", state.Gate.Judged)
	if state.Gate.Reasoning != "" {
		fmt.Fprintf(env.Out, "\n%s\n", state.Gate.Reasoning)
	}
	fmt.Fprintf(env.Out, "\nit is a reading, not an answer — the gate is still yours\n")
}

// gateSpecFor finds the gate a stage declares, which is where the criteria live.
// The pending gate carries what a person is being asked; the flow carries what
// they are being asked to judge it on.
func gateSpecFor(flow []fsm.Stage, id fsm.StageID) *fsm.GateSpec {
	for _, stage := range flow {
		if stage.ID == id {
			return stage.Gate
		}
	}
	return nil
}

// gateAdjust applies a human's edit to the artifact a gate is holding.
//
// Four ways in, because the answer does not always come from a person at a
// terminal. An agent driving Luna — or a script — has no editor to open, and an
// interface that only works interactively would push that caller into approving
// something it meant to change.
//
//	--append "text"   add to the end
//	--replace "text"  swap the whole thing
//	--stdin           read the replacement from stdin
//	(none)            open the editor
func gateAdjust(env Env, id string, state fsm.TaskState, args []string) error {
	if state.Gate.Kind != fsm.GateReviewArtifact {
		return fmt.Errorf("the gate on %q carries no artifact to adjust", id)
	}

	// The stored body, not the gate's payload: for an artifact handed over through
	// the socket the payload is the evidence line naming it — "handed over to
	// Luna, 9e727…" — so an editor or an `--append` would start from a hash
	// instead of from the thing being adjusted. `luna gate show` already fetches
	// the body for the same reason.
	current := state.Gate.Payload
	if blob, err := env.Store.LatestBlob(id, "", string(state.Gate.Artifact)); err == nil {
		current = string(blob.Body)
	}

	payload, err := adjustedPayload(env, current, args)
	if err != nil {
		return err
	}

	// An adjustment that changes nothing is how someone says "never mind" —
	// whether they left the editor untouched or passed an empty append. Reading it
	// as approval would put words in their mouth.
	if payload == current {
		fmt.Fprintln(env.Out, "unchanged — nothing was applied")
		return nil
	}

	// The store is where the next stage reads it from, so the correction has to
	// land there too. The reducer records the adjusted payload as evidence, which
	// is the most a pure function can do — and it is not what `luna artifact get`
	// serves. Measured on TALLY-5: a contract adjusted to turn a "should" into a
	// "MUST", the exact weakness the gate had refused it for, and the store went
	// on serving the "should".
	if err := env.Store.PutBlob(store.Blob{
		TaskID:   id,
		Stage:    string(state.Gate.Stage),
		Artifact: string(state.Gate.Artifact),
		Seq:      state.Seq,
		Body:     []byte(payload),
	}); err != nil {
		return fmt.Errorf("recording the adjusted %s: %w", state.Gate.Artifact, err)
	}

	return answer(env, id, fsm.GateAdjust{Payload: payload}, "adjusted")
}

// adjustedPayload works out the new content from however the caller chose to
// supply it.
func adjustedPayload(env Env, current string, args []string) (string, error) {
	flags, err := parseFlags(args)
	if err != nil {
		return "", err
	}

	if len(flags) > 1 {
		return "", fmt.Errorf("%w: choose one of --append, --replace or --stdin", ErrUsage)
	}

	for name, value := range flags {
		switch name {
		case "append":
			// A newline between the two: appending to a contract should not glue
			// the addition onto the last line of what it is commenting on.
			return strings.TrimRight(current, "\n") + "\n" + value + "\n", nil
		case "replace":
			return value, nil
		case "stdin":
			return readAll(env)
		default:
			return "", fmt.Errorf("%w: unknown flag --%s", ErrUsage, name)
		}
	}

	if env.Edit == nil {
		return "", errors.New(
			"no editor configured; set `editor` in .luna/config.toml, or use " +
				"--append/--replace/--stdin",
		)
	}
	return env.Edit(current)
}

// readAll reads the replacement from stdin, which is how a pipeline supplies it.
func readAll(env Env) (string, error) {
	if env.In == nil {
		return "", errors.New("--stdin was given but nothing is connected to read from")
	}

	content, err := io.ReadAll(env.In)
	if err != nil {
		return "", fmt.Errorf("reading the replacement from stdin: %w", err)
	}
	return string(content), nil
}

// answer records a gate response, after checking the reducer accepts it. Writing
// first and discovering the problem at replay would leave a log that cannot be
// rebuilt.
func answer(env Env, id string, action fsm.Action, verb string) error {
	state, err := env.replay(id)
	if err != nil {
		return err
	}
	if _, err := fsm.Reduce(state, action); err != nil {
		return err
	}
	// Conditional on the log still ending where it was read: a gate approved from
	// one terminal while a run advances the same task in another is exactly the
	// case the position-declaring append exists for, and it is the likeliest one — a gate frees the
	// slot, so the approval arrives from somewhere else by design.
	if err := env.Store.AppendActionAt(id, state.Seq, action); err != nil {
		return err
	}

	fmt.Fprintf(env.Out, "%s %s\n", verb, id)
	return nil
}

// valuelessFlags are the switches: present or absent, never `--flag value`.
var valuelessFlags = map[string]bool{
	"stdin": true, "dry-run": true, "json": true, "notify": true, "force": true,
	"adopt": true, "simulated": true,
}

// parseFlags reads --name=value and --name value pairs.
func parseFlags(args []string) (map[string]string, error) {
	flags := map[string]string{}

	for i := 0; i < len(args); i++ {
		arg := args[i]
		if !strings.HasPrefix(arg, "--") {
			return nil, fmt.Errorf("%w: unexpected argument %q", ErrUsage, arg)
		}
		arg = strings.TrimPrefix(arg, "--")

		if name, value, found := strings.Cut(arg, "="); found {
			flags[name] = value
			continue
		}
		// A few flags are switches rather than settings; they carry no value.
		if valuelessFlags[arg] {
			flags[arg] = ""
			continue
		}
		if i+1 >= len(args) {
			return nil, fmt.Errorf("%w: --%s needs a value", ErrUsage, arg)
		}
		i++
		flags[arg] = args[i]
	}
	return flags, nil
}

func parseKind(value string) (fsm.TaskKind, error) {
	switch fsm.TaskKind(value) {
	case fsm.KindFeature, fsm.KindBug, fsm.KindChore, fsm.KindDocs:
		return fsm.TaskKind(value), nil
	default:
		return "", fmt.Errorf("%w: unknown kind %q (feature, bug, chore, docs)", ErrUsage, value)
	}
}

func sortedArtifacts(set map[fsm.Artifact]bool) []fsm.Artifact {
	artifacts := make([]fsm.Artifact, 0, len(set))
	for a, present := range set {
		if present {
			artifacts = append(artifacts, a)
		}
	}
	sort.Slice(artifacts, func(i, j int) bool { return artifacts[i] < artifacts[j] })
	return artifacts
}

// printSpend reports what a task cost, per stage and in total.
//
// It exists because the argument the whole design rests on — that driving work
// through stages beats doing it in one session — was unmeasurable for as long as
// nothing recorded the price. The harness reports it in its own answer, so the
// number costs nothing to keep and everything to be without.
//
// Per stage rather than one total, because the total cannot say which stage is
// expensive and that is the part worth acting on. The context column is here for
// the same reason: fresh and live differ by an order of magnitude, and a cost
// nobody can attribute to a setting cannot settle which setting to use.
func printSpend(env Env, state fsm.TaskState, flow []fsm.Stage) {
	if len(state.Spent) == 0 {
		return
	}

	fmt.Fprintf(env.Out, "\nspent\n")
	// Flow order rather than map order, so the column reads like the run.
	for _, stage := range flow {
		spend, ran := state.Spent[stage.ID]
		if !ran {
			continue
		}
		fmt.Fprintf(env.Out, "  %-14s %8d tokens  $%.4f  %d turns  %s\n",
			stage.ID, spend.Tokens(), spend.CostUSD, spend.Turns, spend.Context)
	}

	total := state.TotalSpend()
	fmt.Fprintf(env.Out, "  %-14s %8d tokens  $%.4f\n", "total", total.Tokens(), total.CostUSD)

	// The ceiling beside the total, because a total on its own does not say whether
	// the task can finish — which is the only question anybody reads this column for
	// on a run nobody watched.
	if state.BudgetUSD > 0 {
		fmt.Fprintf(env.Out, "  %-14s %8s  $%.2f  ($%.4f left)\n",
			"budget", "", state.BudgetUSD, state.BudgetUSD-total.CostUSD)
	}
}

// whichWorkstream reads the two flags that pick a task's durable memory, and says
// whether it handled the flag.
//
// Two flags rather than one, and the second is the only way Luna opens a ledger.
// Inferring "create it" from a name that does not exist would make a typo open a
// second workstream instead of stopping the stage — and a task quietly writing
// somewhere nobody meant is the failure a named workstream exists to prevent.
func whichWorkstream(opts *taskOptions, name, value string) (bool, error) {
	switch name {
	case "workstream":
		opts.memory = fsm.TaskMemory{Workstream: value}
	case "new-workstream":
		opts.memory = fsm.TaskMemory{Workstream: value, MayCreate: true}
	default:
		return false, nil
	}

	if value == "" {
		return true, fmt.Errorf("%w: --%s needs a name", ErrUsage, name)
	}
	return true, nil
}

// namedFlow refuses a flow this build does not carry.
//
// Refused here rather than at the first replay: the name is about to be written
// into an append-only log, and a task opened against a flow that does not exist
// is one no command can read afterwards.
func namedFlow(opts *taskOptions, value string) error {
	if _, err := fsm.FlowNamed(value); err != nil {
		return fmt.Errorf("%w: %w", ErrUsage, err)
	}
	opts.flow = value
	return nil
}
