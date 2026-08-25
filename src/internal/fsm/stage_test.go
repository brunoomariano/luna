package fsm

import "testing"

// auditOnlyStage returns a stage that produces only an audit report — the shape of
// qa in the default flow. A named fake rather than an inline literal: the same
// contract is exercised by several scenarios, and naming it says why it looks so.
func auditOnlyStage() Stage {
	return Stage{
		ID:               "qa",
		Role:             "qa",
		Requires:         []Artifact{"ci_green", "briefing"},
		Produces:         nil,
		ProducesForHuman: []Artifact{"qa_report"},
	}
}

// TestStageSeparatesTheThreeFields covers scenario A1.
//
// A stage declares three distinct things: what it requires to start, what it
// delivers to the flow and what it delivers to a person. The three are separate
// fields — merging them would erase the difference between "product someone
// consumes" and "report someone reads", which the three fields exist to preserve.
func TestStageSeparatesTheThreeFields(t *testing.T) {
	build := Stage{
		ID:       "build",
		Requires: []Artifact{"scenarios", "approach", "worktree"},
		Produces: []Artifact{"code", "tests_green"},
	}

	if len(build.Requires) != 3 {
		t.Errorf("Requires: want 3 artifacts, got %d (%v)", len(build.Requires), build.Requires)
	}
	if len(build.Produces) != 2 {
		t.Errorf("Produces: want 2 artifacts, got %d (%v)", len(build.Produces), build.Produces)
	}
	if len(build.ProducesForHuman) != 0 {
		t.Errorf("ProducesForHuman: want empty, got %v", build.ProducesForHuman)
	}
}

// TestAuditArtifactIsNotAFlowProduct covers scenario A2.
//
// A report declared in ProducesForHuman does not show up among the products the
// flow consumes. That is what stops an audit artifact from satisfying, by
// mistake, another stage's Requires.
func TestAuditArtifactIsNotAFlowProduct(t *testing.T) {
	qa := auditOnlyStage()

	if containsArtifact(qa.Produces, "qa_report") {
		t.Error("qa_report is an audit artifact and should not count as a flow product")
	}
	if len(qa.ProducesForHuman) != 1 || qa.ProducesForHuman[0] != "qa_report" {
		t.Errorf("ProducesForHuman: want [qa_report], got %v", qa.ProducesForHuman)
	}
}

// TestStageMayProduceNothingForTheFlow covers scenario A3.
//
// The qa stage delivers only an audit report. That is a legitimate declaration,
// not a malformed stage: its value is the assessment a person reads, not an
// artifact the flow chains onward.
func TestStageMayProduceNothingForTheFlow(t *testing.T) {
	qa := auditOnlyStage()

	if len(qa.Produces) != 0 {
		t.Errorf("Produces: want empty for an audit-only stage, got %v", qa.Produces)
	}
	if len(qa.ProducesForHuman) == 0 {
		t.Error("a stage that produces nothing for the flow must produce something for someone")
	}
}

// TestConditionalStageAppliesByKind covers the base of scenarios C1 and C2.
//
// A stage with a condition enters the flow for some task kinds and not others.
// spec enters on feature and bug; it is skipped on chore and docs.
func TestConditionalStageAppliesByKind(t *testing.T) {
	spec := Stage{
		ID:       "spec",
		Requires: []Artifact{"approach"},
		Produces: []Artifact{"contract"},
		When:     IsFeatureOrBug,
	}

	cases := []struct {
		kind TaskKind
		want bool
	}{
		{KindFeature, true},
		{KindBug, true},
		{KindChore, false},
		{KindDocs, false},
	}

	for _, c := range cases {
		if got := spec.AppliesTo(NewTaskContext(c.kind)); got != c.want {
			t.Errorf("spec.AppliesTo(%q): want %v, got %v", c.kind, c.want, got)
		}
	}
}

// TestUnconditionalStageAlwaysApplies covers the edge of AppliesTo.
//
// A stage with no declared condition enters for any task kind. That is the
// majority: only the heavy review stages and spec are conditional.
func TestUnconditionalStageAlwaysApplies(t *testing.T) {
	setup := Stage{ID: "setup", Requires: []Artifact{"repos"}, Produces: []Artifact{"worktree"}}

	for _, k := range []TaskKind{KindFeature, KindBug, KindChore, KindDocs} {
		if !setup.AppliesTo(NewTaskContext(k)) {
			t.Errorf("setup.AppliesTo(%q): an unconditional stage must always enter", k)
		}
	}
}

// TestStageConditionedOnDiscoveredFact covers the condition that is not about the
// nature of the task.
//
// The architecture stage only enters when the change touched the structure — a
// fact nobody knows at intake, and which only appears after looking at what build
// produced. It is the case that made When take the whole context instead of just
// the kind.
func TestStageConditionedOnDiscoveredFact(t *testing.T) {
	arch := Stage{
		ID:               "architecture",
		Requires:         []Artifact{"code"},
		ProducesForHuman: []Artifact{"arch_report"},
		When:             TouchedStructure,
	}

	ctx := NewTaskContext(KindFeature)
	if arch.AppliesTo(ctx) {
		t.Error("without the discovered fact, architecture should not enter")
	}

	ctx.Facts[TouchesStructure] = true
	if !arch.AppliesTo(ctx) {
		t.Error("with the discovered fact, architecture should enter")
	}
}

// TestTaskContextHandlesNilMaps covers the edge of a hand-built context.
//
// A condition should not have to know whether someone initialized the maps before
// consulting them — reading a nil map in Go yields the zero value, and that is
// what is expected here.
func TestTaskContextHandlesNilMaps(t *testing.T) {
	var ctx TaskContext

	if ctx.HasFact(TouchesStructure) {
		t.Error("an empty context has no discovered fact")
	}
	if ctx.HasArtifact(TaskID) {
		t.Error("an empty context has no artifact")
	}
}
