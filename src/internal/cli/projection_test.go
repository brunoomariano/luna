package cli

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/brunoomariano/luna/src/internal/fsm"
	"github.com/brunoomariano/luna/src/internal/registry"
)

// TestTheRegistryLearnsWhereATaskIs is what ADR-0054 asked for and nothing did.
//
// Three of its four "what moves" bullets were written by no production caller:
// Luna read the registry and never told it anything, so a task it had driven
// through eight stages looked untouched from another checkout.
func TestTheRegistryLearnsWhereATaskIs(t *testing.T) {
	reg := &fakeRegistry{task: registry.Task{ID: "LUNA-1", Status: registry.StatusOpen}}

	project := projectWith(reg)
	if project == nil {
		t.Fatal("a project with a registry got no projection")
	}

	err := project(context.Background(), fsm.TaskState{
		ID: "LUNA-1", Status: fsm.StatusRunning, Stage: "build",
	})
	if err != nil {
		t.Fatalf("projecting: %v", err)
	}

	if reg.movedTo != registry.StatusInProgress {
		t.Errorf("the registry was moved to %q, want in_progress", reg.movedTo)
	}
	if len(reg.stages) != 1 || reg.stages[0] != "build" {
		t.Errorf("the stage did not reach the registry, got %q", reg.stages)
	}
}

// TestAStatusThatDidNotChangeIsNotWritten covers the guard's cost.
//
// `Move` is guarded and has no unguarded sibling (ADR-0054), so calling it when
// nothing moved spends a `bd` invocation per pass of the lead's loop to write
// what is already there.
func TestAStatusThatDidNotChangeIsNotWritten(t *testing.T) {
	reg := &fakeRegistry{task: registry.Task{ID: "LUNA-1", Status: registry.StatusInProgress}}

	err := projectWith(reg)(context.Background(), fsm.TaskState{
		ID: "LUNA-1", Status: fsm.StatusRunning, Stage: "build",
	})
	if err != nil {
		t.Fatalf("projecting: %v", err)
	}

	if reg.movedTo != "" {
		t.Errorf("a status that had not changed was written anyway: %q", reg.movedTo)
	}
}

// TestEveryLunaStatusProjectsOntoOneBeadsStatus is the map between two
// vocabularies that are deliberately kept apart (ADR-0054).
//
// It is lossy in one direction and that is correct: beads answers "does anyone
// need to look at this", not "which of six states is it in".
func TestEveryLunaStatusProjectsOntoOneBeadsStatus(t *testing.T) {
	want := map[fsm.Status]registry.Status{
		fsm.StatusReady:        registry.StatusOpen,
		fsm.StatusRunning:      registry.StatusInProgress,
		fsm.StatusStageDone:    registry.StatusInProgress,
		fsm.StatusBlocked:      registry.StatusBlocked,
		fsm.StatusAwaitingGate: registry.StatusBlocked,
		fsm.StatusDone:         registry.StatusClosed,
		fsm.StatusAbandoned:    registry.StatusClosed,
	}

	for status, expected := range want {
		if got := registryStatus(status); got != expected {
			t.Errorf("%q projects to %q, want %q", status, got, expected)
		}
	}

	// Every Luna status is covered: one added without a mapping would silently
	// land in the default and report a task as running when it is not.
	for _, status := range []fsm.Status{
		fsm.StatusReady, fsm.StatusRunning, fsm.StatusStageDone,
		fsm.StatusAwaitingGate, fsm.StatusBlocked, fsm.StatusDone, fsm.StatusAbandoned,
	} {
		if _, ok := want[status]; !ok {
			t.Errorf("fsm.%s has no declared projection", status)
		}
	}
}

// TestAWaitingTaskLooksBlockedFromAnotherCheckout is the mapping worth arguing
// about, so it is pinned.
//
// A gate is a planned pause rather than an anomaly. But from another checkout the
// useful question is "does this need me", and a task waiting on a person answers
// yes exactly as a blocked one does — which is the half of INV-core-12 the
// registry exists to serve.
func TestAWaitingTaskLooksBlockedFromAnotherCheckout(t *testing.T) {
	if got := registryStatus(fsm.StatusAwaitingGate); got != registry.StatusBlocked {
		t.Errorf("a task waiting at a gate projects as %q, and `luna stuck` would not find it", got)
	}
}

// TestWithNoRegistryThereIsNothingToProject covers the project that never
// adopted one.
func TestWithNoRegistryThereIsNothingToProject(t *testing.T) {
	if projectWith(nil) != nil {
		t.Error("a project with no registry was given a projection anyway")
	}
}

// TestAFailedProjectionIsReportedAndNotFatal is the direction that matters.
//
// A projection that is briefly stale is a projection; a state that is briefly
// stale would be a bug. The lead reports and carries on (ADR-0065) — the same
// shape the worktree cleanup and the branch landing already follow.
func TestAFailedProjectionIsReportedAndNotFatal(t *testing.T) {
	h := newHarness(t)
	h.env.Registry = &fakeRegistry{
		task:    registry.Task{ID: "LUNA-1", Status: registry.StatusOpen},
		moveErr: errors.New("beads is not answering"),
	}
	h.mustRun(t, "task", "new", "LUNA-1")
	h.mustRun(t, "autonomy", "LUNA-1", "10")

	// The run has to finish: the work happened, and a registry that would not
	// take the news is a smaller fact than that.
	out := h.mustRun(t, "run", "LUNA-1", "--dry-run")
	if !strings.Contains(out, "finished") {
		t.Errorf("a failed projection stopped the task:\n%s", out)
	}

	// Reported rather than swallowed: a projection nobody can see failing is how
	// a registry drifts from the log without anyone noticing.
	if !strings.Contains(h.errOut.String(), "registry was not updated") {
		t.Errorf("the failure was not reported: %q", h.errOut.String())
	}
}
