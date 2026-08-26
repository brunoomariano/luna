package cli

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"sync"
	"testing"

	"github.com/brunoomariano/luna/src/internal/fsm"
)

// TestAFleetRunsEveryEligibleTask. The unit of parallelism is the task, which is
// not a fleet decision but the property the whole design rests on: one worktree
// and one lead per task is what makes two tasks unable to see each other's work.
func TestAFleetRunsEveryEligibleTask(t *testing.T) {
	h := newHarness(t)
	for _, id := range []string{"F-1", "F-2", "F-3"} {
		h.mustRun(t, "task", "new", id, "--kind", "chore", "--flow", "chore", "--simulated")
	}

	out := h.mustRun(t, "fleet", "run", "--dry-run", "--concurrency", "2")

	for _, id := range []string{"F-1", "F-2", "F-3"} {
		if !strings.Contains(out, id) {
			t.Errorf("%s was not run:\n%s", id, out)
		}
		state, err := h.env.Store.ReplayOwnFlow(id)
		if err != nil {
			t.Fatalf("replaying %s: %v", id, err)
		}
		if state.Operation() != fsm.OperationClean {
			t.Errorf("%s ended as %q", id, state.Operation())
		}
	}
}

// TestABlockedTaskIsNotEligible. It stopped for a reason somebody has to deal
// with, and a fleet that retried it every night would turn a notified block into
// a nightly bill.
func TestABlockedTaskIsNotEligible(t *testing.T) {
	h := newHarness(t)
	h.mustRun(t, "task", "new", "F-4", "--kind", "chore", "--flow", "chore", "--simulated")

	chore, err := fsm.FlowNamed("chore")
	if err != nil {
		t.Fatalf("loading the chore flow: %v", err)
	}
	if err := h.env.Store.AppendAction("F-4", fsm.Advance{Flow: chore}); err != nil {
		t.Fatalf("opening a stage: %v", err)
	}
	if err := h.env.Store.AppendAction("F-4", fsm.Block{Reason: "git is not installed"}); err != nil {
		t.Fatalf("blocking: %v", err)
	}

	eligible, err := eligibleTasks(h.env, "")
	if err != nil {
		t.Fatalf("listing what is eligible: %v", err)
	}
	for _, id := range eligible {
		if id == "F-4" {
			t.Error("a blocked task was picked up by the fleet")
		}
	}

	// And unblocking is what makes it eligible again, which is the whole recovery
	// path — otherwise a block would be permanent.
	h.mustRun(t, "unblock", "F-4")
	eligible, err = eligibleTasks(h.env, "")
	if err != nil {
		t.Fatalf("listing what is eligible: %v", err)
	}
	if !contains(eligible, "F-4") {
		t.Errorf("an unblocked task did not become eligible again: %v", eligible)
	}
}

// TestAFleetRunsOnlyTheFlowItWasAskedFor. A night given to mechanical work should
// not pick up the feature somebody left half-planned.
func TestAFleetRunsOnlyTheFlowItWasAskedFor(t *testing.T) {
	h := newHarness(t)
	h.mustRun(t, "task", "new", "F-5", "--kind", "chore", "--flow", "chore", "--simulated")
	h.mustRun(t, "task", "new", "F-6", "--kind", "bug", "--flow", "fix", "--simulated")

	eligible, err := eligibleTasks(h.env, "fix")
	if err != nil {
		t.Fatalf("listing what is eligible: %v", err)
	}
	if len(eligible) != 1 || eligible[0] != "F-6" {
		t.Errorf("the flow filter picked up %v", eligible)
	}
}

// TestTheFleetCeilingStopsStartingRatherThanStopsRunning. A task already under way
// holds a worktree and an agent, and killing it mid-stage would spend the money
// and throw away the delivery — the same reasoning as a task's own ceiling
// stopping the next stage rather than the one that paid.
func TestTheFleetCeilingStopsStartingRatherThanStopsRunning(t *testing.T) {
	h := newHarness(t)
	if err := h.run(t, "fleet", "run", "--budget-usd", "-1"); err == nil {
		t.Error("a negative fleet ceiling was accepted")
	}
	if err := h.run(t, "fleet", "run", "--concurrency", "0"); err == nil {
		t.Error("a concurrency of zero was accepted — it would start nothing and report success")
	}
	if err := h.run(t, "fleet", "run", "--nonsense"); err == nil {
		t.Error("an unknown flag was accepted")
	}
}

// TestTheMorningReportGroupsByWhatHasToHappen. The question somebody opens it with
// is "what do I have to do?", and a flat list of every task answers it only after
// they have read all of it.
func TestTheMorningReportGroupsByWhatHasToHappen(t *testing.T) {
	h := newHarness(t)
	h.mustRun(t, "task", "new", "F-7", "--kind", "chore", "--flow", "chore", "--simulated")
	h.mustRun(t, "fleet", "run", "--dry-run")

	h.mustRun(t, "task", "new", "F-8", "--kind", "chore", "--flow", "chore")

	out := h.mustRun(t, "fleet", "report")
	for _, heading := range []string{"ready", "still running", "spent"} {
		if !strings.Contains(out, heading) {
			t.Errorf("the report has no %q group:\n%s", heading, out)
		}
	}
}

// TestTheMorningReportIsReadableByAMachine. The report is the fleet's product, and
// a product only a person can read cannot be piped into anything.
func TestTheMorningReportIsReadableByAMachine(t *testing.T) {
	h := newHarness(t)
	h.mustRun(t, "task", "new", "F-9", "--kind", "bug", "--flow", "fix", "--simulated")
	h.mustRun(t, "fleet", "run", "--dry-run")

	var report FleetReport
	if err := json.Unmarshal([]byte(h.mustRun(t, "fleet", "report", "--json")), &report); err != nil {
		t.Fatalf("the report does not parse: %v", err)
	}
	if len(report.Tasks) != 1 {
		t.Fatalf("want one task in the report, got %d", len(report.Tasks))
	}
	if report.Tasks[0].Flow != "fix" || report.Tasks[0].Product != string(fsm.ProductSimulated) {
		t.Errorf("the task is misreported: %+v", report.Tasks[0])
	}
}

// TestAFleetWithNothingToDoSaysSo rather than printing an empty table, which reads
// as a failure to run.
func TestAFleetWithNothingToDoSaysSo(t *testing.T) {
	h := newHarness(t)

	if out := h.mustRun(t, "fleet", "run"); !strings.Contains(out, "nothing to run") {
		t.Errorf("an empty fleet printed:\n%s", out)
	}
	if out := h.mustRun(t, "fleet", "report"); !strings.Contains(out, "nothing to report") {
		t.Errorf("an empty report printed:\n%s", out)
	}
}

// TestFleetRefusesASubcommandItDoesNotHave.
func TestFleetRefusesASubcommandItDoesNotHave(t *testing.T) {
	h := newHarness(t)

	if err := h.run(t, "fleet"); err == nil {
		t.Error("fleet with no subcommand was accepted")
	}
	if err := h.run(t, "fleet", "stampede"); err == nil {
		t.Error("an unknown fleet subcommand was accepted")
	}
	if err := h.run(t, "fleet", "report", "--since", "soon"); err == nil {
		t.Error("a --since that is not a duration was accepted")
	}
}

func contains(ids []string, want string) bool {
	for _, id := range ids {
		if id == want {
			return true
		}
	}
	return false
}

// TestTheFleetReportsATaskThatWentWrongRatherThanSwallowingIt. A task the fleet
// could not drive is exactly the one that would otherwise vanish: nobody watched
// the terminal, and a run that printed nothing about it looks like a run where it
// never existed.
func TestTheFleetReportsATaskThatWentWrongRatherThanSwallowingIt(t *testing.T) {
	h := newHarness(t)
	// A real task, and a dry fleet. The simulation guard refuses to mix the two,
	// which makes it the cheapest honest way to get a task the fleet cannot drive.
	h.mustRun(t, "task", "new", "F-10", "--kind", "chore", "--flow", "chore")

	out := h.mustRun(t, "fleet", "run", "--dry-run")

	if !strings.Contains(out, "F-10") || !strings.Contains(out, "error") {
		t.Errorf("a task the fleet could not drive is not in the report:\n%s", out)
	}
}

// TestTheFleetRunNamesTheBlockThatStoppedEachTask. A column of "stopped" answers
// nothing; the kind is what sorts a morning's work.
func TestTheFleetRunNamesTheBlockThatStoppedEachTask(t *testing.T) {
	blocked := fsm.TaskState{Status: fsm.StatusBlocked, BlockedBy: fsm.BlockBudget}
	if got := operationLine(blocked); !strings.Contains(got, "over-budget") {
		t.Errorf("operationLine = %q, want the block's kind in it", got)
	}

	running := fsm.TaskState{Status: fsm.StatusRunning}
	if got := operationLine(running); got != string(fsm.OperationRunning) {
		t.Errorf("operationLine = %q for a task with no block", got)
	}
}

// TestTheReportWindowExcludesWhatIsOlderThanIt. `--since last-night` is the
// question the morning report is actually opened with, and a window that let
// everything through would make it a listing of the whole store.
func TestTheReportWindowExcludesWhatIsOlderThanIt(t *testing.T) {
	h := newHarness(t)
	h.mustRun(t, "task", "new", "F-11", "--kind", "chore", "--flow", "chore")

	// A window that closed before anything happened.
	var report FleetReport
	out := h.mustRun(t, "fleet", "report", "--since", "1ns", "--json")
	if err := json.Unmarshal([]byte(out), &report); err != nil {
		t.Fatalf("the report does not parse: %v", err)
	}
	if len(report.Tasks) != 0 {
		t.Errorf("a task older than the window was reported: %+v", report.Tasks)
	}

	// And a window wide enough to contain it.
	out = h.mustRun(t, "fleet", "report", "--since", "24h", "--json")
	if err := json.Unmarshal([]byte(out), &report); err != nil {
		t.Fatalf("the report does not parse: %v", err)
	}
	if len(report.Tasks) != 1 {
		t.Errorf("want the task inside a 24h window, got %+v", report.Tasks)
	}
}

// TestTheFleetTakesTheAgentOverride, so a whole night can be pinned to one
// harness while the roles are still being tuned.
func TestTheFleetTakesTheAgentOverride(t *testing.T) {
	opts, err := parseFleetOptions([]string{"--agent", "codex", "--flow", "fix", "--budget-usd", "20"})
	if err != nil {
		t.Fatalf("parsing: %v", err)
	}
	if opts.run.Agent != "codex" || opts.flow != "fix" || opts.budgetUSD != 20 {
		t.Errorf("the flags did not land: %+v", opts)
	}
	if opts.concurrency != DefaultConcurrency {
		t.Errorf("concurrency defaulted to %d", opts.concurrency)
	}
}

// TestParsePositiveRefusesWhatWouldStartNothing. Zero read as "no limit" would be
// a fleet that starts no task and reports success, which is the silent no-op this
// project treats as worse than an error.
func TestParsePositiveRefusesWhatWouldStartNothing(t *testing.T) {
	for _, bad := range []string{"0", "-3", "many", ""} {
		if _, err := parsePositive(bad); err == nil {
			t.Errorf("parsePositive(%q) was accepted", bad)
		}
	}
	if n, err := parsePositive("8"); err != nil || n != 8 {
		t.Errorf("parsePositive(\"8\") = %d (%v)", n, err)
	}
}

// TestTheFleetStopsStartingOnceItsCeilingIsReached is the behaviour the flag
// exists for, as opposed to the flag being parsed.
//
// The ceiling is checked before each task is started, so what it bounds is how
// much more the night may cost — not how much it already has. A task already
// under way is holding a worktree and an agent, and stopping it would spend the
// money and throw away the delivery.
func TestTheFleetStopsStartingOnceItsCeilingIsReached(t *testing.T) {
	h := newHarness(t)

	chore, err := fsm.FlowNamed("chore")
	if err != nil {
		t.Fatalf("loading the chore flow: %v", err)
	}

	// Named so the expensive one is first: the fleet takes them in id order, and
	// the ceiling can only stop what has not started.
	h.mustRun(t, "task", "new", "A-COSTLY", "--kind", "chore", "--flow", "chore", "--simulated")
	if err := h.env.Store.AppendAction("A-COSTLY", fsm.Advance{Flow: chore}); err != nil {
		t.Fatalf("opening a stage: %v", err)
	}
	if err := h.env.Store.AppendAction("A-COSTLY", fsm.Complete{
		Flow:      chore,
		Delivered: []fsm.Artifact{"worktree"},
		Evidence:  map[fsm.Artifact]fsm.Evidence{"worktree": fsm.Exists(1)},
		Spent:     fsm.Spend{CostUSD: 9},
		Commit:    "abc",
	}); err != nil {
		t.Fatalf("recording the spend: %v", err)
	}

	h.mustRun(t, "task", "new", "B-NEXT", "--kind", "chore", "--flow", "chore", "--simulated")
	h.mustRun(t, "task", "new", "C-NEXT", "--kind", "chore", "--flow", "chore", "--simulated")

	// One at a time, so the first task's bill is in before the second is weighed.
	out := h.mustRun(t, "fleet", "run", "--dry-run", "--concurrency", "1", "--budget-usd", "5")

	if !strings.Contains(out, "ceiling of $5.00 was reached") {
		t.Errorf("the fleet did not stop on its ceiling:\n%s", out)
	}
	if !strings.Contains(out, "still eligible") {
		t.Errorf("the fleet does not say the unstarted tasks can still be run:\n%s", out)
	}

	// And what it did not start was genuinely not started.
	for _, id := range []string{"B-NEXT", "C-NEXT"} {
		state, err := h.env.Store.ReplayOwnFlow(id)
		if err != nil {
			t.Fatalf("replaying %s: %v", id, err)
		}
		if state.Operation() == fsm.OperationClean {
			t.Errorf("%s ran despite the ceiling", id)
		}
	}
}

// TestATaskThatNoLongerReplaysIsReportedRatherThanSkipped. It is exactly the task
// that would otherwise sit unnoticed forever, which is the second form of silent
// failure INV-5 names.
func TestATaskThatNoLongerReplaysIsReportedRatherThanSkipped(t *testing.T) {
	h := newHarness(t)

	if err := h.env.Store.AppendAction("GONE-1", fsm.TaskCreated{
		Kind: fsm.KindFeature, FlowName: "a-flow-nobody-ships",
	}); err != nil {
		t.Fatalf("creating the task: %v", err)
	}

	out := h.mustRun(t, "fleet", "report")
	if !strings.Contains(out, "no longer replay") || !strings.Contains(out, "GONE-1") {
		t.Errorf("a task that cannot be replayed is missing from the report:\n%s", out)
	}

	// And the fleet does not try to run it: there is nothing to run it against.
	eligible, err := eligibleTasks(h.env, "")
	if err != nil {
		t.Fatalf("listing what is eligible: %v", err)
	}
	if contains(eligible, "GONE-1") {
		t.Error("a task whose flow is gone was picked up by the fleet")
	}
}

// TestTheReportShowsATaskSomebodyCalledOff. Every ending gets a pile, or a task
// disappears from the morning's account of what happened.
func TestTheReportShowsATaskSomebodyCalledOff(t *testing.T) {
	h := newHarness(t)
	h.mustRun(t, "task", "new", "OFF-1", "--kind", "chore", "--flow", "chore")
	h.mustRun(t, "task", "abandon", "OFF-1", "superseded")

	out := h.mustRun(t, "fleet", "report")
	if !strings.Contains(out, "called off") || !strings.Contains(out, "OFF-1") {
		t.Errorf("an abandoned task is missing from the report:\n%s", out)
	}
}

// fleetLead reports every stage of whichever task it is asked about.
//
// A named fake rather than a canned string, and one that reads the task id out
// of the order it is given: the fleet drives several at once, and a lead that
// assumed one id would pass this test by conducting the same task N times.
type fleetLead struct {
	h  *harness
	t  *testing.T
	mu sync.Mutex
}

func (l *fleetLead) ask(_ context.Context, prompt string) (string, error) {
	// The task's id comes off the order the lead was handed, not out of band: a
	// fake told which task it is would pass this test by conducting the same one
	// N times, which is the exact failure a fleet has.
	id := ""
	for _, line := range strings.Split(prompt, "\n") {
		if rest, found := strings.CutPrefix(strings.TrimSpace(line), "task="); found {
			id = rest
			break
		}
	}
	if id == "" {
		return "", fmt.Errorf("the order named no task:\n%s", prompt)
	}

	l.mu.Lock()
	defer l.mu.Unlock()

	state, err := l.h.env.replay(id)
	if err != nil {
		return "", err
	}
	flow, err := l.h.env.flowOf(id)
	if err != nil {
		return "", err
	}
	order, err := fsm.NextOrder(state, flow, l.h.env.profiles().Roles)
	if err != nil {
		return "", err
	}

	owed := strings.Join(artifactNames(order.Produces), ",")
	if err := Run(l.h.env, []string{"done", id, "--delivered", owed}); err != nil {
		return "", err
	}
	return "carried out " + string(order.Stage), nil
}

// TestAFleetConductsThroughTheLead is the fleet's engine.
//
// It used to drive its own way — Luna advancing and running a node per stage,
// with no lead conducting anything — while `luna lead` drove the other. Two
// engines reaching the same states meant every fix landed on one of them, and
// the fleet was the half nobody was watching. This asserts the fleet goes
// through the lead, by counting the leads: a fleet that drove itself would
// finish these tasks without asking a model anything.
func TestAFleetConductsThroughTheLead(t *testing.T) {
	h := newHarness(t)
	for _, id := range []string{"F-1", "F-2"} {
		h.mustRun(t, "task", "new", id, "--kind", "chore", "--flow", "chore")
	}

	lead := &fleetLead{h: h, t: t}
	h.env.Lead = lead.ask

	out := h.mustRun(t, "fleet", "run", "--concurrency", "1")

	for _, id := range []string{"F-1", "F-2"} {
		state, err := h.env.Store.ReplayOwnFlow(id)
		if err != nil {
			t.Fatalf("replaying %s: %v", id, err)
		}
		if state.Stage == "" {
			t.Errorf("%s never entered a stage, so no lead conducted it", id)
		}
		// Every line a task wrote carries its id, because N leads narrate at once
		// and interleaved half-sentences cannot be read back to a task.
		if !strings.Contains(out, id+" | ") {
			t.Errorf("%s's output is not attributed to it:\n%s", id, out)
		}
	}
}

// TestAFleetWithNoLeadSaysSo. Luna hosts no model, and a fleet that quietly did
// nothing would look like a fleet with nothing eligible.
func TestAFleetWithNoLeadSaysSo(t *testing.T) {
	h := newHarness(t)
	h.mustRun(t, "task", "new", "F-1", "--kind", "chore", "--flow", "chore")
	h.env.Lead = nil

	out := h.mustRun(t, "fleet", "run")

	if !strings.Contains(out, "no lead is configured") {
		t.Errorf("a fleet with no model did not say why nothing happened:\n%s", out)
	}
}

// TestASimulatedTaskIsNotConductedForReal. Its stages recorded checks that never
// ran, so continuing it for real would build on proof nobody produced.
func TestASimulatedTaskIsNotConductedForReal(t *testing.T) {
	h := newHarness(t)
	h.mustRun(t, "task", "new", "F-1", "--kind", "chore", "--flow", "chore", "--simulated")

	lead := &fleetLead{h: h, t: t}
	h.env.Lead = lead.ask

	out := h.mustRun(t, "fleet", "run")

	if !strings.Contains(out, "simulation") {
		t.Errorf("a simulated task was conducted for real:\n%s", out)
	}
}
