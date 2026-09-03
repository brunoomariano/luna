package cli

import (
	"context"
	"fmt"
	"io"
	"strings"

	"github.com/brunoomariano/luna/src/internal/ledger"
	"github.com/brunoomariano/luna/src/internal/verify"
)

// positional pulls the first bare argument out of a command line, leaving the
// flags for the parser. It takes only the first: a second bare argument is a
// mistake worth reporting rather than ignoring, and the parser reports it.
func positional(args []string) (first string, rest []string) {
	rest = make([]string, 0, len(args))
	for i, arg := range args {
		if first == "" && !strings.HasPrefix(arg, "-") && notAFlagValue(args, i) {
			first = arg
			continue
		}
		rest = append(rest, arg)
	}
	return first, rest
}

// notAFlagValue reports whether the argument at i stands on its own, rather than
// being the value of the flag before it.
//
// `--run MAX-2` must not have MAX-2 read as the positional. `--json MAX-2` must,
// because a boolean flag takes no value — so the boolean names are listed rather
// than inferred. Getting this backwards is silent in both directions: the id is
// either eaten or ignored, and neither says anything.
func notAFlagValue(args []string, i int) bool {
	if i == 0 {
		return true
	}
	previous := args[i-1]
	if !strings.HasPrefix(previous, "-") || strings.Contains(previous, "=") {
		return true
	}
	return booleanFlags[strings.TrimLeft(previous, "-")]
}

// booleanFlags are the flags that consume no value. Kept as a list because the
// flag set is not built yet when the positional is lifted out.
var booleanFlags = map[string]bool{"json": true, "dry-run": true, "no-record": true}

func trailCommand(env Env, args []string) error {
	set := flags("trail", env)
	var (
		run    = set.String("run", "", "which run (default: the branch says)")
		asJSON = set.Bool("json", false, "report as JSON")
	)

	// A positional id reads better here than a flag — `luna trail MAX-2` is what
	// somebody types to read a finished task — but Go's flag package stops at the
	// first non-flag argument, so `luna trail MAX-2 --json` would take the id and
	// silently drop the flag. Lifting the positional out before parsing is what
	// makes both orders mean the same thing.
	id, rest := positional(args)
	if err := set.Parse(rest); err != nil {
		return fmt.Errorf("%w: %w", ErrUsage, err)
	}
	if *run != "" {
		id = *run
	}
	if id == "" {
		id = verify.Identify(context.Background(), env.Dir).Run
	}
	if id == "" {
		return fmt.Errorf("%w: name a run — `luna trail <id>` — or stand in its worktree", ErrUsage)
	}

	entries, err := ledger.Ledger{Path: env.Ledger}.Trail(id)
	if err != nil {
		return err
	}
	if *asJSON {
		return writeJSON(env.Out, entries)
	}
	if len(entries) == 0 {
		fmt.Fprintf(env.Out, "%s: nothing recorded\n", id)
		return nil
	}
	printTrail(env.Out, id, entries)
	return nil
}

// printTrail prints the whole story of one run, oldest first.
//
// One line per event, with the detail indented under the line it belongs to.
// Reading a trail is how somebody reconstructs what a run did after it ended,
// so it is ordered by time and never summarised: a report that collapses four
// rounds into "3 failed" hides which round failed and why.
func printTrail(out io.Writer, run string, entries []ledger.Entry) {
	header(out, run, entries)

	for _, e := range entries {
		fmt.Fprintf(out, "%s  %-10s %-9s %s\n",
			e.At.Local().Format("15:04"), e.Event, e.Phase, summary(e))
		for _, line := range detail(e) {
			fmt.Fprintf(out, "                            %s\n", line)
		}
	}
}

// header names the run and where it happened, once, so the lines beneath it do
// not have to repeat it.
func header(out io.Writer, run string, entries []ledger.Entry) {
	first := entries[0]
	fmt.Fprintf(out, "%s", run)
	if first.Project != "" {
		fmt.Fprintf(out, "  %s", first.Project)
	}
	fmt.Fprintln(out)

	last := entries[len(entries)-1]
	if last.Status != "" {
		fmt.Fprintf(out, "%s, %d events\n", last.Status, len(entries))
	}
	fmt.Fprintln(out)
}

// summary is the one-line account of an event.
func summary(e ledger.Entry) string {
	switch e.Event {
	case ledger.EventCheck:
		return checkSummary(e)
	case ledger.EventDiscovery:
		return fmt.Sprintf("%s   (%s)", e.Found, e.Where)
	case ledger.EventGate:
		return strings.TrimSpace(e.Gate + " " + e.Answer)
	case ledger.EventBlock:
		return e.Question
	case ledger.EventAutonomy:
		return e.Autonomy
	case ledger.EventPhase, ledger.EventUnblock:
		// Both say the same thing — where the run moved to, and why if it said so.
	}

	said := string(e.Status)
	if e.Round > 0 {
		said += fmt.Sprintf("  round %d", e.Round)
	}

	// Found on a phase reaches here rather than through the discovery case, and
	// it used to be dropped: the summary read Found only for a discovery, so four
	// phases recorded with `--found` rendered as blank lines while the text sat
	// in the ledger, intact and invisible. A field that was written and is not
	// shown is worse than one that was refused, because the writer believes they
	// left a record.
	for _, extra := range []string{e.Found, e.Note} {
		if extra != "" {
			said += "  " + extra
		}
	}
	return strings.TrimSpace(said)
}

func checkSummary(e ledger.Entry) string {
	mark := "ok  "
	if e.Verdict != "passed" {
		mark = "FAIL"
	}
	line := fmt.Sprintf("%s %-18s %-10s", mark, e.Artifact, e.Scope)
	if e.Round > 0 {
		line += fmt.Sprintf("r%d  ", e.Round)
	}
	return strings.TrimSpace(line + " " + e.Command)
}

// detail is what belongs under an event rather than beside it: the output of a
// failing check, and the three parts of a block.
func detail(e ledger.Entry) []string {
	if e.Event == ledger.EventBlock {
		lines := make([]string, 0, len(e.Looked)+1)
		for _, place := range e.Looked {
			lines = append(lines, "looked in  "+place)
		}
		if e.Needs != "" {
			lines = append(lines, "needs      "+e.Needs)
		}
		return lines
	}
	if e.Event == ledger.EventCheck && e.Verdict != "passed" && e.Note != "" {
		return []string{e.Note}
	}
	return nil
}
