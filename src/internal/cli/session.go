package cli

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"strings"
	"syscall"

	"github.com/brunoomariano/luna/src/internal/ledger"
	"github.com/brunoomariano/luna/src/internal/verify"
)

// There is no default launcher.
//
// Luna does not compose sandboxes or memory itself — that is another tool's
// whole product, and taking it over is what the redesign undid. It hands the
// briefing to whatever the caller already uses, and naming that is the caller's
// to do: a default pointing at one person's tool made every other machine print
// a note about something it was never going to have.

// sessionCommand starts an agent with what this repository's ledger already
// knows, as its first message.
//
// The call is still agent -> Luna: this hands over a briefing and gets out of
// the way. It reads the ledger, writes nothing, and replaces itself with the
// launcher, so nothing of Luna is left as a parent process. The project has one
// recorded failure from being the parent, and one from driving an agent's human
// interface; execve is neither.
func sessionCommand(env Env, args []string) error {
	set := flags("session", env)
	var (
		launcher = set.String("launcher", "", "what composes the session around the agent")
		skill    = set.String("skill", defaultSkill, "the skill to name in the briefing (empty: name none)")
		printIt  = set.Bool("print", false, "print the briefing and exit, launching nothing")
	)

	agent, rest := positional(args)
	if err := set.Parse(rest); err != nil {
		return fmt.Errorf("%w: %w", ErrUsage, err)
	}

	ctx := context.Background()
	where := verify.Identify(ctx, env.Dir)
	briefing, err := brief(env, where, *skill)
	if err != nil {
		return err
	}

	if *printIt {
		fmt.Fprintln(env.Out, briefing)
		return nil
	}
	if agent == "" {
		return fmt.Errorf("%w: session needs an agent to start — `luna session claude`", ErrUsage)
	}

	return env.launch(compose(agent, briefing, *launcher))
}

// compose decides what actually gets started.
//
// A launcher is only ever one the caller named, so it never falls back: it is
// left composed even when it is missing, and launch reports that and stops.
// `--launcher firejail` is a request for containment, and silently starting an
// unsandboxed agent because firejail was absent is a security surprise.
//
// Naming none is how you say you want none. There was a `--bare` flag for that
// while a default launcher existed and had to be turned off; with no default it
// said exactly what passing nothing already says.
func compose(agent, briefing, launcher string) []string {
	if launcher == "" {
		return []string{agent, briefing}
	}
	return []string{launcher, agent, briefing}
}

// launch replaces this process with the composed command.
//
// A seam rather than a direct syscall so a test can observe what would be run:
// asserting on a process that has replaced the test binary is not possible, and
// the composition is the part worth pinning.
func (e Env) launch(command []string) error {
	if e.Launch != nil {
		return e.Launch(command)
	}

	path, err := exec.LookPath(command[0])
	if err != nil {
		return fmt.Errorf("cannot start a session: %s is not on your PATH: %w", command[0], err)
	}
	// The command is variable because that is the verb: the caller names which
	// agent to start and, with --launcher, what composes the session around it.
	// Both come from this person's own command line, the same trust boundary as
	// typing the launcher directly — and unlike a shell, execve interprets
	// nothing, so a briefing full of quotes is one argument rather than syntax.
	return syscall.Exec(path, command, os.Environ()) //nolint:gosec // G204: see above
}

// defaultSkill is the skill the briefing names.
//
// Named rather than described because a conductor that has it should use it, and
// an exact name is what a model can act on — a description of a skill reads as a
// suggestion to improvise one. The briefing says what to do without it, so a
// machine that does not have it is not stranded.
//
// It is the skill this binary carries and `install-skills` writes, so the name
// is one Luna can actually produce. It named a house-only conductor skill for
// two renames after that skill stopped existing: the rule banning those was
// enforced against the travelling document and never against the briefing.
const defaultSkill = "luna"

// brief is the first message an agent receives: what this repository has open,
// and what to do about it.
//
// Prose rather than JSON. It is read by a model as a message from a person, and
// the open runs are few enough to read — a report with forty rows would want a
// different shape, and the ledger does not have forty.
func brief(env Env, where verify.Where, skill string) (string, error) {
	runs, err := ledger.Ledger{Path: env.Ledger}.Report(ledger.Filter{
		Project: where.Project,
		Open:    true,
	})
	if err != nil {
		return "", err
	}

	var b strings.Builder
	b.WriteString("You are starting work in " + where.Project + ".\n\n")
	writeOpenRuns(&b, runs)
	writeHowToConduct(&b, skill, len(runs) > 0)
	return b.String(), nil
}

// writeOpenRuns says what this repository already has going.
//
// The unfinished ones only. A listing of everything ever done would bury the one
// fact that changes what happens next, and `luna runs` is one command away for
// the rest.
func writeOpenRuns(b *strings.Builder, runs []ledger.Run) {
	if len(runs) == 0 {
		b.WriteString("No Luna run is open here. Nothing is waiting to be resumed.\n\n")
		return
	}

	fmt.Fprintf(b, "Luna has %s open in this repository:\n\n", plural(len(runs), "run"))
	for _, r := range runs {
		e := r.Latest
		fmt.Fprintf(b, "  %s  %s  phase %s  (%s)",
			e.Run, e.Status, blankAs(e.Phase, "unnamed"), ago(e.At))
		if r.Failed > 0 {
			fmt.Fprintf(b, "  %s failed", plural(r.Failed, "check"))
		}
		b.WriteString("\n")
		if e.Question != "" {
			b.WriteString("      blocked on: " + e.Question + "\n")
		}
	}
	b.WriteString("\nRead one with `luna trail <id>` before deciding anything about it.\n\n")
}

// writeHowToConduct says what to do, and asks rather than assumes.
//
// An open run is not an instruction to resume it: the person may have opened this
// session for something else entirely, and a session that resumed the wrong task
// on its own costs more than the question does. Autonomy starts at manual for the
// same reason.
func writeHowToConduct(b *strings.Builder, skill string, hasOpen bool) {
	if hasOpen {
		b.WriteString("Ask which one before you act: resume one of the above, or start\n" +
			"something new. Do not choose on your own — an open run is not a request\n" +
			"to continue it.\n\n")
	}

	if skill != "" {
		b.WriteString("Conduct the work with the " + skill + " skill.\n" +
			"If that skill is not available in this session, conduct it yourself and\n" +
			"still record through Luna.\n\n")
	}

	b.WriteString("Either way, Luna is how the work is proven and recorded:\n" +
		"  luna check --contract -   prove a phase's delivery by running its checks\n" +
		"  luna record               a phase, a gate, a block, a discovery\n" +
		"  luna state | luna trail   where a run stands, and everything it did\n" +
		"  luna help                 every verb and flag\n\n" +
		"Autonomy starts at manual: every gate comes to the person unless they ask\n" +
		"for semi or auto. Record the mode when it moves.\n")
}

// blankAs answers with a stand-in when a field was never set, so a briefing does
// not have a hole in the middle of a sentence.
func blankAs(value, whenEmpty string) string {
	if value == "" {
		return whenEmpty
	}
	return value
}

// plural writes "1 run" and "2 runs", because a briefing that says "1 runs" reads
// as generated text and gets skimmed.
func plural(n int, noun string) string {
	if n == 1 {
		return "1 " + noun
	}
	return fmt.Sprintf("%d %ss", n, noun)
}
