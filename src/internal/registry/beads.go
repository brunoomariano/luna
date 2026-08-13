// Package registry is where a task lives: its status, its stage, and the
// provenance of what each stage delivered.
//
// It is beads (RFC-0002 phase 2). Luna's own append-only store answered the same
// question and answered it well, but it was one SQLite file per worktree — so
// "what is waiting on a person across everything" meant replaying every log in
// every checkout, and the six pieces of machinery built to defend that choice
// were all in service of a registry that already exists.
//
// What Luna keeps is the part beads does not do: the contract, the verification,
// and the decision about which stage comes next. Those never move.
package registry

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os/exec"
	"strings"
	"time"
)

// commandTimeout bounds a call to bd. It is a local process against a local
// database; ten seconds is generous and still bounded, because a registry that
// hangs must not hang the task asking about it.
const commandTimeout = 10 * time.Second

// Exit codes bd uses, measured against 1.2.1 rather than read from its
// documentation — the discipline ADR-0036 arrived at after ten protocol facts
// about herdr turned out to be wrong.
const (
	// exitGuardMismatch is what `--if-status` returns when the precondition no
	// longer holds. bd's own help is explicit that nothing was written and that
	// retrying the same guard is pointless: another actor won the race.
	exitGuardMismatch = 13
)

// ErrLostTheRace means something else moved the task between the decision and
// the write.
//
// It is the same guarantee `AppendActionAt` gave, arrived at differently: there
// the log had to still end where the caller read it, here the status has to
// still be what the caller saw. Both refuse to write a decision taken against a
// state that has since changed (ADR-0047).
var ErrLostTheRace = errors.New("the task moved between the decision and the write")

// ErrNoSuchTask is a task the registry has never heard of.
var ErrNoSuchTask = errors.New("no such task")

// Status is what beads says about a task. The values are beads' own, not Luna's:
// mapping them onto fsm.Status happens at the edge, and keeping the vocabularies
// apart is what stops a rename in either one silently meaning something else.
type Status string

// The four beads statuses Luna uses. beads ships three more — deferred, pinned
// and hooked — and Luna names none of them: a status it does not write is a
// status it should not claim to understand.
const (
	StatusOpen       Status = "open"
	StatusInProgress Status = "in_progress"
	StatusBlocked    Status = "blocked"
	StatusClosed     Status = "closed"
)

// Work is what a task is about, as a person stated it.
//
// The three fields are beads' own — measured against bd 1.2.1, `create` takes
// `-d`, `--design` and `--acceptance`, and `show --json` returns them as
// `description`, `design` and `acceptance_criteria`. Luna does not invent a
// parallel place for the same information: the registry is where a task lives
// (ADR-0054), so this is where the statement of work lives too.
//
// Only Title is required. A task may enter with a one-line title and acquire the
// rest later, in beads, by hand — which is the point of the registry being
// somewhere other than inside Luna.
type Work struct {
	Title string

	// Description is what to build. Its absence is what left the agents inferring
	// the goal from the task id.
	Description string

	// Design is the technical route, when one was already decided.
	Design string

	// Acceptance is how the work will be judged. It is what a `spec` or a
	// `scenarios` stage has to write against, and without it those stages stop to
	// ask — measured, six times out of six.
	Acceptance string
}

// Task is what the registry holds about one task.
type Task struct {
	ID     string `json:"id"`
	Title  string `json:"title"`
	Status Status `json:"status"`

	// What the task is about. Read back so a task created directly in beads —
	// with no `luna task new` involved — reaches the agents with its statement of
	// work intact.
	Description string `json:"description,omitempty"`
	Design      string `json:"design,omitempty"`
	Acceptance  string `json:"acceptance_criteria,omitempty"`

	// Labels carry the stage. beads has no concept of a stage and should not
	// grow one — the flow is Luna's (INV-core-1) — so the current stage rides as
	// a label rather than as a field beads would have to understand.
	Labels []string `json:"labels,omitempty"`

	UpdatedAt time.Time `json:"updated_at"`
}

// Stage reads the stage back out of the labels.
func (t Task) Stage() string {
	for _, label := range t.Labels {
		if after, ok := strings.CutPrefix(label, stagePrefix); ok {
			return after
		}
	}
	return ""
}

// stagePrefix namespaces Luna's labels so they cannot collide with a label a
// person put on a task for their own reasons.
const stagePrefix = "luna:stage:"

// Beads talks to the `bd` binary.
//
// Shelling out rather than importing: beads is a separate product with its own
// release cycle, and linking it in would make Luna's build depend on its module
// graph. The CLI is its documented surface and speaks JSON, which is the same
// reasoning that put herdr behind a command rather than a library (ADR-0027).
type Beads struct {
	// Dir is the repository whose registry to use. bd discovers `.beads/*.db`
	// from it, the same way git discovers `.git`.
	Dir string

	// Run executes bd and returns its stdout, its exit code and an error for a
	// failure to run at all. A field so a test can exercise every path without
	// the binary installed, and so the real one can be measured in the few tests
	// that need to be.
	Run func(ctx context.Context, dir string, args ...string) (stdout []byte, exitCode int, err error)
}

// New returns a registry backed by the real binary.
func New(dir string) *Beads {
	return &Beads{Dir: dir, Run: runBD}
}

// Create opens a task in the registry and returns the id beads assigned.
//
// The id comes back from beads rather than being chosen by Luna, which is the
// point of a central registry: two worktrees cannot invent the same one.
//
// An empty field is omitted rather than sent empty. bd tells an absent field
// apart from a blank one, and `-d ""` would erase a description somebody wrote by
// hand — the case where a person drafts the task in beads and Luna picks it up.
func (b *Beads) Create(ctx context.Context, work Work) (string, error) {
	// A fixed order, not a map: the same task must produce the same command line
	// every run, or two identical creations look different in a log.
	args := []string{"create", work.Title, "--json"}
	for _, field := range []struct{ flag, value string }{
		{"-d", work.Description},
		{"--design", work.Design},
		{"--acceptance", work.Acceptance},
	} {
		if field.value != "" {
			args = append(args, field.flag, field.value)
		}
	}

	out, _, err := b.run(ctx, args...)
	if err != nil {
		return "", err
	}

	var created Task
	if err := json.Unmarshal(out, &created); err != nil {
		return "", fmt.Errorf("reading the created task: %w", err)
	}
	if created.ID == "" {
		return "", fmt.Errorf("beads created a task with no id: %s", truncate(out))
	}
	return created.ID, nil
}

// Task reads one task back.
func (b *Beads) Task(ctx context.Context, id string) (Task, error) {
	out, code, err := b.run(ctx, "show", id, "--json")
	if err != nil {
		if code == 1 {
			return Task{}, fmt.Errorf("%w: %s", ErrNoSuchTask, id)
		}
		return Task{}, err
	}

	// `bd show` returns an array even for one id.
	var tasks []Task
	if err := json.Unmarshal(out, &tasks); err != nil {
		return Task{}, fmt.Errorf("reading %s: %w", id, err)
	}
	if len(tasks) == 0 {
		return Task{}, fmt.Errorf("%w: %s", ErrNoSuchTask, id)
	}
	return tasks[0], nil
}

// Move changes a task's status, but only if it is still where the caller saw it.
//
// The guard is not optional and has no unguarded sibling, deliberately. An
// unconditional write is how two leads both decide from the same state and both
// act on it, and the whole reason this is a central registry is that there can
// now be more than one of them.
func (b *Beads) Move(ctx context.Context, id string, from, to Status) error {
	_, code, err := b.run(ctx, "update", id,
		"--status", string(to),
		"--if-status", string(from),
		"--json")

	if code == exitGuardMismatch {
		return fmt.Errorf("%w: %s was %s when the decision was made", ErrLostTheRace, id, from)
	}
	return err
}

// EnterStage records which stage a task is in.
//
// The old stage label is removed before the new one is added, so a task carries
// exactly one. Two would make `Stage()` return whichever came first, which is a
// question with no right answer.
func (b *Beads) EnterStage(ctx context.Context, id, stage string) error {
	current, err := b.Task(ctx, id)
	if err != nil {
		return err
	}

	if previous := current.Stage(); previous != "" {
		if previous == stage {
			return nil
		}
		if _, _, err := b.run(ctx, "label", "remove", id, stagePrefix+previous); err != nil {
			return fmt.Errorf("clearing the stage label on %s: %w", id, err)
		}
	}

	if _, _, err := b.run(ctx, "label", "add", id, stagePrefix+stage); err != nil {
		return fmt.Errorf("recording stage %s on %s: %w", stage, id, err)
	}
	return nil
}

// RecordCommit binds a commit to a task, as provenance.
//
// This is the audit trail in the new model: git holds how the task got there,
// and the registry holds the pointers. beads' provenance log is append-only by
// construction — no update, no delete — and idempotent on a deterministic id, so
// a stage reported twice records once (INV-core-2).
//
// The ref-kind is `git-sha`, which beads validates: it insists on a full
// 40-character lowercase hex, and a short sha is refused rather than stored.
// That refusal is worth keeping — an abbreviated commit is ambiguous in a
// repository large enough to matter.
func (b *Beads) RecordCommit(ctx context.Context, id, commit, stage string) error {
	payload, err := json.Marshal(map[string]string{"stage": stage})
	if err != nil {
		return fmt.Errorf("describing the delivery: %w", err)
	}

	_, _, err = b.run(ctx, "provenance", "record",
		"--issue", id,
		"--kind", "commit",
		"--source", "luna",
		"--ref", commit,
		"--ref-kind", "git-sha",
		"--payload", string(payload),
		"--json")
	return err
}

// Commits lists what a task has delivered, oldest first.
func (b *Beads) Commits(ctx context.Context, id string) ([]Delivery, error) {
	out, _, err := b.run(ctx, "provenance", "log", id, "--json")
	if err != nil {
		return nil, err
	}

	var events []struct {
		Kind      string    `json:"kind"`
		Ref       string    `json:"ref"`
		Payload   string    `json:"payload"`
		CreatedAt time.Time `json:"created_at"`
	}
	if err := json.Unmarshal(out, &events); err != nil {
		return nil, fmt.Errorf("reading the provenance of %s: %w", id, err)
	}

	var deliveries []Delivery
	for _, e := range events {
		if e.Kind != "commit" {
			continue
		}
		d := Delivery{Commit: e.Ref, At: e.CreatedAt}
		// The payload is opaque to beads, so a shape it cannot parse is Luna's
		// problem rather than an error: the commit is the fact, the stage is a
		// convenience.
		var detail struct {
			Stage string `json:"stage"`
		}
		if json.Unmarshal([]byte(e.Payload), &detail) == nil {
			d.Stage = detail.Stage
		}
		deliveries = append(deliveries, d)
	}
	return deliveries, nil
}

// Delivery is one stage's commit, as the registry recorded it.
type Delivery struct {
	Commit string
	Stage  string
	At     time.Time
}

// Blocked lists the tasks the registry says are stopped.
//
// This is the query the local store could not answer without replaying every
// log in every worktree, and it is most of why the registry moved (INV-core-12).
func (b *Beads) Blocked(ctx context.Context) ([]Task, error) {
	out, _, err := b.run(ctx, "list", "--status", string(StatusBlocked), "--json")
	if err != nil {
		return nil, err
	}

	var tasks []Task
	if err := json.Unmarshal(out, &tasks); err != nil {
		return nil, fmt.Errorf("reading the blocked tasks: %w", err)
	}
	return tasks, nil
}

// run calls bd and turns a non-zero exit into an error carrying what it said.
func (b *Beads) run(ctx context.Context, args ...string) ([]byte, int, error) {
	if b.Run == nil {
		return nil, 0, errors.New("no bd runner configured")
	}

	out, code, err := b.Run(ctx, b.Dir, args...)
	if err != nil {
		return out, code, fmt.Errorf("bd %s: %w", args[0], err)
	}
	// A guard mismatch is a documented outcome rather than a failure, so it is
	// returned without an error and the caller decides what it means.
	if code != 0 && code != exitGuardMismatch {
		return out, code, fmt.Errorf("bd %s exited %d: %s", args[0], code, truncate(out))
	}
	return out, code, nil
}

// runBD executes the real binary.
func runBD(ctx context.Context, dir string, args ...string) ([]byte, int, error) {
	ctx, cancel := context.WithTimeout(ctx, commandTimeout)
	defer cancel()

	cmd := exec.CommandContext(ctx, "bd", args...) //nolint:gosec // fixed subcommands
	cmd.Dir = dir

	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr

	err := cmd.Run()
	if err == nil {
		return stdout.Bytes(), 0, nil
	}

	var exit *exec.ExitError
	if errors.As(err, &exit) {
		// A non-zero exit is information, not a failure to run. What bd wrote to
		// stderr is carried in the message because that is where it explains
		// itself.
		out := stdout.Bytes()
		if stderr.Len() > 0 {
			out = append(out, stderr.Bytes()...)
		}
		return out, exit.ExitCode(), nil
	}
	return nil, 0, fmt.Errorf("running bd: %w (is it installed? `mise install`)", err)
}

// truncate keeps an error message readable when bd writes a wall of output.
func truncate(out []byte) string {
	const limit = 300

	s := strings.TrimSpace(string(out))
	if len(s) <= limit {
		return s
	}
	return s[:limit] + "…"
}
