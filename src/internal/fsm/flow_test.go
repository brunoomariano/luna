package fsm

import (
	"errors"
	"testing"
)

// TestDefaultFlowHasNoContractGap is the test that matters most in this package.
//
// If the flow Luna ships with has a requirement without a producer, every task
// walking it will stall — and stall on the wrong stage, with the symptom
// displaced from the cause. This test catches that in CI, before any run.
func TestDefaultFlowHasNoContractGap(t *testing.T) {
	gaps := AuditContract(DefaultFlow())

	if len(gaps) != 0 {
		for _, g := range gaps {
			t.Errorf("stage %q requires %v, which no earlier stage produces", g.Stage, g.Missing)
		}
	}
}

// TestDefaultFlowMatchesDocumentedStages guards the table in docs/architecture.md.
//
// This is not counting for counting's sake: a stage that disappears from
// DefaultFlow without disappearing from the documentation leaves the two out of
// sync, and the documentation is the contract.
//
// Whether a stage is mechanical is compared too, and that is not decoration.
// Comparing IDs and order
// alone let the table claim for a long time that diagnose, spec and verify were
// mechanical while the flow gave all three an agent — and Stage.Mechanical decides
// whether an agent starts at all, so the table was wrong about who runs the work.
func TestDefaultFlowMatchesDocumentedStages(t *testing.T) {
	flow := DefaultFlow()

	// The roles are the pack: five of them, and a stage names the one that owns it.
	// `luna lead` collapses them onto `lead` for a single-agent run, which is why
	// this table is about what the flow *declares* and not about what every mode
	// resolves.
	//
	// One mechanical stage is left. `setup` was the other and stopped being one
	// when it took on discovering what the project runs under: creating a worktree
	// is not judgement, but concluding which command bootstraps a repository is.
	// `pipeline` merged into `forge`. `commit` went when integration left Luna's
	// scope.
	want := []struct {
		ID         StageID
		Mechanical bool
	}{
		{"setup", false},
		{"intake", false},
		{"diagnose", false},
		{"plan", false},
		{"forge", false},
		{"shipping", false},
		{"review", false},
	}

	if len(flow) != len(want) {
		t.Errorf("want %d stages per docs/architecture.md, got %d", len(want), len(flow))
	}

	for i, documented := range want {
		if i >= len(flow) {
			t.Fatalf("flow ended before %q", documented.ID)
		}
		if flow[i].ID != documented.ID {
			t.Errorf("position %d: want %q, got %q", i, documented.ID, flow[i].ID)
		}
		if flow[i].Mechanical() != documented.Mechanical {
			t.Errorf("%q: docs say mechanical=%v, flow declares %v",
				documented.ID, documented.Mechanical, flow[i].Mechanical())
		}
	}
}

// TestAuditReportsAreNotFlowProducts covers INV-3 on the real flow.
//
// The assessment the review stage writes exists for a person to read. If it
// became a Produces, it would start satisfying another stage's Requires, and the
// distinction between a flow product and a human-read report would lose its
// meaning in the flow that matters most.
func TestAuditReportsAreNotFlowProducts(t *testing.T) {
	reports := map[StageID]Artifact{
		"review":   "review_report",
		"diagnose": "min_case",
	}

	for _, stage := range DefaultFlow() {
		report, isReport := reports[stage.ID]
		if !isReport {
			continue
		}
		if containsArtifact(stage.Produces, report) {
			t.Errorf("%q: %q is an audit artifact and should not be in Produces", stage.ID, report)
		}
		if !containsArtifact(stage.ProducesForHuman, report) {
			t.Errorf("%q: want %q in ProducesForHuman, got %v", stage.ID, report, stage.ProducesForHuman)
		}
	}
}

// TestDefaultFlowConditionalStages guards the "Condition" column of the table.
//
// Running a mutation test on a one-line chore is the ceremony conditional stages
// exist to cut. If a condition gets lost, the flow starts running an expensive
// stage where it does not pay off — and nobody notices, because the result stays
// correct.
func TestDefaultFlowConditionalStages(t *testing.T) {
	// The investigation is the one conditional stage, and it turns on a fact the
	// intake recorded rather than on the kind the task was opened with. Which is
	// the point: whether something is broken or merely missing is what reading the
	// code settles, and the kind is typed before anyone has read it.
	cases := []struct {
		stage   StageID
		triaged bool
		applies bool
	}{
		{"diagnose", true, true},
		{"diagnose", false, false},
		{"forge", true, true},
		{"forge", false, true},
		{"plan", false, true}, // planning is unconditional; it carries the contract
	}

	byID := map[StageID]Stage{}
	for _, s := range DefaultFlow() {
		byID[s.ID] = s
	}

	for _, c := range cases {
		stage, ok := byID[c.stage]
		if !ok {
			t.Fatalf("stage %q does not exist in the default flow", c.stage)
		}
		ctx := NewTaskContext(KindFeature)
		if c.triaged {
			ctx.Facts[TriagedBug] = true
		}
		if got := stage.AppliesTo(ctx); got != c.applies {
			t.Errorf("%q with triaged=%v: want applies=%v, got %v", c.stage, c.triaged, c.applies, got)
		}
	}
}

// TestTheEngineDoesNotDependOnTheShippedFlow is "a project brings its own flow"
// as a test.
//
// The promise is that a project can bring its own flow. Every other test in this
// package drives DefaultFlow(), so an assumption about the shipped fourteen
// stages could sit in the engine for a long time without anything noticing —
// which is how a flow becomes hardcoded by accident rather than by decision.
//
// The shape is borrowed from Erlang/OTP's gen_statem suite, which runs the same
// test bodies under both callback modes: if the observable behaviour has to be
// identical, one test proves both.
func TestTheEngineDoesNotDependOnTheShippedFlow(t *testing.T) {
	// A flow with nothing in common with the shipped one but its shape: different
	// stage names, different artifacts, a conditional stage, and a mechanical one.
	custom := []Stage{
		{ID: "gather", Requires: []Artifact{TaskID}, Produces: []Artifact{"notes"}},
		{ID: "draft", Requires: []Artifact{"notes"}, Produces: []Artifact{"text"}},
		{
			ID: "translate", Requires: []Artifact{"text"}, Produces: []Artifact{"translated"},
			When: Condition{Name: "is-feature", Applies: func(c TaskContext) bool { return c.Kind == KindFeature }},
		},
		{ID: "publish", Requires: []Artifact{"text"}, Produces: []Artifact{"url"}},
	}

	if gaps := AuditContract(custom); len(gaps) > 0 {
		t.Fatalf("the custom flow must be well-formed to prove anything: %v", gaps)
	}

	for _, kind := range []TaskKind{KindFeature, KindChore} {
		state := NewTaskState("LUNA-1", kind)
		state.Profile = ProfileTurbo // no gates: this is about the flow, not the pauses

		var visited []StageID
		for range custom {
			next, err := Reduce(state, Advance{Flow: custom})
			if err != nil {
				t.Fatalf("%s: advancing: %v", kind, err)
			}
			if next.IsTerminal() {
				state = next
				break
			}
			visited = append(visited, next.Stage)

			stage := stageIn(custom, next.Stage)
			owed := append(append([]Artifact{}, stage.Produces...), stage.ProducesForHuman...)
			state, err = Reduce(next, Complete{Delivered: owed, Evidence: passing(stage, owed), Flow: custom})
			if err != nil {
				t.Fatalf("%s: completing %q: %v", kind, next.Stage, err)
			}
		}

		// Fall off the end, unless a skipped stage already got us there: a flow
		// with a condition ends after a different number of advances per kind,
		// which is itself part of what this proves.
		if !state.IsTerminal() {
			state = mustReduce(t, state, Advance{Flow: custom})
		}
		if state.Status != StatusDone {
			t.Errorf("%s: a custom flow finishes like any other, got %q (%s)", kind, state.Status, state.Blocked)
		}

		// The conditional stage is the part that proves the engine read *this*
		// flow's rules rather than falling back to what it knows.
		wantTranslate := kind == KindFeature
		var sawTranslate bool
		for _, id := range visited {
			if id == "translate" {
				sawTranslate = true
			}
		}
		if sawTranslate != wantTranslate {
			t.Errorf("%s: translate visited=%v, want %v (visited %v)", kind, sawTranslate, wantTranslate, visited)
		}
	}
}

// TestACustomFlowCanOpenItsOwnGates is what a stage declaring its own gate bought.
//
// Gates used to be a switch over the shipped stage ids, so a project bringing its
// own flow got no gates at all — and the same for where a review sends work back
// and what that invalidates. A flow that cannot pause for a person is not a flow
// anyone would choose; it was just what the code did.
func TestACustomFlowCanOpenItsOwnGates(t *testing.T) {
	custom := []Stage{
		{
			ID: "draft", Requires: []Artifact{TaskID}, Produces: []Artifact{"text"},
			Gate: &GateSpec{Kind: GateReviewArtifact, Artifact: "text", Reason: "read the draft"},
		},
		{
			ID: "critique", Requires: []Artifact{"text"}, ProducesForHuman: []Artifact{"notes"},
			Review: &ReviewSpec{SendsBackTo: "draft", Invalidates: []Artifact{"text"}},
		},
	}

	state := NewTaskState("LUNA-1", KindDocs)
	state, err := Reduce(state, Advance{Flow: custom})
	if err != nil {
		t.Fatalf("entering the first stage: %v", err)
	}

	// The gate opens when `draft` closes, because that is the first moment `text`
	// exists to be reviewed.
	state, err = Reduce(state, Complete{
		Delivered: []Artifact{"text"},
		Evidence: map[Artifact]Evidence{
			"text": {Scope: ScopeExistence, Verdict: VerdictPassed, Detail: "what the stage wrote"},
		},
		Flow: custom,
	})
	if err != nil {
		t.Fatalf("closing the first stage: %v", err)
	}

	if state.Status != StatusAwaitingGate {
		t.Fatalf("a custom flow's gate must stop the task, got %q", state.Status)
	}
	if state.Gate == nil || state.Gate.Artifact != "text" {
		t.Errorf("the gate must carry what the stage declared, got %+v", state.Gate)
	}
}

// TestACustomReviewSendsWorkWhereItSays covers the other half.
func TestACustomReviewSendsWorkWhereItSays(t *testing.T) {
	custom := []Stage{
		{
			ID: "draft", Requires: []Artifact{TaskID}, Produces: []Artifact{"text"},
			// Proven by a command rather than by existence: existence survives an
			// edit on purpose — the file still exists — so only a real check has
			// anything to go stale.
			Verifiers: map[Artifact]Verifier{"text": Command{Run: "make lint", Scope: ScopeTargeted}},
		},
		{
			ID: "critique", Requires: []Artifact{"text"}, ProducesForHuman: []Artifact{"notes"},
			Review: &ReviewSpec{SendsBackTo: "draft", Invalidates: []Artifact{"text"}},
		},
	}

	// Drive to the review stage with its input in hand.
	state := NewTaskState("LUNA-1", KindDocs)
	state.Profile = ProfileTurbo
	state = mustReduce(t, state, Advance{Flow: custom})
	state = mustReduce(t, state, Complete{
		Delivered: []Artifact{"text"},
		Evidence:  passing(custom[0], []Artifact{"text"}),
		Flow:      custom,
	})
	state = mustReduce(t, state, Advance{Flow: custom})

	state = mustReduce(t, state, ReviewFinding{Aligned: true, Summary: "needs work", Flow: custom})

	if state.Stage != "draft" {
		t.Errorf("the finding must send the work where the stage says, got %q", state.Stage)
	}
	if state.Context.HasArtifact("text") {
		t.Error("what the review invalidated must leave the context")
	}
	if state.Evidence["text"].Verdict != VerdictStale {
		t.Errorf("the evidence goes stale rather than disappearing, got %+v", state.Evidence["text"])
	}
}

// TestAStageThatDoesNotReviewCannotSendWorkBack is whoever-writes-does-not-review
// inside the engine.
//
// It used to be a hardcoded set of four stage ids, so renaming `code-review` in a
// custom flow lost the protection silently. Now the refusal comes from the stage's
// own declaration.
func TestAStageThatDoesNotReviewCannotSendWorkBack(t *testing.T) {
	state := atStage(t, KindFeature, "forge")

	_, err := Reduce(state, ReviewFinding{Aligned: true, Summary: "my own work looks wrong"})
	if !errors.Is(err, ErrIllegalTransition) {
		t.Errorf("a stage that does not review must not send work back, got %v", err)
	}
}
