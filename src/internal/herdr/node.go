package herdr

import (
	"context"
	"fmt"

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
	// is interactive. The kind must be one herdr knows (ADR-0031).
	StartAgent(ctx context.Context, ws Workspace, kind string) (string, error)

	// Prompt submits text and waits for the agent to settle, returning the status
	// it settled at. Prompt and wait are one call because two would race.
	Prompt(ctx context.Context, pane, text string) (AgentStatus, error)

	// Verify runs a command in the worktree and reports how it exited. This is
	// what produces evidence: the real tool, not a status (INV-core-4).
	Verify(ctx context.Context, ws Workspace, command string) (exitCode int, output string, err error)
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

	pane, err := n.Runner.StartAgent(ctx, ws, n.Agent)
	if err != nil {
		return lead.Result{}, err
	}

	status, err := n.Runner.Prompt(ctx, pane, n.prompt(state, stage))
	if err != nil {
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

	for _, artifact := range owed {
		evidence, err := n.prove(ctx, ws, state.Seq, fsm.VerifierFor(stage, artifact))
		if err != nil {
			return lead.Result{}, err
		}
		result.Evidence[artifact] = evidence
	}
	return result, nil
}

// prove runs one verifier and records what it observed.
func (n *Node) prove(ctx context.Context, ws Workspace, seq int, v fsm.Verifier) (fsm.Evidence, error) {
	command, ok := v.(fsm.Command)
	if !ok {
		// Existence, and anything else that runs nothing: the artifact was
		// delivered and that is all this claims.
		return fsm.Evidence{Scope: v.Proves(), Verdict: fsm.VerdictPassed, RecordedAt: seq}, nil
	}

	exit, output, err := n.Runner.Verify(ctx, ws, command.Run)
	if err != nil {
		// The command could not be run at all — a missing tool, a dead socket.
		// That is not a failing check, and reporting it as one would tell the
		// audit the tests ran and lost.
		return fsm.Evidence{}, err
	}

	verdict := fsm.VerdictPassed
	if exit != 0 {
		verdict = fsm.VerdictFailed
	}
	return fsm.Evidence{
		Scope:      command.Proves(),
		Verdict:    verdict,
		Command:    command.Run,
		ExitCode:   exit,
		Detail:     firstLine(output),
		RecordedAt: seq,
	}, nil
}

func (n *Node) prompt(state fsm.TaskState, stage fsm.Stage) string {
	if n.Prompt != nil {
		return n.Prompt(state, stage)
	}
	return fmt.Sprintf("Task %s, stage %s.", state.ID, stage.ID)
}

// branchFor is the branch a task's worktree lives on. One per task, named after
// it, so the checkout is findable without consulting Luna.
func branchFor(taskID string) string { return "luna/" + taskID }

// firstLine keeps evidence readable: the summary line, not the whole build log.
func firstLine(s string) string {
	for i, r := range s {
		if r == '\n' {
			return s[:i]
		}
	}
	if len(s) > 200 {
		return s[:200]
	}
	return s
}
