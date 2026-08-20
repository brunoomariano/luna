package fsm

import (
	"reflect"
	"testing"
)

// The tests here are named acceptance criteria: docs/invariants.md lists,
// for five invariants, the tests without which the invariant is described rather
// than implemented. AGENTS.md is explicit that a piece of the engine whose
// criteria are uncovered is not done.
//
// Most were already covered by tests written alongside the code they guard.
// These two were not, and both are the same shape: an invariant asserting
// something about *every* path, with nothing walking every path.

// TestNoRunningPathEndsWithoutAnEnding is INV-5's third criterion.
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
				// The endings INV-5 names, plus abandoned — added as terminal
				// later, and the invariant's wording predates it.
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

// TestAStageDoesNotCloseWithoutItsHumanFacingArtifact is INV-3's first
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
	// letting it in would make it satisfy some stage's Requires.
	if delivered.Context.Artifacts["qa_report"] {
		t.Error("a human-facing artifact entered the flow's context")
	}
}

// TestEveryMechanicallyProvableArtifactRunsSomething is INV-1's first
// criterion, and the one the shipped flow had already broken.
//
// The rule is that an artifact closing on `existence` alone must be a **declared**
// choice rather than the default nobody noticed. The parser enforces half of it —
// `TestAnArtifactWithNoVerifierIsRefused` — but a TOML file cannot tell a choice
// from an omission: `kind = "existence"` is what both look like.
//
// What separates them is whether a command *could* have proved it. That is not a
// property the engine can compute, so this test names the artifacts nothing can
// prove and demands a command for everything else. Adding an artifact to that
// list is the declaration, and it is a line somebody has to write in a test that
// says why.
//
// It caught `refactor`: it required `tests_green`, produced only `code`, and
// `code` closes on existence — so the stage whose whole purpose is rewriting
// working code closed without running anything. It produces `tests_green` now,
// re-earning the green rather than inheriting it.
func TestEveryMechanicallyProvableArtifactRunsSomething(t *testing.T) {
	// Artifacts no command can prove, each for a reason that is about the artifact
	// and not about the effort of writing the check.
	unprovable := map[Artifact]string{
		"worktree":    "a directory either exists or the stage that makes it failed",
		"briefing":    "prose: what it says is judgement, and running it is not a thing",
		"kind":        "a classification, which is a word rather than a state of the repository",
		"root_cause":  "prose about why something happened",
		"scenarios":   "prose a person reads to decide whether the work was understood",
		"approach":    "prose naming what will change",
		"contract":    "prose stating obligations; whether it is right is the gate's question",
		"code":        "the compiler is part of `make test`, and a non-empty diff proves nothing",
		"dod_checked": "a checklist a person reads; recording it as a passing check is a lie about what ran",

		// The report. What it says is judgement — whether the review is fair,
		// whether it found the right gaps — and a command can only ever prove that
		// a file was written. It is handed over instead, so at least the writing is
		// Luna's answer rather than the agent's.
		"review_report": "a report: whether the review is right is not a thing a command decides",

		// Deliberately here rather than given a command, and the reason is worth
		// writing down: `min_case` is a runnable reproduction, so a command *could*
		// run it — but what it would prove is that the bug still reproduces, which
		// is true before the fix and false after it. A check that must fail at one
		// end of the stage and pass at the other is two checks wearing one name.
		"min_case": "a reproduction: running it proves the bug is present, which is not what the stage owes",
	}

	// The list is checked against the flow in both directions, because only one of
	// them was for a long time. A name in `unprovable` that no stage produces is
	// invisible to the loop below — it iterates over what is produced, so a dead
	// exemption is simply never looked up. Three of them survived the review merge
	// that way, and the sibling test could not see them either: it checks its own
	// hand-written list, not this map. Verified by inversion — a dead name put
	// back here fails this, and did not before.
	produced := map[Artifact]bool{}
	for _, stage := range DefaultFlow() {
		for _, a := range append(append([]Artifact{}, stage.Produces...), stage.ProducesForHuman...) {
			produced[a] = true
		}
	}
	for artifact := range unprovable {
		if !produced[artifact] {
			t.Errorf("%s is exempted from proof and no stage produces it — the exemption "+
				"outlived the artifact, and the next artifact to take that name inherits "+
				"an argument made for something else", artifact)
		}
	}

	for _, stage := range DefaultFlow() {
		owed := append(append([]Artifact{}, stage.Produces...), stage.ProducesForHuman...)

		for _, artifact := range owed {
			if why, named := unprovable[artifact]; named {
				if why == "" {
					t.Errorf("%s is listed as unprovable with no reason", artifact)
				}
				continue
			}

			if _, runs := VerifierFor(stage, artifact).(Command); !runs {
				t.Errorf("%s produces %s and nothing runs to prove it — either give it a "+
					"command, or add it to `unprovable` above with the reason no command can "+
					"(INV-1)", stage.ID, artifact)
			}
		}
	}
}

// TestTheUnprovableListDescribesTheFlowItGuards keeps the list above honest.
//
// A name left in it after the artifact is gone is a hole nobody sees: the next
// artifact to take that name inherits an exemption argued for something else.
func TestTheUnprovableListDescribesTheFlowItGuards(t *testing.T) {
	produced := map[Artifact]bool{}
	for _, stage := range DefaultFlow() {
		for _, a := range append(append([]Artifact{}, stage.Produces...), stage.ProducesForHuman...) {
			produced[a] = true
		}
	}

	// Rebuilt rather than shared with the test above, so the two cannot drift into
	// agreeing with each other about a list neither checks.
	for _, artifact := range []Artifact{
		"worktree", "briefing", "kind", "root_cause", "scenarios",
		"approach", "contract", "code", "dod_checked", "min_case",
		"review_report",
	} {
		if !produced[artifact] {
			t.Errorf("%s is exempted from proof and no stage produces it — the exemption "+
				"outlived the artifact", artifact)
		}
	}
}

// TestAStageThatRewritesWhatItWasGivenReprovesIt is the other half of
// INV-1's first criterion, and the half that caught the real defect.
//
// The test above asks whether each produced artifact has a proof. It cannot see
// the failure `refactor` had, because that one was about an artifact the stage
// did *not* produce: it required `tests_green`, rewrote the `code` that green
// attested to, and produced only `code` — so the green carried over from `build`,
// describing code that no longer existed. That is the invalidation an aligned
// finding performs, arrived at from the producing side.
//
// The rule: a stage that produces an artifact it also requires has rewritten it,
// and everything that was proven *about* the old one has to be proven again.
func TestAStageThatRewritesWhatItWasGivenReprovesIt(t *testing.T) {
	for _, stage := range DefaultFlow() {
		required := map[Artifact]bool{}
		for _, a := range stage.Requires {
			required[a] = true
		}

		rewrites := false
		for _, a := range stage.Produces {
			if required[a] {
				rewrites = true
			}
		}
		if !rewrites {
			continue
		}

		// Everything else it was given was proven against what it just changed, so
		// it owes those proofs again.
		produces := map[Artifact]bool{}
		for _, a := range stage.Produces {
			produces[a] = true
		}
		for _, given := range stage.Requires {
			if !produces[given] {
				t.Errorf("%s rewrites what it was given and does not re-deliver %s — "+
					"that proof describes the version it replaced (INV-1)",
					stage.ID, given)
			}
		}
	}
}
