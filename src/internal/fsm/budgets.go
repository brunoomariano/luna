package fsm

import (
	"fmt"
	"time"
)

// Budgets bound how long the node waits on an agent before calling it stuck.
//
// They are declarations, not timers. The engine holds them because they belong to
// the profile — the same place that decides which gates wait — but nothing here
// measures anything: the node layer reads them, herdr does the waiting, and a
// stall arrives as a fact inside an action.
type Budgets struct {
	// Turn is how long one prompt may take, from sending it to the agent settling,
	// including any tool it runs on the way.
	//
	// One budget rather than two. An earlier design split this into an idle window
	// and a larger one for a tool in flight, which needs Luna to know a tool is
	// running — and nothing tells it. herdr reports `working`, which covers an
	// agent thinking and an agent compiling alike, so the distinction was never
	// expressible and the idle window was silently bounding whole turns.
	Turn time.Duration
}

// DefaultBudgets are the values the study settled on, from the one project that
// had been burned by getting them wrong.
//
// Two hours because a turn may contain a build, which is the case that number was
// chosen for. Thirty minutes was the old idle value and was wrong for what it
// actually bounded.
//
// Deliberately absent: a wall-clock cap on the task. multica removed theirs after
// it killed legitimate long runs, and the lesson is that progress, not elapsed
// time, is the signal worth acting on — which is a detector Luna does not have yet
// (PRD node-0002).
func DefaultBudgets() Budgets {
	return Budgets{Turn: 2 * time.Hour}
}

// ParseBudget reads a duration from configuration, refusing what it cannot read.
//
// A malformed budget is an error rather than a silent fallback: the cautious
// direction here is to stop, because a profile whose watchdog quietly reverted to
// a default is one whose author believes they tightened it.
func ParseBudget(value string) (time.Duration, error) {
	d, err := time.ParseDuration(value)
	if err != nil {
		return 0, fmt.Errorf("%q is not a duration (try 30m, 2h, 90s)", value)
	}
	if d <= 0 {
		return 0, fmt.Errorf("a budget must be positive, got %q", value)
	}
	return d, nil
}
