package store

import (
	"errors"
	"path/filepath"
	"testing"

	"github.com/brunoomariano/luna/src/internal/fsm"
)

// TestOnlyLunaWritesTheLog is the ownership rule, and it is the one that matters
// most of the three: an agent that appends does not corrupt a file, it fabricates
// history — and history is the audit trail.
func TestOnlyLunaWritesTheLog(t *testing.T) {
	path := filepath.Join(t.TempDir(), "luna.db")

	reader, err := Open(path)
	if err != nil {
		t.Fatalf("opening for reading: %v", err)
	}
	t.Cleanup(func() { _ = reader.Close() })

	err = reader.AppendAction("LUNA-1", fsm.TaskCreated{Kind: fsm.KindFeature, Flow: fsm.Fingerprint(fsm.DefaultFlow())})
	if !errors.Is(err, ErrNotTheOwner) {
		t.Fatalf("err = %v, want ErrNotTheOwner", err)
	}

	// And nothing was written: a refusal that still appended would be worse than
	// no refusal, because it would look enforced.
	events, err := reader.Events("LUNA-1")
	if err != nil {
		t.Fatalf("Events: %v", err)
	}
	if len(events) != 0 {
		t.Errorf("the refused append landed anyway: %d events", len(events))
	}
}

// TestEveryWriteGoesThroughTheOwnerCheck covers every entry point, because
// the rule is only worth having if a caller cannot route around it.
func TestEveryWriteGoesThroughTheOwnerCheck(t *testing.T) {
	path := filepath.Join(t.TempDir(), "luna.db")
	reader, err := Open(path)
	if err != nil {
		t.Fatalf("opening: %v", err)
	}
	t.Cleanup(func() { _ = reader.Close() })

	for name, write := range map[string]func() error{
		"Append":         func() error { return reader.Append("LUNA-1", Event{Action: "Advance"}) },
		"AppendAction":   func() error { return reader.AppendAction("LUNA-1", fsm.Unblock{}) },
		"AppendActionAt": func() error { return reader.AppendActionAt("LUNA-1", 0, fsm.Unblock{}) },
	} {
		t.Run(name, func(t *testing.T) {
			if err := write(); !errors.Is(err, ErrNotTheOwner) {
				t.Errorf("%s wrote without the owner: %v", name, err)
			}
		})
	}
}

// TestReadingNeedsNoOwner is the deliberate asymmetry. Replaying a task, listing
// what is blocked and checking a flow are safe, and gating them would make every
// read command claim an ownership it does not need.
func TestReadingNeedsNoOwner(t *testing.T) {
	path := filepath.Join(t.TempDir(), "luna.db")

	writer, err := OpenAs(path, LunaOwnsTheLog)
	if err != nil {
		t.Fatalf("opening for writing: %v", err)
	}
	if err := writer.AppendAction("LUNA-1", fsm.TaskCreated{Kind: fsm.KindFeature, Flow: fsm.Fingerprint(fsm.DefaultFlow())}); err != nil {
		t.Fatalf("seeding: %v", err)
	}
	if err := writer.Close(); err != nil {
		t.Fatalf("closing: %v", err)
	}

	reader, err := Open(path)
	if err != nil {
		t.Fatalf("opening for reading: %v", err)
	}
	t.Cleanup(func() { _ = reader.Close() })

	state, err := reader.Replay("LUNA-1", fsm.DefaultFlow())
	if err != nil {
		t.Fatalf("a reader could not replay: %v", err)
	}
	if state.ID != "LUNA-1" {
		t.Errorf("replayed %q, want LUNA-1", state.ID)
	}
}
