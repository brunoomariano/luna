package fsm

import (
	"errors"
	"strings"
	"testing"
)

// readyTask returns a task at the very start of the default flow. A named fake:
// most scenarios begin here and change one thing.
func readyTask(kind TaskKind) TaskState {
	return NewTaskState("LUNA-1", kind)
}

// atStage drives a task to the given stage the way the engine would, so the
// scenarios that care about a mid-flow transition do not hand-build a state that
// the reducer could never have produced.
func atStage(t *testing.T, kind TaskKind, target StageID, produced ...Artifact) TaskState {
	t.Helper()

	state := readyTask(kind)
	for range DefaultFlow() {
		if state.Stage == target {
			break
		}
		next, err := Reduce(state, Advance{Flow: DefaultFlow()})
		if err != nil {
			t.Fatalf("driving to %q: %v", target, err)
		}
		if next.Status == StatusAwaitingGate {
			next, err = Reduce(next, GateApprove{})
			if err != nil {
				t.Fatalf("approving gate on the way to %q: %v", target, err)
			}
		}
		if next.Stage != target {
			stage := stageIn(DefaultFlow(), next.Stage)
			owed := append(append([]Artifact{}, stage.Produces...), stage.ProducesForHuman...)
			next, err = Reduce(next, Complete{
				Delivered: owed,
				Evidence:  passing(stage, owed),
			})
			if err != nil {
				t.Fatalf("completing %q: %v", next.Stage, err)
			}
			// A review gate opens on the way *out* now, so a stage that
			// carries one leaves the task waiting here rather than on the next
			// advance. Approving keeps this helper doing what it says: driving.
			if next.Status == StatusAwaitingGate {
				next, err = Reduce(next, GateApprove{})
				if err != nil {
					t.Fatalf("approving the closing gate on %q: %v", next.Stage, err)
				}
			}
		}
		state = next
	}

	for _, a := range produced {
		state.Context.Artifacts[a] = true
	}
	return state
}

// passing is the cheapest evidence that closes a stage: every artifact arrives
// proven at exactly the scope its contract declared. A named fake for the
// scenarios whose subject is the transition rather than the verdict — the ones
// that do care about the verdict build their own Evidence and say what it proves.
//
// It takes the stage rather than a bare list because the exit check compares
// scopes now: an artifact declared with a command is not closed by existence
// alone, so a helper that always answered Exists would be testing a flow no
// contract describes.
func passing(stage Stage, owed []Artifact) map[Artifact]Evidence {
	evidence := map[Artifact]Evidence{}
	for _, a := range owed {
		evidence[a] = Evidence{
			Scope:   VerifierFor(stage, a).Proves(),
			Verdict: VerdictPassed,
			Command: VerifierFor(stage, a).Describe(),
		}
	}
	return evidence
}

// ── block F: entering a stage ────────────────────────────────────────────────

// TestAdvanceEntersTheFirstStage covers scenario F1.
//
// A ready task advances into the flow's entry stage and starts running.
func TestAdvanceEntersTheFirstStage(t *testing.T) {
	state, err := Reduce(readyTask(KindFeature), Advance{Flow: DefaultFlow()})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if state.Stage != "setup" {
		t.Errorf("want setup, got %q", state.Stage)
	}
	// setup is mechanical and opens no gate, so the task runs straight into it.
	// It became the first stage when integration left Luna's scope, removing
	// `commit`, and `discovery` went with it.
	if state.Status != StatusRunning {
		t.Errorf("setup opens no gate; want running, got %q", state.Status)
	}
}

// TestAdvanceRefusesAStageMissingItsInputs covers scenario F2.
//
// This is the contract's entry check as a transition: the FSM does not call an
// agent for a stage whose requires are not in the context. It blocks instead, and
// names what is missing.
func TestAdvanceRefusesAStageMissingItsInputs(t *testing.T) {
	// A flow whose second stage requires something nobody produced.
	flow := []Stage{
		{ID: "first", Requires: []Artifact{TaskID}, Produces: []Artifact{"repos"}},
		{ID: "second", Requires: []Artifact{"never_produced"}, Produces: []Artifact{"x"}},
	}

	state := readyTask(KindFeature)
	state, err := Reduce(state, Advance{Flow: flow})
	if err != nil {
		t.Fatalf("entering the first stage: %v", err)
	}
	repos := []Artifact{"repos"}
	state, err = Reduce(state, Complete{Delivered: repos, Evidence: passing(stageIn(flow, state.Stage), repos), Flow: flow})
	if err != nil {
		t.Fatalf("completing the first stage: %v", err)
	}

	state, err = Reduce(state, Advance{Flow: flow})
	if err != nil {
		t.Fatalf("a refused entry is a state, not an error: %v", err)
	}

	if state.Status != StatusBlocked {
		t.Errorf("want blocked when an input is missing, got %q", state.Status)
	}
	if state.Blocked == "" {
		t.Error("a block must say why")
	}
}

// TestAdvanceEndsTheFlowAfterTheLastStage covers scenario F3.
//
// Reaching the end is the happy path, not a failure: the task becomes done.
func TestAdvanceEndsTheFlowAfterTheLastStage(t *testing.T) {
	// `commit` was the last stage until integration left Luna's scope. On a chore
	// the flow now ends after verify: `review` is the only stage past it, and it
	// is not-chore.
	state := atStage(t, KindChore, "verify")
	stage := stageIn(DefaultFlow(), "verify")

	// verify owes `dod_checked` to a human, so the delivery has to include
	// ProducesForHuman — the exit check counts both fields (INV-3).
	owed := append(append([]Artifact{}, stage.Produces...), stage.ProducesForHuman...)
	state, err := Reduce(state, Complete{
		Delivered: owed,
		Evidence:  passing(stage, owed),
	})
	if err != nil {
		t.Fatalf("completing verify: %v", err)
	}
	state, err = Reduce(state, Advance{Flow: DefaultFlow()})
	if err != nil {
		t.Fatalf("ending the flow is not an error: %v", err)
	}

	if state.Status != StatusDone {
		t.Errorf("want done after the last stage, got %q", state.Status)
	}
}

// ── block G: closing a stage ─────────────────────────────────────────────────

// TestCompleteClosesAStageThatDeliveredEverything covers scenario G1.
//
// The delivered artifacts enter the context, and the task is ready to advance.
func TestCompleteClosesAStageThatDeliveredEverything(t *testing.T) {
	state := atStage(t, KindFeature, "build")
	stage := stageIn(DefaultFlow(), "build")

	evidence := passing(stage, stage.Produces)
	evidence["tests_green"] = Evidence{
		Scope:    ScopeFull,
		Verdict:  VerdictPassed,
		Command:  "go test ./...",
		ExitCode: 0,
	}

	state, err := Reduce(state, Complete{Delivered: stage.Produces, Evidence: evidence})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	for _, a := range stage.Produces {
		if !state.Context.HasArtifact(a) {
			t.Errorf("%q should be in the context after the stage closed", a)
		}
	}
	if state.Evidence["tests_green"].Command != "go test ./..." {
		t.Errorf("the evidence for a delivery must be kept, got %+v", state.Evidence["tests_green"])
	}
}

// TestCompleteRefusesEvidenceWeakerThanTheContract covers INV-1's second
// acceptance criterion.
//
// The delivery is complete and the verdict passed — what is wrong is that the
// check was not the one the contract declared. `build` proves tests_green by
// running a command, so evidence that only witnesses the artifact on disk has
// delivered it, not verified it, and closing on that is the laundering the
// scopes exist to prevent.
func TestCompleteRefusesEvidenceWeakerThanTheContract(t *testing.T) {
	state := atStage(t, KindFeature, "build")
	stage := stageIn(DefaultFlow(), "build")

	evidence := passing(stage, stage.Produces)
	// Everything as the contract asked, except the one artifact that owed a
	// command and arrived witnessed instead.
	evidence["tests_green"] = Exists(0)

	state, err := Reduce(state, Complete{Delivered: stage.Produces, Evidence: evidence})
	if err != nil {
		t.Fatalf("weak evidence is a state, not an error: %v", err)
	}

	if state.Status != StatusBlocked {
		t.Errorf("want blocked when the check was weaker than declared, got %q", state.Status)
	}
	if !strings.Contains(state.Blocked, "tests_green") {
		t.Errorf("the block must name the artifact that was under-proven, got %q", state.Blocked)
	}
	if state.Context.HasArtifact("tests_green") {
		t.Error("nothing enters the context when the stage does not close")
	}
	// The audit needs the record even so: what arrived is what explains the block.
	if state.Evidence["tests_green"].Scope != ScopeExistence {
		t.Errorf("the evidence that caused the block must be kept, got %+v", state.Evidence["tests_green"])
	}
}

// TestStrongerEvidenceThanAskedForIsAccepted is the other direction, and it is
// not symmetric with the test above.
//
// A stage that ran the whole suite where its contract only wanted delivery has
// proven more than it had to. Refusing that would be refusing good news — the
// rule is that evidence never claims *more* than the check proved, not that it
// must claim exactly what was asked.
func TestStrongerEvidenceThanAskedForIsAccepted(t *testing.T) {
	state := atStage(t, KindFeature, "build")
	stage := stageIn(DefaultFlow(), "build")

	evidence := passing(stage, stage.Produces)
	// `code` is declared Existence; this run proved more.
	evidence["code"] = Evidence{Scope: ScopeFull, Verdict: VerdictPassed, Command: "make ci"}

	state, err := Reduce(state, Complete{Delivered: stage.Produces, Evidence: evidence})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if state.Status != StatusStageDone {
		t.Errorf("stronger evidence closes the stage, got %q: %s", state.Status, state.Blocked)
	}
}

// TestCompleteRefusesAPartialDelivery covers scenario G2.
//
// The exit check, and the one that matters most: a stage that promised two
// artifacts and delivered one does not close. Nothing enters the context and no
// handoff is written — the hole is caught where it is born rather than two stages
// downstream.
func TestCompleteRefusesAPartialDelivery(t *testing.T) {
	state := atStage(t, KindFeature, "build")

	state, err := Reduce(state, Complete{Delivered: []Artifact{"code"}})
	if err != nil {
		t.Fatalf("a refused delivery is a state, not an error: %v", err)
	}

	// It does not close, which is the guarantee. What it does instead is ask
	// again: the stage stays running and the next attempt is told what is missing.
	if state.Status != StatusRunning {
		t.Errorf("want the stage still running after a partial delivery, got %q", state.Status)
	}
	if state.Context.HasArtifact("code") {
		t.Error("nothing enters the context when the stage does not close")
	}
	if len(state.StillOwed) == 0 {
		t.Error("the next attempt is not told what is missing")
	}
}

// TestAPartialDeliveryIsAskedAgainAndThenBlocks. The asymmetry this closes was
// measured: a harness that would not start got two retries through Fail, and an
// agent that delivered everything but one handover got none — straight to a block,
// with the retry budget untouched and the socket still open.
func TestAPartialDeliveryIsAskedAgainAndThenBlocks(t *testing.T) {
	state := atStage(t, KindFeature, "build")

	var err error
	for attempt := 1; attempt <= state.Retry.Max; attempt++ {
		if state, err = Reduce(state, Complete{Delivered: []Artifact{"code"}}); err != nil {
			t.Fatalf("attempt %d: %v", attempt, err)
		}
		if state.Status != StatusRunning {
			t.Fatalf("attempt %d ended as %q rather than being asked again", attempt, state.Status)
		}
		if len(state.StillOwed) != 1 || state.StillOwed[0] != "tests_green" {
			t.Errorf("attempt %d does not name what is missing: %v", attempt, state.StillOwed)
		}
	}

	// The budget is spent, so the next one stops the task rather than looping.
	if state, err = Reduce(state, Complete{Delivered: []Artifact{"code"}}); err != nil {
		t.Fatalf("the last attempt: %v", err)
	}
	if state.Status != StatusBlocked {
		t.Fatalf("a stage that never completed its contract did not block: %q", state.Status)
	}
	if !strings.Contains(state.Blocked, "attempts") {
		t.Errorf("the block does not say it was asked more than once: %s", state.Blocked)
	}
}

// TestFinishingTheContractOnASecondAttemptCloses is the case the retry exists for:
// a stage that committed its work and forgot one handover finishes it, and what it
// delivered the first time is not thrown away.
func TestFinishingTheContractOnASecondAttemptCloses(t *testing.T) {
	state := atStage(t, KindFeature, "build")
	stage := stageIn(DefaultFlow(), "build")

	state, err := Reduce(state, Complete{
		Delivered: []Artifact{"code"},
		Evidence:  passing(stage, []Artifact{"code"}),
	})
	if err != nil {
		t.Fatalf("the first attempt: %v", err)
	}

	owed := append(append([]Artifact{}, stage.Produces...), stage.ProducesForHuman...)
	if state, err = Reduce(state, Complete{
		Delivered: owed,
		Evidence:  passing(stage, owed),
		Commit:    "cafe1234",
	}); err != nil {
		t.Fatalf("the second attempt: %v", err)
	}

	if state.Status != StatusStageDone {
		t.Fatalf("the completed contract did not close the stage: %q — %s", state.Status, state.Blocked)
	}
	if len(state.StillOwed) != 0 {
		t.Errorf("a paid debt is still recorded: %v", state.StillOwed)
	}
	if state.Retry.Attempts != 0 {
		t.Errorf("the retry budget was not restored on a stage that closed: %d", state.Retry.Attempts)
	}
}

// TestCompleteRequiresTheHumanReport covers scenario G3 — INV-3.
//
// A stage owing an audit report does not close without it. The report has no
// consumer in the flow, so nothing downstream would ever miss it: without this
// check it would simply never be written.
func TestCompleteRequiresTheHumanReport(t *testing.T) {
	state := atStage(t, KindFeature, "verify")

	// verify produces ci_green for the flow and dod_checked for a person.
	state, err := Reduce(state, Complete{Delivered: []Artifact{"ci_green"}})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if state.Status == StatusStageDone {
		t.Error("a stage closed without the report only a person reads")
	}
	if len(state.StillOwed) != 1 || state.StillOwed[0] != "dod_checked" {
		t.Errorf("the missing report is not named for the next attempt: %v", state.StillOwed)
	}
}

// TestAuditReportDoesNotEnterTheFlowContext covers the other half of G3.
//
// The report is required on exit, but it is not an input: putting it in the
// context would let it satisfy some stage's requires, which is the very thing
// separating `Produces` from `ProducesForHuman` prevents.
func TestAuditReportDoesNotEnterTheFlowContext(t *testing.T) {
	state := atStage(t, KindFeature, "verify")
	stage := stageIn(DefaultFlow(), "verify")

	owed := append(append([]Artifact{}, stage.Produces...), stage.ProducesForHuman...)
	state, err := Reduce(state, Complete{Delivered: owed, Evidence: passing(stage, owed)})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if !state.Context.HasArtifact("ci_green") {
		t.Error("ci_green is a flow product and belongs in the context")
	}
	if state.Context.HasArtifact("dod_checked") {
		t.Error("an audit report must not become an input")
	}
}

// ── block H: failure and gates ───────────────────────────────────────────────

// TestFailRetriesTwiceThenBlocks covers scenario H1 — the retry ceiling.
//
// Two attempts with the error in context, then a block that notifies. No infinite
// retry, and no silent death.
func TestFailRetriesTwiceThenBlocks(t *testing.T) {
	state := atStage(t, KindFeature, "build")

	for attempt := 1; attempt <= 2; attempt++ {
		var err error
		state, err = Reduce(state, Fail{Reason: "compiler error"})
		if err != nil {
			t.Fatalf("attempt %d: %v", attempt, err)
		}
		if state.Status != StatusRunning {
			t.Errorf("attempt %d should retry, got %q", attempt, state.Status)
		}
		if state.Retry.Attempts != attempt {
			t.Errorf("want %d attempts recorded, got %d", attempt, state.Retry.Attempts)
		}
	}

	state, err := Reduce(state, Fail{Reason: "compiler error"})
	if err != nil {
		t.Fatalf("exhausting retries is a state, not an error: %v", err)
	}
	if state.Status != StatusBlocked {
		t.Errorf("want blocked after the retries are spent, got %q", state.Status)
	}
	if state.Blocked == "" {
		t.Error("a blocked task must carry the reason it notifies with")
	}
}

// TestGateApproveResumesTheStage covers scenario H2.
func TestGateApproveResumesTheStage(t *testing.T) {
	// A one-stage flow with a gate: what this is about is the approve, not which
	// shipped stage happens to carry one.
	gated := []Stage{{
		ID: "gated", Role: "someone", Requires: []Artifact{TaskID},
		Produces: []Artifact{"thing"},
		Gate:     &GateSpec{Kind: GateConfirm, Reason: "confirm it"},
	}}

	state, err := Reduce(readyTask(KindFeature), Advance{Flow: gated})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if state.Status != StatusAwaitingGate {
		t.Fatalf("expected a gate, got %q", state.Status)
	}

	state, err = Reduce(state, GateApprove{})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if state.Status != StatusRunning {
		t.Errorf("want running after approval, got %q", state.Status)
	}
	if state.Gate != nil {
		t.Error("an answered gate is no longer pending")
	}
}

// TestGateAdjustReplacesThePayload covers scenario H3 — approve, adjust or reject.
//
// The human edits the artifact and the edited version is what carries on. Without
// this the review would happen outside the system, leaving the handoff describing
// something other than what the next stage consumed.
func TestGateAdjustReplacesThePayload(t *testing.T) {
	state := readyTask(KindFeature)
	state.Status = StatusAwaitingGate
	state.Stage = "plan"
	state.Gate = &PendingGate{
		Kind:     GateReviewArtifact,
		Stage:    "plan",
		Artifact: "contract",
		Payload:  "the generated contract",
	}

	state, err := Reduce(state, GateAdjust{Payload: "the contract a human fixed"})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	// A review gate opens on the way out of the stage that produced its artifact,
	// so answering resumes at `stage_done` — the stage has already
	// run, and `running` would ask for it a second time.
	if state.Status != StatusStageDone {
		t.Errorf("want the task carrying on from the closed stage, got %q", state.Status)
	}
	if state.Evidence["contract"].Detail != "the contract a human fixed" {
		t.Errorf("the adjusted version is what carries on, got %+v", state.Evidence["contract"])
	}
}

// TestGateRejectSendsTheStageBack covers scenario H4 — approve, adjust or reject.
//
// A rejected artifact does not enter the context, and the stage that produced it
// runs again with the rejection in hand. Unlike the review rollback, there is no
// green to invalidate: this happens before the artifact was ever accepted.
func TestGateRejectSendsTheStageBack(t *testing.T) {
	state := readyTask(KindFeature)
	state.Status = StatusAwaitingGate
	state.Stage = "plan"
	state.Gate = &PendingGate{Kind: GateReviewArtifact, Stage: "plan", Artifact: "contract"}

	state, err := Reduce(state, GateReject{Reason: "the approach does not hold"})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if state.Status != StatusRunning {
		t.Errorf("want the stage running again, got %q", state.Status)
	}
	if state.Stage != "plan" {
		t.Errorf("the rejecting stage runs again; got %q", state.Stage)
	}
	if state.Context.HasArtifact("contract") {
		t.Error("a rejected artifact must not enter the context")
	}
}

// ── block I: review findings and loop ceilings ───────────────────────────────

// TestAlignedFindingInvalidatesTheGreen covers scenario I1 — an aligned finding
// invalidating the green.
//
// The check the whole rule exists for: going back to build removes ci_green, so
// the stages downstream cannot be satisfied by a verification that ran against
// code which no longer exists.
func TestAlignedFindingInvalidatesTheGreen(t *testing.T) {
	state := atStage(t, KindFeature, "review")
	state.Context.Artifacts["ci_green"] = true

	state, err := Reduce(state, ReviewFinding{Aligned: true, Summary: "wrong boundary"})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if state.Stage != "build" {
		t.Errorf("an aligned finding goes back to build, got %q", state.Stage)
	}
	if state.Context.HasArtifact("ci_green") {
		t.Error("the green must be invalidated on the way back")
	}
}

// TestUnalignedFindingLeavesTheFlowAlone covers scenario I2.
//
// Out of scope becomes someone else's task. The flow carries on, and the green
// stays valid because the code did not change.
func TestUnalignedFindingLeavesTheFlowAlone(t *testing.T) {
	state := atStage(t, KindFeature, "review")
	state.Context.Artifacts["ci_green"] = true

	state, err := Reduce(state, ReviewFinding{Aligned: false, Summary: "unrelated debt"})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if state.Stage != "review" {
		t.Errorf("an unaligned finding does not move the task, got %q", state.Stage)
	}
	if !state.Context.HasArtifact("ci_green") {
		t.Error("the code did not change, so the green still holds")
	}
}

// TestFindingFromANonReviewStageIsRejected covers scenario I3.
//
// Only a review stage produces a finding. Accepting one from build would let the
// implementer send its own work back, which is the separation the flow
// guarantees.
func TestFindingFromANonReviewStageIsRejected(t *testing.T) {
	state := atStage(t, KindFeature, "build")

	_, err := Reduce(state, ReviewFinding{Aligned: true, Summary: "self-review"})

	if !errors.Is(err, ErrIllegalTransition) {
		t.Errorf("want ErrIllegalTransition from a non-review stage, got %v", err)
	}
}

// TestLoopCeilingOpensAGateRatherThanBlocking covers scenario I4 — a spent loop
// ceiling stopping the task.
//
// Not converging is a decision to make, not a node failure. The distinction shows
// up here: a gate waits for a human with the history in view, a block reports an
// anomaly.
func TestLoopCeilingOpensAGateRatherThanBlocking(t *testing.T) {
	state := atStage(t, KindFeature, "review")
	state.Context.Artifacts["ci_green"] = true
	state.Loop.Rounds = DefaultLoopLimits().MaxRounds

	state, err := Reduce(state, ReviewFinding{Aligned: true, Summary: "again"})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if state.Status != StatusAwaitingGate {
		t.Errorf("a spent loop ceiling opens a gate, got %q", state.Status)
	}
	if state.Gate == nil || state.Gate.Kind != GateLoopCeiling {
		t.Errorf("want a loop-ceiling gate, got %+v", state.Gate)
	}
}

// TestOscillationIsCountedApartFromRounds covers scenario I5 — the loop ceilings.
//
// Returning to a stage already visited in this loop is a distinct pathology from
// simply going round again: one counter for both would let a productive loop and
// a thrashing one hit the same limit.
func TestOscillationIsCountedApartFromRounds(t *testing.T) {
	state := atStage(t, KindFeature, "review")
	state.Context.Artifacts["ci_green"] = true
	state.Loop.Visited = []StageID{"build", "review"}

	state, err := Reduce(state, ReviewFinding{Aligned: true, Summary: "same spot again"})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if state.Loop.Oscillation == 0 {
		t.Error("returning to a visited stage counts as oscillation")
	}
	if state.Loop.Rounds != 1 {
		t.Errorf("rounds counts trips, independently; got %d", state.Loop.Rounds)
	}
}

// ── block J: illegal transitions ─────────────────────────────────────────────

// TestIllegalTransitionsAreRefused covers scenario J1.
//
// The reducer is where the state machine's rules live, so an action that makes no
// sense for the current status is an error rather than a silent no-op. A silent
// no-op would let a caller believe a transition happened.
func TestIllegalTransitionsAreRefused(t *testing.T) {
	cases := []struct {
		name   string
		state  TaskState
		action Action
	}{
		{
			name:   "advancing while a gate is pending",
			state:  TaskState{Status: StatusAwaitingGate, Stage: "discovery"},
			action: Advance{Flow: DefaultFlow()},
		},
		{
			name:   "advancing while blocked",
			state:  TaskState{Status: StatusBlocked, Stage: "build"},
			action: Advance{Flow: DefaultFlow()},
		},
		{
			name:   "advancing a finished task",
			state:  TaskState{Status: StatusDone},
			action: Advance{Flow: DefaultFlow()},
		},
		{
			name:   "completing with no stage running",
			state:  TaskState{Status: StatusReady},
			action: Complete{Delivered: []Artifact{"code"}},
		},
		{
			name:   "failing with no stage running",
			state:  TaskState{Status: StatusReady},
			action: Fail{Reason: "nothing is running"},
		},
		{
			name:   "approving a gate that is not open",
			state:  TaskState{Status: StatusRunning, Stage: "build"},
			action: GateApprove{},
		},
	}

	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if _, err := Reduce(c.state, c.action); !errors.Is(err, ErrIllegalTransition) {
				t.Errorf("want ErrIllegalTransition, got %v", err)
			}
		})
	}
}

// TestUnblockReturnsTheTaskToItsStage covers scenario J2.
//
// A human clearing a block puts the task back to running, with the retry budget
// reset — the block was the escalation, and starting over from the old count
// would spend it again immediately.
func TestUnblockReturnsTheTaskToItsStage(t *testing.T) {
	state := TaskState{
		Status:   StatusBlocked,
		Stage:    "build",
		Blocked:  "tests failed three times",
		Retry:    Retry{Attempts: 2, Max: 2},
		Context:  NewTaskContext(KindFeature),
		Evidence: map[Artifact]Evidence{},
	}

	state, err := Reduce(state, Unblock{})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if state.Status != StatusRunning {
		t.Errorf("want running after an unblock, got %q", state.Status)
	}
	if state.Retry.Attempts != 0 {
		t.Errorf("the retry budget resets on unblock, got %d", state.Retry.Attempts)
	}
	if state.Blocked != "" {
		t.Error("the block reason is cleared once it is cleared")
	}
}

// TestCompleteSaysTheStageIsDone covers the status the exit produces.
//
// A closed stage and a stage about to start used to be both `running`, which left
// the caller to infer the difference from what landed in the context. If a stage
// finished, the status should say so.
func TestCompleteSaysTheStageIsDone(t *testing.T) {
	state := atStage(t, KindFeature, "build")
	stage := stageIn(DefaultFlow(), "build")

	state, err := Reduce(state, Complete{
		Delivered: stage.Produces,
		Evidence:  passing(stage, stage.Produces),
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if state.Status != StatusStageDone {
		t.Errorf("want stage_done after a stage closes, got %q", state.Status)
	}
	if state.Stage != "build" {
		t.Errorf("the stage that closed is still named, got %q", state.Stage)
	}
}

// TestAdvanceRefusesAStageStillRunning covers the guard the new status enables.
//
// Advancing past a node that has not reported would skip its work and its
// contract check both.
func TestAdvanceRefusesAStageStillRunning(t *testing.T) {
	state := atStage(t, KindFeature, "build")

	_, err := Reduce(state, Advance{Flow: DefaultFlow()})

	if !errors.Is(err, ErrIllegalTransition) {
		t.Errorf("want ErrIllegalTransition while a stage runs, got %v", err)
	}
}

// TestBlockStopsTheTaskInOneEvent covers the action that replaced the retry hack.
//
// The lead used to reach a block by recording Fail until the budget ran out,
// which left three failures in the log where there had been one decision. The
// history is the audit trail (INV-2).
func TestBlockStopsTheTaskInOneEvent(t *testing.T) {
	state := atStage(t, KindFeature, "build")

	state, err := Reduce(state, Block{Reason: "the judge escalated"})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if state.Status != StatusBlocked {
		t.Errorf("want the task blocked, got %q", state.Status)
	}
	if state.Blocked != "the judge escalated" {
		t.Errorf("want the reason kept, got %q", state.Blocked)
	}
	// The retry budget is untouched: nothing was attempted.
	if state.Retry.Attempts != 0 {
		t.Errorf("a block is not an attempt, got %d", state.Retry.Attempts)
	}
}

// TestABlockMustSayWhy covers the required reason.
//
// A task that halts without saying why is the silent failure INV-5 forbids,
// so the reason is required rather than defaulted to something unhelpful.
func TestABlockMustSayWhy(t *testing.T) {
	state := atStage(t, KindFeature, "build")

	if _, err := Reduce(state, Block{}); !errors.Is(err, ErrIllegalTransition) {
		t.Errorf("want ErrIllegalTransition for a reasonless block, got %v", err)
	}
}

// TestBlockingWhatIsAlreadyOverIsRefused covers the guard.
func TestBlockingWhatIsAlreadyOverIsRefused(t *testing.T) {
	for _, status := range []Status{StatusDone, StatusBlocked} {
		state := TaskState{Status: status, Evidence: map[Artifact]Evidence{}}

		if _, err := Reduce(state, Block{Reason: "again"}); !errors.Is(err, ErrIllegalTransition) {
			t.Errorf("%s: want ErrIllegalTransition, got %v", status, err)
		}
	}
}

// TestShippedProfilesAreTheDefaults covers the list the engine still owns.
//
// It is a seed for configuration rather than a validation list: the engine does
// not reject a name it has not heard of, because a project defines its own
// .
func TestShippedProfilesAreTheDefaults(t *testing.T) {
	want := map[Profile]bool{ProfileInteractive: true, ProfileTurbo: true, ProfileNightly: true}

	shipped := ShippedProfiles()
	if len(shipped) != len(want) {
		t.Fatalf("want %d shipped profiles, got %d: %v", len(want), len(shipped), shipped)
	}
	for _, p := range shipped {
		if !want[p] {
			t.Errorf("%q is not a shipped profile", p)
		}
	}
}

// ── block M: ending a task that will not finish ───────────────────

// TestAbandonEndsATaskFromWhereverItIs covers the reason abandon is wider than
// the other human actions.
//
// The cases it exists for are the ones nobody planned — a task whose flow changed
// under it, or one simply superseded — so it accepts any state that has not
// already ended.
func TestAbandonEndsATaskFromWhereverItIs(t *testing.T) {
	for _, from := range []struct {
		what  string
		state TaskState
	}{
		{"ready, before anything ran", readyTask(KindFeature)},
		{"running mid-flow", atStage(t, KindFeature, "build")},
		{"waiting at a gate", mustReduce(t, readyTask(KindFeature), Advance{Flow: DefaultFlow()})},
	} {
		state, err := Reduce(from.state, Abandon{Reason: "superseded"})
		if err != nil {
			t.Errorf("%s: abandoning must work: %v", from.what, err)
			continue
		}
		if state.Status != StatusAbandoned {
			t.Errorf("%s: want abandoned, got %q", from.what, state.Status)
		}
		if !state.IsTerminal() {
			t.Errorf("%s: an abandoned task has ended", from.what)
		}
		if state.Blocked != "superseded" {
			t.Errorf("%s: the reason must be kept, got %q", from.what, state.Blocked)
		}
	}
}

// TestAbandonClearsAPendingGate keeps an ended task out of the gate listing.
//
// A gate left pending on a task nobody will finish would sit in `luna gates`
// forever, waiting for a decision that no longer means anything (INV-5).
func TestAbandonClearsAPendingGate(t *testing.T) {
	gated := []Stage{{
		ID: "gated", Role: "someone", Requires: []Artifact{TaskID},
		Produces: []Artifact{"thing"},
		Gate:     &GateSpec{Kind: GateConfirm, Reason: "confirm it"},
	}}
	waiting := mustReduce(t, readyTask(KindFeature), Advance{Flow: gated})
	if waiting.Status != StatusAwaitingGate || waiting.Gate == nil {
		t.Fatalf("setup: want a task waiting at a gate, got %q", waiting.Status)
	}

	state := mustReduce(t, waiting, Abandon{Reason: "not doing this one"})
	if state.Gate != nil {
		t.Errorf("an ended task holds no pending gate, got %+v", state.Gate)
	}
}

// TestAbandonRefusesATaskThatAlreadyEnded keeps a second ending out of the log.
//
// A history showing a task finish twice is worse than an error the caller has to
// read, and the log has no way to take an event back (INV-2).
func TestAbandonRefusesATaskThatAlreadyEnded(t *testing.T) {
	ended := mustReduce(t, readyTask(KindChore), Abandon{Reason: "first"})

	if _, err := Reduce(ended, Abandon{Reason: "second"}); !errors.Is(err, ErrIllegalTransition) {
		t.Errorf("want an illegal transition ending an ended task, got %v", err)
	}
}

// TestAbandonNeedsAReason covers the one thing the engine insists on.
//
// The whole value of abandoning over deleting is that the audit says why, so an
// empty reason is the one case that defeats the point.
func TestAbandonNeedsAReason(t *testing.T) {
	if _, err := Reduce(readyTask(KindChore), Abandon{}); !errors.Is(err, ErrIllegalTransition) {
		t.Errorf("want a refusal without a reason, got %v", err)
	}
}

// TestBlockedIsNotTerminal is the distinction the endings decline to blur.
//
// A block is an anomaly a person clears with Unblock. Counting it as an ending
// would erase the difference between "this failed and someone should look" and
// "this is over" — and it is exactly the shortcut that would have made abandon
// unnecessary and the audit poorer.
func TestBlockedIsNotTerminal(t *testing.T) {
	blocked := mustReduce(t, atStage(t, KindFeature, "build"), Block{Reason: "the node died"})

	if blocked.Status != StatusBlocked {
		t.Fatalf("setup: want blocked, got %q", blocked.Status)
	}
	if blocked.IsTerminal() {
		t.Error("a blocked task is recoverable, not ended")
	}
}

// mustReduce applies one action and fails the test if it was refused.
func mustReduce(t *testing.T, state TaskState, action Action) TaskState {
	t.Helper()

	next, err := Reduce(state, action)
	if err != nil {
		t.Fatalf("applying %T: %v", action, err)
	}
	return next
}

// TestGateClosingAnswersOnlyForTheKindThatAsksAboutWorkDone is the seam the
// caller uses to know whether a decision is owed when a stage closes.
//
// It has to be narrow in both directions: a `confirm` opens on the way in and
// must not be decided twice, and a stage with no gate owes nothing at all.
func TestGateClosingAnswersOnlyForTheKindThatAsksAboutWorkDone(t *testing.T) {
	flow := []Stage{
		{ID: "plain", Requires: []Artifact{TaskID}, Produces: []Artifact{"a"}},
		{
			ID: "reviewed", Requires: []Artifact{"a"}, Produces: []Artifact{"b"},
			Gate: &GateSpec{Kind: GateReviewArtifact, Artifact: "b", Reason: "review it"},
		},
		{
			ID: "confirmed", Requires: []Artifact{"b"}, Produces: []Artifact{"c"},
			Gate: &GateSpec{Kind: GateConfirm, Reason: "carry on?"},
		},
	}

	running := func(stage StageID) TaskState {
		state := NewTaskState("LUNA-1", KindFeature)
		state.Status = StatusRunning
		state.Stage = stage
		return state
	}

	if gate := GateClosing(running("reviewed"), flow); gate == nil || gate.Artifact != "b" {
		t.Errorf("a review gate is owed a decision when its stage closes, got %+v", gate)
	}
	if gate := GateClosing(running("confirmed"), flow); gate != nil {
		t.Errorf("a confirm opens on the way in and must not be decided again, got %+v", gate)
	}
	if gate := GateClosing(running("plain"), flow); gate != nil {
		t.Errorf("a stage with no gate owes no decision, got %+v", gate)
	}

	// And nothing is owed while no stage is running: the question is about a
	// stage that is about to close.
	waiting := running("reviewed")
	waiting.Status = StatusAwaitingGate
	if gate := GateClosing(waiting, flow); gate != nil {
		t.Errorf("a task that is not running owes no closing decision, got %+v", gate)
	}
}
