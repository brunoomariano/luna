package cli

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
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

	flow, err := env.flowOf(id)
	if err != nil {
		return err
	}

	order, err := fsm.NextOrder(state, flow)
	if err != nil {
		return err
	}
	order = fullyBriefed(order, state, flow, env.profiles())

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
// stage's base, so the handoff is the artifact rather than a description of it.
// What Luna does with it is verify — the message describes, the
// diff decides.
//
// Nothing here judges the work. The delivery and its evidence go into the
// action and the reducer decides whether the stage closed: a stage
// that owed two artifacts and delivered one does not close, and no flag on this
// command can make it.
// handReportedCompletion assembles the Complete for a stage somebody finished by
// hand.
//
// Nothing here judges the work: the evidence is the floor, and the reducer decides
// whether that satisfies the contract. The flow comes from the task rather than
// from the build, so a stage on a lean flow is checked against the lean contract.
func handReportedCompletion(env Env, id string, state fsm.TaskState, flags map[string]string) (fsm.Complete, error) {
	flow, err := env.flowOf(id)
	if err != nil {
		return fsm.Complete{}, err
	}

	delivered, err := deliveredArtifacts(flags["delivered"])
	if err != nil {
		return fsm.Complete{}, err
	}

	// The commit is checked against git rather than taken on its word. It becomes
	// the base the next stage branches from, so a value that resolves to nothing
	// strands every stage after this one — and forty hex characters look exactly
	// like a delivery.
	commit, err := node.ResolveCommit(context.Background(), ".", flags["commit"])
	if err != nil {
		return fsm.Complete{}, err
	}

	return fsm.Complete{
		Delivered: delivered,
		Evidence:  handReportedEvidence(delivered, state.Seq),
		Commit:    commit,
		Flow:      flow,
	}, nil
}

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

	action, err := handReportedCompletion(env, id, state, flags)
	if err != nil {
		return err
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

	// The middle outcome, and the one a caller most needs told: the stage came up
	// short and is being asked again rather than given up on. Printing "closed"
	// here would be the false success `done` exists to refuse, and printing
	// nothing would leave a person to discover the shortfall from `task show`.
	if len(after.StillOwed) > 0 {
		fmt.Fprintf(env.Out, "%s did not close — still owed: %s\n",
			state.Stage, fsm.JoinArtifacts(after.StillOwed))
		fmt.Fprintf(env.Out, "  deliver those and report again; what you handed in is kept\n")
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

	flow, err := env.flowOf(id)
	if err != nil {
		return err
	}

	report := statusReport(state, flow)
	// Filled here rather than in statusReport, which is pure and takes no
	// filesystem: reading a ref is a git call, and where the task landed is a
	// fact about the repository rather than about the state.
	if state.Status == fsm.StatusDone {
		report.Branch = node.TaskBranch(state.ID)
	}
	if asJSON {
		return writeJSON(env.Out, report)
	}

	printStatus(env, state, flow, report)
	return nil
}

// printStatus is the whole of what somebody sees when they ask where a task is.
//
// It used to be the walk and the base, which answers "which stage" and nothing
// else — so every other question ("what is this costing", "which worktree",
// "which model answered") meant a second command or a JSON parse. All of it was
// already in the state; none of it was on the screen.
//
// Aligned by hand rather than by a layout library. The reader that matters most
// here is an agent reading the terminal, and it said so: text it can read without
// heuristic parsing was the best thing about the surface. A dependency comes when
// there is a TUI to justify it.
func printStatus(env Env, state fsm.TaskState, flow []fsm.Stage, report StatusReport) {
	fmt.Fprintf(env.Out, "%s  %s%s\n", report.TaskID, report.Status, simulationNote(state))
	printStatusFacts(env, state, flow, report)

	fmt.Fprintln(env.Out)
	printStatusStages(env, state, flow, report)

	if state.Blocked != "" {
		fmt.Fprintf(env.Out, "\nblocked (%s)\n  %s\n", state.BlockedBy, state.Blocked)
	}
}

// printStatusFacts is the block of things that are true about the task rather
// than about one stage.
func printStatusFacts(env Env, state fsm.TaskState, flow []fsm.Stage, report StatusReport) {
	line := func(label, format string, args ...any) {
		fmt.Fprintf(env.Out, "  %-10s %s\n", label, fmt.Sprintf(format, args...))
	}

	if name, err := env.Store.FlowNameOf(state.ID); err == nil {
		line("flow", "%s/%s · %s", name, fsm.Fingerprint(flow), state.Context.Kind)
	}
	if roles := packRoles(flow); len(roles) > 0 {
		line("pack", "%s", strings.Join(roles, ", "))
	}
	if state.Memory.Named() {
		line("workstream", "%s", state.Memory.Workstream)
	}
	line("autonomy", "%d  (%s)", int(state.Knob), knobMeaning(state.Knob, flow))

	spent := state.TotalSpend()
	switch {
	case state.BudgetUSD > 0:
		line("budget", "$%.4f of $%.2f · $%.2f left · %d turns",
			spent.CostUSD, state.BudgetUSD, state.BudgetUSD-spent.CostUSD, spent.Turns)
	case spent.CostUSD > 0:
		line("spent", "$%.4f · %d turns · no ceiling", spent.CostUSD, spent.Turns)
	default:
		line("budget", "%s", "no ceiling, nothing spent")
	}

	if report.Base != "" {
		line("base", "%s", report.Base)
	}
	// Where the work is, which is the half of `done` a person actually needs.
	// `done` means ready to integrate, and integrating is a manual act — so the
	// branch has to be named rather than left to be worked out.
	if report.Branch != "" {
		line("branch", "%s", report.Branch)
	}
}

// printStatusStages is the walk, with what each stage cost and who ran it.
//
// The role and the harness are here rather than in the facts above because they
// differ per stage in a pack — and "which model answered" is a question about one
// stage, not about the task.
func printStatusStages(env Env, state fsm.TaskState, flow []fsm.Stage, report StatusReport) {
	for _, mark := range report.Stages {
		stage := stageIn(flow, mark.ID)
		line := fmt.Sprintf("  %-3s %-10s %-13s %s",
			mark.Mark, mark.ID, stage.Role, stageCostLine(state, stage))
		// A mechanical stage has no role and no cost, so the columns after it are
		// padding — and trailing whitespace is what makes a diff of two runs noisy
		// for a reason that has nothing to do with the runs.
		fmt.Fprintln(env.Out, strings.TrimRight(line, " "))
	}

	printWorktrees(env, state, flow)
}

// stageCostLine is what one stage cost, or what it will run on if it has not.
func stageCostLine(state fsm.TaskState, stage fsm.Stage) string {
	spend, ran := state.Spent[stage.ID]
	if !ran {
		if stage.Mechanical() {
			return ""
		}
		return stage.Agent
	}

	// The model rather than the harness: the harness is which CLI was called and
	// the model is what answered, and a cost belongs to the second. It is recorded
	// only when exactly one model answered a stage.
	answered := spend.Model
	if answered == "" {
		answered = stage.Agent
	}
	return fmt.Sprintf("%-24s %-6s %3dt  $%.4f", answered, spend.Context, spend.Turns, spend.CostUSD)
}

// printWorktrees says where each role's checkout is.
//
// Whether or not it exists: a worktree lasts exactly as long as its stage, so the
// answer to "where is this work" is a path that is there while the stage runs and
// gone after. Naming it either way beats making somebody guess the convention.
func printWorktrees(env Env, state fsm.TaskState, flow []fsm.Stage) {
	roles := packRoles(flow)
	if len(roles) == 0 {
		return
	}

	fmt.Fprintln(env.Out)
	fmt.Fprintln(env.Out, "  worktrees")
	for _, role := range roles {
		path, err := node.WorktreePath(".", state.ID, role)
		if err != nil {
			continue
		}
		here := ""
		if _, err := os.Stat(path); err == nil {
			here = "  (open)"
		}
		fmt.Fprintf(env.Out, "    %-13s %s%s\n", role, nearby(path), here)
	}
}

// knobMeaning says what this setting does for this flow, in the flow's own terms.
//
// A number alone is a setting somebody has to look up. What it means is which of
// *these* gates the lead may answer, and that is a reading of the flow.
func knobMeaning(knob fsm.Knob, flow []fsm.Stage) string {
	var reachable, total int
	for _, stage := range flow {
		if stage.Gate == nil || len(stage.Gate.Judge) == 0 {
			continue
		}
		total++
		if knob.Judges(stage.Gate.AutonomyFloor) {
			reachable++
		}
	}

	switch {
	case total == 0:
		return "this flow opens no gate the lead could answer"
	case reachable == 0:
		return "every gate goes to a person"
	case reachable == total:
		return "the lead may answer every gate this flow has"
	default:
		return fmt.Sprintf("the lead may answer %d of this flow's %d gates", reachable, total)
	}
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

	flow, err := e.flowOf(id)
	if err != nil {
		return fsm.TaskState{}, err
	}
	return e.Store.Replay(id, flow)
}

// flowOf loads the flow a task was opened under.
//
// Every command that reads a task goes through this rather than reaching for the
// shipped flow, and that is the whole of what running several flows costs: a task
// is replayed against its own contract, never against whichever one this build
// calls default.
//
// A name this build no longer has is reported as itself. It is the same situation
// ErrFlowChanged covers — a task whose contract went away underneath it — and the
// remedy is the same, so the message names it.
func (e Env) flowOf(id string) ([]fsm.Stage, error) {
	name, err := e.Store.FlowNameOf(id)
	if err != nil {
		return nil, err
	}
	flow, err := fsm.FlowNamed(name)
	if err != nil {
		return nil, fmt.Errorf("%s ran under %w — `luna task abandon %s` ends a task "+
			"whose flow is gone", id, err, id)
	}
	return flow, nil
}

// handReportedEvidence records the floor for a delivery somebody reported.
//
// Existence, and recorded as exactly that. A stage whose contract declares a
// command will not close on it — underProven refuses the weaker check, and that
// refusal is the point: reporting a stage done by hand must not be a way to
// launder a verdict nobody produced. `luna work` is what closes those, carrying
// what its verifiers actually observed.
func handReportedEvidence(delivered []fsm.Artifact, seq int) map[fsm.Artifact]fsm.Evidence {
	evidence := make(map[fsm.Artifact]fsm.Evidence, len(delivered))
	for _, artifact := range delivered {
		// fsm.Exists rather than a literal: the floor is defined in one place, so
		// a change to what "nothing was checked" records cannot apply here and not
		// to the engine.
		e := fsm.Exists(seq)
		e.Detail = "reported by hand through `luna done`"
		evidence[artifact] = e
	}
	return evidence
}

// fullyBriefed replaces the role's brief with the one the agent would actually be
// given.
//
// `NextOrder` lives in the engine and cannot reach `node.Brief`, so it carried the
// role's own sentence — which is what a role says about itself and not what a
// stage says about this task. A person driving by hand therefore got strictly less
// than the agent Luna starts for the same stage: no contract duty, no gate
// criteria, no list of what is handed over rather than committed.
//
// Composed here because this is the layer that can see both packages. An order for
// a mechanical stage keeps the empty brief it came with: there is no agent, and a
// brief addressed to nobody is noise.
func fullyBriefed(order fsm.Order, state fsm.TaskState, flow []fsm.Stage, cfg Config) fsm.Order {
	if order.Kind != fsm.OrderRun {
		return order
	}

	// One exit rather than three. A mechanical stage and a stage the flow does not
	// contain both leave the order as it came — the first because there is no agent
	// to address, the second because NextOrder only ever names a stage from this
	// flow, so it is a branch no test can reach and none should have to.
	for _, stage := range flow {
		if stage.ID != order.Stage || stage.Mechanical() {
			continue
		}
		order.Brief = node.Brief(state, stage)
		break
	}
	return order
}

// nearby shortens a path against the working directory.
//
// A worktree is a sibling of the repository, so its absolute path is mostly the
// part the reader already knows and is standing in. `../wt-app-T-1-coder` is the
// same answer with the noise removed, and it falls back to the absolute path
// whenever the relative one would be longer or cannot be worked out — which is
// the only case where the long form is the more useful of the two.
func nearby(path string) string {
	here, err := os.Getwd()
	if err != nil {
		return path
	}
	relative, err := filepath.Rel(here, path)
	if err != nil || len(relative) >= len(path) {
		return path
	}
	return relative
}
