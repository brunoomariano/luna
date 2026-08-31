package cli

import (
	"fmt"
	"strings"

	"github.com/brunoomariano/luna/src/internal/fsm"
)

// budgetCommand shows or moves a task's spending ceiling.
//
// The same shape as `luna autonomy`, and for the same reason: the ceiling is a
// term the run happens under, it changes mid-run, and the change is a decision.
// A value re-read from configuration at each step could not say when it moved or
// why — and "why did this task cost $40?" is exactly the question the log has to
// answer for an unattended run.
func budgetCommand(env Env, args []string) error {
	env, id, rest, err := taskFrom(env, args)
	if err != nil {
		return err
	}

	state, err := env.replay(id)
	if err != nil {
		return err
	}

	if len(rest) == 0 {
		showBudget(env, state)
		return nil
	}
	return moveBudget(env, state, rest[0], strings.Join(rest[1:], " "))
}

// showBudget reports the ceiling, what has gone against it, and what is left.
//
// All three, because a ceiling on its own answers nothing a person is asking. The
// question behind the command is always "can this finish?", and that needs the
// remainder rather than the limit.
func showBudget(env Env, state fsm.TaskState) {
	spent := state.TotalSpend().CostUSD

	if state.BudgetUSD <= 0 {
		fmt.Fprintf(env.Out, "%s has no budget — spent $%.4f so far\n", state.ID, spent)
		fmt.Fprintf(env.Out, "  set one with `luna budget %s <usd>`\n", state.ID)
		return
	}

	fmt.Fprintf(env.Out, "%s budget $%.2f — spent $%.4f, $%.4f left\n",
		state.ID, state.BudgetUSD, spent, state.BudgetUSD-spent)

	// Said here because it is the part that surprises: the ceiling stops the *next*
	// stage, so a task can be over it and still hold a delivery that was paid for.
	if spent > state.BudgetUSD {
		fmt.Fprintf(env.Out,
			"  it is over, so no further stage opens — raise it and `luna unblock %s`\n", state.ID)
	}
}

// moveBudget writes the change to the log.
func moveBudget(env Env, state fsm.TaskState, value, reason string) error {
	usd, err := fsm.ParseBudgetUSD(value)
	if err != nil {
		return fmt.Errorf("%w: %w", ErrUsage, err)
	}

	if err := env.Store.AppendAction(state.ID, fsm.SetBudget{BudgetUSD: usd, Reason: reason}); err != nil {
		return err
	}

	fmt.Fprintf(env.Out, "%s budget $%.2f → $%.2f\n", state.ID, state.BudgetUSD, usd)

	// Raising the ceiling does not restart anything. Saying so is what stops the
	// next question being "I raised it, why is it still stopped?".
	if state.Status == fsm.StatusBlocked {
		fmt.Fprintf(env.Out, "the task is still blocked — `luna unblock %s` resumes it\n", state.ID)
	}
	return nil
}
