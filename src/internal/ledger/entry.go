package ledger

import (
	"encoding/json"
	"fmt"
	"sort"
	"strings"
	"time"
)

// Event is what happened. The set is closed: an unknown event is refused rather
// than recorded, because a reader that meets one has no way to tell a new kind of
// fact from a typo.
type Event string

const (
	// EventPhase is a phase changing what it is doing.
	EventPhase Event = "phase"

	// EventCheck is a contract check that ran, with its verdict.
	EventCheck Event = "check"

	// EventGate is a gate being answered, and by whom.
	EventGate Event = "gate"

	// EventBlock is a run stopping for something only a person can settle.
	EventBlock Event = "block"

	// EventUnblock is that block being answered.
	EventUnblock Event = "unblock"

	// EventAutonomy is the autonomy mode moving mid-run.
	EventAutonomy Event = "autonomy"

	// EventDiscovery is what a phase found out about the project it is working
	// in: which command is this repository's own gate, how it bootstraps, what
	// its sandbox allows.
	//
	// Recorded, never consulted. Luna does not learn a project's gate and reuse
	// it — the command arrives in the contract on every call, because a gate Luna
	// remembered is configuration Luna owns, and owning project configuration is
	// what `luna init` and the per-project store were removed for. What this
	// buys is a trail that says *why* a later check ran `pnpm check` rather than
	// leaving a reader to guess.
	EventDiscovery Event = "discovery"
)

var events = map[Event]bool{
	EventPhase: true, EventCheck: true, EventGate: true,
	EventBlock: true, EventUnblock: true, EventAutonomy: true,
	EventDiscovery: true,
}

// Status is where a run stands. Closed for the same reason as Event.
type Status string

const (
	// StatusRunning means a phase is being worked on.
	StatusRunning Status = "running"

	// StatusAwaitingGate means a person has been asked something.
	StatusAwaitingGate Status = "awaiting_gate"

	// StatusAwaitingResume means the run was prepared and is waiting for a
	// session to pick it up. It is what a batch leaves behind, and it is the whole
	// handoff — there is no file beside it.
	StatusAwaitingResume Status = "awaiting_resume"

	// StatusBlocked means the run stopped for missing information.
	StatusBlocked Status = "blocked"

	// StatusDone means the run finished.
	StatusDone Status = "done"

	// StatusAbandoned means the run was given up on.
	StatusAbandoned Status = "abandoned"
)

var statuses = map[Status]bool{
	StatusRunning: true, StatusAwaitingGate: true, StatusAwaitingResume: true,
	StatusBlocked: true, StatusDone: true, StatusAbandoned: true,
}

// Entry is one line of the ledger.
//
// Self-contained on purpose: it carries the run, the project and the phase, so
// reading one line never requires reading the lines before it. That is what lets
// the state be a tail rather than a fold, and what lets a concurrent fleet append
// without coordinating.
//
// Every field but Run, At and Event is optional, and an omitted one stays out of
// the JSON. A line is read by people in a terminal as often as by a program.
type Entry struct {
	// At is when this happened, in UTC. Set by Append when the caller leaves it
	// zero, which is every caller but a test.
	At time.Time `json:"at"`

	// Run is what this line is about. It is the only field a reader needs to
	// group by, and the only one Append refuses to default.
	Run string `json:"run"`

	// Project is where the work is, normalised from the git remote or the
	// checkout path. It is carried so a global report can group by repository
	// without opening any of them.
	Project string `json:"project,omitempty"`

	// Phase is which phase this line belongs to.
	Phase string `json:"phase,omitempty"`

	// Event is what kind of fact this is.
	Event Event `json:"event"`

	// Status is where the run stands after this line, when the event moves it.
	Status Status `json:"status,omitempty"`

	// Worktree is where the work is checked out. The ledger knows where the
	// worktree is, rather than the worktree knowing where the ledger is — which is
	// what lets a record outlive the tree it describes.
	Worktree string `json:"worktree,omitempty"`

	// Round is which round of the phase's convergent loop this is — build, clean,
	// check, judge — counted here rather than by the model.
	//
	// Not a count of verification attempts. The distinction cost a real run: a
	// caller numbered three `check` calls 1, 2 and 3 while the loop had gone
	// round twice, and the loop's ceilings are read off this field. A retry
	// recorded as a round makes a phase look like it iterated when it was the
	// same round being checked again.
	Round int `json:"round,omitempty"`

	// Artifact, Verdict, Scope, Command and Exit describe a check.
	Artifact string `json:"artifact,omitempty"`
	Verdict  string `json:"verdict,omitempty"`
	Scope    string `json:"scope,omitempty"`
	Command  string `json:"command,omitempty"`
	Exit     int    `json:"exit,omitempty"`

	// Gate and Answer describe a gate being settled.
	Gate   string `json:"gate,omitempty"`
	Answer string `json:"answer,omitempty"`

	// Autonomy is the mode, on the line that moves it.
	Autonomy string `json:"autonomy,omitempty"`

	// Question, Looked and Needs are the three parts of a block, and all three
	// are required for one. See INV-5: the middle one is what separates a real
	// block from an unread file.
	Question string   `json:"question,omitempty"`
	Looked   []string `json:"looked,omitempty"`
	Needs    string   `json:"needs,omitempty"`

	// Found is what a discovery concluded, and Where is what it read to conclude
	// it. Both are prose: "pnpm check" and "package.json, scripts.check".
	//
	// Where is not decoration. A gate named with no source is a claim, and the
	// person answering the gate that follows has to be able to check it against
	// the file it came from.
	Found string `json:"found,omitempty"`
	Where string `json:"where,omitempty"`

	// Note is free text for whatever the fields above do not cover.
	Note string `json:"note,omitempty"`

	// Simulated marks a line produced by a dry run. A simulation that reads like
	// a result is a lie with the truth beside it, and the reader only ever sees
	// one of the two.
	Simulated bool `json:"simulated,omitempty"`
}

// Validate refuses a line that cannot be read back usefully.
//
// Every fault at once rather than the first, for the same reason the contract
// lints that way: a caller fixing one field at a time pays a round trip per
// mistake.
func (e Entry) Validate() error {
	var faults []string

	if strings.TrimSpace(e.Run) == "" {
		faults = append(faults, "no run named: a line nothing can be grouped by is a line nobody can read back")
	}
	if !events[e.Event] {
		faults = append(faults, fmt.Sprintf(
			"unknown event %q — one of phase, check, gate, block, unblock, autonomy, discovery", e.Event))
	}
	if e.Status != "" && !statuses[e.Status] {
		faults = append(faults, fmt.Sprintf("unknown status %q", e.Status))
	}
	faults = append(faults, e.validateBlock()...)
	faults = append(faults, e.validateDiscovery()...)

	if len(faults) == 0 {
		return nil
	}
	sort.Strings(faults)
	return fmt.Errorf("this line cannot be recorded:\n  - %s", strings.Join(faults, "\n  - "))
}

// validateBlock holds a block to all three of its parts.
//
// The one that earns the rule is `looked`: a block saying only what it wants is
// indistinguishable from a phase that did not read what it already had. Making it
// mandatory is what keeps "I could not find out" honest.
func (e Entry) validateBlock() []string {
	if e.Event != EventBlock {
		return nil
	}
	var faults []string
	if strings.TrimSpace(e.Question) == "" {
		faults = append(faults, "a block with no question: say what could not be settled")
	}
	if len(e.Looked) == 0 {
		faults = append(faults, "a block with no account of where the answer was looked for — that account is what separates a real block from an unread file")
	}
	if strings.TrimSpace(e.Needs) == "" {
		faults = append(faults, "a block that does not say what would unblock it")
	}
	return faults
}

// validateDiscovery holds a discovery to both of its halves.
//
// The one that earns the rule is `where`: a finding with no source cannot be
// checked, and the whole point of recording it is that somebody answering the
// next gate can go and look.
func (e Entry) validateDiscovery() []string {
	if e.Event != EventDiscovery {
		// `--found` on anything else is accepted and rendered, but `--where` is
		// the half that only means something beside a finding: a source with
		// nothing found is a line that points at a file and says nothing about it.
		if strings.TrimSpace(e.Where) != "" && strings.TrimSpace(e.Found) == "" {
			return []string{fmt.Sprintf(
				"--where on a %q event with no --found: a source with nothing found says nothing", e.Event)}
		}
		return nil
	}
	var faults []string
	if strings.TrimSpace(e.Found) == "" {
		faults = append(faults, "a discovery with nothing found: say what was concluded")
	}
	if strings.TrimSpace(e.Where) == "" {
		faults = append(faults, "a discovery with no source — say which file it was read from, "+
			"because a finding nobody can check is a claim")
	}
	return faults
}

// MarshalJSON writes the line, with the time in a form a person can read.
func (e Entry) MarshalJSON() ([]byte, error) {
	type entry Entry // shed the method, keep the fields
	return json.Marshal(struct {
		At string `json:"at"`
		entry
	}{
		At:    e.At.UTC().Format(time.RFC3339),
		entry: entry(e),
	})
}

// UnmarshalJSON reads a line back.
func (e *Entry) UnmarshalJSON(data []byte) error {
	type entry Entry
	var raw struct {
		At string `json:"at"`
		entry
	}
	if err := json.Unmarshal(data, &raw); err != nil {
		return err
	}
	*e = Entry(raw.entry)
	if raw.At == "" {
		return nil
	}
	at, err := time.Parse(time.RFC3339, raw.At)
	if err != nil {
		return fmt.Errorf("unreadable timestamp %q: %w", raw.At, err)
	}
	e.At = at
	return nil
}

// VerdictFailed is what a check that did not pass records.
//
// It is a constant here as well as in the verify package because the ledger reads
// it back — a report counting failures by matching a literal string would go
// silently wrong the day the other side changed the word.
const VerdictFailed = "failed"
