package store

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/brunoomariano/luna/src/internal/fsm"
)

// TestOpeningAnUnwritablePathIsReported covers the mkdir failure in Open.
//
// A store that cannot create its directory says so at open time. Discovering it
// on the first append instead would surface the problem several transitions after
// the cause.
func TestOpeningAnUnwritablePathIsReported(t *testing.T) {
	if os.Geteuid() == 0 {
		t.Skip("root ignores directory permissions, so this cannot be provoked")
	}

	locked := filepath.Join(t.TempDir(), "locked")
	if err := os.Mkdir(locked, 0o500); err != nil { // read and execute, no write
		t.Fatalf("preparing the fixture: %v", err)
	}

	_, err := Open(filepath.Join(locked, "nested", "luna.db"))

	if err == nil {
		t.Fatal("opening under an unwritable directory must fail")
	}
	if !strings.Contains(err.Error(), "creating") {
		t.Errorf("the error should name what it could not create, got %v", err)
	}
}

// TestOperationsOnAClosedStoreAreReported covers the query error paths.
//
// Every method that talks to the database has to handle the handle being gone.
// Closing the store is the cheapest way to make all of them fail at once, and it
// stands in for the real cases — a deleted file, a full disk, a locked database.
func TestOperationsOnAClosedStoreAreReported(t *testing.T) {
	path := filepath.Join(t.TempDir(), "luna.db")
	s, err := OpenAs(path, LunaOwnsTheLog)
	if err != nil {
		t.Fatalf("opening: %v", err)
	}
	if err := s.Append("LUNA-1", Event{Action: "Advance"}); err != nil {
		t.Fatalf("seeding: %v", err)
	}
	if err := s.Close(); err != nil {
		t.Fatalf("closing: %v", err)
	}

	t.Run("Append", func(t *testing.T) {
		if err := s.Append("LUNA-1", Event{Action: "Advance"}); err == nil {
			t.Error("appending to a closed store must fail")
		}
	})
	t.Run("Events", func(t *testing.T) {
		if _, err := s.Events("LUNA-1"); err == nil {
			t.Error("reading a closed store must fail")
		}
	})
	t.Run("Tasks", func(t *testing.T) {
		if _, err := s.Tasks(); err == nil {
			t.Error("listing a closed store must fail")
		}
	})
	t.Run("Replay", func(t *testing.T) {
		if _, err := s.Replay("LUNA-1", fsm.DefaultFlow()); err == nil {
			t.Error("replaying from a closed store must fail")
		}
	})
	t.Run("AwaitingGate", func(t *testing.T) {
		if _, err := s.AwaitingGate(fsm.DefaultFlow()); err == nil {
			t.Error("listing gates on a closed store must fail")
		}
	})
}

// TestReplayStopsWhenTheReducerRefuses covers the reducer error path in Replay.
//
// A log holding an illegal sequence — two approvals for one gate — cannot be
// replayed into any state. Stopping and naming the sequence number is what makes
// a corrupted log diagnosable; carrying on would produce a state the task was
// never in.
func TestReplayStopsWhenTheReducerRefuses(t *testing.T) {
	s := openTemp(t)

	// Approving a gate that was never opened is illegal for the reducer.
	if err := s.AppendAction("LUNA-1", fsm.GateApprove{}); err != nil {
		t.Fatalf("appending: %v", err)
	}

	_, err := s.Replay("LUNA-1", fsm.DefaultFlow())

	if err == nil {
		t.Fatal("an illegal sequence must stop the replay")
	}
	if !strings.Contains(err.Error(), "seq 1") {
		t.Errorf("the error should name where it stopped, got %v", err)
	}
}

// TestAwaitingGateSurfacesAReplayFailure covers the error path in AwaitingGate.
//
// Listing suspended tasks replays each one. A task whose log will not replay must
// not be quietly dropped from the listing: a task that cannot be rebuilt is
// exactly the one someone needs to hear about.
func TestAwaitingGateSurfacesAReplayFailure(t *testing.T) {
	s := openTemp(t)

	if err := s.Append("LUNA-1", Event{Action: "TimeTravel"}); err != nil {
		t.Fatalf("appending: %v", err)
	}

	if _, err := s.AwaitingGate(fsm.DefaultFlow()); err == nil {
		t.Error("a task that cannot be replayed must not vanish from the listing")
	}
}

// TestOpeningAFileThatIsNotADatabase covers the schema-creation failure in Open.
//
// Pointing the store at an existing file that is not SQLite has to fail at open
// time with a message naming the file. Discovering it on the first append would
// put the error several transitions away from the cause.
func TestOpeningAFileThatIsNotADatabase(t *testing.T) {
	path := filepath.Join(t.TempDir(), "not-a-database.db")
	if err := os.WriteFile(path, []byte("this is just text"), 0o600); err != nil {
		t.Fatalf("preparing the fixture: %v", err)
	}

	_, err := Open(path)

	if err == nil {
		t.Fatal("opening a file that is not a database must fail")
	}
	if !strings.Contains(err.Error(), path) {
		t.Errorf("the error should name the file, got %v", err)
	}
}

// TestEncodingAnActionThatCannotBeSerialised covers withPayload's error branch.
//
// An action carrying something JSON cannot represent must be refused at the point
// of writing rather than producing a log entry that will not decode.
func TestEncodingAnActionThatCannotBeSerialised(t *testing.T) {
	// A channel cannot be marshalled, so this reaches the encoder through a map
	// value that json rejects.
	_, _, err := withPayload("Complete", map[string]any{"bad": make(chan int)})

	if err == nil {
		t.Fatal("an unserialisable payload must be reported")
	}
	if !strings.Contains(err.Error(), "encoding") {
		t.Errorf("the error should say what it failed at, got %v", err)
	}
}
