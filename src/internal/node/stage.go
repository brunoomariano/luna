package node

import (
	"context"
	"errors"
	"fmt"
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
	Roles map[fsm.RoleName]fsm.Role

	// Artifacts is where a handed-over artifact is written. Nil means no stage
	// may declare a handover, which is refused rather than silently ignored.
	Artifacts ArtifactStore

	// Stored answers whether an artifact reached the store, with the hash of what
	// did. Separate from Artifacts because that interface is the socket's
	// contract — what an agent may write — and this is verification asking a
	// question the agent has no part in.
	Stored func(taskID, stage, artifact string) (hash string, err error)

	// Budget bounds one agent call. Zero leaves it to the caller's context.
	Budget time.Duration

	// Sessions carries a session id from one stage to the next, for the stages
	// that declared `context = "live"`. Keyed by role, because a session belongs
	// to the conversation a role has been having and never crosses to another —
	// which is the half of the fresh-context rule that survived.
	Sessions map[fsm.RoleName]string

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

	socket, handsOver, err := r.serveArtifacts(stage, wt)
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

	spend, err := r.call(ctx, state, stage, role, wt, handsOver)
	if err != nil {
		return lead.Result{}, err
	}

	// The agent stopped, whatever that means. Only the verifier says whether the
	// stage delivered.
	return r.verify(ctx, state, stage, wt, spend)
}

// call runs the agent and returns what it cost.
func (r *Runner) call(
	ctx context.Context,
	state fsm.TaskState,
	stage fsm.Stage,
	role fsm.Role,
	wt Worktree,
	handsOver bool,
) (fsm.Spend, error) {
	call := agent.Call{
		Kind:    role.Agent,
		Dir:     wt.Path,
		Prompt:  r.brief(state, stage, role),
		System:  role.Brief,
		Deny:    denied(role),
		Budget:  r.Budget,
		Context: agent.Fresh,
	}
	if handsOver {
		call.Env = append(call.Env, socketEnv+"="+SocketName)
	}

	// A stage asking to continue gets the session its role was last using. An
	// absent one means there is nothing to continue — the first stage a role
	// runs — and starting fresh is the honest answer rather than an error.
	if !stage.Context.Fresh() {
		if session := r.Sessions[fsm.RoleName(stage.Role)]; session != "" {
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
			return spend, fmt.Errorf("%w: %w", lead.ErrInfrastructure, err)
		}
		return spend, fmt.Errorf("stage %q: %w", stage.ID, err)
	}

	// The session is remembered whether or not the next stage wants it: which
	// stage continues is the flow's decision, and it is made after this one ran.
	if result.Session != "" {
		if r.Sessions == nil {
			r.Sessions = map[fsm.RoleName]string{}
		}
		r.Sessions[fsm.RoleName(stage.Role)] = result.Session
	}
	return spend, nil
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

	shell := Shell{Dir: r.Repo, Commit: commit}
	owed := append(append([]fsm.Artifact{}, stage.Produces...), stage.ProducesForHuman...)

	result := lead.Result{
		Evidence: map[fsm.Artifact]fsm.Evidence{},
		Commit:   commit,
		Spent:    spend,
	}
	for i, artifact := range owed {
		verifier := stage.Verifiers[artifact]

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

// serveArtifacts opens the handover socket when the stage declares one.
//
// A stage that declares no handover opens nothing: there is no reason to expose
// a writer to an agent that owes nothing through it.
func (r *Runner) serveArtifacts(stage fsm.Stage, wt Worktree) (server *ArtifactServer, opened bool, err error) {
	if !handsOver(stage) {
		return nil, false, nil
	}
	if r.Artifacts == nil {
		return nil, false, fmt.Errorf("stage %q hands an artifact to Luna and no store is configured", stage.ID)
	}

	server, err = ServeArtifacts(wt.Path, string(stage.ID), r.Artifacts)
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
	role, ok := r.Roles[fsm.RoleName(stage.Role)]
	if !ok {
		return fsm.Role{}, fmt.Errorf("stage %q names the role %q, which is not configured", stage.ID, stage.Role)
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
