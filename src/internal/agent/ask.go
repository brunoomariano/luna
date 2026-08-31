package agent

import (
	"context"
	"errors"
	"fmt"
	"os/exec"
	"strings"
	"syscall"
	"time"
)

// AskTimeout bounds one question to a harness.
//
// Shorter than a stage's budget because something is waiting on the answer: the
// lead is deciding whether a gate passes, not building.
//
// It was two minutes, on the reasoning that a model which has not answered by
// then is not about to. Measured against the one gate the shipped flow has, and
// that reasoning is wrong: judging an 820-word contract against three criteria
// took 68s and 85s over two samples, at 5 and 7 turns — the harness reads before
// it answers, so the time scales with the artifact, not with how hard the
// question is. The first real run at knob 9 went past two minutes and the gate
// recorded `could-not-ask`.
//
// The margin is wide because a timeout does not stop the spend, it only discards
// what was already paid for: those two samples cost $0.55 and $0.47, billed
// whether or not Luna waited for them. TestAskTimeoutClearsWhatJudgingMeasured
// holds the value to that measurement.
const AskTimeout = 10 * time.Minute

// DefaultAsk is the harness asked when none is configured.
const DefaultAsk = "claude"

// Asker puts one question to a model and returns what it said.
//
// It is the whole boundary between Luna and a model that judges rather than
// works: Luna hosts none of its own, so this goes out to an official harness run
// non-interactively. An interface so the lead can be tested without a model.
type Asker interface {
	Ask(ctx context.Context, prompt string) (string, error)
}

// Ask puts one question to the harness and returns what it said.
//
// It does not go through Run, and the difference is the sandbox. Run refuses to
// start without one because it is starting an agent that will write code; Ask is
// the lead reading state and answering a question, and it runs where Luna runs.
// Wrapping it in the jail would contain the one caller that has nothing to
// contain — and would need the store on the inside, which is what INV-4 keeps
// out.
//
// The caller's context bounds it as well as the deadline, so a person who
// cancels a run does not wait out a model's timeout.
func (h Harness) Ask(ctx context.Context, prompt string) (string, error) {
	kind, binary, args, timeout, err := h.asking()
	if err != nil {
		return "", err
	}

	// The default applies only when the caller set no deadline of its own. It is
	// the ceiling for a question nobody bounded, not one imposed over somebody
	// who did: `luna lead` gives the conductor the stage budget, and clamping it
	// back to two minutes killed a stage while its agent was still working.
	// Measured on TALLY-4, twice — the second time after the budget was wired
	// through and this was still cutting it.
	asking := ctx
	if deadline, set := ctx.Deadline(); set {
		timeout = time.Until(deadline)
	} else {
		var stop context.CancelFunc
		asking, stop = context.WithTimeout(ctx, timeout)
		defer stop()
	}

	// The lead's whole loop is Luna commands — `luna next` for the order, `luna
	// done` to report — so a harness that stops to ask a person about each one
	// cannot conduct an unattended task. Measured on TALLY-4: the lead answered
	// "the call needs your approval before it can run" and the task never left
	// setup. Luna's own guard caught the stall, which is how it was seen.
	//
	// This is not the stage's reasoning. A stage bypasses the prompt because the
	// sandbox has already contained it, and the lead runs uncontained. What
	// bounds the lead is the shape of what it is given: an order names one stage
	// and no list of what follows, and nothing it says moves the flow — a
	// transition happens because the reducer recorded one, never because the lead
	// reported it. The prompt was guarding against a decision the lead has no
	// path to make.
	//
	// #nosec G204 — the binary comes from the closed table above, not from input.
	cmd := exec.CommandContext(asking, binary, args...)
	cmd.Stdin = strings.NewReader(prompt)

	// Its own process group, and the deadline kills the group — the same
	// correction Run needed, for the same reason and one more. A harness spawns
	// a model client, so killing the direct child leaves the work running; and
	// `Output` waits on a stdout pipe the grandchild still holds, so the call
	// does not even return. Measured here: a 100ms deadline against a sleeping
	// harness waited the full 30 seconds before this.
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	cmd.Cancel = func() error {
		if cmd.Process == nil {
			return nil
		}
		return syscall.Kill(-cmd.Process.Pid, syscall.SIGKILL)
	}

	out, runErr := cmd.Output()

	// The two cancellations are kept apart: "someone stopped the run" and "the
	// model ran out of time" are different facts, and reporting one as the other
	// sends whoever reads it looking for a slow harness that was never slow.
	//
	// Asked of the error rather than of which context carries it, because when
	// the caller set the deadline the two are the same context — and a run out of
	// time would otherwise read as somebody having stopped it.
	if err := asking.Err(); err != nil {
		if errors.Is(err, context.DeadlineExceeded) {
			return "", fmt.Errorf("%s did not answer within %s", kind, timeout.Round(time.Second))
		}
		return "", fmt.Errorf("%s was stopped before it answered: %w", kind, err)
	}

	var exitErr *exec.ExitError
	if errors.As(runErr, &exitErr) {
		// Both streams, because either can carry the reason and these harnesses
		// print theirs on stdout. Reporting stderr alone left "claude exited 1:"
		// with nothing after the colon at the moment somebody needed to know why.
		// Run had the same fault and the same fix; they were one edit apart.
		return "", fmt.Errorf("%s exited %d: %s", kind, exitErr.ExitCode(),
			firstNonEmpty(tail(string(exitErr.Stderr)), tail(string(out))))
	}
	if runErr != nil {
		return "", fmt.Errorf("asking %s: %w", kind, runErr)
	}

	// Exit code zero is not proof that a turn happened. Measured against
	// opencode 1.17.7: `--print` is not a flag it has, and rather than refusing
	// it printed its banner and exited 0 — so an empty answer read as a
	// successful turn and the caller got a blank line. A harness that fails
	// silently is worse than one that is absent.
	if strings.TrimSpace(string(out)) == "" {
		return "", fmt.Errorf("%s exited 0 without answering — check its non-interactive invocation `%s %s`",
			kind, binary, strings.Join(args, " "))
	}
	return string(out), nil
}

// asking resolves what Ask needs before it starts anything: which harness, which
// binary, and how long it may take.
//
// Split out for the same reason Run has `resolve` — these are the refusals, and
// keeping them apart from the running keeps "this question is wrong" separate
// from "this question went wrong".
func (h Harness) asking() (kind, binary string, args []string, timeout time.Duration, err error) {
	kind = h.Kind
	if kind == "" {
		kind = DefaultAsk
	}

	spec, ok := harnesses[kind]
	if !ok {
		return "", "", nil, 0, fmt.Errorf("%w: %q is not a harness Luna can ask (%s)",
			ErrNoHarness, kind, strings.Join(harnessNames(), ", "))
	}

	binary = h.Binary
	if binary == "" {
		binary = spec.binary
	}

	timeout = h.Deadline
	if timeout <= 0 {
		timeout = AskTimeout
	}
	return kind, binary, append([]string{}, spec.askArgs...), timeout, nil
}

// firstNonEmpty is the first of the diagnostics that says anything.
//
// A harness prints its reason on one stream or the other and rarely both; the
// caller wants whichever is there, not a message with an empty half.
func firstNonEmpty(first, second string) string {
	if strings.TrimSpace(first) != "" {
		return first
	}
	return second
}
