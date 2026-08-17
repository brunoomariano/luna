package cli

import (
	"encoding/json"
	"fmt"
	"io"

	"github.com/brunoomariano/luna/src/internal/fsm"
	"github.com/brunoomariano/luna/src/internal/store"
)

// The JSON shapes below are a contract, not a serialisation of whatever the
// engine happens to hold (ADR-0043).
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

	// Blocked is why the task stopped, and is present exactly when the status is
	// blocked — a task that halts without saying why is the silent failure
	// INV-core-8 forbids.
	Blocked string `json:"blocked,omitempty"`

	// Gate is what the task is waiting on, present only when it is waiting.
	Gate *GateReport `json:"gate,omitempty"`

	// Statement is what a person said the task is about. It used to live in the
	// registry, where `task show` could not see it without a second lookup; now it
	// replays with the task (ADR-0067), so the command that shows a task shows it.
	//
	// Absent when nobody described the task, which is the ordinary case.
	Statement *StatementReport `json:"statement,omitempty"`

	Events   int              `json:"events"`
	Produced []ArtifactReport `json:"produced,omitempty"`

	// ProfileDefined is false when the task names a profile the configuration no
	// longer has. The task still replays — its decisions are in its log — but a
	// reader should be able to say so (ADR-0026).
	ProfileDefined bool `json:"profile_defined"`
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
// is the laundering ADR-0028 exists to prevent.
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
func taskReport(cfg Config, state fsm.TaskState, events int) TaskReport {
	_, defined := cfg.Profile(state.Profile)

	report := TaskReport{
		ID:             state.ID,
		Status:         string(state.Status),
		Kind:           string(state.Context.Kind),
		Profile:        string(state.Profile),
		Stage:          string(state.Stage),
		Blocked:        state.Blocked,
		Events:         events,
		ProfileDefined: defined,
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
		_, defined := cfg.Profile(w.Profile)
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
