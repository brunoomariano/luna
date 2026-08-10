// Package cli is the surface a person uses.
//
// It is deliberately thin: every command reads or writes the store and prints.
// Nothing here decides flow — that is the engine's job — and nothing here runs an
// agent, which belongs to the lead. What it does own is making a suspended task
// discoverable, which is the half of INV-core-12 that no amount of engine work
// can provide.
package cli

import (
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
}

// Run dispatches a command line. args excludes the program name.
func Run(env Env, args []string) error {
	if len(args) == 0 {
		return fmt.Errorf("%w: %s", ErrUsage, Usage())
	}

	switch args[0] {
	case "task":
		return runTask(env, args[1:])
	case "gates":
		return runGates(env, args[1:])
	case "gate":
		return runGate(env, args[1:])
	case "help", "-h", "--help":
		fmt.Fprintln(env.Out, Usage())
		return nil
	default:
		return fmt.Errorf("%w: unknown command %q\n%s", ErrUsage, args[0], Usage())
	}
}

// Usage is the help text. It is a function rather than a constant so the command
// list has one home.
func Usage() string {
	return strings.TrimSpace(`
luna — deterministic orchestration for AI agents

  luna task new <id> --kind <kind> [--profile <profile>]
        open a task's log

  luna task show <id>
        the task's current state and what it has produced

  luna gates
        every task waiting on a person

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

kinds:    feature, bug, chore, docs
profiles: interactive (default), turbo, nightly, plus any the project
          defines in .luna/config.toml
`)
}

func runTask(env Env, args []string) error {
	if len(args) == 0 {
		return fmt.Errorf("%w: task needs a subcommand (new, show)", ErrUsage)
	}

	switch args[0] {
	case "new":
		return taskNew(env, args[1:])
	case "show":
		return taskShow(env, args[1:])
	default:
		return fmt.Errorf("%w: unknown task subcommand %q", ErrUsage, args[0])
	}
}

func taskNew(env Env, args []string) error {
	if len(args) == 0 {
		return fmt.Errorf("%w: task new needs an id", ErrUsage)
	}

	id := args[0]

	kind, profile, err := parseTaskOptions(env.profiles(), args[1:])
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

	if err := env.Store.AppendAction(id, fsm.TaskCreated{Kind: kind, Profile: profile}); err != nil {
		return err
	}

	fmt.Fprintf(env.Out, "created %s (kind=%s profile=%s)\n", id, kind, profile)
	return nil
}

// parseTaskOptions reads --kind and --profile, defaulting to the cautious pair:
// a feature, supervised. An unstated profile must not run unattended.
//
// The valid names come from the project's configuration rather than a list in the
// engine, because a project defines its own profiles (ADR-0026). The rejection is
// still worth doing here: someone who meant `nightly` hears about the typo,
// instead of getting a supervised run with nothing saying why.
func parseTaskOptions(cfg Config, args []string) (fsm.TaskKind, fsm.Profile, error) {
	kind := fsm.KindFeature
	profile := fsm.ProfileInteractive

	flags, err := parseFlags(args)
	if err != nil {
		return "", "", err
	}

	for name, value := range flags {
		switch name {
		case "kind":
			if kind, err = parseKind(value); err != nil {
				return "", "", err
			}
		case "profile":
			if _, ok := cfg.Profile(fsm.Profile(value)); !ok {
				return "", "", fmt.Errorf("%w: unknown profile %q (%s)",
					ErrUsage, value, strings.Join(cfg.ProfileNames(), ", "))
			}
			profile = fsm.Profile(value)
		default:
			return "", "", fmt.Errorf("%w: unknown flag --%s", ErrUsage, name)
		}
	}
	return kind, profile, nil
}

// profiles is the configuration a command should resolve names against, falling
// back to the shipped profiles when nothing was loaded.
//
// The fallback is for tests and for a zero Env, not for a real run: main always
// loads a config, and a missing file already yields the shipped set.
func (e Env) profiles() Config {
	if len(e.Config.Profiles) == 0 {
		return Config{Editor: e.Config.Editor, Profiles: ShippedProfiles()}
	}
	return e.Config
}

func taskShow(env Env, args []string) error {
	if len(args) == 0 {
		return fmt.Errorf("%w: task show needs an id", ErrUsage)
	}
	id := args[0]

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

	fmt.Fprintf(env.Out, "%s  %s\n", state.ID, state.Status)
	fmt.Fprintf(env.Out, "  kind     %s\n", state.Context.Kind)
	fmt.Fprintf(env.Out, "  profile  %s%s\n", state.Profile, undefinedProfileNote(env.profiles(), state.Profile))
	if state.Stage != "" {
		fmt.Fprintf(env.Out, "  stage    %s\n", state.Stage)
	}
	if state.Blocked != "" {
		fmt.Fprintf(env.Out, "  blocked  %s\n", state.Blocked)
	}
	fmt.Fprintf(env.Out, "  events   %d\n", len(events))

	if artifacts := sortedArtifacts(state.Context.Artifacts); len(artifacts) > 0 {
		fmt.Fprintf(env.Out, "\nproduced\n")
		for _, a := range artifacts {
			// Evidence is what the tool reported (ADR-0024). Showing it is the
			// difference between knowing a stage closed and knowing on what grounds.
			if evidence := state.Evidence[a]; evidence != "" {
				fmt.Fprintf(env.Out, "  %-16s %s\n", a, evidence)
				continue
			}
			fmt.Fprintf(env.Out, "  %s\n", a)
		}
	}
	return nil
}

func runGates(env Env, args []string) error {
	if len(args) > 0 {
		return fmt.Errorf("%w: gates takes no arguments", ErrUsage)
	}

	waiting, err := env.Store.AwaitingGate(fsm.DefaultFlow())
	if err != nil {
		return err
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
	if _, ok := cfg.Profile(p); ok {
		return ""
	}
	return fmt.Sprintf("  ⚠ no longer defined — remaining gates treated as %s", fsm.ProfileInteractive)
}

func runGate(env Env, args []string) error {
	if len(args) < 2 {
		return fmt.Errorf("%w: gate needs a subcommand and an id", ErrUsage)
	}

	sub, id := args[0], args[1]

	// The subcommand is checked before the task: a typo in the verb is a usage
	// error whatever state the task is in, and reporting the task's state instead
	// would send someone looking in the wrong place.
	switch sub {
	case "show", "approve", "reject", "adjust":
	default:
		return fmt.Errorf("%w: unknown gate subcommand %q", ErrUsage, sub)
	}

	state, err := env.Store.Replay(id, fsm.DefaultFlow())
	if err != nil {
		return err
	}
	if state.Status != fsm.StatusAwaitingGate || state.Gate == nil {
		return fmt.Errorf("task %q is not waiting at a gate (it is %s)", id, state.Status)
	}

	switch sub {
	case "show":
		return gateShow(env, state)
	case "approve":
		return answer(env, id, fsm.GateApprove{}, "approved")
	case "reject":
		return answer(env, id, fsm.GateReject{Reason: strings.Join(args[2:], " ")}, "rejected")
	case "adjust":
		return gateAdjust(env, id, state, args[2:])
	default:
		// Unreachable: the switch above already rejected anything else.
		return fmt.Errorf("%w: unknown gate subcommand %q", ErrUsage, sub)
	}
}

func gateShow(env Env, state fsm.TaskState) error {
	gate := state.Gate

	fmt.Fprintf(env.Out, "%s  %s\n", state.ID, gate.Kind)
	fmt.Fprintf(env.Out, "  stage   %s\n", gate.Stage)
	fmt.Fprintf(env.Out, "  waiting %s\n", gate.Reason)

	if gate.Kind == fsm.GateReviewArtifact {
		fmt.Fprintf(env.Out, "\n%s\n%s\n", gate.Artifact, gate.Payload)
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
	if err := env.Store.AppendAction(id, action); err != nil {
		return err
	}

	fmt.Fprintf(env.Out, "%s %s\n", verb, id)
	return nil
}

// valuelessFlags are the switches: present or absent, never `--flag value`.
var valuelessFlags = map[string]bool{"stdin": true}

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
