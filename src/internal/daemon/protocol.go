// Package daemon is the one process that writes the log.
//
// The ownership rule was a constant — `LunaOwnsTheLog`, checked at the store's
// single append — and a constant is enforcement only against code that respects
// it. This makes it a process boundary: the daemon opens the store for writing,
// everything else opens it read-only, and an append travels over a socket the
// daemon owns.
//
// What that buys is not tidiness. The log moved out of the checkout, so it lives
// under `$HOME` — and ai-jail gives a contained process a tmpfs `$HOME`. A `luna`
// run inside a jail would create a fresh log there, answer every read from it,
// and keep none of it: the measured failure where a command reported success and
// the task never existed. The daemon's socket is deliberately outside the one
// directory a jail is given, so a contained process cannot reach it — and failing
// to connect is loud, where writing to a tmpfs is silent.
package daemon

import (
	"encoding/json"
	"fmt"

	"github.com/brunoomariano/luna/src/internal/store"
)

// Request is one call to the daemon.
//
// JSON over a unix socket, one request and one response per connection — the same
// shape the handover socket uses, so there is one protocol to learn rather than
// two.
type Request struct {
	// Op is what to do: append, put_blob, forget_blobs, import, tasks, ping.
	Op string `json:"op"`

	// Project scopes a task inside the one central log.
	Project string `json:"project,omitempty"`

	// TaskID and Action are the append itself. Action is the encoded action, in
	// the form the store already writes.
	TaskID string `json:"task_id,omitempty"`
	Action string `json:"action,omitempty"`

	// Payload is the action's JSON body.
	Payload string `json:"payload,omitempty"`

	// After makes the append conditional on the log still ending there, which is
	// what stops two decisions taken from one state. Negative is unconditional.
	After int `json:"after"`

	Blob *store.Blob `json:"blob,omitempty"`

	// Legacy is a former per-project store the daemon should import and archive.
	Legacy string `json:"legacy,omitempty"`
}

// Response is what comes back. An empty Err is success.
type Response struct {
	Err string `json:"err,omitempty"`

	// Tasks is what "tasks" answers: every task in the central database.
	Tasks []TaskLine `json:"tasks,omitempty"`
}

// TaskLine is one task in the global listing — the central view the daemon exists
// to make possible.
type TaskLine struct {
	Project string `json:"project"`
	TaskID  string `json:"task_id"`
	Stage   string `json:"stage"`
	Status  string `json:"status"`
}

// encode writes one message and a newline, which is the frame.
func encode(w interface{ Write([]byte) (int, error) }, v any) error {
	body, err := json.Marshal(v)
	if err != nil {
		return fmt.Errorf("encoding: %w", err)
	}
	if _, err := w.Write(append(body, '\n')); err != nil {
		return fmt.Errorf("writing: %w", err)
	}
	return nil
}
