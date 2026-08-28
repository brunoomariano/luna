package store

import (
	"database/sql"
	"errors"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/brunoomariano/luna/src/internal/fsm"
)

func TestImportLegacyPreservesEventsAndArtifactsExactly(t *testing.T) {
	dir := t.TempDir()
	legacyPath := filepath.Join(dir, "legacy.db")
	legacy, err := OpenAs(legacyPath, LunaOwnsTheLog)
	if err != nil {
		t.Fatal(err)
	}
	legacy.Now = func() time.Time { return time.Unix(1234, 0) }
	if err := legacy.AppendAction("OLD-1", fsm.TaskCreated{
		Kind: fsm.KindChore, Flow: fsm.Fingerprint(fsm.DefaultFlow()),
	}); err != nil {
		t.Fatal(err)
	}
	if err := legacy.PutBlob(Blob{
		TaskID: "OLD-1", Stage: "build", Artifact: "contract", Seq: 1, Body: []byte("terms"),
	}); err != nil {
		t.Fatal(err)
	}
	if err := legacy.Close(); err != nil {
		t.Fatal(err)
	}

	central, err := OpenAs(filepath.Join(dir, "central.db"), LunaOwnsTheLog)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = central.Close() })
	if err := central.ImportLegacy("one-project", legacyPath); err != nil {
		t.Fatalf("importing: %v", err)
	}
	if err := central.ImportLegacy("one-project", legacyPath); err != nil {
		t.Fatalf("repeating an exact import: %v", err)
	}

	project := central.ForProject("one-project")
	events, err := project.Events("OLD-1")
	if err != nil || len(events) != 1 || events[0].Action != "TaskCreated" {
		t.Fatalf("imported events = %+v, %v", events, err)
	}
	at, err := project.LastEventAt("OLD-1")
	if err != nil || at.Unix() != 1234 {
		t.Fatalf("imported timestamp = %v, %v", at, err)
	}
	blob, err := project.LatestBlob("OLD-1", "build", "contract")
	if err != nil || string(blob.Body) != "terms" {
		t.Fatalf("imported artifact = %+v, %v", blob, err)
	}
}

func TestImportLegacyRefusesConflictingHistory(t *testing.T) {
	dir := t.TempDir()
	legacyPath := filepath.Join(dir, "legacy.db")
	legacy, err := OpenAs(legacyPath, LunaOwnsTheLog)
	if err != nil {
		t.Fatal(err)
	}
	if err := legacy.AppendAction("OLD-1", fsm.TaskCreated{
		Kind: fsm.KindChore, Flow: fsm.Fingerprint(fsm.DefaultFlow()),
	}); err != nil {
		t.Fatal(err)
	}
	if err := legacy.Close(); err != nil {
		t.Fatal(err)
	}

	central, err := OpenAs(filepath.Join(dir, "central.db"), LunaOwnsTheLog)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = central.Close() })
	if err := central.ForProject("one-project").Append("OLD-1", Event{Action: "different"}); err != nil {
		t.Fatal(err)
	}
	if err := central.ImportLegacy("one-project", legacyPath); err == nil || !strings.Contains(err.Error(), "history conflict") {
		t.Fatalf("conflicting import error = %v", err)
	}
}

func TestImportLegacyRefusesConflictingArtifacts(t *testing.T) {
	dir := t.TempDir()
	legacyPath := filepath.Join(dir, "legacy.db")
	legacy, err := OpenAs(legacyPath, LunaOwnsTheLog)
	if err != nil {
		t.Fatal(err)
	}
	legacyBlob := Blob{TaskID: "OLD-1", Stage: "build", Artifact: "contract", Seq: 1, Body: []byte("old")}
	if err := legacy.PutBlob(legacyBlob); err != nil {
		t.Fatal(err)
	}
	if err := legacy.Close(); err != nil {
		t.Fatal(err)
	}

	central, err := OpenAs(filepath.Join(dir, "central.db"), LunaOwnsTheLog)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = central.Close() })
	legacyBlob.Body = []byte("different")
	if err := central.ForProject("one-project").PutBlob(legacyBlob); err != nil {
		t.Fatal(err)
	}
	if err := central.ImportLegacy("one-project", legacyPath); err == nil || !strings.Contains(err.Error(), "artifact conflict") {
		t.Fatalf("conflicting artifact error = %v", err)
	}
}

func TestImportLegacyRefusesUnplaceableBlobs(t *testing.T) {
	dir := t.TempDir()
	legacyPath := filepath.Join(dir, "legacy.db")
	db, err := sql.Open("sqlite", legacyPath)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`CREATE TABLE events (
		task_id TEXT NOT NULL, seq INTEGER NOT NULL, action TEXT NOT NULL,
		payload TEXT NOT NULL DEFAULT '', at INTEGER NOT NULL DEFAULT 0,
		PRIMARY KEY (task_id, seq))`); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`CREATE TABLE blobs (sha256 TEXT PRIMARY KEY, content BLOB NOT NULL)`); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`INSERT INTO blobs (sha256, content) VALUES ('abc', 'body')`); err != nil {
		t.Fatal(err)
	}
	if err := db.Close(); err != nil {
		t.Fatal(err)
	}

	central, err := OpenAs(filepath.Join(dir, "central.db"), LunaOwnsTheLog)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = central.Close() })
	if err := central.ImportLegacy("one-project", legacyPath); !errors.Is(err, ErrSchemaTooOld) {
		t.Fatalf("unplaceable artifact error = %v", err)
	}
}

func TestImportLegacyAcceptsAnEventOnlyDatabase(t *testing.T) {
	dir := t.TempDir()
	legacyPath := filepath.Join(dir, "legacy.db")
	db, err := sql.Open("sqlite", legacyPath)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`CREATE TABLE events (
		task_id TEXT NOT NULL, seq INTEGER NOT NULL, action TEXT NOT NULL,
		payload TEXT NOT NULL DEFAULT '', at INTEGER NOT NULL DEFAULT 0,
		PRIMARY KEY (task_id, seq))`); err != nil {
		t.Fatal(err)
	}
	if err := db.Close(); err != nil {
		t.Fatal(err)
	}

	central, err := OpenAs(filepath.Join(dir, "central.db"), LunaOwnsTheLog)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = central.Close() })
	if err := central.ImportLegacy("one-project", legacyPath); err != nil {
		t.Fatalf("event-only import: %v", err)
	}
}

func TestImportLegacyRequiresTheOwnerAndAnExistingSource(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "central.db")
	owner, err := OpenAs(path, LunaOwnsTheLog)
	if err != nil {
		t.Fatal(err)
	}
	if err := owner.ImportLegacy("one-project", filepath.Join(dir, "missing.db")); err == nil {
		t.Fatal("a missing legacy database was accepted")
	}
	if err := owner.Close(); err != nil {
		t.Fatal(err)
	}

	reader, err := OpenReadOnly(path)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = reader.Close() })
	if err := reader.ImportLegacy("one-project", path); !errors.Is(err, ErrNotTheOwner) {
		t.Fatalf("read-only import error = %v", err)
	}
}

func TestImportLegacyReportsMalformedEventRows(t *testing.T) {
	dir := t.TempDir()
	legacyPath := filepath.Join(dir, "malformed-events.db")
	db, err := sql.Open("sqlite", legacyPath)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`
		CREATE TABLE events (
			task_id TEXT, seq INTEGER, action TEXT, payload TEXT, at TEXT,
			PRIMARY KEY (task_id, seq));
		INSERT INTO events VALUES ('OLD-1', 1, 'TaskCreated', '{}', 'not-a-time');
	`); err != nil {
		t.Fatal(err)
	}
	if err := db.Close(); err != nil {
		t.Fatal(err)
	}

	central, err := OpenAs(filepath.Join(dir, "central.db"), LunaOwnsTheLog)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = central.Close() })
	if err := central.ImportLegacy("one-project", legacyPath); err == nil {
		t.Fatal("a malformed legacy event was imported")
	}
}

func TestImportLegacyReportsMissingEvents(t *testing.T) {
	dir := t.TempDir()
	legacyPath := filepath.Join(dir, "no-events.db")
	db, err := sql.Open("sqlite", legacyPath)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`CREATE TABLE unrelated (value TEXT)`); err != nil {
		t.Fatal(err)
	}
	if err := db.Close(); err != nil {
		t.Fatal(err)
	}

	central, err := OpenAs(filepath.Join(dir, "central.db"), LunaOwnsTheLog)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = central.Close() })
	if err := central.ImportLegacy("one-project", legacyPath); err == nil {
		t.Fatal("a legacy database without events was imported")
	}
}

func TestImportLegacyAcceptsAnEmptyPreVersionBlobTable(t *testing.T) {
	dir := t.TempDir()
	legacyPath := filepath.Join(dir, "old-empty-blobs.db")
	db, err := sql.Open("sqlite", legacyPath)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`
		CREATE TABLE events (
			task_id TEXT, seq INTEGER, action TEXT, payload TEXT, at INTEGER,
			PRIMARY KEY (task_id, seq));
		CREATE TABLE blobs (sha256 TEXT PRIMARY KEY, content BLOB NOT NULL);
	`); err != nil {
		t.Fatal(err)
	}
	if err := db.Close(); err != nil {
		t.Fatal(err)
	}

	central, err := OpenAs(filepath.Join(dir, "central.db"), LunaOwnsTheLog)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = central.Close() })
	if err := central.ImportLegacy("one-project", legacyPath); err != nil {
		t.Fatalf("empty pre-version artifact table: %v", err)
	}
}

func TestImportLegacyReportsAClosedCentralStore(t *testing.T) {
	dir := t.TempDir()
	legacyPath := filepath.Join(dir, "legacy.db")
	legacy, err := OpenAs(legacyPath, LunaOwnsTheLog)
	if err != nil {
		t.Fatal(err)
	}
	if err := legacy.Close(); err != nil {
		t.Fatal(err)
	}
	central, err := OpenAs(filepath.Join(dir, "central.db"), LunaOwnsTheLog)
	if err != nil {
		t.Fatal(err)
	}
	if err := central.Close(); err != nil {
		t.Fatal(err)
	}
	if err := central.ImportLegacy("one-project", legacyPath); err == nil {
		t.Fatal("a closed central store imported legacy history")
	}
}
