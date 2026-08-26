package node

import (
	"context"
	"fmt"
	"os/exec"
	"strings"
	"time"

	"github.com/brunoomariano/luna/src/internal/fsm"
	"github.com/brunoomariano/luna/src/internal/lead"
)

// BootstrapTimeout bounds the command that makes a fresh worktree runnable.
//
// Longer than a verifier's, because this is where a dependency install happens
// and those are minutes rather than seconds. Shorter than a stage's, because a
// bootstrap that has not finished in fifteen minutes is stuck rather than slow —
// and the stage behind it has not started, so nothing is lost by saying so.
const BootstrapTimeout = 15 * time.Minute

// bootstrap makes a freshly-opened worktree runnable before the stage starts.
//
// Every serious repository has a step between `git clone` and "the tests run",
// and Luna opens a clean worktree per stage — so without this, every stage
// rediscovers that step, and the ones that cannot fail on a check that was never
// about the work.
//
// Measured on a real task: `tests_green` runs `make test`, `make test` needs
// `make build` first, and the repository's build output is not in git. Two build
// stages failed on it, $10.36 of a $19.13 task, to produce code that had been
// correct since the first attempt. Luna ran the check correctly; the worktree was
// not a place the check could pass.
//
// A failure here is infrastructure and not the work. It blocks with the command
// and its output, because "the machine is not ready" and "the stage delivered
// less than it owed" are different facts with opposite treatments — and a person
// who cannot tell them apart pays for the wrong fix.
func (r *Runner) bootstrap(ctx context.Context, dir string) error {
	command := strings.TrimSpace(r.Bootstrap)
	if command == "" {
		return nil
	}

	timeout := r.BootstrapTimeout
	if timeout <= 0 {
		timeout = BootstrapTimeout
	}
	ctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	cmd := exec.CommandContext(ctx, "sh", "-c", command) //nolint:gosec // the command is project configuration
	cmd.Dir = dir
	out, err := cmd.CombinedOutput()
	if err == nil {
		return nil
	}

	if ctx.Err() != nil {
		return fmt.Errorf("%w: bootstrap %q did not finish in %s",
			lead.ErrInfrastructure, command, timeout)
	}
	return fmt.Errorf("%w: bootstrap %q failed: %s",
		lead.ErrInfrastructure, command, summarise(string(out), fsm.VerdictFailed))
}
