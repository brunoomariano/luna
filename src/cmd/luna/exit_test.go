package main

import (
	"errors"
	"fmt"
	"testing"

	"github.com/brunoomariano/luna/src/internal/cli"
)

// The exit code is the contract with whoever conducts: 0 proven, 2 not proven, 1
// Luna could not run. Reading the second as the third sends work back over a
// broken machine.
func TestAnErrorBecomesTheExitCodeAConductorReads(t *testing.T) {
	cases := []struct {
		name string
		err  error
		want int
	}{
		{"proven", nil, 0},
		{"not proven", fmt.Errorf("%w: forge", cli.ErrFailed), cli.ExitFailed},
		{"wrong command line", fmt.Errorf("%w: unknown command", cli.ErrUsage), 1},
		{"luna could not run", errors.New("the ledger is not on durable storage"), 1},
	}
	for _, c := range cases {
		if got := report(c.err); got != c.want {
			t.Errorf("%s: got exit %d, want %d", c.name, got, c.want)
		}
	}
}

// run wires the working directory and the default ledger into the command
// surface. `help` exercises that wiring without touching either.
func TestRunWiresTheCommandSurface(t *testing.T) {
	if got := run([]string{"help"}); got != 0 {
		t.Errorf("help exited %d", got)
	}
	if got := run([]string{"orchestrate"}); got != 1 {
		t.Errorf("an unknown command exited %d, want 1", got)
	}
}
