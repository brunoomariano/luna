package cli

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/brunoomariano/luna/src/internal/fsm"
)

// TestABudgetIsSetAtCreationAndReadBack. The ceiling is a term of the run, so it
// belongs in the opening event beside the flow and the knob.
func TestABudgetIsSetAtCreationAndReadBack(t *testing.T) {
	h := newHarness(t)
	h.mustRun(t, "task", "new", "B-1", "--kind", "chore", "--flow", "chore", "--budget-usd", "2.50")

	out := h.mustRun(t, "budget", "B-1")
	if !strings.Contains(out, "$2.50") {
		t.Errorf("the ceiling is not reported:\n%s", out)
	}
	if !strings.Contains(out, "left") {
		t.Errorf("what is left is not reported, which is the question being asked:\n%s", out)
	}
}

// TestATaskWithNoBudgetSaysSoAndSaysHow. Zero is no ceiling, and the reading has
// to distinguish that from a ceiling of zero — which would be a task that can
// never run.
func TestATaskWithNoBudgetSaysSoAndSaysHow(t *testing.T) {
	h := newHarness(t)
	h.mustRun(t, "task", "new", "B-2", "--kind", "chore", "--flow", "chore")

	out := h.mustRun(t, "budget", "B-2")
	if !strings.Contains(out, "no budget") {
		t.Errorf("a task with no ceiling does not say so:\n%s", out)
	}
	if !strings.Contains(out, "luna budget B-2") {
		t.Errorf("it does not say how to set one:\n%s", out)
	}
}

func TestBudgetDoesNotCallUnpricedUsageZero(t *testing.T) {
	var out strings.Builder
	showBudget(Env{Out: &out}, fsm.TaskState{
		ID: "B-codex",
		Spent: map[fsm.StageID]fsm.Spend{
			"build": {Agent: "codex", InputTokens: 100},
		},
	})

	got := out.String()
	if !strings.Contains(got, "cost n/a") || strings.Contains(got, "spent $0") {
		t.Errorf("budget turned an absent Codex price into zero:\n%s", got)
	}
}

func TestBudgetCallsAnExistingCeilingUnenforceableWhenUsageIsUnpriced(t *testing.T) {
	var out strings.Builder
	showBudget(Env{Out: &out}, fsm.TaskState{
		ID: "B-codex", BudgetUSD: 5,
		Spent: map[fsm.StageID]fsm.Spend{
			"build": {Agent: "codex", InputTokens: 100},
		},
	})

	if got := out.String(); !strings.Contains(got, "unenforceable") {
		t.Errorf("an existing ceiling hid its missing price:\n%s", got)
	}
}

// TestMovingTheBudgetIsAnEvent. The change is a decision, and a run that cost
// four times its ceiling has to be reviewable afterwards — which needs the log to
// say when the ceiling moved and why.
func TestMovingTheBudgetIsAnEvent(t *testing.T) {
	h := newHarness(t)
	h.mustRun(t, "task", "new", "B-3", "--kind", "chore", "--flow", "chore", "--budget-usd", "1")

	before, err := h.env.Store.Events("B-3")
	if err != nil {
		t.Fatalf("reading the log: %v", err)
	}

	out := h.mustRun(t, "budget", "B-3", "5", "worth finishing")
	if !strings.Contains(out, "$1.00 → $5.00") {
		t.Errorf("the move is not reported:\n%s", out)
	}

	after, err := h.env.Store.Events("B-3")
	if err != nil {
		t.Fatalf("reading the log: %v", err)
	}
	if len(after) != len(before)+1 {
		t.Fatalf("moving the ceiling wrote %d events", len(after)-len(before))
	}
	if !strings.Contains(after[len(after)-1].Payload, "worth finishing") {
		t.Errorf("the reason did not reach the log: %s", after[len(after)-1].Payload)
	}

	state, err := h.env.Store.ReplayOwnFlow("B-3")
	if err != nil {
		t.Fatalf("replaying: %v", err)
	}
	if state.BudgetUSD != 5 {
		t.Errorf("the ceiling replayed as %v", state.BudgetUSD)
	}
}

// TestABudgetThatIsNotAnAmountIsRefused, before the log is written — the opening
// event is append-only, and a task opened with a ceiling nobody meant is one that
// stops in a way nobody expects.
func TestABudgetThatIsNotAnAmountIsRefused(t *testing.T) {
	h := newHarness(t)

	for _, bad := range []string{"lots", "-1"} {
		if err := h.run(t, "task", "new", "B-x", "--kind", "chore", "--budget-usd", bad); err == nil {
			t.Errorf("--budget-usd %q was accepted", bad)
		}
		events, _ := h.env.Store.Events("B-x")
		if len(events) != 0 {
			t.Errorf("--budget-usd %q left %d event(s) behind", bad, len(events))
		}
	}
}

// TestTheSpendColumnShowsTheCeiling. A total on its own does not say whether the
// task can finish, which is the only thing anyone reads that column for on a run
// nobody watched.
func TestTheSpendColumnShowsTheCeiling(t *testing.T) {
	var out strings.Builder
	env := Env{Out: &out}

	printSpend(env, fsm.TaskState{
		ID:        "B-4",
		BudgetUSD: 10,
		Spent:     map[fsm.StageID]fsm.Spend{"build": {CostUSD: 4, InputTokens: 10}},
	}, fsm.DefaultFlow())

	if got := out.String(); !strings.Contains(got, "budget") || !strings.Contains(got, "10.00") {
		t.Errorf("the ceiling is missing from the spend column:\n%s", got)
	}
}

// TestBudgetRefusesWhatItCannotAnswer covers the two ways the command is called
// wrong, both before anything is written.
func TestBudgetRefusesWhatItCannotAnswer(t *testing.T) {
	h := newHarness(t)

	if err := h.run(t, "budget"); err == nil {
		t.Error("budget with no task id was accepted")
	}
	if err := h.run(t, "budget", "nobody"); err == nil {
		t.Error("budget for a task that does not exist was accepted")
	}

	h.mustRun(t, "task", "new", "B-5", "--kind", "chore", "--flow", "chore")
	if err := h.run(t, "budget", "B-5", "lots"); err == nil {
		t.Error("a ceiling that is not an amount was accepted")
	}
}

// TestABlockedTaskIsToldRaisingIsNotResuming. The ceiling moving does not restart
// anything, and not saying so makes the next question "I raised it, why is it
// still stopped?".
func TestABlockedTaskIsToldRaisingIsNotResuming(t *testing.T) {
	h := newHarness(t)
	h.mustRun(t, "task", "new", "B-6", "--kind", "chore", "--flow", "chore", "--budget-usd", "1")

	chore, err := fsm.FlowNamed("chore")
	if err != nil {
		t.Fatalf("loading the chore flow: %v", err)
	}
	if err := h.env.Store.AppendAction("B-6", fsm.Advance{Flow: chore}); err != nil {
		t.Fatalf("opening the first stage: %v", err)
	}
	if err := h.env.Store.AppendAction("B-6", fsm.Complete{
		Flow:      chore,
		Delivered: []fsm.Artifact{"worktree"},
		Evidence:  map[fsm.Artifact]fsm.Evidence{"worktree": fsm.Exists(1)},
		Spent:     fsm.Spend{CostUSD: 4},
		Commit:    "deadbeef",
	}); err != nil {
		t.Fatalf("closing the first stage: %v", err)
	}
	if err := h.env.Store.AppendAction("B-6", fsm.Advance{Flow: chore}); err != nil {
		t.Fatalf("advancing: %v", err)
	}

	over := h.mustRun(t, "budget", "B-6")
	if !strings.Contains(over, "no further stage opens") {
		t.Errorf("a task over its ceiling does not say what that means:\n%s", over)
	}

	out := h.mustRun(t, "budget", "B-6", "10")
	if !strings.Contains(out, "unblock") {
		t.Errorf("raising the ceiling does not say the task is still stopped:\n%s", out)
	}
}

// TestTheStructuredViewCarriesTheFlowAndTheBill. The printed view has shown what
// a task cost since the transport started reporting usage; the JSON one did not,
// so every consumer that reads it — a dashboard, the benchmark, a morning report —
// had to shell out and parse a column written for a person.
func TestTheStructuredViewCarriesTheFlowAndTheBill(t *testing.T) {
	h := newHarness(t)
	h.mustRun(t, "task", "new", "J-1", "--kind", "chore", "--flow", "chore", "--budget-usd", "3")

	out := h.mustRun(t, "task", "show", "J-1", "--json")

	var report TaskReport
	if err := json.Unmarshal([]byte(out), &report); err != nil {
		t.Fatalf("the structured view does not parse: %v\n%s", err, out)
	}
	if report.Flow != "chore" {
		t.Errorf("the flow is missing from the structured view: %q", report.Flow)
	}
	if report.Spend == nil {
		t.Fatalf("the bill is missing from the structured view:\n%s", out)
	}
	if report.Spend.BudgetUSD != 3 {
		t.Errorf("the ceiling is missing: %v", report.Spend.BudgetUSD)
	}
}

// TestATaskThatCostNothingReportsNoBill. Absent rather than zeroed, because a
// reader seeing `"cost_usd": 0` cannot tell "nothing was spent" from "nothing is
// recorded", and only one of those is a fact about the run.
func TestATaskThatCostNothingReportsNoBill(t *testing.T) {
	h := newHarness(t)
	h.mustRun(t, "task", "new", "J-2", "--kind", "chore", "--flow", "chore")

	var report TaskReport
	if err := json.Unmarshal([]byte(h.mustRun(t, "task", "show", "J-2", "--json")), &report); err != nil {
		t.Fatalf("the structured view does not parse: %v", err)
	}
	if report.Spend != nil {
		t.Errorf("a task that has cost nothing reports a bill: %+v", report.Spend)
	}
}

// TestTheBillIsBrokenDownInFlowOrder. How much is half the question; where it
// went is the other half, and map order would make two runs of the same task
// print the stages differently.
func TestTheBillIsBrokenDownInFlowOrder(t *testing.T) {
	flow := fsm.DefaultFlow()
	state := fsm.TaskState{
		ID: "J-3",
		Spent: map[fsm.StageID]fsm.Spend{
			"shipping": {CostUSD: 2, InputTokens: 20, Turns: 3, Context: "fresh"},
			"forge":    {CostUSD: 1, InputTokens: 10, Turns: 2, Context: "live"},
		},
	}

	report := spendReport(state, flow)
	if report == nil || len(report.Stages) != 2 {
		t.Fatalf("want two stages in the breakdown, got %+v", report)
	}
	if report.Stages[0].Stage != "forge" || report.Stages[1].Stage != "shipping" {
		t.Errorf("the breakdown is not in flow order: %s then %s",
			report.Stages[0].Stage, report.Stages[1].Stage)
	}
	if report.CostUSD != 3 {
		t.Errorf("the total is %v, want 3", report.CostUSD)
	}
	if report.Stages[1].Context != "fresh" {
		t.Errorf("whether the call resumed is missing: %+v", report.Stages[1])
	}
}
