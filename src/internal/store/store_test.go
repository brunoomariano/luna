package store

import (
	"errors"
	"fmt"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"github.com/brunoomariano/luna/src/internal/fsm"
)

// openTemp gives each test its own database file, closed when the test ends.
// A file rather than :memory: on purpose — the property under test is surviving
// a restart, and an in-memory database cannot demonstrate that.
func openTemp(t *testing.T) *Store {
	t.Helper()

	path := filepath.Join(t.TempDir(), "luna.db")
	s, err := OpenAs(path, LunaOwnsTheLog)
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

// TestTheStoreExposesNoWayToRewriteHistory covers scenario K4 — INV-2.
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
// state comes back identical. Nothing was ever only in memory.
func TestStateIsRebuiltFromTheLog(t *testing.T) {
	path := filepath.Join(t.TempDir(), "luna.db")

	// First process: drive a task partway through the flow.
	first, err := OpenAs(path, LunaOwnsTheLog)
	if err != nil {
		t.Fatalf("opening: %v", err)
	}

	live := fsm.NewTaskState("LUNA-1", fsm.KindFeature)
	for _, action := range []fsm.Action{
		fsm.Advance{Flow: fsm.DefaultFlow()},
		fsm.Complete{
			Delivered: []fsm.Artifact{"worktree", "setup_report", "bootstrap_command"},
			Evidence: map[fsm.Artifact]fsm.Evidence{
				"worktree":          fsm.Exists(0),
				"setup_report":      fsm.Exists(0),
				"bootstrap_command": fsm.Exists(0),
			},
		},
		fsm.GateApprove{},
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
	second, err := OpenAs(path, LunaOwnsTheLog)
	if err != nil {
		t.Fatalf("reopening: %v", err)
	}
	defer func() { _ = second.Close() }()

	replayed, err := second.Replay("LUNA-1", fsm.DefaultFlow())
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

	state, err := s.Replay("LUNA-1", fsm.DefaultFlow())
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
// Replaying the same log twice gives the same state. This is what keeping the
// reducer pure buys: if a transition could run a test suite or read a
// clock, the second replay could disagree with the first.
func TestReplayIsDeterministic(t *testing.T) {
	s := openTemp(t)

	for _, action := range []fsm.Action{
		fsm.Advance{Flow: fsm.DefaultFlow()},
	} {
		if err := s.AppendAction("LUNA-1", action); err != nil {
			t.Fatalf("appending: %v", err)
		}
	}

	once, err := s.Replay("LUNA-1", fsm.DefaultFlow())
	if err != nil {
		t.Fatalf("first replay: %v", err)
	}
	twice, err := s.Replay("LUNA-1", fsm.DefaultFlow())
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

	_, err := s.Replay("LUNA-1", fsm.DefaultFlow())

	if !errors.Is(err, ErrUnknownAction) {
		t.Errorf("want ErrUnknownAction, got %v", err)
	}
}

// ── block M: the content store ───────────────────────────────────────────────

// ── block N: finding what needs a human ──────────────────────────────────────

// TestSuspendedTasksAreListable covers scenario N1 — INV-5.
//
// A task waiting at a gate has released its slot, so nothing is running to remind
// anyone it exists. If it were not discoverable by a query, it would wait
// forever — the second form of silent failure.
func TestSuspendedTasksAreListable(t *testing.T) {
	s := openTemp(t)

	// One task stops at a gate; another runs past it. Reaching a gate takes a walk
	// now: the shipped flow starts at the mechanical `setup`, which opens none
	// (integration left Luna's scope, removing `commit`, and `discovery` went
	// with it).
	walkToGate(t, s, "waiting")
	for _, action := range []fsm.Action{
		fsm.Advance{Flow: fsm.DefaultFlow()},
	} {
		if err := s.AppendAction("running", action); err != nil {
			t.Fatalf("appending: %v", err)
		}
	}

	waiting, err := s.AwaitingGate()
	if err != nil {
		t.Fatalf("listing: %v", err)
	}

	if len(waiting) != 1 {
		t.Fatalf("want the one suspended task, got %d (%v)", len(waiting), waiting)
	}
	if waiting[0].TaskID != "waiting" {
		t.Errorf("want the task that stopped at a gate, got %q", waiting[0].TaskID)
	}
	// `setup` is where the trail first stops: the sandbox and the workstream are
	// confirmed before anything is paid for.
	if waiting[0].Stage != "setup" {
		t.Errorf("the listing says where it stopped; got %q", waiting[0].Stage)
	}
}

// TestListingTasksWithNothingSuspended covers scenario N2.
//
// Nothing waiting is a normal answer, not an empty-state bug.
func TestListingTasksWithNothingSuspended(t *testing.T) {
	s := openTemp(t)

	waiting, err := s.AwaitingGate()
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

	s, err := OpenAs(path, LunaOwnsTheLog)
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

	first, err := OpenAs(path, LunaOwnsTheLog)
	if err != nil {
		t.Fatalf("opening: %v", err)
	}
	if err := first.Append("LUNA-1", Event{Action: "Advance"}); err != nil {
		t.Fatalf("appending: %v", err)
	}
	if err := first.Close(); err != nil {
		t.Fatalf("closing: %v", err)
	}

	second, err := OpenAs(path, LunaOwnsTheLog)
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

// TestReplayReadsTheKindFromTheLog covers the reason TaskCreated exists.
//
// The kind is produced by intake and lives in the history. Asking a caller to
// supply it alongside would let the two disagree — and `luna gates`, listing
// tasks of several kinds at once, would have no single answer to give.
func TestReplayReadsTheKindFromTheLog(t *testing.T) {
	s := openTemp(t)

	if err := s.AppendAction("LUNA-1", fsm.TaskCreated{
		Kind:    fsm.KindBug,
		Profile: fsm.ProfileNightly,
		Flow:    fsm.Fingerprint(fsm.DefaultFlow()),
	}); err != nil {
		t.Fatalf("appending: %v", err)
	}

	state, err := s.Replay("LUNA-1", fsm.DefaultFlow())
	if err != nil {
		t.Fatalf("replaying: %v", err)
	}

	if state.Context.Kind != fsm.KindBug {
		t.Errorf("the kind comes out of the log, got %q", state.Context.Kind)
	}
	if state.Profile != fsm.ProfileNightly {
		t.Errorf("so does the profile, got %q", state.Profile)
	}
}

// TestTasksOfDifferentKindsCoexist covers what the old signature made impossible.
//
// One store, two tasks, two kinds — and a single listing that gets both right.
func TestTasksOfDifferentKindsCoexist(t *testing.T) {
	s := openTemp(t)

	if err := s.AppendAction("a-bug", fsm.TaskCreated{Kind: fsm.KindBug, Flow: fsm.Fingerprint(fsm.DefaultFlow())}); err != nil {
		t.Fatalf("appending: %v", err)
	}
	if err := s.AppendAction("a-chore", fsm.TaskCreated{Kind: fsm.KindChore, Flow: fsm.Fingerprint(fsm.DefaultFlow())}); err != nil {
		t.Fatalf("appending: %v", err)
	}

	bug, err := s.Replay("a-bug", fsm.DefaultFlow())
	if err != nil {
		t.Fatalf("replaying the bug: %v", err)
	}
	chore, err := s.Replay("a-chore", fsm.DefaultFlow())
	if err != nil {
		t.Fatalf("replaying the chore: %v", err)
	}

	if bug.Context.Kind != fsm.KindBug || chore.Context.Kind != fsm.KindChore {
		t.Errorf("each task keeps its own kind: %q and %q", bug.Context.Kind, chore.Context.Kind)
	}
}

// TestReplayRefusesALogWrittenUnderAnotherFlow is the whole point of the flow
// fingerprint.
//
// Before the fingerprint, a renamed stage replayed as the new name with no error
// at all, and a stage inserted mid-flow made a task re-run work it had already
// finished. Both were silent, and silence is the failure mode the log exists to
// prevent.
func TestReplayRefusesALogWrittenUnderAnotherFlow(t *testing.T) {
	s := openTemp(t)

	born := []fsm.Stage{
		{ID: "first", Requires: []fsm.Artifact{fsm.TaskID}, Produces: []fsm.Artifact{"a"}},
		{ID: "second", Requires: []fsm.Artifact{"a"}, Produces: []fsm.Artifact{"b"}},
	}
	if err := s.AppendAction("LUNA-1", fsm.TaskCreated{
		Kind: fsm.KindChore, Flow: fsm.Fingerprint(born),
	}); err != nil {
		t.Fatalf("creating: %v", err)
	}

	// The same flow it was born under: nothing to complain about.
	if _, err := s.Replay("LUNA-1", born); err != nil {
		t.Fatalf("replaying against its own flow: %v", err)
	}

	// The rename that used to pass in silence.
	renamed := []fsm.Stage{
		{ID: "first", Requires: []fsm.Artifact{fsm.TaskID}, Produces: []fsm.Artifact{"a"}},
		{ID: "second-v2", Requires: []fsm.Artifact{"a"}, Produces: []fsm.Artifact{"b"}},
	}
	_, err := s.Replay("LUNA-1", renamed)
	if !errors.Is(err, ErrFlowChanged) {
		t.Fatalf("want ErrFlowChanged when the flow changed underneath, got %v", err)
	}
	// The message has to name both sides: "it changed" without saying from what to
	// what leaves the reader to diff two builds by hand.
	if !strings.Contains(err.Error(), string(fsm.Fingerprint(born))) ||
		!strings.Contains(err.Error(), string(fsm.Fingerprint(renamed))) {
		t.Errorf("the refusal should name both fingerprints, got %q", err)
	}
}

// TestATaskStampedWithNoFlowDoesNotReplayAgainstOne covers what an unstamped
// opening event means now.
//
// It used to replay against anything, so that a log written before the
// fingerprint existed kept working. Nothing writes such a log — `luna task new`
// stamps every task with the flow it was born under — so the permissive reading
// had no case left to serve except the one it should refuse: a task opened
// against no flow, replayed against a real one, is the silent mismatch the
// fingerprint exists to catch.
func TestATaskStampedWithNoFlowDoesNotReplayAgainstOne(t *testing.T) {
	s := openTemp(t)

	if err := s.AppendAction("LUNA-1", fsm.TaskCreated{Kind: fsm.KindFeature}); err != nil {
		t.Fatalf("creating: %v", err)
	}

	_, err := s.Replay("LUNA-1", fsm.DefaultFlow())

	if !errors.Is(err, ErrFlowChanged) {
		t.Fatalf("want ErrFlowChanged, got %v", err)
	}
}

// TestAnUnreadableTaskDoesNotHideTheOthers covers INV-5 under the flow fingerprint.
//
// A task whose flow changed cannot be read, and returning an error from the
// listing would let that one task hide every other task waiting on a person —
// which is the exact failure the invariant exists to prevent.
func TestAnUnreadableTaskDoesNotHideTheOthers(t *testing.T) {
	s := openTemp(t)

	// One task born under a flow that no longer exists.
	stale := []fsm.Stage{{ID: "gone", Requires: []fsm.Artifact{fsm.TaskID}, Produces: []fsm.Artifact{"x"}}}
	if err := s.AppendAction("LUNA-1", fsm.TaskCreated{Kind: fsm.KindChore, Flow: fsm.Fingerprint(stale)}); err != nil {
		t.Fatalf("creating the stale task: %v", err)
	}

	// One healthy task, waiting at a gate.
	if err := s.AppendAction("LUNA-2", fsm.TaskCreated{
		Kind: fsm.KindFeature, Flow: fsm.Fingerprint(fsm.DefaultFlow()),
	}); err != nil {
		t.Fatalf("creating the healthy task: %v", err)
	}
	walkToGate(t, s, "LUNA-2")

	waiting, err := s.AwaitingGate()
	if err != nil {
		t.Fatalf("one unreadable task must not fail the listing: %v", err)
	}
	if len(waiting) != 1 || waiting[0].TaskID != "LUNA-2" {
		t.Errorf("want the healthy task still listed, got %+v", waiting)
	}
}

// TestAConditionalAppendRefusesAStaleDecision is why an append declares the
// position it read from.
//
// Two writers replay the same state, both validate the same action against it,
// and both try to write. Before this, the second one landed: the log ended up
// holding two decisions taken from one state, and replaying it produced an
// illegal transition forever — with no repair possible, because the store has no
// UPDATE and no DELETE.
func TestAConditionalAppendRefusesAStaleDecision(t *testing.T) {
	s := openTemp(t)

	if err := s.AppendAction("LUNA-1", fsm.TaskCreated{Kind: fsm.KindChore, Flow: fsm.Fingerprint(fsm.DefaultFlow())}); err != nil {
		t.Fatalf("creating: %v", err)
	}

	// Both read the same state. This is the window: everything after here was
	// decided against a log that says the task is at `seq`.
	first, err := s.Replay("LUNA-1", fsm.DefaultFlow())
	if err != nil {
		t.Fatalf("replaying: %v", err)
	}
	second := first

	if err := s.AppendActionAt("LUNA-1", first.Seq, fsm.Advance{Flow: fsm.DefaultFlow()}); err != nil {
		t.Fatalf("the first writer must win: %v", err)
	}

	err = s.AppendActionAt("LUNA-1", second.Seq, fsm.Advance{Flow: fsm.DefaultFlow()})
	if !errors.Is(err, ErrConcurrentWrite) {
		t.Fatalf("want ErrConcurrentWrite for a decision made against a moved task, got %v", err)
	}
	// The message has to say where it was and where it is, or the reader cannot
	// tell a stale decision from a corrupt one.
	if !strings.Contains(err.Error(), "LUNA-1") {
		t.Errorf("the refusal should name the task, got %q", err)
	}

	// The point of refusing: the log still replays.
	if _, err := s.Replay("LUNA-1", fsm.DefaultFlow()); err != nil {
		t.Errorf("refusing the second writer keeps the log readable, got %v", err)
	}
}

// TestAnAppendAtTheCurrentPositionLands is the other half: the check must not
// refuse the ordinary case, which is every append a single writer makes.
func TestAnAppendAtTheCurrentPositionLands(t *testing.T) {
	s := openTemp(t)

	if err := s.AppendAction("LUNA-1", fsm.TaskCreated{Kind: fsm.KindChore, Flow: fsm.Fingerprint(fsm.DefaultFlow())}); err != nil {
		t.Fatalf("creating: %v", err)
	}

	// A real sequence rather than the same action three times: each append reads
	// the log, decides, and writes, which is the loop the lead runs.
	for _, action := range []fsm.Action{
		fsm.Advance{Flow: fsm.DefaultFlow()}, // into setup, which is mechanical
		fsm.Abandon{Reason: "done proving the point"},
	} {
		state, err := s.Replay("LUNA-1", fsm.DefaultFlow())
		if err != nil {
			t.Fatalf("replaying: %v", err)
		}
		if err := s.AppendActionAt("LUNA-1", state.Seq, action); err != nil {
			t.Fatalf("an append from a fresh read must land: %v", err)
		}
	}

	events, err := s.Events("LUNA-1")
	if err != nil {
		t.Fatalf("reading: %v", err)
	}
	if len(events) != 3 {
		t.Errorf("want 3 events, got %d", len(events))
	}
}

// TestTheReducersSequenceIsTheLogPosition is the invariant the conditional append
// rests on.
//
// `state.Seq` counts transitions the reducer applied; `MAX(seq)` counts events in
// the log. They agree because every applied action is one event — and if they ever
// stopped agreeing, every conditional append would be refused against a position
// nobody could reach, so this is worth pinning rather than assuming.
func TestTheReducersSequenceIsTheLogPosition(t *testing.T) {
	s := openTemp(t)

	for _, action := range []fsm.Action{
		fsm.TaskCreated{Kind: fsm.KindChore, Flow: fsm.Fingerprint(fsm.DefaultFlow())},
		fsm.Advance{Flow: fsm.DefaultFlow()},
	} {
		if err := s.AppendAction("LUNA-1", action); err != nil {
			t.Fatalf("appending %T: %v", action, err)
		}
	}

	state, err := s.Replay("LUNA-1", fsm.DefaultFlow())
	if err != nil {
		t.Fatalf("replaying: %v", err)
	}
	events, err := s.Events("LUNA-1")
	if err != nil {
		t.Fatalf("reading: %v", err)
	}

	if state.Seq != events[len(events)-1].Seq {
		t.Errorf("the reducer is at %d and the log ends at %d — a conditional append "+
			"would never match", state.Seq, events[len(events)-1].Seq)
	}
}

// TestConcurrentAppendsAllLand covers the SQLITE_BUSY half of the conditional append.
//
// Without WAL, a busy timeout and BEGIN IMMEDIATE, this lost 19 of 20 appends —
// and each loss aborted a task without recording a block, which is the silent
// failure INV-5 forbids.
//
// Two stores over one file rather than one store used twice, because that is the
// real case: nothing stops a second `luna` from running against the same
// `.luna/luna.db`, and a single store's connection pool already serialises its
// own writers. Different tasks, because this is about contention on the file
// rather than ordering within one log.
func TestConcurrentAppendsAllLand(t *testing.T) {
	path := filepath.Join(t.TempDir(), "luna.db")
	first, err := OpenAs(path, LunaOwnsTheLog)
	if err != nil {
		t.Fatalf("opening: %v", err)
	}
	t.Cleanup(func() { _ = first.Close() })

	second, err := OpenAs(path, LunaOwnsTheLog)
	if err != nil {
		t.Fatalf("opening a second handle: %v", err)
	}
	t.Cleanup(func() { _ = second.Close() })

	const writers = 20
	var wg sync.WaitGroup
	errs := make(chan error, writers)

	for i := range writers {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			s := first
			if i%2 == 1 {
				s = second
			}
			id := fmt.Sprintf("LUNA-%d", i)
			if err := s.AppendAction(id, fsm.TaskCreated{Kind: fsm.KindChore, Flow: fsm.Fingerprint(fsm.DefaultFlow())}); err != nil {
				errs <- err
			}
		}(i)
	}
	wg.Wait()
	close(errs)

	for err := range errs {
		t.Errorf("a concurrent append must wait for the lock, not fail: %v", err)
	}

	ids, err := first.Tasks()
	if err != nil {
		t.Fatalf("listing: %v", err)
	}
	if len(ids) != writers {
		t.Errorf("want %d tasks, got %d", writers, len(ids))
	}
}

// TestAConditionalAppendRefusesAnActionItCannotEncode keeps the conditional path
// from being the one place an unknown action slips through.
//
// It has to fail before the transaction rather than inside it: an action the
// codec cannot write is a programming error, and a half-open write transaction
// waiting on a lock is a poor way to report one.
func TestAConditionalAppendRefusesAnActionItCannotEncode(t *testing.T) {
	s := openTemp(t)

	if err := s.AppendActionAt("LUNA-1", 0, nil); !errors.Is(err, ErrUnknownAction) {
		t.Errorf("want ErrUnknownAction, got %v", err)
	}
}

// walkToGate drives a task through the shipped flow until a stage opens a gate,
// and leaves it suspended there.
//
// It closes each stage on its way, delivering whatever that stage's contract
// asks for — the walk is scaffolding, and what the test is about is the task
// being discoverable once it stops.
func walkToGate(t *testing.T, s *Store, id string) {
	t.Helper()

	flow := fsm.DefaultFlow()
	for range flow {
		state, err := s.Replay(id, flow)
		if err != nil {
			t.Fatalf("walking %s: %v", id, err)
		}
		if state.Gate != nil {
			return
		}

		var action fsm.Action = fsm.Advance{Flow: flow, Gate: fsm.GateAccount{Decision: fsm.GateDecisionWaited}}
		if state.Status == fsm.StatusRunning {
			stage := fsm.Stage{ID: state.Stage}
			for _, candidate := range flow {
				if candidate.ID == state.Stage {
					stage = candidate
				}
			}
			owed := append(append([]fsm.Artifact{}, stage.Produces...), stage.ProducesForHuman...)
			evidence := map[fsm.Artifact]fsm.Evidence{}
			for _, a := range owed {
				evidence[a] = fsm.Evidence{Scope: fsm.VerifierFor(stage, a).Proves(), Verdict: fsm.VerdictPassed}
			}
			action = fsm.Complete{Delivered: owed, Evidence: evidence, Flow: flow}
		}
		if err := s.AppendAction(id, action); err != nil {
			t.Fatalf("walking %s: %v", id, err)
		}
	}
	t.Fatalf("no stage in the shipped flow opens a gate")
}

// TestFlowNameOfReadsTheOpeningEventAlone is the chicken-and-egg a build with
// several flows creates: replaying needs a flow, and which flow a task ran is in
// its log. This is the one read that happens before the replay.
func TestFlowNameOfReadsTheOpeningEventAlone(t *testing.T) {
	s := openTemp(t)

	if err := s.AppendAction("T-1", fsm.TaskCreated{
		Kind: fsm.KindBug, FlowName: "fix", Flow: "whatever",
	}); err != nil {
		t.Fatalf("creating the task: %v", err)
	}

	name, err := s.FlowNameOf("T-1")
	if err != nil {
		t.Fatalf("reading the flow name: %v", err)
	}
	if name != "fix" {
		t.Errorf("FlowNameOf = %q, want %q", name, "fix")
	}
}

// TestFlowNameOfDefaultsRatherThanFailing. The name only decides which flow gets
// loaded; the fingerprint comparison in Replay still decides whether that was the
// right one. So a log with nothing to read here answers with the default, and a
// wrong guess comes back as a refused replay rather than a wrong one.
func TestFlowNameOfDefaultsRatherThanFailing(t *testing.T) {
	s := openTemp(t)

	// A task nobody wrote to.
	name, err := s.FlowNameOf("T-absent")
	if err != nil {
		t.Fatalf("an empty log is not an error: %v", err)
	}
	if name != fsm.DefaultFlowName {
		t.Errorf("an empty log answered %q", name)
	}

	// A task opened before flows were named.
	if err := s.AppendAction("T-2", fsm.TaskCreated{Kind: fsm.KindChore}); err != nil {
		t.Fatalf("creating the task: %v", err)
	}
	if name, err = s.FlowNameOf("T-2"); err != nil || name != fsm.DefaultFlowName {
		t.Errorf("a task naming no flow answered %q (%v)", name, err)
	}
}

// TestReplayOwnFlowRefusesAFlowThisBuildLost. A task whose flow went away is the
// situation ErrFlowChanged exists for, so it comes back as that — which is what
// lets every listing that already skips those skip this one too, rather than one
// unreadable task hiding every other.
func TestReplayOwnFlowRefusesAFlowThisBuildLost(t *testing.T) {
	s := openTemp(t)

	if err := s.AppendAction("T-3", fsm.TaskCreated{
		Kind: fsm.KindFeature, FlowName: "a-flow-nobody-ships",
	}); err != nil {
		t.Fatalf("creating the task: %v", err)
	}

	_, err := s.ReplayOwnFlow("T-3")
	if !errors.Is(err, ErrFlowChanged) {
		t.Errorf("want ErrFlowChanged for a flow this build lost, got %v", err)
	}
}
