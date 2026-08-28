package cli

import (
	"strings"
	"testing"

	"github.com/brunoomariano/luna/src/internal/node"
)

// standingIn is a harness whose working directory says what the identity says.
func standingIn(t *testing.T, id node.Identity) *harness {
	t.Helper()

	h := newHarness(t)
	h.env.Where = func() (node.Identity, error) { return id, nil }
	return h
}

// inAStageWorktree is what Luna's own checkout of a stage looks like.
func inAStageWorktree(t *testing.T, task, stage string) node.Identity {
	t.Helper()

	repo := t.TempDir()
	path, err := node.WorktreePath(repo, task, stage)
	if err != nil {
		t.Fatalf("resolving the worktree path: %v", err)
	}
	return node.Identity{
		Repo: repo, Worktree: path, Linked: true,
		Branch: "luna/" + task + "/" + stage, TaskID: task, Stage: stage,
	}
}

// TestWhereReadsTheTaskOutOfTheCheckout is the whole point: standing in a stage's
// worktree, nothing has to be typed.
func TestWhereReadsTheTaskOutOfTheCheckout(t *testing.T) {
	h := newHarness(t)
	id := inAStageWorktree(t, "LUNA-1", "forge")
	h.env.Where = func() (node.Identity, error) { return id, nil }

	out := h.mustRun(t, "where")

	for _, want := range []string{"LUNA-1", "forge", id.Repo} {
		if !strings.Contains(out, want) {
			t.Errorf("where does not report %q:\n%s", want, out)
		}
	}
}

// TestWhereSaysWhenTheCheckoutNamesNoTask. Luna runs in repositories people also
// work in normally, and the honest answer there is that there is nothing to infer.
func TestWhereSaysWhenTheCheckoutNamesNoTask(t *testing.T) {
	h := standingIn(t, node.Identity{Repo: "/repo", Worktree: "/repo", Branch: "main"})

	out := h.mustRun(t, "where")

	if !strings.Contains(out, "names no task") {
		t.Errorf("want it to say there is nothing to infer:\n%s", out)
	}
}

// TestACommandTakesTheTaskFromTheDirectory covers the fallback every id-taking
// command now shares.
func TestACommandTakesTheTaskFromTheDirectory(t *testing.T) {
	h := newHarness(t)
	h.mustRun(t, "task", "new", "LUNA-9", "--kind", "chore")
	h.env.Where = func() (node.Identity, error) {
		return inAStageWorktree(t, "LUNA-9", "setup"), nil
	}

	out := h.mustRun(t, "status")

	if !strings.Contains(out, "LUNA-9") {
		t.Errorf("status did not take the task from the directory:\n%s", out)
	}
}

// TestAnArgumentBeatsTheDirectory. Naming a task means that task, even standing
// somewhere else — the inference removes typing, it does not override a person.
func TestAnArgumentBeatsTheDirectory(t *testing.T) {
	h := newHarness(t)
	h.mustRun(t, "task", "new", "TYPED-1", "--kind", "chore")
	h.env.Where = func() (node.Identity, error) {
		return inAStageWorktree(t, "INFERRED-1", "forge"), nil
	}

	out := h.mustRun(t, "status", "TYPED-1")

	if !strings.Contains(out, "TYPED-1") || strings.Contains(out, "INFERRED-1") {
		t.Errorf("the typed id did not win:\n%s", out)
	}
}

// TestAFlagIsNotATaskId. `luna status --json` asks about this task as json, not
// about a task called `--json`.
func TestAFlagIsNotATaskId(t *testing.T) {
	h := newHarness(t)
	h.mustRun(t, "task", "new", "LUNA-8", "--kind", "chore")
	h.env.Where = func() (node.Identity, error) {
		return inAStageWorktree(t, "LUNA-8", "setup"), nil
	}

	out := h.mustRun(t, "status", "--json")

	if !strings.Contains(out, "LUNA-8") {
		t.Errorf("a flag was read as the task id:\n%s", out)
	}
}

// TestAnAmbiguousCheckoutIsRefusedRatherThanGuessed.
//
// Inferring an id is a convenience, and a convenience that picks between two
// disagreeing sources is worse than asking: the person is standing somewhere that
// says two things, and only they know which moved.
func TestAnAmbiguousCheckoutIsRefusedRatherThanGuessed(t *testing.T) {
	h := standingIn(t, node.Identity{
		Repo: "/repo", Worktree: "/somewhere/else", Linked: true,
		Branch: "luna/LUNA-3/plan", TaskID: "LUNA-3", Stage: "plan",
	})

	err := h.run(t, "status")

	if err == nil {
		t.Fatal("an ambiguous checkout was resolved anyway")
	}
	if !strings.Contains(err.Error(), "ambiguous") {
		t.Errorf("the refusal does not say what is wrong: %v", err)
	}
}

// TestWithoutAResolverACommandStillSaysWhatIsMissing. A build that cannot tell
// where it is has to name both halves — nothing typed, and nothing inferable.
func TestWithoutAResolverACommandStillSaysWhatIsMissing(t *testing.T) {
	h := newHarness(t)
	h.env.Where = nil

	err := h.run(t, "status")

	if err == nil {
		t.Fatal("a command with no id and no resolver ran anyway")
	}
	if !strings.Contains(err.Error(), "no task id") {
		t.Errorf("the refusal does not name what is missing: %v", err)
	}
}
