package fsm

import (
	"errors"
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
			next, err = Reduce(next, Complete{
				Delivered: append(stage.Produces, stage.ProducesForHuman...),
			})
			if err != nil {
				t.Fatalf("completing %q: %v", next.Stage, err)
			}
		}
		state = next
	}

	for _, a := range produced {
		state.Context.Artifacts[a] = true
	}
	return state
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

	if state.Stage != "discovery" {
		t.Errorf("want discovery, got %q", state.Stage)
	}
	// discovery carries the confirm-repos gate, so under the default profile the
	// task suspends rather than running.
	if state.Status != StatusAwaitingGate {
		t.Errorf("discovery has a gate; want awaiting_gate, got %q", state.Status)
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
	state, err = Reduce(state, Complete{Delivered: []Artifact{"repos"}})
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
	state := atStage(t, KindDocs, "commit")
	stage := stageIn(DefaultFlow(), "commit")

	state, err := Reduce(state, Complete{Delivered: stage.Produces})
	if err != nil {
		t.Fatalf("completing commit: %v", err)
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

	state, err := Reduce(state, Complete{
		Delivered: stage.Produces,
		Evidence:  map[Artifact]string{"tests_green": "go test ./... → ok"},
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	for _, a := range stage.Produces {
		if !state.Context.HasArtifact(a) {
			t.Errorf("%q should be in the context after the stage closed", a)
		}
	}
	if state.Evidence["tests_green"] == "" {
		t.Error("the evidence for a delivery must be kept (ADR-0024)")
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

	if state.Status != StatusBlocked {
		t.Errorf("want blocked on a partial delivery, got %q", state.Status)
	}
	if state.Context.HasArtifact("code") {
		t.Error("nothing enters the context when the stage does not close")
	}
}

// TestCompleteRequiresTheHumanReport covers scenario G3 — INV-core-11.
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

	if state.Status != StatusBlocked {
		t.Errorf("want blocked without the audit report, got %q", state.Status)
	}
}

// TestAuditReportDoesNotEnterTheFlowContext covers the other half of G3.
//
// The report is required on exit, but it is not an input: putting it in the
// context would let it satisfy some stage's requires, which is the very thing
// ADR-0021 separates the two fields to prevent.
func TestAuditReportDoesNotEnterTheFlowContext(t *testing.T) {
	state := atStage(t, KindFeature, "verify")
	stage := stageIn(DefaultFlow(), "verify")

	state, err := Reduce(state, Complete{
		Delivered: append(stage.Produces, stage.ProducesForHuman...),
	})
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

// TestFailRetriesTwiceThenBlocks covers scenario H1 — ADR-0011.
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
	state, err := Reduce(readyTask(KindFeature), Advance{Flow: DefaultFlow()})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if state.Status != StatusAwaitingGate {
		t.Fatalf("expected a gate on discovery, got %q", state.Status)
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

// TestGateAdjustReplacesThePayload covers scenario H3 — ADR-0022.
//
// The human edits the artifact and the edited version is what carries on. Without
// this the review would happen outside the system, leaving the handoff describing
// something other than what the next stage consumed.
func TestGateAdjustReplacesThePayload(t *testing.T) {
	state := readyTask(KindFeature)
	state.Status = StatusAwaitingGate
	state.Stage = "spec"
	state.Gate = &PendingGate{
		Kind:     GateReviewArtifact,
		Stage:    "spec",
		Artifact: "contract",
		Payload:  "the generated contract",
	}

	state, err := Reduce(state, GateAdjust{Payload: "the contract a human fixed"})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if state.Status != StatusRunning {
		t.Errorf("want running after an adjustment, got %q", state.Status)
	}
	if state.Evidence["contract"] != "the contract a human fixed" {
		t.Errorf("the adjusted version is what carries on, got %q", state.Evidence["contract"])
	}
}

// TestGateRejectSendsTheStageBack covers scenario H4 — ADR-0022.
//
// A rejected artifact does not enter the context, and the stage that produced it
// runs again with the rejection in hand. Unlike the review rollback, there is no
// green to invalidate: this happens before the artifact was ever accepted.
func TestGateRejectSendsTheStageBack(t *testing.T) {
	state := readyTask(KindFeature)
	state.Status = StatusAwaitingGate
	state.Stage = "spec"
	state.Gate = &PendingGate{Kind: GateReviewArtifact, Stage: "spec", Artifact: "contract"}

	state, err := Reduce(state, GateReject{Reason: "the approach does not hold"})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if state.Status != StatusRunning {
		t.Errorf("want the stage running again, got %q", state.Status)
	}
	if state.Stage != "spec" {
		t.Errorf("the rejecting stage runs again; got %q", state.Stage)
	}
	if state.Context.HasArtifact("contract") {
		t.Error("a rejected artifact must not enter the context")
	}
}

// ── block I: review findings and loop ceilings ───────────────────────────────

// TestAlignedFindingInvalidatesTheGreen covers scenario I1 — ADR-0020.
//
// The check the whole ADR exists for: going back to build removes ci_green, so
// the stages downstream cannot be satisfied by a verification that ran against
// code which no longer exists.
func TestAlignedFindingInvalidatesTheGreen(t *testing.T) {
	state := atStage(t, KindFeature, "code-review")
	state.Context.Artifacts["ci_green"] = true

	state, err := Reduce(state, ReviewFinding{Aligned: true, Summary: "wrong boundary"})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if state.Stage != "build" {
		t.Errorf("an aligned finding goes back to build, got %q", state.Stage)
	}
	if state.Context.HasArtifact("ci_green") {
		t.Error("the green must be invalidated on the way back (ADR-0020)")
	}
}

// TestUnalignedFindingLeavesTheFlowAlone covers scenario I2.
//
// Out of scope becomes someone else's task. The flow carries on, and the green
// stays valid because the code did not change.
func TestUnalignedFindingLeavesTheFlowAlone(t *testing.T) {
	state := atStage(t, KindFeature, "code-review")
	state.Context.Artifacts["ci_green"] = true

	state, err := Reduce(state, ReviewFinding{Aligned: false, Summary: "unrelated debt"})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if state.Stage != "code-review" {
		t.Errorf("an unaligned finding does not move the task, got %q", state.Stage)
	}
	if !state.Context.HasArtifact("ci_green") {
		t.Error("the code did not change, so the green still holds")
	}
}

// TestFindingFromANonReviewStageIsRejected covers scenario I3.
//
// Only a review stage produces a finding. Accepting one from build would let the
// implementer send its own work back, which is the separation INV-core-7 exists
// to keep.
func TestFindingFromANonReviewStageIsRejected(t *testing.T) {
	state := atStage(t, KindFeature, "build")

	_, err := Reduce(state, ReviewFinding{Aligned: true, Summary: "self-review"})

	if !errors.Is(err, ErrIllegalTransition) {
		t.Errorf("want ErrIllegalTransition from a non-review stage, got %v", err)
	}
}

// TestLoopCeilingOpensAGateRatherThanBlocking covers scenario I4 — ADR-0023.
//
// Not converging is a decision to make, not a node failure. The distinction shows
// up here: a gate waits for a human with the history in view, a block reports an
// anomaly.
func TestLoopCeilingOpensAGateRatherThanBlocking(t *testing.T) {
	state := atStage(t, KindFeature, "code-review")
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

// TestOscillationIsCountedApartFromRounds covers scenario I5 — ADR-0023.
//
// Returning to a stage already visited in this loop is a distinct pathology from
// simply going round again: one counter for both would let a productive loop and
// a thrashing one hit the same limit.
func TestOscillationIsCountedApartFromRounds(t *testing.T) {
	state := atStage(t, KindFeature, "code-review")
	state.Context.Artifacts["ci_green"] = true
	state.Loop.Visited = []StageID{"build", "code-review"}

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
		Evidence: map[Artifact]string{},
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

	state, err := Reduce(state, Complete{Delivered: stage.Produces})
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
// history is the audit trail (INV-core-2).
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
// A task that halts without saying why is the silent failure INV-core-8 forbids,
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
		state := TaskState{Status: status, Evidence: map[Artifact]string{}}

		if _, err := Reduce(state, Block{Reason: "again"}); !errors.Is(err, ErrIllegalTransition) {
			t.Errorf("%s: want ErrIllegalTransition, got %v", status, err)
		}
	}
}

// TestAProfileThisBuildDoesNotKnow covers the forward-compatibility path.
//
// A log written by a newer version can name a profile this one has never heard
// of. The task still replays — treated as the cautious profile — and the surface
// can say so, which is what keeps someone from watching a nightly run stop at
// every gate with no explanation.
func TestAProfileThisBuildDoesNotKnow(t *testing.T) {
	future := Profile("paranoid")

	if future.KnownProfile() {
		t.Error("a profile this build does not list is not known to it")
	}
	if !future.WaitsFor(GateConfirm) {
		t.Error("an unknown profile must fall back to waiting, not to running free")
	}

	for _, known := range []Profile{ProfileInteractive, ProfileTurbo, ProfileNightly} {
		if !known.KnownProfile() {
			t.Errorf("%q is a shipped profile and must be known", known)
		}
	}
}

// TestParseProfileIsTheOneList covers the shared parser.
func TestParseProfileIsTheOneList(t *testing.T) {
	for _, name := range []string{"interactive", "turbo", "nightly"} {
		if _, err := ParseProfile(name); err != nil {
			t.Errorf("%q must parse: %v", name, err)
		}
	}

	if _, err := ParseProfile("nightl"); err == nil {
		t.Error("a typo must be rejected")
	}
}
