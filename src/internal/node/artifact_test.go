package node_test

import (
	"errors"
	"io"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"github.com/brunoomariano/luna/src/internal/node"
)

// memoryArtifacts is the named fake for the store, so the socket can be exercised
// without a database.
type memoryArtifacts struct {
	mu    sync.Mutex
	saved map[string][]byte
	fail  error
}

func newMemoryArtifacts() *memoryArtifacts {
	return &memoryArtifacts{saved: map[string][]byte{}}
}

func (m *memoryArtifacts) PutArtifact(stage, artifact string, body []byte) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.fail != nil {
		return m.fail
	}
	m.saved[stage+"/"+artifact] = body
	return nil
}

func (m *memoryArtifacts) GetArtifact(stage, artifact string) ([]byte, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	key := stage + "/" + artifact
	if stage == "" {
		for k, v := range m.saved {
			if strings.HasSuffix(k, "/"+artifact) {
				return v, nil
			}
		}
		return nil, errors.New("no such artifact")
	}
	body, ok := m.saved[key]
	if !ok {
		return nil, errors.New("no such artifact")
	}
	return body, nil
}

func serve(t *testing.T, stage string, s node.ArtifactStore) *node.ArtifactServer {
	t.Helper()
	// A short path: AF_UNIX caps the address at 108 bytes, and a test temp
	// directory can be most of that on its own.
	dir, err := os.MkdirTemp("/tmp", "luna-wt-")
	if err != nil {
		t.Fatalf("making a worktree: %v", err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(dir) })

	server, err := node.ServeArtifacts(dir, stage, s)
	if err != nil {
		t.Fatalf("serving artifacts: %v", err)
	}
	t.Cleanup(func() { _ = server.Close() })
	return server
}

// TestAnArtifactCrossesTheSocket is the floor: put then get, through the wire.
func TestAnArtifactCrossesTheSocket(t *testing.T) {
	fake := newMemoryArtifacts()
	server := serve(t, "spec", fake)

	resp, err := node.CallArtifact(server.Path(), node.Request{
		Op: "put", Artifact: "contract", Body: []byte("the contract"),
	})
	if err != nil || resp.Err != "" {
		t.Fatalf("putting: %v / %s", err, resp.Err)
	}

	resp, err = node.CallArtifact(server.Path(), node.Request{Op: "get", Artifact: "contract"})
	if err != nil || resp.Err != "" {
		t.Fatalf("getting: %v / %s", err, resp.Err)
	}
	if string(resp.Body) != "the contract" {
		t.Errorf("body: got %q, want %q", resp.Body, "the contract")
	}
}

// TestTheStageComesFromTheServerNotTheAgent pins the attribution rule: an agent
// names the artifact, never the stage, so nothing it says can credit its work to
// somebody else.
func TestTheStageComesFromTheServerNotTheAgent(t *testing.T) {
	fake := newMemoryArtifacts()
	server := serve(t, "qa", fake)

	if _, err := node.CallArtifact(server.Path(), node.Request{
		Op: "put", Stage: "spec", Artifact: "qa_report", Body: []byte("x"),
	}); err != nil {
		t.Fatalf("putting: %v", err)
	}

	if _, ok := fake.saved["qa/qa_report"]; !ok {
		t.Errorf("the write must be attributed to the serving stage, got %v", keys(fake.saved))
	}
	if _, ok := fake.saved["spec/qa_report"]; ok {
		t.Error("an agent naming a stage must not be believed")
	}
}

// TestAStoreRefusalReachesTheAgent covers the ceiling and every other refusal:
// the agent has to learn why, or it retries the same thing forever.
func TestAStoreRefusalReachesTheAgent(t *testing.T) {
	fake := newMemoryArtifacts()
	fake.fail = errors.New("the artifact is larger than the store accepts")
	server := serve(t, "qa", fake)

	resp, err := node.CallArtifact(server.Path(), node.Request{
		Op: "put", Artifact: "qa_report", Body: []byte("x"),
	})
	if err != nil {
		t.Fatalf("calling: %v", err)
	}
	if !strings.Contains(resp.Err, "larger than the store accepts") {
		t.Errorf("the refusal must reach the agent, got %q", resp.Err)
	}
}

// TestAnUnknownOperationIsAnswered pins that a bad request gets a reply rather
// than a closed connection, which reads as a hang.
func TestAnUnknownOperationIsAnswered(t *testing.T) {
	server := serve(t, "spec", newMemoryArtifacts())

	resp, err := node.CallArtifact(server.Path(), node.Request{Op: "delete", Artifact: "contract"})
	if err != nil {
		t.Fatalf("calling: %v", err)
	}
	if !strings.Contains(resp.Err, "delete") {
		t.Errorf("the refusal must name what was asked, got %q", resp.Err)
	}
}

// TestAResumedStageReusesTheSocketPath covers a stage that was killed and left the
// socket file behind: binding must succeed rather than fail on an existing path.
func TestAResumedStageReusesTheSocketPath(t *testing.T) {
	dir, err := os.MkdirTemp("/tmp", "luna-wt-")
	if err != nil {
		t.Fatalf("making a worktree: %v", err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(dir) })

	first, err := node.ServeArtifacts(dir, "spec", newMemoryArtifacts())
	if err != nil {
		t.Fatalf("first server: %v", err)
	}
	_ = first.Close()

	// Go's net package unlinks the socket on Close, so a clean shutdown leaves
	// nothing. What a killed stage leaves is the file, which is put back here —
	// that is the case this covers.
	if err := os.WriteFile(filepath.Join(dir, node.SocketName), []byte("stale"), 0o600); err != nil {
		t.Fatalf("simulating the socket a killed stage left: %v", err)
	}

	second, err := node.ServeArtifacts(dir, "spec", newMemoryArtifacts())
	if err != nil {
		t.Fatalf("a resumed stage must be able to bind again, got %v", err)
	}
	_ = second.Close()
}

// TestTheSocketCrossesTheSandbox is the measurement the whole design rests on: an
// agent contained by ai-jail reaches Luna, and what it writes lands outside.
//
// It skips without ai-jail rather than passing, for the reason this project keeps
// finding: a test that passes without proving anything is worse than no test.
func TestTheSocketCrossesTheSandbox(t *testing.T) {
	jail, err := exec.LookPath("ai-jail")
	if err != nil {
		t.Skip("ai-jail is not installed; the sandbox crossing cannot be measured here")
	}

	dir, err := os.MkdirTemp("/tmp", "luna-wt-")
	if err != nil {
		t.Fatalf("making a worktree: %v", err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(dir) })

	fake := newMemoryArtifacts()
	server, err := node.ServeArtifacts(dir, "spec", fake)
	if err != nil {
		t.Fatalf("serving: %v", err)
	}
	t.Cleanup(func() { _ = server.Close() })

	// A client inside the jail, with the worktree as its only reachable directory.
	client := filepath.Join(dir, "put.py")
	script := `import socket,json
s=socket.socket(socket.AF_UNIX,socket.SOCK_STREAM); s.connect("` + node.SocketName + `")
s.sendall(json.dumps({"op":"put","artifact":"contract","body":"dGhlIGNvbnRyYWN0"}).encode())
print(s.recv(4096).decode())`
	if err := os.WriteFile(client, []byte(script), 0o600); err != nil {
		t.Fatalf("writing the client: %v", err)
	}

	cmd := exec.Command(jail, "python3", "./put.py") //nolint:gosec // a path from LookPath
	cmd.Dir = dir
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("the contained client failed: %v: %s", err, out)
	}

	fake.mu.Lock()
	defer fake.mu.Unlock()
	body, ok := fake.saved["spec/contract"]
	if !ok {
		t.Fatalf("a contained agent's write must land outside the sandbox, got %v", keys(fake.saved))
	}
	// "the contract", base64 over the wire because Body is []byte in JSON.
	if string(body) != "the contract" {
		t.Errorf("body: got %q, want %q", body, "the contract")
	}
}

func keys(m map[string][]byte) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	return out
}

// TestAMalformedRequestIsAnsweredNotDropped: the agent is waiting, and a closed
// connection with no reply reads as a hang.
func TestAMalformedRequestIsAnsweredNotDropped(t *testing.T) {
	server := serve(t, "spec", newMemoryArtifacts())

	conn, err := net.Dial("unix", server.Path())
	if err != nil {
		t.Fatalf("dialing: %v", err)
	}
	defer func() { _ = conn.Close() }()

	if _, err := conn.Write([]byte("this is not json\n")); err != nil {
		t.Fatalf("writing: %v", err)
	}
	answer, err := io.ReadAll(conn)
	if err != nil {
		t.Fatalf("reading: %v", err)
	}
	if !strings.Contains(string(answer), "could not read the request") {
		t.Errorf("a malformed request gets an answer, got %q", answer)
	}
}

// TestAGetForNothingComesBackAsAnError covers the read miss travelling the wire.
func TestAGetForNothingComesBackAsAnError(t *testing.T) {
	server := serve(t, "spec", newMemoryArtifacts())

	resp, err := node.CallArtifact(server.Path(), node.Request{Op: "get", Artifact: "contract"})
	if err != nil {
		t.Fatalf("calling: %v", err)
	}
	if !strings.Contains(resp.Err, "no such artifact") {
		t.Errorf("a miss must say so, got %q", resp.Err)
	}
}

// TestDialingNobodyIsReportedWithThePath covers the client's own failure: the
// socket it was pointed at has nobody behind it.
func TestDialingNobodyIsReportedWithThePath(t *testing.T) {
	_, err := node.CallArtifact("/tmp/luna-nobody.sock", node.Request{Op: "get", Artifact: "x"})
	if err == nil || !strings.Contains(err.Error(), "/tmp/luna-nobody.sock") {
		t.Errorf("the failure must name where Luna was looked for, got %v", err)
	}
}

// TestAWorktreeThatCannotHoldTheSocketIsRefused covers ServeArtifacts's own
// failures: the socket's directory cannot be made, or the stale path cannot be
// cleared.
func TestAWorktreeThatCannotHoldTheSocketIsRefused(t *testing.T) {
	worktree, err := os.MkdirTemp("/tmp", "luna-wt-")
	if err != nil {
		t.Fatalf("making a worktree: %v", err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(worktree) })

	// `.luna` as a file: MkdirAll cannot make a directory over it.
	if err := os.WriteFile(filepath.Join(worktree, ".luna"), []byte("a file"), 0o600); err != nil {
		t.Fatalf("setting up: %v", err)
	}
	if _, err := node.ServeArtifacts(worktree, "spec", newMemoryArtifacts()); err == nil {
		t.Error("a directory that cannot be made must be reported")
	}

	// The socket's path as a non-empty directory: os.Remove cannot clear it.
	if err := os.Remove(filepath.Join(worktree, ".luna")); err != nil {
		t.Fatalf("resetting: %v", err)
	}
	if err := os.MkdirAll(filepath.Join(worktree, node.SocketName, "occupied"), 0o750); err != nil {
		t.Fatalf("setting up: %v", err)
	}
	if _, err := node.ServeArtifacts(worktree, "spec", newMemoryArtifacts()); err == nil {
		t.Error("a stale path that cannot be cleared must be reported")
	}
}
