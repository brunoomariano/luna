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

	db, err := sql.Open("sqlite", path)
	if err != nil {
		return nil, fmt.Errorf("opening %s: %w", path, err)
	}
	if _, err := db.Exec(schema); err != nil {
		_ = db.Close()
		return nil, fmt.Errorf("creating the schema in %s: %w", path, err)
	}

	return &Store{db: db}, nil
}

// Close releases the database handle.
func (s *Store) Close() error { return s.db.Close() }

// Append records an event at the end of a task's log.
//
// There is no counterpart that replaces or removes one: the history is the audit
// trail, and a store that could rewrite it would not be one (INV-core-2).
func (s *Store) Append(taskID string, e Event) error {
	_, err := s.appendTx(taskID, e, nil)
	return err
}

// AppendWithBlob records an event and the snapshot it points at, in a single
// transaction. Either both land or neither does — a process dying between the two
// would otherwise leave an orphan blob or a dangling reference (ADR-0025).
func (s *Store) AppendWithBlob(taskID string, e Event, content []byte) (string, error) {
	return s.appendTx(taskID, e, content)
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

func (s *Store) appendTx(taskID string, e Event, content []byte) (string, error) {
	tx, err := s.db.Begin()
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

	var next int
	// COALESCE because MAX over no rows is NULL: a task's first event is seq 1.
	row := tx.QueryRow(`SELECT COALESCE(MAX(seq), 0) + 1 FROM events WHERE task_id = ?`, taskID)
	if err := row.Scan(&next); err != nil {
		return "", fmt.Errorf("finding the next sequence for %s: %w", taskID, err)
	}

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
func (s *Store) Replay(taskID string, kind fsm.TaskKind, flow []fsm.Stage) (fsm.TaskState, error) {
	events, err := s.Events(taskID)
	if err != nil {
		return fsm.TaskState{}, err
	}

	state := fsm.NewTaskState(taskID, kind)
	for _, e := range events {
		action, err := decodeAction(e, flow)
		if err != nil {
			return fsm.TaskState{}, fmt.Errorf("replaying %s at seq %d: %w", taskID, e.Seq, err)
		}
		state, err = fsm.Reduce(state, action)
		if err != nil {
			return fsm.TaskState{}, fmt.Errorf("replaying %s at seq %d: %w", taskID, e.Seq, err)
		}
	}
	return state, nil
}

// AwaitingGate lists the tasks suspended at a gate.
//
// A suspended task released its slot, so no live process is left to remind anyone
// it exists. Without a query like this it would wait forever — the second form of
// silent failure (INV-core-12).
func (s *Store) AwaitingGate(kind fsm.TaskKind, flow []fsm.Stage) ([]Waiting, error) {
	ids, err := s.Tasks()
	if err != nil {
		return nil, err
	}

	var waiting []Waiting
	for _, id := range ids {
		state, err := s.Replay(id, kind, flow)
		if err != nil {
			return nil, err
		}
		if state.Status != fsm.StatusAwaitingGate {
			continue
		}

		w := Waiting{TaskID: id, Stage: state.Stage}
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
