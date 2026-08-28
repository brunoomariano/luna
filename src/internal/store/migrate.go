package store

import (
	"database/sql"
	"errors"
	"fmt"
)

// schemaVersion is the shape this build writes, recorded in the file itself.
//
// It exists because `CREATE TABLE IF NOT EXISTS` does nothing once a table is
// there: a database written by an older build keeps its own columns, silently,
// and the mismatch surfaces at the first insert — mid-stage, as
// `table blobs has no column named task_id`, which says nothing about why.
//
// Version 1 is the first that records anything. Version 2 moves every project's
// history into one database and adds project to both primary keys. A file at 0
// is either brand new or was written before this existed, and telling those
// apart is what migrateToOne does.
const schemaVersion = 2

// ErrSchemaTooOld is a database this build cannot read without losing something.
var ErrSchemaTooOld = errors.New("the store was written by an older build")

// migrate brings a database up to schemaVersion, or refuses to open it.
//
// Run before the schema itself, because the migration's whole job is to make
// `CREATE TABLE IF NOT EXISTS` mean what it says: a table left in an older shape
// would satisfy the `IF NOT EXISTS` and never be corrected.
func migrate(db *sql.DB) error {
	var version int
	if err := db.QueryRow(`PRAGMA user_version`).Scan(&version); err != nil {
		return fmt.Errorf("reading the store's schema version: %w", err)
	}
	if version > schemaVersion {
		return fmt.Errorf("the store schema is %d, newer than this build's %d", version, schemaVersion)
	}
	if version == schemaVersion {
		return nil
	}

	if version < 1 {
		if err := migrateToOne(db); err != nil {
			return err
		}
	}
	if version < 2 {
		if err := migrateToTwo(db); err != nil {
			return err
		}
	}

	// Not a placeholder: PRAGMA takes no bound parameters, and the value is a
	// constant in this package rather than anything a caller supplies.
	if _, err := db.Exec(fmt.Sprintf(`PRAGMA user_version = %d`, schemaVersion)); err != nil {
		return fmt.Errorf("recording the store's schema version: %w", err)
	}
	return nil
}

// requireCurrentSchema is the read-only side of migration. Readers report an
// old database instead of changing it; only the daemon opens through migrate.
func requireCurrentSchema(db *sql.DB) error {
	var version int
	if err := db.QueryRow(`PRAGMA user_version`).Scan(&version); err != nil {
		return fmt.Errorf("reading the store's schema version: %w", err)
	}
	if version != schemaVersion {
		return fmt.Errorf("%w: schema version is %d, this build needs %d; start the daemon to migrate it",
			ErrSchemaTooOld, version, schemaVersion)
	}
	return nil
}

// migrateToTwo adds project to the identity of every event and blob. Rebuilding
// the tables is necessary because adding a column alone cannot change a primary
// key, and equal task ids from two projects have to coexist.
func migrateToTwo(db *sql.DB) error {
	for _, table := range []string{"events", "blobs"} {
		exists, err := tableExists(db, table)
		if err != nil {
			return err
		}
		if !exists {
			continue
		}
		hasProject, err := tableHasColumn(db, table, "project")
		if err != nil {
			return err
		}
		if hasProject {
			continue
		}
		if err := refuseUnscopedRows(db, table); err != nil {
			return err
		}
		if err := rebuildWithProject(db, table); err != nil {
			return err
		}
	}
	return nil
}

func tableExists(db *sql.DB, table string) (bool, error) {
	var count int
	if err := db.QueryRow(
		`SELECT COUNT(*) FROM sqlite_master WHERE type = 'table' AND name = ?`, table,
	).Scan(&count); err != nil {
		return false, fmt.Errorf("checking for table %s: %w", table, err)
	}
	return count == 1, nil
}

func tableHasColumn(db *sql.DB, table, column string) (bool, error) {
	rows, err := db.Query(fmt.Sprintf(`SELECT name FROM pragma_table_info('%s')`, table))
	if err != nil {
		return false, fmt.Errorf("reading %s columns: %w", table, err)
	}
	defer func() { _ = rows.Close() }()
	for rows.Next() {
		var name string
		if err := rows.Scan(&name); err != nil {
			return false, fmt.Errorf("reading a %s column: %w", table, err)
		}
		if name == column {
			return true, nil
		}
	}
	return false, rows.Err()
}

func rebuildWithProject(db *sql.DB, table string) error {
	statements := map[string]string{
		"events": `
ALTER TABLE events RENAME TO events_v1;
CREATE TABLE events (
    project TEXT NOT NULL DEFAULT '', task_id TEXT NOT NULL, seq INTEGER NOT NULL,
    action TEXT NOT NULL, payload TEXT NOT NULL DEFAULT '', at INTEGER NOT NULL DEFAULT 0,
    PRIMARY KEY (project, task_id, seq)
);
INSERT INTO events (project, task_id, seq, action, payload, at)
SELECT '', task_id, seq, action, payload, at FROM events_v1;
DROP TABLE events_v1;`,
		"blobs": `
ALTER TABLE blobs RENAME TO blobs_v1;
CREATE TABLE blobs (
    project TEXT NOT NULL DEFAULT '', task_id TEXT NOT NULL, stage TEXT NOT NULL,
    artifact TEXT NOT NULL, seq INTEGER NOT NULL, hash TEXT NOT NULL,
    body BLOB NOT NULL, at INTEGER NOT NULL DEFAULT 0,
    PRIMARY KEY (project, task_id, stage, artifact, seq)
);
INSERT INTO blobs (project, task_id, stage, artifact, seq, hash, body, at)
SELECT '', task_id, stage, artifact, seq, hash, body, at FROM blobs_v1;
DROP TABLE blobs_v1;`,
	}
	statement, ok := statements[table]
	if !ok {
		return fmt.Errorf("no migration for table %q", table)
	}
	tx, err := db.Begin()
	if err != nil {
		return fmt.Errorf("starting the %s migration: %w", table, err)
	}
	defer func() { _ = tx.Rollback() }()
	if _, err := tx.Exec(statement); err != nil {
		return fmt.Errorf("adding project identity to %s: %w", table, err)
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("committing project identity to %s: %w", table, err)
	}
	return nil
}

func refuseUnscopedRows(db *sql.DB, table string) error {
	var count int
	if err := db.QueryRow(fmt.Sprintf(`SELECT COUNT(*) FROM %s`, table)).Scan(&count); err != nil {
		return fmt.Errorf("counting unscoped %s rows: %w", table, err)
	}
	if count == 0 {
		return nil
	}
	return fmt.Errorf("%w: %s contains %d rows without a project key; import this former "+
		"per-project store through the daemon instead of opening it as the central database",
		ErrSchemaTooOld, table, count)
}

// migrateToOne handles the one shape that predates versioning.
//
// Before version 1, `blobs` was content-addressed — `(sha256, content)` — with no
// task, stage or artifact on the row. The current table is keyed by all three,
// and there is no way to derive them from a hash: the information is not in the
// table and the mapping cannot be invented.
//
// So the rule is refuse when it costs something, carry on when it does not. An
// old table holding rows means artifacts this build cannot place, and the person
// is told rather than having them disappear. An old table holding none is a file
// that was created and never used — every artifact it could lose is zero of
// them — and dropping it lets the current schema be created in its place.
//
// A fresh database has no `blobs` table at all and reaches neither branch.
func migrateToOne(db *sql.DB) error {
	old, err := isPreVersionBlobs(db)
	if err != nil || !old {
		return err
	}

	var rows int
	if err := db.QueryRow(`SELECT COUNT(*) FROM blobs`).Scan(&rows); err != nil {
		return fmt.Errorf("counting what the old blob table holds: %w", err)
	}
	if rows > 0 {
		return fmt.Errorf(
			"%w: its %d stored artifacts are keyed by hash alone, and this build keys them by "+
				"task, stage and artifact — which cannot be recovered from a hash. The event log "+
				"in the same file is still readable with sqlite3. Move the file aside and let "+
				"Luna create a new one",
			ErrSchemaTooOld, rows)
	}

	if _, err := db.Exec(`DROP TABLE blobs`); err != nil {
		return fmt.Errorf("replacing the empty blob table: %w", err)
	}
	return nil
}

// isPreVersionBlobs reports whether `blobs` exists in the shape that predates
// versioning.
//
// Asked of the table's own columns rather than of the version number, because a
// file at version 0 is equally a brand new one — and a new database must not be
// treated as an old one.
func isPreVersionBlobs(db *sql.DB) (bool, error) {
	rows, err := db.Query(`SELECT name FROM pragma_table_info('blobs')`)
	if err != nil {
		return false, fmt.Errorf("reading the blob table's columns: %w", err)
	}
	defer func() { _ = rows.Close() }()

	found := false
	for rows.Next() {
		var name string
		if err := rows.Scan(&name); err != nil {
			return false, fmt.Errorf("reading the blob table's columns: %w", err)
		}
		found = true
		if name == "task_id" {
			// Already the current shape, whatever the version says.
			return false, rows.Err()
		}
	}
	return found, rows.Err()
}
