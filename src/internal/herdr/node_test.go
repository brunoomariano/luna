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

	// verifyErr makes the verification impossible to run at all, which is a
	// different situation from a check that ran and failed.
	verifyErr error

	prompted []string
	ran      []string
	started  string
}

func (f *fakeHerdr) OpenWorktree(context.Context, string, string) (Workspace, error) {
	return Workspace{ID: "ws-1", RootPane: "pane-1", Path: "/tmp/wt"}, nil
}

func (f *fakeHerdr) StartAgent(_ context.Context, _ Workspace, kind string) (string, error) {
	f.started = kind
	return "pane-1", nil
}

func (f *fakeHerdr) Prompt(_ context.Context, _, text string) (AgentStatus, error) {
	f.prompted = append(f.prompted, text)
	if f.settlesAt == "" {
		return StatusIdle, nil
	}
	return f.settlesAt, nil
}

func (f *fakeHerdr) Verify(_ context.Context, _ Workspace, command string) (int, string, error) {
	f.ran = append(f.ran, command)
	if f.verifyErr != nil {
		return 0, "", f.verifyErr
	}
	return f.exits[command], "output line\nsecond line", nil
}

// stageWithTests is a stage that produces one artifact proven by a real command.
func stageWithTests() fsm.Stage {
	return fsm.Stage{
		ID:       "build",
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
	node := &Node{Runner: herdr, Agent: "claude"}

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
	node := &Node{Runner: herdr, Agent: "claude"}

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
	node := &Node{Runner: herdr, Agent: "claude"}

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
	node := &Node{Runner: herdr, Agent: "claude"}

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
	node := &Node{Runner: herdr, Agent: "claude"}

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
	node := &Node{Runner: herdr, Agent: "claude"}
	stage := fsm.Stage{ID: "scenarios", Produces: []fsm.Artifact{"scenarios"}}

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
	herdr := &fakeHerdr{settlesAt: StatusIdle, verifyErr: broken}
	node := &Node{Runner: herdr, Agent: "claude"}

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
	node := &Node{Runner: herdr, Agent: "claude"}
	stage := fsm.Stage{
		ID:               "verify",
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
	node := &Node{Runner: herdr, Agent: "codex"}

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

func (f *failingRunner) StartAgent(ctx context.Context, ws Workspace, kind string) (string, error) {
	if f.failAt == "start" {
		return "", f.err
	}
	return f.fakeHerdr.StartAgent(ctx, ws, kind)
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
		node := &Node{Runner: runner, Agent: "claude"}

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
		Agent:  "claude",
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
	node := &Node{Runner: herdr, Agent: "claude"}

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

// TestEvidenceDetailIsOneLine covers the trimming.
//
// A build log in an evidence record makes `luna task show` unreadable and bloats
// every replay; the summary line is what a person needs.
func TestEvidenceDetailIsOneLine(t *testing.T) {
	herdr := &fakeHerdr{settlesAt: StatusIdle}
	node := &Node{Runner: herdr, Agent: "claude"}

	result, err := node.Run(context.Background(), fsm.NewTaskState("LUNA-1", ""), stageWithTests())
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if detail := result.Evidence["tests_green"].Detail; detail != "output line" {
		t.Errorf("want just the first line, got %q", detail)
	}
}

// TestALongSingleLineIsTruncated covers the other branch of the trimming: output
// with no newline at all must not carry a whole log into the record.
func TestALongSingleLineIsTruncated(t *testing.T) {
	long := strings.Repeat("x", 500)

	if got := firstLine(long); len(got) != 200 {
		t.Errorf("want a bounded detail, got %d chars", len(got))
	}
	if got := firstLine("short"); got != "short" {
		t.Errorf("a short line is left alone, got %q", got)
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
	node := &Node{Runner: runner, Agent: "claude"}

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
	node := &Node{Runner: runner, Agent: "claude"}

	_, err := node.Run(context.Background(), fsm.NewTaskState("LUNA-1", ""), stageWithTests())

	if errors.Is(err, lead.ErrStalled) {
		t.Errorf("a busy pane is a failure, not a stall: %v", err)
	}
	if err == nil {
		t.Error("it is still an error")
	}
}
