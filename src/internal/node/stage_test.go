package node

import (
	"context"
	"errors"
	"os/exec"
	"strings"
	"testing"

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
		Roles: map[fsm.RoleName]fsm.Role{"implementer": {Agent: "claude", Brief: "You build."}},
	}

	stage := fsm.Stage{ID: "build", Role: "implementer"}
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
		Roles: map[fsm.RoleName]fsm.Role{"implementer": {Agent: "claude"}},
	}

	stage := fsm.Stage{ID: "build", Role: "implementer"}
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

	stage := fsm.Stage{ID: "setup"} // no role
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
		Roles: map[fsm.RoleName]fsm.Role{"implementer": {Agent: "claude"}},
	}

	result, err := r.Run(context.Background(), runningState("T-4"), fsm.Stage{ID: "build", Role: "implementer"})
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
func TestALiveStageContinuesItsRolesSession(t *testing.T) {
	repo := repoWithCommit(t)
	fake := &recordingAgent{result: agent.Result{Text: "done", Session: "s-first"}}

	r := &Runner{
		Repo:  repo,
		Agent: fake,
		Roles: map[fsm.RoleName]fsm.Role{"implementer": {Agent: "claude"}},
	}

	state := runningState("T-5")
	first := fsm.Stage{ID: "build", Role: "implementer"}
	if _, err := r.Run(context.Background(), state, first); err != nil {
		t.Fatalf("first stage: %v", err)
	}

	second := fsm.Stage{ID: "refactor", Role: "implementer", Context: fsm.ContextLive}
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
		Roles: map[fsm.RoleName]fsm.Role{"implementer": {Agent: "claude"}},
	}

	state := runningState("T-6")
	if _, err := r.Run(context.Background(), state, fsm.Stage{ID: "build", Role: "implementer"}); err != nil {
		t.Fatalf("first stage: %v", err)
	}
	// No context declared: the default is fresh.
	if _, err := r.Run(context.Background(), state, fsm.Stage{ID: "refactor", Role: "implementer"}); err != nil {
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
		Roles: map[fsm.RoleName]fsm.Role{"reviewer": {
			Agent:     "claude",
			Brief:     "You review. You do not edit.",
			ToolsDeny: []fsm.Capability{fsm.CapEdit, fsm.CapWrite},
		}},
	}

	stage := fsm.Stage{ID: "code-review", Role: "reviewer"}
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
func TestAnUnknownRoleStopsTheStage(t *testing.T) {
	repo := repoWithCommit(t)
	fake := &recordingAgent{result: agent.Result{Text: "done"}}

	r := &Runner{Repo: repo, Agent: fake, Roles: map[fsm.RoleName]fsm.Role{}}

	_, err := r.Run(context.Background(), runningState("T-8"), fsm.Stage{ID: "build", Role: "implementer"})
	if err == nil {
		t.Fatal("want a refusal for an unconfigured role, got a run")
	}
	if !strings.Contains(err.Error(), "implementer") {
		t.Errorf("want the refusal to name the role, got %q", err)
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
		Roles: map[fsm.RoleName]fsm.Role{"implementer": {Agent: "claude"}},
	}

	_, err := r.Run(context.Background(), runningState("T-9"), fsm.Stage{ID: "build", Role: "implementer"})
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
		Roles:     map[fsm.RoleName]fsm.Role{"qa": {Agent: "claude"}},
		Artifacts: &memoryArtifacts{},
		Stored: func(_, stage, artifact string) (string, error) {
			asked = stage + "/" + artifact
			return "abc123", nil
		},
	}

	stage := fsm.Stage{
		ID: "qa", Role: "qa",
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
		Roles:     map[fsm.RoleName]fsm.Role{"qa": {Agent: "claude"}},
		Artifacts: &memoryArtifacts{},
		Stored: func(_, _, _ string) (string, error) {
			return "", errors.New("no such artifact")
		},
	}

	stage := fsm.Stage{
		ID: "qa", Role: "qa",
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
		ID: "spec", Role: "specifier",
		Produces: []fsm.Artifact{"contract"},
		Verifiers: map[fsm.Artifact]fsm.Verifier{
			"contract": fsm.Existence{Handover: true},
		},
	}

	brief := Brief(runningState("T-12"), stage, fsm.Role{Agent: "claude"})

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
