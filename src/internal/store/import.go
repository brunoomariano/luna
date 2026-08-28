package store

import (
	"bytes"
	"database/sql"
	"fmt"
)

// ImportLegacy copies one former per-project database into the central log.
// It preserves sequence numbers and timestamps, and is idempotent only when a
// row already present is byte-for-byte the same history.
func (s *Store) ImportLegacy(project, path string) error {
	if s.As != LunaOwnsTheLog {
		return ErrNotTheOwner
	}
	legacy, err := sql.Open("sqlite", "file:"+path+"?mode=ro")
	if err != nil {
		return fmt.Errorf("opening legacy store %s: %w", path, err)
	}
	defer func() { _ = legacy.Close() }()
	if err := legacy.Ping(); err != nil {
		return fmt.Errorf("opening legacy store %s: %w", path, err)
	}

	tx, err := s.beginImmediate()
	if err != nil {
		return fmt.Errorf("starting legacy import: %w", err)
	}
	defer func() { _ = tx.Rollback() }()
	if err := importEvents(tx, legacy, project); err != nil {
		return fmt.Errorf("importing %s: %w", path, err)
	}
	if err := importBlobs(tx, legacy, project); err != nil {
		return fmt.Errorf("importing %s: %w", path, err)
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("committing legacy import: %w", err)
	}
	return nil
}

func importEvents(tx *sql.Tx, legacy *sql.DB, project string) error {
	rows, err := legacy.Query(`SELECT task_id, seq, action, payload, at FROM events ORDER BY task_id, seq`)
	if err != nil {
		return fmt.Errorf("reading legacy events: %w", err)
	}
	defer func() { _ = rows.Close() }()

	for rows.Next() {
		var taskID, action, payload string
		var seq int
		var at int64
		if err := rows.Scan(&taskID, &seq, &action, &payload, &at); err != nil {
			return fmt.Errorf("reading a legacy event: %w", err)
		}
		if err := importEvent(tx, project, taskID, seq, action, payload, at); err != nil {
			return err
		}
	}
	return rows.Err()
}

func importEvent(tx *sql.Tx, project, taskID string, seq int, action, payload string, at int64) error {
	_, err := tx.Exec(
		`INSERT OR IGNORE INTO events (project, task_id, seq, action, payload, at)
		 VALUES (?, ?, ?, ?, ?, ?)`, project, taskID, seq, action, payload, at,
	)
	if err != nil {
		return fmt.Errorf("copying %s event %d: %w", taskID, seq, err)
	}
	var gotAction, gotPayload string
	var gotAt int64
	err = tx.QueryRow(
		`SELECT action, payload, at FROM events WHERE project = ? AND task_id = ? AND seq = ?`,
		project, taskID, seq,
	).Scan(&gotAction, &gotPayload, &gotAt)
	if err != nil {
		return fmt.Errorf("checking %s event %d: %w", taskID, seq, err)
	}
	if gotAction != action || gotPayload != payload || gotAt != at {
		return fmt.Errorf("history conflict for %s/%s at event %d", project, taskID, seq)
	}
	return nil
}

func importBlobs(tx *sql.Tx, legacy *sql.DB, project string) error {
	exists, err := tableExists(legacy, "blobs")
	if err != nil || !exists {
		return err
	}
	current, err := tableHasColumn(legacy, "blobs", "task_id")
	if err != nil {
		return err
	}
	if !current {
		return refuseUnplaceableBlobs(legacy)
	}

	rows, err := legacy.Query(
		`SELECT task_id, stage, artifact, seq, hash, body, at
		 FROM blobs ORDER BY task_id, seq, stage, artifact`,
	)
	if err != nil {
		return fmt.Errorf("reading legacy artifacts: %w", err)
	}
	defer func() { _ = rows.Close() }()
	for rows.Next() {
		var taskID, stage, artifact, hash string
		var seq int
		var body []byte
		var at int64
		if err := rows.Scan(&taskID, &stage, &artifact, &seq, &hash, &body, &at); err != nil {
			return fmt.Errorf("reading a legacy artifact: %w", err)
		}
		if err := importBlob(tx, project, taskID, stage, artifact, seq, hash, body, at); err != nil {
			return err
		}
	}
	return rows.Err()
}

func importBlob(tx *sql.Tx, project, taskID, stage, artifact string, seq int, hash string, body []byte, at int64) error {
	_, err := tx.Exec(
		`INSERT OR IGNORE INTO blobs (project, task_id, stage, artifact, seq, hash, body, at)
		 VALUES (?, ?, ?, ?, ?, ?, ?, ?)`, project, taskID, stage, artifact, seq, hash, body, at,
	)
	if err != nil {
		return fmt.Errorf("copying %s/%s at %d: %w", taskID, artifact, seq, err)
	}
	var gotHash string
	var gotBody []byte
	var gotAt int64
	err = tx.QueryRow(
		`SELECT hash, body, at FROM blobs
		 WHERE project = ? AND task_id = ? AND stage = ? AND artifact = ? AND seq = ?`,
		project, taskID, stage, artifact, seq,
	).Scan(&gotHash, &gotBody, &gotAt)
	if err != nil {
		return fmt.Errorf("checking %s/%s at %d: %w", taskID, artifact, seq, err)
	}
	if gotHash != hash || !bytes.Equal(gotBody, body) || gotAt != at {
		return fmt.Errorf("artifact conflict for %s/%s/%s at event %d", project, taskID, artifact, seq)
	}
	return nil
}

func refuseUnplaceableBlobs(legacy *sql.DB) error {
	var count int
	if err := legacy.QueryRow(`SELECT COUNT(*) FROM blobs`).Scan(&count); err != nil {
		return fmt.Errorf("counting legacy artifacts: %w", err)
	}
	if count == 0 {
		return nil
	}
	return fmt.Errorf("%w: %d artifacts are keyed by hash alone and cannot be assigned to a task",
		ErrSchemaTooOld, count)
}
