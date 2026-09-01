package node

import (
	"context"
	"errors"
	"fmt"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"github.com/brunoomariano/luna/src/internal/agent"
	"github.com/brunoomariano/luna/src/internal/fsm"
	"github.com/brunoomariano/luna/src/internal/lead"
)

// Runner runs one stage: opens a worktree, calls the agent inside the sandbox,
// verifies what came back, and reports what it cost.
//
// It replaces conducting a stage through a terminal multiplexer. The agent is a
// subprocess that answers on stdout, so there is no pane to read, no session to
// keep alive and nothing to reap when a stage dies.
type Runner struct {
	// Repo is the repository every worktree is a sibling of, and the checkout
	// verification reads the delivery from.
	Repo string

	// Bootstrap is the project's step between `git clone` and "the tests run",
	// run in every fresh worktree before the stage starts. Empty means the
	// repository needs none.
	//
	// Per stage rather than once, because a worktree is per stage: it is opened
	// clean and removed when the stage ends, so whatever the first one installed
	// is not there for the second. That costs an install per stage, and the
	// alternative costs a stage.
	Bootstrap string

	// BootstrapTimeout bounds it. Zero means BootstrapTimeout — a field so a test
	// can reach the branch without waiting fifteen minutes for it.
	BootstrapTimeout time.Duration

	// Agent runs the agent call. An interface so a test substitutes a named fake
	// for a real process.
	Agent agent.Runner

	// AgentOverride pins every agent-bearing stage to one harness for this run.
	// It is applied to the call, not Flow: Agent participates in the flow
	// fingerprint, and an invocation flag must not rewrite recorded identity.
	AgentOverride string

	// Artifacts opens the store a stage's handover is written to.
	//
	// A factory rather than an instance because the store is scoped to one task
	// and one point in its history, and the runner learns both only when a stage
	// runs. Nil means no stage may declare a handover, which is refused rather
	// than silently ignored.
	//
	// The sequence is what lets a stage hand the same artifact over twice. A
	// loop's second round rewrites the commit plan it wrote in the first, and the
	// store keys a version by it — passing a constant makes the second write
	// collide with the first and the round keeps the stale document.
	Artifacts func(taskID string, seq int) ArtifactStore

	// Stored answers whether an artifact reached the store, with the hash of what
	// did. Separate from Artifacts because that interface is the socket's
	// contract — what an agent may write — and this is verification asking a
	// question the agent has no part in.
	Stored func(taskID, stage, artifact string) (hash string, err error)

	// Configure records something a stage discovered as the project's own setting.
	// Nil means nothing is recorded, which is what a dry run wants.
	Configure func(key, value string) error

	// Budget bounds one agent call. Zero leaves it to the caller's context.
	Budget time.Duration

	// Flow is the task's flow, for resolving which stage belongs to which role
	// when a live stage asks what session to continue.
	//
	// It replaced a `Sessions` map on this struct. A map lives as long as the
	// process, and the shipped flow puts a gate in the middle of the maker's run
	// — answering it ends the process, so `build` started cold every time.
	// Measured on TALLY-3: one resumed pair out of three. The session id is in
	// the log now, and this is how it is found.
	Flow []fsm.Stage

	// Brief overrides what an agent is told. Injected for tests; production
	// leaves it nil and Brief below is what runs.
	Brief func(state fsm.TaskState, stage fsm.Stage) string

	// Warn reports something that went wrong without failing the stage — a
	// worktree that would not go away, a socket that would not close. Nil
	// discards, which is what a caller with nowhere to print has.
	Warn func(format string, args ...any)
}

// Run executes one stage and returns what it delivered.
func (r *Runner) Run(ctx context.Context, state fsm.TaskState, stage fsm.Stage) (lead.Result, error) {
	stage, err := r.stageForRun(state, stage)
	if err != nil {
		return lead.Result{}, err
	}

	wt, err := r.openWorktree(ctx, state, stage)
	if err != nil {
		return lead.Result{}, err
	}
	// The worktree lasts exactly as long as the stage. A failure to remove it
	// does not fail the stage: the work is committed by then, and turning "the
	// stage delivered" into "the stage failed" over a directory would lose the
	// more important of the two.
	defer func() {
		if err := CloseWorktree(context.WithoutCancel(ctx), r.Repo, wt); err != nil {
			r.warn("could not remove the worktree for %s at %s: %v", state.ID, stage.ID, err)
		}
	}()

	// Before anything reads or runs in there. A worktree is a clean checkout, and
	// a repository whose tests need a build step first has none — so the check
	// fails on the machine rather than on the work, and says so with the wrong
	// words. This is where the step between `git clone` and "the tests run" goes.
	if err := r.bootstrap(ctx, wt.Path); err != nil {
		return lead.Result{}, err
	}

	// A mechanical stage runs no agent at all: paying a model to run git buys
	// nothing and can lose something. The verification still runs, so the stage
	// still has to prove what it produced.
	if stage.Mechanical() {
		return r.runMechanically(ctx, state, stage, wt)
	}
	return r.runAgentStage(ctx, state, stage, wt)
}

func (r *Runner) stageForRun(state fsm.TaskState, stage fsm.Stage) (fsm.Stage, error) {
	if r.AgentOverride != "" && !stage.Mechanical() {
		stage.Agent = r.AgentOverride
	}
	if err := checkStage(stage); err != nil {
		return fsm.Stage{}, err
	}
	if !stage.Mechanical() && state.BudgetUSD > 0 && !agent.ReportsCost(stage.Agent) {
		return fsm.Stage{}, fmt.Errorf("%w: harness %q does not report USD cost, so task %q's $%.2f budget cannot be enforced",
			lead.ErrInfrastructure, stage.Agent, state.ID, state.BudgetUSD)
	}
	return stage, nil
}

func (r *Runner) runAgentStage(
	ctx context.Context,
	state fsm.TaskState,
	stage fsm.Stage,
	wt Worktree,
) (lead.Result, error) {
	reportOnlyBefore, err := snapshotReportOnly(ctx, stage, wt.Path)
	if err != nil {
		return lead.Result{}, err
	}

	socket, handsOver, err := r.serveArtifacts(state, stage, wt)
	if err != nil {
		return lead.Result{}, err
	}
	if handsOver {
		defer func() {
			if err := socket.Close(); err != nil {
				r.warn("could not close the artifact socket for %s at %s: %v", state.ID, stage.ID, err)
			}
		}()
	}

	spend, said, err := r.call(ctx, state, stage, wt, handsOver)
	if unchangedErr := verifyReportOnlyUnchanged(ctx, stage, wt.Path, reportOnlyBefore); unchangedErr != nil {
		return lead.Result{Spent: spend}, unchangedErr
	}
	if err != nil {
		return lead.Result{Spent: spend}, err
	}

	// The agent stopped, whatever that means. Only the verifier says whether the
	// stage delivered.
	//
	// The report goes out before the error is returned rather than after a
	// success: a stage whose verification failed is exactly one whose reply is
	// worth keeping.
	result, err := r.verify(ctx, state, stage, wt, spend)
	r.reportEmptyDelivery(state, stage, result, said)
	r.recordBootstrap(state, stage, err)
	return result, err
}

type reportOnlySnapshot struct {
	commit string
	status string
}

func snapshotReportOnly(ctx context.Context, stage fsm.Stage, worktree string) (reportOnlySnapshot, error) {
	if !stage.Uncontained {
		return reportOnlySnapshot{}, nil
	}
	commit, _, err := Handover(ctx, worktree)
	if err != nil {
		return reportOnlySnapshot{}, err
	}
	status, err := git(ctx, worktree, "status", "--porcelain=v1", "--untracked-files=all")
	if err != nil {
		return reportOnlySnapshot{}, fmt.Errorf("reading report-only stage %q: %w", stage.ID, err)
	}
	return reportOnlySnapshot{commit: commit, status: status}, nil
}

func verifyReportOnlyUnchanged(
	ctx context.Context,
	stage fsm.Stage,
	worktree string,
	before reportOnlySnapshot,
) error {
	if !stage.Uncontained {
		return nil
	}
	after, err := snapshotReportOnly(ctx, stage, worktree)
	if err != nil {
		return err
	}
	if after == before {
		return nil
	}
	return fmt.Errorf("stage %q runs uncontained only because it is report-only, but it changed the worktree "+
		"(HEAD %s -> %s, status %q -> %q)", stage.ID, short(before.commit), short(after.commit),
		before.status, after.status)
}

// BootstrapArtifact is what `setup` hands over with the project's own preparation
// command in it.
const BootstrapArtifact fsm.Artifact = "bootstrap_command"

// recordBootstrap keeps what `setup` found, as the project's setting.
//
// The command used to be a key somebody typed into `.luna/config.toml`, and the
// stage that discovers it only ever put it in a report for a person to read — so
// Luna told you the command and then ran whatever the file said, which is two
// sources for one fact.
//
// Recorded per project rather than per task, because that is what it is: the
// lean flows run a mechanical `setup` that discovers nothing, and without this
// they would lose their preparation step entirely when the key went. Discovered
// once, by the flow that reads the project, and used by all three.
//
// Safe to record before the gate answers, because nothing runs it yet: bootstrap
// happens on the way into a stage, and `setup`'s gate opens on the way out of
// this one. The first stage that could run it is the one after a person said yes.
//
// Nothing is recorded for a stage that failed its contract: the artifact may be
// missing or half written, and a command Luna runs in every worktree from here on
// is not something to take from a stage that did not close.
//
// Best effort otherwise. A project whose setting could not be written still gets the run it
// asked for; what it loses is the preparation on the next stage, which announces
// itself as a failed check rather than as silence.
func (r *Runner) recordBootstrap(state fsm.TaskState, stage fsm.Stage, failed error) {
	if failed != nil || r.Configure == nil || r.Artifacts == nil ||
		!declares(stage, BootstrapArtifact) {
		return
	}
	body, err := r.Artifacts(state.ID, state.Seq).
		GetArtifact(string(stage.ID), string(BootstrapArtifact))
	if err != nil {
		r.warn("could not read the bootstrap command %s discovered: %v", stage.ID, err)
		return
	}
	if err := r.Configure("bootstrap", strings.TrimSpace(string(body))); err != nil {
		r.warn("could not record the bootstrap command %s discovered: %v", stage.ID, err)
	}
}

// declares reports whether a stage owes this artifact.
func declares(stage fsm.Stage, artifact fsm.Artifact) bool {
	for _, owed := range stage.Produces {
		if owed == artifact {
			return true
		}
	}
	return false
}

// reportEmptyDelivery surfaces what the agent said when the stage produced
// nothing durable.
//
// On the happy path the reply is noise — the delivery speaks, and the reply is
// the agent narrating it. When a stage commits nothing, the reply is the only
// place the reason exists, and discarding it is how five stages of TALLY-5
// reported "delivered nothing" while the agent was saying, five times over, that
// git was unreachable inside the sandbox. INV-5 is the rule that was breaking.
//
// A delivery equal to the base counts as nothing: the stage handed back what it
// was given, which is the shape that failure actually took.
func (r *Runner) reportEmptyDelivery(state fsm.TaskState, stage fsm.Stage, result lead.Result, said string) {
	if said == "" {
		return
	}
	if !owesCommit(stage) {
		return
	}
	if result.Commit != "" && result.Commit != state.Base {
		return
	}
	r.warn("stage %s of %s committed nothing on top of %s; the agent said: %s",
		stage.ID, state.ID, short(state.Base), said)
}

// call runs the agent and returns what it cost.
func (r *Runner) call(
	ctx context.Context,
	state fsm.TaskState,
	stage fsm.Stage,
	wt Worktree,
	handsOver bool,
) (fsm.Spend, string, error) {
	// Read before the agent starts rather than discovered at the handover: an
	// agent whose git cannot name a committer produces nothing, and finding that
	// out afterwards means having paid for the stage first.
	identity, err := ReadCommitIdentity(ctx, r.Repo)
	if err != nil {
		return fsm.Spend{}, "", fmt.Errorf("%w: %w", lead.ErrInfrastructure, err)
	}

	call := agent.Call{
		Kind:    stage.Agent,
		Dir:     wt.Path,
		Prompt:  r.brief(state, stage),
		System:  stage.Brief,
		Deny:    denied(stage),
		Budget:  r.Budget,
		Context: agent.Fresh,
		Env:     append(identity.Env(), agent.StageEnv+"=1"),

		// The task's workstream, not the stage's and not the role's. Every agent a
		// task starts writes to one ledger, so what the planner learned is there
		// for the coder and for the next task over the same ground — and a stage
		// choosing its own would split one task's memory across several.
		//
		// Selecting is tried first and creating is the fallback, so a task that
		// asked for a new workstream opens it on its first call and selects it on
		// every one after — without Luna having to remember which was which.
		Workstream:          state.Memory.Workstream,
		MayCreateWorkstream: state.Memory.MayCreate,
		Uncontained:         stage.Uncontained,
	}
	if handsOver {
		// An absolute path now, where it used to be relative to the worktree the
		// agent was standing in. The socket left the worktree, so "relative to
		// where you are" no longer names it.
		socket := SocketFor(state.ID, string(stage.ID))
		call.Env = append(call.Env, socketEnv+"="+socket)
		// And the sandbox is asked to expose the directory holding it, without
		// which the agent's connect() answers ENOENT — measured, and the reason the
		// socket lived in the worktree until now.
		call.Reachable = filepath.Dir(socket)
	}

	// A stage asking to continue gets the session an identically briefed stage was
	// last using, read from the log. An absent one means there is nothing to
	// continue — the first stage told this — and starting fresh is the honest
	// answer rather than an error.
	if !stage.Context.Fresh() {
		if session := state.SessionOf(r.Flow, stage); session != "" {
			call.Context, call.Session = agent.Live, session
		}
	}

	result, err := r.Agent.Run(ctx, call)
	spend := spendOf(result, call.Context, call.Kind)
	if err != nil {
		// A missing harness is the machinery breaking rather than the stage
		// failing, so the lead does not spend the retry budget on a binary that
		// will keep not being there.
		if errors.Is(err, agent.ErrNoHarness) {
			return spend, "", fmt.Errorf("%w: %w", lead.ErrInfrastructure, err)
		}
		return spend, "", fmt.Errorf("stage %q: %w", stage.ID, err)
	}

	spend.Session = result.Session
	// The session goes into the spend, and from there into the log: which stage
	// continues it is the flow's decision, made after this one ran.
	return spend, result.Text, nil
}

// verify runs the contract's exit check over what the stage delivered.
func (r *Runner) verify(
	ctx context.Context,
	state fsm.TaskState,
	stage fsm.Stage,
	wt Worktree,
	spend fsm.Spend,
) (lead.Result, error) {
	commit, _, err := Handover(ctx, wt.Path)
	if err != nil {
		return lead.Result{}, fmt.Errorf("reading what stage %q delivered: %w", stage.ID, err)
	}

	// The base as well as the delivery, so a stage that owed a commit and handed
	// back what it was given is told apart from one that built on it. A pure
	// verification stage is different: the commit it received is the subject.
	base := state.Base
	if !owesCommit(stage) {
		base = ""
	}
	shell := Shell{Dir: r.Repo, Commit: commit, Base: base}
	owed := append(append([]fsm.Artifact{}, stage.Produces...), stage.ProducesForHuman...)

	result := lead.Result{
		Evidence: map[fsm.Artifact]fsm.Evidence{},
		Commit:   commit,
		Spent:    spend,
	}
	for i, artifact := range owed {
		evidence, err := r.prove(ctx, state, stage, shell, artifact, i)
		if err != nil {
			return lead.Result{}, err
		}
		result.Evidence[artifact] = evidence
		if evidence.Verdict == fsm.VerdictPassed {
			result.Delivered = append(result.Delivered, artifact)
		}
	}
	return result, nil
}

// prove observes one owed artifact, through whichever witness its contract names.
func (r *Runner) prove(
	ctx context.Context,
	state fsm.TaskState,
	stage fsm.Stage,
	shell Shell,
	artifact fsm.Artifact,
	i int,
) (fsm.Evidence, error) {
	verifier := stage.Verifiers[artifact]

	// An artifact with no declared verifier closes on existence alone — the
	// agent's word, and nothing more is claimed about it. The static check
	// reports the omission so the floor stays a choice; here it must not be a
	// nil dereference, which is what it was until a test asked for it.
	if verifier == nil {
		verifier = fsm.Existence{}
	}

	// An artifact handed to Luna is not in the commit, so the tree is the
	// wrong place to look for it — neither the contract's assumption nor the
	// agent's word can vouch for it. The store answers, and it answers with a
	// hash, which is the location the handoff has to carry.
	if existence, ok := verifier.(fsm.Existence); ok && existence.Handover {
		return r.proveHandover(state, stage, artifact), nil
	}

	evidence, err := shell.Prove(ctx, verifier, i)
	if err != nil {
		return fsm.Evidence{}, fmt.Errorf("proving %q for stage %q: %w", artifact, stage.ID, err)
	}
	return evidence, nil
}

func owesCommit(stage fsm.Stage) bool {
	owed := append(append([]fsm.Artifact{}, stage.Produces...), stage.ProducesForHuman...)
	if len(owed) == 0 {
		return true
	}
	for _, artifact := range owed {
		verifier := stage.Verifiers[artifact]
		switch typed := verifier.(type) {
		case fsm.Command:
			continue
		case fsm.Existence:
			if typed.Handover {
				continue
			}
		}
		return true
	}
	return false
}

// proveHandover asks the store whether the artifact arrived, and records the
// hash of what did.
//
// A missing store is a failed proof rather than a pass: recording a pass with
// nothing to ask would be the self-report Luna refuses everywhere else.
func (r *Runner) proveHandover(state fsm.TaskState, stage fsm.Stage, artifact fsm.Artifact) fsm.Evidence {
	failed := func(detail string) fsm.Evidence {
		return fsm.Evidence{
			Scope:      fsm.ScopeExistence,
			Verdict:    fsm.VerdictFailed,
			Detail:     detail,
			RecordedAt: state.Seq,
		}
	}
	if r.Stored == nil {
		return failed(fmt.Sprintf("%s is handed over to Luna and no store is configured to receive it", artifact))
	}

	hash, err := r.Stored(state.ID, string(stage.ID), string(artifact))
	if err != nil {
		return failed(fmt.Sprintf("%s was not handed over: %v", artifact, err))
	}
	return fsm.Evidence{
		Scope:      fsm.ScopeExistence,
		Verdict:    fsm.VerdictPassed,
		Detail:     fmt.Sprintf("handed over to Luna, %s", hash),
		RecordedAt: state.Seq,
	}
}

// socketEnv names the handover socket in the agent's environment. The CLI reads
// the same name from its own side of the boundary.
const socketEnv = "LUNA_ARTIFACT_SOCKET"

// Sandbox is what every agent is started inside.
//
// Deliberately a constant and not configuration: making it a setting would move
// the containment boundary into the same file as `editor`, where a typo turns
// into an uncontained agent that reports success.
const Sandbox = "ai-jail"

// serveArtifacts opens the handover socket when the stage declares one.
//
// A stage that declares no handover opens nothing: there is no reason to expose
// a writer to an agent that owes nothing through it.
func (r *Runner) serveArtifacts(state fsm.TaskState, stage fsm.Stage, wt Worktree) (server *ArtifactServer, opened bool, err error) {
	if !handsOver(stage) {
		return nil, false, nil
	}
	if r.Artifacts == nil {
		return nil, false, fmt.Errorf("stage %q hands an artifact to Luna and no store is configured", stage.ID)
	}

	server, err = ServeArtifacts(state.ID, string(stage.ID), r.Artifacts(state.ID, state.Seq))
	if err != nil {
		return nil, false, err
	}
	return server, true, nil
}

// handsOver reports whether any of the stage's artifacts is handed to Luna
// rather than committed.
func handsOver(stage fsm.Stage) bool {
	for _, verifier := range stage.Verifiers {
		if existence, ok := verifier.(fsm.Existence); ok && existence.Handover {
			return true
		}
	}
	return false
}

// runMechanically carries out a stage that starts no agent.
//
// Anything such a stage owes through the store is Luna's to write, because the
// handover socket an agent uses is never opened — so the writing happens here,
// before the verification rather than inside it: the check asks whether the
// artifact is there, and something has to have put it there first.
func (r *Runner) runMechanically(
	ctx context.Context, state fsm.TaskState, stage fsm.Stage, wt Worktree,
) (lead.Result, error) {
	if err := r.writeOwnReports(state, stage); err != nil {
		return lead.Result{}, err
	}
	return r.verify(ctx, state, stage, wt, fsm.Spend{})
}

// writeOwnReports puts the artifacts Luna itself produces into the store.
//
// A mechanical stage runs no agent, so the socket an agent hands artifacts over
// through is never opened. Anything such a stage owes through the store is
// therefore Luna's own work, and this is where that work happens.
//
// The list is closed and small on purpose. A general "the node can write any
// artifact" would be a second way for something to enter the store, and the value
// of one way in is that the audit knows what wrote it.
func (r *Runner) writeOwnReports(state fsm.TaskState, stage fsm.Stage) error {
	if r.Artifacts == nil {
		return nil
	}

	for _, artifact := range stage.Produces {
		existence, handed := stage.Verifiers[artifact].(fsm.Existence)
		if !handed || !existence.Handover || artifact != SetupReport {
			continue
		}
		body := []byte(SandboxReport(r.Repo))
		if err := r.Artifacts(state.ID, state.Seq).PutArtifact(string(stage.ID), string(artifact), body); err != nil {
			return fmt.Errorf("writing %s for stage %q: %w", artifact, stage.ID, err)
		}
	}
	return nil
}

// checkStage refuses a stage Luna cannot run as declared, rather than running it
// unbriefed or ungated — either of which would look like a stage that worked.
//
// The role used to be looked up in a catalogue here, and the lookup could fail.
// It cannot now: the agent, the brief and the denials are the stage's own, so
// what is left to check is the one thing a file can still get wrong.
func checkStage(stage fsm.Stage) error {
	if stage.Mechanical() {
		// A stage is mechanical because it names no agent, so "mechanical" and
		// "unrunnable" are now the same condition and only the contract tells them
		// apart. This is the runtime half of AuditAgents: a stage owing something
		// no command can produce would otherwise run as mechanical, deliver
		// nothing, and look like it worked.
		if stage.NeedsAgent() {
			return fmt.Errorf("stage %q owes %v and names no agent to produce it",
				stage.ID, append(append([]fsm.Artifact{}, stage.Produces...), stage.ProducesForHuman...))
		}
		return nil
	}
	if !agent.Known(stage.Agent) {
		return fmt.Errorf("stage %q names unknown harness %q (expected claude or codex)",
			stage.ID, stage.Agent)
	}

	// A stage that withholds capabilities on a harness Luna cannot gate stops
	// rather than running ungated. The alternative is a judging stage that keeps
	// every tool it was supposed to lose, with nothing saying so — and a review
	// written by something that could edit the work is the one failure the flow
	// cannot catch downstream.
	if stage.Gated() {
		if err := agent.CheckGating(stage.Agent, denied(stage)); err != nil {
			return fmt.Errorf("stage %q runs on %q, which denies %v: %w",
				stage.ID, stage.Agent, stage.ToolsDeny, err)
		}
	}
	return nil
}

// denied renders a stage's withheld capabilities as the tool names to remove.
func denied(stage fsm.Stage) []string {
	names := make([]string, 0, len(stage.ToolsDeny))
	for _, capability := range stage.ToolsDeny {
		names = append(names, string(capability))
	}
	return names
}

// spendOf turns a harness's report into what the log records.
func spendOf(result agent.Result, ctx agent.Context, kind string) fsm.Spend {
	return fsm.Spend{
		InputTokens:  result.Usage.InputTokens,
		OutputTokens: result.Usage.OutputTokens,
		CacheRead:    result.Usage.CacheRead,
		CacheWrite:   result.Usage.CacheWrite,
		CostUSD:      result.Usage.CostUSD,
		CostReported: result.Usage.CostReported,
		Model:        result.Usage.Model,
		Agent:        kind,
		Turns:        result.Turns,
		Context:      string(ctx),
	}
}

func (r *Runner) brief(state fsm.TaskState, stage fsm.Stage) string {
	if r.Brief != nil {
		return r.Brief(state, stage)
	}
	return Brief(state, stage)
}

func (r *Runner) warn(format string, args ...any) {
	if r.Warn == nil {
		return
	}
	r.Warn(format, args...)
}

// readableRequires is what a stage can actually fetch of what it requires.
//
// `task_id` is the root of the artifact graph — the one input no stage produces,
// because the task arrives carrying it — so no blob is ever written for it and
// `luna artifact get task_id` answers "no such artifact". Listing it under a
// line that says how to read one sent the setup stage of MAX-2 to fetch it, get
// refused, and hand the gate an open question about whether the briefing or the
// artifact store was stale. Neither was; the brief was describing the task id as
// if it were a document. It is already in the first line of the brief.
func readableRequires(stage fsm.Stage) []fsm.Artifact {
	ready := make([]fsm.Artifact, 0, len(stage.Requires))
	for _, artifact := range stage.Requires {
		if artifact == fsm.TaskID {
			continue
		}
		ready = append(ready, artifact)
	}
	return ready
}

// Brief is what an agent is told when it starts.
//
// It carries the handoff, because a fresh agent did not run the previous stage
// and has no memory of it. What crosses is pointers and the contract — never a
// prose summary of what happened, which would degrade at every hop.
//
// Generated here rather than written by an agent, which is what stops one stage
// from injecting narrative into the next.
func Brief(state fsm.TaskState, stage fsm.Stage) string {
	var b strings.Builder

	fmt.Fprintf(&b, "Task %s (%s), stage %s.\n", state.ID, state.Context.Kind, stage.ID)
	fmt.Fprintf(&b, "You are working in the directory you started in.\n")

	// What the agent is looking at is the sandbox, and it will describe it as
	// though it were the machine. Measured twice: a stage concluded "shellcheck is
	// not installed on this machine" and wrote it into a contract, and a later gate
	// rejected an obligation as unverifiable on the strength of it. Both were
	// wrong — shellcheck is installed, and the exit check runs outside the jail,
	// where it is reachable.
	//
	// Saying so costs two lines and stops a tool's absence from being recorded as a
	// fact about the project.
	fmt.Fprintf(&b, "A tool missing here is missing from the sandbox, not from the "+
		"machine. Luna runs the exit check outside it, so do not record an absence "+
		"you observe here as a fact about the project.\n\n")

	if statement := state.Statement.Description; statement != "" {
		fmt.Fprintf(&b, "What the task is about: %s\n", statement)
	}
	if design := state.Statement.Design; design != "" {
		fmt.Fprintf(&b, "How it should be approached: %s\n", design)
	}
	if acceptance := state.Statement.Acceptance; acceptance != "" {
		fmt.Fprintf(&b, "Done when: %s\n", acceptance)
	}

	if readable := readableRequires(stage); len(readable) > 0 {
		fmt.Fprintf(&b, "\nWhat you have: %s\n", join(readable))
		fmt.Fprintf(&b, "Read one with `luna artifact get <name>`.\n")
	}

	writeContractDuty(&b, stage)
	writeGateCriteria(&b, stage)

	owed := append(append([]fsm.Artifact{}, stage.Produces...), stage.ProducesForHuman...)
	if len(owed) > 0 {
		fmt.Fprintf(&b, "What you owe: %s\n", join(owed))
		fmt.Fprintf(&b, "The stage does not close without all of them.\n")
	}
	writeOutstanding(&b, state)

	// Which artifacts leave through the socket rather than the commit, named
	// individually: an agent told only that "some artifacts are handed over"
	// commits the ones it guessed wrong about, and the tree stops being clean.
	var handed []fsm.Artifact
	for _, artifact := range owed {
		if existence, ok := stage.Verifiers[artifact].(fsm.Existence); ok && existence.Handover {
			handed = append(handed, artifact)
		}
	}
	if len(handed) > 0 {
		fmt.Fprintf(&b, "\nHand these to Luna instead of committing them: %s\n", join(handed))
		fmt.Fprintf(&b, "Use `luna artifact put <name> < file` for each. Do not commit them.\n")
	}

	// Everything else is delivered by committing it, and the commit is the
	// handoff — the next stage branches from it rather than from a description.
	fmt.Fprintf(&b, "\nCommit what you produce. The commit is the handoff.\n")
	return b.String()
}

// writeOutstanding names what the previous attempt at this stage did not deliver.
//
// A retry that repeats the original contract reads as a first attempt, and the
// agent is left to work out which half it already did — so it either redoes
// everything or guesses. The engine knows exactly which artifacts are missing,
// because the exit check is what compared them, and this is where that gets said.
//
// It also names the likeliest cause. Every artifact recorded this way is one the
// stage owed, and the ones that go missing in practice are the handovers: an agent
// that commits its work and forgets a socket call has done the work and not the
// delivery.
func writeOutstanding(b *strings.Builder, state fsm.TaskState) {
	if len(state.StillOwed) == 0 {
		return
	}

	fmt.Fprintf(b, "\nYou have been here before, and %s did not arrive.\n",
		join(state.StillOwed))
	fmt.Fprintf(b, "What you already delivered is kept, so deliver only what is listed above.\n")
	fmt.Fprintf(b, "If it is handed over rather than committed, the command is the one named below.\n")
}

// writeContractDuty tells a stage handed the contract what it is for.
//
// Naming it among the inputs is not enough, and the difference was measured: on
// TALLY-7 the contract required a test pinning one of its own decisions, the
// test was never written, and the stage with every means to notice reported the
// opposite — that all three decisions were pinned by a test. Nothing had asked
// it to compare the document against what was built.
func writeContractDuty(b *strings.Builder, stage fsm.Stage) {
	if !requires(stage, "contract") {
		return
	}
	fmt.Fprintf(b, "\nThe contract is not background: it is what the delivery is judged against.\n")
	fmt.Fprintf(b, "Read it, and check what was built against every obligation it states —\n")
	fmt.Fprintf(b, "including the tests it says exist. An obligation you cannot find\n")
	fmt.Fprintf(b, "satisfied is a finding, whatever the suite says.\n")
	// The half of the gate's question that the gate could not answer. It asked
	// whether an obligation was possible to satisfy, which depends on the code —
	// and the gate is shown the contract and nothing else. Here the code exists,
	// so the question is answerable for the first time.
	fmt.Fprintf(b, "That includes whether an obligation turned out to be impossible:\n")
	fmt.Fprintf(b, "the gate could not tell, having only the document.\n")
}

// writeGateCriteria shows a stage the criteria its own artifact will be judged
// on, when it produces one that opens a gate.
//
// The symmetric half of writeContractDuty: that one tells a stage what to judge
// against, this one tells a stage what it will be judged by. Both close the same
// kind of gap — a rule enforced at one end of the flow and never stated at the
// other.
//
// Measured across four contracts. The gate rejected two of them for the same
// criterion, and both times for a sentence the maker had no reason to think was
// forbidden: "worth raising at close", and "either form satisfies the contract,
// the guarded form is preferable". Both sat in a section the second contract
// itself labelled "Note for the maker" — the natural instinct of an agent trying
// to be useful. The rest of both documents was properly imperative, so this is
// not an agent that cannot write obligations; it is one that was never told the
// document admits nothing else.
func writeGateCriteria(b *strings.Builder, stage fsm.Stage) {
	if stage.Gate == nil || len(stage.Gate.Judge) == 0 {
		return
	}

	fmt.Fprintf(b, "\nWhat you produce here opens a gate, and %s is judged on:\n",
		stage.Gate.Artifact)
	for _, criterion := range stage.Gate.Judge {
		fmt.Fprintf(b, "  - %s\n", criterion)
	}
	fmt.Fprintf(b, "These are the words it is held to, so write to them.\n")
}

// requires reports whether a stage names an artifact among its inputs.
func requires(stage fsm.Stage, artifact fsm.Artifact) bool {
	for _, required := range stage.Requires {
		if required == artifact {
			return true
		}
	}
	return false
}

func join(artifacts []fsm.Artifact) string {
	names := make([]string, 0, len(artifacts))
	for _, artifact := range artifacts {
		names = append(names, string(artifact))
	}
	return strings.Join(names, ", ")
}

// openWorktree cuts the stage's checkout, reporting a missing git as what it is.
//
// A missing git is the machinery breaking rather than the stage failing, and it
// is not a thing a retry can fix — spending the retry budget on it leaves none
// for the failure it was meant for.
func (r *Runner) openWorktree(ctx context.Context, state fsm.TaskState, stage fsm.Stage) (Worktree, error) {
	wt, err := OpenWorktree(ctx, r.Repo, state.ID, string(stage.ID), state.Base)
	if err == nil {
		return wt, nil
	}
	if errors.Is(err, exec.ErrNotFound) {
		return Worktree{}, fmt.Errorf("%w: %w", lead.ErrInfrastructure, err)
	}
	return Worktree{}, err
}
