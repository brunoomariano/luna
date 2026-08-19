package fsm

import (
	"fmt"
	"sort"
	"strings"
)

// OrderKind is what the order tells the lead to do. The four values are
// exhaustive: an order always says exactly one of them.
//
// It is a closed set because the lead switches on it, and a value it does not
// recognise must be a refusal rather than a default. An order that falls through
// to "do nothing" is a task that stops with nobody knowing (INV-5).
type OrderKind string

const (
	// OrderRun is the only kind that asks for work: start this agent, in this
	// worktree, with this brief.
	OrderRun OrderKind = "run"

	// OrderWait is a gate. The task is not stuck and nothing failed — a person
	// was asked something and has not answered.
	OrderWait OrderKind = "wait"

	// OrderBlocked is an anomaly. Something failed or stalled and the flow will
	// not move until a person clears it.
	OrderBlocked OrderKind = "blocked"

	// OrderDone is the end of the flow, including a task that was called off.
	OrderDone OrderKind = "done"
)

// Order is what `luna next` returns: a literal command, not advice.
//
// This is the piece that keeps the lead from becoming the thing Luna exists to
// prevent. The lead is an agent so a person can talk to
// it, and an agent that *chooses* the next stage is the model holding flow
// control. So the FSM does not describe the situation and let the lead work out
// what to do — it names the stage, the role, the agent, the worktree, the base
// commit and what the role may not touch. The lead executes it and reports back.
//
// What is deliberately absent is as load-bearing as what is present: the order
// carries no list of remaining stages and no view of the flow. A lead that can
// see three stages ahead can decide to save a round trip by doing two of them,
// and it will — that is the failure mode the closed order is shaped against.
// Whoever wants the whole picture asks for it explicitly, which is `luna status`
// — a human command, not something an order hands an agent.
type Order struct {
	Kind   OrderKind `json:"kind"`
	TaskID string    `json:"task_id"`

	// Stage and Role are what to run. Empty on every kind but OrderRun.
	Stage StageID  `json:"stage,omitempty"`
	Role  RoleName `json:"role,omitempty"`

	// Agent is the harness that runs the role — resolved from configuration
	// outside the engine and carried here so the lead does not have to look it up.
	// Empty means the stage is mechanical: Luna runs it itself, with
	// no agent at all.
	Agent string `json:"agent,omitempty"`

	// Worktree is where the work happens. One per task and role, so that
	// "whoever writes does not review" is a property of the filesystem rather
	// than a line in a brief — the separation the flow guarantees.
	Worktree string `json:"worktree,omitempty"`

	// Base is the commit the worktree branches from — the previous stage's
	// delivery. This is the handoff: the artifact itself rather than a
	// description of it. Empty on the first stage of a task, where
	// the base is whatever the repository already is.
	Base string `json:"base,omitempty"`

	// Brief is what the agent is told. It is instruction and never enforcement;
	// what the role may not do is Deny, which the harness applies before the
	// agent starts.
	Brief string `json:"brief,omitempty"`

	// Deny names the capabilities the harness must withhold.
	Deny []Capability `json:"deny,omitempty"`

	// Skills are the capability bundles the role loads.
	Skills []string `json:"skills,omitempty"`

	// Produces is what the stage owes when it finishes. The lead does not check
	// it — Luna does, over the commit — but it is in the order because an agent
	// that is not told what it owes will not deliver it (INV-3).
	Produces []Artifact `json:"produces,omitempty"`

	// Reason says why on the kinds that are not OrderRun: what the gate asks,
	// why the task is blocked, how the flow ended.
	Reason string `json:"reason,omitempty"`
}

// NextOrder turns a task's state into the order the lead executes.
//
// It is a pure function of state, flow and catalogue — no clock, no filesystem,
// no process. That is what lets the order be tested without
// infrastructure and reproduced from the log.
//
// The catalogue may be nil. A stage whose role nothing defines still produces a
// runnable order with an empty Agent, and the layer that starts processes
// refuses it there — an engine that refused it here would make the flow
// undrivable on a machine whose config had not loaded yet.
func NextOrder(state TaskState, flow []Stage, catalogue map[RoleName]Role) (Order, error) {
	order := Order{TaskID: state.ID}

	switch {
	case state.IsTerminal():
		order.Kind = OrderDone
		order.Reason = terminalReason(state)
		return order, nil

	case state.Status == StatusBlocked:
		order.Kind = OrderBlocked
		order.Reason = state.Blocked
		return order, nil

	case state.Status == StatusAwaitingGate:
		order.Kind = OrderWait
		order.Reason = gateReason(state)
		if state.Gate != nil {
			order.Stage = state.Gate.Stage
		}
		return order, nil
	}

	stage, err := stageToRun(state, flow)
	if err != nil {
		return Order{}, err
	}
	if stage == nil {
		// The flow is over but the log does not say so yet: the state is still
		// running or stage_done and no stage comes next. The order reports the
		// ending rather than inventing a stage, and recording it stays the
		// reducer's job.
		order.Kind = OrderDone
		order.Reason = "the flow has no stage left for this task"
		return order, nil
	}

	return runOrder(state, *stage, catalogue), nil
}

// stageToRun answers which stage the order should name, or nil when the flow is
// over.
//
// A running task keeps its stage: the order is idempotent, so a lead that asks
// twice — because it crashed, or because a person is driving it by hand — gets
// the same instruction rather than skipping ahead. Anything else advances.
func stageToRun(state TaskState, flow []Stage) (*Stage, error) {
	if state.Status == StatusRunning {
		i := indexOf(flow, state.Stage)
		if i < 0 {
			return nil, fmt.Errorf("%w: %q", ErrUnknownStage, state.Stage)
		}
		return &flow[i], nil
	}

	next, ok, err := NextStage(flow, entryPoint(state), state.Context)
	if err != nil || !ok {
		return nil, err
	}
	i := indexOf(flow, next)
	return &flow[i], nil
}

// entryPoint is where NextStage starts looking. A task that has not begun passes
// the zero stage to get the flow's first one; anything else searches from the
// stage it last closed.
func entryPoint(state TaskState) StageID {
	if state.Status == StatusReady {
		return ""
	}
	return state.Stage
}

// runOrder fills in everything the lead needs to start one stage.
func runOrder(state TaskState, stage Stage, catalogue map[RoleName]Role) Order {
	role := RoleName(stage.Role)
	definition := catalogue[role]

	return Order{
		Kind:     OrderRun,
		TaskID:   state.ID,
		Stage:    stage.ID,
		Role:     role,
		Agent:    definition.Agent,
		Worktree: WorktreeName(state.ID, role),
		Base:     state.Base,
		Brief:    definition.Brief,
		Deny:     definition.ToolsDeny,
		Skills:   definition.Skills,
		Produces: append(append([]Artifact{}, stage.Produces...), stage.ProducesForHuman...),
	}
}

// WorktreeName is where a role works on a task.
//
// One per task *and* role, which is the amendment swarm-forge's own history
// argued for: a per-role worktree that outlives the task accumulates drift that
// "compounds at every hop". This one is branched from the previous
// stage's commit and removed when the stage ends.
//
// A mechanical stage names no role, and its worktree is the task's own — there
// is no agent to keep apart from anyone.
func WorktreeName(taskID string, role RoleName) string {
	if role == "" {
		return "luna-" + taskID
	}
	return "luna-" + taskID + "-" + string(role)
}

func terminalReason(state TaskState) string {
	if state.Status == StatusAbandoned {
		return "the task was called off"
	}
	return "the flow is finished"
}

func gateReason(state TaskState) string {
	if state.Gate == nil {
		return "waiting on a person"
	}
	if state.Gate.Reason != "" {
		return state.Gate.Reason
	}
	return fmt.Sprintf("waiting on a person: %s", state.Gate.Kind)
}

// Text renders the order as key=value lines.
//
// Both shapes exist on purpose. This one is what a person reads while driving
// the machine by hand, which is how phase 1 is meant to be exercised before any
// agent is wired to it; JSON is what the lead reads once one is. The
// pattern follows `luna gates`.
//
// Multi-line values — the brief — are indented under their key rather than
// escaped, because the reader is a person and an escaped newline is not
// something a person reads.
func (o Order) Text() string {
	var b strings.Builder

	line := func(key, value string) {
		if value == "" {
			return
		}
		fmt.Fprintf(&b, "%s=%s\n", key, value)
	}

	line("kind", string(o.Kind))
	line("task", o.TaskID)
	line("stage", string(o.Stage))
	line("role", string(o.Role))
	line("agent", o.Agent)
	line("worktree", o.Worktree)
	line("base", o.Base)
	line("deny", joinCapabilities(o.Deny))
	line("skills", strings.Join(o.Skills, ","))
	line("produces", joinArtifacts(o.Produces))
	line("reason", o.Reason)

	// The brief is last and shaped differently because it is the one field that is
	// prose. Its first line sits on the `brief=` line like any other value, and
	// the rest are indented under it — so a reader scanning for `key=` finds it in
	// the same place as everything else, and a multi-line brief still reads as
	// what it is rather than as an escaped string.
	if o.Brief != "" {
		lines := strings.Split(strings.TrimRight(o.Brief, "\n"), "\n")
		fmt.Fprintf(&b, "brief=%s\n", lines[0])
		for _, l := range lines[1:] {
			fmt.Fprintf(&b, "  %s\n", l)
		}
	}

	return b.String()
}

func joinCapabilities(caps []Capability) string {
	names := make([]string, 0, len(caps))
	for _, c := range caps {
		names = append(names, string(c))
	}
	return strings.Join(names, ",")
}

// joinArtifacts sorts so that two runs over the same contract print the same
// line. The contract's declaration order is meaningful to a person reading the
// stage, but an order is compared against another order.
func joinArtifacts(list []Artifact) string {
	names := make([]string, 0, len(list))
	for _, a := range list {
		names = append(names, string(a))
	}
	sort.Strings(names)
	return strings.Join(names, ",")
}
