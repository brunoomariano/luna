// Package cli is the command surface: five verbs over a contract and a ledger.
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
}

type command func(Env, []string) error

var commands = map[string]command{
	"check":    checkCommand,
	"record":   recordCommand,
	"state":    stateCommand,
	"report":   reportCommand,
	"contract": contractCommand,
	"version":  versionCommand,
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
        --round        which round of a loop this is.
        --dry-run      say what would run, run nothing, record nothing.

  luna contract lint <file|->
        read a contract and report every way it is unusable, without running
        anything. Cheap, and it is where a mistake costs nothing.

  luna record --run <id> --event <kind> [...]
        record something Luna did not verify: a phase starting, a gate being
        answered, a block, an autonomy change.

        --event    phase | gate | block | unblock | autonomy
        --phase    which phase this is about
        --status   running | awaiting_gate | awaiting_resume | blocked | done
                   | abandoned
        --gate --answer          for a gate
        --question --looked --needs   for a block; all three are required, and
                   --looked is repeatable. The account of where the answer was
                   looked for is what separates a real block from an unread
                   file, so it is not optional.
        --autonomy manual | semi | auto

  luna state [--run <id>] [--json]
        where a run stands: its most recent line. With no --run, the branch
        answers — a checkout on luna/<run>/<phase> knows which run it is.

  luna report [--since <duration>] [--json]
        every run, most recently touched first, with what needs a person.

The ledger is one file outside every checkout, at $XDG_DATA_HOME/luna. Luna
refuses to write when it is not on durable storage — inside a sandbox, map that
directory read-write, or the record would be written and lost in silence.`)
}

func versionCommand(env Env, _ []string) error {
	fmt.Fprintln(env.Out, "luna "+Version)
	return nil
}

// Version is the build's version, set at link time.
var Version = "dev"
