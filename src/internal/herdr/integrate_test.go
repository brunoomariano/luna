package herdr

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/brunoomariano/luna/src/internal/fsm"
	"github.com/brunoomariano/luna/src/internal/node"
)

// mergingStage is the mechanical stage that closes a task's work: it produces
// the artifact that means "this is in the repository now".
func mergingStage() fsm.Stage {
	return fsm.Stage{ID: "commit", Produces: []fsm.Artifact{"commit_sha"}}
}

// TestTheWorkIsIntegratedWhenTheTaskCloses is the merge reaching the real path.
//
// Every stage before this one hands on through its commit and its base
// (INV-core-6); nothing needs merging until the work has to stop being a branch
// and become part of the repository.
func TestTheWorkIsIntegratedWhenTheTaskCloses(t *testing.T) {
	herdr := &fakeHerdr{settlesAt: StatusIdle}

	var merged string
	node := &Node{
		Runner: herdr,
		Roles:  fixedRole("claude"),
		Prove:  herdr.proving(),
		Merge: func(_ context.Context, commit, _ string) (node.Merge, error) {
			merged = commit
			return node.Merge{Verdict: node.MergeReady, Commit: "integrated"}, nil
		},
	}

	state := fsm.NewTaskState("LUNA-1", fsm.KindFeature)
	state.Base = "d34db33f"

	if _, err := node.Run(context.Background(), state, mergingStage()); err != nil {
		t.Fatalf("running the closing stage: %v", err)
	}

	if merged != "d34db33f" {
		t.Errorf("merged %q, want the commit the last stage delivered", merged)
	}
}

// TestAConflictStopsTheStageRatherThanResolvingItself is the guarantee the whole
// merge design turns on. A conflict is a verdict, and the stage blocks with the
// file named so somebody knows where to look (ADR-0053).
func TestAConflictStopsTheStageRatherThanResolvingItself(t *testing.T) {
	herdr := &fakeHerdr{settlesAt: StatusIdle}
	conductor := &Node{
		Runner: herdr,
		Roles:  fixedRole("claude"),
		Prove:  herdr.proving(),
		Merge: func(context.Context, string, string) (node.Merge, error) {
			return node.Merge{
				Verdict:   node.MergeBlocked,
				Conflicts: []string{"runner.go"},
				Detail:    "merge conflict on runner.go",
			}, nil
		},
	}

	state := fsm.NewTaskState("LUNA-1", fsm.KindFeature)
	state.Base = "d34db33f"

	_, err := conductor.Run(context.Background(), state, mergingStage())
	if err == nil {
		t.Fatal("a conflicting merge closed the stage")
	}
	if !strings.Contains(err.Error(), "runner.go") {
		t.Errorf("the failure does not name the conflict: %v", err)
	}
}

// TestAStageThatIntegratesNothingIsNotMerged. Only the stage that produces the
// integration artifact merges; `setup` produces a worktree and has nothing to
// bring back.
func TestAStageThatIntegratesNothingIsNotMerged(t *testing.T) {
	herdr := &fakeHerdr{settlesAt: StatusIdle}

	calls := 0
	conductor := &Node{
		Runner: herdr,
		Roles:  fixedRole("claude"),
		Prove:  herdr.proving(),
		Merge: func(context.Context, string, string) (node.Merge, error) {
			calls++
			return node.Merge{Verdict: node.MergeReady}, nil
		},
	}

	state := fsm.NewTaskState("LUNA-1", fsm.KindFeature)
	state.Base = "d34db33f"
	setup := fsm.Stage{ID: "setup", Produces: []fsm.Artifact{"worktree"}}

	if _, err := conductor.Run(context.Background(), state, setup); err != nil {
		t.Fatalf("running setup: %v", err)
	}
	if calls != 0 {
		t.Errorf("a stage with nothing to integrate merged %d time(s)", calls)
	}
}

// TestATaskWithNoBaseHasNothingToMerge covers the first stage of the first task:
// nothing has been delivered, so there is no branch to bring back.
func TestATaskWithNoBaseHasNothingToMerge(t *testing.T) {
	herdr := &fakeHerdr{settlesAt: StatusIdle}

	calls := 0
	conductor := &Node{
		Runner: herdr,
		Roles:  fixedRole("claude"),
		Prove:  herdr.proving(),
		Merge: func(context.Context, string, string) (node.Merge, error) {
			calls++
			return node.Merge{Verdict: node.MergeReady}, nil
		},
	}

	if _, err := conductor.Run(context.Background(), fsm.NewTaskState("LUNA-1", ""), mergingStage()); err != nil {
		t.Fatalf("running: %v", err)
	}
	if calls != 0 {
		t.Errorf("a task that has delivered nothing merged %d time(s)", calls)
	}
}

// TestWithNoMergerTheStageStillRuns. A dry run configures none, and the flow has
// to be exercisable without touching a repository.
func TestWithNoMergerTheStageStillRuns(t *testing.T) {
	herdr := &fakeHerdr{settlesAt: StatusIdle}
	conductor := &Node{Runner: herdr, Roles: fixedRole("claude"), Prove: herdr.proving()}

	state := fsm.NewTaskState("LUNA-1", fsm.KindFeature)
	state.Base = "d34db33f"

	if _, err := conductor.Run(context.Background(), state, mergingStage()); err != nil {
		t.Fatalf("a node with no merger failed the stage: %v", err)
	}
}

// TestAMergerThatBreaksIsReported. A merge that could not be attempted is not a
// conflict — it is the machinery failing, and it must not be reported as the
// work being wrong.
func TestAMergerThatBreaksIsReported(t *testing.T) {
	herdr := &fakeHerdr{settlesAt: StatusIdle}
	conductor := &Node{
		Runner: herdr,
		Roles:  fixedRole("claude"),
		Prove:  herdr.proving(),
		Merge: func(context.Context, string, string) (node.Merge, error) {
			return node.Merge{}, errors.New("the repository vanished")
		},
	}

	state := fsm.NewTaskState("LUNA-1", fsm.KindFeature)
	state.Base = "d34db33f"

	_, err := conductor.Run(context.Background(), state, mergingStage())
	if err == nil {
		t.Fatal("a merger that broke was treated as a clean integration")
	}
	if !strings.Contains(err.Error(), "vanished") {
		t.Errorf("the error does not carry what went wrong: %v", err)
	}
}
