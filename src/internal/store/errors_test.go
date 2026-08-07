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
	s, err := Open(path)
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
	t.Run("AppendWithBlob", func(t *testing.T) {
		if _, err := s.AppendWithBlob("LUNA-1", Event{Action: "Complete"}, []byte("x")); err == nil {
			t.Error("appending with a blob to a closed store must fail")
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
		if _, err := s.Replay("LUNA-1", fsm.KindFeature, fsm.DefaultFlow()); err == nil {
			t.Error("replaying from a closed store must fail")
		}
	})
	t.Run("AwaitingGate", func(t *testing.T) {
		if _, err := s.AwaitingGate(fsm.KindFeature, fsm.DefaultFlow()); err == nil {
			t.Error("listing gates on a closed store must fail")
		}
	})
	t.Run("PutBlob", func(t *testing.T) {
		if _, err := s.PutBlob([]byte("x")); err == nil {
			t.Error("storing a blob on a closed store must fail")
		}
	})
	t.Run("Blob", func(t *testing.T) {
		if _, err := s.Blob("whatever"); err == nil {
			t.Error("reading a blob from a closed store must fail")
		}
	})
	t.Run("BlobCount", func(t *testing.T) {
		if _, err := s.BlobCount(); err == nil {
			t.Error("counting blobs on a closed store must fail")
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

	_, err := s.Replay("LUNA-1", fsm.KindFeature, fsm.DefaultFlow())

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

	if _, err := s.AwaitingGate(fsm.KindFeature, fsm.DefaultFlow()); err == nil {
		t.Error("a task that cannot be replayed must not vanish from the listing")
	}
}

// TestABlobSurvivesTheEventThatCarriedIt covers the dedup branch of appendTx.
//
// Two events carrying identical content share one blob row, and both keep working
// — deduplication must not make the second reference dangle.
func TestABlobSurvivesTheEventThatCarriedIt(t *testing.T) {
	s := openTemp(t)

	content := []byte("the same snapshot twice")
	first, err := s.AppendWithBlob("LUNA-1", Event{Action: "Complete"}, content)
	if err != nil {
		t.Fatalf("first append: %v", err)
	}
	second, err := s.AppendWithBlob("LUNA-2", Event{Action: "Complete"}, content)
	if err != nil {
		t.Fatalf("second append: %v", err)
	}

	if first != second {
		t.Errorf("identical content hashes the same: %q vs %q", first, second)
	}

	count, err := s.BlobCount()
	if err != nil {
		t.Fatalf("counting: %v", err)
	}
	if count != 1 {
		t.Errorf("want one shared row, got %d", count)
	}

	for _, id := range []string{"LUNA-1", "LUNA-2"} {
		events, err := s.Events(id)
		if err != nil {
			t.Fatalf("reading %s: %v", id, err)
		}
		if events[0].Blob != first {
			t.Errorf("%s should point at the shared blob, got %q", id, events[0].Blob)
		}
	}
}

// TestAppendingAnEventThatCarriesAnExistingHash covers the blob-by-reference path.
//
// An event can point at content already in the store without resending it, which
// is what a handoff referring to an unchanged snapshot does.
func TestAppendingAnEventThatCarriesAnExistingHash(t *testing.T) {
	s := openTemp(t)

	hash, err := s.PutBlob([]byte("stored earlier"))
	if err != nil {
		t.Fatalf("storing: %v", err)
	}

	if err := s.Append("LUNA-1", Event{Action: "Complete", Blob: hash}); err != nil {
		t.Fatalf("appending: %v", err)
	}

	events, err := s.Events("LUNA-1")
	if err != nil {
		t.Fatalf("reading: %v", err)
	}
	if events[0].Blob != hash {
		t.Errorf("the event keeps the hash it was given, got %q", events[0].Blob)
	}
}
