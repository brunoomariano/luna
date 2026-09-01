package node

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/brunoomariano/luna/src/internal/agent"
	"github.com/brunoomariano/luna/src/internal/fsm"
	"github.com/brunoomariano/luna/src/internal/lead"
)

// recordingAgent is a named fake for the agent boundary: it answers with a
// canned result and keeps every call it was asked to make.
//
// Named rather than an inline closure because what the tests here assert is the
// *call* — which directory, which session, which denials — and a fake that only
// returned a value would hide the half that matters.
type recordingAgent struct {
	calls  []agent.Call
	result agent.Result
	err    error
}

func (a *recordingAgent) Run(_ context.Context, call agent.Call) (agent.Result, error) {
	a.calls = append(a.calls, call)
	return a.result, a.err
}

func (a *recordingAgent) last(t *testing.T) agent.Call {
	t.Helper()
	if len(a.calls) == 0 {
		t.Fatal("the agent was never called")
	}
	return a.calls[len(a.calls)-1]
}

// repoWithCommit is a real repository with one commit, because the runner asks
// git what a stage delivered and a fake git would test the fake.
func repoWithCommit(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()

	for _, args := range [][]string{
		{"init", "--initial-branch=main"},
		{"config", "user.email", "test@example.com"},
		{"config", "user.name", "Test"},
		// The machine running the tests may sign commits by default, and a test
		// repository has no key.
		{"config", "commit.gpgsign", "false"},
		{"commit", "--allow-empty", "-m", "root"},
	} {
		cmd := exec.Command("git", args...)
		cmd.Dir = dir
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v: %s", args, err, out)
		}
	}
	return dir
}

func runningState(id string) fsm.TaskState {
	return fsm.TaskState{
		ID:      id,
		Status:  fsm.StatusRunning,
		Context: fsm.NewTaskContext(fsm.KindFeature),
	}
}

// TestTheStageRunsInItsOwnWorktree covers the isolation the whole design rests
// on: an agent works in a checkout of its own, never in the repository, so two
// roles cannot read each other's uncommitted work.
func TestTheStageRunsInItsOwnWorktree(t *testing.T) {
	repo := repoWithCommit(t)
	fake := &recordingAgent{result: agent.Result{Text: "done", Session: "s-1"}}

	r := &Runner{
		Repo:  repo,
		Agent: fake,
	}

	stage := fsm.Stage{ID: "build", Agent: "claude", Brief: "You build."}
	if _, err := r.Run(context.Background(), runningState("T-1"), stage); err != nil {
		t.Fatalf("Run: %v", err)
	}

	call := fake.last(t)
	if call.Dir == repo {
		t.Error("the agent ran in the repository rather than in its own worktree")
	}
	if !strings.Contains(call.Dir, "wt-") || !strings.Contains(call.Dir, "T-1") {
		t.Errorf("want a worktree named for the task, got %q", call.Dir)
	}
}

// TestTheWorktreeIsRemovedWhenTheStageEnds covers what survives a stage: the
// commit, not the directory. A worktree left behind is a place the next role
// could read uncommitted work from.
func TestTheWorktreeIsRemovedWhenTheStageEnds(t *testing.T) {
	repo := repoWithCommit(t)
	fake := &recordingAgent{result: agent.Result{Text: "done"}}

	r := &Runner{
		Repo:  repo,
		Agent: fake,
	}

	stage := fsm.Stage{ID: "build", Agent: "claude"}
	if _, err := r.Run(context.Background(), runningState("T-2"), stage); err != nil {
		t.Fatalf("Run: %v", err)
	}

	out, err := exec.Command("git", "-C", repo, "worktree", "list").CombinedOutput()
	if err != nil {
		t.Fatalf("git worktree list: %v", err)
	}
	if strings.Contains(string(out), "T-2") {
		t.Errorf("the stage's worktree outlived the stage:\n%s", out)
	}
}

// TestAMechanicalStageRunsNoAgent covers the stage that needs no model. Paying
// one to run git buys nothing and can lose something.
func TestAMechanicalStageRunsNoAgent(t *testing.T) {
	repo := repoWithCommit(t)
	fake := &recordingAgent{result: agent.Result{Text: "should not happen"}}

	r := &Runner{Repo: repo, Agent: fake}

	stage := fsm.Stage{ID: "setup"} // no agent
	if _, err := r.Run(context.Background(), runningState("T-3"), stage); err != nil {
		t.Fatalf("Run: %v", err)
	}

	if len(fake.calls) != 0 {
		t.Errorf("a mechanical stage called an agent %d times", len(fake.calls))
	}
}

// TestTheStageRecordsWhatItCost is the accounting reaching the result the lead
// writes to the log. A price nobody records cannot settle the question the
// setting was added for.
func TestTheStageRecordsWhatItCost(t *testing.T) {
	repo := repoWithCommit(t)
	fake := &recordingAgent{result: agent.Result{
		Text:    "done",
		Session: "s-1",
		Turns:   4,
		Usage: agent.Usage{
			InputTokens: 100, OutputTokens: 50, CacheRead: 900,
			CostUSD: 0.42, Model: "claude-opus-5",
		},
	}}

	r := &Runner{
		Repo:  repo,
		Agent: fake,
	}

	result, err := r.Run(context.Background(), runningState("T-4"), fsm.Stage{ID: "build", Agent: "claude"})
	if err != nil {
		t.Fatalf("Run: %v", err)
	}

	if result.Spent.CostUSD != 0.42 {
		t.Errorf("want the cost carried out of the stage, got %v", result.Spent.CostUSD)
	}
	if result.Spent.Tokens() != 1050 {
		t.Errorf("want every token counted (1050), got %d", result.Spent.Tokens())
	}
	if result.Spent.Context != string(agent.Fresh) {
		t.Errorf("want how the stage ran recorded, got %q", result.Spent.Context)
	}
	if result.Spent.Model != "claude-opus-5" {
		t.Errorf("want the model recorded, got %q", result.Spent.Model)
	}
}

// TestALiveStageContinuesItsRolesSession is the mechanism behind `context =
// "live"`: the session a role was last using is handed back to it.
//
// The session travels through the state rather than through a field on the
// Runner, which is the whole point of the change: a map on the struct lives as
// long as the process, and the shipped flow gates in the middle of the maker's
// run. So the test hands the first stage's spend back the way the log does.
func TestALiveStageContinuesItsRolesSession(t *testing.T) {
	repo := repoWithCommit(t)
	fake := &recordingAgent{result: agent.Result{Text: "done", Session: "s-first"}}

	first := fsm.Stage{ID: "build", Agent: "claude"}
	second := fsm.Stage{ID: "refactor", Agent: "claude", Context: fsm.ContextLive}

	r := &Runner{
		Repo:  repo,
		Agent: fake,
		Flow:  []fsm.Stage{first, second},
	}

	state := runningState("T-5")
	result, err := r.Run(context.Background(), state, first)
	if err != nil {
		t.Fatalf("first stage: %v", err)
	}
	if result.Spent.Session != "s-first" {
		t.Fatalf("the first stage must report the session it opened, got %q", result.Spent.Session)
	}

	// What the reducer does with a Complete, done by hand: the spend lands in the
	// state, and that is where the next stage reads it from.
	state.Spent = map[fsm.StageID]fsm.Spend{first.ID: result.Spent}

	if _, err := r.Run(context.Background(), state, second); err != nil {
		t.Fatalf("second stage: %v", err)
	}

	call := fake.last(t)
	if call.Context != agent.Live {
		t.Errorf("want the second stage to continue, got %q", call.Context)
	}
	if call.Session != "s-first" {
		t.Errorf("want the first stage's session, got %q", call.Session)
	}
}

// TestAFreshStageStartsCleanEvenWithASessionAvailable is the inverse, and the
// one that fails silently: a stage that inherited a session it did not ask for
// would carry context nobody declared, and still look like it worked.
func TestAFreshStageStartsCleanEvenWithASessionAvailable(t *testing.T) {
	repo := repoWithCommit(t)
	fake := &recordingAgent{result: agent.Result{Text: "done", Session: "s-first"}}

	r := &Runner{
		Repo:  repo,
		Agent: fake,
	}

	state := runningState("T-6")
	if _, err := r.Run(context.Background(), state, fsm.Stage{
		ID:    "build",
		Agent: "claude",
	}); err != nil {
		t.Fatalf("first stage: %v", err)
	}
	// No context declared: the default is fresh.
	if _, err := r.Run(context.Background(), state, fsm.Stage{ID: "refactor", Agent: "claude"}); err != nil {
		t.Fatalf("second stage: %v", err)
	}

	call := fake.last(t)
	if call.Context != agent.Fresh || call.Session != "" {
		t.Errorf("a fresh stage inherited a session: context %q session %q", call.Context, call.Session)
	}
}

// TestARoleStartsWithoutWhatItIsDenied covers gating reaching the agent. The
// tool is absent from the request rather than discouraged in the brief.
func TestARoleStartsWithoutWhatItIsDenied(t *testing.T) {
	repo := repoWithCommit(t)
	fake := &recordingAgent{result: agent.Result{Text: "reviewed"}}

	r := &Runner{
		Repo:  repo,
		Agent: fake,
	}

	stage := fsm.Stage{
		ID:        "code-review",
		Agent:     "claude",
		Brief:     "You review. You do not edit.",
		ToolsDeny: []fsm.Capability{fsm.CapEdit, fsm.CapWrite},
	}
	if _, err := r.Run(context.Background(), runningState("T-7"), stage); err != nil {
		t.Fatalf("Run: %v", err)
	}

	call := fake.last(t)
	if len(call.Deny) != 2 {
		t.Fatalf("want two capabilities denied, got %v", call.Deny)
	}
	if call.System == "" {
		t.Error("want the role's brief carried as the system prompt")
	}
}

// TestAnUnknownRoleStopsTheStage covers the refusal that keeps a misconfigured
// flow from running unbriefed and ungated — which would look like a stage that
// simply went badly.
func TestAStageWithNoAgentStopsRatherThanRunning(t *testing.T) {
	repo := repoWithCommit(t)
	fake := &recordingAgent{result: agent.Result{Text: "done"}}

	r := &Runner{Repo: repo, Agent: fake}

	// It owes `code`, which no command produces — so running it as mechanical
	// would deliver nothing and look like it worked.
	_, err := r.Run(context.Background(), runningState("T-8"),
		fsm.Stage{ID: "build", Produces: []fsm.Artifact{"code"}})
	if err == nil {
		t.Fatal("want a refusal for a stage with no agent, got a run")
	}
	if !strings.Contains(err.Error(), "code") {
		t.Errorf("want the refusal to name what goes unproduced, got %q", err)
	}
	if len(fake.calls) != 0 {
		t.Error("the agent ran despite the refusal")
	}
}

// TestAMissingHarnessIsInfrastructure keeps the lead from spending its retry
// budget on a binary that will keep not being installed.
func TestAMissingHarnessIsInfrastructure(t *testing.T) {
	repo := repoWithCommit(t)
	fake := &recordingAgent{err: agent.ErrNoHarness}

	r := &Runner{
		Repo:  repo,
		Agent: fake,
	}

	_, err := r.Run(context.Background(), runningState("T-9"), fsm.Stage{ID: "build", Agent: "claude"})
	if !errors.Is(err, lead.ErrInfrastructure) {
		t.Fatalf("want the failure classified as infrastructure, got %v", err)
	}
}

// TestAHandedOverArtifactIsProvenByTheStore covers the artifact that is not in
// the commit. The tree cannot vouch for it, so the store answers — with a hash,
// which is the location the handoff has to carry.
func TestAHandedOverArtifactIsProvenByTheStore(t *testing.T) {
	repo := repoWithCommit(t)
	fake := &recordingAgent{result: agent.Result{Text: "done"}}

	asked := ""
	r := &Runner{
		Repo:      repo,
		Agent:     fake,
		Artifacts: func(string, int) ArtifactStore { return &memoryArtifacts{} },
		Stored: func(_, stage, artifact string) (string, error) {
			asked = stage + "/" + artifact
			return "abc123", nil
		},
	}

	stage := fsm.Stage{
		ID: "qa", Agent: "claude",
		ProducesForHuman: []fsm.Artifact{"qa_report"},
		Verifiers: map[fsm.Artifact]fsm.Verifier{
			"qa_report": fsm.Existence{Handover: true},
		},
	}

	result, err := r.Run(context.Background(), runningState("T-10"), stage)
	if err != nil {
		t.Fatalf("Run: %v", err)
	}

	if asked != "qa/qa_report" {
		t.Errorf("want the store asked for the stage's own artifact, got %q", asked)
	}
	evidence := result.Evidence["qa_report"]
	if evidence.Verdict != fsm.VerdictPassed {
		t.Errorf("want the handover proven, got %q", evidence.Verdict)
	}
	if !strings.Contains(evidence.Detail, "abc123") {
		t.Errorf("want the hash in the evidence, got %q", evidence.Detail)
	}
}

// TestAMissingHandoverFailsRatherThanPasses covers the artifact the agent never
// handed over. Closing on it would be the self-report Luna refuses.
func TestAMissingHandoverFailsRatherThanPasses(t *testing.T) {
	repo := repoWithCommit(t)
	fake := &recordingAgent{result: agent.Result{Text: "done"}}

	r := &Runner{
		Repo:      repo,
		Agent:     fake,
		Artifacts: func(string, int) ArtifactStore { return &memoryArtifacts{} },
		Stored: func(_, _, _ string) (string, error) {
			return "", errors.New("no such artifact")
		},
	}

	stage := fsm.Stage{
		ID: "qa", Agent: "claude",
		ProducesForHuman: []fsm.Artifact{"qa_report"},
		Verifiers: map[fsm.Artifact]fsm.Verifier{
			"qa_report": fsm.Existence{Handover: true},
		},
	}

	result, err := r.Run(context.Background(), runningState("T-11"), stage)
	if err != nil {
		t.Fatalf("Run: %v", err)
	}

	if evidence := result.Evidence["qa_report"]; evidence.Verdict != fsm.VerdictFailed {
		t.Errorf("want a missing handover to fail, got %q", evidence.Verdict)
	}
	for _, delivered := range result.Delivered {
		if delivered == "qa_report" {
			t.Error("an artifact the store never saw was reported as delivered")
		}
	}
}

// TestTheBriefNamesWhatIsHandedOverRatherThanCommitted covers the instruction an
// agent needs to keep the delivered tree clean. Told only that "some artifacts
// are handed over", it commits the ones it guessed wrong about.
func TestTheBriefNamesWhatIsHandedOverRatherThanCommitted(t *testing.T) {
	stage := fsm.Stage{
		ID: "spec", Agent: "claude",
		Produces: []fsm.Artifact{"contract"},
		Verifiers: map[fsm.Artifact]fsm.Verifier{
			"contract": fsm.Existence{Handover: true},
		},
	}

	brief := Brief(runningState("T-12"), stage)

	if !strings.Contains(brief, "contract") {
		t.Error("the brief does not name the artifact owed")
	}
	if !strings.Contains(brief, "luna artifact put") {
		t.Error("the brief does not say how to hand an artifact over")
	}
	if !strings.Contains(brief, "Do not commit them") {
		t.Error("the brief does not say to keep the handed-over artifact out of the commit")
	}
}

// memoryArtifacts is a named fake for the handover store: the socket needs a
// TestTheHandoverIsOpenedAtTheTaskSSequence is a regression test for a commit
// plan that described the wrong commit.
//
// The store keys an artifact's versions by the sequence it was written at, and
// the wiring passed a literal 0 for every stage of every task. A stage that hands
// the same artifact over twice therefore wrote twice to the same key, and the
// second write lost to the primary key.
//
// A flow that passes through each stage once never does that. A loop's second
// round always does: on AVG-1 the forge agent was refused, worked around it by
// handing the plan over under a name nothing checks, and the contract passed on
// the round-one document that was still there. `luna gate show` then asked a
// person to approve a commit plan for `f69a8b8` while the delivery was `5970e47`
// — the gate showing the wrong thing with everything reporting green.
func TestTheHandoverIsOpenedAtTheTaskSSequence(t *testing.T) {
	repo := repoWithCommit(t)
	fake := &recordingAgent{result: agent.Result{Text: "done"}}

	opened := []int{}
	r := &Runner{
		Repo:  repo,
		Agent: fake,
		Artifacts: func(_ string, seq int) ArtifactStore {
			opened = append(opened, seq)
			return &memoryArtifacts{}
		},
		Stored: func(string, string, string) (string, error) { return "abc123", nil },
	}

	stage := fsm.Stage{
		ID: "forge", Agent: "claude",
		Produces:  []fsm.Artifact{"commit_plan"},
		Verifiers: map[fsm.Artifact]fsm.Verifier{"commit_plan": fsm.Existence{Handover: true}},
	}

	// The same stage twice, as a loop runs it, at the two sequences the log had
	// reached each time.
	for _, seq := range []int{7, 12} {
		state := runningState("LUNA-1")
		state.Stage, state.Seq = stage.ID, seq
		if _, err := r.Run(context.Background(), state, stage); err != nil {
			t.Fatalf("running the stage at seq %d: %v", seq, err)
		}
	}

	if len(opened) != 2 || opened[0] != 7 || opened[1] != 12 {
		t.Errorf("the handover was opened at %v, want [7 12] — a constant makes the second "+
			"round collide with the first and keep the stale document", opened)
	}
}

// writer to open at all, and these tests are about what the *verification* asks
// it afterwards rather than about what crossed the socket.
type memoryArtifacts struct {
	put map[string][]byte
}

func (m *memoryArtifacts) PutArtifact(stage, artifact string, body []byte) error {
	if m.put == nil {
		m.put = map[string][]byte{}
	}
	m.put[stage+"/"+artifact] = body
	return nil
}

func (m *memoryArtifacts) GetArtifact(stage, artifact string) ([]byte, error) {
	body, ok := m.put[stage+"/"+artifact]
	if !ok {
		return nil, errors.New("no such artifact")
	}
	return body, nil
}

// roleLookup adapts a plain map to the lookup the runner takes, so a test can
// declare its roles as data.
// TestWarningsReachTheCallerWhenThereIsOne covers the reporting channel for what
// goes wrong beside the work rather than in it. A worktree that will not go away
// does not fail the stage, but a checkout left behind on every run eventually
// fills a disk — and the first anyone would hear of it is that.
func TestWarningsReachTheCallerWhenThereIsOne(t *testing.T) {
	var warned []string
	r := &Runner{Warn: func(format string, args ...any) {
		warned = append(warned, format)
	}}

	r.warn("could not remove %s", "something")
	if len(warned) != 1 {
		t.Errorf("want the warning reported, got %d", len(warned))
	}

	// And a runner with nowhere to print must not panic on the same path.
	(&Runner{}).warn("discarded")
}

// TestTheBriefCarriesTheStatementTheTaskWasOpenedWith covers what a fresh agent
// is told. It did not run the previous stage and has no memory of it, so what
// crosses is pointers and the contract — never a prose summary, which would
// degrade at every hop.
func TestTheBriefCarriesTheStatementTheTaskWasOpenedWith(t *testing.T) {
	state := runningState("T-20")
	state.Statement = fsm.Statement{
		Description: "add a --loud flag",
		Design:      "a flag, not a second script",
		Acceptance:  "greet.sh --loud shouts",
	}

	stage := fsm.Stage{
		ID: "build", Agent: "claude",
		Requires: []fsm.Artifact{"scenarios"},
		Produces: []fsm.Artifact{"code"},
	}

	brief := Brief(state, stage)

	for _, want := range []string{
		"add a --loud flag",      // what the task is about
		"a flag, not a second",   // how it should be approached
		"greet.sh --loud shouts", // what done means
		"scenarios",              // what it has
		"code",                   // what it owes
		"T-20",                   // which task
	} {
		if !strings.Contains(brief, want) {
			t.Errorf("the brief does not carry %q:\n%s", want, brief)
		}
	}
}

// TestAStageWithNothingToSayStillGetsAUsableBrief covers the task nobody
// described. It still runs every stage; the agent is simply left with less to go
// on, and an empty statement must not produce an empty instruction.
func TestAStageWithNothingToSayStillGetsAUsableBrief(t *testing.T) {
	brief := Brief(runningState("T-21"), fsm.Stage{ID: "setup"})

	if !strings.Contains(brief, "T-21") {
		t.Error("the brief does not name the task")
	}
	if !strings.Contains(brief, "commit") {
		t.Error("the brief does not say the commit is the handoff")
	}
}

// TestAHandoverWithNoStoreStopsTheStage covers the misconfiguration that would
// otherwise look like an agent failing to deliver.
//
// A stage declaring a handover and given nowhere to write it cannot succeed, and
// saying so before the agent runs is cheaper than an agent spending a turn on an
// artifact with no destination.
func TestAHandoverWithNoStoreStopsTheStage(t *testing.T) {
	repo := repoWithCommit(t)
	fake := &recordingAgent{result: agent.Result{Text: "done"}}

	r := &Runner{
		Repo:  repo,
		Agent: fake,
		// No Artifacts, and the stage below hands one over.
	}

	stage := fsm.Stage{
		ID: "qa", Agent: "claude",
		ProducesForHuman: []fsm.Artifact{"qa_report"},
		Verifiers: map[fsm.Artifact]fsm.Verifier{
			"qa_report": fsm.Existence{Handover: true},
		},
	}

	_, err := r.Run(context.Background(), runningState("T-30"), stage)
	if err == nil {
		t.Fatal("want a refusal when a handover has nowhere to go, got a run")
	}
	if !strings.Contains(err.Error(), "qa") {
		t.Errorf("want the refusal to name the stage, got %q", err)
	}
	if len(fake.calls) != 0 {
		t.Error("the agent ran despite having nowhere to hand its artifact")
	}
}

// TestAFailedAgentStopsTheStageAndKeepsTheBill covers the call that went wrong.
// A failed call was still billed, and dropping the usage would make failures
// look free — the one direction a cost record must not be wrong in.
func TestAFailedAgentStopsTheStageAndKeepsTheBill(t *testing.T) {
	repo := repoWithCommit(t)
	fake := &recordingAgent{
		result: agent.Result{Usage: agent.Usage{CostUSD: 0.05, InputTokens: 100}},
		err:    errors.New("the model refused"),
	}

	r := &Runner{
		Repo:  repo,
		Agent: fake,
	}

	_, err := r.Run(context.Background(), runningState("T-31"), fsm.Stage{ID: "build", Agent: "claude"})
	if err == nil {
		t.Fatal("want the stage to fail when the agent does, got success")
	}
	if !strings.Contains(err.Error(), "build") {
		t.Errorf("want the error to name the stage, got %q", err)
	}
}

// TestAStageProvesEveryArtifactItOwes covers the exit check running over both
// contract fields. An audit report has no consumer downstream, so nothing would
// ever miss it — which is why it is checked here rather than by the flow.
func TestAStageProvesEveryArtifactItOwes(t *testing.T) {
	repo := repoWithCommit(t)
	fake := &recordingAgent{result: agent.Result{Text: "done"}}

	r := &Runner{
		Repo:      repo,
		Agent:     fake,
		Artifacts: func(string, int) ArtifactStore { return &memoryArtifacts{} },
		Stored:    func(_, _, _ string) (string, error) { return "hash", nil },
	}

	stage := fsm.Stage{
		ID: "qa", Agent: "claude",
		Produces:         []fsm.Artifact{"qa_done"},
		ProducesForHuman: []fsm.Artifact{"qa_report"},
		Verifiers: map[fsm.Artifact]fsm.Verifier{
			"qa_report": fsm.Existence{Handover: true},
		},
	}

	result, err := r.Run(context.Background(), runningState("T-32"), stage)
	if err != nil {
		t.Fatalf("Run: %v", err)
	}

	for _, owed := range []fsm.Artifact{"qa_done", "qa_report"} {
		if _, ok := result.Evidence[owed]; !ok {
			t.Errorf("no evidence recorded for %q, which the stage owes", owed)
		}
	}
}

func TestAVerificationStageCanProveTheCommitItReceived(t *testing.T) {
	repo := repoWithCommit(t)
	fake := &recordingAgent{result: agent.Result{Text: "ci is green"}}

	r := &Runner{
		Repo:  repo,
		Agent: fake,
	}

	state := runningState("T-33")
	state.Base = head(t, repo)
	stage := fsm.Stage{
		ID:       "verify",
		Agent:    "claude",
		Produces: []fsm.Artifact{"ci_green"},
		Verifiers: map[fsm.Artifact]fsm.Verifier{
			"ci_green": fsm.Command{Run: "true", Scope: fsm.ScopeFull},
		},
	}

	result, err := r.Run(context.Background(), state, stage)
	if err != nil {
		t.Fatalf("Run: %v", err)
	}

	evidence := result.Evidence["ci_green"]
	if evidence.Verdict != fsm.VerdictPassed || evidence.Scope != fsm.ScopeFull {
		t.Fatalf("want ci_green proven full over the incoming commit, got %+v", evidence)
	}
	if !containsArtifact(result.Delivered, "ci_green") {
		t.Fatalf("ci_green was proven but not delivered: %+v", result.Delivered)
	}
}

func TestAStageThatOwesCodeStillCannotProveTheBase(t *testing.T) {
	repo := repoWithCommit(t)
	fake := &recordingAgent{result: agent.Result{Text: "nothing changed"}}

	r := &Runner{
		Repo:  repo,
		Agent: fake,
	}

	state := runningState("T-34")
	state.Base = head(t, repo)
	stage := fsm.Stage{
		ID:       "build",
		Agent:    "claude",
		Produces: []fsm.Artifact{"code", "tests_green"},
		Verifiers: map[fsm.Artifact]fsm.Verifier{
			"tests_green": fsm.Command{Run: "true", Scope: fsm.ScopeTargeted},
		},
	}

	result, err := r.Run(context.Background(), state, stage)
	if err != nil {
		t.Fatalf("Run: %v", err)
	}

	if result.Evidence["tests_green"].Verdict == fsm.VerdictPassed {
		t.Fatalf("tests_green was proven by the code the stage received: %+v", result.Evidence["tests_green"])
	}
	if containsArtifact(result.Delivered, "tests_green") {
		t.Fatalf("tests_green must not be delivered when the stage added no code: %+v", result.Delivered)
	}
}

// TestTheBriefCanBeReplacedWithoutTouchingTheRunner covers the seam a test uses
// to drive a stage with a known instruction, and that a project would use to say
// something the shipped brief does not.
func TestTheBriefCanBeReplacedWithoutTouchingTheRunner(t *testing.T) {
	repo := repoWithCommit(t)
	fake := &recordingAgent{result: agent.Result{Text: "done"}}

	r := &Runner{
		Repo:  repo,
		Agent: fake,
		Brief: func(fsm.TaskState, fsm.Stage) string {
			return "do exactly this and nothing else"
		},
	}

	if _, err := r.Run(context.Background(), runningState("T-40"), fsm.Stage{
		ID:    "build",
		Agent: "claude",
	}); err != nil {
		t.Fatalf("Run: %v", err)
	}

	if got := fake.last(t).Prompt; got != "do exactly this and nothing else" {
		t.Errorf("the replaced brief did not reach the agent, got %q", got)
	}
}

// TestEveryStageOfATaskRunsInTheTasksWorkstream covers the setting reaching the
// call, and the unit it is measured in.
//
// It used to be a stage's, and a stage that asked for memory got it while its
// neighbours ran blind. That splits one task's memory across several ledgers and
// answers "what happened on this task" with a shrug — so it is the task's now,
// and every stage of it, including the ones that never mention memory.
func TestEveryStageOfATaskRunsInTheTasksWorkstream(t *testing.T) {
	repo := repoWithCommit(t)
	fake := &recordingAgent{result: agent.Result{Text: "done"}}

	r := &Runner{
		Repo:  repo,
		Agent: fake,
	}

	state := runningState("T-41")
	state.Memory = fsm.TaskMemory{Workstream: "nightly"}

	for _, id := range []fsm.StageID{"intake", "build"} {
		if _, err := r.Run(context.Background(), state, fsm.Stage{ID: id, Agent: "claude"}); err != nil {
			t.Fatalf("running %s: %v", id, err)
		}
		if got := fake.last(t).Workstream; got != "nightly" {
			t.Errorf("%s ran in %q rather than the task's workstream", id, got)
		}
		if fake.last(t).MayCreateWorkstream {
			t.Errorf("%s was allowed to open a ledger the task did not ask for", id)
		}
	}
}

// TestATaskWithNoWorkstreamRunsWithNoMemoryAtAll. Empty is not a fallback to
// whatever workstream the machine is pointing at — that is the contamination a
// name exists to prevent, arriving by a different door.
func TestATaskWithNoWorkstreamRunsWithNoMemoryAtAll(t *testing.T) {
	repo := repoWithCommit(t)
	fake := &recordingAgent{result: agent.Result{Text: "done"}}

	r := &Runner{
		Repo:  repo,
		Agent: fake,
	}

	stage := fsm.Stage{ID: "build", Agent: "claude"}
	if _, err := r.Run(context.Background(), runningState("T-42"), stage); err != nil {
		t.Fatalf("Run: %v", err)
	}
	if got := fake.last(t).Workstream; got != "" {
		t.Errorf("a task with no workstream ran inside %q", got)
	}
}

// TestOnlyATaskThatAskedMayOpenAWorkstream. Creating on a name that is simply
// absent would make a typo open a second ledger instead of stopping the stage.
func TestOnlyATaskThatAskedMayOpenAWorkstream(t *testing.T) {
	repo := repoWithCommit(t)
	fake := &recordingAgent{result: agent.Result{Text: "done"}}

	r := &Runner{
		Repo:  repo,
		Agent: fake,
	}

	state := runningState("T-43")
	state.Memory = fsm.TaskMemory{Workstream: "brand-new", MayCreate: true}

	stage := fsm.Stage{ID: "build", Agent: "claude"}
	if _, err := r.Run(context.Background(), state, stage); err != nil {
		t.Fatalf("Run: %v", err)
	}
	if !fake.last(t).MayCreateWorkstream {
		t.Error("a task that asked for a new workstream cannot open one")
	}
}

// TestTheSocketIsOpenedOnlyForAStageThatHandsSomethingOver covers the reason it
// is conditional: there is no reason to expose a writer to an agent that owes
// nothing through it.
func TestTheSocketIsOpenedOnlyForAStageThatHandsSomethingOver(t *testing.T) {
	repo := repoWithCommit(t)
	fake := &recordingAgent{result: agent.Result{Text: "done"}}

	r := &Runner{
		Repo:      repo,
		Agent:     fake,
		Artifacts: func(string, int) ArtifactStore { return &memoryArtifacts{} },
	}

	// A stage that commits everything it owes gets no socket in its environment.
	plain := fsm.Stage{ID: "build", Agent: "claude", Produces: []fsm.Artifact{"code"}}
	if _, err := r.Run(context.Background(), runningState("T-42"), plain); err != nil {
		t.Fatalf("Run: %v", err)
	}
	for _, env := range fake.last(t).Env {
		if strings.Contains(env, "ARTIFACT_SOCKET") {
			t.Errorf("a stage that hands nothing over was given a writer: %q", env)
		}
	}
}

// TestAMissingGitIsInfrastructureRatherThanAFailedStage keeps the lead from
// spending its retry budget on a machine that will keep not having git.
//
// A tool that is not installed is the machinery breaking, and the second attempt
// will find it just as absent as the first.
func TestAMissingGitIsInfrastructureRatherThanAFailedStage(t *testing.T) {
	repo := repoWithCommit(t)
	fake := &recordingAgent{result: agent.Result{Text: "done"}}

	r := &Runner{
		Repo:  repo,
		Agent: fake,
	}

	// A PATH with nothing on it, so opening the worktree cannot find git.
	t.Setenv("PATH", t.TempDir())

	_, err := r.Run(context.Background(), runningState("T-50"), fsm.Stage{ID: "build", Agent: "claude"})
	if !errors.Is(err, lead.ErrInfrastructure) {
		t.Fatalf("want a missing git classified as infrastructure, got %v", err)
	}
	if len(fake.calls) != 0 {
		t.Error("the agent ran despite there being no worktree to run in")
	}
}

// TestAStageOnAMissingRepositoryFailsWithoutClassifyingIt covers the other side
// of that branch: git is present and the repository is not, which is an ordinary
// failure rather than the machinery breaking.
func TestAStageOnAMissingRepositoryFailsWithoutClassifyingIt(t *testing.T) {
	fake := &recordingAgent{result: agent.Result{Text: "done"}}

	r := &Runner{
		Repo:  filepath.Join(t.TempDir(), "not-a-repository"),
		Agent: fake,
	}

	_, err := r.Run(context.Background(), runningState("T-51"), fsm.Stage{ID: "build", Agent: "claude"})
	if err == nil {
		t.Fatal("want an error for a repository that is not there, got a run")
	}
	if errors.Is(err, lead.ErrInfrastructure) {
		t.Errorf("a missing repository is not the machinery breaking: %v", err)
	}
}

// TestAGatedRoleOnAnUngateableHarnessStopsTheStage covers the refusal that keeps
// a review honest.
//
// A role that withholds Edit and Write on a harness Luna cannot gate would run
// with every tool it was supposed to lose, and nothing about the run would say
// so. A review written by something that could edit the work is the one failure
// the flow cannot catch downstream — the next stage reads the review, not the
// reviewer's permissions.
func TestAGatedRoleOnAnUngateableHarnessStopsTheStage(t *testing.T) {
	repo := repoWithCommit(t)
	fake := &recordingAgent{result: agent.Result{Text: "reviewed"}}

	r := &Runner{
		Repo:  repo,
		Agent: fake,
	}

	_, err := r.Run(context.Background(), runningState("T-60"), fsm.Stage{
		ID:        "code-review",
		Agent:     "some-unknown-harness",
		ToolsDeny: []fsm.Capability{fsm.CapEdit, fsm.CapWrite},
	})
	if err == nil {
		t.Fatal("want a refusal for a gated role on an ungateable harness, got a run")
	}
	if !strings.Contains(err.Error(), "some-unknown-harness") {
		t.Errorf("want the refusal to name the harness, got %q", err)
	}
	if len(fake.calls) != 0 {
		t.Error("an ungated reviewer was started")
	}
}

func TestACodexStageCannotClaimAPartialWriteDenial(t *testing.T) {
	err := checkStage(fsm.Stage{
		ID: "review", Agent: "codex", ToolsDeny: []fsm.Capability{fsm.CapEdit},
	})
	if err == nil {
		t.Fatal("Codex cannot deny Edit without denying all worktree writes")
	}
	if !strings.Contains(err.Error(), "Edit and Write") {
		t.Errorf("the refusal does not state the supported denial: %v", err)
	}
}

// TestAnUnknownHarnessStopsBeforeTheWorktree opens keeps a selection typo from
// turning into stage work that can never reach an executable.
func TestAnUnknownHarnessStopsBeforeTheWorktreeOpens(t *testing.T) {
	repo := repoWithCommit(t)
	fake := &recordingAgent{result: agent.Result{Text: "done"}}

	r := &Runner{
		Repo:  repo,
		Agent: fake,
	}

	_, err := r.Run(context.Background(), runningState("T-61"), fsm.Stage{
		ID:    "build",
		Agent: "some-unknown-harness",
	})
	if err == nil || !strings.Contains(err.Error(), "some-unknown-harness") {
		t.Fatalf("an unknown harness must be refused by name, got %v", err)
	}
	if len(fake.calls) != 0 {
		t.Error("the unknown harness reached the agent boundary")
	}
}

func TestTheAgentOverrideIsAppliedOnlyToTheCall(t *testing.T) {
	repo := repoWithCommit(t)
	fake := &recordingAgent{result: agent.Result{Text: "done"}}
	r := &Runner{Repo: repo, Agent: fake, AgentOverride: "codex"}
	stage := fsm.Stage{ID: "build", Agent: "claude"}

	result, err := r.Run(context.Background(), runningState("T-62"), stage)
	if err != nil {
		t.Fatalf("running: %v", err)
	}
	if len(fake.calls) != 1 || fake.calls[0].Kind != "codex" {
		t.Errorf("the harness call did not receive the override: %+v", fake.calls)
	}
	if stage.Agent != "claude" {
		t.Errorf("the recorded stage was mutated to %q", stage.Agent)
	}
	if result.Spent.Agent != "codex" {
		t.Errorf("the audit record lost the harness actually used: %+v", result.Spent)
	}
}

func TestACodexRunRefusesAnUnenforceableUSDBudget(t *testing.T) {
	fake := &recordingAgent{result: agent.Result{Text: "done"}}
	r := &Runner{Agent: fake, AgentOverride: "codex"}
	state := runningState("T-63")
	state.BudgetUSD = 1

	_, err := r.Run(context.Background(), state, fsm.Stage{ID: "build", Agent: "claude"})
	if err == nil {
		t.Fatal("a dollar budget cannot silently become unlimited under Codex")
	}
	if !errors.Is(err, lead.ErrInfrastructure) || !strings.Contains(err.Error(), "does not report USD cost") {
		t.Errorf("the refusal must name why the budget cannot be enforced: %v", err)
	}
	if len(fake.calls) != 0 {
		t.Error("the agent started before the unenforceable budget was refused")
	}
}

// TestAReportOnlyUncontainedStageCannotChangeTheWorktree holds the boundary
// that makes setup's containment exception true in execution, not only in its
// brief. The agent is outside the jail, so the node has to detect the write.
func TestAReportOnlyUncontainedStageCannotChangeTheWorktree(t *testing.T) {
	repo := repoWithCommit(t)
	fake := &committingAgent{
		text: "reported the setup", turns: 1,
		usage: agent.Usage{InputTokens: 100},
	}
	r := &Runner{Repo: repo, Agent: fake}

	result, err := r.Run(context.Background(), runningState("T-64"), fsm.Stage{
		ID: "setup", Agent: "codex", Uncontained: true,
	})
	if err == nil {
		t.Fatal("an uncontained report-only stage changed the worktree")
	}
	for _, want := range []string{"setup", "report-only", "HEAD"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("the refusal does not name %q: %v", want, err)
		}
	}
	if result.Spent.Tokens() != 100 || result.Spent.Agent != "codex" {
		t.Errorf("the rejected call lost its measured usage: %+v", result.Spent)
	}
}

func TestAReportOnlyUncontainedStageMayLeaveTheWorktreeUnchanged(t *testing.T) {
	repo := repoWithCommit(t)
	stage := fsm.Stage{ID: "setup", Agent: "codex", Uncontained: true}

	before, err := snapshotReportOnly(context.Background(), stage, repo)
	if err != nil {
		t.Fatalf("snapshotting the report-only stage: %v", err)
	}
	if err := verifyReportOnlyUnchanged(context.Background(), stage, repo, before); err != nil {
		t.Errorf("an unchanged report-only stage was refused: %v", err)
	}
}

func TestAReportOnlySnapshotNeedsARepository(t *testing.T) {
	_, err := snapshotReportOnly(context.Background(), fsm.Stage{
		ID: "setup", Agent: "codex", Uncontained: true,
	}, t.TempDir())
	if err == nil {
		t.Fatal("a report-only snapshot outside a repository was accepted")
	}
}

// TestWhatTheAgentSaidSurvivesAnEmptyDelivery covers INV-5 on the path that
// broke it.
//
// A stage that commits nothing has said something about why, and the reply was
// being discarded: `Result.Text` reached the runner and nothing read it. Five
// stages of TALLY-5 each reported "delivered nothing" while the agent was
// saying, five times over, that git was unreachable inside the sandbox. The
// failure was silent for a whole run because the only place the reason existed
// was the field nobody kept.
func TestWhatTheAgentSaidSurvivesAnEmptyDelivery(t *testing.T) {
	repo := repoWithCommit(t)
	fake := &recordingAgent{result: agent.Result{
		Text: "I could not commit: fatal: not a git repository",
	}}

	var warned []string
	r := &Runner{
		Repo:  repo,
		Agent: fake,
		Warn: func(format string, args ...any) {
			warned = append(warned, fmt.Sprintf(format, args...))
		},
	}

	// The agent commits nothing, so the worktree's HEAD stays on the base — which
	// is what the runner reads back as the delivery.
	state := runningState("T-30")
	state.Base = head(t, repo)

	if _, err := r.Run(context.Background(), state, fsm.Stage{
		ID:    "build",
		Agent: "claude",
	}); err != nil {
		t.Fatalf("Run: %v", err)
	}

	if !anyContains(warned, "not a git repository") {
		t.Errorf("a stage that delivered nothing discarded the only account of why; warnings were: %v", warned)
	}
}

// TestAStageThatDeliveredDoesNotRepeatTheAgent is the other side, so the
// reporting cannot pass by printing everything.
//
// On the happy path the reply is the agent narrating a delivery that already
// speaks for itself, and echoing it on every stage is how a log stops being
// read.
func TestAStageThatDeliveredDoesNotRepeatTheAgent(t *testing.T) {
	repo := repoWithCommit(t)
	fake := &committingAgent{text: "built it, all green"}

	var warned []string
	r := &Runner{
		Repo:  repo,
		Agent: fake,
		Warn: func(format string, args ...any) {
			warned = append(warned, fmt.Sprintf(format, args...))
		},
	}

	state := runningState("T-31")
	state.Base = head(t, repo)

	if _, err := r.Run(context.Background(), state, fsm.Stage{
		ID:    "build",
		Agent: "claude",
	}); err != nil {
		t.Fatalf("Run: %v", err)
	}

	if anyContains(warned, "all green") {
		t.Errorf("a stage that delivered must not echo the agent's reply; warnings were: %v", warned)
	}
}

// TestTheAgentIsGivenSomeoneToCommitAs covers the identity crossing into the
// sandbox.
//
// The jail has no `~/.gitconfig`, so an agent with no identity in its
// environment does the work and then cannot commit it. The four variables git
// reads before any config file are how the repository's own identity gets there.
func TestTheAgentIsGivenSomeoneToCommitAs(t *testing.T) {
	repo := repoWithCommit(t)
	fake := &recordingAgent{result: agent.Result{Text: "done"}}

	r := &Runner{
		Repo:  repo,
		Agent: fake,
	}

	if _, err := r.Run(context.Background(), runningState("T-32"), fsm.Stage{
		ID:    "build",
		Agent: "claude",
	}); err != nil {
		t.Fatalf("Run: %v", err)
	}

	env := strings.Join(fake.last(t).Env, " ")
	for _, want := range []string{
		"GIT_AUTHOR_NAME=", "GIT_AUTHOR_EMAIL=", "GIT_COMMITTER_NAME=", "GIT_COMMITTER_EMAIL=",
		agent.StageEnv + "=1",
	} {
		if !strings.Contains(env, want) {
			t.Errorf("the agent was given no %s, so its commit fails in the sandbox: %q", want, env)
		}
	}
}

// committingAgent is a named fake that does what a working agent does: it
// commits. The empty-delivery fakes cannot show the other side of the reporting
// rule, because a stage that never commits is exactly the case being reported.
type committingAgent struct {
	text  string
	call  agent.Call
	usage agent.Usage
	turns int
}

func (c *committingAgent) Run(ctx context.Context, call agent.Call) (agent.Result, error) {
	c.call = call
	if err := os.WriteFile(filepath.Join(call.Dir, "built.txt"), []byte("work"), 0o600); err != nil {
		return agent.Result{}, err
	}
	for _, args := range [][]string{{"add", "."}, {"commit", "-m", "the work"}} {
		cmd := exec.CommandContext(ctx, "git", args...)
		cmd.Dir = call.Dir
		cmd.Env = append(os.Environ(), call.Env...)
		if out, err := cmd.CombinedOutput(); err != nil {
			return agent.Result{}, fmt.Errorf("git %v: %w: %s", args, err, out)
		}
	}
	return agent.Result{Text: c.text, Usage: c.usage, Turns: c.turns}, nil
}

func anyContains(list []string, want string) bool {
	for _, got := range list {
		if strings.Contains(got, want) {
			return true
		}
	}
	return false
}

func containsArtifact(list []fsm.Artifact, want fsm.Artifact) bool {
	for _, got := range list {
		if got == want {
			return true
		}
	}
	return false
}

// TestTheBriefSaysAnAbsentToolIsAbsentFromTheSandbox covers an agent describing
// its jail as though it were the machine.
//
// Measured twice on one task. A `plan` agent could not run `shellcheck` inside
// the sandbox and wrote "shellcheck is not installed on this machine" into the
// contract as a fact about the project. The gate that followed then rejected an
// obligation as unverifiable on the strength of it. Both were wrong: shellcheck
// is installed, and Luna runs the exit check outside the jail where it is
// reachable — the same `make ci` the contract called impossible passed on the
// first try.
//
// The agent cannot know where the check runs, so the brief has to say.
func TestTheBriefSaysAnAbsentToolIsAbsentFromTheSandbox(t *testing.T) {
	brief := Brief(runningState("T-40"), fsm.Stage{ID: "plan", Agent: "claude"})

	for _, want := range []string{"sandbox", "outside"} {
		if !strings.Contains(brief, want) {
			t.Errorf("the brief does not warn about %q, so an agent records the jail's "+
				"limits as the project's:\n%s", want, brief)
		}
	}
}

// TestAStageGivenTheContractIsToldToJudgeAgainstIt closes the gap between two
// things that share a name.
//
// The stage contract — requires and produces — is checked by the engine. The
// contract *artifact*, the document the plan stage wrote, was checked by nobody:
// the gate judged whether it was coherent, and nothing afterwards asked whether
// the delivery honoured it.
//
// Measured on TALLY-7. The contract required, in as many words, a test pinning
// one of its own decisions. The test was never written, `build` closed green —
// correctly, since its stage contract asked for `code` and `tests_green` and
// both arrived — and `verify` then reported that all three decisions were pinned
// by a test. It had every means to notice and had never been asked to compare
// the two.
func TestAStageGivenTheContractIsToldToJudgeAgainstIt(t *testing.T) {
	stage := fsm.Stage{
		ID: "verify", Agent: "claude",
		Requires: []fsm.Artifact{"code", "scenarios", "contract"},
		Produces: []fsm.Artifact{"ci_green"},
	}

	brief := Brief(runningState("T-50"), stage)

	for _, want := range []string{
		"judged against",
		"every obligation",
		// The half of the gate's question the gate could not answer: whether an
		// obligation was possible at all depends on the code, and the gate carries
		// only the document. This stage has both.
		"impossible",
	} {
		if !strings.Contains(brief, want) {
			t.Errorf("a stage handed the contract is not told to judge against it (%q):\n%s", want, brief)
		}
	}

	// And it is told how to read one, since naming an artifact it cannot open
	// would be the same gap wearing a different hat.
	if !strings.Contains(brief, "luna artifact get") {
		t.Errorf("the brief names inputs without saying how to read them:\n%s", brief)
	}
}

// TestAStageWithoutTheContractIsNotToldToJudgeIt is the other side.
//
// `build` writes code against the contract; it is not the stage that audits the
// delivery against it. Telling every stage to judge would put the instruction
// where it does not belong and cost tokens on every one of them.
func TestAStageWithoutTheContractIsNotToldToJudgeIt(t *testing.T) {
	stage := fsm.Stage{
		ID: "intake", Agent: "claude",
		Requires: []fsm.Artifact{"task_id", "worktree"},
		Produces: []fsm.Artifact{"briefing"},
	}

	if brief := Brief(runningState("T-51"), stage); strings.Contains(brief, "judged against") {
		t.Errorf("a stage that was not handed the contract is told to judge it:\n%s", brief)
	}
}

// TestAStageIsShownTheCriteriaItsArtifactWillBeJudgedOn closes a rule enforced
// at one end of the flow and never stated at the other.
//
// The gate holds the contract to declared criteria. The stage that writes the
// contract was never shown them, so it wrote to its own idea of what a contract
// is — which is a reasonable one, and not the one being marked.
//
// Measured across four contracts: two were rejected for the same criterion, and
// both times for a sentence the maker had no reason to think was forbidden.
func TestAStageIsShownTheCriteriaItsArtifactWillBeJudgedOn(t *testing.T) {
	stage := fsm.Stage{
		ID: "plan", Agent: "claude",
		Produces: []fsm.Artifact{"contract"},
		Gate: &fsm.GateSpec{
			Kind:     fsm.GateReviewArtifact,
			Artifact: "contract",
			Judge:    []string{"the contract states what is required, with no suggestions"},
		},
	}

	brief := Brief(runningState("T-60"), stage)

	if !strings.Contains(brief, "with no suggestions") {
		t.Errorf("the stage writing the artifact is not shown what it is judged on:\n%s", brief)
	}
	if !strings.Contains(brief, "opens a gate") {
		t.Errorf("the brief does not say the artifact faces a gate:\n%s", brief)
	}
}

// TestAStageWithNoGateIsShownNoCriteria is the other side: `build` produces code
// that opens no gate, and listing criteria there would be noise on every stage.
func TestAStageWithNoGateIsShownNoCriteria(t *testing.T) {
	stage := fsm.Stage{ID: "build", Produces: []fsm.Artifact{"code"}}

	if brief := Brief(runningState("T-61"), stage); strings.Contains(brief, "opens a gate") {
		t.Errorf("a stage with no gate was told its artifact faces one:\n%s", brief)
	}
}

// TestARetryIsToldWhatIsStillMissing. A retry briefed with the original contract
// reads as a first attempt, and the agent is left to work out which half it
// already did — so it either repeats everything or guesses.
func TestARetryIsToldWhatIsStillMissing(t *testing.T) {
	state := fsm.NewTaskState("LUNA-1", fsm.KindFeature)
	state.Stage = "verify"
	state.StillOwed = []fsm.Artifact{"dod_checked"}

	stage := fsm.Stage{
		ID:               "verify",
		Agent:            "claude",
		Produces:         []fsm.Artifact{"ci_green"},
		ProducesForHuman: []fsm.Artifact{"dod_checked"},
		Verifiers: map[fsm.Artifact]fsm.Verifier{
			"ci_green":    fsm.Command{Run: "make ci", Scope: fsm.ScopeFull},
			"dod_checked": fsm.Existence{Handover: true},
		},
	}

	brief := Brief(state, stage)

	if !strings.Contains(brief, "dod_checked did not arrive") {
		t.Errorf("the retry is not told what is outstanding:\n%s", brief)
	}
	if !strings.Contains(brief, "already delivered is kept") {
		t.Errorf("the retry is not told its earlier work stands:\n%s", brief)
	}
}

// TestAFirstAttemptIsNotToldItHasBeenHereBefore is the other half: the section
// only appears when there is a debt, or every stage would open by describing a
// failure that has not happened.
func TestAFirstAttemptIsNotToldItHasBeenHereBefore(t *testing.T) {
	state := fsm.NewTaskState("LUNA-1", fsm.KindFeature)
	state.Stage = "build"

	stage := fsm.Stage{
		ID:        "build",
		Agent:     "claude",
		Produces:  []fsm.Artifact{"code"},
		Verifiers: map[fsm.Artifact]fsm.Verifier{"code": fsm.Existence{}},
	}

	if brief := Brief(state, stage); strings.Contains(brief, "been here before") {
		t.Errorf("a first attempt is told it is a retry:\n%s", brief)
	}
}

// TestAWorktreeIsBootstrappedBeforeTheStageStarts is the most expensive thing a
// real run found.
//
// A worktree is a clean checkout opened per stage, so a repository whose tests
// need a build step first has none — and the check fails on the machine while
// saying the stage delivered too little. Measured: `tests_green` runs `make
// test`, `make test` needs `make build`, and two build stages failed on it for
// $10.36 of a $19.13 task, producing code that had been correct all along.
func TestAWorktreeIsBootstrappedBeforeTheStageStarts(t *testing.T) {
	repo := repoWithCommit(t)
	fake := &recordingAgent{result: agent.Result{Text: "done"}}

	marker := filepath.Join(t.TempDir(), "bootstrapped")
	r := &Runner{
		Repo:      repo,
		Agent:     fake,
		Bootstrap: "touch " + marker,
	}

	stage := fsm.Stage{ID: "build", Agent: "claude"}
	if _, err := r.Run(context.Background(), runningState("B-1"), stage); err != nil {
		t.Fatalf("Run: %v", err)
	}

	if _, err := os.Stat(marker); err != nil {
		t.Fatalf("the worktree was handed to the stage without being made runnable: %v", err)
	}
}

// TestABootstrapThatFailsIsInfrastructure. "The machine is not ready" and "the
// stage delivered less than it owed" are different facts with opposite
// treatments, and a person who cannot tell them apart pays for the wrong fix.
func TestABootstrapThatFailsIsInfrastructure(t *testing.T) {
	repo := repoWithCommit(t)
	fake := &recordingAgent{result: agent.Result{Text: "done"}}

	r := &Runner{
		Repo:      repo,
		Agent:     fake,
		Bootstrap: "echo 'no lockfile here' >&2; exit 1",
	}

	_, err := r.Run(context.Background(), runningState("B-2"), fsm.Stage{ID: "build", Agent: "claude"})
	if !errors.Is(err, lead.ErrInfrastructure) {
		t.Fatalf("a failed bootstrap was reported as the work failing: %v", err)
	}
	// And it carries what went wrong, because the whole point is that somebody can
	// act on it without running the suite by hand in a parallel worktree.
	if !strings.Contains(err.Error(), "no lockfile here") {
		t.Errorf("the failure does not carry the command's output: %v", err)
	}

	// The agent never started: there was nothing for it to work in.
	if len(fake.calls) != 0 {
		t.Errorf("an agent was billed for a worktree that was not ready: %d calls", len(fake.calls))
	}
}

// TestARepositoryThatNeedsNoBootstrapRunsNone. Empty means none, and a project
// whose tests run from a clean checkout should not pay for a shell per stage.
func TestARepositoryThatNeedsNoBootstrapRunsNone(t *testing.T) {
	repo := repoWithCommit(t)
	fake := &recordingAgent{result: agent.Result{Text: "done"}}

	r := &Runner{
		Repo:  repo,
		Agent: fake,
	}

	if _, err := r.Run(context.Background(), runningState("B-3"), fsm.Stage{ID: "build", Agent: "claude"}); err != nil {
		t.Fatalf("Run: %v", err)
	}
	if len(fake.calls) != 1 {
		t.Errorf("the stage did not run: %d calls", len(fake.calls))
	}
}

// TestABootstrapThatNeverFinishesIsStuckRatherThanSlow. The stage behind it has
// not started, so nothing is lost by saying so — and a wait with no ceiling is
// the silent hang INV-5 forbids.
func TestABootstrapThatNeverFinishesIsStuckRatherThanSlow(t *testing.T) {
	repo := repoWithCommit(t)
	fake := &recordingAgent{result: agent.Result{Text: "done"}}

	r := &Runner{
		Repo:             repo,
		Agent:            fake,
		Bootstrap:        "sleep 30",
		BootstrapTimeout: 20 * time.Millisecond,
	}

	_, err := r.Run(context.Background(), runningState("B-4"), fsm.Stage{ID: "build", Agent: "claude"})
	if !errors.Is(err, lead.ErrInfrastructure) {
		t.Fatalf("a bootstrap that hung was reported as the work failing: %v", err)
	}
	if !strings.Contains(err.Error(), "did not finish") {
		t.Errorf("the failure does not say it ran out of time: %v", err)
	}
	if len(fake.calls) != 0 {
		t.Errorf("an agent was billed for a worktree that never became ready: %d", len(fake.calls))
	}
}

// TestWhatSetupDiscoversBecomesTheProjectsSetting closes the gap between the
// stage that reads the project and the code that acts on it.
//
// The preparation command was a key somebody typed into a file, and `setup` only
// ever put what it found into a report for a person — so Luna named the command
// it had discovered and then ran whatever the file said. Two sources for one
// fact, and nothing reconciling them.
func TestWhatSetupDiscoversBecomesTheProjectsSetting(t *testing.T) {
	repo := repoWithCommit(t)
	fake := &recordingAgent{result: agent.Result{Text: "done"}}
	store := &memoryArtifacts{}

	recorded := map[string]string{}
	r := &Runner{
		Repo:      repo,
		Agent:     fake,
		Artifacts: func(string, int) ArtifactStore { return store },
		Stored:    func(string, string, string) (string, error) { return "abc123", nil },
		Configure: func(key, value string) error {
			recorded[key] = value
			return nil
		},
	}

	stage := fsm.Stage{
		ID: "setup", Agent: "claude",
		Produces: []fsm.Artifact{BootstrapArtifact},
		Verifiers: map[fsm.Artifact]fsm.Verifier{
			BootstrapArtifact: fsm.Existence{Handover: true},
		},
	}

	// What the agent handed over, with the whitespace a heredoc leaves behind.
	if err := store.PutArtifact("setup", string(BootstrapArtifact), []byte("make bootstrap\n")); err != nil {
		t.Fatalf("seeding the handover: %v", err)
	}

	state := runningState("LUNA-1")
	state.Stage = stage.ID
	if _, err := r.Run(context.Background(), state, stage); err != nil {
		t.Fatalf("running the stage: %v", err)
	}

	if recorded["bootstrap"] != "make bootstrap" {
		t.Errorf("the project's bootstrap is %q, want the discovered command with no "+
			"trailing newline — it is passed to `sh -c`", recorded["bootstrap"])
	}
}

// TestAStageThatFailedRecordsNothing. The artifact may be missing or half
// written, and a command Luna runs in every worktree from here on is not
// something to take from a stage that did not close.
func TestAStageThatFailedRecordsNothing(t *testing.T) {
	repo := repoWithCommit(t)
	fake := &recordingAgent{result: agent.Result{Text: "done"}}

	recorded := map[string]string{}
	r := &Runner{
		Repo:      repo,
		Agent:     fake,
		Artifacts: func(string, int) ArtifactStore { return &memoryArtifacts{} },
		// Nothing was handed over, so the contract cannot close.
		Stored:    func(string, string, string) (string, error) { return "", errors.New("no such artifact") },
		Configure: func(key, value string) error { recorded[key] = value; return nil },
	}

	stage := fsm.Stage{
		ID: "setup", Agent: "claude",
		Produces: []fsm.Artifact{BootstrapArtifact},
		Verifiers: map[fsm.Artifact]fsm.Verifier{
			BootstrapArtifact: fsm.Existence{Handover: true},
		},
	}

	state := runningState("LUNA-1")
	state.Stage = stage.ID
	_, _ = r.Run(context.Background(), state, stage)

	if len(recorded) != 0 {
		t.Errorf("a stage that delivered nothing set the project's configuration: %v", recorded)
	}
}

// TestAStageThatOwesNoCommandIsNotAskedForOne. Every stage runs through the same
// path, and one that never declared the artifact must not be read for it.
func TestAStageThatOwesNoCommandIsNotAskedForOne(t *testing.T) {
	repo := repoWithCommit(t)
	fake := &recordingAgent{result: agent.Result{Text: "done"}}

	asked := false
	r := &Runner{
		Repo:      repo,
		Agent:     fake,
		Artifacts: func(string, int) ArtifactStore { return &memoryArtifacts{} },
		Stored:    func(string, string, string) (string, error) { return "abc123", nil },
		Configure: func(string, string) error { asked = true; return nil },
	}

	stage := fsm.Stage{
		ID: "intake", Agent: "claude",
		Produces:  []fsm.Artifact{"briefing"},
		Verifiers: map[fsm.Artifact]fsm.Verifier{"briefing": fsm.Existence{Handover: true}},
	}

	state := runningState("LUNA-1")
	state.Stage = stage.ID
	if _, err := r.Run(context.Background(), state, stage); err != nil {
		t.Fatalf("running the stage: %v", err)
	}

	if asked {
		t.Error("a stage that owes no bootstrap command set one anyway")
	}
}

// TestAConfigurationThatCannotBeWrittenDoesNotFailTheStage. The stage delivered;
// what is lost is the preparation on the next one, which announces itself as a
// failed check rather than as silence.
func TestAConfigurationThatCannotBeWrittenDoesNotFailTheStage(t *testing.T) {
	repo := repoWithCommit(t)
	fake := &recordingAgent{result: agent.Result{Text: "done"}}
	handed := &memoryArtifacts{}

	r := &Runner{
		Repo:      repo,
		Agent:     fake,
		Artifacts: func(string, int) ArtifactStore { return handed },
		Stored:    func(string, string, string) (string, error) { return "abc123", nil },
		Configure: func(string, string) error { return errors.New("the database is read-only") },
	}

	stage := fsm.Stage{
		ID: "setup", Agent: "claude",
		Produces: []fsm.Artifact{BootstrapArtifact},
		Verifiers: map[fsm.Artifact]fsm.Verifier{
			BootstrapArtifact: fsm.Existence{Handover: true},
		},
	}
	if err := handed.PutArtifact("setup", string(BootstrapArtifact), []byte("make bootstrap")); err != nil {
		t.Fatalf("seeding the handover: %v", err)
	}

	state := runningState("LUNA-1")
	state.Stage = stage.ID
	if _, err := r.Run(context.Background(), state, stage); err != nil {
		t.Errorf("a setting that could not be written failed the stage: %v", err)
	}
}

// TestTheBriefDoesNotOfferTheTaskIDAsSomethingToFetch.
//
// `task_id` is the root of the artifact graph: no stage produces it, so no blob
// is written for it and `luna artifact get task_id` answers "no such artifact".
// Listing it under "read one with" sent MAX-2's setup stage to fetch it, get
// refused, and hand the gate an open question about whether the briefing or the
// store was stale. Neither was.
func TestTheBriefDoesNotOfferTheTaskIDAsSomethingToFetch(t *testing.T) {
	stage := fsm.Stage{
		ID: "intake", Agent: "claude",
		Requires: []fsm.Artifact{fsm.TaskID, "worktree"},
	}

	brief := Brief(runningState("MAX-2"), stage)

	have := ""
	for _, line := range strings.Split(brief, "\n") {
		if strings.HasPrefix(line, "What you have:") {
			have = line
		}
	}
	if have == "" {
		t.Fatalf("the brief no longer says what the stage has:\n%s", brief)
	}
	if strings.Contains(have, string(fsm.TaskID)) {
		t.Errorf("the brief offers task_id as a document to fetch: %q", have)
	}
	if !strings.Contains(have, "worktree") {
		t.Errorf("the brief lost an artifact that can be read: %q", have)
	}
	// The id itself is not hidden — it is in the first line, where it belongs.
	if !strings.Contains(brief, "MAX-2") {
		t.Error("the brief no longer names the task at all")
	}
}

// TestAStageThatRequiresOnlyTheTaskIDIsOfferedNothingToFetch, rather than an
// empty list under a line explaining how to read one.
func TestAStageThatRequiresOnlyTheTaskIDIsOfferedNothingToFetch(t *testing.T) {
	stage := fsm.Stage{ID: "setup", Agent: "claude", Requires: []fsm.Artifact{fsm.TaskID}}

	brief := Brief(runningState("MAX-2"), stage)

	if strings.Contains(brief, "What you have:") {
		t.Errorf("a stage with nothing readable was still told what it has:\n%s", brief)
	}
	if strings.Contains(brief, "luna artifact get <name>") {
		t.Error("the brief says how to read an artifact the stage cannot read")
	}
}
