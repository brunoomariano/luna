package daemon

import (
	"database/sql"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/brunoomariano/luna/src/internal/fsm"
	"github.com/brunoomariano/luna/src/internal/store"
)

func TestOnlyOneDaemonCanOwnTheCentralStore(t *testing.T) {
	dir := t.TempDir()
	database := filepath.Join(dir, "luna.db")
	first, err := Listen(Options{Socket: filepath.Join(dir, "first.sock"), Store: database})
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = first.Close() }()

	_, err = Listen(Options{Socket: filepath.Join(dir, "second.sock"), Store: database})
	if err == nil || !strings.Contains(err.Error(), "already has a daemon") {
		t.Fatalf("second daemon error = %v, want central ownership refusal", err)
	}
}

// TestTheDaemonWritesTwoProjectsIntoOneDatabase covers the process boundary and
// the project boundary together. Both clients reach one writer and one file,
// while equal task ids remain independent histories.
func TestTheDaemonWritesTwoProjectsIntoOneDatabase(t *testing.T) {
	dir := t.TempDir()
	database := filepath.Join(dir, "luna.db")
	server, err := Listen(Options{Socket: filepath.Join(dir, SocketName), Store: database})
	if err != nil {
		t.Fatalf("starting the daemon: %v", err)
	}
	t.Cleanup(func() { _ = server.Close() })

	for _, project := range []string{"github.com-one-app-a1", "github.com-two-app-b2"} {
		client := Client{Path: filepath.Join(dir, SocketName)}
		central, err := store.OpenReadOnly(database)
		if err != nil {
			t.Fatalf("opening the central reader: %v", err)
		}
		central.Via = client
		reader := central.ForProject(project)
		if err := reader.AppendAction("TASK-1", fsm.TaskCreated{
			Kind: fsm.KindChore, Flow: fsm.Fingerprint(fsm.DefaultFlow()),
		}); err != nil {
			t.Fatalf("creating TASK-1 in %s: %v", project, err)
		}
		_ = central.Close()
	}

	reader, err := store.OpenReadOnly(database)
	if err != nil {
		t.Fatalf("reopening the central reader: %v", err)
	}
	t.Cleanup(func() { _ = reader.Close() })
	refs, err := reader.TaskRefs()
	if err != nil {
		t.Fatalf("listing the central database: %v", err)
	}
	if len(refs) != 2 {
		t.Fatalf("want two project-scoped tasks in one file, got %+v", refs)
	}
}

// TestArtifactWritesAlsoCrossTheDaemon closes the ownership rule over the two
// write paths that are not events: storing content and forgetting it.
func TestArtifactWritesAlsoCrossTheDaemon(t *testing.T) {
	dir := t.TempDir()
	database := filepath.Join(dir, "luna.db")
	socket := filepath.Join(dir, SocketName)
	server, err := Listen(Options{Socket: socket, Store: database})
	if err != nil {
		t.Fatalf("starting the daemon: %v", err)
	}
	t.Cleanup(func() { _ = server.Close() })

	central, err := store.OpenReadOnly(database)
	if err != nil {
		t.Fatalf("opening the reader: %v", err)
	}
	t.Cleanup(func() { _ = central.Close() })
	client := Client{Path: socket}
	central.Via = client
	project := central.ForProject("one-project")

	blob := store.Blob{
		TaskID: "TASK-1", Stage: "plan", Artifact: "contract", Seq: 1, Body: []byte("terms"),
	}
	if err := project.PutBlob(blob); err != nil {
		t.Fatalf("forwarding the artifact: %v", err)
	}
	if _, err := project.LatestBlob("TASK-1", "plan", "contract"); err != nil {
		t.Fatalf("the daemon did not store the artifact: %v", err)
	}
	if err := project.ForgetBlobs("TASK-1"); err != nil {
		t.Fatalf("forwarding the cleanup: %v", err)
	}
	if _, err := project.LatestBlob("TASK-1", "plan", "contract"); !errors.Is(err, store.ErrNoSuchBlob) {
		t.Fatalf("the daemon did not forget the artifact: %v", err)
	}
}

// TestTheDaemonImportsAndArchivesPerProjectStores covers the upgrade path. The
// old file remains recoverable under a migrated name, and rerunning the daemon
// cannot duplicate its append-only history.
func TestTheDaemonImportsAndArchivesPerProjectStores(t *testing.T) {
	dir := t.TempDir()
	legacyRoot := filepath.Join(dir, "projects")
	project := "github.com-one-app-a1"
	legacyPath := filepath.Join(legacyRoot, project, "luna.db")

	if err := os.MkdirAll(filepath.Dir(legacyPath), 0o750); err != nil {
		t.Fatal(err)
	}
	legacy, err := sql.Open("sqlite", legacyPath)
	if err != nil {
		t.Fatalf("creating the legacy store: %v", err)
	}
	if _, err := legacy.Exec(`
		PRAGMA user_version = 1;
		CREATE TABLE events (
			task_id TEXT NOT NULL, seq INTEGER NOT NULL, action TEXT NOT NULL,
			payload TEXT NOT NULL DEFAULT '', at INTEGER NOT NULL DEFAULT 0,
			PRIMARY KEY (task_id, seq));
		CREATE TABLE blobs (
			task_id TEXT NOT NULL, stage TEXT NOT NULL, artifact TEXT NOT NULL,
			seq INTEGER NOT NULL, hash TEXT NOT NULL, body BLOB NOT NULL,
			at INTEGER NOT NULL DEFAULT 0,
			PRIMARY KEY (task_id, stage, artifact, seq));
		INSERT INTO events VALUES ('OLD-1', 1, 'TaskCreated', '{"kind":"chore"}', 1234);
	`); err != nil {
		t.Fatalf("seeding the legacy store: %v", err)
	}
	if err := legacy.Close(); err != nil {
		t.Fatalf("closing the legacy store: %v", err)
	}

	database := filepath.Join(dir, "luna.db")
	server, err := Listen(Options{
		Socket: filepath.Join(dir, SocketName), Store: database, LegacyRoot: legacyRoot,
	})
	if err != nil {
		t.Fatalf("starting the migrating daemon: %v", err)
	}
	if err := server.Close(); err != nil {
		t.Fatalf("closing the migrating daemon: %v", err)
	}

	reader, err := store.OpenReadOnly(database)
	if err != nil {
		t.Fatalf("opening the migrated database: %v", err)
	}
	t.Cleanup(func() { _ = reader.Close() })
	ids, err := reader.ForProject(project).Tasks()
	if err != nil {
		t.Fatalf("listing the imported project: %v", err)
	}
	if len(ids) != 1 || ids[0] != "OLD-1" {
		t.Fatalf("the old task was not imported: %v", ids)
	}
	if _, err := os.Stat(legacyPath + ".migrated"); err != nil {
		t.Fatalf("the imported store was not archived: %v", err)
	}
}

func TestAClientCanImportOneCheckoutStore(t *testing.T) {
	client, dir := running(t)
	legacyPath := filepath.Join(dir, "checkout", ".luna", "luna.db")
	legacy, err := store.OpenAs(legacyPath, store.LunaOwnsTheLog)
	if err != nil {
		t.Fatalf("creating the checkout store: %v", err)
	}
	if err := legacy.AppendAction("OLD-2", fsm.TaskCreated{
		Kind: fsm.KindChore, Flow: fsm.Fingerprint(fsm.DefaultFlow()),
	}); err != nil {
		t.Fatalf("seeding the checkout store: %v", err)
	}
	if err := legacy.Close(); err != nil {
		t.Fatalf("closing the checkout store: %v", err)
	}

	if err := client.ImportLegacy("checkout-project", legacyPath); err != nil {
		t.Fatalf("asking the daemon to import: %v", err)
	}
	if _, err := os.Stat(legacyPath + ".migrated"); err != nil {
		t.Fatalf("the checkout store was not archived: %v", err)
	}

	reader, err := store.OpenReadOnly(filepath.Join(dir, "luna.db"))
	if err != nil {
		t.Fatalf("opening the central reader: %v", err)
	}
	t.Cleanup(func() { _ = reader.Close() })
	ids, err := reader.ForProject("checkout-project").Tasks()
	if err != nil || len(ids) != 1 || ids[0] != "OLD-2" {
		t.Fatalf("imported checkout tasks = %v, %v", ids, err)
	}
}

func TestTheDaemonRejectsIncompleteCentralWriteRequests(t *testing.T) {
	client, _ := running(t)
	requests := []Request{
		{Op: "put_blob"},
		{Op: "put_blob", Project: "one-project"},
		{Op: "forget_blobs"},
		{Op: "import", Project: "one-project"},
	}
	for _, req := range requests {
		if _, err := client.Do(req); err == nil {
			t.Errorf("incomplete %s request was accepted", req.Op)
		}
	}
}

func TestDaemonStartupRejectsInvalidStoreLocations(t *testing.T) {
	if _, err := Listen(Options{}); err == nil {
		t.Fatal("a daemon with no central store was accepted")
	}

	dir := t.TempDir()
	blockedParent := filepath.Join(dir, "blocked")
	if err := os.WriteFile(blockedParent, []byte("file"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := Listen(Options{
		Socket: filepath.Join(dir, "blocked-parent.sock"),
		Store:  filepath.Join(blockedParent, "luna.db"),
	}); err == nil {
		t.Fatal("a daemon created its lock below a file")
	}

	storePath := filepath.Join(dir, "store-is-a-directory")
	if err := os.Mkdir(storePath, 0o750); err != nil {
		t.Fatal(err)
	}
	if _, err := Listen(Options{
		Socket: filepath.Join(dir, "directory-store.sock"), Store: storePath,
	}); err == nil {
		t.Fatal("a directory was accepted as the central database")
	}

	legacyRoot := filepath.Join(dir, "legacy-is-a-file")
	if err := os.WriteFile(legacyRoot, []byte("file"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := Listen(Options{
		Socket: filepath.Join(dir, "legacy-file.sock"),
		Store:  filepath.Join(dir, "legacy-file.db"), LegacyRoot: legacyRoot,
	}); err == nil {
		t.Fatal("a legacy root that is a file was accepted")
	}
}

func TestLegacyScanIgnoresEntriesWithoutDatabases(t *testing.T) {
	dir := t.TempDir()
	legacyRoot := filepath.Join(dir, "projects")
	if err := os.MkdirAll(filepath.Join(legacyRoot, "empty-project"), 0o750); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(legacyRoot, "README"), []byte("not a project"), 0o600); err != nil {
		t.Fatal(err)
	}

	server, err := Listen(Options{
		Socket: filepath.Join(dir, "scan.sock"),
		Store:  filepath.Join(dir, "scan.db"), LegacyRoot: legacyRoot,
	})
	if err != nil {
		t.Fatalf("scanning harmless legacy entries: %v", err)
	}
	if err := server.Close(); err != nil {
		t.Fatalf("closing the daemon: %v", err)
	}

	server, err = Listen(Options{
		Socket: filepath.Join(dir, "missing-root.sock"),
		Store:  filepath.Join(dir, "missing-root.db"), LegacyRoot: filepath.Join(dir, "missing"),
	})
	if err != nil {
		t.Fatalf("starting without a legacy root: %v", err)
	}
	if err := server.Close(); err != nil {
		t.Fatalf("closing the daemon: %v", err)
	}
}

func TestCentralImportReportsCopyAndArchiveFailures(t *testing.T) {
	dir := t.TempDir()
	central, err := store.OpenAs(filepath.Join(dir, "central.db"), store.LunaOwnsTheLog)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = central.Close() })
	if err := importAndArchive(central, "one-project", filepath.Join(dir, "missing.db")); err == nil {
		t.Fatal("a missing legacy store was reported as imported")
	}

	legacyPath := filepath.Join(dir, "legacy.db")
	legacy, err := store.OpenAs(legacyPath, store.LunaOwnsTheLog)
	if err != nil {
		t.Fatal(err)
	}
	if err := legacy.Close(); err != nil {
		t.Fatal(err)
	}
	blockedArchive := legacyPath + ".migrated"
	if err := os.Mkdir(blockedArchive, 0o750); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(blockedArchive, "in-the-way"), []byte("x"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := importAndArchive(central, "one-project", legacyPath); err == nil {
		t.Fatal("an archive destination that cannot be replaced was ignored")
	}
}

func TestDaemonTaskListingReportsAClosedStore(t *testing.T) {
	s, err := store.OpenAs(filepath.Join(t.TempDir(), "closed.db"), store.LunaOwnsTheLog)
	if err != nil {
		t.Fatal(err)
	}
	if err := s.Close(); err != nil {
		t.Fatal(err)
	}
	resp := (&Server{store: s}).answer(Request{Op: "tasks"})
	if resp.Err == "" {
		t.Fatal("a closed central store produced a successful task listing")
	}
}

func TestDaemonReportsAnUnopenableLockFile(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "luna.db")
	if err := os.Mkdir(path+".lock", 0o750); err != nil {
		t.Fatal(err)
	}
	if _, err := Listen(Options{Socket: filepath.Join(dir, "daemon.sock"), Store: path}); err == nil {
		t.Fatal("a directory was accepted as the daemon lock file")
	}
}
