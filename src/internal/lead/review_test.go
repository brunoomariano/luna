package lead

import (
	"context"
	"strings"
	"testing"

	"github.com/brunoomariano/luna/src/internal/fsm"
)

// reviewFlow is a build stage and a review stage that sends work back to it.
// Small on purpose: what is under test is the emitter, not the shipped flow.
func reviewFlow() []fsm.Stage {
	return []fsm.Stage{
		{
			ID:       "build",
			Role:     "implementer",
			Requires: []fsm.Artifact{fsm.TaskID},
			Produces: []fsm.Artifact{"code", "ci_green"},
		},
		{
			ID:   "code-review",
			Role: "reviewer",
			Review: &fsm.ReviewSpec{
				SendsBackTo: "build",
				Invalidates: []fsm.Artifact{"ci_green"},
			},
			Requires:         []fsm.Artifact{"code", "ci_green"},
			ProducesForHuman: []fsm.Artifact{"review_report"},
		},
	}
}

// reportingNode delivers each stage's contract, and hands the review stage's
// report back as the evidence's detail — which is where delivered content
// travels (the gate payload reads it from the same place).
type reportingNode struct{ report string }

func (n reportingNode) Run(_ context.Context, _ fsm.TaskState, stage fsm.Stage) (Result, error) {
	owed := append(append([]fsm.Artifact{}, stage.Produces...), stage.ProducesForHuman...)

	evidence := map[fsm.Artifact]fsm.Evidence{}
	for _, artifact := range owed {
		e := fsm.Exists(0)
		if artifact == "review_report" {
			e.Detail = n.report
		}
		evidence[artifact] = e
	}
	return Result{Delivered: owed, Evidence: evidence}, nil
}

// runReview drives a task through build and review with the given report, and
// returns where it ended up.
func runReview(t *testing.T, report string) fsm.TaskState {
	t.Helper()

	s := newStore(t)
	if err := s.AppendAction("LUNA-1", fsm.TaskCreated{
		Kind:    fsm.KindFeature,
		Profile: fsm.ProfileNightly,
		Flow:    fsm.Fingerprint(reviewFlow()),
	}); err != nil {
		t.Fatalf("opening the task: %v", err)
	}

	conductor := &Lead{Store: s, Node: reportingNode{report: report}, Flow: reviewFlow()}

	state, err := conductor.Run(context.Background(), "LUNA-1")
	if err != nil {
		t.Fatalf("running: %v", err)
	}
	return state
}

// TestABlockingFindingSendsTheWorkBack is B7 working, and the reason the loop
// ceilings existed with nothing able to reach them.
//
// Before this, `ReviewFinding` was handled by the reducer, serialised by the
// codec, and produced by nothing — so a `[BLOCKING]` finding had no effect at
// all.
func TestABlockingFindingSendsTheWorkBack(t *testing.T) {
	state := runReview(t, "- [BLOCKING] B1: the failure path has no test")

	if state.Stage != "build" {
		t.Errorf("stage = %q, want build — a blocking finding returns the work", state.Stage)
	}
	if state.Loop.Rounds == 0 {
		t.Error("the loop counter did not move, so the ceilings can never fire")
	}
	// The green attested to code that is going to change.
	if state.Context.Artifacts["ci_green"] {
		t.Error("ci_green survived a finding that sent the work back")
	}
}

// TestTheLogSaysWhatSentItBack. "Something blocked" sends a person looking;
// "B1: the failure path has no test" tells them where.
func TestTheLogSaysWhatSentItBack(t *testing.T) {
	s := newStore(t)
	if err := s.AppendAction("LUNA-1", fsm.TaskCreated{
		Kind:    fsm.KindFeature,
		Profile: fsm.ProfileNightly,
		Flow:    fsm.Fingerprint(reviewFlow()),
	}); err != nil {
		t.Fatalf("opening: %v", err)
	}

	conductor := &Lead{
		Store: s,
		Node:  reportingNode{report: "- [BLOCKING] B1: the failure path has no test"},
		Flow:  reviewFlow(),
	}
	if _, err := conductor.Run(context.Background(), "LUNA-1"); err != nil {
		t.Fatalf("running: %v", err)
	}

	events, err := s.Events("LUNA-1")
	if err != nil {
		t.Fatalf("events: %v", err)
	}

	var found bool
	for _, e := range events {
		if e.Action != "ReviewFinding" {
			continue
		}
		found = true
		if !strings.Contains(e.Payload, "B1") {
			t.Errorf("the recorded finding does not name it: %s", e.Payload)
		}
	}
	if !found {
		t.Error("no ReviewFinding was recorded")
	}
}

// TestAReportWithNothingBlockingLetsTheTaskFinish is the other direction, and it
// is what stops the emitter from turning every review into a loop.
func TestAReportWithNothingBlockingLetsTheTaskFinish(t *testing.T) {
	state := runReview(t, "- [SHOULD-FIX] S1: extract this\n- [NIT] N1: spelling")

	if state.Status != fsm.StatusDone {
		t.Errorf("status = %q, want done — nothing blocking was reported", state.Status)
	}
}

// TestAReviewThatFoundNothingIsNotALoop. A reviewer that read the diff and had
// no concerns writes prose, and prose is a passing review.
func TestAReviewThatFoundNothingIsNotALoop(t *testing.T) {
	state := runReview(t, "I read the diff and the tests. No concerns.")

	if state.Status != fsm.StatusDone {
		t.Errorf("status = %q, want done", state.Status)
	}
	if state.Loop.Rounds != 0 {
		t.Errorf("rounds = %d, want 0 — nothing sent the work back", state.Loop.Rounds)
	}
}

// TestOnlyAReviewStageCanSendWorkBack is the write/review separation at the emitter rather than
// at the reducer. A build stage that produced something looking like a report
// must not be read as one — an implementer returning its own work is reviewing
// itself.
func TestOnlyAReviewStageCanSendWorkBack(t *testing.T) {
	// A flow whose only stage produces a report and reviews nothing.
	flow := []fsm.Stage{{
		ID:               "build",
		Role:             "implementer",
		Requires:         []fsm.Artifact{fsm.TaskID},
		Produces:         []fsm.Artifact{"code"},
		ProducesForHuman: []fsm.Artifact{"review_report"},
	}}

	s := newStore(t)
	if err := s.AppendAction("LUNA-1", fsm.TaskCreated{
		Kind:    fsm.KindFeature,
		Profile: fsm.ProfileNightly,
		Flow:    fsm.Fingerprint(flow),
	}); err != nil {
		t.Fatalf("opening: %v", err)
	}

	conductor := &Lead{
		Store: s,
		Node:  reportingNode{report: "- [BLOCKING] B1: I do not like my own work"},
		Flow:  flow,
	}

	state, err := conductor.Run(context.Background(), "LUNA-1")
	if err != nil {
		t.Fatalf("running: %v", err)
	}

	if state.Status != fsm.StatusDone {
		t.Errorf("status = %q — a stage that reviews nothing sent work back", state.Status)
	}
	if state.Loop.Rounds != 0 {
		t.Errorf("rounds = %d, want 0", state.Loop.Rounds)
	}
}

// TestTheLoopCeilingStopsAReviewThatNeverPasses. With the emitter wired, the
// loop ceilings finally have something that can reach them — so the
// review-forever case has to end somewhere.
func TestTheLoopCeilingStopsAReviewThatNeverPasses(t *testing.T) {
	// Every round blocks, so nothing converges.
	state := runReview(t, "- [BLOCKING] B1: still not right")

	if state.Loop.Rounds == 0 {
		t.Fatal("the loop never ran")
	}
	if state.Status == fsm.StatusRunning {
		t.Errorf("a review that never passes left the task running: %+v", state.Loop)
	}
}

// TestARoundThatDeliveredNothingNewIsCountedAsSuch is PRD node-0002 reaching the
// lead: the signal a round is judged by is the commit it delivered.
//
// The PRD's open question asked what to hash — artifacts, the worktree diff, the
// evidence — and worried about meaningless variation, a timestamp in a diff that
// never matches itself. The commit sidesteps that: the handoff *is* the commit
// , so two rounds delivering the same sha delivered the same work.
func TestARoundThatDeliveredNothingNewIsCountedAsSuch(t *testing.T) {
	s := newStore(t)
	if err := s.AppendAction("LUNA-1", fsm.TaskCreated{
		Kind:    fsm.KindFeature,
		Profile: fsm.ProfileNightly,
		Flow:    fsm.Fingerprint(reviewFlow()),
	}); err != nil {
		t.Fatalf("opening: %v", err)
	}

	conductor := &Lead{
		Store: s,
		Node:  reportingNode{report: "- [BLOCKING] B1: still not right"},
		Flow:  reviewFlow(),
	}
	if _, err := conductor.Run(context.Background(), "LUNA-1"); err != nil {
		t.Fatalf("running: %v", err)
	}

	state, err := s.Replay("LUNA-1", reviewFlow())
	if err != nil {
		t.Fatalf("replay: %v", err)
	}

	// The node delivers no commit, so every round's signal is empty — which the
	// reducer reads as "no comparison available" rather than as no progress. What
	// this asserts is that the loop still ended, through the round ceiling, and
	// that the detector did not fire on silence.
	if state.Loop.NoProgress != 0 {
		t.Errorf("NoProgress = %d — rounds that observed nothing were counted as "+
			"having made no progress", state.Loop.NoProgress)
	}
	if state.Status == fsm.StatusRunning {
		t.Error("the loop never ended")
	}
}

// TestAFindingWithNoIdStillNamesItself. The id is the handle for the
// conversation afterwards and a reviewer that omitted it has still
// found the defect — so the summary carries the text rather than nothing.
func TestAFindingWithNoIdStillNamesItself(t *testing.T) {
	got := summarise([]fsm.Finding{
		{Severity: fsm.SeverityBlocking, Text: "the failure path has no test"},
		{Severity: fsm.SeverityBlocking, ID: "B2", Text: "and this one leaks"},
		{Severity: fsm.SeverityNit, Text: "spelling"},
	})

	if !strings.Contains(got, "the failure path has no test") {
		t.Errorf("a finding with no id lost its text: %q", got)
	}
	if !strings.Contains(got, "B2: and this one leaks") {
		t.Errorf("a finding with an id lost its handle: %q", got)
	}
	if strings.Contains(got, "spelling") {
		t.Errorf("a non-blocking finding reached the summary of what blocked: %q", got)
	}
}

// runReviewWatching is runReview with somewhere for the warnings to go.
//
// Separate rather than a parameter on runReview, because every existing caller
// asserts on the state and none of them cares about the reporting channel.
func runReviewWatching(t *testing.T, report string, warn func(string, ...any)) fsm.TaskState {
	t.Helper()

	s := newStore(t)
	if err := s.AppendAction("LUNA-1", fsm.TaskCreated{
		Kind:    fsm.KindFeature,
		Profile: fsm.ProfileNightly,
		Flow:    fsm.Fingerprint(reviewFlow()),
	}); err != nil {
		t.Fatalf("opening the task: %v", err)
	}

	conductor := &Lead{Store: s, Node: reportingNode{report: report}, Flow: reviewFlow(), Warn: warn}

	state, err := conductor.Run(context.Background(), "LUNA-1")
	if err != nil {
		t.Fatalf("running: %v", err)
	}
	return state
}

// handoverNode delivers the review report the way the shipped flow does: the
// body goes to the store and the evidence carries the hash line, not the text.
//
// The plain reportingNode puts the report in the evidence's detail, which is how
// a report used to travel and no longer does — so every test built on it passed
// while production read a hash and found no findings.
type handoverNode struct{ report string }

func (n handoverNode) Run(_ context.Context, _ fsm.TaskState, stage fsm.Stage) (Result, error) {
	owed := append(append([]fsm.Artifact{}, stage.Produces...), stage.ProducesForHuman...)

	evidence := map[fsm.Artifact]fsm.Evidence{}
	for _, artifact := range owed {
		e := fsm.Exists(0)
		if artifact == "review_report" {
			e.Detail = "handed over to Luna, 7ce9c924d46f"
		}
		evidence[artifact] = e
	}
	return Result{Delivered: owed, Evidence: evidence}, nil
}

// runReviewStoring drives a review whose report is in the store rather than in
// the evidence, which is what the shipped flow does.
func runReviewStoring(t *testing.T, report string) fsm.TaskState {
	t.Helper()

	s := newStore(t)
	if err := s.AppendAction("LUNA-1", fsm.TaskCreated{
		Kind:    fsm.KindFeature,
		Profile: fsm.ProfileNightly,
		Flow:    fsm.Fingerprint(reviewFlow()),
	}); err != nil {
		t.Fatalf("opening the task: %v", err)
	}

	conductor := &Lead{
		Store: s, Node: handoverNode{report: report}, Flow: reviewFlow(),
		Artifact: func(_, artifact string) (string, bool) {
			return report, artifact == "review_report"
		},
	}

	state, err := conductor.Run(context.Background(), "LUNA-1")
	if err != nil {
		t.Fatalf("running: %v", err)
	}
	return state
}
