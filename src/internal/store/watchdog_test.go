package store

import (
	"strings"
	"testing"
	"time"

	"github.com/brunoomariano/luna/src/internal/fsm"
)

// TestOneUnreadableTaskDoesNotSilenceTheWatchdog is INV-5 on the path that has
// nobody watching it.
//
// `luna gates` already refuses to let a task born under a retired flow hide the
// others. The watchdog is the same listing with a clock attached, and it is the
// one that matters more: a gate listing is read by a person who is already
// looking, while the watchdog is what tells them to look. A single stale task
// making `Stalled` return an error would turn the unattended net into silence,
// and silence is what a healthy night looks like.
func TestOneUnreadableTaskDoesNotSilenceTheWatchdog(t *testing.T) {
	s, now := clockedStore(t)

	// A task born under a flow this build no longer has: it cannot be replayed.
	retired := []fsm.Stage{{ID: "gone", Requires: []fsm.Artifact{fsm.TaskID}, Produces: []fsm.Artifact{"x"}}}
	if err := s.AppendAction("LUNA-1", fsm.TaskCreated{
		Kind: fsm.KindChore, Flow: fsm.Fingerprint(retired),
	}); err != nil {
		t.Fatalf("creating the stale task: %v", err)
	}

	blockedTask(t, s, "LUNA-2", "merge conflict on runner.go")
	*now = now.Add(6 * time.Hour)

	stuck, err := s.Stalled(fsm.DefaultFlow(), time.Hour)
	if err != nil {
		t.Fatalf("one unreadable task must not fail the watchdog: %v", err)
	}
	if len(stuck) != 1 || stuck[0].TaskID != "LUNA-2" {
		t.Errorf("want the readable stall still reported, got %+v", stuck)
	}
}

// TestACorruptLogStopsTheWatchdogRatherThanBeingSkipped is the other half of
// the line above, and the two must not be confused.
//
// A retired flow is a known, expected condition — `luna flow check` names those
// tasks, so skipping them is right. A log holding an action this build cannot
// decode is not: it means the log is damaged or written by something else, and
// the watchdog quietly walking past it would hide a task that can never be
// replayed again, forever, with nothing anywhere saying so.
func TestACorruptLogStopsTheWatchdogRatherThanBeingSkipped(t *testing.T) {
	s, _ := clockedStore(t)

	if err := s.Append("LUNA-1", Event{Action: "TimeTravel"}); err != nil {
		t.Fatalf("seeding: %v", err)
	}

	if _, err := s.Stalled(fsm.DefaultFlow(), time.Hour); err == nil {
		t.Error("a log that cannot be decoded must not be skipped in silence")
	}
}

// TestTheWatchdogFailsLoudlyWhenTheLogIsGone covers the store itself being
// unreachable — a deleted file, a full disk, a handle already closed.
//
// Returning an empty list would be the worst possible answer: nothing stuck is
// exactly what a healthy night reports, so a broken watchdog and a quiet one
// would be indistinguishable to whatever polls it.
func TestTheWatchdogFailsLoudlyWhenTheLogIsGone(t *testing.T) {
	s, _ := clockedStore(t)
	blockedTask(t, s, "LUNA-1", "merge conflict on runner.go")

	if err := s.Close(); err != nil {
		t.Fatalf("closing: %v", err)
	}

	stuck, err := s.Stalled(fsm.DefaultFlow(), 0)
	if err == nil {
		t.Fatalf("an unreadable store reported %d stalls instead of failing", len(stuck))
	}
}

// TestTheStallReportsWhatTheGateIsAsking is what makes an alert actionable.
//
// A blocked task carries its own reason, and that path is covered. A task
// waiting at a gate does not: the reason lives on the gate, and reading it from
// there is the difference between "LUNA-1 has been awaiting_gate for 6h" and a
// line that says which question nobody answered. The first sends the reader back
// to `luna task show`; the second is the alert doing its job.
func TestTheStallReportsWhatTheGateIsAsking(t *testing.T) {
	state := fsm.TaskState{
		ID:     "LUNA-1",
		Status: fsm.StatusAwaitingGate,
		Gate:   &fsm.PendingGate{Stage: "plan", Reason: "does this plan match what was asked?"},
	}

	if got := whyStopped(state); got != "does this plan match what was asked?" {
		t.Errorf("the stall does not carry the gate's question, got %q", got)
	}
}

// TestABlockOutranksAnOpenGateInTheReport covers the order of the two reasons.
//
// A task can hold both: a gate opened, and then something broke. The block is
// the newer fact and the one somebody has to act on — reporting the gate's
// question instead would send them to approve an artifact for a task that
// cannot move whatever they decide.
func TestABlockOutranksAnOpenGateInTheReport(t *testing.T) {
	state := fsm.TaskState{
		ID:      "LUNA-1",
		Status:  fsm.StatusBlocked,
		Blocked: "the sandbox is not installed",
		Gate:    &fsm.PendingGate{Stage: "plan", Reason: "does this plan match what was asked?"},
	}

	if got := whyStopped(state); got != "the sandbox is not installed" {
		t.Errorf("want the block reported over the open gate, got %q", got)
	}
}

// TestOnlyLunaSaysWhyItRefusedTheWrite is the message a person actually meets.
//
// The refusal is enforced and asserted everywhere; what nothing asserted is what
// it says. An agent that appends gets this sentence and nothing else, so if it
// degrades to "permission denied" the agent has no way to learn that reporting
// through `luna done` is the route it was supposed to take — and it will retry
// the append.
func TestOnlyLunaSaysWhyItRefusedTheWrite(t *testing.T) {
	got := ErrNotTheOwner.Error()

	for _, want := range []string{"only Luna writes the log", "luna done"} {
		if !strings.Contains(got, want) {
			t.Errorf("the refusal does not say %q, got %q", want, got)
		}
	}
}
