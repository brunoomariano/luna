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
	"syscall"

	"github.com/brunoomariano/luna/src/internal/sock"
	"github.com/brunoomariano/luna/src/internal/store"
)

// SocketName is the daemon's own socket, beside the handover directory rather
// than inside it.
const SocketName = "daemon.sock"

// Options names the daemon's socket, its one database, and the directory that
// may still contain former per-project stores.
type Options struct {
	Socket     string
	Store      string
	LegacyRoot string
}

// Server is the daemon. It holds the central store open and is its only writer.
type Server struct {
	listener net.Listener
	store    *store.Store
	lock     *os.File
	wg       sync.WaitGroup
}

// Listen opens and migrates the central database before exposing its socket.
func Listen(opts Options) (*Server, error) {
	if opts.Store == "" {
		return nil, errors.New("the daemon needs one central store")
	}
	lock, err := lockStore(opts.Store)
	if err != nil {
		return nil, err
	}
	central, err := store.OpenAs(opts.Store, store.LunaOwnsTheLog)
	if err != nil {
		_ = lock.Close()
		return nil, err
	}
	if err := importLegacyRoot(central, opts.LegacyRoot); err != nil {
		_ = central.Close()
		_ = lock.Close()
		return nil, err
	}

	listener, err := listenSocket(opts.Socket)
	if err != nil {
		_ = central.Close()
		_ = lock.Close()
		return nil, err
	}
	s := &Server{listener: listener, store: central, lock: lock}
	s.wg.Add(1)
	go s.accept()
	return s, nil
}

func lockStore(path string) (*os.File, error) {
	if err := os.MkdirAll(filepath.Dir(path), 0o750); err != nil {
		return nil, fmt.Errorf("making room for the daemon lock: %w", err)
	}
	lock, err := os.OpenFile(path+".lock", os.O_CREATE|os.O_RDWR, 0o600) //nolint:gosec // the database path is operator-selected
	if err != nil {
		return nil, fmt.Errorf("opening the daemon lock for %s: %w", path, err)
	}
	if err := syscall.Flock(int(lock.Fd()), syscall.LOCK_EX|syscall.LOCK_NB); err != nil {
		_ = lock.Close()
		if errors.Is(err, syscall.EWOULDBLOCK) {
			return nil, fmt.Errorf("the central store %s already has a daemon", path)
		}
		return nil, fmt.Errorf("locking the central store %s: %w", path, err)
	}
	return lock, nil
}

func listenSocket(path string) (net.Listener, error) {
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return nil, fmt.Errorf("making room for the daemon socket: %w", err)
	}
	if err := os.Remove(path); err != nil && !errors.Is(err, os.ErrNotExist) {
		return nil, fmt.Errorf("clearing the daemon socket at %s: %w", path, err)
	}
	bindable, done, err := sock.Short(path)
	if err != nil {
		return nil, err
	}
	defer done()
	listener, err := net.Listen("unix", bindable)
	if err != nil {
		return nil, fmt.Errorf("opening the daemon socket at %s: %w", path, err)
	}
	return listener, nil
}

// Close stops the daemon and releases its one database handle.
func (s *Server) Close() error {
	err := s.listener.Close()
	s.wg.Wait()
	_ = s.store.Close()
	_ = s.lock.Close()
	return err
}

func (s *Server) accept() {
	defer s.wg.Done()
	for {
		conn, err := s.listener.Accept()
		if err != nil {
			return
		}
		s.wg.Add(1)
		go func() {
			defer s.wg.Done()
			s.serve(conn)
		}()
	}
}

// serve answers one request. A CLI process connects once, asks once, and exits.
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

func (s *Server) answer(req Request) Response {
	var err error
	switch req.Op {
	case "ping":
		return Response{}
	case "append":
		err = s.append(req)
	case "put_blob":
		err = s.putBlob(req)
	case "forget_blobs":
		err = s.forgetBlobs(req)
	case "import":
		err = s.importLegacy(req)
	case "tasks":
		lines, listErr := s.tasks()
		if listErr != nil {
			return Response{Err: listErr.Error()}
		}
		return Response{Tasks: lines}
	default:
		return Response{Err: fmt.Sprintf("unknown operation %q", req.Op)}
	}
	if err != nil {
		return Response{Err: err.Error()}
	}
	return Response{}
}

func (s *Server) append(req Request) error {
	project, err := s.project(req)
	if err != nil {
		return err
	}
	event := store.Event{Action: req.Action, Payload: req.Payload}
	if req.After < 0 {
		return project.Append(req.TaskID, event)
	}
	return project.AppendAt(req.TaskID, req.After, event)
}

func (s *Server) putBlob(req Request) error {
	project, err := s.project(req)
	if err != nil {
		return err
	}
	if req.Blob == nil {
		return errors.New("the request carries no artifact")
	}
	return project.PutBlob(*req.Blob)
}

func (s *Server) forgetBlobs(req Request) error {
	project, err := s.project(req)
	if err != nil {
		return err
	}
	return project.ForgetBlobs(req.TaskID)
}

func (s *Server) importLegacy(req Request) error {
	if req.Project == "" || req.Legacy == "" {
		return errors.New("a legacy import needs a project and a path")
	}
	return importAndArchive(s.store, req.Project, req.Legacy)
}

func (s *Server) project(req Request) (*store.Store, error) {
	if req.Project == "" {
		return nil, errors.New("the request names no project")
	}
	return s.store.ForProject(req.Project), nil
}

func (s *Server) tasks() ([]TaskLine, error) {
	refs, err := s.store.TaskRefs()
	if err != nil {
		return nil, err
	}
	lines := make([]TaskLine, 0, len(refs))
	for _, ref := range refs {
		line := TaskLine{Project: ref.Project, TaskID: ref.ID}
		if state, err := s.store.ForProject(ref.Project).ReplayOwnFlow(ref.ID); err == nil {
			line.Stage, line.Status = string(state.Stage), string(state.Status)
		} else {
			line.Status = "unreadable"
		}
		lines = append(lines, line)
	}
	return lines, nil
}

func importLegacyRoot(central *store.Store, root string) error {
	if root == "" {
		return nil
	}
	entries, err := os.ReadDir(root)
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	if err != nil {
		return fmt.Errorf("reading legacy stores in %s: %w", root, err)
	}
	for _, entry := range entries {
		if !entry.IsDir() {
			continue
		}
		path := filepath.Join(root, entry.Name(), "luna.db")
		if _, err := os.Stat(path); errors.Is(err, os.ErrNotExist) {
			continue
		} else if err != nil {
			return fmt.Errorf("reading legacy store %s: %w", path, err)
		}
		if err := importAndArchive(central, entry.Name(), path); err != nil {
			return err
		}
	}
	return nil
}

func importAndArchive(central *store.Store, project, path string) error {
	if err := central.ImportLegacy(project, path); err != nil {
		return err
	}
	if err := os.Rename(path, path+".migrated"); err != nil {
		return fmt.Errorf("archiving imported store %s: %w", path, err)
	}
	for _, suffix := range []string{"-wal", "-shm"} {
		from, to := path+suffix, path+".migrated"+suffix
		if err := os.Rename(from, to); err != nil && !errors.Is(err, os.ErrNotExist) {
			return fmt.Errorf("archiving imported store %s: %w", from, err)
		}
	}
	return nil
}
