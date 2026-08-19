package herdr

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/brunoomariano/luna/src/internal/fsm"
)

// TestEachRoleGetsItsOwnWorktree is the write/review separation becoming a property of the
// filesystem rather than a line in a brief.
//
// A reviewer that shares a directory with the implementer it is reviewing can
// see uncommitted work, half-finished edits and build output — and a review of
// that is not a review of what was delivered.
func TestEachRoleGetsItsOwnWorktree(t *testing.T) {
	herdr := &fakeHerdr{settlesAt: StatusIdle}
	node := &Node{Runner: herdr, Roles: fixedRole("claude"), Prove: herdr.proving()}

	state := fsm.NewTaskState("LUNA-1", fsm.KindFeature)

	build := stageWithTests()
	review := fsm.Stage{ID: "review", Role: "reviewer", Produces: []fsm.Artifact{"verdict"}}

	if _, err := node.Run(context.Background(), state, build); err != nil {
		t.Fatalf("running build: %v", err)
	}
	if _, err := node.Run(context.Background(), state, review); err != nil {
		t.Fatalf("running review: %v", err)
	}

	if len(herdr.opened) != 2 {
		t.Fatalf("opened %d worktrees, want one per stage", len(herdr.opened))
	}
	if herdr.opened[0].Branch() == herdr.opened[1].Branch() {
		t.Errorf("both roles worked on %s — the separation the flow relies on "+
			"is the directory, and sharing it gives that away", herdr.opened[0].Branch())
	}
	if herdr.opened[0].Role != "implementer" || herdr.opened[1].Role != "reviewer" {
		t.Errorf("the worktrees do not carry their roles: %+v", herdr.opened)
	}
}

// TestTheWorktreeBranchesFromWhatTheLastStageDelivered is the handoff. The next
// agent starts from the artifact rather than from a description of it
// .
func TestTheWorktreeBranchesFromWhatTheLastStageDelivered(t *testing.T) {
	herdr := &fakeHerdr{settlesAt: StatusIdle}
	node := &Node{Runner: herdr, Roles: fixedRole("claude"), Prove: herdr.proving()}

	state := fsm.NewTaskState("LUNA-1", fsm.KindFeature)
	state.Base = "d34db33f"

	if _, err := node.Run(context.Background(), state, stageWithTests()); err != nil {
		t.Fatalf("running the stage: %v", err)
	}

	if herdr.opened[0].Base != "d34db33f" {
		t.Errorf("base = %q, want the previous stage's commit", herdr.opened[0].Base)
	}
}

// TestTheFirstStageBranchesFromTheRepository. A task that has delivered nothing
// has no base, and asking herdr to branch from "" would be asking for a ref that
// does not exist.
func TestTheFirstStageBranchesFromTheRepository(t *testing.T) {
	herdr := &fakeHerdr{settlesAt: StatusIdle}
	node := &Node{Runner: herdr, Roles: fixedRole("claude"), Prove: herdr.proving()}

	if _, err := node.Run(context.Background(), fsm.NewTaskState("LUNA-1", ""), stageWithTests()); err != nil {
		t.Fatalf("running the stage: %v", err)
	}

	if herdr.opened[0].Base != "" {
		t.Errorf("base = %q, want empty on a task that has delivered nothing", herdr.opened[0].Base)
	}
}

// TestTheWorktreeIsRemovedWhenTheStageEnds is the amendment swarm-forge's own
// history argued for: a role branch that is never reset accumulates divergence
// that "compounds at every hop", so feature N faces N-1 features of drift.
func TestTheWorktreeIsRemovedWhenTheStageEnds(t *testing.T) {
	herdr := &fakeHerdr{settlesAt: StatusIdle}
	node := &Node{Runner: herdr, Roles: fixedRole("claude"), Prove: herdr.proving()}

	if _, err := node.Run(context.Background(), fsm.NewTaskState("LUNA-1", ""), stageWithTests()); err != nil {
		t.Fatalf("running the stage: %v", err)
	}

	if len(herdr.closed) != 1 {
		t.Fatalf("closed %d worktrees, want the one the stage opened", len(herdr.closed))
	}
}

// TestAFailedStageStillRemovesItsWorktree. A stage that went wrong leaves a
// checkout behind exactly like one that went right, and the failure path is the
// one that runs when nobody is watching.
func TestAFailedStageStillRemovesItsWorktree(t *testing.T) {
	herdr := &fakeHerdr{settlesAt: StatusBlocked}
	node := &Node{Runner: herdr, Roles: fixedRole("claude"), Prove: herdr.proving()}

	_, _ = node.Run(context.Background(), fsm.NewTaskState("LUNA-1", ""), stageWithTests())

	if len(herdr.closed) != 1 {
		t.Errorf("a stage that did not settle cleanly left its worktree behind")
	}
}

// TestAWorktreeThatWillNotGoAwayDoesNotFailTheStage. By the time cleanup runs
// the work is committed, and turning "the stage delivered" into "the stage
// failed" because a directory would not go away loses the more important of the
// two. It is reported rather than swallowed.
func TestAWorktreeThatWillNotGoAwayDoesNotFailTheStage(t *testing.T) {
	herdr := &fakeHerdr{settlesAt: StatusIdle, closeErr: errors.New("device busy")}

	var warned []string
	node := &Node{
		Runner: herdr,
		Roles:  fixedRole("claude"),
		Prove:  herdr.proving(),
		Warn:   func(format string, args ...any) { warned = append(warned, format) },
	}

	result, err := node.Run(context.Background(), fsm.NewTaskState("LUNA-1", ""), stageWithTests())
	if err != nil {
		t.Fatalf("a worktree that would not be removed failed the stage: %v", err)
	}
	if len(result.Delivered) == 0 {
		t.Error("the delivery was lost")
	}
	if len(warned) != 1 {
		t.Errorf("the cleanup failure was swallowed rather than reported (%d warnings)", len(warned))
	}
}

// TestANodeWithNoReporterStillRuns. A warning channel is optional; a stage is
// not.
func TestANodeWithNoReporterStillRuns(t *testing.T) {
	herdr := &fakeHerdr{settlesAt: StatusIdle, closeErr: errors.New("device busy")}
	node := &Node{Runner: herdr, Roles: fixedRole("claude"), Prove: herdr.proving()}

	if _, err := node.Run(context.Background(), fsm.NewTaskState("LUNA-1", ""), stageWithTests()); err != nil {
		t.Fatalf("a node with no Warn failed the stage: %v", err)
	}
}

// TestAMechanicalStageWorksInTheTasksOwnWorktree. `setup` and `commit` name no
// role, so there is nobody to keep them apart from.
//
// This asserted `luna/LUNA-1/reviewer` until the real flow ran: the shape looked
// right and git refuses it, because the roleless branch of the same task then has
// to be a directory. The role is still in the name — it is joined with "-", and
// TestAMechanicalStageDoesNotCollideWithARoleBranch is what holds that.
func TestAMechanicalStageWorksInTheTasksOwnWorktree(t *testing.T) {
	spec := WorktreeSpec{TaskID: "LUNA-1"}

	if got := spec.Branch(); got != "luna/LUNA-1" {
		t.Errorf("branch = %q, want the task's own", got)
	}
	if got := (WorktreeSpec{TaskID: "LUNA-1", Role: "reviewer"}).Branch(); got != "luna/LUNA-1-reviewer" {
		t.Errorf("branch = %q, want it named by role", got)
	}
}

// TestClosingSendsTheWorkspaceAndForcesIt covers what actually reaches herdr.
//
// --force because the tree will not be clean: build output and anything the
// agent did not commit are still there, and that is precisely what must not
// survive. A polite removal would refuse and leave the checkout behind.
func TestClosingSendsTheWorkspaceAndForcesIt(t *testing.T) {
	server, path := newFakeServer(t)
	server.reply("worktree.remove", `{"id":"1","result":{"type":"worktree_removed"}}`)

	runner := NewRunner(dialFake(t, path), "/repo", time.Minute)
	if err := runner.CloseWorktree(context.Background(), Workspace{ID: "w1", Path: "/tmp/wt"}); err != nil {
		t.Fatalf("CloseWorktree: %v", err)
	}

	sent := server.sent("worktree.remove")
	if len(sent) != 1 {
		t.Fatalf("want one remove, got %d", len(sent))
	}
	params, err := json.Marshal(sent[0].Params)
	if err != nil {
		t.Fatalf("re-encoding the params: %v", err)
	}
	for _, want := range []string{`"workspace_id":"w1"`, `"force":true`} {
		if !strings.Contains(string(params), want) {
			t.Errorf("want %s in the params, got %s", want, params)
		}
	}
}

// TestClosingAWorkspaceThatWasNeverOpenedIsNotAnError. A stage that failed
// before herdr answered has no workspace id, and there is nothing to remove.
func TestClosingAWorkspaceThatWasNeverOpenedIsNotAnError(t *testing.T) {
	server, path := newFakeServer(t)
	runner := NewRunner(dialFake(t, path), "/repo", time.Minute)

	if err := runner.CloseWorktree(context.Background(), Workspace{}); err != nil {
		t.Fatalf("closing an empty workspace: %v", err)
	}
	if sent := server.sent("worktree.remove"); len(sent) != 0 {
		t.Errorf("a removal was sent for a workspace that was never opened: %v", sent)
	}
}

// TestARefusedRemovalIsReported. The stage does not fail on it, but it must not
// be silent either: a checkout left behind on every run eventually fills a disk,
// and the first anyone would hear of it is that.
func TestARefusedRemovalIsReported(t *testing.T) {
	server, path := newFakeServer(t)
	server.reply("worktree.remove",
		`{"id":"1","error":{"code":"worktree_busy","message":"the worktree is in use"}}`)

	runner := NewRunner(dialFake(t, path), "/repo", time.Minute)
	err := runner.CloseWorktree(context.Background(), Workspace{ID: "w1", Path: "/tmp/wt"})

	if err == nil {
		t.Fatal("a refused removal was reported as success")
	}
	if !strings.Contains(err.Error(), "/tmp/wt") {
		t.Errorf("the error does not say which worktree: %v", err)
	}
}

// TestTheBaseIsSentOnlyWhenThereIsOne covers the parameter herdr receives. An
// empty base would ask it to branch from a ref named "", where omitting the
// field means the repository's own head.
func TestTheBaseIsSentOnlyWhenThereIsOne(t *testing.T) {
	for _, tc := range []struct {
		name string
		base string
		want bool
	}{
		{"with a base", "d34db33f", true},
		{"without one", "", false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			server, path := newFakeServer(t)
			server.reply("worktree.create", worktreeReply)

			runner := NewRunner(dialFake(t, path), "/repo", time.Minute)
			_, err := runner.OpenWorktree(context.Background(), WorktreeSpec{
				TaskID: "LUNA-1",
				Role:   "implementer",
				Base:   tc.base,
			})
			if err != nil {
				t.Fatalf("OpenWorktree: %v", err)
			}

			sent := server.sent("worktree.create")
			if len(sent) != 1 {
				t.Fatalf("want one create, got %d", len(sent))
			}
			params, err := json.Marshal(sent[0].Params)
			if err != nil {
				t.Fatalf("re-encoding the params: %v", err)
			}
			if got := strings.Contains(string(params), `"base"`); got != tc.want {
				t.Errorf("base present = %v, want %v in: %s", got, tc.want, params)
			}
		})
	}
}
