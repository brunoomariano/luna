package herdr

import (
	"bufio"
	"encoding/json"
	"errors"
	"net"
	"path/filepath"
	"strings"
	"testing"
)

// serveOnce answers one request on a temporary Unix socket and returns its path.
//
// A real socket rather than a mock of the connection: the thing under test is the
// framing — one JSON object per line — and a mock of the transport would assert
// the framing I wrote rather than the one herdr expects.
func serveOnce(t *testing.T, reply string) string {
	t.Helper()

	path := filepath.Join(t.TempDir(), "h.sock")
	listener, err := net.Listen("unix", path)
	if err != nil {
		t.Fatalf("listening: %v", err)
	}
	t.Cleanup(func() { _ = listener.Close() })

	go func() {
		conn, err := listener.Accept()
		if err != nil {
			return
		}
		defer func() { _ = conn.Close() }()

		// Read the request line so the client's write completes, then answer.
		if _, err := bufio.NewReader(conn).ReadBytes('\n'); err != nil {
			return
		}
		_, _ = conn.Write([]byte(reply + "\n"))
	}()

	return path
}

// TestACallRoundTripsOverTheSocket covers the framing herdr documents: newline
// delimited JSON, not JSON-RPC 2.0.
func TestACallRoundTripsOverTheSocket(t *testing.T) {
	path := serveOnce(t, `{"id":1,"result":{"workspace_id":"ws-7"}}`)

	client, err := Dial(path)
	if err != nil {
		t.Fatalf("dialling: %v", err)
	}
	defer func() { _ = client.Close() }()

	var out struct {
		WorkspaceID string `json:"workspace_id"`
	}
	if err := client.Call("worktree.create", map[string]string{"branch": "luna/LUNA-1"}, &out); err != nil {
		t.Fatalf("calling: %v", err)
	}

	if out.WorkspaceID != "ws-7" {
		t.Errorf("want the decoded result, got %q", out.WorkspaceID)
	}
}

// TestTheRequestCarriesIDAndMethod checks what actually goes on the wire.
//
// A client that sends the right thing and a server that answers anything would
// both pass a round-trip test; this reads the bytes.
func TestTheRequestCarriesIDAndMethod(t *testing.T) {
	path := filepath.Join(t.TempDir(), "h.sock")
	listener, err := net.Listen("unix", path)
	if err != nil {
		t.Fatalf("listening: %v", err)
	}
	defer func() { _ = listener.Close() }()

	seen := make(chan string, 1)
	go func() {
		conn, err := listener.Accept()
		if err != nil {
			return
		}
		defer func() { _ = conn.Close() }()

		line, err := bufio.NewReader(conn).ReadBytes('\n')
		if err != nil {
			return
		}
		seen <- string(line)
		_, _ = conn.Write([]byte(`{"id":1,"result":{}}` + "\n"))
	}()

	client, err := Dial(path)
	if err != nil {
		t.Fatalf("dialling: %v", err)
	}
	defer func() { _ = client.Close() }()

	if err := client.Call("ping", nil, nil); err != nil {
		t.Fatalf("calling: %v", err)
	}

	var sent request
	if err := json.Unmarshal([]byte(<-seen), &sent); err != nil {
		t.Fatalf("the request must be one JSON object per line: %v", err)
	}
	if sent.Method != "ping" {
		t.Errorf("want the method on the wire, got %q", sent.Method)
	}
	if sent.ID == 0 {
		t.Error("every request carries an id")
	}
}

// TestAnErrorReplyComesBackAsAnError covers herdr saying no.
//
// It must stay distinguishable from herdr going away: one is an answer, the other
// is the absence of one, and they lead to different transitions (ADR-0033).
func TestAnErrorReplyComesBackAsAnError(t *testing.T) {
	path := serveOnce(t, `{"id":1,"error":{"code":"target_busy","message":"the pane is occupied"}}`)

	client, err := Dial(path)
	if err != nil {
		t.Fatalf("dialling: %v", err)
	}
	defer func() { _ = client.Close() }()

	err = client.Call("agent.start", nil, nil)

	if err == nil {
		t.Fatal("an error reply must be reported")
	}
	if errors.Is(err, ErrGone) {
		t.Error("herdr answering is not herdr going away")
	}
	if !strings.Contains(err.Error(), "target_busy") {
		t.Errorf("the error should carry herdr's code, got %v", err)
	}
}

// TestAMissingSocketIsErrGone covers the case ADR-0033 turns into a block.
func TestAMissingSocketIsErrGone(t *testing.T) {
	_, err := Dial(filepath.Join(t.TempDir(), "nothing-here.sock"))

	if !errors.Is(err, ErrGone) {
		t.Errorf("want ErrGone when herdr is not there, got %v", err)
	}
}

// TestALostConnectionIsErrGone covers the socket dying mid-stage.
//
// This is the situation that must never be confused with a stage failure: the
// lead blocks on it rather than spending the retry budget on infrastructure.
func TestALostConnectionIsErrGone(t *testing.T) {
	path := filepath.Join(t.TempDir(), "h.sock")
	listener, err := net.Listen("unix", path)
	if err != nil {
		t.Fatalf("listening: %v", err)
	}

	go func() {
		conn, err := listener.Accept()
		if err != nil {
			return
		}
		// Hang up without answering, which is what a herdr exiting looks like.
		_ = conn.Close()
	}()

	client, err := Dial(path)
	if err != nil {
		t.Fatalf("dialling: %v", err)
	}
	defer func() { _ = client.Close() }()
	_ = listener.Close()

	if err := client.Call("ping", nil, nil); !errors.Is(err, ErrGone) {
		t.Errorf("want ErrGone when the connection dies, got %v", err)
	}
}

// TestAMalformedReplyIsNotErrGone covers the third case.
//
// herdr answered with something unreadable. That is a protocol problem, not an
// absence, and treating it as ErrGone would block a task for a bug that a
// version check should surface instead.
func TestAMalformedReplyIsNotErrGone(t *testing.T) {
	path := serveOnce(t, `{not json`)

	client, err := Dial(path)
	if err != nil {
		t.Fatalf("dialling: %v", err)
	}
	defer func() { _ = client.Close() }()

	err = client.Call("ping", nil, nil)

	if err == nil {
		t.Fatal("a malformed reply must be reported")
	}
	if errors.Is(err, ErrGone) {
		t.Error("an unreadable answer is still an answer; herdr is there")
	}
}

// TestSocketPathFollowsTheDocumentedOrder covers the resolution herdr documents,
// so the rest of Luna never has to know the convention.
func TestSocketPathFollowsTheDocumentedOrder(t *testing.T) {
	t.Setenv("HERDR_SOCKET_PATH", "/tmp/explicit.sock")
	t.Setenv("HERDR_SESSION", "/tmp/session.sock")

	got, err := SocketPath()
	if err != nil {
		t.Fatalf("resolving: %v", err)
	}
	if got != "/tmp/explicit.sock" {
		t.Errorf("HERDR_SOCKET_PATH wins, got %q", got)
	}

	t.Setenv("HERDR_SOCKET_PATH", "")
	if got, _ = SocketPath(); got != "/tmp/session.sock" {
		t.Errorf("HERDR_SESSION comes next, got %q", got)
	}

	t.Setenv("HERDR_SESSION", "")
	got, err = SocketPath()
	if err != nil {
		t.Fatalf("resolving the default: %v", err)
	}
	if !strings.HasSuffix(got, filepath.Join(".config", "herdr", "herdr.sock")) {
		t.Errorf("want the documented default, got %q", got)
	}
}

// TestDialWithNoPathResolvesTheEnvironment covers the empty-path branch.
func TestDialWithNoPathResolvesTheEnvironment(t *testing.T) {
	path := serveOnce(t, `{"id":1,"result":{}}`)
	t.Setenv("HERDR_SOCKET_PATH", path)

	client, err := Dial("")
	if err != nil {
		t.Fatalf("an empty path resolves from the environment: %v", err)
	}
	defer func() { _ = client.Close() }()

	if err := client.Call("ping", nil, nil); err != nil {
		t.Errorf("the resolved connection must work: %v", err)
	}
}
