package cli

import (
	"strings"
	"testing"

	"github.com/brunoomariano/luna/src/internal/fsm"
)

// TestTaskShowReportsWhatTheTaskCost is the number reaching a person.
//
// Recording the price and never showing it would be the shape INV-5 rejects
// elsewhere: a fact that exists in the log and that nobody can find without
// knowing it is there. The measurement was the point of collecting it.
func TestTaskShowReportsWhatTheTaskCost(t *testing.T) {
	var out strings.Builder
	env := Env{Out: &out}

	state := fsm.TaskState{
		ID: "T-1",
		Spent: map[fsm.StageID]fsm.Spend{
			"build":  {InputTokens: 1000, OutputTokens: 200, CostUSD: 0.30, Turns: 4, Context: "fresh"},
			"verify": {InputTokens: 500, CacheRead: 9000, CostUSD: 0.05, Turns: 2, Context: "live"},
		},
	}

	printSpend(env, state)
	got := out.String()

	for _, want := range []string{"spent", "build", "verify", "total", "fresh", "live"} {
		if !strings.Contains(got, want) {
			t.Errorf("want %q in the report, got:\n%s", want, got)
		}
	}
	// 1000+200 + 500+9000 — the cache read counts, which is what keeps a
	// continued session from looking free.
	if !strings.Contains(got, "10700") {
		t.Errorf("want the total to count cached tokens (10700), got:\n%s", got)
	}
	if !strings.Contains(got, "0.3500") {
		t.Errorf("want the costs summed (0.35), got:\n%s", got)
	}
}

// TestATaskThatSpentNothingSaysNothing covers the task with no agent stage yet,
// and every task that ran before the accounting existed. A row of zeros would
// suggest a measurement that never happened.
func TestATaskThatSpentNothingSaysNothing(t *testing.T) {
	var out strings.Builder
	printSpend(Env{Out: &out}, fsm.TaskState{ID: "T-2"})

	if got := out.String(); got != "" {
		t.Errorf("want silence for a task that spent nothing, got:\n%s", got)
	}
}

// TestTheReportReadsInFlowOrder covers why the stages are not printed in map
// order: the column is read against the run, and Go randomises map iteration, so
// the same task would print differently every time.
func TestTheReportReadsInFlowOrder(t *testing.T) {
	var out strings.Builder
	state := fsm.TaskState{
		ID: "T-3",
		Spent: map[fsm.StageID]fsm.Spend{
			"review": {InputTokens: 1, CostUSD: 0.01},
			"build":  {InputTokens: 1, CostUSD: 0.01},
			"plan":   {InputTokens: 1, CostUSD: 0.01},
		},
	}

	printSpend(Env{Out: &out}, state)
	got := out.String()

	plan := strings.Index(got, "plan")
	build := strings.Index(got, "build")
	review := strings.Index(got, "review")
	if plan >= build || build >= review {
		t.Errorf("want the stages in flow order, got:\n%s", got)
	}
}
