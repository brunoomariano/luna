package node

import (
	"context"
	"errors"
	"fmt"
	"os/exec"
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

	// Agent runs the agent call. An interface so a test substitutes a named fake
	// for a real process.
	Agent agent.Runner

	// Roles resolves a stage's role to the agent that runs it, the brief that
	// opens its context and what it is denied.
	//
	// A lookup rather than a map because an override (`--agent`) applies at the
	// point of use: rebuilding a map to carry one would put the same rule in two
	// places.
	Roles func(fsm.RoleName) (fsm.Role, bool)

	// Artifacts opens the store a stage's handover is written to.
	//
	// A factory rather than an instance because the store is scoped to one task,
	// and the runner learns which task only when a stage runs. Nil means no stage
	// may declare a handover, which is refused rather than silently ignored.
	Artifacts func(taskID string) ArtifactStore

	// Stored answers whether an artifact reached the store, with the hash of what
	// did. Separate from Artifacts because that interface is the socket's
	// contract — what an agent may write — and this is verification asking a
	// question the agent has no part in.
	Stored func(taskID, stage, artifact string) (hash string, err error)

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
	Brief func(state fsm.TaskState, stage fsm.Stage, role fsm.Role) string

	// Warn reports something that went wrong without failing the stage — a
	// worktree that would not go away, a socket that would not close. Nil
	// discards, which is what a caller with nowhere to print has.
	Warn func(format string, args ...any)
}

// Run executes one stage and returns what it delivered.
func (r *Runner) Run(ctx context.Context, state fsm.TaskState, stage fsm.Stage) (lead.Result, error) {
	role, err := r.roleFor(stage)
	if err != nil {
		return lead.Result{}, err
	}

	wt, err := OpenWorktree(ctx, r.Repo, state.ID, stage.Role, state.Base)
	if err != nil {
		// A missing git is the machinery breaking rather than the stage failing,
		// and it is not a thing a retry can fix.
		if errors.Is(err, exec.ErrNotFound) {
			return lead.Result{}, fmt.Errorf("%w: %w", lead.ErrInfrastructure, err)
		}
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

	// A mechanical stage runs no agent at all: paying a model to run git buys
	// nothing and can lose something. The verification still runs, so the stage
	// still has to prove what it produced.
	if stage.Mechanical() {
		return r.verify(ctx, state, stage, wt, fsm.Spend{})
	}

	socket, handsOver, err := r.serveArtifacts(state.ID, stage, wt)
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

	spend, said, err := r.call(ctx, state, stage, role, wt, handsOver)
	if err != nil {
		return lead.Result{}, err
	}

	// The agent stopped, whatever that means. Only the verifier says whether the
	// stage delivered.
	//
	// The report goes out before the error is returned rather than after a
	// success: a stage whose verification failed is exactly one whose reply is
	// worth keeping.
	result, err := r.verify(ctx, state, stage, wt, spend)
	r.reportEmptyDelivery(state, stage, result, said)
	return result, err
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
	role fsm.Role,
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
		Kind:    role.Agent,
		Dir:     wt.Path,
		Prompt:  r.brief(state, stage, role),
		System:  role.Brief,
		Deny:    denied(role),
		Budget:  r.Budget,
		Context: agent.Fresh,
		Memory:  agent.MemoryOff,
		Env:     identity.Env(),
	}
	if stage.Memory.Enabled() {
		call.Memory = agent.MemoryOn
	}
	if handsOver {
		call.Env = append(call.Env, socketEnv+"="+SocketName)
	}

	// A stage asking to continue gets the session its role was last using, read
	// from the log. An absent one means there is nothing to continue — the first
	// stage a role runs — and starting fresh is the honest answer rather than an
	// error.
	if !stage.Context.Fresh() {
		if session := state.SessionOf(r.Flow, stage.Role); session != "" {
			call.Context, call.Session = agent.Live, session
		}
	}

	result, err := r.Agent.Run(ctx, call)
	spend := spendOf(result, call.Context)
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

	// The base as well as the delivery, so a stage that handed back what it was
	// given is told apart from one that built on it.
	shell := Shell{Dir: r.Repo, Commit: commit, Base: state.Base}
	owed := append(append([]fsm.Artifact{}, stage.Produces...), stage.ProducesForHuman...)

	result := lead.Result{
		Evidence: map[fsm.Artifact]fsm.Evidence{},
		Commit:   commit,
		Spent:    spend,
	}
	for i, artifact := range owed {
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
			evidence := r.proveHandover(state, stage, artifact)
			result.Evidence[artifact] = evidence
			if evidence.Verdict == fsm.VerdictPassed {
				result.Delivered = append(result.Delivered, artifact)
			}
			continue
		}

		evidence, err := shell.Prove(ctx, verifier, i)
		if err != nil {
			return lead.Result{}, fmt.Errorf("proving %q for stage %q: %w", artifact, stage.ID, err)
		}
		result.Evidence[artifact] = evidence
		if evidence.Verdict == fsm.VerdictPassed {
			result.Delivered = append(result.Delivered, artifact)
		}
	}
	return result, nil
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
func (r *Runner) serveArtifacts(taskID string, stage fsm.Stage, wt Worktree) (server *ArtifactServer, opened bool, err error) {
	if !handsOver(stage) {
		return nil, false, nil
	}
	if r.Artifacts == nil {
		return nil, false, fmt.Errorf("stage %q hands an artifact to Luna and no store is configured", stage.ID)
	}

	server, err = ServeArtifacts(wt.Path, string(stage.ID), r.Artifacts(taskID))
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

// roleFor resolves the stage's role. An unknown one stops the stage rather than
// running it unbriefed and ungated, which would look like a stage that worked.
func (r *Runner) roleFor(stage fsm.Stage) (fsm.Role, error) {
	if stage.Mechanical() {
		return fsm.Role{}, nil
	}
	if r.Roles == nil {
		return fsm.Role{}, fmt.Errorf("stage %q names the role %q and no roles are configured", stage.ID, stage.Role)
	}
	role, ok := r.Roles(fsm.RoleName(stage.Role))
	if !ok {
		return fsm.Role{}, fmt.Errorf("stage %q names the role %q, which is not configured", stage.ID, stage.Role)
	}

	// A role that withholds capabilities on a harness Luna cannot gate stops the
	// stage rather than running ungated. The alternative is a reviewer that keeps
	// every tool it was supposed to lose, with nothing saying so — and a review
	// written by something that could edit the work is the one failure the flow
	// cannot catch downstream.
	if role.Gated() && !agent.CanGate(role.Agent) {
		return fsm.Role{}, fmt.Errorf(
			"stage %q runs %q on %q, which denies %v — and Luna cannot withhold a capability on that harness",
			stage.ID, stage.Role, role.Agent, role.ToolsDeny,
		)
	}
	return role, nil
}

// denied renders a role's withheld capabilities as the tool names to remove.
func denied(role fsm.Role) []string {
	names := make([]string, 0, len(role.ToolsDeny))
	for _, capability := range role.ToolsDeny {
		names = append(names, string(capability))
	}
	return names
}

// spendOf turns a harness's report into what the log records.
func spendOf(result agent.Result, ctx agent.Context) fsm.Spend {
	return fsm.Spend{
		InputTokens:  result.Usage.InputTokens,
		OutputTokens: result.Usage.OutputTokens,
		CacheRead:    result.Usage.CacheRead,
		CacheWrite:   result.Usage.CacheWrite,
		CostUSD:      result.Usage.CostUSD,
		Model:        result.Usage.Model,
		Turns:        result.Turns,
		Context:      string(ctx),
	}
}

func (r *Runner) brief(state fsm.TaskState, stage fsm.Stage, role fsm.Role) string {
	if r.Brief != nil {
		return r.Brief(state, stage, role)
	}
	return Brief(state, stage, role)
}

func (r *Runner) warn(format string, args ...any) {
	if r.Warn == nil {
		return
	}
	r.Warn(format, args...)
}

// Brief is what an agent is told when it starts.
//
// It carries the handoff, because a fresh agent did not run the previous stage
// and has no memory of it. What crosses is pointers and the contract — never a
// prose summary of what happened, which would degrade at every hop.
//
// Generated here rather than written by an agent, which is what stops one stage
// from injecting narrative into the next.
func Brief(state fsm.TaskState, stage fsm.Stage, role fsm.Role) string {
	var b strings.Builder

	fmt.Fprintf(&b, "Task %s (%s), stage %s.\n", state.ID, state.Context.Kind, stage.ID)
	fmt.Fprintf(&b, "You are working in the directory you started in.\n\n")

	if statement := state.Statement.Description; statement != "" {
		fmt.Fprintf(&b, "What the task is about: %s\n", statement)
	}
	if design := state.Statement.Design; design != "" {
		fmt.Fprintf(&b, "How it should be approached: %s\n", design)
	}
	if acceptance := state.Statement.Acceptance; acceptance != "" {
		fmt.Fprintf(&b, "Done when: %s\n", acceptance)
	}

	if len(stage.Requires) > 0 {
		fmt.Fprintf(&b, "\nWhat you have: %s\n", join(stage.Requires))
	}

	owed := append(append([]fsm.Artifact{}, stage.Produces...), stage.ProducesForHuman...)
	if len(owed) > 0 {
		fmt.Fprintf(&b, "What you owe: %s\n", join(owed))
		fmt.Fprintf(&b, "The stage does not close without all of them.\n")
	}

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

func join(artifacts []fsm.Artifact) string {
	names := make([]string, 0, len(artifacts))
	for _, artifact := range artifacts {
		names = append(names, string(artifact))
	}
	return strings.Join(names, ", ")
}
