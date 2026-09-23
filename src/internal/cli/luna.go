// Package cli is the command surface: nine verbs over a contract and a ledger.
//
// Luna is invoked by whoever conducts the work, at a phase boundary. It starts no
// agent, builds no sandbox and decides no transition — a person composes the
// environment before the agent starts, and the conductor decides what runs next.
package cli

import (
	"errors"
	"flag"
	"fmt"
	"io"
	"strings"
)

// ErrUsage means the command line was wrong, and the caller prints usage.
var ErrUsage = errors.New("usage")

// ExitFailed is what a check that observed a failure exits with.
//
// Distinct from 1, which is Luna failing to run at all. A conductor has to tell
// "the delivery is not proven" from "Luna is broken", and one exit code for both
// makes a broken machine read as a failed delivery.
const ExitFailed = 2

// Env is what a command is given, so tests do not touch the real terminal or the
// real ledger.
type Env struct {
	Out    io.Writer
	Err    io.Writer
	Dir    string
	Ledger string

	// Launch replaces this process with a composed command. Nil means the real
	// execve; a test sets it to observe what would have run, because a process
	// that has replaced the test binary cannot be asserted on.
	Launch func(command []string) error
}

type command func(Env, []string) error

var commands = map[string]command{
	"check":    checkCommand,
	"record":   recordCommand,
	"state":    stateCommand,
	"trail":    trailCommand,
	"runs":     runsCommand,
	"contract": contractCommand,
	"session":  sessionCommand,
	"version":  versionCommand,

	"install-skills": installSkillsCommand,

	// `report` was this verb's name until it was found to mean three things in
	// one binary and to be unfindable with rg among Go's "reports whether"
	// idiom. Kept undocumented for one release so a skill stack calling it does
	// not break the moment the binary updates.
	"report": runsCommand,
}

// Run dispatches one command line.
func Run(env Env, args []string) error {
	if len(args) == 0 {
		return fmt.Errorf("%w: %s", ErrUsage, Usage())
	}
	switch args[0] {
	case "help", "-h", "--help":
		fmt.Fprintln(env.Out, Usage())
		return nil
	}

	run, known := commands[args[0]]
	if !known {
		return fmt.Errorf("%w: unknown command %q\n%s", ErrUsage, args[0], Usage())
	}
	return run(env, args[1:])
}

// flags builds a flag set that reports errors rather than exiting, so a bad flag
// is one more error to print rather than a process that vanishes.
func flags(name string, env Env) *flag.FlagSet {
	set := flag.NewFlagSet(name, flag.ContinueOnError)
	set.SetOutput(env.Err)
	return set
}

// Usage is the help text, grouped by what somebody is trying to do.
func Usage() string {
	return strings.TrimSpace(`
luna — a phase is proven by running the tool, and the record outlives the work

Luna does not run agents and does not decide what happens next. It is called at
a phase boundary to prove what was delivered, and to record what happened.

  luna check --contract <file|-> [--run <id>] [--commit <sha>] [--base <sha>]
        run every check the contract declares, over what was delivered, and
        record each verdict. Exits 0 when everything passed, 2 when something
        did not, 1 when the check could not be run at all.

        --contract -   read the contract from stdin, which is the usual way:
                       it is written by whoever conducts, and Luna keeps none.
        --commit       what to verify. Defaults to this checkout's HEAD.
        --base         what the phase started from. Given, a delivery equal to
                       it is reported as no delivery — a phase that committed
                       nothing would otherwise pass on the code it was handed.
        --round        which round of the phase's convergent loop this is —
                       build, clean, check, judge. It is not a count of how
                       many times check was called: the loop's ceilings are
                       read off it, and a retry numbered as a round makes a
                       phase look like it iterated when it did not.
        --dry-run      say what would run, run nothing, record nothing. On
                       record the same flag does the opposite and writes the
                       line, marked as a simulation — a rehearsed flow has to
                       leave a trail, a rehearsed verdict must not.
        --no-record    run every check and report, but write no ledger line.
                       For trying a contract while writing one; a phase that
                       really delivered records what its checks observed.

  luna contract lint <file|->
        read a contract and report every way it is unusable, without running
        anything. Cheap, and it is where a mistake costs nothing.

  luna record --run <id> --event <kind> [...]
        record something Luna did not verify: a phase starting, a gate being
        answered, a block, an autonomy change.

        --event    phase | gate | block | unblock | autonomy | discovery

                   check is the seventh event and is deliberately not here: it
                   is written by luna check, from a command that ran. Recording
                   one by hand is accepted by the ledger and carries no verdict,
                   because there are no flags for one, so it reads as a failed
                   check. Prove it or record a phase; do not assert it.
        --phase    which phase this is about
        --status   running | awaiting_gate | awaiting_resume | blocked | done
                   | abandoned
        --gate --answer          for a gate
        --question --looked --needs   for a block; all three are required, and
                   --looked is repeatable. The account of where the answer was
                   looked for is what separates a real block from an unread
                   file, so it is not optional.
        --autonomy manual | semi | auto
        --found --where       for a discovery: what was concluded about this
                   project, and which file it was read from. Both are
                   required — a finding nobody can check is a claim.
        --round    which round of the phase's convergent loop this is — not how
                   many times the phase was retried
        --project  which repository this run is about, when the command runs
                   somewhere else — a batch seeding runs from one checkout
        --note     anything the fields above do not cover

        --found also reads on a phase, not only on a discovery, and the trail
        shows it there. Closing a run that never ran a check says so.

  luna state [--run <id>] [--json]
        where a run stands: its most recent line. With no --run, the branch
        answers — a checkout on luna/<run> knows which run it is.

  luna trail [<id>] [--json]
        everything that happened in one run, oldest first: the phases it
        walked, every check with its verdict and scope, what it discovered
        about the project, the gates, the blocks and the notes. This is the
        log of a task: state says where it is, this says what it did.

  luna runs [--here] [--open] [--project <repo>] [--since <duration>] [--json]
        every run, most recently touched first, with what needs a person.
        This is the listing: which tasks exist, and where each one stands.

        --here      only runs that touched the repository you are standing in
        --project   the same, for a repository you are not standing in, named
                    the way the ledger normalises it: github.com/owner/repo
        --open      only runs that have not finished
        --since     only runs touched within a window, e.g. 24h

        A run is listed under every repository it touched, not only the last
        one, because a run that moved between two belongs to both.

  luna session <agent> [--launcher <cmd>] [--skill <name>] [--bare] [--print]
        start an agent with what this repository already has open as its first
        message: the unfinished runs, what each is blocked on, and how to
        conduct the work. It reads the ledger, writes nothing, and replaces
        itself with the launcher — nothing of Luna stays as a parent process.

        --launcher  what composes the session's layers around the agent.
                    Defaults to ai-run; a sandbox and durable memory are
                    another tool's product, and Luna does not build either.
                    If the default is not installed, the agent starts directly
                    and Luna says so. A launcher you NAME is never dropped:
                    asking for containment and silently getting none is a
                    surprise nobody should have to catch.
        --skill     the skill the briefing names, default lsh-luna-soul. Empty
                    names none, and the briefing still says how to use Luna.
        --bare      start the agent directly, with no launcher at all.
        --print     print the briefing and exit, launching nothing.

  luna install-skills <claude|codex> [--dir <path>] [--dry-run] [--print]
        write the skills this build carries into an agent's skill directory, so
        a session knows how to call Luna without being told. Idempotent: it
        overwrites what it wrote before and deletes nothing else.

        The skill is embedded in the binary rather than shipped beside it. A
        skill that documents a verb this build does not answer costs somebody a
        session, and a test holds the two together.

        --dir       install somewhere else — for a harness Luna does not know
        --print     print the skills and place them yourself
        --dry-run   say what would be written, write nothing

  luna version
        what this build calls itself, and the commit it came from.

The ledger is one file outside every checkout, at $XDG_DATA_HOME/luna. Luna
refuses to write when it is not on durable storage — inside a sandbox, map that
directory read-write, or the record would be written and lost in silence.`)
}

func versionCommand(env Env, _ []string) error {
	fmt.Fprintln(env.Out, "luna "+Version)
	if Commit != "" {
		fmt.Fprintf(env.Out, "  %s\n", Commit)
	}
	return nil
}

// Version is what this build calls itself.
//
// The constant here is the source of truth, and `make install` overrides it at
// link time with `git describe` so a build from a dirty or unreleased tree says
// so rather than claiming the release it was cut near. It said "dev" for the
// tool's whole life because nothing set it — a placeholder nobody wired is
// indistinguishable from a version nobody bumped, and both mean a person holding
// two binaries cannot tell them apart.
//
// Pre-release while the shape is still moving: the verb set settled in the
// September 2026 redesign at six, gained a seventh, and the flags on them have
// changed twice since.
var Version = "0.1.0-rc.1"

// Commit is the revision this was built from, set at link time. Empty in a build
// that was not made through the Makefile, where there is nothing honest to say.
var Commit = ""
