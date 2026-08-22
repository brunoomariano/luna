package cli

import (
	"context"
	"fmt"
	"sort"
	"strconv"
	"sync"
	"time"

	"github.com/brunoomariano/luna/src/internal/fsm"
)

// DefaultConcurrency is how many tasks a fleet runs at once when nobody says.
//
// Four rather than one, because a fleet of one is `luna run` with extra words,
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

// fleetOptions is what a fleet run was asked for.
type fleetOptions struct {
	run         runOptions
	flow        string
	budgetUSD   float64
	concurrency int
}

// fleetRun drives every eligible task, several at a time, until the fleet's own
// ceiling is reached or there is nothing left to start.
//
// Parallelism is between tasks and never inside one, which is not a fleet
// decision but the property the whole design rests on: one worktree and one lead
// per task is what makes two tasks unable to see each other's work.
func fleetRun(env Env, args []string) error {
	opts, err := parseFleetOptions(args)
	if err != nil {
		return err
	}

	eligible, err := eligibleTasks(env, opts.flow)
	if err != nil {
		return err
	}
	if len(eligible) == 0 {
		fmt.Fprintf(env.Out, "nothing to run\n")
		return nil
	}

	fmt.Fprintf(env.Out, "starting %d task(s), %d at a time\n", len(eligible), opts.concurrency)
	landed := driveFleet(context.Background(), env, eligible, opts)

	reportFleetRun(env, landed)
	return nil
}

// landing is where one task ended up, and what it cost getting there.
type landing struct {
	id      string
	state   fsm.TaskState
	err     error
	costUSD float64
}

// driveFleet runs the tasks, bounded by the concurrency limit and by the fleet's
// ceiling.
//
// The ceiling stops *starting* rather than stops running: a task already under
// way is holding a worktree and an agent, and killing it mid-stage would spend
// the money and throw away the delivery. So the fleet overshoots by at most the
// tasks in flight, which is the same shape as a task's own ceiling overshooting
// by one stage, and for the same reason — the bill arrives after the work.
func driveFleet(ctx context.Context, env Env, ids []string, opts fleetOptions) []landing {
	var (
		wg      sync.WaitGroup
		mu      sync.Mutex
		spent   float64
		landed  []landing
		stopped bool
		slots   = make(chan struct{}, opts.concurrency)
	)

	for _, id := range ids {
		// The slot is taken *before* the ceiling is weighed, and the order is the
		// whole correctness of this loop. Checking first would decide while the
		// previous task was still running, against a total that did not yet include
		// it — so a fleet of one would always start one task too many. Waiting for
		// a slot means at least one task has finished and booked its bill, which is
		// the only moment the number is worth reading.
		slots <- struct{}{}

		mu.Lock()
		over := opts.budgetUSD > 0 && spent >= opts.budgetUSD
		if over {
			stopped = true
		}
		mu.Unlock()
		if over {
			<-slots
			break
		}

		wg.Add(1)
		go func(id string) {
			defer wg.Done()
			defer func() { <-slots }()

			state, err := driveTask(ctx, env, id, opts.run)
			cost := state.TotalSpend().CostUSD

			mu.Lock()
			spent += cost
			landed = append(landed, landing{id: id, state: state, err: err, costUSD: cost})
			mu.Unlock()
		}(id)
	}
	wg.Wait()

	if stopped {
		fmt.Fprintf(env.Out, "the fleet's ceiling of $%.2f was reached — "+
			"the tasks it did not start are still eligible\n", opts.budgetUSD)
	}

	sort.Slice(landed, func(i, j int) bool { return landed[i].id < landed[j].id })
	return landed
}

// eligibleTasks is every task a fleet may start: not finished, not called off,
// not waiting on a person, not blocked.
//
// A blocked task is deliberately not eligible. It stopped for a reason somebody
// has to deal with, and a fleet that retried it every night would turn a notified
// block into a nightly bill. `luna unblock` is how it becomes eligible again.
func eligibleTasks(env Env, flow string) ([]string, error) {
	ids, err := env.Store.Tasks()
	if err != nil {
		return nil, err
	}

	var eligible []string
	for _, id := range ids {
		state, err := env.Store.ReplayOwnFlow(id)
		// A task this build cannot read is not one to start. It surfaces by name in
		// `luna flow check`, which is where a person deals with it.
		if err != nil {
			continue
		}
		if state.Operation() != fsm.OperationRunning {
			continue
		}
		if flow != "" && state.FlowName != flow {
			continue
		}
		eligible = append(eligible, id)
	}
	return eligible, nil
}

// reportFleetRun prints where each task landed, which is the product of a night's
// work rather than a side effect of it.
func reportFleetRun(env Env, landed []landing) {
	var total float64
	for _, l := range landed {
		total += l.costUSD
	}

	fmt.Fprintf(env.Out, "\n%-14s %-10s %-22s %9s\n", "task", "product", "flow", "usd")
	for _, l := range landed {
		if l.err != nil {
			fmt.Fprintf(env.Out, "%-14s %-10s %-22s %9s\n", l.id, "?", "error", "—")
			fmt.Fprintf(env.Out, "  %v\n", l.err)
			continue
		}
		fmt.Fprintf(env.Out, "%-14s %-10s %-22s %9.4f\n",
			l.id, l.state.Product(), operationLine(l.state), l.costUSD)
	}
	fmt.Fprintf(env.Out, "%-14s %-10s %-22s %9.4f\n", "total", "", "", total)
}

// operationLine is the operational verdict with the block's kind after it, which
// is what turns a column of "stopped" into a column somebody can act on.
func operationLine(state fsm.TaskState) string {
	if state.BlockedBy == "" {
		return string(state.Operation())
	}
	return fmt.Sprintf("%s (%s)", state.Operation(), state.BlockedBy)
}

// parseFleetOptions reads the flags a fleet run takes, refusing what it does not
// know rather than ignoring it.
func parseFleetOptions(args []string) (fleetOptions, error) {
	opts := fleetOptions{concurrency: DefaultConcurrency}

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
	case "flow":
		if _, err := fsm.FlowNamed(value); err != nil {
			return fmt.Errorf("%w: %w", ErrUsage, err)
		}
		opts.flow = value
	case "budget-usd":
		usd, err := fsm.ParseBudgetUSD(value)
		if err != nil {
			return fmt.Errorf("%w: %w", ErrUsage, err)
		}
		opts.budgetUSD = usd
	case "concurrency":
		n, err := parsePositive(value)
		if err != nil {
			return fmt.Errorf("%w: concurrency %w", ErrUsage, err)
		}
		opts.concurrency = n
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

// parsePositive reads a count that has to be at least one.
//
// Zero is refused rather than read as "no limit": a concurrency of nothing is a
// fleet that starts no task and reports success, which is the silent no-op this
// project treats as worse than an error.
func parsePositive(value string) (int, error) {
	n, err := strconv.Atoi(value)
	if err != nil {
		return 0, fmt.Errorf("has to be a whole number, got %q", value)
	}
	if n < 1 {
		return 0, fmt.Errorf("has to be at least 1, got %d", n)
	}
	return n, nil
}
