package cli

import (
	"errors"
	"strings"
	"testing"

	"github.com/brunoomariano/luna/src/internal/fsm"
	"github.com/brunoomariano/luna/src/internal/node"
)

// TestATaskInAnotherProjectCanBeActedOn is the half of centralisation that was
// missing.
//
// Every global listing prints `project/task` — `luna gates`, `luna stuck`, `luna
// task list`, `luna fleet report` — and nothing accepted it back. A person
// reading the panel had to work out which checkout a task belonged to and go
// there, and a task in a project with no checkout at all could never be touched
// again: the four a test run left behind sat in every listing for the life of the
// machine.
func TestATaskInAnotherProjectCanBeActedOn(t *testing.T) {
	h := newHarness(t)
	h.env.Store.Project = "here"

	// A task belonging to somewhere else entirely.
	elsewhere := h.env.GlobalStore.ForProject("app-23ff6664")
	if err := elsewhere.AppendAction("GHOST-1", fsm.TaskCreated{
		Kind: fsm.KindChore, Flow: fsm.Fingerprint(fsm.DefaultFlow()),
	}); err != nil {
		t.Fatalf("seeding the other project: %v", err)
	}

	// Unqualified, it is not this project's and is not found.
	if err := h.run(t, "task", "show", "GHOST-1"); err == nil {
		t.Error("a task from another project answered to a bare id")
	}

	// Qualified the way the listings print it, it is.
	out := h.mustRun(t, "task", "show", "app-23ff6664/GHOST-1")
	if !strings.Contains(out, "GHOST-1") {
		t.Errorf("the task was not found by the name every listing prints:\n%s", out)
	}

	// And it can be acted on, which is the point: a listing that shows what
	// nothing can reach is a panel, not a control.
	h.mustRun(t, "task", "abandon", "app-23ff6664/GHOST-1", "a ghost from a test run")
	after := h.mustRun(t, "task", "show", "app-23ff6664/GHOST-1")
	if !strings.Contains(after, "abandoned") {
		t.Errorf("the task in the other project was not abandoned:\n%s", after)
	}
}

// TestAProjectWithNoTaskIsRefused. `project/` names half a reference, and
// treating the empty half as a task id would look for a task called "".
func TestAProjectWithNoTaskIsRefused(t *testing.T) {
	h := newHarness(t)

	err := h.run(t, "task", "show", "app-23ff6664/")

	if !errors.Is(err, ErrUsage) {
		t.Errorf("a reference naming no task answered %v, want a usage error", err)
	}
}

// TestTheCheckoutNamesTheTaskWhenNobodyDoes is the other half.
//
// Luna derives the repository, the worktree, the branch and the task from the
// directory — `luna where` prints exactly that — and every command still asked
// for the id to be typed. Standing in a stage's worktree and being asked to name
// the task the branch already carries is the identity being derived and thrown
// away.
func TestTheCheckoutNamesTheTaskWhenNobodyDoes(t *testing.T) {
	h := newHarness(t)
	h.mustRun(t, "task", "new", "LUNA-1", "--kind", "chore")
	h.env.Where = func() (node.Identity, error) {
		return node.Identity{
			Repo:     "/repos/luna",
			Worktree: "/repos/wt-luna-LUNA-1-setup",
			Branch:   "luna/LUNA-1/setup",
			TaskID:   "LUNA-1",
			Stage:    "setup",
			Linked:   true,
		}, nil
	}

	out := h.mustRun(t, "task", "show")

	if !strings.Contains(out, "LUNA-1") {
		t.Errorf("the checkout names the task and the command still asked for it:\n%s", out)
	}
}

// TestAnAbandonedTaskLeavesThePanelEvenIfItCannotBeRead.
//
// A task whose flow changed under it stops replaying, and `task abandon` is its
// one way out — it appends without reading. The listings read by replaying, so
// they went on calling it unreadable and the person who had just ended it watched
// it sit in the panel unchanged, with nothing else to try.
func TestAnAbandonedTaskLeavesThePanelEvenIfItCannotBeRead(t *testing.T) {
	h := newHarness(t)
	h.env.Store.Project = "here"

	// A fingerprint no flow in this build has.
	if err := h.env.Store.AppendAction("GONE-1", fsm.TaskCreated{
		Kind: fsm.KindChore, Flow: "0123456789abcdef",
	}); err != nil {
		t.Fatalf("seeding: %v", err)
	}

	out := h.mustRun(t, "task", "list")
	if !strings.Contains(out, "unreadable") {
		t.Fatalf("a task nobody can replay must be reported, not dropped:\n%s", out)
	}

	h.mustRun(t, "task", "abandon", "GONE-1", "the flow it named is gone")

	out = h.mustRun(t, "task", "list")
	if strings.Contains(out, "GONE-1") {
		t.Errorf("the task was ended and the panel still lists it as active:\n%s", out)
	}

	report := h.mustRun(t, "fleet", "report")
	if strings.Contains(report, "no longer replay") {
		t.Errorf("the report still groups an ended task as unreadable:\n%s", report)
	}
}

// TestACheckoutOnSomeoneElsesBranchNamesNoTask. Inferring only works from a
// branch Luna made, and a repository somebody is working in normally is the
// ordinary case — so the refusal has to say what shape it was looking for.
func TestACheckoutOnSomeoneElsesBranchNamesNoTask(t *testing.T) {
	h := newHarness(t)
	h.env.Where = func() (node.Identity, error) {
		return node.Identity{
			Repo:     "/repos/luna",
			Worktree: "/repos/luna",
			Branch:   "main",
		}, nil
	}

	err := h.run(t, "task", "show")

	if !errors.Is(err, ErrUsage) {
		t.Fatalf("a checkout naming no task answered %v, want a usage error", err)
	}
	if !strings.Contains(err.Error(), "luna/<task>/<stage>") {
		t.Errorf("the refusal does not say what a checkout Luna made looks like: %v", err)
	}
}

// TestWhereSaysSoWhenItCannotTell. `luna where` exists to answer what the
// directory is, so a directory it cannot read has to be reported rather than
// printed as a blank listing that reads like "nothing here".
func TestWhereSaysSoWhenItCannotTell(t *testing.T) {
	h := newHarness(t)
	h.env.Where = func() (node.Identity, error) {
		return node.Identity{}, errors.New("not a git repository")
	}

	if err := h.run(t, "where"); err == nil {
		t.Error("a directory that could not be read printed a listing instead")
	}
}

// TestAnUnreachableLogIsNotAnEndedTask. Reading the last action is how an
// unreadable task is told apart from an ended one, and a read that fails has to
// answer "not ended" — calling it ended would hide a live task from every panel.
func TestAnUnreachableLogIsNotAnEndedTask(t *testing.T) {
	h := newHarness(t)
	if err := h.env.Store.Close(); err != nil {
		t.Fatalf("closing the store: %v", err)
	}

	if endedInTheLog(h.env.Store, "LUNA-1", errors.New("the flow changed")) {
		t.Error("a log that could not be read was reported as an ended task")
	}
}

// TestStatusAnswersWhereATaskStandsWithoutASecondCommand.
//
// It had the flow, the pack, the cost and the worktrees and could not say what
// the task was about or what it had delivered; `task show` had those and none of
// the rest. Asking "where does this stand" took two commands and neither
// answered it.
func TestStatusAnswersWhereATaskStandsWithoutASecondCommand(t *testing.T) {
	h := newHarness(t)
	h.mustRun(t, "task", "new", "LUNA-1", "--kind", "chore", "--simulated",
		"--about", "the thing that is missing")

	out := h.mustRun(t, "status", "LUNA-1")

	for _, want := range []string{"the thing that is missing", "produced", "task_id"} {
		if !strings.Contains(out, want) {
			t.Errorf("status does not carry %q, so it still takes two commands:\n%s", want, out)
		}
	}
}
