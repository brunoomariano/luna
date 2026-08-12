// Package store persists a task's history and replays it back into state.
//
// The current state is not stored — it is derived by replaying the event log
// through the reducer (ADR-0010, INV-core-2). That is what makes killing the
// process and starting it again rebuild the exact state: it was never only in
// memory. It also means the log can never be rewritten, so this package offers
// no way to update or delete an event.
package store

import (
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"errors"
	"fmt"
	"os"
	"path/filepath"

	_ "modernc.org/sqlite" // pure-Go driver, no CGO — see ADR-0025

	"github.com/brunoomariano/luna/src/internal/fsm"
)

// ErrNoSuchBlob is returned when a hash has no content behind it. A handoff
// pointing at a blob the store does not hold is a broken chain, so this is an
// error rather than empty bytes — returning nothing would let the next stage
// start on emptiness.
var ErrNoSuchBlob = errors.New("no blob for that hash")

// ErrUnknownAction is returned when the log holds an action this build cannot
// read — a log written by a newer version, most likely. Replay stops rather than
// guessing, because guessing would rebuild the task into a state it was never in.
var ErrUnknownAction = errors.New("unknown action in the log")

// ErrFlowChanged is returned when a log was written under a different flow than
// the one it is being replayed against (ADR-0046).
//
// The same refusal as ErrUnknownAction and for the same reason, except that this
// one used to be silent: a renamed stage replayed as the new name with no error
// at all, and a stage inserted mid-flow made the task re-run work it had already
// completed. There is no recovery inside an append-only log, so a task that meets
// this is ended with `luna task abandon`.
var ErrFlowChanged = errors.New("the flow changed under an open task")

// ErrConcurrentWrite is returned when an append was conditional on the log ending
// somewhere and it does not (ADR-0047).
//
// It means two writers decided from the same state. Refusing the second is the
// point: letting it land would put two decisions taken from one state into the
// log, and replaying that yields ErrIllegalTransition forever with no way to
// repair it, since the store has no UPDATE and no DELETE (INV-core-2).
var ErrConcurrentWrite = errors.New("the task moved since it was read")

// firstSeq is the sequence of a task's opening event. Sequences start at 1, and
// TaskCreated is always first — which is what makes the log self-describing.
const firstSeq = 1

// unconditional is the `after` for an append by a caller that did not read the
// log first — creating a task, or storing a handoff blob. Negative rather than
// zero, because zero is a real position: the state of a task whose log is empty.
const unconditional = -1

// Event is one recorded transition. Seq orders it within its task; Blob points at
// the snapshot taken at that moment, when there is one.
type Event struct {
	Seq     int
	Action  string
	Payload string
	Blob    string
}

// Waiting is a task suspended at a gate, as `luna gates` would list it
// (INV-core-12).
type Waiting struct {
	TaskID string
	Stage  fsm.StageID
	Reason string

	// Profile is carried so the listing can flag one this build does not know,
	// which happens when a log was written by a newer version.
	Profile fsm.Profile
}

// Store is the append-only log plus the content store, in one SQLite file.
// Keeping them together is what makes writing an event and its snapshot atomic
// (ADR-0025).
type Store struct{ db *sql.DB }

const schema = `
CREATE TABLE IF NOT EXISTS events (
    task_id  TEXT    NOT NULL,
    seq      INTEGER NOT NULL,
    action   TEXT    NOT NULL,
    payload  TEXT    NOT NULL DEFAULT '',
    blob     TEXT    NOT NULL DEFAULT '',
    PRIMARY KEY (task_id, seq)
);

CREATE TABLE IF NOT EXISTS blobs (
    sha256   TEXT PRIMARY KEY,
    content  BLOB NOT NULL
);
`

// Open returns the store at path, creating the file and schema if they are not
// there yet. A path that does not exist is someone's first run, not an error.
func Open(path string) (*Store, error) {
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

	return &Store{db: db}, nil
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
// trail, and a store that could rewrite it would not be one (INV-core-2).
func (s *Store) Append(taskID string, e Event) error {
	_, err := s.appendTx(taskID, e, nil, unconditional)
	return err
}

// AppendWithBlob records an event and the snapshot it points at, in a single
// transaction. Either both land or neither does — a process dying between the two
// would otherwise leave an orphan blob or a dangling reference (ADR-0025).
func (s *Store) AppendWithBlob(taskID string, e Event, content []byte) (string, error) {
	return s.appendTx(taskID, e, content, unconditional)
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
// read it (ADR-0047).
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
	_, err = s.appendTx(taskID, Event{Action: name, Payload: payload}, nil, after)
	return err
}

// appendTx writes one event, and optionally the blob it points at, atomically.
//
// A non-negative `after` makes the append conditional on the log still ending
// there. Callers that have not read the log pass unconditional.
func (s *Store) appendTx(taskID string, e Event, content []byte, after int) (string, error) {
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
		return "", fmt.Errorf("starting a transaction: %w", err)
	}
	// Rolling back a committed transaction is a no-op returning an error, so the
	// result is deliberately dropped: this defer only matters on the paths that
	// return early.
	defer func() { _ = tx.Rollback() }()

	hash := e.Blob
	if content != nil {
		hash = hashOf(content)
		// OR IGNORE rather than INSERT: identical content shares one row, so a
		// task looping through build does not store the same file a dozen times.
		if _, err := tx.Exec(`INSERT OR IGNORE INTO blobs (sha256, content) VALUES (?, ?)`, hash, content); err != nil {
			return "", fmt.Errorf("storing the blob: %w", err)
		}
	}

	var last int
	// COALESCE because MAX over no rows is NULL: a task's first event is seq 1.
	row := tx.QueryRow(`SELECT COALESCE(MAX(seq), 0) FROM events WHERE task_id = ?`, taskID)
	if err := row.Scan(&last); err != nil {
		return "", fmt.Errorf("finding the next sequence for %s: %w", taskID, err)
	}

	// The write lock is held from BEGIN IMMEDIATE, so what this read sees is what
	// the insert will land on. Comparing here is the whole optimistic check: a
	// caller decided from the log as it stood at `after`, and if it no longer ends
	// there, something else decided in between.
	if after >= 0 && last != after {
		return "", fmt.Errorf("%w: %s was at %d when the decision was made and is now at %d",
			ErrConcurrentWrite, taskID, after, last)
	}
	next := last + 1

	if _, err := tx.Exec(
		`INSERT INTO events (task_id, seq, action, payload, blob) VALUES (?, ?, ?, ?, ?)`,
		taskID, next, e.Action, e.Payload, hash,
	); err != nil {
		return "", fmt.Errorf("appending to %s: %w", taskID, err)
	}

	if err := tx.Commit(); err != nil {
		return "", fmt.Errorf("committing: %w", err)
	}
	return hash, nil
}

// Events returns a task's log in order. A task nobody has written to has no
// events, which is a normal answer rather than an error.
func (s *Store) Events(taskID string) ([]Event, error) {
	rows, err := s.db.Query(
		`SELECT seq, action, payload, blob FROM events WHERE task_id = ? ORDER BY seq`, taskID,
	)
	if err != nil {
		return nil, fmt.Errorf("reading the log of %s: %w", taskID, err)
	}
	defer func() { _ = rows.Close() }()

	var events []Event
	for rows.Next() {
		var e Event
		if err := rows.Scan(&e.Seq, &e.Action, &e.Payload, &e.Blob); err != nil {
			return nil, fmt.Errorf("reading an event of %s: %w", taskID, err)
		}
		events = append(events, e)
	}
	return events, rows.Err()
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
// derived. It works because the reducer is pure (ADR-0024) — a transition that
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
		// already finished (ADR-0046).
		if e.Seq == firstSeq && !state.Flow.Matches(flow) {
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
// silent failure (INV-core-12).
func (s *Store) AwaitingGate(flow []fsm.Stage) ([]Waiting, error) {
	ids, err := s.Tasks()
	if err != nil {
		return nil, err
	}

	var waiting []Waiting
	for _, id := range ids {
		state, err := s.Replay(id, flow)
		// A task written under a different flow is skipped rather than fatal. It
		// cannot be read, but the listing exists so nothing waits forever unseen
		// (INV-core-12), and returning an error here would let one unreadable task
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

// PutBlob stores content and returns its hash. Storing the same content twice
// returns the same hash and keeps one row.
func (s *Store) PutBlob(content []byte) (string, error) {
	hash := hashOf(content)
	if _, err := s.db.Exec(`INSERT OR IGNORE INTO blobs (sha256, content) VALUES (?, ?)`, hash, content); err != nil {
		return "", fmt.Errorf("storing a blob: %w", err)
	}
	return hash, nil
}

// Blob returns the content behind a hash.
func (s *Store) Blob(hash string) ([]byte, error) {
	var content []byte
	err := s.db.QueryRow(`SELECT content FROM blobs WHERE sha256 = ?`, hash).Scan(&content)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, fmt.Errorf("%w: %s", ErrNoSuchBlob, hash)
	}
	if err != nil {
		return nil, fmt.Errorf("reading blob %s: %w", hash, err)
	}
	return content, nil
}

// BlobCount reports how many distinct blobs are stored.
func (s *Store) BlobCount() (int, error) {
	var n int
	if err := s.db.QueryRow(`SELECT COUNT(*) FROM blobs`).Scan(&n); err != nil {
		return 0, fmt.Errorf("counting blobs: %w", err)
	}
	return n, nil
}

func hashOf(content []byte) string {
	sum := sha256.Sum256(content)
	return hex.EncodeToString(sum[:])
}
