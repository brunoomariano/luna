package cli

import (
	"strings"
	"testing"

	"github.com/brunoomariano/luna/src/internal/fsm"
)

// TestATaskRecordsTheFlowItNamed. The name is what a later replay resolves, so it
// has to be in the opening event and not inferred from anything a build happens
// to call default.
func TestATaskRecordsTheFlowItNamed(t *testing.T) {
	h := newHarness(t)

	out := h.mustRun(t, "task", "new", "FIX-1", "--kind", "bug", "--flow", "fix")
	if !strings.Contains(out, "flow=fix/") {
		t.Errorf("the flow the task was opened under is not reported:\n%s", out)
	}

	state, err := h.env.Store.ReplayOwnFlow("FIX-1")
	if err != nil {
		t.Fatalf("replaying a task on its own flow: %v", err)
	}
	if state.FlowName != "fix" {
		t.Errorf("the task recorded flow %q, want %q", state.FlowName, "fix")
	}

	fix, err := fsm.FlowNamed("fix")
	if err != nil {
		t.Fatalf("loading the fix flow: %v", err)
	}
	if want := fsm.Fingerprint(fix); state.Flow != want {
		t.Errorf("the task recorded fingerprint %s, want %s", state.Flow, want)
	}
}

// TestATaskOnALeanFlowIsOrderedThroughThatFlow is the whole point of the feature:
// the order a task gets has to come from its own contract. Before flows were
// named, every task was ordered through the shipped one — so a task meant to skip
// planning would have been sent to `intake` regardless.
func TestATaskOnALeanFlowIsOrderedThroughThatFlow(t *testing.T) {
	h := newHarness(t)
	h.mustRun(t, "task", "new", "FIX-2", "--kind", "bug", "--flow", "fix")

	order := h.mustRun(t, "next", "FIX-2")
	if !strings.Contains(order, "stage=setup") {
		t.Fatalf("the first order is not the flow's first stage:\n%s", order)
	}

	// And the stage after setup is the one `fix` puts there, not the one `full`
	// does: `diagnose` rather than `intake`.
	h.mustRun(t, "next", "FIX-2")
	full := h.mustRun(t, "status", "FIX-2")
	if strings.Contains(full, "intake") {
		t.Errorf("a task on the fix flow was shown a stage that flow does not have:\n%s", full)
	}
}

// TestAnUnknownFlowIsRefusedBeforeTheLogIsWritten. The opening event goes into an
// append-only log, so a task opened against a flow that does not exist is one no
// command can read afterwards — including the one that would abandon it.
func TestAnUnknownFlowIsRefusedBeforeTheLogIsWritten(t *testing.T) {
	h := newHarness(t)

	if err := h.run(t, "task", "new", "X-1", "--flow", "nightly-lean"); err == nil {
		t.Fatal("a task was opened against a flow this build does not have")
	}

	events, err := h.env.Store.Events("X-1")
	if err != nil {
		t.Fatalf("reading the log: %v", err)
	}
	if len(events) != 0 {
		t.Errorf("the refused task left %d event(s) in the log", len(events))
	}
}

// TestATaskOnAnotherFlowStillAppearsInTheListings is the INV-5 regression this
// change could have introduced silently.
//
// Both listings replay every task in the store. While there was one flow they
// could pass it in; with several, passing one would make every task on any other
// flow fail its fingerprint check and be skipped as unreadable — so a task
// waiting on a person would wait forever, invisible to the command whose whole
// job is that it cannot.
func TestATaskOnAnotherFlowStillAppearsInTheListings(t *testing.T) {
	h := newHarness(t)
	h.mustRun(t, "task", "new", "CHORE-1", "--kind", "chore", "--flow", "chore")

	chore, err := fsm.FlowNamed("chore")
	if err != nil {
		t.Fatalf("loading the chore flow: %v", err)
	}

	// Blocked rather than gated, because the lean flows open no gate — and a block
	// is the other ending INV-5 says must be discoverable. The retry budget is
	// spent first, since a bounded retry is what turns a failure into a block.
	if err := h.env.Store.AppendAction("CHORE-1", fsm.Advance{Flow: chore}); err != nil {
		t.Fatalf("opening the first stage: %v", err)
	}
	// Three: the budget is two retries, and the attempt past it is what blocks.
	// A fourth would be an illegal transition against a task that already stopped.
	for range 3 {
		if err := h.env.Store.AppendAction("CHORE-1", fsm.Fail{Reason: "no agent here"}); err != nil {
			t.Fatalf("failing the stage: %v", err)
		}
	}

	stuck, err := h.env.Store.Stalled(0)
	if err != nil {
		t.Fatalf("listing what is stopped: %v", err)
	}
	var found bool
	for _, s := range stuck {
		if s.TaskID == "CHORE-1" {
			found = true
		}
	}
	if !found {
		t.Errorf("a stopped task on the %q flow is invisible to the watchdog: %+v", "chore", stuck)
	}
}

// TestFlowCheckCoversEveryFlow. A build that audits one of its flows and reports
// "the contract holds" is saying something true about a third of what it runs.
func TestFlowCheckCoversEveryFlow(t *testing.T) {
	h := newHarness(t)

	out := h.mustRun(t, "flow", "check")
	for _, name := range fsm.FlowNames() {
		if !strings.Contains(out, "flow "+name+"/") {
			t.Errorf("flow %q is not covered by `flow check`:\n%s", name, out)
		}
	}

	only := h.mustRun(t, "flow", "check", "--flow", "chore")
	if !strings.Contains(only, "flow chore/") {
		t.Errorf("--flow did not report the flow it named:\n%s", only)
	}
	if strings.Contains(only, "flow "+fsm.DefaultFlowName+"/") {
		t.Errorf("--flow reported a flow it was not asked about:\n%s", only)
	}
}

// TestFlowCheckRefusesWhatItCannotCheck. Both refusals happen before anything is
// printed, so a mistyped flag or flow name does not produce a report that looks
// like an answer.
func TestFlowCheckRefusesWhatItCannotCheck(t *testing.T) {
	h := newHarness(t)

	if err := h.run(t, "flow", "check", "--nonsense"); err == nil {
		t.Error("an unknown flag was accepted")
	}
	if err := h.run(t, "flow", "check", "--flow", "nightly-lean"); err == nil {
		t.Error("a flow this build does not have was checked")
	}
}

// TestATaskWhoseFlowIsGoneIsReadableEnoughToEnd. The whole point of recording the
// name is that it can stop resolving — someone deletes a flow directory with a
// task still open on it. Every command that reads the task then fails, and the
// one that must still work is the one that ends it.
func TestATaskWhoseFlowIsGoneIsReadableEnoughToEnd(t *testing.T) {
	h := newHarness(t)

	if err := h.env.Store.AppendAction("LOST-1", fsm.TaskCreated{
		Kind: fsm.KindFeature, FlowName: "a-flow-nobody-ships",
	}); err != nil {
		t.Fatalf("creating the task: %v", err)
	}

	err := h.run(t, "task", "show", "LOST-1")
	if err == nil {
		t.Fatal("a task whose flow is gone was read as though it were fine")
	}
	if !strings.Contains(err.Error(), "abandon") {
		t.Errorf("the failure does not say how to get rid of the task: %v", err)
	}

	if err := h.run(t, "task", "abandon", "LOST-1", "its flow went away"); err != nil {
		t.Errorf("a task whose flow is gone cannot be ended: %v", err)
	}
}
