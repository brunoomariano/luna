package fsm

import (
	"errors"
	"testing"
)

// NoStage is the zero StageID: what NextStage returns alongside ok=false when the
// flow is over. Declared here so the tests name the concept instead of comparing
// against a bare "".
const NoStage StageID = ""

// TestFlowStartsAtTheFirstStage covers scenario D1.
//
// A task that has not started yet has no current stage. Asking what comes next
// hands back the first stage of the flow — there is nothing to skip past.
func TestFlowStartsAtTheFirstStage(t *testing.T) {
	ctx := NewTaskContext(KindFeature)

	next, ok, err := NextStage(DefaultFlow(), NoStage, ctx)
	if err != nil {
		t.Fatalf("starting a flow is not an error: %v", err)
	}
	if !ok {
		t.Fatal("a non-empty flow always has a first stage")
	}
	if next != "setup" {
		t.Errorf("want setup as the entry stage, got %q", next)
	}
}

// TestNextStageFollowsDeclarationOrder covers scenario D2.
//
// With no condition in the way, the next stage is simply the following one in the
// flow. Order of declaration is order of execution.
func TestNextStageFollowsDeclarationOrder(t *testing.T) {
	ctx := NewTaskContext(KindFeature)

	next, ok, err := NextStage(DefaultFlow(), "setup", ctx)

	if err != nil || !ok {
		t.Fatalf("intake follows setup; got ok=%v err=%v", ok, err)
	}
	if next != "intake" {
		t.Errorf("want intake after setup, got %q", next)
	}
}

// TestUnsatisfiedConditionIsSkipped covers scenario D3.
//
// A stage whose condition does not hold is skipped, not blocked: on a feature,
// diagnose simply does not exist in that flow, and scenarios comes right after
// intake.
func TestUnsatisfiedConditionIsSkipped(t *testing.T) {
	ctx := NewTaskContext(KindFeature)

	next, ok, err := NextStage(DefaultFlow(), "intake", ctx)

	if err != nil || !ok {
		t.Fatalf("a skipped stage is not an error; got ok=%v err=%v", ok, err)
	}
	if next != "scenarios" {
		t.Errorf("diagnose is bug-only, so scenarios should follow intake; got %q", next)
	}
}

// TestSeveralConditionalsAreSkippedAtOnce covers scenario D4.
//
// Skipping is not one stage at a time. On a chore, qa is out by kind, so
// code-review — which is only out on docs — is what follows verify: two stages
// collapse into one step.
//
// The tail of the same flow skips three in a row: after code-review, harden is
// out by kind and architecture is out for lack of the discovered fact, so commit
// comes next.
func TestSeveralConditionalsAreSkippedAtOnce(t *testing.T) {
	ctx := NewTaskContext(KindChore)

	next, ok, err := NextStage(DefaultFlow(), "verify", ctx)
	if err != nil || !ok {
		t.Fatalf("want a stage after verify; got ok=%v err=%v", ok, err)
	}
	if next != "code-review" {
		t.Errorf("qa is out on a chore but code-review is not; want code-review, got %q", next)
	}

	// harden is feature-or-bug and architecture needs a discovered fact, so on a
	// chore both are out — and since ADR-0062 removed `commit`, code-review is the
	// last stage that runs. Reaching the end is ok=false, not an error.
	if _, ok, err := NextStage(DefaultFlow(), "code-review", ctx); ok || err != nil {
		t.Errorf("a chore ends at code-review; got ok=%v err=%v", ok, err)
	}
}

// TestFlowEndsAfterTheLastStage covers scenario D5.
//
// After the last stage there is nothing left, and that is a normal outcome — not
// a failure. The caller learns it from ok=false rather than from an error.
func TestFlowEndsAfterTheLastStage(t *testing.T) {
	ctx := NewTaskContext(KindFeature)

	next, ok, err := NextStage(DefaultFlow(), "architecture", ctx)
	if err != nil {
		t.Fatalf("reaching the end of the flow is not an error: %v", err)
	}
	if ok {
		t.Errorf("nothing follows commit, got %q", next)
	}
	if next != NoStage {
		t.Errorf("want the zero StageID when the flow is over, got %q", next)
	}
}

// TestConditionIsEvaluatedAgainstTheCurrentContext covers scenario D6.
//
// The condition is checked when the transition happens, not once at the start.
// architecture stays out until the build reveals the change touched the
// structure; from then on it is in the flow.
//
// This is the scenario that justifies When taking the whole TaskContext: if the
// condition were evaluated against the initial state, architecture could never
// enter.
func TestConditionIsEvaluatedAgainstTheCurrentContext(t *testing.T) {
	ctx := NewTaskContext(KindFeature)

	next, _, err := NextStage(DefaultFlow(), "harden", ctx)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if next == "architecture" {
		t.Fatal("without the discovered fact, architecture must stay out")
	}

	ctx.Facts[TouchesStructure] = true

	next, ok, err := NextStage(DefaultFlow(), "harden", ctx)
	if err != nil || !ok {
		t.Fatalf("want a stage after harden; got ok=%v err=%v", ok, err)
	}
	if next != "architecture" {
		t.Errorf("once the fact is discovered architecture enters; got %q", next)
	}
}

// TestUnknownStageIsAnError covers scenario D7.
//
// Asking what follows a stage that is not in the flow is a programming error, and
// it says so. Treating it as "start from the beginning" — which is what a naive
// index lookup does, since a miss yields -1 — would silently restart the task on
// a typo.
func TestUnknownStageIsAnError(t *testing.T) {
	ctx := NewTaskContext(KindFeature)

	_, _, err := NextStage(DefaultFlow(), "buld", ctx)

	if err == nil {
		t.Fatal("an unknown stage must be an error, not a silent restart")
	}
	if !errors.Is(err, ErrUnknownStage) {
		t.Errorf("want ErrUnknownStage, got %v", err)
	}
}

// TestNothingIsMissingWhenTheContextIsComplete covers scenario E1.
//
// With every required artifact present, the stage is clear to start and the
// answer is an empty list.
func TestNothingIsMissingWhenTheContextIsComplete(t *testing.T) {
	build := Stage{
		ID:       "build",
		Requires: []Artifact{"scenarios", "approach", "worktree"},
		Produces: []Artifact{"code"},
	}
	ctx := NewTaskContext(KindFeature)
	for _, a := range build.Requires {
		ctx.Artifacts[a] = true
	}

	if missing := MissingFor(build, ctx); len(missing) != 0 {
		t.Errorf("with everything in context nothing is missing, got %v", missing)
	}
}

// TestMissingArtifactIsNamed covers scenario E2.
//
// The answer names what is missing. "Incomplete" alone would send whoever is
// debugging back to the contract to work out which input never arrived.
func TestMissingArtifactIsNamed(t *testing.T) {
	build := Stage{
		ID:       "build",
		Requires: []Artifact{"scenarios", "approach"},
		Produces: []Artifact{"code"},
	}
	ctx := NewTaskContext(KindFeature)
	ctx.Artifacts["scenarios"] = true

	missing := MissingFor(build, ctx)

	if len(missing) != 1 {
		t.Fatalf("want 1 missing artifact, got %d (%v)", len(missing), missing)
	}
	if missing[0] != "approach" {
		t.Errorf("want approach named, got %q", missing[0])
	}
}

// TestOnlyTheAbsentArtifactsAreReported covers scenario E3.
//
// A partial context reports only the gap, not the whole requirement list. What is
// already there is not the problem.
func TestOnlyTheAbsentArtifactsAreReported(t *testing.T) {
	build := Stage{
		ID:       "build",
		Requires: []Artifact{"scenarios", "approach", "worktree"},
		Produces: []Artifact{"code"},
	}
	ctx := NewTaskContext(KindFeature)
	ctx.Artifacts["scenarios"] = true
	ctx.Artifacts["approach"] = true

	missing := MissingFor(build, ctx)

	if len(missing) != 1 || missing[0] != "worktree" {
		t.Errorf("want only worktree reported, got %v", missing)
	}
}

// TestEntryCheckAgreesWithTheStaticCheck covers scenario E4.
//
// The entry check and the static check answer different questions — one about the
// running context, the other about the flow on paper — but they agree on what
// counts as an input. An audit artifact satisfies neither.
//
// The two overlap on purpose: if someone ever loosens the static check, this one
// still catches it at runtime.
func TestEntryCheckAgreesWithTheStaticCheck(t *testing.T) {
	commit := Stage{
		ID:       "architecture",
		Requires: []Artifact{"dod_checked"},
		Produces: []Artifact{"commit_sha"},
	}
	ctx := NewTaskContext(KindFeature)

	// dod_checked was produced for a human to read, not for the flow to consume.
	// Whoever wrote it into the context did so as an audit artifact.
	ctx.Artifacts["dod_checked"] = false

	missing := MissingFor(commit, ctx)

	if len(missing) != 1 || missing[0] != "dod_checked" {
		t.Errorf("an audit artifact does not satisfy a requirement, got %v", missing)
	}
}

// TestMissingForOnAStageWithNoRequirements covers the degenerate edge.
//
// A stage that requires nothing is always clear to start. Callers should not have
// to special-case it.
func TestMissingForOnAStageWithNoRequirements(t *testing.T) {
	free := Stage{ID: "free", Produces: []Artifact{"something"}}

	if missing := MissingFor(free, NewTaskContext(KindFeature)); len(missing) != 0 {
		t.Errorf("a stage with no requirements is always ready, got %v", missing)
	}
}
