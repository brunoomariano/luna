package cli

import (
	"bufio"
	"errors"
	"fmt"
	"strings"
)

// Interpreter turns what a person said into a Luna command, and turns Luna's
// answer back into something readable.
//
// It is where a model lives, and the boundary is the point: it reads intent and
// picks a command, it reads state and phrases it. It never chooses a stage, never
// answers a gate, never declares a stage complete, and never writes to the log —
// every transition still goes through the reducer, which refuses an illegal one
// whoever proposed it.
//
// An interface rather than a call to a model so the loop can be tested without
// one, and so a project can put its own interpreter behind it.
type Interpreter interface {
	// Interpret turns a person's words into the command Luna should run, given
	// what Luna currently reports. An empty command means the interpreter had
	// nothing to run and Reply carries the answer.
	Interpret(said string, state string) (Intent, error)

	// Phrase turns a command's output into an answer for the person.
	Phrase(said string, command []string, output string) (string, error)
}

// Intent is what the interpreter understood.
type Intent struct {
	// Command is the Luna command to run, already split. Empty means nothing to
	// run — the interpreter is answering from what it already knows.
	Command []string

	// Reply is what to say when there is no command, or nothing to add after one.
	Reply string
}

// NeedsConfirmation reports the one write a person has to authorise.
//
// `gate approve` is not a command that happens to write: it is the statement "a
// human looked", and the log records it indistinguishably from the person having
// read the artifact. A layer approving on its own reading is a model saying a
// human approved — the forgery agent-of-empires designed nonces against.
//
// Everything else writes freely. The log is append-only, a misread intent costs a
// wasted run, and confirming everything turns a conversation into a form.
func (i Intent) NeedsConfirmation() bool {
	return len(i.Command) > 2 && i.Command[0] == "gate" && i.Command[1] == "approve"
}

// Chat runs the conversation until the person leaves.
//
// It is a client of the CLI and nothing more: every command it runs is one a
// person could have typed, which is what makes the boundary checkable rather than
// a matter of prompt discipline.
func Chat(env Env, interpreter Interpreter, confirm func(*bufio.Scanner, string) bool) error {
	if env.In == nil {
		return errors.New("chat needs something to read from")
	}

	fmt.Fprintln(env.Out, "luna — say what you want, or `exit` to leave")

	// One scanner for the whole session. A second one over the same reader would
	// lose whatever the first had already buffered, which is how a confirmation
	// silently reads nothing and every approval gets refused.
	lines := bufio.NewScanner(env.In)
	for {
		fmt.Fprint(env.Out, "> ")
		if !lines.Scan() {
			return lines.Err()
		}

		said := strings.TrimSpace(lines.Text())
		if said == "" {
			continue
		}
		if said == "exit" || said == "quit" {
			return nil
		}

		if err := turn(env, interpreter, confirm, lines, said); err != nil {
			// A failed turn is not a failed session: the person can try again, and
			// dropping them out of the conversation for a misread sentence would
			// be worse than saying what went wrong.
			fmt.Fprintf(env.Out, "%v\n", err)
		}
	}
}

// turn handles one thing the person said.
func turn(env Env, interpreter Interpreter, confirm func(*bufio.Scanner, string) bool, lines *bufio.Scanner, said string) error {
	// The interpreter sees what Luna currently reports, so it can answer "how is
	// LUNA-1" without a round trip for the obvious cases.
	state, err := capture(env, []string{"gates", "--json"})
	if err != nil {
		return err
	}

	intent, err := interpreter.Interpret(said, state)
	if err != nil {
		return err
	}

	if len(intent.Command) == 0 {
		fmt.Fprintln(env.Out, intent.Reply)
		return nil
	}

	if intent.NeedsConfirmation() {
		if !confirm(lines, strings.Join(append([]string{"luna"}, intent.Command...), " ")) {
			fmt.Fprintln(env.Out, "left alone")
			return nil
		}
	}

	output, err := capture(env, intent.Command)
	if err != nil {
		return err
	}

	answer, err := interpreter.Phrase(said, intent.Command, output)
	if err != nil {
		return err
	}

	fmt.Fprintln(env.Out, answer)
	return nil
}

// capture runs a Luna command and returns what it printed.
//
// The layer reads what a person at a terminal would read — it has no other view
// of the task, and giving it one would break the boundary that makes its
// authority checkable.
func capture(env Env, command []string) (string, error) {
	var out strings.Builder

	quiet := env
	quiet.Out = &out
	quiet.Err = &out

	if err := Run(quiet, command); err != nil {
		// The error is the answer: the interpreter phrases a refusal the same way
		// it phrases a result, and hiding it would leave the person told nothing.
		return out.String() + err.Error(), nil
	}
	return out.String(), nil
}

// chatCommand opens the conversation.
//
// The interpreter is not built here: Luna hosts no model. Wiring one is the
// caller's business, and until something does, the command says so rather than
// pretending to work.
func chatCommand(env Env, args []string) error {
	if len(args) > 0 {
		return fmt.Errorf("%w: chat takes no arguments", ErrUsage)
	}
	if env.Interpret == nil {
		return errors.New("chat needs an interpreter, and none is configured")
	}

	return Chat(env, env.Interpret, func(lines *bufio.Scanner, command string) bool {
		fmt.Fprintf(env.Out, "run `%s`? [y/N] ", command)

		if !lines.Scan() {
			return false
		}
		answer := strings.ToLower(strings.TrimSpace(lines.Text()))
		return answer == "y" || answer == "yes"
	})
}
