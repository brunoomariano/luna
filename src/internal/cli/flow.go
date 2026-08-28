package cli

import (
	"errors"
	"fmt"
	"sort"
	"strings"

	"github.com/brunoomariano/luna/src/internal/fsm"
	"github.com/brunoomariano/luna/src/internal/store"
)

// runFlow answers questions about the flow this build carries.
func runFlow(env Env, args []string) error {
	if len(args) == 0 {
		return fmt.Errorf("%w: flow needs a subcommand (check)", ErrUsage)
	}

	switch args[0] {
	case "check":
		return flowCheck(env, args[1:])
	default:
		return fmt.Errorf("%w: unknown flow subcommand %q", ErrUsage, args[0])
	}
}

// flowCheck reports whether the flow can be changed safely.
//
// Changing the flow while a task is open rewrites how that task's history reads,
// so the rule is to stop first and confirm nothing is in flight. This is the
// command that answers "is anything in flight", and it is the primary defence —
// the fingerprint in the log is the second line, for when the rule was not
// followed.
//
// It also names the tasks that already cannot be replayed against this build,
// because those are exactly the ones somebody has to decide about before the next
// change compounds the problem.
// reportFlowGaps runs the three static checks and prints what they found.
//
// They were written with the engine and never called from anywhere but their own
// tests — the flow was audited in the test suite and never by the command whose
// name says it checks the flow. Wiring them here is what makes the contract's
// paper check something a project can run against its own flow, rather than
// something Luna checks about Luna.
func reportFlowGaps(env Env, flow []fsm.Stage) {
	contract := fsm.AuditContract(flow)
	agents := fsm.AuditAgents(flow)
	names := fsm.AuditFlowNames(flow)
	contexts := fsm.AuditContextChain(flow)
	criteria := fsm.AuditGateCriteria(flow)

	if len(contract)+len(agents)+len(names)+len(contexts)+len(criteria) == 0 {
		fmt.Fprintf(env.Out, "the contract holds: every stage's inputs are produced before it\n")
		return
	}

	for _, gap := range contract {
		fmt.Fprintf(env.Out, "  %s requires %v, which no earlier stage produces\n",
			gap.Stage, gap.Missing)
	}
	for _, gap := range agents {
		fmt.Fprintf(env.Out, "  %s produces %v and names no agent — nothing but judgement makes those\n",
			gap.Stage, gap.Produces)
	}
	for _, gap := range names {
		fmt.Fprintf(env.Out, "  %s is long enough that it leaves only %d characters for a task id\n",
			gap.Stage, gap.Budget)
	}
	for _, gap := range criteria {
		fmt.Fprintf(env.Out, "  %s declares %q readable, and judges no criterion by that name\n",
			gap.Stage, gap.Criterion)
	}
	for _, gap := range contexts {
		if gap.From == "" {
			fmt.Fprintf(env.Out, "  %s asks to continue a session and is the first stage — there is none to continue\n",
				gap.Stage)
			continue
		}
		fmt.Fprintf(env.Out, "  %s asks to continue %s, which is briefed differently — a session does not cross that\n",
			gap.Stage, gap.From)
	}
}

// reportGates lists every gate in the flow and what it takes to answer it.
//
// It exists because the knob is a number, and a number is meaningless without the
// scale it is compared against. A person deciding whether to run at 5 needs to see
// which gates that reaches — and reading fourteen stage files to find out is how a
// setting gets chosen by guess.
//
// The mechanical half is deliberately absent here: checks are declared per task in
// the registry, not in the flow, so this command cannot know them. Saying so is
// better than implying a gate has no checks because this view cannot see them.
func reportGates(env Env, flow []fsm.Stage) {
	var gated []fsm.Stage
	for _, stage := range flow {
		if stage.Gate != nil {
			gated = append(gated, stage)
		}
	}

	if len(gated) == 0 {
		fmt.Fprintf(env.Out, "\nno stage opens a gate — nothing stops for a person\n")
		return
	}

	fmt.Fprintf(env.Out, "\n%d gate(s), and the knob that reaches each:\n", len(gated))
	for _, stage := range gated {
		gate := stage.Gate
		fmt.Fprintf(env.Out, "  %-12s %-16s needs autonomy %2d → knob %d+",
			stage.ID, gate.Kind, gate.Resolved(), gate.Resolved())

		if gate.AutonomyFloor == 0 {
			fmt.Fprintf(env.Out, " (undeclared, so the highest)")
		}
		if len(gate.Judge) == 0 {
			fmt.Fprintf(env.Out, ", nothing to judge")
		} else {
			fmt.Fprintf(env.Out, ", %d criteri%s", len(gate.Judge),
				map[bool]string{true: "on", false: "a"}[len(gate.Judge) == 1])
		}
		fmt.Fprintln(env.Out)
	}
	fmt.Fprintf(env.Out, "checks are declared per task — `luna gate checks <id> --on <gate>` — not here\n")
}

// flowsToCheck reads which flows the check was asked about.
//
// Every flow by default, because a build that runs several has no single "the
// flow" and checking one of them would leave the others unaudited — which is the
// state this command exists to prevent.
func flowsToCheck(args []string) ([]string, error) {
	flags, err := parseFlags(args)
	if err != nil {
		return nil, err
	}
	for name := range flags {
		if name != "flow" {
			return nil, fmt.Errorf("%w: unknown flag --%s", ErrUsage, name)
		}
	}
	if only, asked := flags["flow"]; asked {
		if _, err := fsm.FlowNamed(only); err != nil {
			return nil, fmt.Errorf("%w: %w", ErrUsage, err)
		}
		return []string{only}, nil
	}
	return fsm.FlowNames(), nil
}

func flowCheck(env Env, args []string) error {
	names, err := flowsToCheck(args)
	if err != nil {
		return err
	}
	if err := auditFlows(env, names); err != nil {
		return err
	}

	open, unreadable, err := surveyTasks(env)
	if err != nil {
		return err
	}
	reportTaskSurvey(env, open, unreadable)
	return nil
}

// auditFlows reports what each named flow is and whether it holds together.
//
// Whether a flow holds together on paper is a different question from whether
// anything is in flight, and this command was only asking the second: a flow can
// have every task finished and still be broken — a stage requiring an artifact
// nothing produces, a stage that needs judgement and names no role, a stage id
// long enough to truncate a task's agent name.
//
// It reports rather than refuses, like the rest of this command. Whoever typed it
// is the one who knows whether a gap matters.
func auditFlows(env Env, names []string) error {
	for i, name := range names {
		if i > 0 {
			fmt.Fprintln(env.Out)
		}
		flow, err := fsm.FlowNamed(name)
		if err != nil {
			return err
		}
		fmt.Fprintf(env.Out, "flow %s/%s (%d stages)\n",
			name, fsm.Fingerprint(flow), len(flow))

		// The pack, because it is the second thing a person choosing a flow needs
		// and it is a reading of the flow rather than a setting beside it: how many
		// agents `luna fleet run` will keep, and what each one is for. `luna lead`
		// collapses all of them onto one.
		if members := packStages(flow); len(members) > 0 {
			names := make([]string, len(members))
			for i, id := range members {
				names[i] = string(id)
			}
			fmt.Fprintf(env.Out, "pack of %d: %s — `luna lead` runs the same flow with one\n",
				len(members), strings.Join(names, ", "))
		}

		reportWhatItCost(env, name, flow)
		reportFlowGaps(env, flow)
		reportGates(env, flow)
	}
	return nil
}

// surveyTasks splits every task in the store into the ones still open and the
// ones that no longer replay at all.
//
// Each against its own flow, which is what makes the listing true once a build
// runs several: replaying every task against one of them would report every task
// on any other flow as unreadable, and "unreadable" is the word this command uses
// for a task somebody has to go and end.
func surveyTasks(env Env) (open, unreadable []string, err error) {
	ids, err := env.Store.Tasks()
	if err != nil {
		return nil, nil, err
	}

	for _, id := range ids {
		state, err := env.Store.ReplayOwnFlow(id)
		if errors.Is(err, store.ErrFlowChanged) {
			unreadable = append(unreadable, id)
			continue
		}
		if err != nil {
			return nil, nil, err
		}
		if !state.IsTerminal() {
			open = append(open, fmt.Sprintf("%s (%s on %s)", id, state.Status, state.FlowName))
		}
	}
	return open, unreadable, nil
}

// reportTaskSurvey prints what the survey found.
//
// Listed rather than refused: this command reports, and whoever runs it decides.
// A guard that exited non-zero would be a gate, and the person who typed it is the
// one who knows whether those tasks matter.
func reportTaskSurvey(env Env, open, unreadable []string) {
	if len(unreadable) > 0 {
		fmt.Fprintf(env.Out, "\n%d task(s) no longer replay against the flow they name:\n", len(unreadable))
		for _, id := range unreadable {
			fmt.Fprintf(env.Out, "  %s\n", id)
		}
		fmt.Fprintf(env.Out, "end one with `luna task abandon <id> <reason>`\n")
	}

	if len(open) == 0 {
		fmt.Fprintf(env.Out, "\nno task is open — the flow can change\n")
		return
	}

	fmt.Fprintf(env.Out, "\n%d task(s) still open:\n  %s\n", len(open), strings.Join(open, "\n  "))
	fmt.Fprintf(env.Out, "\nfinish or abandon them before changing the flow — "+
		"a task whose flow changes under it stops replaying\n")
}

// reportWhatItCost says what this flow has cost before, per stage, from this
// project's own log.
//
// The most expensive decision here is irreversible: `--flow` cannot change after
// a task opens, and the three differ by a factor nobody can guess. What existed
// was a note in a skill — $0.88 on the right task class against $2.88 on the
// wrong one — and a tool that knew neither, so the number lived in prose while
// the decision was made in the terminal.
//
// The median rather than the mean, because one runaway stage moves a mean and
// says nothing about the next run. Nothing is printed when the log has no
// finished stage to read: an estimate from no observations is a number somebody
// would plan against.
func reportWhatItCost(env Env, name string, flow []fsm.Stage) {
	spent := pastSpend(env, name)
	if len(spent) == 0 {
		return
	}

	var total float64
	lines := make([]string, 0, len(flow))
	for _, stage := range flow {
		costs := spent[stage.ID]
		if len(costs) == 0 {
			continue
		}
		median := medianOf(costs)
		total += median
		lines = append(lines, fmt.Sprintf("  %-10s $%.4f  (%d run%s)",
			stage.ID, median, len(costs), plural(len(costs))))
	}
	if len(lines) == 0 {
		return
	}

	fmt.Fprintf(env.Out, "what it has cost here: $%.4f a task, median by stage\n", total)
	for _, line := range lines {
		fmt.Fprintln(env.Out, line)
	}
}

// pastSpend collects what each stage of a flow cost, across every task that ran
// it in this project.
func pastSpend(env Env, name string) map[fsm.StageID][]float64 {
	ids, err := env.Store.Tasks()
	if err != nil {
		return nil
	}

	spent := map[fsm.StageID][]float64{}
	for _, id := range ids {
		ran, err := env.Store.FlowNameOf(id)
		if err != nil || ran != name {
			continue
		}
		state, err := env.Store.ReplayOwnFlow(id)
		if err != nil {
			// A task this build cannot read is not one to average. It surfaces by
			// name elsewhere in this command, which is where a person deals with it.
			continue
		}
		for stage, spend := range state.Spent {
			if spend.CostUSD > 0 {
				spent[stage] = append(spent[stage], spend.CostUSD)
			}
		}
	}
	return spent
}

// medianOf is the middle observation, which is what survives one runaway stage.
func medianOf(costs []float64) float64 {
	sorted := append([]float64{}, costs...)
	sort.Float64s(sorted)

	middle := len(sorted) / 2
	if len(sorted)%2 == 1 {
		return sorted[middle]
	}
	return (sorted[middle-1] + sorted[middle]) / 2
}

func plural(n int) string {
	if n == 1 {
		return ""
	}
	return "s"
}
