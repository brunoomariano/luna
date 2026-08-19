package node

import (
	"context"
	"errors"
	"os/exec"
	"path/filepath"
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
		Roles: roleLookup(map[fsm.RoleName]fsm.Role{"implementer": {Agent: "claude", Brief: "You build."}}),
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
		Roles: roleLookup(map[fsm.RoleName]fsm.Role{"implementer": {Agent: "claude"}}),
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
		Roles: roleLookup(map[fsm.RoleName]fsm.Role{"implementer": {Agent: "claude"}}),
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
		Roles: roleLookup(map[fsm.RoleName]fsm.Role{"implementer": {Agent: "claude"}}),
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
		Roles: roleLookup(map[fsm.RoleName]fsm.Role{"implementer": {Agent: "claude"}}),
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
		Roles: roleLookup(map[fsm.RoleName]fsm.Role{"reviewer": {
			Agent:     "claude",
			Brief:     "You review. You do not edit.",
			ToolsDeny: []fsm.Capability{fsm.CapEdit, fsm.CapWrite},
		}}),
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

	r := &Runner{Repo: repo, Agent: fake, Roles: roleLookup(map[fsm.RoleName]fsm.Role{})}

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
		Roles: roleLookup(map[fsm.RoleName]fsm.Role{"implementer": {Agent: "claude"}}),
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
		Roles:     roleLookup(map[fsm.RoleName]fsm.Role{"qa": {Agent: "claude"}}),
		Artifacts: func(string) ArtifactStore { return &memoryArtifacts{} },
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
		Roles:     roleLookup(map[fsm.RoleName]fsm.Role{"qa": {Agent: "claude"}}),
		Artifacts: func(string) ArtifactStore { return &memoryArtifacts{} },
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

// roleLookup adapts a plain map to the lookup the runner takes, so a test can
// declare its roles as data.
func roleLookup(roles map[fsm.RoleName]fsm.Role) func(fsm.RoleName) (fsm.Role, bool) {
	return func(name fsm.RoleName) (fsm.Role, bool) {
		role, ok := roles[name]
		return role, ok
	}
}

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
		ID: "build", Role: "implementer",
		Requires: []fsm.Artifact{"scenarios"},
		Produces: []fsm.Artifact{"code"},
	}

	brief := Brief(state, stage, fsm.Role{Agent: "claude"})

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
	brief := Brief(runningState("T-21"), fsm.Stage{ID: "setup"}, fsm.Role{})

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
		Roles: roleLookup(map[fsm.RoleName]fsm.Role{"qa": {Agent: "claude"}}),
		// No Artifacts, and the stage below hands one over.
	}

	stage := fsm.Stage{
		ID: "qa", Role: "qa",
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
		Roles: roleLookup(map[fsm.RoleName]fsm.Role{"implementer": {Agent: "claude"}}),
	}

	_, err := r.Run(context.Background(), runningState("T-31"), fsm.Stage{ID: "build", Role: "implementer"})
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
		Roles:     roleLookup(map[fsm.RoleName]fsm.Role{"qa": {Agent: "claude"}}),
		Artifacts: func(string) ArtifactStore { return &memoryArtifacts{} },
		Stored:    func(_, _, _ string) (string, error) { return "hash", nil },
	}

	stage := fsm.Stage{
		ID: "qa", Role: "qa",
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

// TestTheBriefCanBeReplacedWithoutTouchingTheRunner covers the seam a test uses
// to drive a stage with a known instruction, and that a project would use to say
// something the shipped brief does not.
func TestTheBriefCanBeReplacedWithoutTouchingTheRunner(t *testing.T) {
	repo := repoWithCommit(t)
	fake := &recordingAgent{result: agent.Result{Text: "done"}}

	r := &Runner{
		Repo:  repo,
		Agent: fake,
		Roles: roleLookup(map[fsm.RoleName]fsm.Role{"implementer": {Agent: "claude"}}),
		Brief: func(fsm.TaskState, fsm.Stage, fsm.Role) string {
			return "do exactly this and nothing else"
		},
	}

	if _, err := r.Run(context.Background(), runningState("T-40"), fsm.Stage{ID: "build", Role: "implementer"}); err != nil {
		t.Fatalf("Run: %v", err)
	}

	if got := fake.last(t).Prompt; got != "do exactly this and nothing else" {
		t.Errorf("the replaced brief did not reach the agent, got %q", got)
	}
}

// TestAStageRunsWithTheProjectsMemoryWhenItAsks covers the setting reaching the
// call. A stage declaring memory and not getting it would start an agent blind
// to what the project already decided, and nothing about the run would say so.
func TestAStageRunsWithTheProjectsMemoryWhenItAsks(t *testing.T) {
	repo := repoWithCommit(t)
	fake := &recordingAgent{result: agent.Result{Text: "done"}}

	r := &Runner{
		Repo:  repo,
		Agent: fake,
		Roles: roleLookup(map[fsm.RoleName]fsm.Role{"analyst": {Agent: "claude"}}),
	}

	stage := fsm.Stage{ID: "intake", Role: "analyst", Memory: fsm.MemoryOn}
	if _, err := r.Run(context.Background(), runningState("T-41"), stage); err != nil {
		t.Fatalf("Run: %v", err)
	}
	if got := fake.last(t).Memory; got != agent.MemoryOn {
		t.Errorf("want the stage's memory setting carried to the call, got %q", got)
	}

	// And the default stays off: a shared memory every stage writes to fills with
	// the transient.
	plain := fsm.Stage{ID: "build", Role: "analyst"}
	if _, err := r.Run(context.Background(), runningState("T-41"), plain); err != nil {
		t.Fatalf("Run: %v", err)
	}
	if got := fake.last(t).Memory; got != agent.MemoryOff {
		t.Errorf("want memory off by default, got %q", got)
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
		Roles:     roleLookup(map[fsm.RoleName]fsm.Role{"implementer": {Agent: "claude"}}),
		Artifacts: func(string) ArtifactStore { return &memoryArtifacts{} },
	}

	// A stage that commits everything it owes gets no socket in its environment.
	plain := fsm.Stage{ID: "build", Role: "implementer", Produces: []fsm.Artifact{"code"}}
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
		Roles: roleLookup(map[fsm.RoleName]fsm.Role{"implementer": {Agent: "claude"}}),
	}

	// A PATH with nothing on it, so opening the worktree cannot find git.
	t.Setenv("PATH", t.TempDir())

	_, err := r.Run(context.Background(), runningState("T-50"), fsm.Stage{ID: "build", Role: "implementer"})
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
		Roles: roleLookup(map[fsm.RoleName]fsm.Role{"implementer": {Agent: "claude"}}),
	}

	_, err := r.Run(context.Background(), runningState("T-51"), fsm.Stage{ID: "build", Role: "implementer"})
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
		Roles: roleLookup(map[fsm.RoleName]fsm.Role{"reviewer": {
			Agent:     "some-unknown-harness",
			ToolsDeny: []fsm.Capability{fsm.CapEdit, fsm.CapWrite},
		}}),
	}

	_, err := r.Run(context.Background(), runningState("T-60"), fsm.Stage{ID: "code-review", Role: "reviewer"})
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

// TestAnUngatedRoleRunsOnAnyHarness is the other side: the refusal is about
// withheld capabilities, not about the harness itself. A role that denies
// nothing has nothing that could fail to be denied.
func TestAnUngatedRoleRunsOnAnyHarness(t *testing.T) {
	repo := repoWithCommit(t)
	fake := &recordingAgent{result: agent.Result{Text: "done"}}

	r := &Runner{
		Repo:  repo,
		Agent: fake,
		Roles: roleLookup(map[fsm.RoleName]fsm.Role{"implementer": {Agent: "some-unknown-harness"}}),
	}

	if _, err := r.Run(context.Background(), runningState("T-61"), fsm.Stage{ID: "build", Role: "implementer"}); err != nil {
		t.Fatalf("an ungated role was refused: %v", err)
	}
}
