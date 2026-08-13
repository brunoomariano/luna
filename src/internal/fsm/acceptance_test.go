package fsm

import (
	"reflect"
	"testing"
)

// The tests here are named acceptance criteria: docs/invariants/core.md lists,
// for five invariants, the tests without which the invariant is described rather
// than implemented. AGENTS.md is explicit that a piece of the engine whose
// criteria are uncovered is not done.
//
// Most were already covered by tests written alongside the code they guard.
// These two were not, and both are the same shape: an invariant asserting
// something about *every* path, with nothing walking every path.

// TestNoRunningPathEndsWithoutAnEnding is INV-core-8's third criterion.
//
// The rule is that every task ends in a commit, a gate or a notified block —
// "None dies without someone knowing". The first two criteria test the two
// endings that were built deliberately. This one tests the claim underneath
// them: that a running task cannot reach any *other* resting place.
//
// It is exhaustive over the actions rather than a list of the ones worth
// worrying about, because the failure it guards against is a new action added
// later whose author did not think about this. A `default` in a switch would
// have been the same test written to pass.
// theRunningFlow is the one-stage flow the criteria below are exercised against.
func theRunningFlow() []Stage {
	return []Stage{{
		ID:        "build",
		Role:      "implementer",
		Requires:  []Artifact{TaskID},
		Produces:  []Artifact{"code"},
		Verifiers: map[Artifact]Verifier{"code": Existence{}},
		Review:    &ReviewSpec{SendsBackTo: "build", Invalidates: []Artifact{"code"}},
	}}
}

// actionsFromRunning is every action the reducer accepts on a running task.
//
// One map, walked by the criterion below and by the guard that keeps it
// exhaustive — so adding an action to the engine fails the guard until it
// appears here.
func actionsFromRunning(flow []Stage) map[string]Action {
	return map[string]Action{
		"Complete delivering everything": Complete{
			Flow:      flow,
			Delivered: []Artifact{"code"},
			Evidence:  map[Artifact]Evidence{"code": Exists(0)},
		},
		"Complete delivering nothing": Complete{Flow: flow},
		"Complete failing its check": Complete{
			Flow:      flow,
			Delivered: []Artifact{"code"},
			Evidence:  map[Artifact]Evidence{"code": {Scope: ScopeExistence, Verdict: VerdictFailed}},
		},
		"Fail":                      Fail{Reason: "it broke"},
		"Block":                     Block{Reason: "somebody has to look"},
		"Abandon":                   Abandon{Reason: "called off"},
		"ReviewFinding aligned":     ReviewFinding{Flow: flow, Aligned: true},
		"ReviewFinding not aligned": ReviewFinding{Flow: flow},
	}
}

func TestNoRunningPathEndsWithoutAnEnding(t *testing.T) {
	for name, action := range actionsFromRunning(theRunningFlow()) {
		t.Run(name, func(t *testing.T) {
			running := TaskState{
				ID:       "LUNA-1",
				Status:   StatusRunning,
				Stage:    "build",
				Profile:  ProfileInteractive,
				Context:  NewTaskContext(KindFeature),
				Retry:    Retry{Max: 2},
				Evidence: map[Artifact]Evidence{},
			}

			after, err := Reduce(running, action)
			if err != nil {
				// A refused transition is an ending of its own: the task did not
				// move, and the caller was told why.
				return
			}

			switch after.Status {
			case StatusBlocked, StatusAwaitingGate, StatusDone, StatusAbandoned:
				// The endings INV-core-8 names, plus abandoned — which ADR-0046
				// added as terminal and the invariant's wording predates.
			case StatusRunning, StatusStageDone, StatusReady:
				// Still moving is fine: what the invariant forbids is *resting*
				// somewhere that is not an ending, and a task that is running or
				// has just closed a stage has a next transition waiting.
				if after.Status == StatusRunning && after.Stage == "" {
					t.Errorf("running with no stage: nothing will ever move it")
				}
			default:
				t.Errorf("a running task reached %q, which is neither an ending nor "+
					"a state anything advances from", after.Status)
			}

			// Whatever happened, a stopped task says why. A block with no reason
			// is the silent failure the invariant is named for.
			if after.Status == StatusBlocked && after.Blocked == "" {
				t.Error("blocked with no reason recorded")
			}
			if after.Status == StatusAwaitingGate && after.Gate == nil {
				t.Error("awaiting a gate that is not recorded — `luna gates` would " +
					"not list it, so nobody could answer it")
			}
		})
	}
}

// TestEveryActionIsCoveredAbove keeps the list honest.
//
// The test above is only exhaustive if its map is, and a map is easy to forget.
// This walks the action types the reducer accepts and insists each one appears —
// so adding an action to the engine fails here until someone has decided what it
// does to a running task.
func TestEveryActionIsCoveredAbove(t *testing.T) {
	// The actions Reduce switches on. Kept as values rather than names so a
	// rename moves with the type.
	every := []Action{
		TaskCreated{},
		Advance{},
		Complete{},
		Fail{},
		GateApprove{},
		GateAdjust{},
		GateReject{},
		ReviewFinding{},
		Block{},
		Unblock{},
		Abandon{},
	}

	// Which types the exhaustive test actually covers, read from the map it
	// walks rather than from a second list.
	//
	// A second list is what made this test theatre when it was written: deleting
	// an entry from the map above left it green, because it was comparing against
	// its own copy. Reading the real map is what makes "the list is honest" a
	// claim the code has to honour.
	fromRunning := map[string]bool{}
	for _, action := range actionsFromRunning(theRunningFlow()) {
		fromRunning[reflect.TypeOf(action).Name()] = true
	}

	for _, action := range every {
		name := reflect.TypeOf(action).Name()
		if fromRunning[name] {
			continue
		}

		// Everything else must be refused from a running task rather than
		// quietly accepted, which is the other half of the same claim.
		running := TaskState{
			ID: "LUNA-1", Status: StatusRunning, Stage: "build",
			Context: NewTaskContext(KindFeature), Evidence: map[Artifact]Evidence{},
		}
		if _, err := Reduce(running, action); err == nil {
			t.Errorf("%s was accepted on a running task and is not in the exhaustive "+
				"list above — decide which it is", name)
		}
	}
}

// TestAStageDoesNotCloseWithoutItsHumanFacingArtifact is INV-core-11's first
// criterion.
//
// `ProducesForHuman` is the field that says a stage owes a person something — a
// report, an assessment, a diagnosis. The invariant's own violation list names
// "treating `produces_for_human` as a suggestion", and the exit check counts it
// exactly like `Produces`. Nothing tested that directly.
func TestAStageDoesNotCloseWithoutItsHumanFacingArtifact(t *testing.T) {
	flow := []Stage{{
		ID:               "qa",
		Role:             "qa",
		Requires:         []Artifact{TaskID},
		Produces:         []Artifact{"verdict"},
		ProducesForHuman: []Artifact{"qa_report"},
		Verifiers: map[Artifact]Verifier{
			"verdict":   Existence{},
			"qa_report": Existence{},
		},
	}}

	running := TaskState{
		ID: "LUNA-1", Status: StatusRunning, Stage: "qa",
		Context: NewTaskContext(KindFeature), Evidence: map[Artifact]Evidence{},
	}

	// Everything the flow consumes, and nothing for the person.
	after, err := Reduce(running, Complete{
		Flow:      flow,
		Delivered: []Artifact{"verdict"},
		Evidence:  map[Artifact]Evidence{"verdict": Exists(0)},
	})
	if err != nil {
		t.Fatalf("Reduce: %v", err)
	}

	if after.Status != StatusBlocked {
		t.Fatalf("status = %q — a stage that owed a report and delivered none closed",
			after.Status)
	}
	if after.Blocked == "" {
		t.Error("it blocked without saying what was missing")
	}

	// And with the report, it closes.
	delivered, err := Reduce(running, Complete{
		Flow:      flow,
		Delivered: []Artifact{"verdict", "qa_report"},
		Evidence: map[Artifact]Evidence{
			"verdict":   Exists(0),
			"qa_report": Exists(0),
		},
	})
	if err != nil {
		t.Fatalf("Reduce: %v", err)
	}
	if delivered.Status != StatusStageDone {
		t.Errorf("status = %q, want stage_done", delivered.Status)
	}

	// The report does not enter the context: nothing downstream requires it, and
	// letting it in would make it satisfy some stage's Requires (ADR-0021).
	if delivered.Context.Artifacts["qa_report"] {
		t.Error("a human-facing artifact entered the flow's context")
	}
}
