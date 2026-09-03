package cli

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"strings"
	"time"

	"github.com/brunoomariano/luna/src/internal/ledger"
	"github.com/brunoomariano/luna/src/internal/verify"
)

// repeatable collects a flag given more than once.
type repeatable []string

func (r *repeatable) String() string { return strings.Join(*r, "; ") }
func (r *repeatable) Set(v string) error {
	*r = append(*r, v)
	return nil
}

func recordCommand(env Env, args []string) error {
	set := flags("record", env)
	var (
		run      = set.String("run", "", "which run this is (default: the branch says)")
		event    = set.String("event", "", "phase | gate | block | unblock | autonomy")
		phase    = set.String("phase", "", "which phase this is about")
		status   = set.String("status", "", "running | awaiting_gate | awaiting_resume | blocked | done | abandoned")
		gate     = set.String("gate", "", "which gate was answered")
		answer   = set.String("answer", "", "how it was answered")
		autonomy = set.String("autonomy", "", "manual | semi | auto")
		question = set.String("question", "", "what could not be settled")
		needs    = set.String("needs", "", "what would unblock it")
		note     = set.String("note", "", "anything the fields above do not cover")
		round    = set.Int("round", 0, "which round of a loop this is")
		worktree = set.String("worktree", "", "where the work is checked out (default: here)")
		simulate = set.Bool("dry-run", false, "mark the line as a simulation")
	)
	var looked repeatable
	set.Var(&looked, "looked", "where the answer was looked for, and what it failed to say (repeatable)")

	if err := set.Parse(args); err != nil {
		return fmt.Errorf("%w: %w", ErrUsage, err)
	}
	if *event == "" {
		return fmt.Errorf("%w: record needs --event", ErrUsage)
	}
	if *autonomy != "" {
		if _, err := ledger.ParseAutonomy(*autonomy); err != nil {
			return fmt.Errorf("%w: %w", ErrUsage, err)
		}
	}

	where := verify.Identify(context.Background(), env.Dir)
	tree := *worktree
	if tree == "" {
		tree = where.Worktree
	}

	line := ledger.Entry{
		Run:       runID(*run, where),
		Project:   where.Project,
		Phase:     *phase,
		Event:     ledger.Event(*event),
		Status:    ledger.Status(*status),
		Worktree:  tree,
		Round:     *round,
		Gate:      *gate,
		Answer:    *answer,
		Autonomy:  *autonomy,
		Question:  *question,
		Looked:    looked,
		Needs:     *needs,
		Note:      *note,
		Simulated: *simulate,
	}

	l := ledger.Ledger{Path: env.Ledger}
	if err := l.Append(line); err != nil {
		return err
	}
	fmt.Fprintf(env.Out, "recorded: %s %s\n", line.Run, line.Event)
	return nil
}

func stateCommand(env Env, args []string) error {
	set := flags("state", env)
	var (
		run    = set.String("run", "", "which run (default: the branch says)")
		asJSON = set.Bool("json", false, "report as JSON")
	)
	if err := set.Parse(args); err != nil {
		return fmt.Errorf("%w: %w", ErrUsage, err)
	}

	where := verify.Identify(context.Background(), env.Dir)
	id := runID(*run, where)
	if id == "" {
		return fmt.Errorf("%w: no run named, and the branch %q does not name one either — pass --run",
			ErrUsage, where.Branch)
	}

	l := ledger.Ledger{Path: env.Ledger}
	entry, found, err := l.State(id)
	if err != nil {
		return err
	}
	if !found {
		if *asJSON {
			return writeJSON(env.Out, map[string]any{"run": id, "known": false})
		}
		fmt.Fprintf(env.Out, "%s: nothing recorded\n", id)
		return nil
	}

	if *asJSON {
		return writeJSON(env.Out, entry)
	}
	printState(env.Out, entry)
	return nil
}

func printState(out io.Writer, e ledger.Entry) {
	fmt.Fprintf(out, "%s  %s\n", e.Run, e.Status)
	if e.Phase != "" {
		fmt.Fprintf(out, "  phase     %s\n", e.Phase)
	}
	if e.Round > 0 {
		fmt.Fprintf(out, "  round     %d\n", e.Round)
	}
	if e.Worktree != "" {
		fmt.Fprintf(out, "  worktree  %s\n", e.Worktree)
	}
	if e.Simulated {
		fmt.Fprintf(out, "  simulated (nothing here was really run)\n")
	}
	printBlock(out, e)
}

// printBlock puts the three parts of a block in front of whoever has to answer
// it, in the order they are read: what is being asked, what was already tried,
// what would settle it.
func printBlock(out io.Writer, e ledger.Entry) {
	if e.Question == "" {
		return
	}
	fmt.Fprintf(out, "\n  the question   %s\n", e.Question)
	if len(e.Looked) > 0 {
		fmt.Fprintf(out, "  looked in\n")
		for _, place := range e.Looked {
			fmt.Fprintf(out, "    %s\n", place)
		}
	}
	if e.Needs != "" {
		fmt.Fprintf(out, "  what unblocks  %s\n", e.Needs)
	}
}

func reportCommand(env Env, args []string) error {
	set := flags("report", env)
	var (
		since  = set.Duration("since", 0, "only runs touched within this window")
		asJSON = set.Bool("json", false, "report as JSON")
	)
	if err := set.Parse(args); err != nil {
		return fmt.Errorf("%w: %w", ErrUsage, err)
	}

	l := ledger.Ledger{Path: env.Ledger}
	runs, err := l.Report(*since)
	if err != nil {
		return err
	}

	if *asJSON {
		return writeJSON(env.Out, reportPayload(runs))
	}
	if len(runs) == 0 {
		fmt.Fprintln(env.Out, "nothing recorded yet")
		return nil
	}
	printReport(env.Out, runs)
	return nil
}

// printReport puts what needs a person first.
//
// A listing ordered only by time buries the blocked run under three that are
// merrily running, and the whole reason to read a report is to find the one that
// stopped.
func printReport(out io.Writer, runs []ledger.Run) {
	var waiting, moving []ledger.Run
	for _, r := range runs {
		if r.NeedsSomebody() {
			waiting = append(waiting, r)
			continue
		}
		moving = append(moving, r)
	}

	if len(waiting) > 0 {
		fmt.Fprintln(out, "needs somebody")
		for _, r := range waiting {
			printRun(out, r)
		}
		if len(moving) > 0 {
			fmt.Fprintln(out)
		}
	}
	if len(moving) > 0 {
		fmt.Fprintln(out, "in flight")
		for _, r := range moving {
			printRun(out, r)
		}
	}
}

func printRun(out io.Writer, r ledger.Run) {
	e := r.Latest
	line := fmt.Sprintf("  %-14s %-16s %-12s %s", e.Run, e.Status, e.Phase, ago(e.At))
	if r.Failed > 0 {
		line += fmt.Sprintf("  (%d failed)", r.Failed)
	}
	if e.Simulated {
		line += "  (simulated)"
	}
	fmt.Fprintln(out, line)
	if e.Question != "" {
		fmt.Fprintf(out, "                 %s\n", e.Question)
	}
}

// ago is a rough age, because a report is read to triage rather than to audit.
func ago(t time.Time) string {
	d := time.Since(t)
	switch {
	case d < time.Minute:
		return "just now"
	case d < time.Hour:
		return fmt.Sprintf("%dm ago", int(d.Minutes()))
	case d < 24*time.Hour:
		return fmt.Sprintf("%dh ago", int(d.Hours()))
	}
	return fmt.Sprintf("%dd ago", int(d.Hours()/24))
}

func reportPayload(runs []ledger.Run) []map[string]any {
	out := make([]map[string]any, 0, len(runs))
	for _, r := range runs {
		out = append(out, map[string]any{
			"run":            r.Latest.Run,
			"project":        r.Latest.Project,
			"status":         string(r.Status()),
			"phase":          r.Latest.Phase,
			"at":             r.Latest.At,
			"lines":          r.Lines,
			"failed":         r.Failed,
			"needs_somebody": r.NeedsSomebody(),
			"simulated":      r.Latest.Simulated,
		})
	}
	return out
}

func writeJSON(out io.Writer, v any) error {
	encoder := json.NewEncoder(out)
	encoder.SetIndent("", "  ")
	return encoder.Encode(v)
}
