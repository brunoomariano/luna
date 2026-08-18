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
)

// SocketName is where the server listens, relative to the agent's worktree.
//
// Inside the worktree because that is the only place a contained agent can reach:
// measured against ai-jail 1.17.0, a socket in $HOME, in /tmp, or reached through
// a symlink out of the working directory all answer ENOENT, and a real socket
// under the cwd connects (RFC-0008). Landlock permits connect() on an inode it can
// see; the process on the other end is not contained and writes wherever it likes.
const SocketName = ".luna/artifact.sock"

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

	wg   sync.WaitGroup
	once sync.Once
}

// ServeArtifacts starts the server inside a worktree.
//
// The socket is removed first if one is there: a stage that was killed leaves the
// file behind, and bind fails on an existing path. That is a resumed stage rather
// than a conflict — there is one server per worktree by construction.
func ServeArtifacts(worktree, stage string, s ArtifactStore) (*ArtifactServer, error) {
	path := filepath.Join(worktree, SocketName)
	if err := os.MkdirAll(filepath.Dir(path), 0o750); err != nil {
		return nil, fmt.Errorf("making room for the artifact socket: %w", err)
	}
	if err := os.Remove(path); err != nil && !errors.Is(err, os.ErrNotExist) {
		return nil, fmt.Errorf("clearing the artifact socket at %s: %w", path, err)
	}

	listener, err := net.Listen("unix", path)
	if err != nil {
		return nil, fmt.Errorf("opening the artifact socket at %s: %w", path, err)
	}

	server := &ArtifactServer{listener: listener, store: s, stage: stage}
	server.wg.Add(1)
	go server.accept()
	return server, nil
}

// Path is where the socket is, for the brief to name.
func (s *ArtifactServer) Path() string { return s.listener.Addr().String() }

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
	conn, err := net.Dial("unix", socket)
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
