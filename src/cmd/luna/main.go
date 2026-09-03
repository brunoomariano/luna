// Command luna proves what a phase delivered, and records what happened.
package main

import (
	"errors"
	"fmt"
	"os"

	"github.com/brunoomariano/luna/src/internal/cli"
	"github.com/brunoomariano/luna/src/internal/ledger"
)

func main() {
	os.Exit(run(os.Args[1:]))
}

// run is main with the exit taken out, so the mapping from an error to an exit
// code can be tested rather than only exercised through a subprocess.
func run(args []string) int {
	wd, err := os.Getwd()
	if err != nil {
		fmt.Fprintln(os.Stderr, "luna: cannot tell where it is being run:", err)
		return 1
	}

	env := cli.Env{
		Out:    os.Stdout,
		Err:    os.Stderr,
		Dir:    wd,
		Ledger: ledger.Default(),
	}
	return report(cli.Run(env, args))
}

// report turns an error into the exit code a conductor reads.
//
// The distinction that matters is between a delivery that is not proven and Luna
// being unable to run at all: one exit code for both would make a broken machine
// read as a failed delivery, and a conductor would send work back over it.
func report(err error) int {
	switch {
	case err == nil:
		return 0
	case errors.Is(err, cli.ErrFailed):
		return cli.ExitFailed
	default:
		fmt.Fprintln(os.Stderr, "luna:", err)
		return 1
	}
}
