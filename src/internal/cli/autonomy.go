package cli

import (
	"fmt"
	"strings"

	"github.com/brunoomariano/luna/src/internal/fsm"
)

// autonomyCommand shows or moves a task's knob.
//
// A command that writes an event rather than a setting read from configuration,
// because the change is itself a decision: a run where the lead judged three
// gates has to be reviewable afterwards, and a value that moved with no record
// makes "why was nobody asked here?" unanswerable.
//
// It is also the surface anything outside Luna drives the knob through, `luna
// chat` included. Whatever changes the value changes it by running this command
// and writing this event — a proxy over the log, never a path around it.
func autonomyCommand(env Env, args []string) error {
	env, id, rest, err := taskFrom(env, args)
	if err != nil {
		return err
	}

	state, err := env.replay(id)
	if err != nil {
		return err
	}

	if len(rest) == 0 {
		return showAutonomy(env, state)
	}
	return moveAutonomy(env, state, rest[0], strings.Join(rest[1:], " "))
}

// showAutonomy reports where the knob stands and what that reaches.
//
// The number alone is not an answer — "5" means nothing without the gates it
// absorbs — so the reading names the behaviour on both halves: which gates the
// lead may judge, and what it does about a failure.
func showAutonomy(env Env, state fsm.TaskState) error {
	fmt.Fprintf(env.Out, "%s autonomy %d\n", state.ID, state.Knob)

	if state.Knob == fsm.KnobAsk {
		fmt.Fprintf(env.Out, "  gates:    every gate with criteria goes to a person\n")
	} else {
		fmt.Fprintf(env.Out, "  gates:    the lead judges gates needing autonomy %d or less\n", state.Knob)
	}
	fmt.Fprintf(env.Out, "  failures: retry once, then %s\n", state.Knob.Autonomy())

	// Said plainly because it is the counter-intuitive half: the most autonomous
	// setting still stops when the lead cannot honestly conclude.
	if state.Knob == fsm.KnobAll {
		fmt.Fprintf(env.Out,
			"\neven here a gate reaches a person when the lead cannot decide from what it has\n")
	}
	return nil
}

// moveAutonomy writes the change to the log.
func moveAutonomy(env Env, state fsm.TaskState, value, reason string) error {
	knob, err := fsm.ParseKnob(value)
	if err != nil {
		return fmt.Errorf("%w: %w", ErrUsage, err)
	}

	if err := env.Store.AppendAction(state.ID, fsm.SetKnob{Knob: knob, Reason: reason}); err != nil {
		return err
	}

	fmt.Fprintf(env.Out, "%s autonomy %d → %d\n", state.ID, state.Knob, knob)

	// An open gate keeps the answer it opened with, and saying so here is what
	// stops the next question being "I raised it, why did it still ask me?".
	if state.Gate != nil {
		fmt.Fprintf(env.Out,
			"the gate already open at %s still goes to a person — only later gates see this\n",
			state.Gate.Stage)
	}
	return nil
}
