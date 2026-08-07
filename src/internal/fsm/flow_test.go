package fsm

import "testing"

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

// TestDefaultFlowMatchesDocumentedStages guards the table in docs/architecture/stages.md.
//
// This is not counting for counting's sake: a stage that disappears from
// DefaultFlow without disappearing from the documentation leaves the two out of
// sync, and the documentation is the contract.
func TestDefaultFlowMatchesDocumentedStages(t *testing.T) {
	flow := DefaultFlow()

	if len(flow) != 14 {
		t.Errorf("want 14 stages per docs/architecture/stages.md, got %d", len(flow))
	}

	want := []StageID{
		"discovery", "setup", "intake", "diagnose", "scenarios", "spec", "build",
		"refactor", "verify", "qa", "code-review", "harden", "architecture", "commit",
	}
	for i, id := range want {
		if i >= len(flow) {
			t.Fatalf("flow ended before %q", id)
		}
		if flow[i].ID != id {
			t.Errorf("position %d: want %q, got %q", i, id, flow[i].ID)
		}
	}
}

// TestAuditReportsAreNotFlowProducts covers INV-core-11 on the real flow.
//
// The assessments from qa, code-review, harden and architecture exist for a
// person to read. If one of them became a Produces, it would start satisfying
// another stage's Requires and the distinction in ADR-0021 would lose its meaning
// in the flow that matters most.
func TestAuditReportsAreNotFlowProducts(t *testing.T) {
	reports := map[StageID]Artifact{
		"qa":           "qa_report",
		"code-review":  "review_report",
		"harden":       "mutation_report",
		"architecture": "arch_report",
		"verify":       "dod_checked",
		"diagnose":     "min_case",
	}

	for _, stage := range DefaultFlow() {
		report, isReport := reports[stage.ID]
		if !isReport {
			continue
		}
		if stage.ProducesArtifact(report) {
			t.Errorf("%q: %q is an audit artifact and should not be in Produces", stage.ID, report)
		}
		if !stage.ProducesForHumanArtifact(report) {
			t.Errorf("%q: want %q in ProducesForHuman, got %v", stage.ID, report, stage.ProducesForHuman)
		}
	}
}

// TestDefaultFlowConditionalStages guards the "Condition" column of the table.
//
// Running a mutation test on a one-line chore is the ceremony ADR-0014 exists to
// cut. If a condition gets lost, the flow starts running an expensive stage where
// it does not pay off — and nobody notices, because the result stays correct.
func TestDefaultFlowConditionalStages(t *testing.T) {
	cases := []struct {
		stage   StageID
		kind    TaskKind
		applies bool
	}{
		{"diagnose", KindBug, true},
		{"diagnose", KindFeature, false},
		{"spec", KindFeature, true},
		{"spec", KindChore, false},
		{"qa", KindChore, false},
		{"qa", KindFeature, true},
		{"code-review", KindDocs, false},
		{"code-review", KindFeature, true},
		{"harden", KindBug, true},
		{"harden", KindDocs, false},
		{"build", KindDocs, true},
		{"architecture", KindFeature, false}, // without the discovered fact, it stays out
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
		if got := stage.AppliesTo(NewTaskContext(c.kind)); got != c.applies {
			t.Errorf("%q with kind=%q: want applies=%v, got %v", c.stage, c.kind, c.applies, got)
		}
	}
}
