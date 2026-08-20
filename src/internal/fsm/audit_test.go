package fsm

import (
	"strings"
	"testing"
)

// soundFlow returns a short flow where every Requires has an earlier producer.
// A named fake: several scenarios start from it and change one point at a time.
func soundFlow() []Stage {
	return []Stage{
		{ID: "discovery", Requires: []Artifact{TaskID}, Produces: []Artifact{"repos"}},
		{ID: "setup", Requires: []Artifact{"repos"}, Produces: []Artifact{"worktree"}},
		{ID: "intake", Requires: []Artifact{TaskID, "worktree"}, Produces: []Artifact{"briefing"}},
	}
}

// TestSoundFlowReportsNoGap covers scenario B1.
//
// Walking the stages in order, every required artifact was produced by an earlier
// stage. Such a flow is sound on paper, and the audit reports nothing.
func TestSoundFlowReportsNoGap(t *testing.T) {
	gaps := AuditContract(soundFlow())

	if len(gaps) != 0 {
		t.Errorf("a sound flow should report no gap, got %v", gaps)
	}
}

// TestRequirementWithoutProducerIsReported covers scenario B2.
//
// A stage requiring an artifact nobody produces earlier breaks the flow on paper.
// The audit reports it naming both the stage and the missing artifact — without
// the two, the message costs a debugging session.
func TestRequirementWithoutProducerIsReported(t *testing.T) {
	flow := []Stage{
		{ID: "discovery", Requires: []Artifact{TaskID}, Produces: []Artifact{"repos"}},
		{ID: "build", Requires: []Artifact{"approach"}, Produces: []Artifact{"code"}},
	}

	gaps := AuditContract(flow)

	if len(gaps) != 1 {
		t.Fatalf("want 1 gap, got %d (%v)", len(gaps), gaps)
	}
	if gaps[0].Stage != "build" {
		t.Errorf("gap stage: want build, got %q", gaps[0].Stage)
	}
	if len(gaps[0].Missing) != 1 || gaps[0].Missing[0] != "approach" {
		t.Errorf("Missing: want [approach], got %v", gaps[0].Missing)
	}
}

// TestAuditArtifactDoesNotSatisfyRequirement covers scenario B3.
//
// A ProducesForHuman does not join the set of available artifacts: it is read by a
// person, not consumed by the flow. A stage requiring an audit artifact is
// requiring something the flow does not deliver, and that is a gap.
//
// This is the test proving ProducesForHuman does something: if it started
// satisfying Requires, the field would be decorative.
func TestAuditArtifactDoesNotSatisfyRequirement(t *testing.T) {
	flow := []Stage{
		{ID: "verify", Requires: []Artifact{TaskID}, Produces: []Artifact{"ci_green"}, ProducesForHuman: []Artifact{"dod_checked"}},
		{ID: "commit", Requires: []Artifact{"dod_checked"}, Produces: []Artifact{"commit_sha"}},
	}

	gaps := AuditContract(flow)

	if len(gaps) != 1 {
		t.Fatalf("an audit artifact does not satisfy Requires; want 1 gap, got %d (%v)", len(gaps), gaps)
	}
	if gaps[0].Stage != "commit" || gaps[0].Missing[0] != "dod_checked" {
		t.Errorf("want gap commit/dod_checked, got %s/%v", gaps[0].Stage, gaps[0].Missing)
	}
}

// TestArtifactProducedLaterDoesNotSatisfy covers scenario B4.
//
// Order matters: an artifact produced by a later stage is not available to an
// earlier one. The audit reports it even though a producer exists in the set —
// what it checks is precedence, not existence.
func TestArtifactProducedLaterDoesNotSatisfy(t *testing.T) {
	flow := []Stage{
		{ID: "build", Requires: []Artifact{"scenarios"}, Produces: []Artifact{"code"}},
		{ID: "scenarios", Requires: []Artifact{TaskID}, Produces: []Artifact{"scenarios"}},
	}

	gaps := AuditContract(flow)

	if len(gaps) != 1 {
		t.Fatalf("want 1 gap from inverted order, got %d (%v)", len(gaps), gaps)
	}
	if gaps[0].Stage != "build" {
		t.Errorf("want gap in build, got %q", gaps[0].Stage)
	}
}

// TestTaskIDIsAvailableFromTheStart covers scenario B5.
//
// The task already arrives with its identifier: it is the root of the graph and
// the only input no stage produces. Every other artifact needs a declared
// producer.
func TestTaskIDIsAvailableFromTheStart(t *testing.T) {
	flow := []Stage{
		{ID: "discovery", Requires: []Artifact{TaskID}, Produces: []Artifact{"repos"}},
	}

	if gaps := AuditContract(flow); len(gaps) != 0 {
		t.Errorf("task_id is the root and should report no gap, got %v", gaps)
	}

	withoutRoot := []Stage{
		{ID: "discovery", Requires: []Artifact{"something_else"}, Produces: []Artifact{"repos"}},
	}

	if gaps := AuditContract(withoutRoot); len(gaps) != 1 {
		t.Errorf("task_id is the only external input; want 1 gap, got %v", gaps)
	}
}

// TestEveryGapIsReported covers scenario B6.
//
// The audit walks the whole flow and reports everything it finds. Stopping at the
// first gap would make whoever fixes it discover the rest one at a time, on every
// new run.
func TestEveryGapIsReported(t *testing.T) {
	flow := []Stage{
		{ID: "a", Requires: []Artifact{"missing_one"}, Produces: []Artifact{"x"}},
		{ID: "b", Requires: []Artifact{"missing_two"}, Produces: []Artifact{"y"}},
		{ID: "c", Requires: []Artifact{"missing_three"}, Produces: []Artifact{"z"}},
	}

	gaps := AuditContract(flow)

	if len(gaps) != 3 {
		t.Fatalf("want 3 gaps reported together, got %d (%v)", len(gaps), gaps)
	}
	for i, want := range []StageID{"a", "b", "c"} {
		if gaps[i].Stage != want {
			t.Errorf("gap %d: want stage %q, got %q", i, want, gaps[i].Stage)
		}
	}
}

// TestGapGroupsMissingArtifactsByStage covers the edge of B6.
//
// A stage missing two inputs yields one gap carrying both, not two gaps — whoever
// reads the report wants to know what the stage needs in order to start.
func TestGapGroupsMissingArtifactsByStage(t *testing.T) {
	flow := []Stage{
		{ID: "build", Requires: []Artifact{"scenarios", "approach"}, Produces: []Artifact{"code"}},
	}

	gaps := AuditContract(flow)

	if len(gaps) != 1 {
		t.Fatalf("want 1 gap grouped by stage, got %d (%v)", len(gaps), gaps)
	}
	if len(gaps[0].Missing) != 2 {
		t.Errorf("Missing: want 2 artifacts in the same gap, got %v", gaps[0].Missing)
	}
}

// TestEmptyFlowReportsNoGap covers the degenerate edge.
//
// A flow with no stages has no requirement to violate. It is a limit case, but
// callers should not have to handle it themselves.
func TestEmptyFlowReportsNoGap(t *testing.T) {
	if gaps := AuditContract(nil); len(gaps) != 0 {
		t.Errorf("an empty flow has nothing to report, got %v", gaps)
	}
}

// TestAReadableCriterionMustBeOneTheGateActuallyJudges keeps a typo from quietly
// narrowing what the lead may approve on.
//
// `judge_by_reading` names criteria out of `judge`. A name that matches none of
// them is not an extra criterion — it is an exception that applies to nothing,
// so the criterion it was meant to free stays unsupported and the gate goes on
// waiting for a person. That is the failure this whole field exists to remove,
// arriving silently through a spelling mistake.
func TestAReadableCriterionMustBeOneTheGateActuallyJudges(t *testing.T) {
	flow := []Stage{{
		ID: "plan", Role: "maker",
		Produces: []Artifact{"contract"},
		Gate: &GateSpec{
			Kind:          GateReviewArtifact,
			Artifact:      "contract",
			Judge:         []string{"the contract states what is forbidden"},
			ReadableJudge: []string{"the contract sates what is forbidden"}, // typo
		},
	}}

	gaps := AuditGateCriteria(flow)
	if len(gaps) == 0 {
		t.Fatal("a readable criterion that matches no judged one must be reported")
	}
	if !strings.Contains(gaps[0].Criterion, "sates") {
		t.Errorf("the report must name the invalid value, got %+v", gaps[0])
	}
}

// TestAGateWhoseReadableCriteriaAllMatchIsClean is the other side, so the check
// cannot pass by reporting everything.
func TestAGateWhoseReadableCriteriaAllMatchIsClean(t *testing.T) {
	flow := []Stage{{
		ID: "plan", Role: "maker",
		Produces: []Artifact{"contract"},
		Gate: &GateSpec{
			Kind:          GateReviewArtifact,
			Artifact:      "contract",
			Judge:         []string{"a", "b"},
			ReadableJudge: []string{"b"},
		},
	}}

	if gaps := AuditGateCriteria(flow); len(gaps) != 0 {
		t.Errorf("a gate whose readable criteria all match must be clean, got %+v", gaps)
	}
}
