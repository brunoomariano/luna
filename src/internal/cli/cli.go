// Package cli is the surface a person uses.
//
// It is deliberately thin: every command reads or writes the store and prints.
// Nothing here decides flow — that is the engine's job — and nothing here runs an
// agent, which belongs to the lead. What it does own is making a suspended task
// discoverable, which is the half of INV-core-12 that no amount of engine work
// can provide.
package cli

import (
	"context"
	"errors"
	"fmt"
	"io"
	"sort"
	"strings"

	"github.com/brunoomariano/luna/src/internal/fsm"
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

	// Notify tells a person a task stopped. Injected for the same reason as Edit:
	// a test must not draw a banner, and a machine with no notifier should print
	// and carry on rather than fail the run (INV-core-8).
	Notify func(ctx context.Context, taskID, reason string) error

	// Interpret is what turns plain language into commands for `luna chat`.
	// Injected because Luna hosts no model, and nil means the command says so
	// rather than pretending to work (ADR-0043).
	Interpret Interpreter

	// Lead is the model that conducts a task for `luna lead`. Injected for the
	// same reason as Interpret, and nil for the same reason: `luna run` drives
	// the same flow without one.
	Lead func(ctx context.Context, prompt string) (string, error)

	// Stock is the project's copy of the stages, roles and profiles — `.luna/stock`
	// (RFC-0003). Empty, or a directory that has none, means the embedded copy is
	// what runs, which is what a project that never ran `luna init` gets.
	Stock string
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
	// Built here rather than as a package variable because `chat` runs Luna
	// commands for you, so its entry refers back to this function — a package-level
	// table would be an initialisation cycle.
	commands := map[string]func(Env, []string) error{
		"task":     runTask,
		"run":      runTaskCommand,
		"unblock":  unblockCommand,
		"chat":     chatCommand,
		"plugin":   pluginCommand,
		"gates":    runGates,
		"gate":     runGate,
		"flow":     runFlow,
		"next":     nextCommand,
		"done":     doneCommand,
		"status":   statusCommand,
		"stuck":    stuckCommand,
		"lead":     leadCommand,
		"autonomy": autonomyCommand,
		"init":     initCommand,
	}

	command, ok := commands[args[0]]
	if !ok {
		return fmt.Errorf("%w: unknown command %q\n%s", ErrUsage, args[0], Usage())
	}
	return command(env, args[1:])
}

// Usage is the help text. It is a function rather than a constant so the command
// list has one home.
func Usage() string {
	return strings.TrimSpace(`
luna — deterministic orchestration for AI agents

  luna task new <id> --kind <kind> [--profile <profile>]
        [--about <what>] [--design <how>] [--acceptance <done when>]
        open a task's log, with what the task is about

  luna task show <id> [--json]
        the task's current state and what it has produced

  luna next <id> [--json]
        the order for this task: which stage, which role, which worktree,
        which base commit, what is denied. It is an instruction, not advice,
        and reading it changes nothing.

  luna done <id> --delivered <a,b> [--commit <sha>]
        report the running stage finished, and hand in the commit it
        produced. Luna checks the delivery against the contract — a stage
        that owed more than it delivered does not close.

  luna status <id> [--json]
        the whole flow and where the task stands in it. Separate from
        next on purpose: an order carries no view of what comes after it.

  luna run <id> [--agent <kind>] [--dry-run]
        drive the task until it needs a person or finishes.
        --dry-run exercises the flow with no herdr and no agent.

  luna lead <id> [--autonomy 0-10]
        hand the task to the lead agent: Luna gives it one order at a
        time and it carries them out. It never chooses a stage — the
        autonomy knob bounds which gates it may answer and what it may
        do about a failure. 0 judges nothing, and is the default.

  luna autonomy <id> [<0-10> [reason]]
        show the knob, or move it mid-run. Moving it writes an event, so
        the log says when it changed and why. A gate already open still
        goes to a person; only later gates see the new value.

  luna task abandon <id> <reason>
        end a task that will not be finished. The log keeps everything —
        abandoning records that a person called it off, and why.

  luna unblock <id>
        clear a block once whatever caused it is dealt with

  luna init [--force]
        copy the stages, roles and profiles into .luna/stock so this
        project can edit them. Until then the shipped ones run.

  luna flow check
        what flow this build carries, and whether anything is open.
        Changing the flow under an open task stops it replaying.

  luna chat
        say what you want in plain language. It runs Luna commands for you
        and reads the answers back — it never decides a stage, and it asks
        before approving a gate.

  luna plugin install | uninstall | status
        register luna chat as a herdr pane, so the conversation sits
        beside the tasks it is about. Optional: chat works in any terminal.

  luna gates [--json]
        every task waiting on a person

  luna stuck [--for <duration>] [--notify] [--json]
        what has been stopped for too long — a blocked merge, a gate
        nobody answered. Defaults to an hour. --notify tells a person
        instead of only whoever ran the command.

  luna gate show <id>
        what a suspended task is waiting for

  luna gate approve <id>
        accept and carry on

  luna gate adjust <id> [--append <text> | --replace <text> | --stdin]
        change the artifact under review, then accept the changed version.
        With no flag, opens the editor. The flags exist so an agent or a
        script can answer a gate without a terminal.

  luna gate reject <id> [reason]
        refuse the artifact; the stage that produced it runs again

  luna gate checks <id> --on <gate> [--run <command>]...
        declare the commands that answer a gate mechanically. They run
        against what the stage delivered, and the first failure is the
        answer. With no --run, the gate is declared to have no mechanical
        answer and goes to judgement.

  luna task statement <id> [--about ...] [--design ...] [--acceptance ...]
        correct what a task is about. The previous wording stays in the
        log — a revision is an event, not an overwrite.

kinds:    feature, bug, chore, docs
profiles: interactive (default), turbo, nightly, plus any the project
          defines in .luna/config.toml
`)
}

func runTask(env Env, args []string) error {
	if len(args) == 0 {
		return fmt.Errorf("%w: task needs a subcommand (new, show, statement, abandon)", ErrUsage)
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
	default:
		return fmt.Errorf("%w: unknown task subcommand %q", ErrUsage, args[0])
	}
}

// taskAbandon ends a task a person decided not to finish (ADR-0046).
//
// It is the one command that does not replay before acting, and that is the whole
// reason it exists. A task whose flow changed under it no longer replays at all,
// so a command that read the state first could never end the tasks that most need
// ending. It appends against the log's existence instead, and lets the reducer
// refuse on the next read if the task had already finished.
//
// The cost is accepted knowingly: abandoning an already-done task writes an event
// that will fail to replay. That is a worse trade than it sounds only if it can
// happen by accident, and it cannot — nothing reaches this without a person
// typing the id and a reason.
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

	if err := env.Store.AppendAction(id, fsm.Abandon{Reason: reason}); err != nil {
		return err
	}

	fmt.Fprintf(env.Out, "%s abandoned: %s\n", id, reason)
	return nil
}

func taskNew(env Env, args []string) error {
	if len(args) == 0 {
		return fmt.Errorf("%w: task new needs an id", ErrUsage)
	}

	id := args[0]

	// Validated here, at the only place an id enters the system. Everything
	// downstream treats it as safe: it becomes a directory name and part of an
	// agent name, and neither checked (ADR-0037 flagged the first and left the
	// guard unbuilt).
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

	// The flow the task is born under is recorded with it, so a later replay can
	// tell it is being read against a different one (ADR-0046).
	flow := fsm.Fingerprint(fsm.DefaultFlow())
	created := fsm.TaskCreated{
		Kind:      opts.kind,
		Profile:   opts.profile,
		Flow:      flow,
		Statement: opts.stated,
	}
	if err := env.Store.AppendAction(id, created); err != nil {
		return err
	}

	fmt.Fprintf(env.Out, "created %s (kind=%s profile=%s flow=%s)\n", id, opts.kind, opts.profile, flow)
	if opts.stated.Stated() {
		fmt.Fprintf(env.Out, "  about: %s\n", opts.stated.Description)
	}
	return nil
}

// taskStatement records a new statement of work for a task that already exists.
//
// Separate from `task new` because the two answer different questions — "open
// this" and "here is what it turned out to be" — and because a revision is an
// action of its own, so the previous statement stays in the log (ADR-0067).
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

	state, err := env.Store.Replay(id, fsm.DefaultFlow())
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
// engine, because a project defines its own profiles (ADR-0026). The rejection is
// still worth doing here: someone who meant `nightly` hears about the typo,
// instead of getting a supervised run with nothing saying why.
// taskOptions is everything `task new` was told: how to run the task, and what
// the task is about.
type taskOptions struct {
	kind    fsm.TaskKind
	profile fsm.Profile

	// stated is what the task is about, as it was given on the command line. It
	// rides into the log with the task rather than into a registry (ADR-0067).
	stated fsm.Statement
}

// howToRun reads the flags that decide how the task is conducted, as opposed to
// what it is about. An unknown flag lands here because this is the last of the
// three, and it is the one that can say what the valid names are.
func howToRun(opts *taskOptions, cfg Config, name, value string) error {
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
	opts := taskOptions{kind: fsm.KindFeature, profile: fsm.ProfileInteractive}

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

// profiles is the configuration a command should resolve names against, falling
// back to the shipped profiles when nothing was loaded.
//
// The fallback is for tests and for a zero Env, not for a real run: main always
// loads a config, and a missing file already yields the shipped set.
func (e Env) profiles() Config {
	if len(e.Config.Profiles) == 0 {
		return Config{Editor: e.Config.Editor, Profiles: ShippedProfiles(), Roles: ShippedRoles()}
	}
	return e.Config
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

	state, err := env.Store.Replay(id, fsm.DefaultFlow())
	if err != nil {
		return err
	}

	if asJSON {
		return writeJSON(env.Out, taskReport(env.profiles(), state, len(events)))
	}

	printTask(env, state, len(events))
	return nil
}

// gateChecks declares the commands that answer one of a task's gates.
//
// It exists because the declaration used to be hand-written JSON in the
// registry's metadata (`bd update --metadata '{"luna_gates":...}'`), which is why
// almost no task ever carried one. A gate that can be answered by a command
// should not need a second tool and a schema to say so (ADR-0067).
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

	state, err := env.Store.Replay(id, fsm.DefaultFlow())
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

// knobNote says what a knob setting means, because a bare number does not.
//
// The two ends are the ones worth naming: 0 is the default and sends every gate
// to a person, and 10 lets the lead judge all of them. In between, the number is
// only meaningful against `luna flow` — which lists each gate's criticality — so
// that is what it points at.
func knobNote(knob fsm.Knob) string {
	switch knob {
	case fsm.KnobAsk:
		return "  (every gate goes to a person)"
	case fsm.KnobAll:
		return "  (the lead may judge every gate)"
	default:
		return "  (the lead judges gates up to this criticality — see `luna flow`)"
	}
}

// printTask writes the form a person reads.
//
// Split from taskShow so the two output shapes stay separable: the structured one
// is a contract (ADR-0043) and this one is prose, and mixing their construction
// is how they drift.
func printTask(env Env, state fsm.TaskState, events int) {
	fmt.Fprintf(env.Out, "%s  %s\n", state.ID, state.Status)
	fmt.Fprintf(env.Out, "  kind     %s\n", state.Context.Kind)
	// The knob rather than the profile: the profile decides nothing since
	// ADR-0063 and is kept only so old logs replay, while the knob is what bounds
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

	// What the task is about comes out of the log now rather than the registry
	// (ADR-0067), so the command that shows a task can show it without a second
	// lookup — and a person can check what the agents were told.
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
		// Evidence is what the tool reported (ADR-0024). Showing it is the
		// difference between knowing a stage closed and knowing on what grounds —
		// and the scope is what separates a green suite from a file that merely
		// exists (ADR-0032).
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

	waiting, err := env.Store.AwaitingGate(fsm.DefaultFlow())
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
// their log (ADR-0026) — but the ones still moving have no policy left to decide
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
	state, err := env.Store.Replay(id, fsm.DefaultFlow())
	if err != nil {
		return err
	}
	if state.Status != fsm.StatusAwaitingGate || state.Gate == nil {
		return fmt.Errorf("task %q is not waiting at a gate (it is %s)", id, state.Status)
	}

	switch sub {
	case "show":
		return gateShow(env, state, fsm.DefaultFlow())
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

	if gate.Kind == fsm.GateReviewArtifact {
		fmt.Fprintf(env.Out, "\n%s\n%s\n", gate.Artifact, gate.Payload)
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
// mechanically, so a person can see both halves before deciding (RFC-0006).
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

	payload, err := adjustedPayload(env, state.Gate.Payload, args)
	if err != nil {
		return err
	}

	// An adjustment that changes nothing is how someone says "never mind" —
	// whether they left the editor untouched or passed an empty append. Reading it
	// as approval would put words in their mouth.
	if payload == state.Gate.Payload {
		fmt.Fprintln(env.Out, "unchanged — nothing was applied")
		return nil
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
	state, err := env.Store.Replay(id, fsm.DefaultFlow())
	if err != nil {
		return err
	}
	if _, err := fsm.Reduce(state, action); err != nil {
		return err
	}
	// Conditional on the log still ending where it was read: a gate approved from
	// one terminal while a run advances the same task in another is exactly the
	// case ADR-0047 exists for, and it is the likeliest one — a gate frees the
	// slot, so the approval arrives from somewhere else by design (ADR-0012).
	if err := env.Store.AppendActionAt(id, state.Seq, action); err != nil {
		return err
	}

	fmt.Fprintf(env.Out, "%s %s\n", verb, id)
	return nil
}

// valuelessFlags are the switches: present or absent, never `--flag value`.
var valuelessFlags = map[string]bool{
	"stdin": true, "dry-run": true, "json": true, "notify": true, "force": true,
	"adopt": true,
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
