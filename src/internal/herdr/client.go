// Package herdr talks to a running herdr over its local socket.
//
// It is the only place in Luna that knows what a pane is. Everything above it
// speaks stages, artifacts and evidence; the translation happens here and nowhere
// else, which is what keeps leaving herdr a matter of writing another Node rather
// than a refactor (ADR-0030).
//
// The division it implements: Luna decides and verifies, herdr executes and shows
// (ADR-0027). Nothing here decides a transition. What comes back from the socket
// is observed fact, and turning fact into action is the lead's job.
package herdr

import (
	"bufio"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"os"
	"path/filepath"
	"strconv"
	"sync"
)

// ErrGone reports that herdr is not reachable, or stopped being reachable.
//
// It is a named error because losing herdr mid-stage is not a stage failure: the
// task blocks and a person decides what to do with the worktree (ADR-0033).
// Collapsing it into a generic I/O error would let the lead spend the retry
// budget on an infrastructure event.
var ErrGone = errors.New("herdr is not reachable")

// request is the envelope herdr expects: one JSON object per line.
//
// The id is a string, not a number. herdr refuses an integer outright
// ("invalid type: integer `1`, expected a string"), which is the kind of thing
// only a real server tells you — a fake would have accepted whatever it was
// handed.
type request struct {
	ID     string `json:"id"`
	Method string `json:"method"`
	Params any    `json:"params,omitempty"`
}

// response is what comes back. Either result or error is set, never both.
type response struct {
	ID     string          `json:"id"`
	Result json.RawMessage `json:"result,omitempty"`
	Error  *apiError       `json:"error,omitempty"`
}

type apiError struct {
	Code    string `json:"code"`
	Message string `json:"message"`
}

func (e *apiError) Error() string { return fmt.Sprintf("herdr %s: %s", e.Code, e.Message) }

// Client talks to herdr, one connection per request.
//
// herdr closes the socket after answering — it is request/response, not a
// session. Reusing a connection works exactly once and then fails with a broken
// pipe on the second call, which is the sort of thing a fake server never
// reveals: mine answered once too, and agreed with the bug.
//
// Dialling per call also removes the need to multiplex replies by id, and means a
// dropped connection costs one request rather than the whole run.
type Client struct {
	mu   sync.Mutex
	path string
	seq  int
}

// Dial connects to herdr at the given socket path.
//
// An empty path resolves the same order herdr's own CLI documents:
// $HERDR_SOCKET_PATH, then $HERDR_SESSION, then the default under the user's
// config directory. Resolving it here means the rest of Luna never has to know
// the convention.
func Dial(socket string) (*Client, error) {
	path := socket
	if path == "" {
		var err error
		if path, err = SocketPath(); err != nil {
			return nil, err
		}
	}

	// Reachability is checked by asking, not by opening a socket and dropping it:
	// a bare connect would spend a request herdr has already accepted, and every
	// call opens its own connection anyway.
	client := &Client{path: path}
	if err := client.Call("ping", nil, nil); err != nil && errors.Is(err, ErrGone) {
		// Only unreachability stops the dial. A herdr that answers — even to
		// refuse, even unintelligibly — is a herdr that is there, and whatever it
		// said is the caller's problem to interpret, not a reason to give up.
		return nil, err
	}
	return client, nil
}

// SocketPath is where herdr listens, by the documented resolution order.
func SocketPath() (string, error) {
	if p := os.Getenv("HERDR_SOCKET_PATH"); p != "" {
		return p, nil
	}
	if p := os.Getenv("HERDR_SESSION"); p != "" {
		return p, nil
	}

	home, err := os.UserHomeDir()
	if err != nil {
		return "", fmt.Errorf("%w: no socket given and no home directory: %w", ErrGone, err)
	}
	return filepath.Join(home, ".config", "herdr", "herdr.sock"), nil
}

// Call sends one request and decodes the reply into out.
//
// A closed or broken connection comes back wrapped in ErrGone, so the caller can
// tell "herdr went away" from "herdr said no" — two situations that warrant
// different transitions (ADR-0033).
func (c *Client) Call(method string, params, out any) error {
	c.mu.Lock()
	c.seq++
	id := strconv.Itoa(c.seq)
	c.mu.Unlock()

	line, err := json.Marshal(request{ID: id, Method: method, Params: params})
	if err != nil {
		return fmt.Errorf("encoding %s: %w", method, err)
	}

	conn, err := net.Dial("unix", c.path)
	if err != nil {
		return fmt.Errorf("%w: %s: %w", ErrGone, c.path, err)
	}
	defer func() { _ = conn.Close() }()

	if _, err := conn.Write(append(line, '\n')); err != nil {
		return fmt.Errorf("%w: writing %s: %w", ErrGone, method, err)
	}

	raw, err := bufio.NewReader(conn).ReadBytes('\n')
	if err != nil {
		return fmt.Errorf("%w: reading the reply to %s: %w", ErrGone, method, err)
	}

	var reply response
	if err := json.Unmarshal(raw, &reply); err != nil {
		return fmt.Errorf("decoding the reply to %s from %q: %w", method, raw, err)
	}
	if reply.Error != nil {
		return reply.Error
	}
	if out == nil || len(reply.Result) == 0 {
		return nil
	}
	if err := json.Unmarshal(reply.Result, out); err != nil {
		return fmt.Errorf("decoding the result of %s: %w", method, err)
	}
	return nil
}

// Close exists so callers can defer it. There is no persistent connection to
// release: each call opens and closes its own.
func (c *Client) Close() error { return nil }
