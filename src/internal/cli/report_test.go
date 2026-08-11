package cli

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/brunoomariano/luna/src/internal/fsm"
)

// TestTaskShowAnswersStructure covers the contract the conversational layer
// reads.
//
// Without it the layer parses output written for people, and every reworded
// message becomes a silent breakage (ADR-0043).
func TestTaskShowAnswersStructure(t *testing.T) {
	h := newHarness(t)
	h.mustRun(t, "task", "new", "LUNA-1", "--kind", "bug", "--profile", "nightly")

	var report TaskReport
	if err := json.Unmarshal([]byte(h.mustRun(t, "task", "show", "LUNA-1", "--json")), &report); err != nil {
		t.Fatalf("the answer must be JSON: %v", err)
	}

	if report.ID != "LUNA-1" || report.Kind != "bug" || report.Profile != "nightly" {
		t.Errorf("want the task as created, got %+v", report)
	}
	if !report.ProfileDefined {
		t.Error("nightly is a shipped profile and is defined")
	}
	if report.Events == 0 {
		t.Error("a created task has events")
	}
}

// TestTheStructuredViewCarriesTheScope is the field that matters most.
//
// A reader that cannot tell a green suite from a file that merely exists would
// report the two the same way, which is the laundering ADR-0028 exists to
// prevent.
func TestTheStructuredViewCarriesTheScope(t *testing.T) {
	h := newHarness(t)
	h.mustRun(t, "task", "new", "LUNA-1", "--profile", "nightly")
	h.mustRun(t, "run", "LUNA-1", "--dry-run")

	var report TaskReport
	if err := json.Unmarshal([]byte(h.mustRun(t, "task", "show", "LUNA-1", "--json")), &report); err != nil {
		t.Fatalf("decoding: %v", err)
	}

	if len(report.Produced) == 0 {
		t.Fatal("a finished task produced something")
	}
	for _, artifact := range report.Produced {
		if artifact.Name == "task_id" {
			continue // seeded, not produced by a stage
		}
		// A dry run proves nothing, and the report has to say so rather than
		// looking like a verified delivery.
		if artifact.Scope != string(fsm.ScopeExistence) {
			t.Errorf("%s: a rehearsal proves existence, got %q", artifact.Name, artifact.Scope)
		}
	}
}

// TestAWaitingTaskCarriesItsGate covers what the layer needs to answer "what is
// it waiting for".
func TestAWaitingTaskCarriesItsGate(t *testing.T) {
	h := newHarness(t)
	h.mustRun(t, "task", "new", "LUNA-1") // interactive: stops at the first gate
	h.mustRun(t, "run", "LUNA-1", "--dry-run")

	var report TaskReport
	if err := json.Unmarshal([]byte(h.mustRun(t, "task", "show", "LUNA-1", "--json")), &report); err != nil {
		t.Fatalf("decoding: %v", err)
	}

	if report.Status != string(fsm.StatusAwaitingGate) {
		t.Fatalf("want the task waiting, got %q", report.Status)
	}
	if report.Gate == nil {
		t.Fatal("a waiting task must say what it waits for")
	}
	if report.Gate.Reason == "" {
		t.Error("the gate must carry why it stopped")
	}
}

// TestGatesAnswersAnArrayEvenWhenEmpty covers the shape a reader loops over.
//
// A reader should not have to distinguish "no tasks" from "the field was absent".
func TestGatesAnswersAnArrayEvenWhenEmpty(t *testing.T) {
	h := newHarness(t)

	out := h.mustRun(t, "gates", "--json")

	var report GatesReport
	if err := json.Unmarshal([]byte(out), &report); err != nil {
		t.Fatalf("decoding: %v", err)
	}
	if report.Waiting == nil {
		t.Error("want an empty array, not null")
	}
	if !strings.Contains(out, `"waiting": []`) {
		t.Errorf("want an empty array on the wire, got %s", out)
	}
}

// TestTheStructuredGatesListNamesEachTask covers the populated case.
func TestTheStructuredGatesListNamesEachTask(t *testing.T) {
	h := newHarness(t)
	h.mustRun(t, "task", "new", "LUNA-1")
	h.mustRun(t, "run", "LUNA-1", "--dry-run")

	var report GatesReport
	if err := json.Unmarshal([]byte(h.mustRun(t, "gates", "--json")), &report); err != nil {
		t.Fatalf("decoding: %v", err)
	}

	if len(report.Waiting) != 1 {
		t.Fatalf("want the one waiting task, got %+v", report.Waiting)
	}
	if report.Waiting[0].TaskID != "LUNA-1" {
		t.Errorf("want the task named, got %+v", report.Waiting[0])
	}
	if report.Waiting[0].Reason == "" {
		t.Error("a waiting task must say why")
	}
}

// TestTheHumanFormIsUnchangedByTheFlag covers the promise that adding --json
// costs a person nothing.
func TestTheHumanFormIsUnchangedByTheFlag(t *testing.T) {
	h := newHarness(t)
	h.mustRun(t, "task", "new", "LUNA-1")

	plain := h.mustRun(t, "task", "show", "LUNA-1")

	if strings.HasPrefix(strings.TrimSpace(plain), "{") {
		t.Error("without the flag the output stays the one a person reads")
	}
	if !strings.Contains(plain, "LUNA-1") {
		t.Errorf("want the readable form, got %q", plain)
	}
}

// TestAnUnknownFlagOnAReadingCommandIsRefused covers the strictness.
//
// Reading commands take --json and nothing else, so a typo is a mistake worth
// naming rather than ignoring.
func TestAnUnknownFlagOnAReadingCommandIsRefused(t *testing.T) {
	h := newHarness(t)
	h.mustRun(t, "task", "new", "LUNA-1")

	for _, command := range [][]string{
		{"task", "show", "LUNA-1", "--jsonn"},
		{"gates", "--jsonn"},
	} {
		if err := h.run(t, command...); err == nil {
			t.Errorf("%v: a mistyped flag must be reported", command)
		}
	}
}

// TestAProfileNoLongerDefinedIsFlaggedInTheStructure covers the machine-readable
// half of the warning the text form already carries (ADR-0026).
func TestAProfileNoLongerDefinedIsFlaggedInTheStructure(t *testing.T) {
	h := newHarness(t)
	if err := h.env.Store.AppendAction("LUNA-1", fsm.TaskCreated{
		Kind:    fsm.KindFeature,
		Profile: fsm.Profile("deleted-last-week"),
	}); err != nil {
		t.Fatalf("seeding: %v", err)
	}

	var report TaskReport
	if err := json.Unmarshal([]byte(h.mustRun(t, "task", "show", "LUNA-1", "--json")), &report); err != nil {
		t.Fatalf("decoding: %v", err)
	}

	if report.ProfileDefined {
		t.Error("a profile the config no longer has must be flagged")
	}
	if report.Profile != "deleted-last-week" {
		t.Errorf("want the profile as recorded, got %q", report.Profile)
	}
}
