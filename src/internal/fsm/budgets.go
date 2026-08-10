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
// stall arrives as a fact inside an action (ADR-0024, ADR-0034).
//
// Two budgets rather than one. A real build runs silent for many minutes, so a
// single window either kills legitimate work or is too loose to catch anything.
type Budgets struct {
	// Idle is how long an agent may go without reacting when nothing is running.
	Idle time.Duration

	// Tool is the far larger window that applies while a tool is in flight. A
	// compile is not a stall.
	Tool time.Duration
}

// DefaultBudgets are the values the study settled on, from the one project that
// had been burned by getting them wrong.
//
// Deliberately absent: a wall-clock cap. multica removed theirs after it killed
// legitimate long runs, and the lesson is that progress, not elapsed time, is the
// signal worth acting on.
func DefaultBudgets() Budgets {
	return Budgets{Idle: 30 * time.Minute, Tool: 2 * time.Hour}
}

// Resolve fills in whatever the profile left unstated.
//
// A profile that names one budget and not the other still works, and a profile
// that names neither behaves exactly as one written before budgets existed. Both
// are the ordinary case, not an error.
func (b Budgets) Resolve() Budgets {
	defaults := DefaultBudgets()
	if b.Idle <= 0 {
		b.Idle = defaults.Idle
	}
	if b.Tool <= 0 {
		b.Tool = defaults.Tool
	}
	return b
}

// For reports the budget that applies right now.
//
// The distinction is the whole point: silence while a tool runs is a build, and
// silence with nothing running is an agent that stopped (ADR-0034).
func (b Budgets) For(toolInFlight bool) time.Duration {
	resolved := b.Resolve()
	if toolInFlight {
		return resolved.Tool
	}
	return resolved.Idle
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
