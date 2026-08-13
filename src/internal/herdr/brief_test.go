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

// TestTheCommitTheStageMadeIsReported is the other end of the handoff. The
// reducer has always advanced the base from what Complete carried; nothing ever
// filled it in from a real run, so every stage branched from where the task
// started.
func TestTheCommitTheStageMadeIsReported(t *testing.T) {
	fake := &fakeHerdr{settlesAt: StatusIdle}
	node := &Node{
		Runner:    fake,
		Prove:     fake.proving(),
		Roles:     fixedRole("claude"),
		Delivered: func(context.Context, string) (string, string) { return "c0ffee1", "" },
	}

	result, err := node.Run(context.Background(), fsm.NewTaskState("LUNA-1", ""), stageWithTests())
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if result.Commit != "c0ffee1" {
		t.Errorf("commit = %q, want the one the stage left in its worktree", result.Commit)
	}
}

// TestAStageThatCommittedNothingReportsNothing. An empty commit is a state, not a
// failure: the base simply does not move, and the next stage starts where this
// one did.
func TestAStageThatCommittedNothingReportsNothing(t *testing.T) {
	fake := &fakeHerdr{settlesAt: StatusIdle}
	node := &Node{Runner: fake, Prove: fake.proving(), Roles: fixedRole("claude")}

	result, err := node.Run(context.Background(), fsm.NewTaskState("LUNA-1", ""), stageWithTests())
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if result.Commit != "" {
		t.Errorf("commit = %q, want empty when nothing read it", result.Commit)
	}
}

// TestTheAgentsDeclarationBeatsTheAssumption is the fix for a stage closing
// green while owing an artifact.
//
// Measured on a full run: `verify` declared `dod_checked`, committed a file
// called `verification`, and closed — because the node reported `Delivered:
// owed`, so the contract's exit check compared the stage's promises against a
// copy of themselves and always agreed.
func TestTheAgentsDeclarationBeatsTheAssumption(t *testing.T) {
	fake := &fakeHerdr{settlesAt: StatusIdle}
	node := &Node{
		Runner: fake,
		Prove:  fake.proving(),
		Roles:  fixedRole("claude"),
		Delivered: func(context.Context, string) (string, string) {
			return "c0ffee1", "chore: did something else\n\nDelivered: something_else\n"
		},
	}

	result, err := node.Run(context.Background(), fsm.NewTaskState("LUNA-1", ""), stageWithTests())
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	for _, got := range result.Delivered {
		if got != "something_else" {
			t.Errorf("delivered = %v, want only what the agent declared", result.Delivered)
			break
		}
	}
	if len(result.Delivered) != 1 {
		t.Errorf("delivered = %v, want exactly what the agent declared", result.Delivered)
	}
}

// TestAnAgentThatDeclaredNothingFallsBack. Every agent that ran before this
// existed wrote no such line, and a stage must not start failing over the shape
// of a commit message.
func TestAnAgentThatDeclaredNothingFallsBack(t *testing.T) {
	fake := &fakeHerdr{settlesAt: StatusIdle}
	stage := stageWithTests()
	node := &Node{
		Runner: fake,
		Prove:  fake.proving(),
		Roles:  fixedRole("claude"),
		Delivered: func(context.Context, string) (string, string) {
			return "c0ffee1", "chore: a message with no declaration"
		},
	}

	result, err := node.Run(context.Background(), fsm.NewTaskState("LUNA-1", ""), stage)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if len(result.Delivered) != len(stage.Produces)+len(stage.ProducesForHuman) {
		t.Errorf("delivered = %v, want the fallback to what the stage owed", result.Delivered)
	}
}

// TestTheBriefAsksForTheDeclaration. The check only works if the agent is told
// to write the line, by name.
func TestTheBriefAsksForTheDeclaration(t *testing.T) {
	stage := fsm.Stage{ID: "verify", Produces: []fsm.Artifact{"ci_green"}, ProducesForHuman: []fsm.Artifact{"dod_checked"}}

	got := brief(fsm.NewTaskState("LUNA-1", ""), stage, fsm.Role{})

	if !strings.Contains(got, "Delivered: ci_green, dod_checked") {
		t.Errorf("the brief does not ask for the declaration it reads:\n%s", got)
	}
}

// TestAMechanicalStageDeclaresNothingAndStillCloses.
//
// A mechanical stage runs no agent, so nobody writes a declaration — but its
// worktree is branched from the previous stage's commit, whose message carries
// one. Reading that is worse than reading nothing: `setup` reported `repos`,
// which is what `discovery` delivered, and blocked owing `worktree`.
//
// The rule is that a declaration only counts when this stage's own agent wrote
// it, which is exactly the stages that run one.
func TestAMechanicalStageDeclaresNothingAndStillCloses(t *testing.T) {
	fake := &fakeHerdr{settlesAt: StatusIdle}
	node := &Node{
		Runner: fake,
		Prove:  fake.proving(),
		Roles:  fixedRole("claude"),
		Delivered: func(context.Context, string) (string, string) {
			// The previous stage's commit, inherited with the branch.
			return "c0ffee1", "chore: the stage before this one\n\nDelivered: repos\n"
		},
	}

	// The task is already at that commit: the worktree was branched from it, and
	// the mechanical stage adds nothing of its own.
	state := fsm.NewTaskState("LUNA-1", "")
	state.Base = "c0ffee1"

	// No role: `setup` and `commit` are mechanical (ADR-0040).
	stage := fsm.Stage{ID: "setup", Produces: []fsm.Artifact{"worktree"}}
	result, err := node.Run(context.Background(), state, stage)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if len(result.Delivered) != 1 || result.Delivered[0] != "worktree" {
		t.Errorf("delivered = %v, want [worktree] — a mechanical stage owes what the contract says", result.Delivered)
	}
}

// TestAStageThatCommittedNothingDoesNotInheritADeclaration is the same guard on
// the path that matters more.
//
// An agent that works and commits nothing leaves HEAD where its worktree was
// branched from — pointing at the previous stage's commit, and that message
// declares the previous stage's artifacts. Accepting it would let a stage close
// on somebody else's delivery.
func TestAStageThatCommittedNothingDoesNotInheritADeclaration(t *testing.T) {
	fake := &fakeHerdr{settlesAt: StatusIdle}
	node := &Node{
		Runner: fake,
		Prove:  fake.proving(),
		Roles:  fixedRole("claude"),
		Delivered: func(context.Context, string) (string, string) {
			return "base1", "chore: the stage before\n\nDelivered: repos\n"
		},
	}

	state := fsm.NewTaskState("LUNA-1", "")
	state.Base = "base1" // the agent added nothing, so HEAD is still the base
	stage := stageWithTests()

	result, err := node.Run(context.Background(), state, stage)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	for _, got := range result.Delivered {
		if got == "repos" {
			t.Errorf("delivered = %v, inherited from the previous stage's commit", result.Delivered)
		}
	}
}

// TestAMechanicalStageAfterAnAgentStageDeclaresNothing.
//
// The `Commit != Base` guard is not enough on its own. `commit` runs after
// `code-review`, whose commit becomes its worktree's HEAD — and by then the base
// is the *verify* commit, two behind. So HEAD differs from base, the guard
// passes, and the stage reads `Delivered: review_report`.
//
// Measured on a real chore run: `commit` blocked owing `commit_sha` while
// reporting what `code-review` had delivered.
//
// The rule that holds is simpler: a stage with no role runs no agent, so no
// declaration can be its own.
func TestAMechanicalStageAfterAnAgentStageDeclaresNothing(t *testing.T) {
	fake := &fakeHerdr{settlesAt: StatusIdle}
	node := &Node{
		Runner: fake,
		Prove:  fake.proving(),
		Roles:  fixedRole("claude"),
		Delivered: func(context.Context, string) (string, string) {
			// code-review's commit, inherited as this worktree's HEAD.
			return "review1", "chore: review\n\nDelivered: review_report\n"
		},
	}

	state := fsm.NewTaskState("LUNA-1", "")
	state.Base = "verify1" // two stages back: HEAD differs from base

	stage := fsm.Stage{ID: "commit", Produces: []fsm.Artifact{"commit_sha"}}
	result, err := node.Run(context.Background(), state, stage)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if len(result.Delivered) != 1 || result.Delivered[0] != "commit_sha" {
		t.Errorf("delivered = %v, want [commit_sha] — a mechanical stage has no agent to declare", result.Delivered)
	}
}
