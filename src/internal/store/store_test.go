package store

import (
	"errors"
	"path/filepath"
	"testing"

	"github.com/brunoomariano/luna/src/internal/fsm"
)

// openTemp gives each test its own database file, closed when the test ends.
// A file rather than :memory: on purpose — the property under test is surviving
// a restart, and an in-memory database cannot demonstrate that.
func openTemp(t *testing.T) *Store {
	t.Helper()

	path := filepath.Join(t.TempDir(), "luna.db")
	s, err := Open(path)
	if err != nil {
		t.Fatalf("opening the store: %v", err)
	}
	t.Cleanup(func() { _ = s.Close() })
	return s
}

// ── block K: the log only ever grows ─────────────────────────────────────────

// TestAppendedEventsComeBackInOrder covers scenario K1.
//
// The log is the state, so the order it comes back in is the order the task
// happened in. Getting this wrong would replay a task into a state it never was.
func TestAppendedEventsComeBackInOrder(t *testing.T) {
	s := openTemp(t)

	for _, action := range []string{"Advance", "GateApprove", "Complete"} {
		if err := s.Append("LUNA-1", Event{Action: action}); err != nil {
			t.Fatalf("appending %s: %v", action, err)
		}
	}

	events, err := s.Events("LUNA-1")
	if err != nil {
		t.Fatalf("reading back: %v", err)
	}

	want := []string{"Advance", "GateApprove", "Complete"}
	if len(events) != len(want) {
		t.Fatalf("want %d events, got %d", len(want), len(events))
	}
	for i, action := range want {
		if events[i].Action != action {
			t.Errorf("position %d: want %q, got %q", i, action, events[i].Action)
		}
		if events[i].Seq != i+1 {
			t.Errorf("position %d: want seq %d, got %d", i, i+1, events[i].Seq)
		}
	}
}

// TestSequenceIsPerTaskNotGlobal covers scenario K2.
//
// Two tasks writing to the same store number their own events. A global counter
// would make a task's history depend on what other tasks did meanwhile, which is
// exactly the coupling per-task worktrees exist to avoid.
func TestSequenceIsPerTaskNotGlobal(t *testing.T) {
	s := openTemp(t)

	for _, id := range []string{"LUNA-1", "LUNA-2", "LUNA-1"} {
		if err := s.Append(id, Event{Action: "Advance"}); err != nil {
			t.Fatalf("appending to %s: %v", id, err)
		}
	}

	first, err := s.Events("LUNA-1")
	if err != nil {
		t.Fatalf("reading LUNA-1: %v", err)
	}
	second, err := s.Events("LUNA-2")
	if err != nil {
		t.Fatalf("reading LUNA-2: %v", err)
	}

	if len(first) != 2 || first[1].Seq != 2 {
		t.Errorf("LUNA-1 numbers its own events; got %+v", first)
	}
	if len(second) != 1 || second[0].Seq != 1 {
		t.Errorf("LUNA-2 starts at 1 regardless of LUNA-1; got %+v", second)
	}
}

// TestAnUnknownTaskHasNoEvents covers scenario K3.
//
// Asking about a task that never wrote anything is not an error — it is a task
// with no history, which is a normal thing to ask about.
func TestAnUnknownTaskHasNoEvents(t *testing.T) {
	s := openTemp(t)

	events, err := s.Events("never-existed")
	if err != nil {
		t.Fatalf("an unknown task is not an error: %v", err)
	}
	if len(events) != 0 {
		t.Errorf("want no events, got %d", len(events))
	}
}

// TestTheStoreExposesNoWayToRewriteHistory covers scenario K4 — INV-core-2.
//
// The invariant is enforced by absence: there is no Update and no Delete on the
// type, so no caller can reach for one. A test cannot prove a method is missing,
// but it can prove the append path never overwrites what is already there.
func TestTheStoreExposesNoWayToRewriteHistory(t *testing.T) {
	s := openTemp(t)

	if err := s.Append("LUNA-1", Event{Action: "Advance", Payload: "first"}); err != nil {
		t.Fatalf("first append: %v", err)
	}
	if err := s.Append("LUNA-1", Event{Action: "Advance", Payload: "second"}); err != nil {
		t.Fatalf("second append: %v", err)
	}

	events, err := s.Events("LUNA-1")
	if err != nil {
		t.Fatalf("reading back: %v", err)
	}

	if len(events) != 2 {
		t.Fatalf("an append never replaces; want 2 events, got %d", len(events))
	}
	if events[0].Payload != "first" {
		t.Errorf("the earlier event is untouched; got %q", events[0].Payload)
	}
}

// ── block L: replay, and surviving a restart ─────────────────────────────────

// TestStateIsRebuiltFromTheLog covers scenario L1 — the reason this wave exists.
//
// Write a task's history, throw the process away, open the file again, and the
// state comes back identical. Nothing was ever only in memory (INV-core-2).
func TestStateIsRebuiltFromTheLog(t *testing.T) {
	path := filepath.Join(t.TempDir(), "luna.db")

	// First process: drive a task partway through the flow.
	first, err := Open(path)
	if err != nil {
		t.Fatalf("opening: %v", err)
	}

	live := fsm.NewTaskState("LUNA-1", fsm.KindFeature)
	for _, action := range []fsm.Action{
		fsm.Advance{Flow: fsm.DefaultFlow()},
		fsm.GateApprove{},
		fsm.Complete{Delivered: []fsm.Artifact{"repos"}},
		fsm.Advance{Flow: fsm.DefaultFlow()},
	} {
		live, err = fsm.Reduce(live, action)
		if err != nil {
			t.Fatalf("reducing: %v", err)
		}
		if err := first.AppendAction("LUNA-1", action); err != nil {
			t.Fatalf("appending: %v", err)
		}
	}
	if err := first.Close(); err != nil {
		t.Fatalf("closing: %v", err)
	}

	// Second process: nothing in memory, only the file on disk.
	second, err := Open(path)
	if err != nil {
		t.Fatalf("reopening: %v", err)
	}
	defer func() { _ = second.Close() }()

	replayed, err := second.Replay("LUNA-1", fsm.KindFeature, fsm.DefaultFlow())
	if err != nil {
		t.Fatalf("replaying: %v", err)
	}

	if replayed.Stage != live.Stage {
		t.Errorf("stage: want %q, got %q", live.Stage, replayed.Stage)
	}
	if replayed.Status != live.Status {
		t.Errorf("status: want %q, got %q", live.Status, replayed.Status)
	}
	for artifact := range live.Context.Artifacts {
		if !replayed.Context.HasArtifact(artifact) {
			t.Errorf("artifact %q was lost in the replay", artifact)
		}
	}
}

// TestReplayingAnEmptyLogGivesAFreshTask covers scenario L2.
//
// A task with no events is a task that has not started. Replay should say so
// rather than failing — that is the state of every task before its first move.
func TestReplayingAnEmptyLogGivesAFreshTask(t *testing.T) {
	s := openTemp(t)

	state, err := s.Replay("LUNA-1", fsm.KindFeature, fsm.DefaultFlow())
	if err != nil {
		t.Fatalf("replaying an empty log is not an error: %v", err)
	}

	if state.Status != fsm.StatusReady {
		t.Errorf("want a ready task, got %q", state.Status)
	}
	if state.Stage != "" {
		t.Errorf("a fresh task is in no stage, got %q", state.Stage)
	}
}

// TestReplayIsDeterministic covers scenario L3.
//
// Replaying the same log twice gives the same state. This is what ADR-0024 buys
// by keeping the reducer pure: if a transition could run a test suite or read a
// clock, the second replay could disagree with the first.
func TestReplayIsDeterministic(t *testing.T) {
	s := openTemp(t)

	for _, action := range []fsm.Action{
		fsm.Advance{Flow: fsm.DefaultFlow()},
		fsm.GateApprove{},
	} {
		if err := s.AppendAction("LUNA-1", action); err != nil {
			t.Fatalf("appending: %v", err)
		}
	}

	once, err := s.Replay("LUNA-1", fsm.KindFeature, fsm.DefaultFlow())
	if err != nil {
		t.Fatalf("first replay: %v", err)
	}
	twice, err := s.Replay("LUNA-1", fsm.KindFeature, fsm.DefaultFlow())
	if err != nil {
		t.Fatalf("second replay: %v", err)
	}

	if once.Stage != twice.Stage || once.Status != twice.Status {
		t.Errorf("replay disagreed with itself: %+v vs %+v", once, twice)
	}
}

// TestAnUnknownActionInTheLogIsReported covers scenario L4.
//
// A log written by a newer version can hold an action this build does not know.
// Guessing would replay the task into a state it was never in, so replay stops
// and says which action it could not read.
func TestAnUnknownActionInTheLogIsReported(t *testing.T) {
	s := openTemp(t)

	if err := s.Append("LUNA-1", Event{Action: "TimeTravel", Payload: "{}"}); err != nil {
		t.Fatalf("appending: %v", err)
	}

	_, err := s.Replay("LUNA-1", fsm.KindFeature, fsm.DefaultFlow())

	if !errors.Is(err, ErrUnknownAction) {
		t.Errorf("want ErrUnknownAction, got %v", err)
	}
}

// ── block M: the content store ───────────────────────────────────────────────

// TestABlobComesBackByItsHash covers scenario M1.
//
// Content addressing: the key is what the content hashes to, so asking for a hash
// either gives you exactly that content or nothing at all.
func TestABlobComesBackByItsHash(t *testing.T) {
	s := openTemp(t)

	content := []byte("the contract a human approved")
	hash, err := s.PutBlob(content)
	if err != nil {
		t.Fatalf("storing: %v", err)
	}

	got, err := s.Blob(hash)
	if err != nil {
		t.Fatalf("reading back: %v", err)
	}
	if string(got) != string(content) {
		t.Errorf("want %q, got %q", content, got)
	}
}

// TestTheSameContentStoresOnce covers scenario M2.
//
// Two handoffs pointing at identical content share one row. Without this, a task
// looping through build a dozen times would store a dozen copies of the same
// unchanged file.
func TestTheSameContentStoresOnce(t *testing.T) {
	s := openTemp(t)

	first, err := s.PutBlob([]byte("identical"))
	if err != nil {
		t.Fatalf("first put: %v", err)
	}
	second, err := s.PutBlob([]byte("identical"))
	if err != nil {
		t.Fatalf("second put: %v", err)
	}

	if first != second {
		t.Errorf("the same content must hash the same: %q vs %q", first, second)
	}

	count, err := s.BlobCount()
	if err != nil {
		t.Fatalf("counting: %v", err)
	}
	if count != 1 {
		t.Errorf("identical content is stored once, got %d rows", count)
	}
}

// TestAMissingBlobIsReported covers scenario M3.
//
// Asking for content that was never stored is an error rather than empty bytes:
// a handoff pointing at a hash the store does not have is a broken chain, and
// silently returning nothing would let the next stage start on emptiness.
func TestAMissingBlobIsReported(t *testing.T) {
	s := openTemp(t)

	_, err := s.Blob("0000000000000000000000000000000000000000000000000000000000000000")

	if !errors.Is(err, ErrNoSuchBlob) {
		t.Errorf("want ErrNoSuchBlob, got %v", err)
	}
}

// TestEventAndBlobLandTogether covers scenario M4 — the atomicity ADR-0025 buys.
//
// The snapshot and the event that points at it are written in one transaction. A
// process dying between the two would otherwise leave either an orphan blob or a
// handoff referring to content that was never stored.
func TestEventAndBlobLandTogether(t *testing.T) {
	s := openTemp(t)

	content := []byte("the snapshot at handoff time")
	hash, err := s.AppendWithBlob("LUNA-1", Event{Action: "Complete"}, content)
	if err != nil {
		t.Fatalf("appending with blob: %v", err)
	}

	events, err := s.Events("LUNA-1")
	if err != nil {
		t.Fatalf("reading events: %v", err)
	}
	if len(events) != 1 {
		t.Fatalf("want the event, got %d", len(events))
	}
	if events[0].Blob != hash {
		t.Errorf("the event points at the blob: want %q, got %q", hash, events[0].Blob)
	}

	blob, err := s.Blob(hash)
	if err != nil {
		t.Fatalf("the blob must be there too: %v", err)
	}
	if string(blob) != string(content) {
		t.Errorf("want %q, got %q", content, blob)
	}
}

// ── block N: finding what needs a human ──────────────────────────────────────

// TestSuspendedTasksAreListable covers scenario N1 — INV-core-12.
//
// A task waiting at a gate has released its slot, so nothing is running to remind
// anyone it exists. If it were not discoverable by a query, it would wait
// forever — the second form of silent failure.
func TestSuspendedTasksAreListable(t *testing.T) {
	s := openTemp(t)

	// One task stops at the discovery gate; another runs past it.
	if err := s.AppendAction("waiting", fsm.Advance{Flow: fsm.DefaultFlow()}); err != nil {
		t.Fatalf("appending: %v", err)
	}
	for _, action := range []fsm.Action{
		fsm.Advance{Flow: fsm.DefaultFlow()},
		fsm.GateApprove{},
	} {
		if err := s.AppendAction("running", action); err != nil {
			t.Fatalf("appending: %v", err)
		}
	}

	waiting, err := s.AwaitingGate(fsm.KindFeature, fsm.DefaultFlow())
	if err != nil {
		t.Fatalf("listing: %v", err)
	}

	if len(waiting) != 1 {
		t.Fatalf("want the one suspended task, got %d (%v)", len(waiting), waiting)
	}
	if waiting[0].TaskID != "waiting" {
		t.Errorf("want the task that stopped at a gate, got %q", waiting[0].TaskID)
	}
	if waiting[0].Stage != "discovery" {
		t.Errorf("the listing says where it stopped; got %q", waiting[0].Stage)
	}
}

// TestListingTasksWithNothingSuspended covers scenario N2.
//
// Nothing waiting is a normal answer, not an empty-state bug.
func TestListingTasksWithNothingSuspended(t *testing.T) {
	s := openTemp(t)

	waiting, err := s.AwaitingGate(fsm.KindFeature, fsm.DefaultFlow())
	if err != nil {
		t.Fatalf("listing an empty store is not an error: %v", err)
	}
	if len(waiting) != 0 {
		t.Errorf("want nothing waiting, got %v", waiting)
	}
}

// TestTaskIDsAreListable covers scenario N3.
func TestTaskIDsAreListable(t *testing.T) {
	s := openTemp(t)

	for _, id := range []string{"LUNA-2", "LUNA-1", "LUNA-2"} {
		if err := s.Append(id, Event{Action: "Advance"}); err != nil {
			t.Fatalf("appending: %v", err)
		}
	}

	ids, err := s.Tasks()
	if err != nil {
		t.Fatalf("listing tasks: %v", err)
	}

	if len(ids) != 2 {
		t.Fatalf("want 2 distinct tasks, got %d (%v)", len(ids), ids)
	}
	if ids[0] != "LUNA-1" || ids[1] != "LUNA-2" {
		t.Errorf("want them sorted, got %v", ids)
	}
}

// ── opening and closing ──────────────────────────────────────────────────────

// TestOpeningCreatesTheSchema covers the bootstrap path.
//
// A store opened against a path that does not exist yet is a new store, not an
// error: that is what happens on someone's first run.
func TestOpeningCreatesTheSchema(t *testing.T) {
	path := filepath.Join(t.TempDir(), "nested", "luna.db")

	s, err := Open(path)
	if err != nil {
		t.Fatalf("opening a fresh path: %v", err)
	}
	defer func() { _ = s.Close() }()

	if err := s.Append("LUNA-1", Event{Action: "Advance"}); err != nil {
		t.Errorf("a fresh store must be usable immediately: %v", err)
	}
}

// TestReopeningKeepsWhatWasThere covers the idempotence of Open.
func TestReopeningKeepsWhatWasThere(t *testing.T) {
	path := filepath.Join(t.TempDir(), "luna.db")

	first, err := Open(path)
	if err != nil {
		t.Fatalf("opening: %v", err)
	}
	if err := first.Append("LUNA-1", Event{Action: "Advance"}); err != nil {
		t.Fatalf("appending: %v", err)
	}
	if err := first.Close(); err != nil {
		t.Fatalf("closing: %v", err)
	}

	second, err := Open(path)
	if err != nil {
		t.Fatalf("reopening: %v", err)
	}
	defer func() { _ = second.Close() }()

	events, err := second.Events("LUNA-1")
	if err != nil {
		t.Fatalf("reading: %v", err)
	}
	if len(events) != 1 {
		t.Errorf("reopening must not wipe the log, got %d events", len(events))
	}
}
