package cli

import (
	"errors"
	"fmt"
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

// flowCheck reports whether the flow can be changed safely (ADR-0046).
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
// something Luna checks about Luna (ADR-0058).
func reportFlowGaps(env Env, flow []fsm.Stage) {
	contract := fsm.AuditContract(flow)
	roles := fsm.AuditRoles(flow)
	names := fsm.AuditFlowNames(flow)

	if len(contract)+len(roles)+len(names) == 0 {
		fmt.Fprintf(env.Out, "the contract holds: every stage's inputs are produced before it\n")
		return
	}

	for _, gap := range contract {
		fmt.Fprintf(env.Out, "  %s requires %v, which no earlier stage produces\n",
			gap.Stage, gap.Missing)
	}
	for _, gap := range roles {
		fmt.Fprintf(env.Out, "  %s produces %v and names no role — nothing but judgement makes those\n",
			gap.Stage, gap.Produces)
	}
	for _, gap := range names {
		fmt.Fprintf(env.Out, "  %s leaves %d characters for a task id, which is too few for its agent name\n",
			gap.Stage, gap.Budget)
	}
}

func flowCheck(env Env, args []string) error {
	if len(args) > 0 {
		return fmt.Errorf("%w: flow check takes no arguments", ErrUsage)
	}

	flow := fsm.DefaultFlow()
	fmt.Fprintf(env.Out, "flow %s (%d stages) %s\n",
		fsm.Fingerprint(flow), len(flow), stockNote(env.Stock))

	// Whether the flow holds together on paper, before whether anything is in
	// flight. The two questions are different and this command was only asking
	// the second: a flow can have every task finished and still be broken —
	// a stage requiring an artifact nothing produces, a stage that needs
	// judgement and names no role, a stage id long enough to truncate a task's
	// agent name (ADR-0058).
	//
	// It reports rather than refuses, like the rest of this command. Whoever
	// typed it is the one who knows whether a gap matters.
	reportFlowGaps(env, flow)

	ids, err := env.Store.Tasks()
	if err != nil {
		return err
	}

	var open, unreadable []string
	for _, id := range ids {
		state, err := env.Store.Replay(id, flow)
		if errors.Is(err, store.ErrFlowChanged) {
			unreadable = append(unreadable, id)
			continue
		}
		if err != nil {
			return err
		}
		if !state.IsTerminal() {
			open = append(open, fmt.Sprintf("%s (%s)", id, state.Status))
		}
	}

	if len(unreadable) > 0 {
		fmt.Fprintf(env.Out, "\n%d task(s) no longer replay against this flow:\n", len(unreadable))
		for _, id := range unreadable {
			fmt.Fprintf(env.Out, "  %s\n", id)
		}
		fmt.Fprintf(env.Out, "end one with `luna task abandon <id> <reason>`\n")
	}

	if len(open) == 0 {
		fmt.Fprintf(env.Out, "\nno task is open — the flow can change\n")
		return nil
	}

	// Listed rather than refused: this command reports, and whoever runs it
	// decides. A guard that exits non-zero would be a gate, and the person who
	// typed it is the one who knows whether those tasks matter.
	fmt.Fprintf(env.Out, "\n%d task(s) still open:\n  %s\n", len(open), strings.Join(open, "\n  "))
	fmt.Fprintf(env.Out, "\nfinish or abandon them before changing the flow — "+
		"a task whose flow changes under it stops replaying\n")
	return nil
}
