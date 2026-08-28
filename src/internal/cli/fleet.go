package cli

import (
	"fmt"
	"time"

	"github.com/brunoomariano/luna/src/internal/fsm"
)

// DefaultConcurrency is how many tasks a fleet runs at once when nobody says.
//
// Four rather than one, because a fleet of one is `luna lead` with extra words,
// and rather than many, because each task holds a worktree, a sandbox and an
// agent subprocess — the limit that bites first is the machine, not the model.
const DefaultConcurrency = 4

// runFleet is the entry point for the commands that work on many tasks at once.
func runFleet(env Env, args []string) error {
	if len(args) == 0 {
		return fmt.Errorf("%w: fleet needs a subcommand (run, report)", ErrUsage)
	}

	switch args[0] {
	case "run":
		return fleetRun(env, args[1:])
	case "report":
		return fleetReport(env, args[1:])
	default:
		return fmt.Errorf("%w: unknown fleet subcommand %q", ErrUsage, args[0])
	}
}

// fleetOptions is what a pack run was asked for.
type fleetOptions struct {
	run     runOptions
	knob    fsm.Knob
	knobSet bool
}

// parseFleetOptions reads the flags a fleet run takes, refusing what it does not
// know rather than ignoring it.
func parseFleetOptions(args []string) (fleetOptions, error) {
	opts := fleetOptions{}

	flags, err := parseFlags(args)
	if err != nil {
		return fleetOptions{}, err
	}

	for name, value := range flags {
		if err := fleetFlag(&opts, name, value); err != nil {
			return fleetOptions{}, err
		}
	}
	return opts, nil
}

// fleetFlag reads one flag into the options, refusing what it does not know.
func fleetFlag(opts *fleetOptions, name, value string) error {
	switch name {
	case "autonomy":
		// The flag governs this invocation; an absent one leaves the task's own
		// recorded knob alone. `--flow` and `--concurrency` are gone with the
		// cross-task fleet: a pack runs the flow its task was opened on, and the
		// concurrency inside it is the pack, which the flow declares.
		knob, err := fsm.ParseKnob(value)
		if err != nil {
			return fmt.Errorf("%w: %w", ErrUsage, err)
		}
		opts.knob = knob
		opts.knobSet = true
	case "repo":
		opts.run.Repo = value
	case "agent":
		opts.run.Agent = value
	case "dry-run":
		opts.run.Dry = true
	default:
		return fmt.Errorf("%w: unknown flag --%s", ErrUsage, name)
	}
	return nil
}

// fleetReport is the morning's answer: what happened, grouped by what it needs.
//
// Grouped rather than listed, because the question a person opens this with is
// "what do I have to do?" and the answer is a handful of piles — these are ready,
// these want a decision, this one wants the machine fixed.
func fleetReport(env Env, args []string) error {
	since, asJSON, err := parseReportOptions(args)
	if err != nil {
		return err
	}

	ids, err := env.Store.Tasks()
	if err != nil {
		return err
	}

	report := FleetReport{Tasks: []FleetTaskReport{}}
	for _, id := range ids {
		state, err := env.Store.ReplayOwnFlow(id)
		if err != nil {
			// Unreadable rather than skipped: a task nobody can replay is exactly
			// the one that would otherwise sit unnoticed forever (INV-5).
			report.Unreadable = append(report.Unreadable, id)
			continue
		}
		if since > 0 {
			at, err := env.Store.LastEventAt(id)
			if err != nil {
				return err
			}
			if at.IsZero() || time.Since(at) > since {
				continue
			}
		}

		report.Tasks = append(report.Tasks, FleetTaskReport{
			ID:        id,
			Flow:      state.FlowName,
			Product:   string(state.Product()),
			Operation: string(state.Operation()),
			BlockedBy: string(state.BlockedBy),
			Blocked:   state.Blocked,
			CostUSD:   state.TotalSpend().CostUSD,
			Tokens:    state.TotalSpend().Tokens(),
		})
		report.CostUSD += state.TotalSpend().CostUSD
	}

	if asJSON {
		return writeJSON(env.Out, report)
	}
	printFleetReport(env, report)
	return nil
}

// printFleetReport writes the morning report for a person.
func printFleetReport(env Env, report FleetReport) {
	if len(report.Tasks) == 0 && len(report.Unreadable) == 0 {
		fmt.Fprintf(env.Out, "nothing to report\n")
		return
	}

	// Grouped by what has to happen next, in the order somebody would act: what is
	// ready to look at, what wants a decision, what is stuck, what is still going.
	for _, group := range []struct {
		heading string
		matches func(FleetTaskReport) bool
	}{
		{"ready", func(t FleetTaskReport) bool { return t.Operation == string(fsm.OperationClean) }},
		{"waiting on a person", func(t FleetTaskReport) bool { return t.Operation == string(fsm.OperationWaiting) }},
		{"stopped", func(t FleetTaskReport) bool { return t.Operation == string(fsm.OperationStopped) }},
		{"still running", func(t FleetTaskReport) bool { return t.Operation == string(fsm.OperationRunning) }},
		{"called off", func(t FleetTaskReport) bool { return t.Operation == string(fsm.OperationCalledOff) }},
	} {
		var in []FleetTaskReport
		for _, task := range report.Tasks {
			if group.matches(task) {
				in = append(in, task)
			}
		}
		printFleetGroup(env, group.heading, in)
	}

	if len(report.Unreadable) > 0 {
		fmt.Fprintf(env.Out, "\nno longer replay (%d)\n", len(report.Unreadable))
		for _, id := range report.Unreadable {
			fmt.Fprintf(env.Out, "  %s\n", id)
		}
	}

	fmt.Fprintf(env.Out, "\nspent $%.4f across %d task(s)\n", report.CostUSD, len(report.Tasks))
}

// printFleetGroup writes one pile of the morning report, or nothing when the pile
// is empty — a heading with no tasks under it is a line somebody reads and learns
// nothing from.
func printFleetGroup(env Env, heading string, tasks []FleetTaskReport) {
	if len(tasks) == 0 {
		return
	}

	fmt.Fprintf(env.Out, "\n%s (%d)\n", heading, len(tasks))
	for _, task := range tasks {
		fmt.Fprintf(env.Out, "  %-14s %-9s %-8s $%.4f", task.ID, task.Product, task.Flow, task.CostUSD)
		if task.BlockedBy != "" {
			fmt.Fprintf(env.Out, "  %s", task.BlockedBy)
		}
		fmt.Fprintln(env.Out)
	}
}

// parseReportOptions reads the two flags the report takes.
func parseReportOptions(args []string) (since time.Duration, asJSON bool, err error) {
	flags, err := parseFlags(args)
	if err != nil {
		return 0, false, err
	}

	for name, value := range flags {
		switch name {
		case "since":
			since, err = time.ParseDuration(value)
			if err != nil {
				return 0, false, fmt.Errorf("%w: --since takes a duration like 12h, got %q", ErrUsage, value)
			}
		case "json":
			asJSON = true
		default:
			return 0, false, fmt.Errorf("%w: unknown flag --%s", ErrUsage, name)
		}
	}
	return since, asJSON, nil
}

// fleetRun conducts one task with the pack its flow declares.
//
// A pack is internal to the task: the lead conducts, and the flow's roles do the
// work — one worktree and one session each, kept across the stages that role
// owns. That is what the pack buys and what `luna lead` cannot have, because a
// single agent is a single session.
//
// The size of the pack is the flow's, not a flag's. `chore` declares one working
// role, `fix` two, `full` five — so choosing the flow chooses the depth, which is
// the same shape SwarmForge gives its two-, four- and six-packs.
func fleetRun(env Env, args []string) error {
	if len(args) == 0 {
		return fmt.Errorf("%w: fleet run needs a task id", ErrUsage)
	}
	id := args[0]

	opts, err := parseFleetOptions(args[1:])
	if err != nil {
		return err
	}

	repo := opts.run.Repo
	if repo == "" {
		repo = "."
	}

	// A dry run has no model to conduct with and no agent to dispatch to, so it
	// takes the node-driven loop like `luna lead --dry-run` does. It exercises the
	// flow, which is what it is for.
	if opts.run.Dry {
		opts.run.Repo = repo
		opts.run.Knob = opts.knob
		opts.run.KnobSet = opts.knobSet
		return dryRun(env, id, opts.run)
	}

	state, err := packRun(env, id, repo, opts.knob, opts.knobSet)
	if err != nil {
		return err
	}
	reportSoloEnding(env, id, state)
	return nil
}

// packStages is what a flow's pack is made of, in the order the flow meets them.
//
// Reported rather than configured: it is a reading of the flow and never a second
// place that could disagree with it.
//
// Counted by distinct brief, which is what a pack member actually is. It counted
// distinct role names until the briefs moved onto the stages, and then it
// undercounted: `verify` and `audit` share a role, are told different things, and
// the flow reported five members where seven agents run.
func packStages(flow []fsm.Stage) []fsm.StageID {
	var members []fsm.StageID
	seen := map[string]bool{}

	for _, stage := range flow {
		if stage.Mechanical() || seen[stage.Brief] {
			continue
		}
		seen[stage.Brief] = true
		members = append(members, stage.ID)
	}
	return members
}
