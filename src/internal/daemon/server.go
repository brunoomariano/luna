package daemon

import (
	"bufio"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"os"
	"path/filepath"
	"sync"

	"github.com/brunoomariano/luna/src/internal/store"
)

// SocketName is the daemon's own socket, beside the handover directory rather
// than inside it.
//
// Outside on purpose: the handover directory is the one thing a contained agent
// is given, and an agent that could reach this socket could write the log.
const SocketName = "daemon.sock"

// Server is the daemon. It holds one open store per project and appends to them.
type Server struct {
	listener net.Listener

	// mu guards stores. Appends from two projects are independent, and the store
	// serialises writers within one — so the lock is only over the map.
	mu     sync.Mutex
	stores map[string]*store.Store

	wg sync.WaitGroup
}

// Listen starts the daemon on a socket.
//
// A socket left by a killed daemon is removed first: bind fails on an existing
// path, and a daemon that refused to start because the last one was killed would
// need a person to clean up after a crash.
func Listen(path string) (*Server, error) {
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return nil, fmt.Errorf("making room for the daemon socket: %w", err)
	}
	if err := os.Remove(path); err != nil && !errors.Is(err, os.ErrNotExist) {
		return nil, fmt.Errorf("clearing the daemon socket at %s: %w", path, err)
	}

	listener, err := net.Listen("unix", path)
	if err != nil {
		return nil, fmt.Errorf("opening the daemon socket at %s: %w", path, err)
	}

	s := &Server{listener: listener, stores: map[string]*store.Store{}}
	s.wg.Add(1)
	go s.accept()
	return s, nil
}

// Close stops the daemon and closes every store it opened.
func (s *Server) Close() error {
	err := s.listener.Close()
	s.wg.Wait()

	s.mu.Lock()
	defer s.mu.Unlock()
	for _, open := range s.stores {
		_ = open.Close()
	}
	return err
}

func (s *Server) accept() {
	defer s.wg.Done()

	for {
		conn, err := s.listener.Accept()
		if err != nil {
			// The listener closed, which is the ordinary end. Anything else is a
			// connection that failed and the next one may not.
			return
		}
		s.wg.Add(1)
		go func() {
			defer s.wg.Done()
			s.serve(conn)
		}()
	}
}

// serve answers one request.
//
// One per connection: the client is a CLI process that asks once and exits, and a
// long-lived connection would be state the daemon has to reason about across
// requests for nothing.
func (s *Server) serve(conn net.Conn) {
	defer func() { _ = conn.Close() }()

	line, err := bufio.NewReader(conn).ReadBytes('\n')
	if err != nil {
		return
	}

	var req Request
	if err := json.Unmarshal(line, &req); err != nil {
		_ = encode(conn, Response{Err: "the request is not readable: " + err.Error()})
		return
	}
	_ = encode(conn, s.answer(req))
}

// answer is the whole of what the daemon does.
func (s *Server) answer(req Request) Response {
	switch req.Op {
	case "ping":
		return Response{}
	case "append":
		if err := s.append(req); err != nil {
			return Response{Err: err.Error()}
		}
		return Response{}
	case "tasks":
		lines, err := s.tasks()
		if err != nil {
			return Response{Err: err.Error()}
		}
		return Response{Tasks: lines}
	default:
		return Response{Err: fmt.Sprintf("unknown operation %q", req.Op)}
	}
}

// append writes one event, which is the only write anything does.
func (s *Server) append(req Request) error {
	open, err := s.storeFor(req.Store)
	if err != nil {
		return err
	}
	event := store.Event{Action: req.Action, Payload: req.Payload}
	if req.After < 0 {
		return open.Append(req.TaskID, event)
	}
	return open.AppendAt(req.TaskID, req.After, event)
}

// tasks is the central view: every task in every project the daemon has opened.
//
// What it has opened rather than every project on the machine, and the difference
// is honest: the daemon learns about a project when something asks it about one.
// A listing that swept the disk would be reporting on repositories nobody in this
// session has touched.
func (s *Server) tasks() ([]TaskLine, error) {
	s.mu.Lock()
	open := make(map[string]*store.Store, len(s.stores))
	for path, st := range s.stores {
		open[path] = st
	}
	s.mu.Unlock()

	var lines []TaskLine
	for path, st := range open {
		ids, err := st.Tasks()
		if err != nil {
			return nil, fmt.Errorf("listing %s: %w", path, err)
		}
		for _, id := range ids {
			line := TaskLine{Store: path, TaskID: id}
			// A task whose flow this build cannot read is listed rather than
			// dropped: it is exactly the one somebody needs to hear about.
			if state, err := st.ReplayOwnFlow(id); err == nil {
				line.Stage, line.Status = string(state.Stage), string(state.Status)
			} else {
				line.Status = "unreadable"
			}
			lines = append(lines, line)
		}
	}
	return lines, nil
}

// storeFor opens a project's log once and keeps it.
//
// Kept because opening a SQLite database is not free and the daemon is asked
// repeatedly about the same few projects. One connection per store is what the
// store already enforces, so holding it is what makes the daemon the single
// writer rather than merely the usual one.
func (s *Server) storeFor(path string) (*store.Store, error) {
	if path == "" {
		return nil, errors.New("the request names no log")
	}

	s.mu.Lock()
	defer s.mu.Unlock()

	if open, ok := s.stores[path]; ok {
		return open, nil
	}
	open, err := store.OpenAs(path, store.LunaOwnsTheLog)
	if err != nil {
		return nil, fmt.Errorf("opening %s: %w", path, err)
	}
	s.stores[path] = open
	return open, nil
}
