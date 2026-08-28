package node

import (
	"bufio"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"os"
	"path/filepath"
	"sync"

	"github.com/brunoomariano/luna/src/internal/sock"
)

// SocketDirName is the directory the handover sockets live in, under the runtime
// directory. One per stage, named after the task and the stage.
//
// Outside the worktree, which took a measurement to make possible. It was inside
// it — `.luna/artifact.sock` — because that was the only position a contained
// agent could reach: against ai-jail 1.17.0 a socket in $HOME, in /tmp, or behind
// a symlink out of the working directory all answered ENOENT.
//
// What changed is that Luna asks for the directory rather than hoping. It builds
// the sandbox's command line, so it passes `--map` for this path, and against
// ai-jail 1.20.1 that connects. Two things were measured with it and both matter:
// a **read-only** map is enough, because Landlock permits connect() on an inode
// it can merely see — and the agent then cannot write into the directory at all,
// which it could when the socket lived in a worktree it owned. And the same
// mapping through a project's own `.ai-jail` is refused by design, so this has to
// come from Luna's flags: a repository must not be able to name what gets mounted.
const SocketDirName = "luna"

// HandoverDirName is the subdirectory the stage sockets live in, and it is
// separate from everything else under SocketDir for one reason: this is the
// directory the sandbox is asked to expose.
//
// A contained agent can reach whatever is in here. That is right for a handover
// socket, whose whole protocol is "give this artifact to Luna", and wrong for
// anything that writes the log — an agent that appends to the log does not
// corrupt a file, it fabricates history. So the daemon's own socket lives beside
// this directory rather than in it, and is never mapped.
const HandoverDirName = "handover"

// SocketDir is where Luna's sockets are opened, under $XDG_RUNTIME_DIR.
//
// The runtime directory rather than the data home: these are sockets, they last
// exactly as long as the stage, and the runtime directory is the one the system
// already clears. It is also short, which `sun_path` cares about — 108 bytes for
// the whole path, and `/run/user/1000/luna/` leaves room for a task and a stage.
func SocketDir() string {
	if run := os.Getenv("XDG_RUNTIME_DIR"); run != "" {
		return filepath.Join(run, SocketDirName)
	}

	// No runtime directory is the ordinary case on a machine without a session
	// bus — a container, a CI runner. The temporary directory is short, per-user
	// on any sane system, and cleared on reboot, which is the whole of what this
	// needs.
	return filepath.Join(os.TempDir(), SocketDirName)
}

// HandoverDir is the one directory a contained agent is given.
func HandoverDir() string {
	return filepath.Join(SocketDir(), HandoverDirName)
}

// SocketFor names the socket one stage of one task listens on.
//
// Both halves are in the name: two stages of one task run in sequence and a
// resumed stage binds the same path, so a name carrying only the task would put
// two different stages on one socket.
func SocketFor(taskID, stage string) string {
	return filepath.Join(HandoverDir(), taskID+"-"+stage+".sock")
}

// ArtifactStore is what the server needs from the store. It is an interface so
// the socket can be exercised without a database, and so this package does not
// import the store — the dependency runs the other way.
type ArtifactStore interface {
	PutArtifact(stage, artifact string, body []byte) error
	GetArtifact(stage, artifact string) ([]byte, error)
}

// Request is one call from the agent's CLI.
type Request struct {
	Op       string `json:"op"`
	Stage    string `json:"stage,omitempty"`
	Artifact string `json:"artifact"`
	Body     []byte `json:"body,omitempty"`
}

// Response is what the server answers. Err is a message rather than a code
// because the reader is a person looking at their terminal, and the CLI prints it
// verbatim.
type Response struct {
	Err  string `json:"err,omitempty"`
	Body []byte `json:"body,omitempty"`
}

// ArtifactServer accepts put and get over a Unix socket for one stage.
//
// One per running stage, opened before the agent starts and closed with it. It is
// bound to the stage it was opened for: the agent names the artifact, never the
// stage, so nothing an agent says can attribute work to somebody else.
type ArtifactServer struct {
	listener net.Listener
	store    ArtifactStore
	stage    string

	// path is where the socket really is. The listener may know it by a shorter
	// alias (see shortEnough), and the alias dies with the file descriptor behind
	// it — this is the name that stays valid.
	path string

	wg   sync.WaitGroup
	once sync.Once
}

// ServeArtifacts starts the server for one stage of one task.
//
// The socket is removed first if one is there: a stage that was killed leaves the
// file behind, and bind fails on an existing path. That is a resumed stage rather
// than a conflict — there is one server per task and stage by construction.
func ServeArtifacts(taskID, stage string, s ArtifactStore) (*ArtifactServer, error) {
	path := SocketFor(taskID, stage)
	// 0o700: the directory holds one person's sockets, and a mode anyone could
	// write to would let anyone bind a name a contained agent then connects to.
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return nil, fmt.Errorf("making room for the artifact socket: %w", err)
	}
	if err := os.Remove(path); err != nil && !errors.Is(err, os.ErrNotExist) {
		return nil, fmt.Errorf("clearing the artifact socket at %s: %w", path, err)
	}

	bindable, done, err := sock.Short(path)
	if err != nil {
		return nil, err
	}
	defer done()

	listener, err := net.Listen("unix", bindable)
	if err != nil {
		return nil, fmt.Errorf("opening the artifact socket at %s: %w", path, err)
	}

	server := &ArtifactServer{listener: listener, store: s, stage: stage, path: path}
	server.wg.Add(1)
	go server.accept()
	return server, nil
}

// Path is where the socket is, for the brief to name.
func (s *ArtifactServer) Path() string { return s.path }

// Close stops the server and waits for connections in flight.
func (s *ArtifactServer) Close() error {
	var err error
	s.once.Do(func() {
		err = s.listener.Close()
		s.wg.Wait()
	})
	return err
}

func (s *ArtifactServer) accept() {
	defer s.wg.Done()
	for {
		conn, err := s.listener.Accept()
		if err != nil {
			return // the listener was closed, which is how this ends
		}
		s.wg.Add(1)
		go func() {
			defer s.wg.Done()
			defer func() { _ = conn.Close() }()
			s.handle(conn)
		}()
	}
}

// handle answers one request.
//
// A malformed request is answered rather than dropped: the agent is waiting, and
// a closed connection with no reply reads as a hang.
func (s *ArtifactServer) handle(conn net.Conn) {
	var req Request
	if err := json.NewDecoder(bufio.NewReader(conn)).Decode(&req); err != nil {
		s.reply(conn, Response{Err: "could not read the request: " + err.Error()})
		return
	}

	// The CLI refuses an empty name, but the socket is the boundary that decides:
	// anything on the jail's side of it is the agent, and one of them was measured
	// getting an unnamed blob stored — a row nothing can ask for by name, listed
	// as a blank line beside the real one.
	if req.Artifact == "" {
		s.reply(conn, Response{Err: "the request names no artifact"})
		return
	}

	switch req.Op {
	case "put":
		if err := s.store.PutArtifact(s.stage, req.Artifact, req.Body); err != nil {
			s.reply(conn, Response{Err: err.Error()})
			return
		}
		s.reply(conn, Response{})
	case "get":
		body, err := s.store.GetArtifact(req.Stage, req.Artifact)
		if err != nil {
			s.reply(conn, Response{Err: err.Error()})
			return
		}
		s.reply(conn, Response{Body: body})
	default:
		s.reply(conn, Response{Err: fmt.Sprintf("unknown operation %q: this socket takes put and get", req.Op)})
	}
}

func (s *ArtifactServer) reply(conn net.Conn, r Response) {
	_ = json.NewEncoder(conn).Encode(r)
}

// CallArtifact is the client half, used by the CLI from inside the sandbox.
//
// It takes the socket path rather than discovering it, because the caller knows
// where it is standing and a client that searches would be a client that can find
// the wrong stage's socket.
func CallArtifact(socket string, req Request) (Response, error) {
	dialable, done, err := sock.Short(socket)
	if err != nil {
		return Response{}, err
	}
	defer done()

	conn, err := net.Dial("unix", dialable)
	if err != nil {
		return Response{}, fmt.Errorf("reaching Luna at %s: %w", socket, err)
	}
	defer func() { _ = conn.Close() }()

	if err := json.NewEncoder(conn).Encode(req); err != nil {
		return Response{}, fmt.Errorf("sending the request: %w", err)
	}

	var resp Response
	if err := json.NewDecoder(conn).Decode(&resp); err != nil && !errors.Is(err, io.EOF) {
		return Response{}, fmt.Errorf("reading the answer: %w", err)
	}
	return resp, nil
}
