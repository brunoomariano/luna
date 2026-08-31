package cli

import (
	"context"
	"encoding/json"
	"fmt"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/brunoomariano/luna/src/internal/fsm"
	"github.com/brunoomariano/luna/src/internal/store"
)

// TestTheMorningReportGroupsByWhatHasToHappen. The question somebody opens it with
// is "what do I have to do?", and a flat list of every task answers it only after
// they have read all of it.
func TestTheMorningReportGroupsByWhatHasToHappen(t *testing.T) {
	h := newHarness(t)
	h.mustRun(t, "task", "new", "F-7", "--kind", "chore", "--flow", "chore", "--simulated")
	h.mustRun(t, "fleet", "run", "F-7", "--dry-run")

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
	h.mustRun(t, "fleet", "run", "F-9", "--dry-run")

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

func TestAGlobalReportWindowReadsEachProjectsClock(t *testing.T) {
	h := newHarness(t)
	central := h.env.Store
	for _, project := range []string{"one-project", "two-project"} {
		if err := central.ForProject(project).AppendAction("TASK-"+project, fsm.TaskCreated{
			Kind: fsm.KindChore, Flow: fsm.Fingerprint(fsm.DefaultFlow()),
		}); err != nil {
			t.Fatalf("seeding %s: %v", project, err)
		}
	}
	h.env.Store = central.ForProject("one-project")
	h.env.GlobalStore = central

	var report FleetReport
	out := h.mustRun(t, "fleet", "report", "--since", "24h", "--json")
	if err := json.Unmarshal([]byte(out), &report); err != nil {
		t.Fatalf("decoding the global report: %v", err)
	}
	if len(report.Tasks) != 2 {
		t.Fatalf("global report tasks = %+v", report.Tasks)
	}
}

func TestFleetReportSurfacesEmptyInvalidAndStorageStates(t *testing.T) {
	h := newHarness(t)
	if out := h.mustRun(t, "fleet", "report"); !strings.Contains(out, "nothing to report") {
		t.Fatalf("empty fleet report = %q", out)
	}
	for _, args := range [][]string{
		{"fleet", "report", "--since"},
		{"fleet", "report", "--unknown", "value"},
		{"fleet", "run"},
	} {
		if err := h.run(t, args...); err == nil {
			t.Errorf("luna %s was accepted", strings.Join(args, " "))
		}
	}

	db, err := store.OpenAs(filepath.Join(t.TempDir(), "closed.db"), store.LunaOwnsTheLog)
	if err != nil {
		t.Fatal(err)
	}
	if err := db.Close(); err != nil {
		t.Fatal(err)
	}
	_, err = changedSince(Env{Store: db}, globalTask{ID: "TASK-1"}, time.Hour)
	if err == nil {
		t.Fatal("a closed central store was reported as a valid time window")
	}
}

func TestFleetReportPrintsAConcreteBlocker(t *testing.T) {
	h := newHarness(t)
	printFleetGroup(h.env, "stopped", []FleetTaskReport{{
		Project: "one-project", ID: "TASK-1", BlockedBy: "network unavailable",
	}})
	if !strings.Contains(h.out.String(), "network unavailable") {
		t.Fatalf("fleet group omitted the blocker:\n%s", h.out.String())
	}

	opts, err := parseFleetOptions([]string{"--repo", "/tmp/project"})
	if err != nil || opts.run.Repo != "/tmp/project" {
		t.Fatalf("fleet repository override = %+v, %v", opts, err)
	}
}

// TestTheFleetTakesTheAgentOverride, so a whole night can be pinned to one
// harness while the roles are still being tuned.
func TestTheFleetTakesTheAgentOverride(t *testing.T) {
	opts, err := parseFleetOptions([]string{"--agent", "codex", "--autonomy", "7"})
	if err != nil {
		t.Fatalf("parsing: %v", err)
	}
	if opts.run.Agent != "codex" {
		t.Errorf("the agent override did not land: %+v", opts)
	}
	if opts.knob != 7 {
		t.Errorf("the knob did not land: %+v", opts)
	}
	if !opts.knobSet {
		t.Errorf("the invocation knob was not marked as present: %+v", opts)
	}
}

func TestTheFleetCanLowerAutonomyForOneInvocation(t *testing.T) {
	opts, err := parseFleetOptions([]string{"--autonomy", "0"})
	if err != nil {
		t.Fatalf("parsing: %v", err)
	}
	if opts.knob != fsm.KnobAsk || !opts.knobSet {
		t.Errorf("want an explicit knob 0, got %+v", opts)
	}
}

// TestAPackRunRefusesTheFlagsTheCrossTaskFleetHad keeps a removed capability from
// reading as a working one.
//
// `--flow`, `--concurrency` and `--budget-usd` selected and bounded a fleet over
// many tasks. A pack is inside one task: its flow is the task's, its concurrency
// is what the flow declares, and its ceiling is the task's own budget. Accepting
// those flags and ignoring them would be the silent no-op this project treats as
// worse than an error.
func TestAPackRunRefusesTheFlagsTheCrossTaskFleetHad(t *testing.T) {
	for _, flag := range []string{"--flow", "--concurrency", "--budget-usd"} {
		if _, err := parseFleetOptions([]string{flag, "4"}); err == nil {
			t.Errorf("%s was accepted by a pack run, which has nothing to do with it", flag)
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
	if !strings.Contains(out, "unreadable") || !strings.Contains(out, "GONE-1") {
		t.Errorf("a task that cannot be replayed is missing from the report:\n%s", out)
	}

	// And a pack cannot be run on it either: there is nothing to run it against.
	if err := h.run(t, "fleet", "run", "GONE-1"); err == nil {
		t.Error("a pack started on a task whose flow this build cannot read")
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
	order, err := fsm.NextOrder(state, flow)
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

	out := h.mustRun(t, "fleet", "run", "F-1")
	out += h.mustRun(t, "fleet", "run", "F-2")

	for _, id := range []string{"F-1", "F-2"} {
		state, err := h.env.Store.ReplayOwnFlow(id)
		if err != nil {
			t.Fatalf("replaying %s: %v", id, err)
		}
		if state.Stage == "" {
			t.Errorf("%s never entered a stage, so no lead conducted it", id)
		}
		if !strings.Contains(out, id) {
			t.Errorf("the run said nothing about %s:\n%s", id, out)
		}
	}
}

// TestAFleetWithNoLeadSaysSo. Luna hosts no model, and a fleet that quietly did
// nothing would look like a pack that quietly did nothing.
func TestAFleetWithNoLeadSaysSo(t *testing.T) {
	h := newHarness(t)
	h.mustRun(t, "task", "new", "F-1", "--kind", "chore", "--flow", "chore")
	h.env.Lead = nil

	err := h.run(t, "fleet", "run", "F-1")

	if err == nil || !strings.Contains(err.Error(), "no lead is configured") {
		t.Errorf("a pack with no model to conduct it did not say so: %v", err)
	}
}

// TestASimulatedTaskIsNotConductedForReal. Its stages recorded checks that never
// ran, so continuing it for real would build on proof nobody produced.
func TestASimulatedTaskIsNotConductedForReal(t *testing.T) {
	h := newHarness(t)
	h.mustRun(t, "task", "new", "F-1", "--kind", "chore", "--flow", "chore", "--simulated")

	lead := &fleetLead{h: h, t: t}
	h.env.Lead = lead.ask

	err := h.run(t, "fleet", "run", "F-1")

	if err == nil || !strings.Contains(err.Error(), "simulation") {
		t.Errorf("a simulated task was conducted for real: %v", err)
	}
}
