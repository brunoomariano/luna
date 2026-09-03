package cli

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"strings"

	"github.com/brunoomariano/luna/src/internal/contract"
	"github.com/brunoomariano/luna/src/internal/ledger"
	"github.com/brunoomariano/luna/src/internal/verify"
)

// ErrFailed means a check observed a failure. It is a verdict about the work,
// not about Luna, and main turns it into its own exit code.
var ErrFailed = errors.New("the delivery is not proven")

func checkCommand(env Env, args []string) error {
	set := flags("check", env)
	var (
		path     = set.String("contract", "", "the contract to hold the phase to, or - for stdin")
		run      = set.String("run", "", "which run this is (default: the branch says)")
		commit   = set.String("commit", "", "what to verify (default: this checkout's HEAD)")
		base     = set.String("base", "", "what the phase started from")
		round    = set.Int("round", 0, "which round of a loop this is")
		dry      = set.Bool("dry-run", false, "say what would run, run nothing, record nothing")
		noRecord = set.Bool("no-record", false, "verify without writing to the ledger")
		asJSON   = set.Bool("json", false, "report as JSON")
	)
	if err := set.Parse(args); err != nil {
		return fmt.Errorf("%w: %w", ErrUsage, err)
	}
	if *path == "" {
		return fmt.Errorf("%w: check needs --contract (use - for stdin)", ErrUsage)
	}

	c, err := readContract(env, *path)
	if err != nil {
		return err
	}
	if err := c.Lint(); err != nil {
		return err
	}

	ctx := context.Background()
	where := verify.Identify(ctx, env.Dir)
	if *dry {
		return describe(env, c, where, *asJSON)
	}

	evidence, err := proveDelivery(ctx, where, c, *commit, *base)
	if err != nil {
		return err
	}

	id := runID(*run, where)
	if !*noRecord {
		if err := recordEvidence(env, id, where, c, evidence, *round); err != nil {
			return err
		}
	}
	return reportEvidence(env, c, evidence, id, *asJSON)
}

// proveDelivery runs the contract over what was delivered.
//
// An empty commit means "nothing was delivered" to the verifier, which is the
// TALLY-3 guard and is right — but it is not what a caller who simply did not
// pass --commit meant. Resolving HEAD here keeps the guard for a phase that
// really delivered nothing (its HEAD equals its base, which --base catches)
// while making the documented default true.
func proveDelivery(ctx context.Context, where verify.Where, c contract.Contract, commit, base string) ([]verify.Evidence, error) {
	resolved := commit == ""
	if resolved {
		commit = headOf(ctx, where.Repo)
	}
	return verify.Shell{Dir: where.Repo, Commit: commit, Base: base, Resolved: resolved}.Prove(ctx, c)
}

// reportEvidence prints the verdicts and answers whether the phase is proven.
func reportEvidence(env Env, c contract.Contract, evidence []verify.Evidence, run string, asJSON bool) error {
	if asJSON {
		if err := writeJSON(env.Out, evidenceReport(run, c.Phase, evidence)); err != nil {
			return err
		}
	} else {
		report(env.Out, c, evidence)
	}

	if !verify.Passed(evidence) {
		return fmt.Errorf("%w: %s", ErrFailed, c.Phase)
	}
	return nil
}

// headOf resolves this checkout's HEAD, or answers empty when there is none.
//
// Empty is the honest answer for a repository with no commit at all — the first
// phase of the first run — and the verifier already treats that as "verify the
// working tree, it is all there is".
func headOf(ctx context.Context, repo string) string {
	head, err := verify.HeadOf(ctx, repo)
	if err != nil {
		return ""
	}
	return head
}

// runID answers which run this is: what was named, or what the branch says.
//
// The branch is the authority when nobody named one, because it travels with the
// work. A run that can be neither named nor inferred is refused by the ledger,
// which is where that rule belongs.
func runID(named string, where verify.Where) string {
	if named != "" {
		return named
	}
	return where.Run
}

// readContract takes the contract from a file or from stdin.
//
// Stdin is the usual way: the contract is written by whoever conducts, from
// criteria a person approved, and Luna keeps none of it. A file is for a
// conductor that would rather keep one, and for `contract lint` on something
// being written.
func readContract(env Env, path string) (contract.Contract, error) {
	if path == "-" {
		body, err := io.ReadAll(os.Stdin)
		if err != nil {
			return contract.Contract{}, fmt.Errorf("reading the contract from stdin: %w", err)
		}
		return contract.Parse(string(body), "<stdin>")
	}

	body, err := os.ReadFile(path) //nolint:gosec // the path is an argument the caller typed
	if err != nil {
		return contract.Contract{}, fmt.Errorf("reading the contract: %w", err)
	}
	return contract.Parse(string(body), path)
}

// recordEvidence writes one line per check.
//
// One line each rather than one for the phase: a report that says "forge failed"
// cannot say which obligation went unmet, and the artifact is the thing somebody
// acts on.
func recordEvidence(env Env, run string, where verify.Where, c contract.Contract, all []verify.Evidence, round int) error {
	l := ledger.Ledger{Path: env.Ledger}
	for _, e := range all {
		line := ledger.Entry{
			Run:      run,
			Project:  where.Project,
			Phase:    c.Phase,
			Event:    ledger.EventCheck,
			Worktree: where.Worktree,
			Round:    round,
			Artifact: e.Artifact,
			Verdict:  string(e.Verdict),
			Scope:    string(e.Scope),
			Command:  e.Command,
			Exit:     e.ExitCode,
			Note:     e.Detail,
		}
		if err := l.Append(line); err != nil {
			return err
		}
	}
	return nil
}

// describe says what would run without running any of it.
//
// A simulation that reads like a result is a lie with the truth beside it, so
// this never records and never reports a verdict.
func describe(env Env, c contract.Contract, where verify.Where, asJSON bool) error {
	if asJSON {
		return writeJSON(env.Out, map[string]any{
			"phase":     c.Phase,
			"project":   where.Project,
			"simulated": true,
			"would_run": wouldRun(c),
		})
	}

	fmt.Fprintf(env.Out, "%s — nothing was run, nothing was recorded\n", c.Phase)
	for _, artifact := range c.Owed() {
		v, _ := c.VerifierFor(artifact)
		fmt.Fprintf(env.Out, "  %-24s %s\n", artifact, v.Describe())
	}
	return nil
}

func wouldRun(c contract.Contract) []map[string]string {
	owed := c.Owed()
	out := make([]map[string]string, 0, len(owed))
	for _, artifact := range owed {
		v, _ := c.VerifierFor(artifact)
		out = append(out, map[string]string{
			"artifact": artifact,
			"check":    v.Describe(),
			"scope":    string(v.Proves()),
		})
	}
	return out
}

// report prints what each check observed.
//
// Every owed artifact, passing ones included: a report listing only failures
// cannot be told from one where nothing ran.
func report(out io.Writer, c contract.Contract, all []verify.Evidence) {
	for _, e := range all {
		mark := "ok  "
		if e.Verdict != verify.VerdictPassed {
			mark = "FAIL"
		}
		fmt.Fprintf(out, "%s %-24s %-10s %s\n", mark, e.Artifact, e.Scope, e.Command)
		if e.Detail != "" && e.Verdict != verify.VerdictPassed {
			fmt.Fprintf(out, "     %s\n", e.Detail)
		}
	}

	failed := verify.Failures(all)
	if len(failed) == 0 {
		fmt.Fprintf(out, "\n%s is proven: %s\n", c.Phase, strings.Join(c.Owed(), ", "))
		return
	}

	var names []string
	for _, e := range failed {
		names = append(names, e.Artifact)
	}
	fmt.Fprintf(out, "\n%s is not proven: %s\n", c.Phase, strings.Join(names, ", "))
}

func evidenceReport(run, phase string, all []verify.Evidence) map[string]any {
	checks := make([]map[string]any, 0, len(all))
	for _, e := range all {
		checks = append(checks, map[string]any{
			"artifact": e.Artifact,
			"verdict":  string(e.Verdict),
			"scope":    string(e.Scope),
			"command":  e.Command,
			"exit":     e.ExitCode,
			"detail":   e.Detail,
		})
	}
	return map[string]any{
		"run":    run,
		"phase":  phase,
		"passed": verify.Passed(all),
		"checks": checks,
	}
}

func contractCommand(env Env, args []string) error {
	if len(args) == 0 || args[0] != "lint" {
		return fmt.Errorf("%w: the only contract subcommand is lint", ErrUsage)
	}
	if len(args) < 2 {
		return fmt.Errorf("%w: contract lint needs a file, or - for stdin", ErrUsage)
	}

	c, err := readContract(env, args[1])
	if err != nil {
		return err
	}
	if err := c.Lint(); err != nil {
		return err
	}

	fmt.Fprintf(env.Out, "%s: %d owed, %s\n", c.Phase, len(c.Owed()), strings.Join(c.Owed(), ", "))
	if c.Loop != nil {
		fmt.Fprintf(env.Out, "loop: converges on %s, at most %d rounds\n",
			strings.Join(c.Loop.ConvergesOn, ", "), c.Loop.MaxRounds)
	}
	return nil
}
