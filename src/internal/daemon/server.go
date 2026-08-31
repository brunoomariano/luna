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
	"time"

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

	// StoreCheck is how often the daemon asks whether its database is still
	// there. Zero takes StoreCheckInterval; a test sets it small.
	StoreCheck time.Duration
}

// StoreCheckInterval is how long a daemon may go on serving a database that has
// been deleted.
//
// It exists because a daemon outlives the command that started it, deliberately,
// and nothing else ever tells it to stop. The test suite starts one per end-to-end
// case in a temporary directory, the directory is removed when the case ends, and
// the daemon stays: nineteen of them were found alive on one machine, each holding
// a socket and a database nobody could reach. A person deleting their data home
// leaves the same thing behind.
//
// Thirty seconds because nothing waits on it — the check costs one stat, and a
// daemon that lingers half a minute after its file is gone bothers nobody.
const StoreCheckInterval = 30 * time.Second

// Server is the daemon. It holds the central store open and is its only writer.
type Server struct {
	listener net.Listener
	store    *store.Store
	lock     *os.File
	wg       sync.WaitGroup

	// path is the database being served, kept so the watcher can ask whether it
	// is still there.
	path string

	// gone closes when it is not. Separate from stopping, because the two have
	// different callers: a signal stops the daemon from outside, and this is the
	// daemon noticing it has nothing left to serve.
	gone chan struct{}

	// stopping closes when Close is called, so the watcher does not outlive the
	// server it belongs to and hold Close's own wg.Wait open.
	stopping chan struct{}
	once     sync.Once
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
	s := &Server{
		listener: listener, store: central, lock: lock,
		path:     opts.Store,
		gone:     make(chan struct{}),
		stopping: make(chan struct{}),
	}
	s.wg.Add(2)
	go s.accept()
	go s.watchStore(opts.StoreCheck)
	return s, nil
}

// Gone closes when the database this daemon serves has been deleted.
//
// A channel rather than an exit, because the daemon does not own the decision to
// stop: whoever ran it does, and in the one command that does, stopping is the
// same close a signal takes.
func (s *Server) Gone() <-chan struct{} { return s.gone }

// watchStore closes Gone once the database is no longer on disk.
//
// Only os.ErrNotExist counts. A stat that fails for any other reason — a
// filesystem briefly unavailable, a permission that changed — is not evidence the
// database was deleted, and shutting down on it would turn a hiccup into an
// outage.
func (s *Server) watchStore(every time.Duration) {
	defer s.wg.Done()
	if every <= 0 {
		every = StoreCheckInterval
	}
	ticker := time.NewTicker(every)
	defer ticker.Stop()

	for {
		select {
		case <-s.stopping:
			return
		case <-ticker.C:
			if _, err := os.Stat(s.path); errors.Is(err, os.ErrNotExist) {
				close(s.gone)
				return
			}
		}
	}
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
//
// Idempotent, and that is not tidiness: there are two ways here now — a signal
// and a database that went away — and a second call that reported "use of closed
// network connection" would make a clean shutdown look like a failure.
//
// The stopping channel closes first. The watcher is in the same wait group, so a
// Close that did not release it would block on it forever.
func (s *Server) Close() error {
	var err error
	s.once.Do(func() {
		close(s.stopping)
		err = s.listener.Close()
		s.wg.Wait()
		_ = s.store.Close()
		_ = s.lock.Close()
	})
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
	case "set_setting":
		err = s.store.PutSetting(req.Project, req.Key, req.Value)
	default:
		// The reads answer with a body rather than an empty success, so they are
		// split out: keeping them here made one switch carry two shapes of answer,
		// which is what pushed this function past the complexity gate.
		return s.read(req)
	}
	if err != nil {
		return Response{Err: err.Error()}
	}
	return Response{}
}

// read answers the operations that come back with something rather than with
// nothing.
func (s *Server) read(req Request) Response {
	switch req.Op {
	case "settings":
		current, err := s.store.Settings(req.Project)
		if err != nil {
			return Response{Err: err.Error()}
		}
		return Response{Settings: current}
	default:
		return Response{Err: fmt.Sprintf("unknown operation %q", req.Op)}
	}
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
