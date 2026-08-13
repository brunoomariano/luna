package store

import (
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/brunoomariano/luna/src/internal/fsm"
)

// clockedStore is a store whose log can be placed in time without any test
// sleeping. A watchdog measured against a real clock either takes hours to test
// or is tested with a patience so short it proves nothing.
func clockedStore(t *testing.T) (*Store, *time.Time) {
	t.Helper()

	s := openTemp(t)
	at := time.Date(2026, 8, 12, 9, 0, 0, 0, time.UTC)
	s.Now = func() time.Time { return at }
	return s, &at
}

// blockedTask opens a task and blocks it, leaving the log stamped at the
// store's current time.
func blockedTask(t *testing.T, s *Store, id, reason string) {
	t.Helper()

	if err := s.AppendAction(id, fsm.TaskCreated{Kind: fsm.KindFeature, Profile: fsm.ProfileNightly}); err != nil {
		t.Fatalf("opening %s: %v", id, err)
	}
	if err := s.AppendAction(id, fsm.Block{Reason: reason}); err != nil {
		t.Fatalf("blocking %s: %v", id, err)
	}
}

// TestAFreshBlockIsNotYetStuck is the first half of the watchdog being useful.
// Everything blocked is not an alert; something blocked and unattended is.
func TestAFreshBlockIsNotYetStuck(t *testing.T) {
	s, now := clockedStore(t)
	blockedTask(t, s, "LUNA-1", "merge conflict on runner.go")

	*now = now.Add(5 * time.Minute)

	stuck, err := s.Stalled(fsm.DefaultFlow(), time.Hour)
	if err != nil {
		t.Fatalf("Stalled: %v", err)
	}
	if len(stuck) != 0 {
		t.Errorf("a five-minute-old block was reported as stuck: %v", stuck)
	}
}

// TestABlockNobodyAnsweredBecomesStuck is the failure this whole phase exists
// for. swarm-forge's own bugs.md records a multi-hour stall where the dashboard
// never said why — nothing waits for hours with nobody knowing.
func TestABlockNobodyAnsweredBecomesStuck(t *testing.T) {
	s, now := clockedStore(t)
	blockedTask(t, s, "LUNA-1", "merge conflict on runner.go")

	*now = now.Add(3 * time.Hour)

	stuck, err := s.Stalled(fsm.DefaultFlow(), time.Hour)
	if err != nil {
		t.Fatalf("Stalled: %v", err)
	}
	if len(stuck) != 1 {
		t.Fatalf("got %d stuck tasks, want 1", len(stuck))
	}

	got := stuck[0]
	if got.TaskID != "LUNA-1" {
		t.Errorf("task = %q, want LUNA-1", got.TaskID)
	}
	if got.Status != fsm.StatusBlocked {
		t.Errorf("status = %q, want blocked", got.Status)
	}
	if got.Since != 3*time.Hour {
		t.Errorf("since = %s, want 3h", got.Since)
	}
	// The reason has to survive to the alert. "A task is stuck" sends someone
	// looking; "merge conflict on runner.go" tells them where to look.
	if !strings.Contains(got.Reason, "runner.go") {
		t.Errorf("reason = %q, want the recorded block", got.Reason)
	}
	if !strings.Contains(got.String(), "3h") {
		t.Errorf("the notification does not lead with the duration: %q", got.String())
	}
}

// TestAGateNobodyAnsweredIsAlsoStuck covers the planned pause. A gate is not a
// failure, but a gate nobody answers for six hours is indistinguishable from one
// to whoever is waiting behind it.
func TestAGateNobodyAnsweredIsAlsoStuck(t *testing.T) {
	s, now := clockedStore(t)

	if err := s.AppendAction("LUNA-1", fsm.TaskCreated{Kind: fsm.KindFeature, Profile: fsm.ProfileInteractive}); err != nil {
		t.Fatalf("opening the task: %v", err)
	}
	if err := s.AppendAction("LUNA-1", fsm.Advance{
		Flow:         fsm.DefaultFlow(),
		GateDecision: fsm.GateDecisionWaited,
	}); err != nil {
		t.Fatalf("advancing to the gate: %v", err)
	}

	state, err := s.Replay("LUNA-1", fsm.DefaultFlow())
	if err != nil {
		t.Fatalf("replay: %v", err)
	}
	if state.Status != fsm.StatusAwaitingGate {
		t.Skipf("the first stage did not open a gate (status %q); this test needs one", state.Status)
	}

	*now = now.Add(6 * time.Hour)

	stuck, err := s.Stalled(fsm.DefaultFlow(), time.Hour)
	if err != nil {
		t.Fatalf("Stalled: %v", err)
	}
	if len(stuck) != 1 {
		t.Fatalf("got %d stuck tasks, want the unanswered gate", len(stuck))
	}
	if stuck[0].Status != fsm.StatusAwaitingGate {
		t.Errorf("status = %q, want awaiting_gate", stuck[0].Status)
	}
}

// TestATaskThatIsMovingIsNeverStuck is the control. A watchdog that reports
// healthy work is a watchdog people turn off.
func TestATaskThatIsMovingIsNeverStuck(t *testing.T) {
	s, now := clockedStore(t)

	if err := s.AppendAction("LUNA-1", fsm.TaskCreated{Kind: fsm.KindFeature, Profile: fsm.ProfileNightly}); err != nil {
		t.Fatalf("opening the task: %v", err)
	}
	*now = now.Add(9 * time.Hour)

	stuck, err := s.Stalled(fsm.DefaultFlow(), time.Hour)
	if err != nil {
		t.Fatalf("Stalled: %v", err)
	}
	if len(stuck) != 0 {
		t.Errorf("a task that is not stopped was reported stuck: %v", stuck)
	}
}

// TestTheClockRunsFromTheLastEvent is what keeps the alert honest. A task
// blocked, cleared, and blocked again ten minutes ago has been stuck for ten
// minutes — not since the first block.
func TestTheClockRunsFromTheLastEvent(t *testing.T) {
	s, now := clockedStore(t)
	blockedTask(t, s, "LUNA-1", "first block")

	*now = now.Add(5 * time.Hour)
	if err := s.AppendAction("LUNA-1", fsm.Unblock{}); err != nil {
		t.Fatalf("unblocking: %v", err)
	}

	*now = now.Add(10 * time.Minute)
	if err := s.AppendAction("LUNA-1", fsm.Block{Reason: "second block"}); err != nil {
		t.Fatalf("blocking again: %v", err)
	}

	*now = now.Add(20 * time.Minute)

	stuck, err := s.Stalled(fsm.DefaultFlow(), time.Hour)
	if err != nil {
		t.Fatalf("Stalled: %v", err)
	}
	if len(stuck) != 0 {
		t.Errorf("the age was measured from the first block rather than the last "+
			"event: %v", stuck)
	}

	// And with a patience it does exceed, it reports the shorter age.
	stuck, _ = s.Stalled(fsm.DefaultFlow(), 10*time.Minute)
	if len(stuck) != 1 {
		t.Fatalf("got %d, want the task stuck for 20 minutes", len(stuck))
	}
	if stuck[0].Since != 20*time.Minute {
		t.Errorf("since = %s, want 20m — measured from the second block", stuck[0].Since)
	}
}

// TestALogWithoutTimestampsIsNotReportedStuck covers the upgrade. An event
// written before the column existed has an age of zero, and treating that as the
// epoch would report every old task as stuck for fifty-six years — which is how
// a watchdog teaches people to ignore it.
func TestALogWithoutTimestampsIsNotReportedStuck(t *testing.T) {
	s, now := clockedStore(t)
	blockedTask(t, s, "LUNA-1", "blocked before the upgrade")

	// Exactly what an old log looks like: rows with the column's default.
	if _, err := s.db.Exec(`UPDATE events SET at = 0 WHERE task_id = ?`, "LUNA-1"); err != nil {
		t.Fatalf("ageing the log: %v", err)
	}
	*now = now.Add(50 * time.Hour)

	stuck, err := s.Stalled(fsm.DefaultFlow(), time.Hour)
	if err != nil {
		t.Fatalf("Stalled: %v", err)
	}
	if len(stuck) != 0 {
		t.Errorf("a log with no timestamps was reported stuck: %v", stuck)
	}
}

// TestZeroPatienceListsEverythingStopped is the `--all` case: no judgement about
// how long, just what is currently waiting on a person.
func TestZeroPatienceListsEverythingStopped(t *testing.T) {
	s, now := clockedStore(t)
	blockedTask(t, s, "LUNA-1", "one")
	blockedTask(t, s, "LUNA-2", "two")

	*now = now.Add(time.Second)

	stuck, err := s.Stalled(fsm.DefaultFlow(), 0)
	if err != nil {
		t.Fatalf("Stalled: %v", err)
	}
	if len(stuck) != 2 {
		t.Errorf("got %d, want both stopped tasks", len(stuck))
	}
}

// TestAGateWithNoRecordedReasonStillReportsSomething. A stuck task whose reason
// is blank tells whoever reads the alert nothing at all, which is the silent
// stop INV-core-8 exists against.
func TestAGateWithNoRecordedReasonStillReportsSomething(t *testing.T) {
	s, _ := clockedStore(t)

	state := fsm.NewTaskState("LUNA-1", fsm.KindFeature)
	state.Status = fsm.StatusAwaitingGate
	state.Gate = &fsm.PendingGate{Kind: fsm.GateConfirm, Stage: "build"}

	if got := whyStopped(state); got == "" {
		t.Error("a gate with no reason produced an empty explanation")
	}
	_ = s
}

// TestADurationIsRoundedForReading covers the three shapes a person sees.
// "3h12m" is a duration; "3h12m7.4381s" is a number nobody finishes reading.
func TestADurationIsRoundedForReading(t *testing.T) {
	for _, tc := range []struct {
		in   time.Duration
		want string
	}{
		{3*time.Hour + 12*time.Minute + 7400*time.Millisecond, "3h12m0s"},
		{12*time.Minute + 7400*time.Millisecond, "12m7s"},
		{7400 * time.Millisecond, "7s"},
	} {
		if got := round(tc.in).String(); got != tc.want {
			t.Errorf("round(%s) = %s, want %s", tc.in, got, tc.want)
		}
	}
}

// TestAStoreReopensWithoutLosingItsColumns is the upgrade path. `CREATE TABLE IF
// NOT EXISTS` does nothing once the table is there, so a column added later
// arrives through the migration or not at all.
func TestAStoreReopensWithoutLosingItsColumns(t *testing.T) {
	path := filepath.Join(t.TempDir(), "luna.db")

	first, err := OpenAs(path, LunaOwnsTheLog)
	if err != nil {
		t.Fatalf("opening: %v", err)
	}
	at := time.Date(2026, 8, 12, 9, 0, 0, 0, time.UTC)
	first.Now = func() time.Time { return at }

	if err := first.AppendAction("LUNA-1", fsm.TaskCreated{Kind: fsm.KindFeature}); err != nil {
		t.Fatalf("appending: %v", err)
	}
	if err := first.Close(); err != nil {
		t.Fatalf("closing: %v", err)
	}

	// Reopening runs the migration a second time. It has to be a no-op rather
	// than an error: this is every run after the first.
	second, err := OpenAs(path, LunaOwnsTheLog)
	if err != nil {
		t.Fatalf("reopening a store that is already current: %v", err)
	}
	t.Cleanup(func() { _ = second.Close() })

	got, err := second.lastEventAt("LUNA-1")
	if err != nil {
		t.Fatalf("lastEventAt: %v", err)
	}
	if !got.Equal(at) {
		t.Errorf("the timestamp did not survive the reopen: %s", got)
	}
}

// TestATaskWrittenUnderAnotherFlowDoesNotHideTheRest is the same reasoning
// AwaitingGate uses: the listing exists so nothing waits unseen, and one
// unreadable task must not take every other one with it. `luna flow check` is
// where those surface by name.
func TestATaskWrittenUnderAnotherFlowDoesNotHideTheRest(t *testing.T) {
	s, now := clockedStore(t)
	blockedTask(t, s, "LUNA-1", "readable")
	blockedTask(t, s, "LUNA-2", "written under another flow")

	// A different flow than the one LUNA-2's opening event recorded.
	other := []fsm.Stage{{ID: "only", Requires: []fsm.Artifact{fsm.TaskID}, Produces: []fsm.Artifact{"a"}}}
	if err := s.AppendAction("LUNA-3", fsm.TaskCreated{
		Kind:    fsm.KindFeature,
		Profile: fsm.ProfileNightly,
		Flow:    fsm.Fingerprint(other),
	}); err != nil {
		t.Fatalf("opening LUNA-3: %v", err)
	}

	*now = now.Add(3 * time.Hour)

	stuck, err := s.Stalled(fsm.DefaultFlow(), time.Hour)
	if err != nil {
		t.Fatalf("one unreadable task made the whole listing fail: %v", err)
	}
	if len(stuck) != 2 {
		t.Errorf("got %d stuck tasks, want the two readable ones: %v", len(stuck), stuck)
	}
}

// TestABlockedTaskWithNoRecordedReasonStillReports. The reducer refuses a block
// with no reason, so this can only arrive from a log written by hand or by an
// older build — and a stuck task the alert cannot explain is still worth saying
// out loud.
func TestABlockedTaskWithNoRecordedReasonStillReports(t *testing.T) {
	state := fsm.TaskState{ID: "LUNA-1", Status: fsm.StatusBlocked}

	if got := whyStopped(state); got == "" {
		t.Error("a blocked task with no recorded reason produced an empty explanation")
	}
}

// TestTheAgeIsReadFromTheLogNotTheState is the boundary the watchdog lives on.
// lastEventAt is the only clock reader in the path, and a task nobody has
// written to has no age rather than an age of zero seconds ago.
func TestTheAgeIsReadFromTheLogNotTheState(t *testing.T) {
	s, _ := clockedStore(t)

	at, err := s.lastEventAt("NEVER-WRITTEN")
	if err != nil {
		t.Fatalf("lastEventAt on an unknown task: %v", err)
	}
	if !at.IsZero() {
		t.Errorf("a task with no log reported an age: %s", at)
	}
}

// TestTheLogRecordsWhenItWasWritten is the mechanism the rest depends on. It is
// asserted directly so that a regression here fails as itself rather than as
// six confusing watchdog failures.
func TestTheLogRecordsWhenItWasWritten(t *testing.T) {
	s, now := clockedStore(t)
	blockedTask(t, s, "LUNA-1", "blocked")

	at, err := s.lastEventAt("LUNA-1")
	if err != nil {
		t.Fatalf("lastEventAt: %v", err)
	}
	if !at.Equal(*now) {
		t.Errorf("the log says %s, the clock said %s", at, *now)
	}
}

// TestTheTimestampNeverReachesTheState is the boundary ADR-0024 draws. The
// reducer is pure, and a state that carried the clock would make a replay depend
// on when it ran.
func TestTheTimestampNeverReachesTheState(t *testing.T) {
	s, now := clockedStore(t)
	blockedTask(t, s, "LUNA-1", "blocked")

	first, err := s.Replay("LUNA-1", fsm.DefaultFlow())
	if err != nil {
		t.Fatalf("replay: %v", err)
	}

	// Replaying the same log much later must produce exactly the same state.
	*now = now.Add(100 * time.Hour)
	second, err := s.Replay("LUNA-1", fsm.DefaultFlow())
	if err != nil {
		t.Fatalf("replay: %v", err)
	}

	if first.Status != second.Status || first.Blocked != second.Blocked || first.Seq != second.Seq {
		t.Errorf("replaying the same log at a different time gave a different state:\n%+v\n%+v",
			first, second)
	}
}
