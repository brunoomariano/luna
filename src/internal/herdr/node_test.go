package herdr

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"testing"

	"github.com/brunoomariano/luna/src/internal/fsm"
	"github.com/brunoomariano/luna/src/internal/lead"
)

// fakeHerdr stands in for a running herdr. Named rather than inline because the
// house rule is that an external boundary gets a named fake — and because every
// test here is about what Luna does with what herdr said, so the answers have to
// be set per case.
type fakeHerdr struct {
	settlesAt AgentStatus

	// exits maps a command to the code it returns. Absent means zero.
	exits map[string]int

	prompted []string
	ran      []string
	started  string

	// startArgs is what was passed through to the agent, which is how a denied
	// capability reaches it (ADR-0042).
	startArgs []string
}

func (f *fakeHerdr) OpenWorktree(context.Context, string, string) (Workspace, error) {
	return Workspace{ID: "ws-1", RootPane: "pane-1", Path: "/tmp/wt"}, nil
}

func (f *fakeHerdr) StartAgent(_ context.Context, _ Workspace, kind, _ string, args []string) (string, error) {
	f.started = kind
	f.startArgs = args
	return "pane-1", nil
}

func (f *fakeHerdr) Prompt(_ context.Context, _, text string) (AgentStatus, error) {
	f.prompted = append(f.prompted, text)
	if f.settlesAt == "" {
		return StatusIdle, nil
	}
	return f.settlesAt, nil
}

// recordingProver runs the contract's checks the way the node package would, and
// remembers what it was asked to run so a test can assert that a check happened
// — or did not.
type recordingProver struct {
	exits *map[string]int
	ran   *[]string
}

func (r recordingProver) Prove(_ context.Context, v fsm.Verifier, seq int) (fsm.Evidence, error) {
	command, ok := v.(fsm.Command)
	if !ok {
		return fsm.Evidence{Scope: v.Proves(), Verdict: fsm.VerdictPassed, RecordedAt: seq}, nil
	}

	*r.ran = append(*r.ran, command.Run)

	verdict := fsm.VerdictPassed
	exit := (*r.exits)[command.Run]
	if exit != 0 {
		verdict = fsm.VerdictFailed
	}
	return fsm.Evidence{
		Scope:      command.Proves(),
		Verdict:    verdict,
		Command:    command.Run,
		ExitCode:   exit,
		RecordedAt: seq,
	}, nil
}

// proving wires a node to a recordingProver backed by this fake's answers.
func (f *fakeHerdr) proving() func(Workspace) Prover {
	if f.exits == nil {
		f.exits = map[string]int{}
	}
	return func(Workspace) Prover {
		return recordingProver{exits: &f.exits, ran: &f.ran}
	}
}

// brokenProver stands in for a check that cannot run at all — a missing tool, an
// unreadable worktree. Distinct from a check that ran and failed, which is a
// verdict rather than an error.
type brokenProver struct{ err error }

func (b brokenProver) Prove(context.Context, fsm.Verifier, int) (fsm.Evidence, error) {
	return fsm.Evidence{}, b.err
}

// fixedRole resolves every role to one agent, which is what a test that is not
// about role resolution wants.
func fixedRole(agent string) func(fsm.RoleName) (fsm.Role, bool) {
	return func(fsm.RoleName) (fsm.Role, bool) {
		return fsm.Role{Agent: agent}, true
	}
}

// stageWithTests is a stage that produces one artifact proven by a real command.
func stageWithTests() fsm.Stage {
	return fsm.Stage{
		ID:       "build",
		Role:     "implementer",
		Produces: []fsm.Artifact{"tests_green"},
		Verifiers: map[fsm.Artifact]fsm.Verifier{
			"tests_green": fsm.Command{Run: "go test ./...", Scope: fsm.ScopeFull},
		},
	}
}

// TestAPassingCommandBecomesPassingEvidence is the happy path of ADR-0028: the
// agent settled, the real tool ran, and the verdict is what closes the stage.
func TestAPassingCommandBecomesPassingEvidence(t *testing.T) {
	herdr := &fakeHerdr{settlesAt: StatusIdle}
	node := &Node{Runner: herdr, Roles: fixedRole("claude"), Prove: herdr.proving()}

	result, err := node.Run(context.Background(), fsm.NewTaskState("LUNA-1", ""), stageWithTests())
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	evidence := result.Evidence["tests_green"]
	if !evidence.Passing() {
		t.Errorf("a zero exit is passing evidence, got %+v", evidence)
	}
	if evidence.Scope != fsm.ScopeFull {
		t.Errorf("want the scope the verifier declared, got %q", evidence.Scope)
	}
	if evidence.Command != "go test ./..." {
		t.Errorf("the evidence names what ran, got %q", evidence.Command)
	}
	if len(herdr.ran) != 1 {
		t.Errorf("the real tool runs exactly once, got %v", herdr.ran)
	}
}

// TestIdleDoesNotCloseAStageOnItsOwn is the finding the whole study turns on.
//
// herdr says the agent stopped moving and the command says the tests failed. The
// command wins: `Idle` means "prompt visible", never "it worked" (ADR-0028).
func TestIdleDoesNotCloseAStageOnItsOwn(t *testing.T) {
	herdr := &fakeHerdr{
		settlesAt: StatusIdle,
		exits:     map[string]int{"go test ./...": 1},
	}
	node := &Node{Runner: herdr, Roles: fixedRole("claude"), Prove: herdr.proving()}

	result, err := node.Run(context.Background(), fsm.NewTaskState("LUNA-1", ""), stageWithTests())
	if err != nil {
		t.Fatalf("a failing check is a verdict, not an error: %v", err)
	}

	evidence := result.Evidence["tests_green"]
	if evidence.Passing() {
		t.Error("the agent going idle must not turn a failing command into a pass")
	}
	if evidence.Verdict != fsm.VerdictFailed {
		t.Errorf("want a failed verdict, got %q", evidence.Verdict)
	}
	if evidence.ExitCode != 1 {
		t.Errorf("the exit code is part of the evidence, got %d", evidence.ExitCode)
	}
}

// TestDoneIsNotAVerdictEither covers herdr's other misleading status.
//
// `done` is idle that nobody has looked at yet — it decays to `idle` when a human
// focuses the tab. A status that changes because someone looked at it cannot
// close a stage.
func TestDoneIsNotAVerdictEither(t *testing.T) {
	herdr := &fakeHerdr{
		settlesAt: StatusDone,
		exits:     map[string]int{"go test ./...": 2},
	}
	node := &Node{Runner: herdr, Roles: fixedRole("claude"), Prove: herdr.proving()}

	result, err := node.Run(context.Background(), fsm.NewTaskState("LUNA-1", ""), stageWithTests())
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if result.Evidence["tests_green"].Passing() {
		t.Error("`done` is a UI flag, not a verdict on the work")
	}
}

// TestUnknownStillVerifies covers the status herdr itself says proves nothing.
//
// A known agent matching no detection rule falls back to a status Luna must not
// read as success. It settles, so the check runs — and the check decides.
func TestUnknownStillVerifies(t *testing.T) {
	herdr := &fakeHerdr{settlesAt: StatusUnknown}
	node := &Node{Runner: herdr, Roles: fixedRole("claude"), Prove: herdr.proving()}

	result, err := node.Run(context.Background(), fsm.NewTaskState("LUNA-1", ""), stageWithTests())
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if len(herdr.ran) != 1 {
		t.Error("an unknown status still triggers the verification")
	}
	if !result.Evidence["tests_green"].Passing() {
		t.Error("the command passed, so the stage has its evidence")
	}
}

// TestABlockedAgentIsReportedForEscalation covers ADR-0029.
//
// The agent is asking a person for something the flow did not foresee. That is
// not a verification outcome, so it comes back as an error for the lead to
// escalate — and the message names the pane so someone can find it.
func TestABlockedAgentIsReportedForEscalation(t *testing.T) {
	herdr := &fakeHerdr{settlesAt: StatusBlocked}
	node := &Node{Runner: herdr, Roles: fixedRole("claude"), Prove: herdr.proving()}

	_, err := node.Run(context.Background(), fsm.NewTaskState("LUNA-1", ""), stageWithTests())

	if err == nil {
		t.Fatal("a blocked agent must be escalated, not treated as a delivery")
	}
	if !strings.Contains(err.Error(), "pane-1") {
		t.Errorf("the error should name the pane a person has to go to, got %v", err)
	}
	if len(herdr.ran) != 0 {
		t.Error("nothing was delivered, so nothing should have been verified")
	}
}

// TestAnArtifactWithNoVerifierClosesOnExistence covers the honest floor of
// ADR-0032: prose has no exit code, and saying so beats inventing a check.
func TestAnArtifactWithNoVerifierClosesOnExistence(t *testing.T) {
	herdr := &fakeHerdr{settlesAt: StatusIdle}
	node := &Node{Runner: herdr, Roles: fixedRole("claude"), Prove: herdr.proving()}
	stage := fsm.Stage{ID: "scenarios", Role: "gherkin", Produces: []fsm.Artifact{"scenarios"}}

	result, err := node.Run(context.Background(), fsm.NewTaskState("LUNA-1", ""), stage)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	evidence := result.Evidence["scenarios"]
	if evidence.Scope != fsm.ScopeExistence {
		t.Errorf("prose is proven by existence, got scope %q", evidence.Scope)
	}
	if !evidence.Passing() {
		t.Error("existence still closes the stage; it just does not claim a check ran")
	}
	if len(herdr.ran) != 0 {
		t.Errorf("nothing should have been executed, got %v", herdr.ran)
	}
}

// TestAnUnrunnableCheckIsNotAFailedCheck covers the distinction that keeps the
// audit honest.
//
// A missing tool or a dead socket means the verification could not happen.
// Recording that as a failing test would tell the audit the tests ran and lost.
func TestAnUnrunnableCheckIsNotAFailedCheck(t *testing.T) {
	broken := errors.New("no such tool")
	herdr := &fakeHerdr{settlesAt: StatusIdle}
	node := &Node{
		Runner: herdr,
		Roles:  fixedRole("claude"),
		Prove:  func(Workspace) Prover { return brokenProver{err: broken} },
	}

	_, err := node.Run(context.Background(), fsm.NewTaskState("LUNA-1", ""), stageWithTests())

	if !errors.Is(err, broken) {
		t.Errorf("a check that could not run is an error, not a verdict, got %v", err)
	}
}

// TestTheHumanReportIsVerifiedToo covers INV-core-11 at this layer: an audit
// report has no consumer downstream, so nothing would ever miss it if the node
// quietly skipped it.
func TestTheHumanReportIsVerifiedToo(t *testing.T) {
	herdr := &fakeHerdr{settlesAt: StatusIdle}
	node := &Node{Runner: herdr, Roles: fixedRole("claude"), Prove: herdr.proving()}
	stage := fsm.Stage{
		ID:               "verify",
		Role:             "verifier",
		Produces:         []fsm.Artifact{"ci_green"},
		ProducesForHuman: []fsm.Artifact{"dod_checked"},
	}

	result, err := node.Run(context.Background(), fsm.NewTaskState("LUNA-1", ""), stage)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	for _, artifact := range []fsm.Artifact{"ci_green", "dod_checked"} {
		if !result.Evidence[artifact].Delivered() {
			t.Errorf("%q owes evidence like any other product", artifact)
		}
	}
}

// TestTheAgentKindIsWhatWasConfigured covers ADR-0031: the kind comes from
// herdr's allowlist, and the node passes through what it was given.
func TestTheAgentKindIsWhatWasConfigured(t *testing.T) {
	herdr := &fakeHerdr{settlesAt: StatusIdle}
	node := &Node{Runner: herdr, Roles: fixedRole("codex")}

	if _, err := node.Run(context.Background(), fsm.NewTaskState("LUNA-1", ""), stageWithTests()); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if herdr.started != "codex" {
		t.Errorf("want the configured agent kind, got %q", herdr.started)
	}
}

// TestSettledNamesTheStatusesWorthCheckingOn covers the trigger rule directly.
func TestSettledNamesTheStatusesWorthCheckingOn(t *testing.T) {
	cases := map[AgentStatus]bool{
		StatusIdle:    true,
		StatusDone:    true,
		StatusUnknown: true,
		StatusWorking: false,
		StatusBlocked: false,
	}

	for status, want := range cases {
		if got := status.Settled(); got != want {
			t.Errorf("%q: want settled=%v, got %v", status, want, got)
		}
	}
}

// TestNodeSatisfiesTheLeadBoundary is the compile-time check that ADR-0030 holds.
//
// If this stops compiling, herdr has stopped being replaceable by another
// implementation, and that is a design regression rather than a build error.
func TestNodeSatisfiesTheLeadBoundary(t *testing.T) {
	var _ lead.Node = &Node{Runner: &fakeHerdr{}}
}

// failingRunner refuses at one named step, so each early return in Run can be
// exercised for what it is: an infrastructure failure, not a stage verdict.
type failingRunner struct {
	fakeHerdr
	failAt string
	err    error
}

func (f *failingRunner) OpenWorktree(ctx context.Context, id, branch string) (Workspace, error) {
	if f.failAt == "worktree" {
		return Workspace{}, f.err
	}
	return f.fakeHerdr.OpenWorktree(ctx, id, branch)
}

func (f *failingRunner) StartAgent(ctx context.Context, ws Workspace, kind, name string, args []string) (string, error) {
	if f.failAt == "start" {
		return "", f.err
	}
	return f.fakeHerdr.StartAgent(ctx, ws, kind, name, args)
}

func (f *failingRunner) Prompt(ctx context.Context, pane, text string) (AgentStatus, error) {
	if f.failAt == "prompt" {
		return "", f.err
	}
	return f.fakeHerdr.Prompt(ctx, pane, text)
}

// TestAnInfrastructureFailureStopsBeforeVerifying covers the three early returns.
//
// None of these is a statement about the work: the worktree could not be made,
// the agent would not start, the prompt did not land. Reporting any of them as a
// delivery would put a verdict in the log that nothing produced.
func TestAnInfrastructureFailureStopsBeforeVerifying(t *testing.T) {
	for _, step := range []string{"worktree", "start", "prompt"} {
		broken := errors.New("herdr said no at " + step)
		runner := &failingRunner{failAt: step, err: broken}
		node := &Node{Runner: runner, Roles: fixedRole("claude"), Prove: runner.proving()}

		_, err := node.Run(context.Background(), fsm.NewTaskState("LUNA-1", ""), stageWithTests())

		if !errors.Is(err, broken) {
			t.Errorf("%s: want the failure surfaced, got %v", step, err)
		}
		if len(runner.ran) != 0 {
			t.Errorf("%s: nothing ran, so nothing should have been verified", step)
		}
	}
}

// TestTheConfiguredPromptIsWhatTheAgentGets covers the injected wording.
//
// The prompt is configuration, not code: a project says how it talks to its
// agents without editing the node.
func TestTheConfiguredPromptIsWhatTheAgentGets(t *testing.T) {
	herdr := &fakeHerdr{settlesAt: StatusIdle}
	node := &Node{
		Runner: herdr,
		Roles:  fixedRole("claude"),
		Prove:  herdr.proving(),
		Prompt: func(state fsm.TaskState, stage fsm.Stage) string {
			return "custom brief for " + string(stage.ID)
		},
	}

	if _, err := node.Run(context.Background(), fsm.NewTaskState("LUNA-1", ""), stageWithTests()); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if len(herdr.prompted) != 1 || herdr.prompted[0] != "custom brief for build" {
		t.Errorf("want the configured prompt, got %v", herdr.prompted)
	}
}

// TestTheDefaultPromptNamesTheTaskAndStage covers the fallback wording, which has
// to be useful enough that a missing configuration is not a silent mystery.
func TestTheDefaultPromptNamesTheTaskAndStage(t *testing.T) {
	herdr := &fakeHerdr{settlesAt: StatusIdle}
	node := &Node{Runner: herdr, Roles: fixedRole("claude"), Prove: herdr.proving()}

	if _, err := node.Run(context.Background(), fsm.NewTaskState("LUNA-9", ""), stageWithTests()); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if len(herdr.prompted) != 1 {
		t.Fatalf("want one prompt, got %v", herdr.prompted)
	}
	for _, want := range []string{"LUNA-9", "build"} {
		if !strings.Contains(herdr.prompted[0], want) {
			t.Errorf("the default prompt should name %q, got %q", want, herdr.prompted[0])
		}
	}
}

// TestAStallIsTranslatedIntoLunasVocabulary covers ADR-0034's boundary rule.
//
// herdr answers `agent_prompt_stalled`. Nothing above this package should have to
// know that code, so it comes out as lead.ErrStalled — and the lead turns it into
// a block without asking a model (ADR-0030).
func TestAStallIsTranslatedIntoLunasVocabulary(t *testing.T) {
	runner := &failingRunner{
		failAt: "prompt",
		err:    &apiError{Code: "agent_prompt_stalled", Message: "no observed state change within 5000 ms"},
	}
	node := &Node{Runner: runner, Roles: fixedRole("claude"), Prove: runner.proving()}

	_, err := node.Run(context.Background(), fsm.NewTaskState("LUNA-1", ""), stageWithTests())

	if !errors.Is(err, lead.ErrStalled) {
		t.Errorf("a stall must reach the lead as its own condition, got %v", err)
	}
	if !strings.Contains(err.Error(), "build") {
		t.Errorf("the error should name the stage that stalled, got %v", err)
	}
	if len(runner.ran) != 0 {
		t.Error("an agent that never reacted delivered nothing to verify")
	}
}

// TestStalledReadsTheCodeNotTheMessage covers why the detection keys on herdr's
// error code: a reworded message must not silently stop being recognised.
func TestStalledReadsTheCodeNotTheMessage(t *testing.T) {
	if !Stalled(&apiError{Code: "agent_prompt_stalled", Message: "anything at all"}) {
		t.Error("the code is what identifies a stall")
	}
	if Stalled(&apiError{Code: "target_busy", Message: "agent_prompt_stalled"}) {
		t.Error("a message that merely mentions the code is not a stall")
	}
	if !Stalled(fmt.Errorf("wrapped: %w", lead.ErrStalled)) {
		t.Error("an already-translated stall is still a stall")
	}
	if Stalled(errors.New("something else")) {
		t.Error("an unrelated error is not a stall")
	}
}

// TestAnOrdinaryFailureIsNotAStall covers the other side of the distinction.
//
// A stall blocks without spending the retry budget; a failure goes to the judge.
// Confusing them would misroute both.
func TestAnOrdinaryFailureIsNotAStall(t *testing.T) {
	runner := &failingRunner{
		failAt: "prompt",
		err:    &apiError{Code: "target_busy", Message: "the pane is occupied"},
	}
	node := &Node{Runner: runner, Roles: fixedRole("claude"), Prove: runner.proving()}

	_, err := node.Run(context.Background(), fsm.NewTaskState("LUNA-1", ""), stageWithTests())

	if errors.Is(err, lead.ErrStalled) {
		t.Errorf("a busy pane is a failure, not a stall: %v", err)
	}
	if err == nil {
		t.Error("it is still an error")
	}
}

// TestTheAgentNameFitsHerdrsRules covers the constraints a live herdr enforces.
//
// Names are unique across the whole server and must match [a-z][a-z0-9_-]{0,31}.
// Both were learned by being refused: `agent_name_taken` on the second task, then
// `invalid_agent_name` on an id with capitals in it.
func TestTheAgentNameFitsHerdrsRules(t *testing.T) {
	cases := map[string]string{
		"LUNA-1":                             "luna-luna-1-build",
		"feature/big-thing":                  "luna-feature-big-thing-build",
		"AAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAA": "luna-aaaaaaaaaaaaaaaaaaaaaaaaaaa",
	}

	for taskID, want := range cases {
		got := agentName(taskID, "build")
		if got != want {
			t.Errorf("%q → %q, want %q", taskID, got, want)
		}
		if len(got) > 32 {
			t.Errorf("%q produced %d characters, over herdr's limit", taskID, len(got))
		}
		for i, r := range got {
			ok := (r >= 'a' && r <= 'z') || (r >= '0' && r <= '9') || r == '-' || r == '_'
			first := r >= 'a' && r <= 'z'
			if !ok || (i == 0 && !first) {
				t.Errorf("%q produced %q, which herdr refuses", taskID, got)
				break
			}
		}
	}

	// Different tasks must not collide: the name is what herdr keys on.
	if agentName("LUNA-1", "build") == agentName("LUNA-2", "build") {
		t.Error("two tasks must not share an agent name")
	}

	// Nor may two stages of the same task. An agent is born for its stage now
	// (ADR-0039), and herdr refusing a repeated name is what makes a collision
	// loud instead of silently reusing the previous stage's agent.
	if agentName("LUNA-1", "build") == agentName("LUNA-1", "code-review") {
		t.Error("two stages must not share an agent name")
	}
}

// TestAMechanicalStageStartsNoAgent is the point of ADR-0040.
//
// `setup` is a worktree and `commit` is git. Starting a model to run those pays
// tokens for something deterministic and lets it fail creatively at something
// Luna would get right.
func TestAMechanicalStageStartsNoAgent(t *testing.T) {
	herdr := &fakeHerdr{settlesAt: StatusIdle}
	node := &Node{Runner: herdr, Roles: fixedRole("claude"), Prove: herdr.proving()}
	stage := fsm.Stage{ID: "setup", Produces: []fsm.Artifact{"worktree"}}

	result, err := node.Run(context.Background(), fsm.NewTaskState("LUNA-1", ""), stage)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if herdr.started != "" {
		t.Errorf("no agent should have started, got %q", herdr.started)
	}
	if len(herdr.prompted) != 0 {
		t.Errorf("nothing should have been prompted, got %v", herdr.prompted)
	}
	// It still has to prove what it produced: mechanical is not exempt from the
	// contract, it is exempt from the agent.
	if !result.Evidence["worktree"].Delivered() {
		t.Error("a mechanical stage still owes evidence")
	}
}

// TestAMechanicalStageStillRunsItsVerifier covers the half that is easy to lose.
//
// `verify` running the pipeline is mechanical and is exactly where the evidence
// has to come from a real exit code (INV-core-4).
func TestAMechanicalStageStillRunsItsVerifier(t *testing.T) {
	herdr := &fakeHerdr{settlesAt: StatusIdle}
	node := &Node{Runner: herdr, Roles: fixedRole("claude"), Prove: herdr.proving()}
	stage := fsm.Stage{
		ID:       "commit",
		Produces: []fsm.Artifact{"commit_sha"},
		Verifiers: map[fsm.Artifact]fsm.Verifier{
			"commit_sha": fsm.Command{Run: "git rev-parse HEAD", Scope: fsm.ScopeFull},
		},
	}

	if _, err := node.Run(context.Background(), fsm.NewTaskState("LUNA-1", ""), stage); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if len(herdr.ran) != 1 || herdr.ran[0] != "git rev-parse HEAD" {
		t.Errorf("the verifier must run even with no agent, got %v", herdr.ran)
	}
}

// TestTheStagesRoleDecidesTheAgent covers the lookup that replaced the fixed
// field.
func TestTheStagesRoleDecidesTheAgent(t *testing.T) {
	herdr := &fakeHerdr{settlesAt: StatusIdle}
	node := &Node{
		Runner: herdr,
		Prove:  herdr.proving(),
		Roles: func(name fsm.RoleName) (fsm.Role, bool) {
			if name != "implementer" {
				t.Errorf("want the stage's own role, got %q", name)
			}
			return fsm.Role{Agent: "codex"}, true
		},
	}

	if _, err := node.Run(context.Background(), fsm.NewTaskState("LUNA-1", ""), stageWithTests()); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if herdr.started != "codex" {
		t.Errorf("want the role's agent, got %q", herdr.started)
	}
}

// TestARoleThatResolvesToNothingStopsTheStage covers the refusal.
//
// Running the stage on some fallback agent would produce work attributed to a
// role nobody defined — worse than stopping, because it looks like it worked.
func TestARoleThatResolvesToNothingStopsTheStage(t *testing.T) {
	herdr := &fakeHerdr{settlesAt: StatusIdle}

	for name, node := range map[string]*Node{
		"no roles configured": {Runner: herdr, Prove: herdr.proving()},
		"role not found": {
			Runner: herdr,
			Prove:  herdr.proving(),
			Roles:  func(fsm.RoleName) (fsm.Role, bool) { return fsm.Role{}, false },
		},
		"role names no agent": {
			Runner: herdr,
			Prove:  herdr.proving(),
			Roles:  func(fsm.RoleName) (fsm.Role, bool) { return fsm.Role{}, true },
		},
	} {
		_, err := node.Run(context.Background(), fsm.NewTaskState("LUNA-1", ""), stageWithTests())

		if err == nil {
			t.Errorf("%s: the stage must stop rather than guess", name)
			continue
		}
		if !strings.Contains(err.Error(), "implementer") {
			t.Errorf("%s: the error should name the role, got %v", name, err)
		}
	}
}

// TestThePromptCarriesTheHandoff covers what an agent is told when it starts.
//
// The agent is new: it did not run the previous stage. What crosses is the
// contract and pointers, generated by Luna — never a prose summary of what
// happened, which would degrade at every hop (INV-core-6).
func TestThePromptCarriesTheHandoff(t *testing.T) {
	herdr := &fakeHerdr{settlesAt: StatusIdle}
	node := &Node{
		Runner: herdr,
		Prove:  herdr.proving(),
		Roles: func(fsm.RoleName) (fsm.Role, bool) {
			return fsm.Role{Agent: "claude", Brief: "You make the scenarios pass."}, true
		},
	}

	stage := fsm.Stage{
		ID:       "build",
		Role:     "implementer",
		Requires: []fsm.Artifact{"scenarios"},
		Produces: []fsm.Artifact{"tests_green"},
		Verifiers: map[fsm.Artifact]fsm.Verifier{
			"tests_green": fsm.Command{Run: "go test ./...", Scope: fsm.ScopeFull},
		},
	}

	state := fsm.NewTaskState("LUNA-1", fsm.KindFeature)
	state.Evidence["scenarios"] = fsm.Exists(1)

	if _, err := node.Run(context.Background(), state, stage); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(herdr.prompted) != 1 {
		t.Fatalf("want one prompt, got %v", herdr.prompted)
	}

	prompt := herdr.prompted[0]
	for _, want := range []string{
		"You make the scenarios pass.", // the role's brief
		"LUNA-1",                       // which task
		"build",                        // which stage
		"scenarios",                    // what it may read
		"tests_green",                  // what it owes
		"go test ./...",                // what it will be held to
	} {
		if !strings.Contains(prompt, want) {
			t.Errorf("the prompt must carry %q, got:\n%s", want, prompt)
		}
	}
}

// TestThePromptNamesTheScopeOfWhatItInherits covers the detail that keeps an
// agent from over-trusting its input.
//
// Reading an artifact proven by a full suite and one that merely exists are
// different situations, and the scope is what says which (ADR-0032).
func TestThePromptNamesTheScopeOfWhatItInherits(t *testing.T) {
	herdr := &fakeHerdr{settlesAt: StatusIdle}
	node := &Node{Runner: herdr, Roles: fixedRole("claude"), Prove: herdr.proving()}

	stage := fsm.Stage{
		ID:       "qa",
		Role:     "qa",
		Requires: []fsm.Artifact{"ci_green"},
		Produces: []fsm.Artifact{"code"},
	}

	state := fsm.NewTaskState("LUNA-1", fsm.KindFeature)
	state.Evidence["ci_green"] = fsm.Evidence{Scope: fsm.ScopeFull, Verdict: fsm.VerdictPassed}

	if _, err := node.Run(context.Background(), state, stage); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if !strings.Contains(herdr.prompted[0], string(fsm.ScopeFull)) {
		t.Errorf("the prompt should say how its input was proven, got:\n%s", herdr.prompted[0])
	}
}
