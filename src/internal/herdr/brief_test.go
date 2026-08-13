package herdr

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"testing"

	"github.com/brunoomariano/luna/src/internal/fsm"
)

// The brief had no test of its own until the first full run against a real
// repository, which is how it came to omit the two things an agent most needs:
// what the task is about, and how to hand the work over.

// TestTheBriefSaysToCommit is the handoff made explicit.
//
// ADR-0055 makes the commit the handoff and ADR-0058 makes it the snapshot, so
// every stage after the first reads what the previous one committed. Nothing said
// so to the agent: six stages closed green across five role branches and `git log
// --all` showed only the repository's original commit.
func TestTheBriefSaysToCommit(t *testing.T) {
	state := fsm.NewTaskState("LUNA-1", "")
	stage := fsm.Stage{ID: "build", Produces: []fsm.Artifact{"code"}}

	got := brief(state, stage, fsm.Role{})

	if !strings.Contains(got, "commit") {
		t.Errorf("the brief never mentions committing, so the handoff has no medium:\n%s", got)
	}
}

// TestTheBriefNamesTheBaseCommit covers the other half of the handoff: an agent
// told to read "the approach" and not where it is goes looking around the
// filesystem — which is what tripped the harness's own permission prompt.
func TestTheBriefNamesTheBaseCommit(t *testing.T) {
	state := fsm.NewTaskState("LUNA-1", "")
	state.Base = "c1f3872"
	stage := fsm.Stage{ID: "build", Requires: []fsm.Artifact{"approach"}}

	got := brief(state, stage, fsm.Role{})

	if !strings.Contains(got, "c1f3872") {
		t.Errorf("the base commit is where the previous stage's work is, and it is not named:\n%s", got)
	}
}

// TestTheFirstStageDoesNotInventABase. A task's first stage has nothing behind
// it, and naming an empty commit would send the agent looking for one.
func TestTheFirstStageDoesNotInventABase(t *testing.T) {
	state := fsm.NewTaskState("LUNA-1", "")
	stage := fsm.Stage{ID: "discovery", Produces: []fsm.Artifact{"repos"}}

	got := brief(state, stage, fsm.Role{})

	if strings.Contains(got, "Base commit:") {
		t.Errorf("a first stage was given a base it does not have:\n%s", got)
	}
}

// TestTheBriefCarriesWhatTheTaskIsAbout is the statement of work reaching the
// agent.
//
// Without it every stage inferred the goal from the task id, and `spec` stopped
// to ask a person — six times out of six, because a contract cannot be written
// against a name.
func TestTheBriefCarriesWhatTheTaskIsAbout(t *testing.T) {
	state := fsm.NewTaskState("LUNA-1", "")
	state.Statement = fsm.Statement{
		Description: "the counts should be consumable by other programs",
		Acceptance:  "valid JSON out; the default output unchanged",
	}
	stage := fsm.Stage{ID: "spec", Produces: []fsm.Artifact{"contract"}}

	got := brief(state, stage, fsm.Role{})

	for _, want := range []string{state.Statement.Description, state.Statement.Acceptance} {
		if !strings.Contains(got, want) {
			t.Errorf("the brief does not carry %q:\n%s", want, got)
		}
	}
}

// TestTheStatementReachesTheAgent is the wiring, not the formatting: a lookup
// that exists and is never called is the failure this project has met twice.
func TestTheStatementReachesTheAgent(t *testing.T) {
	fake := &fakeHerdr{settlesAt: StatusIdle}
	node := &Node{
		Runner: fake,
		Prove:  fake.proving(),
		Roles:  fixedRole("claude"),
		Statement: func(context.Context, string) (fsm.Statement, error) {
			return fsm.Statement{Description: "consumable by other programs"}, nil
		},
	}

	if _, err := node.Run(context.Background(), fsm.NewTaskState("LUNA-1", ""), stageWithTests()); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if len(fake.prompted) != 1 || !strings.Contains(fake.prompted[0], "consumable by other programs") {
		t.Errorf("the statement never reached the agent:\n%s", fake.prompted)
	}
}

// TestARegistryThatCannotAnswerStillBriefs. Everything the contract requires is
// in the state; the statement is extra. A stage that refuses to run because a
// tracker is unreachable fails for a reason that has nothing to do with the work.
func TestARegistryThatCannotAnswerStillBriefs(t *testing.T) {
	fake := &fakeHerdr{settlesAt: StatusIdle}
	var warned []string
	node := &Node{
		Runner: fake,
		Prove:  fake.proving(),
		Roles:  fixedRole("claude"),
		Warn:   func(format string, args ...any) { warned = append(warned, fmt.Sprintf(format, args...)) },
		Statement: func(context.Context, string) (fsm.Statement, error) {
			return fsm.Statement{}, errors.New("bd: database is locked")
		},
	}

	if _, err := node.Run(context.Background(), fsm.NewTaskState("LUNA-1", ""), stageWithTests()); err != nil {
		t.Fatalf("an unreachable registry failed the stage: %v", err)
	}
	if len(warned) != 1 {
		t.Errorf("the failure to read the statement was swallowed (%d warnings)", len(warned))
	}
}

// TestATaskWithNoStatementStillGetsABrief. A project with no registry states
// nothing, and the brief has to remain usable rather than grow empty headings.
func TestATaskWithNoStatementStillGetsABrief(t *testing.T) {
	state := fsm.NewTaskState("LUNA-1", "")
	stage := fsm.Stage{ID: "build", Produces: []fsm.Artifact{"code"}}

	got := brief(state, stage, fsm.Role{})

	if strings.Contains(got, "What the task is about:") {
		t.Errorf("an empty statement was given a heading anyway:\n%s", got)
	}
	if !strings.Contains(got, "LUNA-1") {
		t.Errorf("the brief lost the task it is about:\n%s", got)
	}
}
