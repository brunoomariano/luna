// Package interpret turns what a person said into a Luna command.
//
// It lives apart from the CLI it serves and from the node layer it resembles,
// because it is neither: it is a harness being asked a question. Keeping it here
// means the CLI depends on an interface it declares rather than on a model, and
// that swapping the interpreter is a different package rather than an edit
// (ADR-0044).
package interpret

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os/exec"
	"strings"
	"time"

	"github.com/brunoomariano/luna/src/internal/cli"
)

// Timeout bounds one turn of the conversation.
//
// Shorter than a verification's deadline because a person is waiting: a model
// that has not answered in two minutes is not about to, and the honest thing is
// to say so rather than leave someone looking at a prompt.
const Timeout = 2 * time.Minute

// Harness interprets by running one of the official agents non-interactively.
//
// Luna hosts no model (ADR-0044). It spawns `claude --print`, `pi --print`,
// `codex exec` or `opencode --print` with what the person said and what Luna
// reports, and reads back the command to run. That brings no API key, no HTTP
// client and no SDK into a project whose external dependency count is one.
type Harness struct {
	// Agent is which official harness to ask. Empty means the house default.
	Agent string

	// Timeout bounds one turn. Zero means InterpretTimeout.
	Deadline time.Duration
}

// nonInteractive is how each official harness takes a prompt and answers once.
//
// A closed table for the same reason the gating one is closed: an agent Luna
// guesses at fails in a way nobody sees until a person is already talking to it.
var nonInteractive = map[string][]string{
	"claude":   {"--print"},
	"pi":       {"--print"},
	"codex":    {"exec"},
	"opencode": {"--print"},
}

// DefaultInterpreter is the harness asked when none is configured, from the house
// preference order (ADR-0042).
const DefaultInterpreter = "claude"

// Interpret turns what a person said into the command Luna should run.
func (h Harness) Interpret(said, state string) (cli.Intent, error) {
	answer, err := h.ask(interpretPrompt(said, state))
	if err != nil {
		return cli.Intent{}, err
	}

	intent, err := decodeIntent(answer)
	if err != nil {
		// A model that will not produce a command is a turn that says so, not a
		// crash: the person can rephrase, and telling them what came back is more
		// use than an error about JSON.
		return cli.Intent{Reply: strings.TrimSpace(answer)}, nil
	}
	return intent, nil
}

// Phrase turns a command's output into an answer for the person.
func (h Harness) Phrase(said string, command []string, output string) (string, error) {
	answer, err := h.ask(phrasePrompt(said, command, output))
	if err != nil {
		return "", err
	}

	// An empty answer is worse than a raw one: the person asked something and
	// would be told nothing.
	if strings.TrimSpace(answer) == "" {
		return strings.TrimSpace(output), nil
	}
	return strings.TrimSpace(answer), nil
}

// ask runs the harness once and returns what it said.
func (h Harness) ask(prompt string) (string, error) {
	agent := h.Agent
	if agent == "" {
		agent = DefaultInterpreter
	}

	args, ok := nonInteractive[agent]
	if !ok {
		return "", fmt.Errorf("%q is not a harness Luna can interpret with (%s)",
			agent, strings.Join(interpreters(), ", "))
	}

	timeout := h.Deadline
	if timeout <= 0 {
		timeout = Timeout
	}

	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()

	cmd := exec.CommandContext(ctx, agent, append(args, prompt)...) //nolint:gosec // the agent comes from a closed table
	out, err := cmd.Output()

	if ctx.Err() != nil {
		return "", fmt.Errorf("%s did not answer within %s", agent, timeout)
	}

	var exitErr *exec.ExitError
	if errors.As(err, &exitErr) {
		return "", fmt.Errorf("%s exited %d: %s", agent, exitErr.ExitCode(), firstLine(string(exitErr.Stderr)))
	}
	if err != nil {
		return "", fmt.Errorf("running %s: %w", agent, err)
	}
	return string(out), nil
}

// interpretPrompt asks for a command and nothing else.
//
// The instruction is explicit about what the model may not decide, because the
// boundary is not enforced here — it is enforced by the command being one a
// person could have typed (ADR-0038). This is the part that shapes the answer;
// the CLI is what bounds the consequences.
func interpretPrompt(said, state string) string {
	return fmt.Sprintf(`You translate a person's request into one Luna command.

Answer with JSON and nothing else, in one of two shapes:

  {"command": ["gate", "approve", "LUNA-1"]}
  {"reply": "nothing is waiting"}

Use "reply" when the state below already answers them, or when you cannot tell
which task they mean. Never guess a task id.

The commands you may choose from:

  task show <id> --json     what one task is doing
  gates --json              every task waiting on a person
  gate show <id>            what a task is waiting for
  gate approve <id>         accept the artifact and carry on
  gate reject <id> [reason] refuse it; the stage runs again
  run <id>                  drive the task until it needs a person
  unblock <id>              clear a block once it is dealt with

You do not decide which stage runs, whether a stage is finished, or whether an
artifact is good. Those are the engine's, and asking for them is a "reply"
saying so.

What Luna reports right now:
%s

The person said:
%s`, strings.TrimSpace(state), said)
}

// phrasePrompt asks for an answer a person can read.
func phrasePrompt(said string, command []string, output string) string {
	return fmt.Sprintf(`Answer the person in one or two sentences, plainly.

Do not invent anything the output does not say. If it reports an error, say what
went wrong and what would fix it. If it reports evidence, the scope matters: a
full check and something that merely exists are different claims.

The person said:
%s

Luna ran: luna %s

It answered:
%s`, said, strings.Join(command, " "), strings.TrimSpace(output))
}

// decodeIntent reads the model's answer, tolerating the fence it often wraps
// JSON in.
func decodeIntent(answer string) (cli.Intent, error) {
	body := strings.TrimSpace(answer)

	// Models fence JSON in markdown often enough that refusing it would turn a
	// correct answer into a failed turn.
	if fenced := strings.Index(body, "```"); fenced >= 0 {
		body = body[fenced+3:]
		if newline := strings.IndexByte(body, '\n'); newline >= 0 {
			body = body[newline+1:]
		}
		if closing := strings.Index(body, "```"); closing >= 0 {
			body = body[:closing]
		}
		body = strings.TrimSpace(body)
	}

	var decoded struct {
		Command []string `json:"command"`
		Reply   string   `json:"reply"`
	}
	if err := json.Unmarshal([]byte(body), &decoded); err != nil {
		return cli.Intent{}, fmt.Errorf("the answer was not a command: %w", err)
	}
	if len(decoded.Command) == 0 && decoded.Reply == "" {
		return cli.Intent{}, errors.New("the answer named neither a command nor a reply")
	}

	return cli.Intent{Command: decoded.Command, Reply: decoded.Reply}, nil
}

// interpreters names the harnesses that can interpret, in preference order.
func interpreters() []string {
	// Ordered rather than ranged over the map: an error message that lists them
	// differently each time is one nobody can match against the documentation.
	ordered := []string{"claude", "pi", "codex", "opencode"}

	available := make([]string, 0, len(ordered))
	for _, agent := range ordered {
		if _, ok := nonInteractive[agent]; ok {
			available = append(available, agent)
		}
	}
	return available
}

// firstLine keeps a harness's error readable: the reason, not its whole stderr.
func firstLine(s string) string {
	trimmed := strings.TrimSpace(s)
	if newline := strings.IndexByte(trimmed, '\n'); newline >= 0 {
		trimmed = trimmed[:newline]
	}
	if len(trimmed) > 200 {
		return trimmed[:200]
	}
	return trimmed
}
