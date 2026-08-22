package cli

import (
	"encoding/json"
	"fmt"
	"io"

	"github.com/brunoomariano/luna/src/internal/fsm"
	"github.com/brunoomariano/luna/src/internal/store"
)

// The JSON shapes below are a contract, not a serialisation of whatever the
// engine happens to hold.
//
// They exist because the conversational layer has to read state, and parsing
// output written for people makes every reworded message a silent breakage. A
// declared shape also means the engine's internals can change without breaking a
// reader — which is the whole point of writing them out by hand rather than
// tagging fsm.TaskState and hoping.

// TaskReport is what `luna task show --json` answers.
type TaskReport struct {
	ID      string `json:"id"`
	Status  string `json:"status"`
	Kind    string `json:"kind"`
	Profile string `json:"profile"`
	Stage   string `json:"stage,omitempty"`

	// Simulated marks a task whose stages ran no agent. Present in the structured
	// view as well as the printed one, because a consumer reading only the JSON
	// is the reader most likely to treat a verdict as a measurement.
	Simulated bool `json:"simulated,omitempty"`

	// Blocked is why the task stopped, and is present exactly when the status is
	// blocked — a task that halts without saying why is the silent failure
	// INV-5 forbids.
	Blocked string `json:"blocked,omitempty"`

	// Gate is what the task is waiting on, present only when it is waiting.
	Gate *GateReport `json:"gate,omitempty"`

	// Statement is what a person said the task is about. It used to live in the
	// registry, where `task show` could not see it without a second lookup; now it
	// replays with the task, so the command that shows a task shows it.
	//
	// Absent when nobody described the task, which is the ordinary case.
	Statement *StatementReport `json:"statement,omitempty"`

	// Loop is where a convergence loop stands, absent when there is none. The
	// three counters are separate because the ceilings are.
	Loop *LoopReport `json:"loop,omitempty"`

	Events   int              `json:"events"`
	Produced []ArtifactReport `json:"produced,omitempty"`

	// ProfileDefined is false when the task names a profile the configuration no
	// longer has. The task still replays — its decisions are in its log — but a
	// reader should be able to say so.
	ProfileDefined bool `json:"profile_defined"`

	// Flow is which flow the task runs, by name. A reader that cannot tell a task
	// on the lean flow from one on the full flow cannot compare their cost, and
	// comparing their cost is the reason both exist.
	Flow string `json:"flow,omitempty"`

	// Spend is what the task cost, per stage and in total.
	//
	// The printed view has carried this since the transport started reporting
	// usage; the structured one did not, so every consumer that reads JSON — a
	// dashboard, a benchmark, the morning report — had to shell out and parse a
	// column written for a person. What a run costs is the measurement the whole
	// orchestration is judged on, and it belongs in the answer a machine reads.
	Spend *SpendReport `json:"spend,omitempty"`
}

// SpendReport is what a task cost and what it may still spend.
type SpendReport struct {
	CostUSD float64 `json:"cost_usd"`
	Tokens  int     `json:"tokens"`
	Turns   int     `json:"turns"`

	// BudgetUSD is the ceiling, absent when there is none.
	BudgetUSD float64 `json:"budget_usd,omitempty"`

	// Stages is the same numbers per stage, in flow order, so a reader can see
	// where the money went rather than only how much of it there was.
	Stages []StageSpendReport `json:"stages,omitempty"`
}

// StageSpendReport is one stage's bill.
type StageSpendReport struct {
	Stage   string  `json:"stage"`
	CostUSD float64 `json:"cost_usd"`
	Tokens  int     `json:"tokens"`
	Turns   int     `json:"turns"`

	// Context says whether the call started cold or resumed, which is most of why
	// two stages with the same work cost differently.
	Context string `json:"context,omitempty"`
}

// LoopReport is a convergence loop's position, for a reader that wants to know
// how close a task is to a ceiling before it fires.
type LoopReport struct {
	Rounds      int `json:"rounds"`
	NoProgress  int `json:"no_progress"`
	Oscillation int `json:"oscillation"`

	// Compared is the signal the last round produced, which is what "no progress"
	// was decided against (PRD node-0002, RF3).
	Compared string `json:"compared,omitempty"`
}

// StatementReport is what the task is about, as a reader sees it.
type StatementReport struct {
	About      string `json:"about,omitempty"`
	Design     string `json:"design,omitempty"`
	Acceptance string `json:"acceptance,omitempty"`
}

// GateReport is a pause waiting for a person.
type GateReport struct {
	Kind     string `json:"kind"`
	Stage    string `json:"stage"`
	Reason   string `json:"reason"`
	Artifact string `json:"artifact,omitempty"`
	Payload  string `json:"payload,omitempty"`
}

// ArtifactReport is one delivered artifact and what was proven about it.
//
// Scope is the field that matters most here: a reader that cannot tell a green
// suite from a file that merely exists would report the two the same way, which
// is the laundering the scope rule exists to prevent.
type ArtifactReport struct {
	Name     string `json:"name"`
	Scope    string `json:"scope,omitempty"`
	Verdict  string `json:"verdict,omitempty"`
	Command  string `json:"command,omitempty"`
	ExitCode int    `json:"exit_code,omitempty"`
	Detail   string `json:"detail,omitempty"`
}

// GatesReport is what `luna gates --json` answers.
type GatesReport struct {
	// Waiting is never null: a reader looping over it should not have to
	// distinguish "no tasks" from "the field was absent".
	Waiting []WaitingReport `json:"waiting"`
}

// WaitingReport is one task stopped at a gate.
type WaitingReport struct {
	TaskID         string `json:"task_id"`
	Stage          string `json:"stage"`
	Reason         string `json:"reason"`
	Profile        string `json:"profile"`
	ProfileDefined bool   `json:"profile_defined"`
}

// taskReport builds the machine-readable view of a task.
func taskReport(cfg Config, state fsm.TaskState, events int, flow []fsm.Stage) TaskReport {
	defined := cfg.Defines(state.Profile)

	report := TaskReport{
		ID:             state.ID,
		Status:         string(state.Status),
		Kind:           string(state.Context.Kind),
		Profile:        string(state.Profile),
		Stage:          string(state.Stage),
		Simulated:      state.Simulated,
		Blocked:        state.Blocked,
		Events:         events,
		ProfileDefined: defined,
		Flow:           state.FlowName,
		Spend:          spendReport(state, flow),
	}

	if state.Loop.Rounds > 0 {
		report.Loop = &LoopReport{
			Rounds:      state.Loop.Rounds,
			NoProgress:  state.Loop.NoProgress,
			Oscillation: state.Loop.Oscillation,
			Compared:    state.Loop.LastProgress,
		}
	}

	if state.Statement.Stated() {
		report.Statement = &StatementReport{
			About:      state.Statement.Description,
			Design:     state.Statement.Design,
			Acceptance: state.Statement.Acceptance,
		}
	}

	if state.Gate != nil {
		report.Gate = &GateReport{
			Kind:     string(state.Gate.Kind),
			Stage:    string(state.Gate.Stage),
			Reason:   state.Gate.Reason,
			Artifact: string(state.Gate.Artifact),
			Payload:  state.Gate.Payload,
		}
	}

	for _, name := range sortedArtifacts(state.Context.Artifacts) {
		artifact := ArtifactReport{Name: string(name)}
		if evidence, ok := state.Evidence[name]; ok && evidence.Delivered() {
			artifact.Scope = string(evidence.Scope)
			artifact.Verdict = string(evidence.Verdict)
			artifact.Command = evidence.Command
			artifact.ExitCode = evidence.ExitCode
			artifact.Detail = evidence.Detail
		}
		report.Produced = append(report.Produced, artifact)
	}

	return report
}

// writeJSON prints a report, indented so a person reading it over someone's
// shoulder can still follow it.
func writeJSON(out io.Writer, v any) error {
	encoded, err := json.MarshalIndent(v, "", "  ")
	if err != nil {
		return fmt.Errorf("encoding the report: %w", err)
	}

	_, err = fmt.Fprintln(out, string(encoded))
	return err
}

// gatesReport builds the machine-readable view of what is waiting.
func gatesReport(cfg Config, waiting []store.Waiting) GatesReport {
	// Never null: a reader looping over it should not have to distinguish "no
	// tasks" from "the field was absent".
	report := GatesReport{Waiting: []WaitingReport{}}

	for _, w := range waiting {
		defined := cfg.Defines(w.Profile)
		report.Waiting = append(report.Waiting, WaitingReport{
			TaskID:         w.TaskID,
			Stage:          string(w.Stage),
			Reason:         w.Reason,
			Profile:        string(w.Profile),
			ProfileDefined: defined,
		})
	}
	return report
}

// wantsJSON reads the one flag a reading command accepts.
//
// Reading commands take `--json` and nothing else, so an unknown flag is a
// mistake worth naming rather than ignoring.
func wantsJSON(args []string) (bool, error) {
	flags, err := parseFlags(args)
	if err != nil {
		return false, err
	}

	for name := range flags {
		if name != "json" {
			return false, fmt.Errorf("%w: unknown flag --%s", ErrUsage, name)
		}
	}

	_, asJSON := flags["json"]
	return asJSON, nil
}

// spendReport is what the task cost, or nothing when it has not cost anything.
//
// Absent rather than zeroed for a task that never ran a stage: a reader seeing
// `"cost_usd": 0` cannot tell "nothing was spent" from "nothing is recorded", and
// only one of those is a fact about the run.
func spendReport(state fsm.TaskState, flow []fsm.Stage) *SpendReport {
	if len(state.Spent) == 0 && state.BudgetUSD == 0 {
		return nil
	}

	total := state.TotalSpend()
	report := &SpendReport{
		CostUSD:   total.CostUSD,
		Tokens:    total.Tokens(),
		Turns:     total.Turns,
		BudgetUSD: state.BudgetUSD,
	}

	// Flow order rather than map order, so the list reads like the run did.
	for _, stage := range flow {
		spend, ran := state.Spent[stage.ID]
		if !ran {
			continue
		}
		report.Stages = append(report.Stages, StageSpendReport{
			Stage:   string(stage.ID),
			CostUSD: spend.CostUSD,
			Tokens:  spend.Tokens(),
			Turns:   spend.Turns,
			Context: spend.Context,
		})
	}
	return report
}
