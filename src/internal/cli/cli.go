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
	Out   io.Writer
	Err   io.Writer

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

  luna gate adjust <id>
        edit the artifact under review, then accept the edited version

  luna gate reject <id> [reason]
        refuse the artifact; the stage that produced it runs again

kinds:    feature, bug, chore, docs
profiles: interactive (default), turbo, nightly
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

	kind, profile, err := parseTaskOptions(args[1:])
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
func parseTaskOptions(args []string) (fsm.TaskKind, fsm.Profile, error) {
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
			if profile, err = parseProfile(value); err != nil {
				return "", "", err
			}
		default:
			return "", "", fmt.Errorf("%w: unknown flag --%s", ErrUsage, name)
		}
	}
	return kind, profile, nil
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
	fmt.Fprintf(env.Out, "  profile  %s\n", state.Profile)
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
	}
	return nil
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
		return gateAdjust(env, id, state)
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

func gateAdjust(env Env, id string, state fsm.TaskState) error {
	if state.Gate.Kind != fsm.GateReviewArtifact {
		return fmt.Errorf("the gate on %q carries no artifact to adjust", id)
	}
	if env.Edit == nil {
		return errors.New("no editor available; set $EDITOR or use approve/reject")
	}

	edited, err := env.Edit(state.Gate.Payload)
	if err != nil {
		return err
	}

	// Leaving the editor without changing anything is how a person says "never
	// mind". Treating it as an approval would put words in their mouth.
	if edited == state.Gate.Payload {
		fmt.Fprintln(env.Out, "unchanged — nothing was applied")
		return nil
	}

	return answer(env, id, fsm.GateAdjust{Payload: edited}, "adjusted")
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

// parseProfile rejects an unknown name here rather than letting it reach the
// engine. The engine treats what it does not recognise as interactive, which is
// the safe guess — but a typo silently costing someone their nightly run is worth
// catching at the edge, where the name was typed.
func parseProfile(value string) (fsm.Profile, error) {
	switch fsm.Profile(value) {
	case fsm.ProfileInteractive, fsm.ProfileTurbo, fsm.ProfileNightly:
		return fsm.Profile(value), nil
	default:
		return "", fmt.Errorf("%w: unknown profile %q (interactive, turbo, nightly)", ErrUsage, value)
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
