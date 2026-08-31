package node_test

import (
	"context"
	"errors"
	"io"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

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
	// A short runtime directory: AF_UNIX caps the address at 108 bytes, and a test
	// temp directory can be most of that on its own.
	runtimeDir(t)

	server, err := node.ServeArtifacts("T-1", stage, s)
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
// runtimeDir points the sockets at a directory of this test's own, short enough
// for AF_UNIX's 108-byte address and cleared with the test.
//
// Isolated rather than shared: without it a test run opens sockets in whoever ran
// it, and two tests naming the same stage would collide.
func runtimeDir(t *testing.T) string {
	t.Helper()

	dir, err := os.MkdirTemp("/tmp", "luna-rt-")
	if err != nil {
		t.Fatalf("making a runtime directory: %v", err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(dir) })
	t.Setenv("XDG_RUNTIME_DIR", dir)
	return dir
}

func TestAResumedStageReusesTheSocketPath(t *testing.T) {
	runtimeDir(t)

	first, err := node.ServeArtifacts("T-2", "spec", newMemoryArtifacts())
	if err != nil {
		t.Fatalf("first server: %v", err)
	}
	_ = first.Close()

	// Go's net package unlinks the socket on Close, so a clean shutdown leaves
	// nothing. What a killed stage leaves is the file, which is put back here —
	// that is the case this covers.
	stale := node.SocketFor("T-2", "spec")
	if err := os.WriteFile(stale, []byte("stale"), 0o600); err != nil {
		t.Fatalf("simulating the socket a killed stage left: %v", err)
	}

	second, err := node.ServeArtifacts("T-2", "spec", newMemoryArtifacts())
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

	runtimeDir(t)
	dir, err := os.MkdirTemp("/tmp", "luna-wt-")
	if err != nil {
		t.Fatalf("making a worktree: %v", err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(dir) })

	fake := newMemoryArtifacts()
	server, err := node.ServeArtifacts("T-9", "spec", fake)
	if err != nil {
		t.Fatalf("serving: %v", err)
	}
	t.Cleanup(func() { _ = server.Close() })

	socket := node.SocketFor("T-9", "spec")
	// The socket is outside the worktree now, which is the whole point of this
	// test: the agent's only writable directory does not contain it.
	if strings.HasPrefix(socket, dir) {
		t.Fatalf("the socket is inside the worktree: %s", socket)
	}

	client := filepath.Join(dir, "put.py")
	script := `import socket,json
s=socket.socket(socket.AF_UNIX,socket.SOCK_STREAM); s.connect("` + socket + `")
s.sendall(json.dumps({"op":"put","artifact":"contract","body":"dGhlIGNvbnRyYWN0"}).encode())
print(s.recv(4096).decode())`
	if err := os.WriteFile(client, []byte(script), 0o600); err != nil {
		t.Fatalf("writing the client: %v", err)
	}

	// `--map`, read-only, exactly as Luna passes it. Without the flag this answers
	// ENOENT, which is what kept the socket in the worktree until it was measured.
	ctx, stop := context.WithTimeout(context.Background(), 10*time.Second)
	defer stop()
	cmd := exec.CommandContext(ctx, jail, "--map", filepath.Dir(socket), //nolint:gosec // a path from LookPath
		"python3", "./put.py")
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

// TestASocketDirectoryThatCannotBeMadeIsRefused covers ServeArtifacts's own
// failures: the directory cannot be made, or the stale path cannot be cleared.
func TestASocketDirectoryThatCannotBeMadeIsRefused(t *testing.T) {
	dir := runtimeDir(t)

	// The socket directory's name taken by a file: MkdirAll cannot make a
	// directory over it.
	if err := os.WriteFile(filepath.Join(dir, node.SocketDirName), []byte("a file"), 0o600); err != nil {
		t.Fatalf("setting up: %v", err)
	}
	if _, err := node.ServeArtifacts("T-3", "spec", newMemoryArtifacts()); err == nil {
		t.Error("a directory that cannot be made must be reported")
	}

	// The socket's own path as a non-empty directory: os.Remove cannot clear it.
	if err := os.Remove(filepath.Join(dir, node.SocketDirName)); err != nil {
		t.Fatalf("resetting: %v", err)
	}
	stale := node.SocketFor("T-3", "spec")
	if err := os.MkdirAll(filepath.Join(stale, "occupied"), 0o750); err != nil {
		t.Fatalf("setting up: %v", err)
	}
	if _, err := node.ServeArtifacts("T-3", "spec", newMemoryArtifacts()); err == nil {
		t.Error("a stale path that cannot be cleared must be reported")
	}
}

// TestADeepWorktreeNoLongerReachesTheSocketPath is what moving the socket out of
// the worktree bought, stated as a test rather than left as a side effect.
//
// AF_UNIX caps a socket address at 108 bytes, and the first real run died on
// `bind: invalid argument` because a worktree 140 bytes down carried the socket
// with it. The socket lives under the runtime directory now, so how deep a
// worktree sits has stopped being able to break the handover at all.
func TestADeepWorktreeNoLongerReachesTheSocketPath(t *testing.T) {
	runtimeDir(t)

	deep := filepath.Join(t.TempDir(), strings.Repeat("deep-directory-name/", 6))
	if err := os.MkdirAll(deep, 0o750); err != nil {
		t.Fatalf("digging the deep worktree: %v", err)
	}
	if len(deep) <= 107 {
		t.Fatalf("the fixture is not deep enough to exercise the limit")
	}

	socket := node.SocketFor("T-4", "spec")
	if len(socket) > 107 {
		t.Errorf("the socket path is %d bytes, past what AF_UNIX accepts: %s", len(socket), socket)
	}

	fake := newMemoryArtifacts()
	server, err := node.ServeArtifacts("T-4", "spec", fake)
	if err != nil {
		t.Fatalf("serving: %v", err)
	}
	t.Cleanup(func() { _ = server.Close() })

	resp, err := node.CallArtifact(server.Path(), node.Request{
		Op: "put", Artifact: "contract", Body: []byte("deep content"),
	})
	if err != nil || resp.Err != "" {
		t.Fatalf("putting from a deep worktree: %v / %s", err, resp.Err)
	}
	if string(fake.saved["spec/contract"]) != "deep content" {
		t.Errorf("the content must arrive whole, got %q", fake.saved["spec/contract"])
	}
}

// TestADeepSocketWhoseDirectoryIsGoneIsReported covers shortEnough's own
// failure: the path is too long to use directly and its directory cannot be
// opened to shorten it.
func TestADeepSocketWhoseDirectoryIsGoneIsReported(t *testing.T) {
	gone := "/tmp/luna-nowhere/" + strings.Repeat("deep-directory-name/", 6) + "artifact.sock"

	_, err := node.CallArtifact(gone, node.Request{Op: "get", Artifact: "x"})
	if err == nil || !strings.Contains(err.Error(), "socket's directory") {
		t.Errorf("an unopenable directory must be reported as itself, got %v", err)
	}
}

// TestAnUnnamedArtifactIsRefusedAtTheSocket is the regression for a blob stored
// with an empty name: the CLI refuses one, but the socket is the boundary that
// decides, and a real agent got one through — a row nothing can ask for by name,
// listed as a blank line beside the real one.
func TestAnUnnamedArtifactIsRefusedAtTheSocket(t *testing.T) {
	fake := newMemoryArtifacts()
	server := serve(t, "code-review", fake)

	for _, op := range []string{"put", "get"} {
		resp, err := node.CallArtifact(server.Path(), node.Request{Op: op, Body: []byte("x")})
		if err != nil {
			t.Fatalf("%s: calling: %v", op, err)
		}
		if !strings.Contains(resp.Err, "names no artifact") {
			t.Errorf("%s with no name must be refused, got %q", op, resp.Err)
		}
	}
	if len(fake.saved) != 0 {
		t.Errorf("nothing may be stored under no name, got %v", keys(fake.saved))
	}
}

// TestTheSocketDirectoryFollowsTheRuntimeThenFallsBack covers both branches.
//
// The runtime directory is where sockets belong — the system clears it, and it is
// short, which AF_UNIX's 108-byte address cares about. A machine without one is
// the ordinary container or CI runner, and the temporary directory has the same
// two properties.
func TestTheSocketDirectoryFollowsTheRuntimeThenFallsBack(t *testing.T) {
	run := t.TempDir()
	t.Setenv("XDG_RUNTIME_DIR", run)

	dir := node.SocketDir()
	if dir != filepath.Join(run, node.SocketDirName) {
		t.Errorf("dir = %s, want it under %s", dir, run)
	}

	t.Setenv("XDG_RUNTIME_DIR", "")
	dir = node.SocketDir()
	if !strings.HasPrefix(dir, os.TempDir()) {
		t.Errorf("dir = %s, want it under the temporary directory", dir)
	}
}

// TestASocketIsNamedForItsTaskAndStage. Two stages of one task run in sequence
// and a resumed one binds the same name — so the name has to carry both, or a
// second task's stage would collide with the first's.
func TestASocketIsNamedForItsTaskAndStage(t *testing.T) {
	runtimeDir(t)

	one := node.SocketFor("LUNA-1", "forge")
	for _, other := range [][2]string{{"LUNA-1", "review"}, {"LUNA-2", "forge"}} {
		got := node.SocketFor(other[0], other[1])
		if got == one {
			t.Errorf("%s/%s shares a socket with LUNA-1/forge: %s", other[0], other[1], got)
		}
	}
}

// TestALongSocketPathStillBinds. Moving the socket to the runtime directory made
// the address shorter, and did not make the limit go away: a task id may be 64
// characters and a runtime directory is whatever the system chose, so the sum can
// still pass AF_UNIX's 108 bytes.
//
// The fallback binds through `/proc/self/fd`, which is short whatever the real
// path is. This drives it from the outside — a long runtime directory — rather
// than calling the helper, because what has to keep working is the handover.
func TestALongSocketPathStillBinds(t *testing.T) {
	long := filepath.Join(t.TempDir(), strings.Repeat("padding-directory/", 5))
	if err := os.MkdirAll(long, 0o750); err != nil {
		t.Fatalf("digging: %v", err)
	}
	t.Setenv("XDG_RUNTIME_DIR", long)

	if len(node.SocketFor("LUNA-1", "forge")) <= 107 {
		t.Fatalf("the fixture is not long enough to exercise the limit")
	}

	fake := newMemoryArtifacts()
	server, err := node.ServeArtifacts("LUNA-1", "forge", fake)
	if err != nil {
		t.Fatalf("a long socket path must still bind, got %v", err)
	}
	t.Cleanup(func() { _ = server.Close() })

	resp, err := node.CallArtifact(server.Path(), node.Request{
		Op: "put", Artifact: "contract", Body: []byte("through a long path"),
	})
	if err != nil || resp.Err != "" {
		t.Fatalf("putting through a long socket: %v / %s", err, resp.Err)
	}
	if string(fake.saved["forge/contract"]) != "through a long path" {
		t.Errorf("the content did not arrive: %q", fake.saved["forge/contract"])
	}
}

// TestCallingASocketThatIsNotThereIsReported. An agent handing an artifact over
// to a Luna that is no longer listening has to hear about it: the alternative is
// a stage that believes it delivered and a store that never received.
func TestCallingASocketThatIsNotThereIsReported(t *testing.T) {
	_, err := node.CallArtifact(filepath.Join(t.TempDir(), "nobody.sock"),
		node.Request{Op: "get", Artifact: "contract"})

	if err == nil {
		t.Fatal("a call to a socket nobody is on was taken as success")
	}
	if !strings.Contains(err.Error(), "nobody.sock") {
		t.Errorf("the failure does not name what was unreachable: %v", err)
	}
}
