package herdr

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/brunoomariano/luna/src/internal/fsm"
	"github.com/brunoomariano/luna/src/internal/lead"
	"github.com/brunoomariano/luna/src/internal/node"
)

// AgentStatus is what herdr reports about a pane's occupant.
//
// Two of these are traps, and naming them here is half of why this package
// exists. `Idle` means "prompt visible, nothing happening" — not that the work
// succeeded. `Done` is a UI flag that decays to `Idle` once a human focuses the
// tab. Neither is a verdict, and neither closes a stage (ADR-0028).
type AgentStatus string

// The five statuses herdr reports. Two of them are traps, which is why they are
// spelled out here rather than passed through as opaque strings.
const (
	StatusIdle    AgentStatus = "idle"
	StatusWorking AgentStatus = "working"
	StatusBlocked AgentStatus = "blocked"
	StatusDone    AgentStatus = "done"
	StatusUnknown AgentStatus = "unknown"
)

// Settled reports whether the agent stopped moving.
//
// It is the trigger for verification and nothing more. `Unknown` counts as
// settled because the agent is not working — but it carries no claim about the
// outcome, which is why the verdict still comes from running the real check.
func (s AgentStatus) Settled() bool {
	return s == StatusIdle || s == StatusDone || s == StatusUnknown
}

// stallCode is what herdr calls it on the wire.
const stallCode = "agent_prompt_stalled"

// Stalled reports whether an error from herdr is the stall condition.
//
// It reads herdr's error code rather than matching on message text: the code is
// the contract, and a reworded message must not silently stop being detected.
func Stalled(err error) bool {
	var api *apiError
	if errors.As(err, &api) {
		return api.Code == stallCode
	}
	return errors.Is(err, lead.ErrStalled)
}

// Runner is the part of herdr the node actually needs.
//
// An interface rather than *Client so the node can be tested against a fake
// socket without a running herdr — the same reason lead.Node exists. It is
// deliberately small: opening a worktree, closing it, starting an agent and
// prompting it are the whole of what driving a stage requires.
type Runner interface {
	// OpenWorktree creates the worktree a stage works in and returns the
	// workspace that holds it. The binding anchors on the workspace because pane
	// ids move.
	//
	// One worktree per task *and* role, branched from the base (ADR-0055). The
	// role is what makes "whoever writes does not review" a property of the
	// filesystem rather than a line in a brief (INV-core-7), and the base is what
	// makes the handoff the previous stage's commit rather than a description of
	// it (INV-core-6).
	OpenWorktree(ctx context.Context, w WorktreeSpec) (Workspace, error)

	// CloseWorktree removes it again.
	//
	// A worktree that outlives its stage is the failure swarm-forge's own fork
	// documented: a role branch that is never reset accumulates divergence that
	// "compounds at every hop", so feature N faces N-1 features of drift.
	CloseWorktree(ctx context.Context, ws Workspace) error

	// StartAgent puts an agent into a pane in that workspace and waits until it
	// is interactive. The kind must be one herdr knows (ADR-0031); the name is
	// herdr-wide and must be unique, so it carries the task and the stage.
	//
	// args are passed through to the agent itself, which is how a denied
	// capability reaches it (ADR-0042).
	StartAgent(ctx context.Context, ws Workspace, kind, name string, args []string) (string, error)

	// Prompt submits text and waits for the agent to settle, returning the status
	// it settled at. Prompt and wait are one call because two would race.
	Prompt(ctx context.Context, pane, text string) (AgentStatus, error)
}

// Prover runs the checks that prove an artifact.
//
// It is deliberately not part of Runner: verification is not a herdr operation
// (ADR-0035). herdr creates the worktree and hosts the agent; the check runs
// against a real exit code somewhere else, which is what keeps the evidence the
// tool's answer rather than something scraped off a screen.
type Prover interface {
	Prove(ctx context.Context, v fsm.Verifier, seq int) (fsm.Evidence, error)
}

// Workspace is herdr's home for one stage: the worktree, its workspace and the
// pane the agent runs in.
type Workspace struct {
	ID       string
	RootPane string
	Path     string
}

// WorktreeSpec is what a stage needs a checkout to be.
//
// It is a struct rather than three parameters because the three travel together
// and one of them is easy to leave out: a call that forgot the base would branch
// from whatever the repository is on, silently discarding every stage before it
// — and it would look like it worked.
type WorktreeSpec struct {
	TaskID string
	Role   fsm.RoleName

	// Base is the commit to branch from — the previous stage's delivery. Empty
	// means the repository's own head, which is the first stage of a task.
	Base string
}

// Branch is where this stage's work lives.
//
// Named after the task and the role so a checkout is findable without consulting
// Luna, and so two roles on one task cannot land on the same branch.
//
// The role is joined with "-" rather than "/", which is not cosmetic: git refs
// are a directory tree, so `luna/<task>/<role>` makes `luna/<task>` a directory
// and the roleless form of the same task can no longer exist. A mechanical stage
// following any agent stage — `setup`, which is stage 2 of every task — died on
// `cannot lock ref`. One "/" keeps Luna's branches in their own namespace; a
// second one would put every task's stages in a namespace of their own, which is
// what collides.
func (w WorktreeSpec) Branch() string {
	if w.Role == "" {
		return "luna/" + w.TaskID
	}
	return "luna/" + w.TaskID + "-" + string(w.Role)
}

// Node runs a stage inside herdr. It satisfies lead.Node (ADR-0030).
type Node struct {
	Runner Runner

	// Roles resolves a stage's role to what runs it. A stage whose role does not
	// resolve stops loudly rather than running under some fallback agent and
	// having the result called that role's opinion (ADR-0040).
	Roles func(fsm.RoleName) (fsm.Role, bool)

	// Prompt builds what the agent is told for a stage. Injected rather than
	// built here so the wording is configuration, not code.
	Prompt func(state fsm.TaskState, stage fsm.Stage) string

	// Prove runs the contract's checks. It takes the worktree path, because that
	// is where the commands run, and the node is what knows it (ADR-0035).
	Prove func(ws Workspace) Prover

	// Warn reports something that went wrong beside the work rather than in it —
	// a worktree that would not be removed. Nil discards, because a node with no
	// reporter configured should still run a stage.
	Warn func(format string, args ...any)

	// Merge integrates a delivered commit into the shared branch, and reports a
	// conflict rather than resolving one (ADR-0053).
	//
	// A function rather than the Merger itself so a test can exercise a conflict
	// without building two diverging repositories — but it returns node's own
	// verdict rather than a copy of it, because two spellings of `merge_blocked`
	// is exactly the drift that makes a rename stop working silently.
	//
	// Nil skips integration, which is what a dry run wants.
	Merge func(ctx context.Context, commit, message string) (node.Merge, error)
}

// warn reports a problem that must not fail the stage.
func (n *Node) warn(format string, args ...any) {
	if n.Warn != nil {
		n.Warn(format, args...)
	}
}

// runMechanical performs a stage with no agent in it.
//
// Integrating is part of what such a stage does, when there is something to
// integrate: the work lives on the role's own branch (ADR-0055), so the stage
// that closes a task has to bring it back — and that merge is Luna's,
// deterministic, with no model in it (ADR-0053).
func (n *Node) runMechanical(ctx context.Context, state fsm.TaskState, stage fsm.Stage, ws Workspace) (lead.Result, error) {
	if err := n.integrate(ctx, state, stage); err != nil {
		return lead.Result{}, err
	}
	return n.verify(ctx, ws, state, stage)
}

// integrate brings the delivered work back into the shared branch.
//
// It runs on the mechanical stage that closes a task's work — `commit` — because
// that is where a task stops being a branch and starts being part of the
// repository. Every earlier stage hands on through its commit and its base
// (INV-core-6); nothing needs merging until the end.
//
// The merge itself is `node.Merger`: one owner, a dry run in a throwaway
// worktree separate from the real merge, and no automatic conflict resolution
// (ADR-0053). A conflict is a verdict, and it becomes a blocked stage rather
// than an error — so the watchdog can see it and a person is told.
func (n *Node) integrate(ctx context.Context, state fsm.TaskState, stage fsm.Stage) error {
	if n.Merge == nil || state.Base == "" {
		return nil
	}
	if !stage.ProducesArtifact(mergedArtifact) {
		return nil
	}

	result, err := n.Merge(ctx, state.Base, fmt.Sprintf("luna: %s (%s)", state.ID, stage.ID))
	if err != nil {
		return err
	}
	if result.Verdict == node.MergeBlocked {
		// Named, not summarised: the whole point of stopping is that somebody has
		// to look, and "there was a conflict" does not tell them where.
		return fmt.Errorf("%s could not be integrated: %s", state.ID, result.Detail)
	}
	return nil
}

// mergedArtifact is what a stage produces when it has integrated the work.
//
// Keyed on the artifact rather than the stage id so a project that renames
// `commit` keeps the behaviour — the same reasoning ADR-0049 applies to gates
// and reviews.
const mergedArtifact fsm.Artifact = "commit_sha"

// Run drives one stage and reports what it delivered.
//
// The shape is the whole of ADR-0028: herdr's status decides only *when* to
// verify, and the verdict decides what happened. A stage that settles without
// passing verification comes back with failing evidence, and the reducer turns
// that into a block — this layer never decides a transition.
func (n *Node) Run(ctx context.Context, state fsm.TaskState, stage fsm.Stage) (lead.Result, error) {
	result, err := n.conduct(ctx, state, stage)
	// herdr going away is the machinery breaking, not the stage failing. Marking it
	// here rather than at each return keeps the lead from importing this package to
	// tell the two apart (ADR-0030, ADR-0033).
	if errors.Is(err, ErrGone) {
		return result, fmt.Errorf("%w: %w", lead.ErrInfrastructure, err)
	}
	return result, err
}

// conduct is Run without the error classification, so the wrapper above has one
// place to mark what came from the transport rather than from the stage.
func (n *Node) conduct(ctx context.Context, state fsm.TaskState, stage fsm.Stage) (lead.Result, error) {
	ws, err := n.Runner.OpenWorktree(ctx, WorktreeSpec{
		TaskID: state.ID,
		Role:   fsm.RoleName(stage.Role),
		Base:   state.Base,
	})
	if err != nil {
		return lead.Result{}, err
	}

	// The worktree lasts exactly as long as the stage. What survives is the
	// commit, which is the handoff — so the next role starts from the artifact
	// and never from a directory somebody else was working in (ADR-0055).
	//
	// A failure to clean up does not fail the stage: the work is committed by
	// then, and turning "the stage delivered" into "the stage failed" because a
	// directory would not go away would lose the more important of the two. It
	// is reported rather than swallowed.
	defer func() {
		if err := n.Runner.CloseWorktree(context.WithoutCancel(ctx), ws); err != nil {
			n.warn("could not remove the worktree for %s at %s: %v", state.ID, stage.ID, err)
		}
	}()

	// A mechanical stage runs no agent at all: `setup` is a worktree, `commit` is
	// git, and paying a model to run those buys nothing and can lose something
	// (ADR-0040). The verification still runs, so the stage still has to prove
	// what it produced.
	if stage.Mechanical() {
		return n.runMechanical(ctx, state, stage, ws)
	}

	role, err := n.role(stage)
	if err != nil {
		return lead.Result{}, err
	}

	name := agentName(state.ID, stage.ID)

	// A gated role starts without what it must not have — the tool is absent
	// rather than discouraged (ADR-0018). A harness Luna cannot gate stops the
	// stage instead of running an ungated review (ADR-0041).
	args, err := gateArgs(role)
	if err != nil {
		return lead.Result{}, fmt.Errorf("stage %q: %w", stage.ID, err)
	}

	pane, err := n.Runner.StartAgent(ctx, ws, role.Agent, name, args)
	if err != nil {
		return lead.Result{}, err
	}

	// The prompt targets the agent by name rather than by pane. herdr resolves a
	// pane id to a terminal, not to "the named agent running in it", and refuses
	// with `agent_not_ready` — the name is the handle it wants.
	status, err := n.Runner.Prompt(ctx, name, n.prompt(state, stage, role))
	if err != nil {
		// A stall is translated here so nothing above this package has to read
		// herdr's error codes. What crosses the boundary is Luna's vocabulary
		// (ADR-0030), and the lead decides what a stall means (ADR-0034).
		if Stalled(err) {
			return lead.Result{}, fmt.Errorf("%w: the agent did not react in stage %q", lead.ErrStalled, stage.ID)
		}
		return lead.Result{}, err
	}

	// A blocked agent is asking a person for something the flow did not foresee.
	// It is reported as an error so the lead escalates it, and the reason names
	// the pane so someone can find what is asking (ADR-0029).
	if status == StatusBlocked {
		return lead.Result{}, fmt.Errorf("the agent in pane %s is asking for input", pane)
	}

	// Anything else settled means it is worth checking. The status said the agent
	// stopped; only the verifier says whether the stage delivered.
	return n.verify(ctx, ws, state, stage)
}

// verify runs each artifact's verifier and turns the results into evidence.
//
// Every owed artifact gets a record, including the ones nothing checked: an
// artifact verified by existence says so in its scope rather than borrowing the
// appearance of a passing test (ADR-0032).
func (n *Node) verify(ctx context.Context, ws Workspace, state fsm.TaskState, stage fsm.Stage) (lead.Result, error) {
	owed := append(append([]fsm.Artifact{}, stage.Produces...), stage.ProducesForHuman...)
	result := lead.Result{
		Delivered: owed,
		Evidence:  make(map[fsm.Artifact]fsm.Evidence, len(owed)),
	}

	prover := n.prover(ws)
	for _, artifact := range owed {
		evidence, err := prover.Prove(ctx, fsm.VerifierFor(stage, artifact), state.Seq)
		if err != nil {
			return lead.Result{}, err
		}
		result.Evidence[artifact] = evidence
	}
	return result, nil
}

// prover is what proves this stage's artifacts, defaulting to one that runs
// nothing. The default keeps a zero Node usable — a stage still closes, on
// existence evidence, which is the truth about what a Node with no prover proved.
func (n *Node) prover(ws Workspace) Prover {
	if n.Prove != nil {
		return n.Prove(ws)
	}
	return existenceOnly{}
}

// existenceOnly records delivery without running anything.
type existenceOnly struct{}

func (existenceOnly) Prove(_ context.Context, v fsm.Verifier, seq int) (fsm.Evidence, error) {
	return fsm.Evidence{Scope: v.Proves(), Verdict: fsm.VerdictPassed, RecordedAt: seq}, nil
}

func (n *Node) prompt(state fsm.TaskState, stage fsm.Stage, role fsm.Role) string {
	if n.Prompt != nil {
		return n.Prompt(state, stage)
	}
	return brief(state, stage, role)
}

// brief is what an agent is told when it starts.
//
// It has to carry the handoff, because the agent is new: it did not run the
// previous stage and has no memory of it. What crosses is pointers and the
// contract — never a prose summary of what happened, which would degrade at every
// hop (INV-core-6).
//
// The body is generated here rather than written by an agent, which is what stops
// one stage from injecting narrative into the next.
func brief(state fsm.TaskState, stage fsm.Stage, role fsm.Role) string {
	var b strings.Builder

	if role.Brief != "" {
		b.WriteString(role.Brief)
		b.WriteString("\n\n")
	}

	fmt.Fprintf(&b, "Task %s (%s), stage %s.\n", state.ID, state.Context.Kind, stage.ID)
	fmt.Fprintf(&b, "Worktree: the directory you are in.\n")

	if len(stage.Requires) > 0 {
		fmt.Fprintf(&b, "\nAlready produced, and yours to read:\n")
		for _, artifact := range stage.Requires {
			fmt.Fprintf(&b, "  - %s%s\n", artifact, provenance(state, artifact))
		}
	}

	owed := append(append([]fsm.Artifact{}, stage.Produces...), stage.ProducesForHuman...)
	if len(owed) > 0 {
		fmt.Fprintf(&b, "\nThis stage does not close until it delivers:\n")
		for _, artifact := range owed {
			fmt.Fprintf(&b, "  - %s%s\n", artifact, howProven(stage, artifact))
		}
	}

	return b.String()
}

// provenance names how a required artifact was proven, so the agent knows whether
// it is reading something checked or something merely delivered (ADR-0032).
func provenance(state fsm.TaskState, artifact fsm.Artifact) string {
	evidence, ok := state.Evidence[artifact]
	if !ok || !evidence.Delivered() {
		return ""
	}
	return fmt.Sprintf(" (%s)", evidence.Scope)
}

// howProven names the check an artifact will face, so the agent knows what it is
// being held to before it starts rather than after it fails.
func howProven(stage fsm.Stage, artifact fsm.Artifact) string {
	verifier := fsm.VerifierFor(stage, artifact)
	if _, runs := verifier.(fsm.Command); !runs {
		return ""
	}
	return fmt.Sprintf(" — checked by `%s`", verifier.Describe())
}

// role resolves the stage's role, refusing rather than falling back.
//
// A role that does not resolve is a configuration mistake, and running the stage
// on some default agent would produce work attributed to a role nobody defined —
// which is worse than stopping, because it looks like it worked.
func (n *Node) role(stage fsm.Stage) (fsm.Role, error) {
	if n.Roles == nil {
		return fsm.Role{}, fmt.Errorf("stage %q names role %q and no roles are configured", stage.ID, stage.Role)
	}

	role, ok := n.Roles(fsm.RoleName(stage.Role))
	if !ok {
		return fsm.Role{}, fmt.Errorf("stage %q names role %q, which resolves to nothing", stage.ID, stage.Role)
	}
	if role.Agent == "" {
		return fsm.Role{}, fmt.Errorf("role %q names no agent to run it", stage.Role)
	}
	return role, nil
}

// agentName is what herdr calls this stage's agent.
//
// Two constraints, both learned from a live herdr rather than from documentation.
// Names are unique across the whole server, not per workspace, so a second task
// reusing one is refused with `agent_name_taken` — naming it after the task makes
// that impossible and keeps the pane findable by anyone who knows the task id.
// And the name must match `[a-z][a-z0-9_-]{0,31}`, so a task id like "LUNA-1" has
// to be folded rather than passed through.
func agentName(taskID string, stage fsm.StageID) string {
	var b strings.Builder
	b.WriteString("luna-")

	for _, r := range strings.ToLower(taskID + "-" + string(stage)) {
		switch {
		case r >= 'a' && r <= 'z', r >= '0' && r <= '9', r == '-', r == '_':
			b.WriteRune(r)
		default:
			b.WriteRune('-')
		}
		// The cap is herdr's, and it should never be reached: task ids are
		// validated against it (fsm.MaxTaskIDLen) and a flow with stage names long
		// enough to squeeze them is reported by fsm.AuditFlowNames. This stays as
		// the floor, because sending a name herdr refuses would fail the stage —
		// but truncating is why two stages of one task once produced the same name,
		// and reuse() then prompted the wrong pane.
		if b.Len() >= agentNameLimit {
			break
		}
	}
	return b.String()
}

// agentNameLimit is what herdr accepts for an agent name, verified against a
// running server: `[a-z][a-z0-9_-]{0,31}` (ADR-0036).
const agentNameLimit = 32
