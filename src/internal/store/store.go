// Package store persists a task's history and replays it back into state.
//
// The current state is not stored — it is derived by replaying the event log
// through the reducer. That is what makes killing the
// process and starting it again rebuild the exact state: it was never only in
// memory. It also means the log can never be rewritten, so this package offers
// no way to update or delete an event.
package store

import (
	"database/sql"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"time"

	_ "modernc.org/sqlite" // pure-Go driver, no CGO

	"github.com/brunoomariano/luna/src/internal/fsm"
)

// ErrUnknownAction is returned when the log holds an action this build cannot
// read — a log written by a newer version, most likely. Replay stops rather than
// guessing, because guessing would rebuild the task into a state it was never in.
var ErrUnknownAction = errors.New("unknown action in the log")

// ErrFlowChanged is returned when a log was written under a different flow than
// the one it is being replayed against.
//
// The same refusal as ErrUnknownAction and for the same reason, except that this
// one used to be silent: a renamed stage replayed as the new name with no error
// at all, and a stage inserted mid-flow made the task re-run work it had already
// completed. There is no recovery inside an append-only log, so a task that meets
// this is ended with `luna task abandon`.
var ErrFlowChanged = errors.New("the flow changed under an open task")

// ErrConcurrentWrite is returned when an append was conditional on the log ending
// somewhere and it does not.
//
// It means two writers decided from the same state. Refusing the second is the
// point: letting it land would put two decisions taken from one state into the
// log, and replaying that yields ErrIllegalTransition forever with no way to
// repair it, since the store has no UPDATE and no DELETE.
var ErrConcurrentWrite = errors.New("the task moved since it was read")

// unconditional is the `after` for an append by a caller that did not read the
// log first — creating a task. Negative rather than zero, because zero is a real
// position: the state of a task whose log is empty.
const unconditional = -1

// Event is one recorded transition. Seq orders it within its task.
type Event struct {
	Seq     int
	Action  string
	Payload string
}

// Waiting is a task suspended at a gate, as `luna gates` would list it.
type Waiting struct {
	TaskID string
	Stage  fsm.StageID
	Reason string

	// Profile is carried so the listing can flag one this build does not know,
	// which happens when a log was written by a newer version.
	Profile fsm.Profile
}

// Store is the append-only log, in one SQLite file in the main repository.
//
// The log is all of it. A content-addressed store once sat beside it so a
// handoff could carry a snapshot of what the previous stage produced; the commit
// replaced that, and git stores content better than a table of
// blobs ever did. Nothing here holds an artifact — it holds the facts
// about what happened to them.
type Store struct {
	db *sql.DB

	// Now is the clock the log is stamped with. It is a field so a test can
	// place events in time without sleeping, and it is on the store rather than
	// anywhere nearer the engine because this is the only layer allowed to read
	// a clock at all.
	Now func() time.Time

	// As is who this store writes on behalf of. It has to be LunaOwnsTheLog, and
	// the field exists precisely so that it cannot default to it — a zero value
	// meaning "Luna" would make the ownership rule true by accident, and a rule
	// that holds by accident is one a refactor removes without a test noticing.
	//
	// Reading does not require it. Anyone may replay a task; only Luna appends.
	As Owner
}

// mayWrite reports whether this store is allowed to append.
func (s *Store) mayWrite() error {
	if s.As != LunaOwnsTheLog {
		return ErrNotTheOwner
	}
	return nil
}

// now is the store's clock, defaulting to the real one.
func (s *Store) now() time.Time {
	if s.Now == nil {
		return time.Now()
	}
	return s.Now()
}

const schema = `
CREATE TABLE IF NOT EXISTS events (
    task_id  TEXT    NOT NULL,
    seq      INTEGER NOT NULL,
    action   TEXT    NOT NULL,
    payload  TEXT    NOT NULL DEFAULT '',
    -- When the row was written, in unix seconds. It is metadata about the log
    -- and never part of the state: Replay does not read it, and the reducer
    -- could not use it without ceasing to be pure.
    --
    -- It exists for the watchdog, which asks a question no replay can answer —
    -- "how long has this been blocked" — because a rebuilt state carries no
    -- clock. Every insert writes it; the default is what an aggregate
    -- over no rows returns, which the watchdog reads as "no age" rather than as a
    -- time in 1970.
    at       INTEGER NOT NULL DEFAULT 0,
    PRIMARY KEY (task_id, seq)
);

-- What a stage handed over that does not belong in the repository: the contract,
-- the scenarios, the audit reports.
--
-- Append-only like the log, and for the same reason (INV-2): a revised
-- artifact is a new row and both versions stay readable. There is no UPDATE and
-- no DELETE except by task, which is the cleanup a finished task earns.
--
-- Keyed by stage as well as artifact because the pair collides in practice:
-- "build" and "refactor" both produce "code" and "tests_green", a loop revisits a
-- stage, and a gate replaces an artifact with the human's version. Without the
-- stage those are one line of history separated only by seq, and INV-3 asks
-- for the location of each artifact *produced* — which a stage declares.
CREATE TABLE IF NOT EXISTS blobs (
    task_id  TEXT    NOT NULL,
    stage    TEXT    NOT NULL,
    artifact TEXT    NOT NULL,
    seq      INTEGER NOT NULL,
    -- The content's sha256, so evidence can name what it saw rather than that it
    -- saw something.
    hash     TEXT    NOT NULL,
    body     BLOB    NOT NULL,
    at       INTEGER NOT NULL DEFAULT 0,
    PRIMARY KEY (task_id, stage, artifact, seq)
);
`

// Open returns a store that may read but not write.
//
// Reading is the safe half and needs no ceremony: replaying a task, listing what
// is blocked, checking a flow. Appending is what has an owner, and OpenAs is how
// it is claimed.
//
// Nothing in Luna's own binary calls this today, and that is honest rather than
// an oversight: `luna` is the owner, so it opens for writing and the read
// commands simply do not write. It exists because the read-only store is the
// shape anything *else* should get — a dashboard, an agent that wants to look at
// its own task, a script — and because the ownership rule is only demonstrable
// if there is a store that lacks it. Its callers are the tests that prove the
// rule holds.
func Open(path string) (*Store, error) {
	return openOwned(path, "")
}

// OpenAs returns a store that writes on behalf of owner.
//
// Only LunaOwnsTheLog may append, and the value has to be passed rather than
// defaulted. That is the whole mechanism: a caller who wants to write says so at
// the point of opening, in code, where it is visible in review — the same shape
// as `Merger.As` for the shared git.
func OpenAs(path string, owner Owner) (*Store, error) {
	return openOwned(path, owner)
}

// openOwned is what both constructors call. Separate from them so the owner is
// always an explicit argument on the way in, even for the read-only door.
func openOwned(path string, owner Owner) (*Store, error) {
	if dir := filepath.Dir(path); dir != "" {
		if err := os.MkdirAll(dir, 0o750); err != nil {
			return nil, fmt.Errorf("creating %s: %w", dir, err)
		}
	}

	// The pragmas are not tuning, they are what makes a second luna process a
	// wait instead of a failure. Without them a plain `sql.Open` gives rollback
	// journalling and no busy handler, so two commands on one store lose almost
	// every append to SQLITE_BUSY — measured at 19 of 20, and each loss aborted a
	// task without recording a block.
	//
	//   - WAL lets a reader and a writer work at once, which is the ordinary case
	//     here: `luna gates` replays every task while a run is mid-flight.
	//   - busy_timeout makes a writer wait for the lock rather than fail on
	//     contact. Five seconds is far longer than an append needs and far shorter
	//     than a person waits before assuming something hung.
	db, err := sql.Open("sqlite", path+"?_pragma=journal_mode(WAL)&_pragma=busy_timeout(5000)")
	if err != nil {
		return nil, fmt.Errorf("opening %s: %w", path, err)
	}
	// One connection per store. SQLite serialises writers anyway, and the pool was
	// producing the contention it looks like it should relieve: two connections
	// from the same process race for the write lock without either of them going
	// through the busy handler, so the second gets SQLITE_BUSY immediately. With a
	// single connection, database/sql queues them instead — waiting rather than
	// failing is the whole point.
	//
	// Cross-process contention is what busy_timeout above is for.
	db.SetMaxOpenConns(1)

	if _, err := db.Exec(schema); err != nil {
		_ = db.Close()
		return nil, fmt.Errorf("creating the schema in %s: %w", path, err)
	}

	return &Store{db: db, As: owner}, nil
}

// Close releases the database handle.
func (s *Store) Close() error { return s.db.Close() }

// beginImmediate starts a write transaction that waits for the lock instead of
// failing on contact with another writer.
//
// database/sql has no option for it — `BeginTx` always issues a deferred BEGIN —
// so the statement is sent by hand on the connection the transaction will use.
func (s *Store) beginImmediate() (*sql.Tx, error) {
	tx, err := s.db.Begin()
	if err != nil {
		return nil, err
	}
	if _, err := tx.Exec("ROLLBACK; BEGIN IMMEDIATE"); err != nil {
		_ = tx.Rollback()
		return nil, err
	}
	return tx, nil
}

// Append records an event at the end of a task's log.
//
// There is no counterpart that replaces or removes one: the history is the audit
// trail, and a store that could rewrite it would not be one.
func (s *Store) Append(taskID string, e Event) error {
	return s.appendTx(taskID, e, unconditional)
}

// AppendAction records a reducer action, which is the form the log actually takes
// during a run.
func (s *Store) AppendAction(taskID string, action fsm.Action) error {
	name, payload, err := encodeAction(action)
	if err != nil {
		return err
	}
	return s.Append(taskID, Event{Action: name, Payload: payload})
}

// AppendActionAt records an action only if the log is still where the caller last
// read it.
//
// `after` is the sequence the caller's state was replayed from — the append lands
// at `after + 1` or not at all. Anyone who decided from a state and then writes
// should use this rather than Append: between the replay and the append there is a
// window, and something landing in it means the decision was made against a task
// that has since moved.
//
// Without it the second writer wins silently and the log ends up holding two
// decisions taken from the same state, which replays into ErrIllegalTransition
// forever. There is no repair — the store has no UPDATE and no DELETE — so the
// only way out of that is to abandon the task.
func (s *Store) AppendActionAt(taskID string, after int, action fsm.Action) error {
	name, payload, err := encodeAction(action)
	if err != nil {
		return err
	}
	return s.appendTx(taskID, Event{Action: name, Payload: payload}, after)
}

// appendTx writes one event.
//
// A non-negative `after` makes the append conditional on the log still ending
// there. Callers that have not read the log pass unconditional.
func (s *Store) appendTx(taskID string, e Event, after int) error {
	// The one choke point every append goes through, which is why the ownership
	// check lives here rather than on each of the four public entry points: a
	// fifth one added later inherits the rule instead of having to remember it.
	if err := s.mayWrite(); err != nil {
		return err
	}

	// BEGIN IMMEDIATE rather than a plain Begin, which is the difference between
	// waiting and failing.
	//
	// A deferred transaction takes a read lock first and asks to upgrade it on the
	// first write. SQLite refuses that upgrade immediately with SQLITE_BUSY when
	// another writer holds the lock — the busy handler is deliberately skipped,
	// because two readers both waiting to upgrade would deadlock. So busy_timeout
	// does nothing for this path, which is why the pragma alone left most
	// concurrent appends failing.
	//
	// IMMEDIATE takes the write lock up front, where the busy handler does apply.
	tx, err := s.beginImmediate()
	if err != nil {
		return fmt.Errorf("starting a transaction: %w", err)
	}
	// Rolling back a committed transaction is a no-op returning an error, so the
	// result is deliberately dropped: this defer only matters on the paths that
	// return early.
	defer func() { _ = tx.Rollback() }()

	var last int
	// COALESCE because MAX over no rows is NULL: a task's first event is seq 1.
	row := tx.QueryRow(`SELECT COALESCE(MAX(seq), 0) FROM events WHERE task_id = ?`, taskID)
	if err := row.Scan(&last); err != nil {
		return fmt.Errorf("finding the next sequence for %s: %w", taskID, err)
	}

	// The write lock is held from BEGIN IMMEDIATE, so what this read sees is what
	// the insert will land on. Comparing here is the whole optimistic check: a
	// caller decided from the log as it stood at `after`, and if it no longer ends
	// there, something else decided in between.
	if after >= 0 && last != after {
		return fmt.Errorf("%w: %s was at %d when the decision was made and is now at %d",
			ErrConcurrentWrite, taskID, after, last)
	}
	next := last + 1

	// The clock is read here and nowhere the reducer can reach. Writing the time
	// is an observation about the log; reading it back into a transition would
	// make a replay depend on when it ran.
	if _, err := tx.Exec(
		`INSERT INTO events (task_id, seq, action, payload, at) VALUES (?, ?, ?, ?, ?)`,
		taskID, next, e.Action, e.Payload, s.now().Unix(),
	); err != nil {
		return fmt.Errorf("appending to %s: %w", taskID, err)
	}

	if err := tx.Commit(); err != nil {
		return fmt.Errorf("committing: %w", err)
	}
	return nil
}

// Events returns a task's log in order. A task nobody has written to has no
// events, which is a normal answer rather than an error.
func (s *Store) Events(taskID string) ([]Event, error) {
	rows, err := s.db.Query(
		`SELECT seq, action, payload FROM events WHERE task_id = ? ORDER BY seq`, taskID,
	)
	if err != nil {
		return nil, fmt.Errorf("reading the log of %s: %w", taskID, err)
	}
	defer func() { _ = rows.Close() }()

	var events []Event
	for rows.Next() {
		var e Event
		if err := rows.Scan(&e.Seq, &e.Action, &e.Payload); err != nil {
			return nil, fmt.Errorf("reading an event of %s: %w", taskID, err)
		}
		events = append(events, e)
	}
	return events, rows.Err()
}

// ReplayOwnFlow replays a task against the flow it was opened under.
//
// This is what every reader wants, and `Replay` is the narrower door beneath it:
// passing a flow in is right for `luna flow check`, which is asking what a
// *different* flow would do to this log, and wrong for everything else. While a
// build ran one flow the two were the same call; with several, handing in the
// default silently reads a task against a contract it never ran.
//
// A flow this build no longer carries comes back as ErrFlowChanged, which is what
// it is — the task's contract went away underneath it — so every caller that
// already tolerates that error tolerates this too.
func (s *Store) ReplayOwnFlow(taskID string) (fsm.TaskState, error) {
	name, err := s.FlowNameOf(taskID)
	if err != nil {
		return fsm.TaskState{}, err
	}
	flow, err := fsm.FlowNamed(name)
	if err != nil {
		return fsm.TaskState{}, fmt.Errorf("%w: %s ran under %w", ErrFlowChanged, taskID, err)
	}
	return s.Replay(taskID, flow)
}

// FlowNameOf reads which flow a task was opened under, without replaying it.
//
// The chicken-and-egg a build with several flows creates: replaying needs a flow,
// and which flow this task ran is in the log. So the opening event is decoded on
// its own, ahead of the replay, and only for its name.
//
// A name it cannot find is DefaultFlowName rather than an error, and that is safe
// for a reason worth stating: the name only decides which flow gets *loaded*, and
// the fingerprint comparison in Replay still decides whether that was the right
// one. A wrong name cannot produce a wrong replay — only a refused one.
func (s *Store) FlowNameOf(taskID string) (string, error) {
	row := s.db.QueryRow(
		`SELECT seq, action, payload FROM events WHERE task_id = ? ORDER BY seq LIMIT 1`, taskID,
	)

	var e Event
	if err := row.Scan(&e.Seq, &e.Action, &e.Payload); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return fsm.DefaultFlowName, nil
		}
		return "", fmt.Errorf("reading the opening event of %s: %w", taskID, err)
	}

	// Nil flow: only Advance, Complete and ReviewFinding need one injected, and
	// none of them can be the opening event.
	action, err := decodeAction(e, nil)
	if err != nil {
		return "", fmt.Errorf("reading the opening event of %s: %w", taskID, err)
	}
	created, opening := action.(fsm.TaskCreated)
	if !opening || created.FlowName == "" {
		return fsm.DefaultFlowName, nil
	}
	return created.FlowName, nil
}

// Tasks returns every task the store has heard of, sorted.
func (s *Store) Tasks() ([]string, error) {
	rows, err := s.db.Query(`SELECT DISTINCT task_id FROM events ORDER BY task_id`)
	if err != nil {
		return nil, fmt.Errorf("listing tasks: %w", err)
	}
	defer func() { _ = rows.Close() }()

	var ids []string
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			return nil, fmt.Errorf("reading a task id: %w", err)
		}
		ids = append(ids, id)
	}
	return ids, rows.Err()
}

// Replay rebuilds a task's state by feeding its log through the reducer.
//
// This is the whole point of an append-only store: the state is not kept, it is
// derived. It works because the reducer is pure — a transition that
// could run a test suite would try to run it again on every replay.
//
// The kind and the profile are not parameters: they arrive in the log's opening
// TaskCreated event. Asking a caller for what the history already holds would let
// the two disagree.
func (s *Store) Replay(taskID string, flow []fsm.Stage) (fsm.TaskState, error) {
	events, err := s.Events(taskID)
	if err != nil {
		return fsm.TaskState{}, err
	}

	state := fsm.NewTaskState(taskID, "")
	for _, e := range events {
		action, err := decodeAction(e, flow)
		if err != nil {
			return fsm.TaskState{}, fmt.Errorf("replaying %s at seq %d: %w", taskID, e.Seq, err)
		}
		state, err = fsm.Reduce(state, action)
		if err != nil {
			return fsm.TaskState{}, fmt.Errorf("replaying %s at seq %d: %w", taskID, e.Seq, err)
		}

		// Checked right after the opening event rather than up front, because the
		// fingerprint arrives in the log rather than alongside it — the same reason
		// kind and profile are not parameters.
		//
		// Reading further would rebuild the task against a contract it never ran
		// under, and the failure is silent: a renamed stage simply becomes the new
		// name, and a stage inserted mid-flow makes the task re-run work it had
		// already finished.
		//
		// Only a `TaskCreated` carries a fingerprint, so only it is compared. A log
		// that opens with anything else records no flow to disagree with — it is a
		// malformed log rather than a mismatched one, and reporting it as a changed
		// flow would name the wrong problem.
		if _, opening := action.(fsm.TaskCreated); opening && !state.Flow.Matches(flow) {
			return fsm.TaskState{}, fmt.Errorf(
				"%w: %s ran under flow %s and this build's flow is %s — "+
					"the log cannot be read against a flow it was not written under",
				ErrFlowChanged, taskID, state.Flow, fsm.Fingerprint(flow),
			)
		}
	}
	return state, nil
}

// AwaitingGate lists the tasks suspended at a gate.
//
// A suspended task released its slot, so no live process is left to remind anyone
// it exists. Without a query like this it would wait forever — the second form of
// silent failure.
func (s *Store) AwaitingGate() ([]Waiting, error) {
	ids, err := s.Tasks()
	if err != nil {
		return nil, err
	}

	var waiting []Waiting
	for _, id := range ids {
		state, err := s.ReplayOwnFlow(id)
		// A task written under a different flow is skipped rather than fatal. It
		// cannot be read, but the listing exists so nothing waits forever unseen,
		// and returning an error here would let one unreadable task
		// hide every other task waiting on a person. `luna flow check` is where
		// those surface, by name.
		if errors.Is(err, ErrFlowChanged) {
			continue
		}
		if err != nil {
			return nil, err
		}
		if state.Status != fsm.StatusAwaitingGate {
			continue
		}

		w := Waiting{TaskID: id, Stage: state.Stage, Profile: state.Profile}
		if state.Gate != nil {
			w.Reason = state.Gate.Reason
		}
		waiting = append(waiting, w)
	}
	return waiting, nil
}

// What is not here: a content store.
//
// One sat beside the log so a handoff could carry a snapshot of what the
// previous stage produced, and the commit replaced it — git already stores
// content far better than a table of blobs does. The API went first, then the
// empty table and the column it wrote to.
//
// The note survives the code because the alternative is a decision, not an
// omission: a reader who finds the decision recorded and no blobs should learn
// that the snapshot moved to git rather than that somebody forgot to build it.
