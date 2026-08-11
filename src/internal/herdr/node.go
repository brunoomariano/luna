package herdr

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/brunoomariano/luna/src/internal/fsm"
	"github.com/brunoomariano/luna/src/internal/lead"
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
// deliberately small: four operations are the whole of what driving a stage
// requires.
type Runner interface {
	// OpenWorktree creates the worktree for a task and returns the workspace that
	// holds it. One worktree per task (ADR-0027), and the binding anchors on the
	// workspace because pane ids move.
	OpenWorktree(ctx context.Context, taskID, branch string) (Workspace, error)

	// StartAgent puts an agent into a pane in that workspace and waits until it
	// is interactive. The kind must be one herdr knows (ADR-0031); the name is
	// herdr-wide and must be unique, so it carries the task id.
	StartAgent(ctx context.Context, ws Workspace, kind, name string) (string, error)

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

// Workspace is herdr's home for one task: the worktree, its workspace and the
// pane the agent runs in.
type Workspace struct {
	ID       string
	RootPane string
	Path     string
}

// Node runs a stage inside herdr. It satisfies lead.Node (ADR-0030).
type Node struct {
	Runner Runner

	// Agent is the herdr agent kind to start, from herdr's allowlist (ADR-0031).
	Agent string

	// Prompt builds what the agent is told for a stage. Injected rather than
	// built here so the wording is configuration, not code.
	Prompt func(state fsm.TaskState, stage fsm.Stage) string

	// Prove runs the contract's checks. It takes the worktree path, because that
	// is where the commands run, and the node is what knows it (ADR-0035).
	Prove func(ws Workspace) Prover
}

// Run drives one stage and reports what it delivered.
//
// The shape is the whole of ADR-0028: herdr's status decides only *when* to
// verify, and the verdict decides what happened. A stage that settles without
// passing verification comes back with failing evidence, and the reducer turns
// that into a block — this layer never decides a transition.
func (n *Node) Run(ctx context.Context, state fsm.TaskState, stage fsm.Stage) (lead.Result, error) {
	ws, err := n.Runner.OpenWorktree(ctx, state.ID, branchFor(state.ID))
	if err != nil {
		return lead.Result{}, err
	}

	name := agentName(state.ID)

	pane, err := n.Runner.StartAgent(ctx, ws, n.Agent, name)
	if err != nil {
		return lead.Result{}, err
	}

	// The prompt targets the agent by name rather than by pane. herdr resolves a
	// pane id to a terminal, not to "the named agent running in it", and refuses
	// with `agent_not_ready` — the name is the handle it wants.
	status, err := n.Runner.Prompt(ctx, name, n.prompt(state, stage))
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

func (n *Node) prompt(state fsm.TaskState, stage fsm.Stage) string {
	if n.Prompt != nil {
		return n.Prompt(state, stage)
	}
	return fmt.Sprintf("Task %s, stage %s.", state.ID, stage.ID)
}

// agentName is what herdr calls this task's agent.
//
// Two constraints, both learned from a live herdr rather than from documentation.
// Names are unique across the whole server, not per workspace, so a second task
// reusing one is refused with `agent_name_taken` — naming it after the task makes
// that impossible and keeps the pane findable by anyone who knows the task id.
// And the name must match `[a-z][a-z0-9_-]{0,31}`, so a task id like "LUNA-1" has
// to be folded rather than passed through.
func agentName(taskID string) string {
	var b strings.Builder
	b.WriteString("luna-")

	for _, r := range strings.ToLower(taskID) {
		switch {
		case r >= 'a' && r <= 'z', r >= '0' && r <= '9', r == '-', r == '_':
			b.WriteRune(r)
		default:
			b.WriteRune('-')
		}
		if b.Len() >= 32 {
			break
		}
	}
	return b.String()
}

// branchFor is the branch a task's worktree lives on. One per task, named after
// it, so the checkout is findable without consulting Luna.
func branchFor(taskID string) string { return "luna/" + taskID }
