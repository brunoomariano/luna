package store

import (
	"database/sql"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/brunoomariano/luna/src/internal/fsm"
)

// TestOneLogKeepsEqualTaskIDsSeparateByProject covers the identity boundary of
// the central store. A task id is only unique inside its project, so moving all
// history into one database must not merge two projects that both use it.
func TestOneLogKeepsEqualTaskIDsSeparateByProject(t *testing.T) {
	s, err := OpenAs(filepath.Join(t.TempDir(), "luna.db"), LunaOwnsTheLog)
	if err != nil {
		t.Fatalf("opening the central store: %v", err)
	}
	t.Cleanup(func() { _ = s.Close() })

	for _, project := range []string{"github.com-one-app-a1", "github.com-two-app-b2"} {
		projectStore := s.ForProject(project)
		if err := projectStore.AppendAction("TASK-1", fsm.TaskCreated{
			Kind: fsm.KindChore, Flow: fsm.Fingerprint(fsm.DefaultFlow()),
		}); err != nil {
			t.Fatalf("creating TASK-1 in %s: %v", project, err)
		}
	}

	refs, err := s.TaskRefs()
	if err != nil {
		t.Fatalf("listing the central store: %v", err)
	}
	if len(refs) != 2 || refs[0].Project == refs[1].Project || refs[0].ID != "TASK-1" || refs[1].ID != "TASK-1" {
		t.Fatalf("equal ids from two projects were not kept separate: %+v", refs)
	}
}

func TestAppendAtUsesTheProjectScopedSequence(t *testing.T) {
	s, err := OpenAs(filepath.Join(t.TempDir(), "luna.db"), LunaOwnsTheLog)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = s.Close() })
	project := s.ForProject("one-project")
	if err := project.AppendAt("TASK-1", 0, Event{Action: "first"}); err != nil {
		t.Fatalf("conditional first append: %v", err)
	}
	if err := project.Close(); err != nil {
		t.Fatalf("closing a scoped view: %v", err)
	}
	events, err := project.Events("TASK-1")
	if err != nil || len(events) != 1 || events[0].Seq != 1 {
		t.Fatalf("project events = %+v, %v", events, err)
	}
}

func TestReadOnlyOpenRequiresTheDaemonSchema(t *testing.T) {
	path := filepath.Join(t.TempDir(), "old.db")
	db, err := sql.Open("sqlite", path)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`PRAGMA user_version = 1`); err != nil {
		t.Fatal(err)
	}
	if err := db.Close(); err != nil {
		t.Fatal(err)
	}

	if _, err := OpenReadOnly(path); !errors.Is(err, ErrSchemaTooOld) {
		t.Fatalf("read-only open of schema 1 = %v", err)
	}
}

func TestWriterRefusesANewerSchema(t *testing.T) {
	path := filepath.Join(t.TempDir(), "future.db")
	db, err := sql.Open("sqlite", path)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`PRAGMA user_version = 99`); err != nil {
		t.Fatal(err)
	}
	if err := db.Close(); err != nil {
		t.Fatal(err)
	}

	_, err = OpenAs(path, LunaOwnsTheLog)
	if err == nil || !strings.Contains(err.Error(), "newer than this build") {
		t.Fatalf("opening schema 99 = %v", err)
	}
}

// TestAReadOnlyOpenCannotCreateOrAppend proves the CLI side of the ownership
// boundary. A reader may inspect the daemon's database, but cannot bootstrap a
// missing file or append an event to an existing one.
func TestAReadOnlyOpenCannotCreateOrAppend(t *testing.T) {
	dir := t.TempDir()
	missing := filepath.Join(dir, "missing.db")
	if _, err := OpenReadOnly(missing); err == nil {
		t.Fatal("a read-only open created a missing central store")
	}
	if _, err := os.Stat(missing); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("the missing store was written: %v", err)
	}

	path := filepath.Join(dir, "luna.db")
	writer, err := OpenAs(path, LunaOwnsTheLog)
	if err != nil {
		t.Fatalf("creating the store through its owner: %v", err)
	}
	if err := writer.Close(); err != nil {
		t.Fatalf("closing the owner: %v", err)
	}

	reader, err := OpenReadOnly(path)
	if err != nil {
		t.Fatalf("opening the reader: %v", err)
	}
	t.Cleanup(func() { _ = reader.Close() })
	if err := reader.AppendAction("TASK-1", fsm.TaskCreated{
		Kind: fsm.KindChore, Flow: fsm.Fingerprint(fsm.DefaultFlow()),
	}); !errors.Is(err, ErrNotTheOwner) {
		t.Fatalf("a CLI reader appended to the central store: %v", err)
	}
}
