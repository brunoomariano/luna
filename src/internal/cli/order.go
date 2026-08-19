package cli

import (
	"fmt"
	"strings"

	"github.com/brunoomariano/luna/src/internal/fsm"
	"github.com/brunoomariano/luna/src/internal/node"
)

// nextCommand prints the order for a task: what to run, where, from which
// commit, with what denied.
//
// This is the interface between the FSM and whatever is driving it — a person
// today, an agent lead later. It is a read: asking twice changes
// nothing, and a running stage is reported again rather than skipped past. That
// matters more than it looks, because the caller may be a process that crashed
// and restarted, and an order that advanced on being read would lose a stage
// with nothing recording that it happened.
func nextCommand(env Env, args []string) error {
	if len(args) == 0 {
		return fmt.Errorf("%w: next needs a task id", ErrUsage)
	}
	id := args[0]

	asJSON, err := wantsJSON(args[1:])
	if err != nil {
		return err
	}

	state, err := env.replay(id)
	if err != nil {
		return err
	}

	order, err := fsm.NextOrder(state, fsm.DefaultFlow(), env.profiles().Roles)
	if err != nil {
		return err
	}

	if asJSON {
		return writeJSON(env.Out, order)
	}
	fmt.Fprint(env.Out, order.Text())
	return nil
}

// doneCommand reports that the stage named in the order finished, and hands in
// the commit it produced.
//
// The commit is what makes this more than a status update. It becomes the next
// stage's base, so the handoff is the artifact rather than a description of it
// . What Luna does with it is verify — the message describes, the
// diff decides.
//
// Nothing here judges the work. The delivery and its evidence go into the
// action and the reducer decides whether the stage closed: a stage
// that owed two artifacts and delivered one does not close, and no flag on this
// command can make it.
func doneCommand(env Env, args []string) error {
	if len(args) == 0 {
		return fmt.Errorf("%w: done needs a task id", ErrUsage)
	}
	id := args[0]

	flags, err := parseFlags(args[1:])
	if err != nil {
		return err
	}

	state, err := env.replay(id)
	if err != nil {
		return err
	}
	if state.Status != fsm.StatusRunning {
		return fmt.Errorf("task %q has no running stage to finish (it is %s)", id, state.Status)
	}

	delivered, err := deliveredArtifacts(flags["delivered"])
	if err != nil {
		return err
	}

	// Existence is the floor, and it is recorded as exactly that. A stage whose
	// contract declares a command will not close on it — underProven refuses the
	// weaker check. That refusal is the point: reporting a stage
	// done by hand must not be a way to launder a verdict nobody produced.
	evidence := map[fsm.Artifact]fsm.Evidence{}
	for _, artifact := range delivered {
		// fsm.Exists rather than a literal: the floor is defined in one place, so
		// a change to what "nothing was checked" records cannot apply here and
		// not to the engine.
		e := fsm.Exists(state.Seq)
		e.Detail = "reported by hand through `luna done`"
		evidence[artifact] = e
	}

	action := fsm.Complete{
		Delivered: delivered,
		Evidence:  evidence,
		Commit:    flags["commit"],
		Flow:      fsm.DefaultFlow(),
	}

	if err := env.Store.AppendActionAt(id, state.Seq, action); err != nil {
		return err
	}

	after, err := env.replay(id)
	if err != nil {
		return err
	}

	// The outcome is printed rather than assumed. A stage that did not close
	// leaves the task blocked with the reason recorded, and a caller told only
	// "ok" would carry on against a task that has stopped.
	if after.Status == fsm.StatusBlocked {
		fmt.Fprintf(env.Out, "%s did not close: %s\n", state.Stage, after.Blocked)
		return nil
	}
	fmt.Fprintf(env.Out, "%s closed\n", state.Stage)
	return nil
}

// statusCommand is the whole picture, asked for on purpose.
//
// It exists because `luna next` deliberately does not carry one. An order that
// listed the stages ahead would invite whoever reads it to save a round trip by
// doing two of them, which is the model taking flow control back.
// Keeping the panorama in a separate command means seeing it is an explicit act
// rather than something that arrives alongside an instruction.
func statusCommand(env Env, args []string) error {
	if len(args) == 0 {
		return fmt.Errorf("%w: status needs a task id", ErrUsage)
	}
	id := args[0]

	asJSON, err := wantsJSON(args[1:])
	if err != nil {
		return err
	}

	state, err := env.replay(id)
	if err != nil {
		return err
	}

	report := statusReport(state, fsm.DefaultFlow())
	// Filled here rather than in statusReport, which is pure and takes no
	// filesystem: reading a ref is a git call, and where the task landed is a
	// fact about the repository rather than about the state.
	if state.Status == fsm.StatusDone {
		report.Branch = node.TaskBranch(state.ID)
	}
	if asJSON {
		return writeJSON(env.Out, report)
	}

	fmt.Fprintf(env.Out, "%s  %s\n", report.TaskID, report.Status)
	if report.Base != "" {
		fmt.Fprintf(env.Out, "  base   %s\n", report.Base)
	}
	// Where the work is, which is the half of `done` a person actually needs.
	// `done` means ready to integrate, and integrating is a manual act — so the
	// branch has to be named rather than left to be worked out.
	if report.Branch != "" {
		fmt.Fprintf(env.Out, "  branch %s\n", report.Branch)
	}
	fmt.Fprintln(env.Out)
	for _, stage := range report.Stages {
		fmt.Fprintf(env.Out, "  %-3s %s\n", stage.Mark, stage.ID)
	}
	return nil
}

// StatusReport is the structured shape of `luna status`.
type StatusReport struct {
	TaskID string      `json:"task_id"`
	Status fsm.Status  `json:"status"`
	Stage  fsm.StageID `json:"stage,omitempty"`
	Base   string      `json:"base,omitempty"`

	// Branch is the ref this task's work is on. Empty until the task ends —
	// before that the branch exists but is stranded where the task opened, and
	// naming it would point a person at the wrong commit.
	Branch string      `json:"branch,omitempty"`
	Stages []StageMark `json:"stages"`
}

// StageMark is one stage's place in the walk.
type StageMark struct {
	ID    fsm.StageID `json:"id"`
	State string      `json:"state"`
	Mark  string      `json:"-"`
}

// statusReport walks the flow and marks where the task stands.
//
// A stage the task's conditions exclude is reported as skipped rather than
// omitted: "qa does not apply to a chore" is an answer, and a flow that silently
// dropped it would read as a flow that forgot it.
func statusReport(state fsm.TaskState, flow []fsm.Stage) StatusReport {
	report := StatusReport{
		TaskID: state.ID,
		Status: state.Status,
		Stage:  state.Stage,
		Base:   state.Base,
	}

	current := indexOfStage(flow, state.Stage)
	for i, stage := range flow {
		mark := StageMark{ID: stage.ID}
		switch {
		case !stage.AppliesTo(state.Context):
			mark.State, mark.Mark = "skipped", "-"
		case state.Stage != "" && i == current:
			mark.State, mark.Mark = "current", "*"
		case state.Stage != "" && i < current, state.IsTerminal():
			mark.State, mark.Mark = "done", "x"
		default:
			mark.State, mark.Mark = "ahead", " "
		}
		report.Stages = append(report.Stages, mark)
	}
	return report
}

func indexOfStage(flow []fsm.Stage, id fsm.StageID) int {
	for i, stage := range flow {
		if stage.ID == id {
			return i
		}
	}
	return -1
}

// deliveredArtifacts parses the comma-separated list a caller reports.
func deliveredArtifacts(list string) ([]fsm.Artifact, error) {
	if strings.TrimSpace(list) == "" {
		return nil, fmt.Errorf("%w: done needs --delivered with what the stage produced", ErrUsage)
	}

	var artifacts []fsm.Artifact
	for _, name := range strings.Split(list, ",") {
		name = strings.TrimSpace(name)
		if name == "" {
			continue
		}
		artifacts = append(artifacts, fsm.Artifact(name))
	}
	return artifacts, nil
}

// replay rebuilds a task's state, refusing an id the store has never seen.
//
// Without the existence check a replay of nothing returns a zero state, and a
// typo in a task id would produce a confident order for a task that does not
// exist.
func (e Env) replay(id string) (fsm.TaskState, error) {
	events, err := e.Store.Events(id)
	if err != nil {
		return fsm.TaskState{}, err
	}
	if len(events) == 0 {
		return fsm.TaskState{}, fmt.Errorf("no task %q", id)
	}
	return e.Store.Replay(id, fsm.DefaultFlow())
}
