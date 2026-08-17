package cli

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/brunoomariano/luna/src/internal/fsm"
	"github.com/brunoomariano/luna/src/internal/registry"
)

// fakeRegistry stands in for beads. Named rather than an inline closure because
// several tests need the same shape, and one of them needs it to fail.
//
// A pointer receiver because creating records what it was asked: the claim that
// the statement of work reaches the registry is about what was *sent*, not about
// what comes back.
type fakeRegistry struct {
	tasks []registry.Task
	err   error

	// created is the id this registry hands back, and work is what it was asked
	// to create — recorded for the tests that assert on the statement of work.
	created string
	work    registry.Work

	// task is what Task returns when a test adopts one, and taskErr is how it
	// fails to.
	task    registry.Task
	taskErr error

	// What was projected into it: the last status it was moved to, and every
	// stage it was told about. Recorded rather than asserted on a mock, because
	// the claim these tests make is about what Luna *sent* (ADR-0065).
	movedTo registry.Status
	stages  []string

	// moveErr and stageErr are how a projection fails.
	moveErr  error
	stageErr error
}

func (f *fakeRegistry) Move(_ context.Context, _ string, _, to registry.Status) error {
	if f.moveErr != nil {
		return f.moveErr
	}
	f.movedTo = to
	f.task.Status = to
	return nil
}

func (f *fakeRegistry) EnterStage(_ context.Context, _, stage string) error {
	if f.stageErr != nil {
		return f.stageErr
	}
	f.stages = append(f.stages, stage)
	return nil
}

func (f *fakeRegistry) Blocked(context.Context) ([]registry.Task, error) {
	return f.tasks, f.err
}

func (f *fakeRegistry) Create(_ context.Context, work registry.Work) (string, error) {
	f.work = work
	return f.created, f.err
}

func (f *fakeRegistry) Task(context.Context, string) (registry.Task, error) {
	return f.task, f.taskErr
}

// TestStuckSeesWhatIsBlockedInAnotherCheckout is the registry earning its place.
//
// The local log knows this repository. A task blocked in another checkout is
// invisible to it, and finding that out was the reason the registry became
// central (ADR-0054, INV-core-12).
func TestStuckSeesWhatIsBlockedInAnotherCheckout(t *testing.T) {
	h := newHarness(t)
	h.env.Registry = &fakeRegistry{tasks: []registry.Task{
		{ID: "OTHER-1", Status: registry.StatusBlocked, Labels: []string{"luna:stage:verify"}},
	}}

	out := h.mustRun(t, "stuck", "--for", "1ns")

	if !strings.Contains(out, "OTHER-1") {
		t.Errorf("a task blocked elsewhere was not reported:\n%s", out)
	}
	if !strings.Contains(out, "verify") {
		t.Errorf("the stage did not survive the query:\n%s", out)
	}
}

// TestATaskTheLogKnowsIsNotReportedTwice. Where both know a task, the log knows
// more: it has an age and the recorded reason, while the registry has a status
// and a label.
func TestATaskTheLogKnowsIsNotReportedTwice(t *testing.T) {
	h := newHarness(t)
	blockAndAge(t, h, "LUNA-1", "merge conflict on runner.go", 3*time.Hour)

	h.env.Registry = &fakeRegistry{tasks: []registry.Task{
		{ID: "LUNA-1", Status: registry.StatusBlocked},
	}}

	out := h.mustRun(t, "stuck")

	if strings.Count(out, "LUNA-1") != 1 {
		t.Errorf("the task was listed twice:\n%s", out)
	}
	// And it is the local entry that survived — the one that says why and for
	// how long.
	if !strings.Contains(out, "runner.go") || !strings.Contains(out, "3h") {
		t.Errorf("the registry's thinner entry replaced the log's:\n%s", out)
	}
}

// TestARegistryThatIsDownDoesNotStopTheWatchdog. A watchdog that stops watching
// because a tracker is unreachable stops watching exactly when something is
// wrong.
func TestARegistryThatIsDownDoesNotStopTheWatchdog(t *testing.T) {
	h := newHarness(t)
	blockAndAge(t, h, "LUNA-1", "merge conflict on runner.go", 3*time.Hour)
	h.env.Registry = &fakeRegistry{err: errors.New("bd: database is locked")}

	out := h.mustRun(t, "stuck")

	if !strings.Contains(out, "LUNA-1") {
		t.Errorf("the local listing was lost when the registry failed:\n%s", out)
	}
	if !strings.Contains(h.errOut.String(), "database is locked") {
		t.Errorf("the registry's failure was swallowed: %q", h.errOut.String())
	}
}

// TestWithNoRegistryTheLocalListingStillWorks. Luna runs in a project that has
// not adopted beads, and every cross-checkout question answers "nothing" there.
func TestWithNoRegistryTheLocalListingStillWorks(t *testing.T) {
	h := newHarness(t)
	blockAndAge(t, h, "LUNA-1", "merge conflict", 3*time.Hour)
	h.env.Registry = nil

	if out := h.mustRun(t, "stuck"); !strings.Contains(out, "LUNA-1") {
		t.Errorf("the listing broke without a registry:\n%s", out)
	}
}

// TestFlowCheckAuditsTheContract is the static check reaching the command whose
// name says it checks the flow.
//
// The three audits were written with the engine and called from nowhere but
// their own tests: the flow was verified in Luna's test suite and never by a
// project against its own flow (ADR-0058).
func TestFlowCheckAuditsTheContract(t *testing.T) {
	h := newHarness(t)

	out := h.mustRun(t, "flow", "check")

	if !strings.Contains(out, "the contract holds") {
		t.Errorf("the shipped flow was not reported as sound:\n%s", out)
	}
}

// TestABrokenFlowIsReportedByName exercises the reporting path with a flow that
// genuinely does not hold: a stage requiring an artifact nothing produces.
func TestABrokenFlowIsReportedByName(t *testing.T) {
	h := newHarness(t)

	broken := []fsm.Stage{{
		ID:       "build",
		Role:     "implementer",
		Requires: []fsm.Artifact{"a_thing_nobody_makes"},
		Produces: []fsm.Artifact{"code"},
	}}

	reportFlowGaps(h.env, broken)

	out := h.out.String()
	if !strings.Contains(out, "a_thing_nobody_makes") {
		t.Errorf("the missing input was not named:\n%s", out)
	}
	if strings.Contains(out, "the contract holds") {
		t.Errorf("a broken flow was reported as sound:\n%s", out)
	}
}

// TestAStageNameThatCrowdsOutTheTaskIdIsReported covers the third audit.
//
// An agent's name is herdr-wide and bounded, and it carries both the task and
// the stage — so a long stage id silently steals characters from the task id
// until two tasks collide. The check exists to say so at build time rather than
// let it truncate (ADR-0058).
func TestAStageNameThatCrowdsOutTheTaskIdIsReported(t *testing.T) {
	h := newHarness(t)

	long := []fsm.Stage{{
		ID:       fsm.StageID(strings.Repeat("verylongstagename", 3)),
		Role:     "implementer",
		Requires: []fsm.Artifact{fsm.TaskID},
		Produces: []fsm.Artifact{"code"},
	}}

	reportFlowGaps(h.env, long)

	if out := h.out.String(); !strings.Contains(out, "characters for a task id") {
		t.Errorf("an overlong stage id was not reported:\n%s", out)
	}
}

// TestStatusMarksTheStagesAlreadyWalked. The marks are the whole output, so each
// of the three has to mean what it says.
func TestStatusMarksTheStagesAlreadyWalked(t *testing.T) {
	h := newHarness(t)
	h.mustRun(t, "task", "new", "LUNA-1", "--kind", "feature", "--profile", "nightly")

	// Walk one full stage so there is something behind the task as well as ahead.
	first := mustOrderFrom(t, h, "LUNA-1")
	enterStage(t, h, "LUNA-1")
	h.mustRun(t, "done", "LUNA-1",
		"--delivered", strings.Join(artifactNames(first.Produces), ","),
		"--commit", "c0ffee")
	enterStage(t, h, "LUNA-1")

	raw := h.mustRun(t, "status", "LUNA-1", "--json")
	var report StatusReport
	if err := json.Unmarshal([]byte(raw), &report); err != nil {
		t.Fatalf("status --json: %v", err)
	}

	seen := map[string]int{}
	for _, stage := range report.Stages {
		seen[stage.State]++
	}
	if seen["done"] == 0 {
		t.Errorf("no stage was marked done after one closed: %+v", report.Stages)
	}
	if seen["ahead"] == 0 {
		t.Errorf("no stage was marked ahead: %+v", report.Stages)
	}
	if seen["current"] != 1 {
		t.Errorf("got %d current stages, want exactly one", seen["current"])
	}
}

// TestAStageThatNeedsJudgementAndNamesNoRoleIsReported covers the second audit.
// The mechanical path is silent by nature: a stage that should have had a role
// runs, delivers nothing, and looks like it worked.
func TestAStageThatNeedsJudgementAndNamesNoRoleIsReported(t *testing.T) {
	h := newHarness(t)

	broken := []fsm.Stage{{
		ID:       "build",
		Requires: []fsm.Artifact{fsm.TaskID},
		Produces: []fsm.Artifact{"code"}, // judgement, and no role
	}}

	reportFlowGaps(h.env, broken)

	if out := h.out.String(); !strings.Contains(out, "names no role") {
		t.Errorf("a stage needing judgement with no role was not reported:\n%s", out)
	}
}
