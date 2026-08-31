package daemon

import (
	"bufio"
	"encoding/json"
	"errors"
	"net"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/brunoomariano/luna/src/internal/fsm"
	"github.com/brunoomariano/luna/src/internal/store"
)

// running starts a daemon on a short socket path and returns a client for it.
func running(t *testing.T) (Client, string) {
	t.Helper()

	// Short: AF_UNIX caps an address at 108 bytes and a test temp directory can be
	// most of that on its own.
	dir, err := os.MkdirTemp("/tmp", "luna-d-")
	if err != nil {
		t.Fatalf("making a runtime directory: %v", err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(dir) })

	socket := filepath.Join(dir, SocketName)
	database := filepath.Join(dir, "luna.db")
	server, err := Listen(Options{Socket: socket, Store: database})
	if err != nil {
		t.Fatalf("starting the daemon: %v", err)
	}
	t.Cleanup(func() { _ = server.Close() })

	return Client{Path: socket}, dir
}

// TestTheDaemonIsTheOneThatWrites is the whole point of the process boundary.
//
// The ownership rule was a constant checked inside one process. A store opened
// read-only refuses to append; forwarding to the daemon is what lets every
// command keep calling `Append` while exactly one process holds the file.
func TestTheDaemonIsTheOneThatWrites(t *testing.T) {
	client, dir := running(t)

	// A read-only store refuses on its own.
	central, err := store.OpenReadOnly(filepath.Join(dir, "luna.db"))
	if err != nil {
		t.Fatalf("opening read-only: %v", err)
	}
	t.Cleanup(func() { _ = central.Close() })
	reader := central.ForProject("test-project")

	err = reader.AppendAction("D-1", fsm.TaskCreated{
		Kind: fsm.KindChore, Flow: fsm.Fingerprint(fsm.DefaultFlow()),
	})
	if !errors.Is(err, store.ErrNotTheOwner) {
		t.Fatalf("a read-only store wrote the log: %v", err)
	}

	// And accepts once it knows who owns it.
	reader.Via = client
	if err := reader.AppendAction("D-1", fsm.TaskCreated{
		Kind: fsm.KindChore, Flow: fsm.Fingerprint(fsm.DefaultFlow()),
	}); err != nil {
		t.Fatalf("forwarding to the daemon: %v", err)
	}

	// The event is in the file, which is the claim: the daemon wrote it, and the
	// reader can see it.
	events, err := reader.Events("D-1")
	if err != nil {
		t.Fatalf("reading back: %v", err)
	}
	if len(events) != 1 || events[0].Action != "TaskCreated" {
		t.Errorf("the daemon's write did not land: %+v", events)
	}
}

// TestTheConditionalAppendSurvivesTheSocket. The window between reading a state
// and appending from it belongs to the caller — the daemon cannot see it, so the
// sequence has to travel with the request.
//
// Without this, two decisions taken from one state both land, and the log replays
// into an illegal transition forever with no repair available.
func TestTheConditionalAppendSurvivesTheSocket(t *testing.T) {
	client, dir := running(t)
	central, err := store.OpenReadOnly(filepath.Join(dir, "luna.db"))
	if err != nil {
		t.Fatalf("opening: %v", err)
	}
	t.Cleanup(func() { _ = central.Close() })
	reader := central.ForProject("test-project")
	reader.Via = client

	created := fsm.TaskCreated{Kind: fsm.KindChore, Flow: fsm.Fingerprint(fsm.DefaultFlow())}
	if err := reader.AppendAction("D-2", created); err != nil {
		t.Fatalf("seeding: %v", err)
	}

	// A decision taken from seq 1 lands.
	if err := reader.AppendActionAt("D-2", 1, fsm.Advance{Flow: fsm.DefaultFlow()}); err != nil {
		t.Fatalf("the first decision from seq 1: %v", err)
	}
	// A second decision taken from the same state does not.
	err = reader.AppendActionAt("D-2", 1, fsm.Advance{Flow: fsm.DefaultFlow()})
	if err == nil {
		t.Fatal("two decisions from one state both landed")
	}
}

// TestEveryProjectCoexistsInTheCentralStore is what one database for every
// project has to hold: two tasks with different projects, each replaying on its
// own, neither seeing the other.
func TestEveryProjectCoexistsInTheCentralStore(t *testing.T) {
	client, dir := running(t)

	database := filepath.Join(dir, "luna.db")

	for _, project := range []string{"test-project", "other-project"} {
		central, err := store.OpenReadOnly(database)
		if err != nil {
			t.Fatalf("opening: %v", err)
		}
		reader := central.ForProject(project)
		reader.Via = client
		if err := reader.AppendAction("T-"+project, fsm.TaskCreated{
			Kind: fsm.KindChore, Flow: fsm.Fingerprint(fsm.DefaultFlow()),
		}); err != nil {
			t.Fatalf("seeding %s: %v", project, err)
		}
		_ = central.Close()
	}

	// Read the way the CLI reads it: off its own read-only handle, across every
	// project. The daemon owns the writing; the listing never asked it anything.
	central, err := store.OpenReadOnly(database)
	if err != nil {
		t.Fatalf("opening: %v", err)
	}
	t.Cleanup(func() { _ = central.Close() })

	refs, err := central.TaskRefs()
	if err != nil {
		t.Fatalf("listing: %v", err)
	}
	if len(refs) != 2 {
		t.Fatalf("want a task from each project, got %+v", refs)
	}
	seen := map[string]bool{}
	for _, ref := range refs {
		seen[ref.Project] = true
		if _, err := central.ForProject(ref.Project).ReplayOwnFlow(ref.ID); err != nil {
			t.Errorf("%s/%s does not replay: %v", ref.Project, ref.ID, err)
		}
	}
	if len(seen) != 2 {
		t.Errorf("both projects should appear, got %v", seen)
	}
}

// TestAnUnknownOperationIsRefused. A daemon that ignored what it did not
// recognise would answer success to a client from a newer build asking for
// something it cannot do.
func TestAnUnknownOperationIsRefused(t *testing.T) {
	client, _ := running(t)

	_, err := client.Do(Request{Op: "delete-everything"})

	if err == nil {
		t.Fatal("an unknown operation was accepted")
	}
}

// TestWithNoDaemonAndNoWayToStartOneTheFailureIsLoud.
//
// This is the case the whole boundary exists for: a `luna` inside a sandbox
// cannot reach the daemon, and what it must not do is quietly write somewhere
// that evaporates. Failing to connect is the loud answer.
func TestWithNoDaemonAndNoWayToStartOneTheFailureIsLoud(t *testing.T) {
	client := Client{Path: filepath.Join(t.TempDir(), "nobody.sock")}

	_, err := client.Do(Request{Op: "ping"})

	if !errors.Is(err, ErrNoDaemon) {
		t.Fatalf("want a refusal naming the missing daemon, got %v", err)
	}
}

// TestADaemonThatWillNotStartIsReportedOnce. A retry loop here would be the
// invisible wait INV-5 refuses: a daemon that does not come up does not come up
// on the third attempt either.
func TestADaemonThatWillNotStartIsReportedOnce(t *testing.T) {
	attempts := 0
	client := Client{
		Path:  filepath.Join(t.TempDir(), "nobody.sock"),
		Start: func() error { attempts++; return errors.New("no") },
	}

	if _, err := client.Do(Request{Op: "ping"}); err == nil {
		t.Fatal("a client with no daemon succeeded")
	}
	if attempts != 1 {
		t.Errorf("the daemon was started %d times, want once", attempts)
	}
}

// TestASocketLeftByAKilledDaemonIsCleared. Bind fails on an existing path, and a
// daemon that refused to start because the last one was killed would need a
// person to clean up after a crash.
func TestASocketLeftByAKilledDaemonIsCleared(t *testing.T) {
	dir, err := os.MkdirTemp("/tmp", "luna-d-")
	if err != nil {
		t.Fatalf("making a directory: %v", err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(dir) })

	socket := filepath.Join(dir, SocketName)
	if err := os.WriteFile(socket, []byte("what a killed daemon leaves"), 0o600); err != nil {
		t.Fatalf("simulating: %v", err)
	}

	server, err := Listen(Options{Socket: socket, Store: filepath.Join(dir, "luna.db")})
	if err != nil {
		t.Fatalf("a daemon must start over a stale socket, got %v", err)
	}
	if err := server.Close(); err != nil {
		t.Fatalf("closing: %v", err)
	}
}

// TestARequestNamingNoProjectIsRefused. Task ids are project-local, so guessing
// a project would make the daemon append to somebody else's history.
func TestARequestNamingNoProjectIsRefused(t *testing.T) {
	client, _ := running(t)

	_, err := client.Do(Request{Op: "append", TaskID: "X-1", Action: "TaskCreated", After: -1})

	if err == nil {
		t.Fatal("an append naming no project was accepted")
	}
}

// TestAClientStartsTheDaemonItNeeds is the auto-start, and it is what keeps
// `luna` feeling like a single command after the writer became a second process.
//
// The whole retry is exercised: connect, find nobody, start one, wait for it to
// bind, connect again. Nothing about that is visible to the person running a
// command, which is the point.
func TestAClientStartsTheDaemonItNeeds(t *testing.T) {
	dir, err := os.MkdirTemp("/tmp", "luna-d-")
	if err != nil {
		t.Fatalf("making a directory: %v", err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(dir) })

	socket := filepath.Join(dir, SocketName)
	started := 0
	client := Client{
		Path: socket,
		Start: func() error {
			started++
			server, err := Listen(Options{Socket: socket, Store: filepath.Join(dir, "luna.db")})
			if err != nil {
				return err
			}
			t.Cleanup(func() { _ = server.Close() })
			return nil
		},
	}

	// Nothing is listening, so the first call has to bring one up and then work.
	if _, err := client.Do(Request{Op: "ping"}); err != nil {
		t.Fatalf("the client did not start the daemon it needed: %v", err)
	}
	if started != 1 {
		t.Fatalf("the daemon was started %d times, want once", started)
	}

	// And the second call finds it, rather than starting another.
	if _, err := client.Do(Request{Op: "ping"}); err != nil {
		t.Fatalf("the second call: %v", err)
	}
	if started != 1 {
		t.Errorf("a running daemon was started again (%d times)", started)
	}
}

// TestADaemonThatStartsAndNeverBindsIsGivenUpOn. The wait is bounded because an
// unbounded one is the invisible wait INV-5 refuses — a command that hangs
// forever tells nobody anything.
func TestADaemonThatStartsAndNeverBindsIsGivenUpOn(t *testing.T) {
	client := Client{
		Path:  filepath.Join(t.TempDir(), "never.sock"),
		Start: func() error { return nil }, // claims success, binds nothing
	}

	_, err := client.Do(Request{Op: "ping"})

	if !errors.Is(err, ErrNoDaemon) {
		t.Fatalf("want it given up on, got %v", err)
	}
}

// TestSpawnStartsThisBinaryAndReleasesIt is the auto-start's other half, driven
// against a real process because that is what it does.
//
// The same binary rather than a name on the PATH: a `luna` that started some
// other `luna` would be a version skew nobody asked for. Released rather than
// waited on, because the command that started it is about to exit.
func TestSpawnStartsThisBinaryAndReleasesIt(t *testing.T) {
	// The test binary is what `os.Executable` returns here, and it does not know
	// the `daemon` subcommand — so it exits rather than serving. What this covers
	// is that a process is started and let go, not that it becomes a daemon.
	dir, err := os.MkdirTemp("/tmp", "luna-sp-")
	if err != nil {
		t.Fatalf("making a directory: %v", err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(dir) })

	if err := Spawn(filepath.Join(dir, SocketName), filepath.Join(dir, "luna.db"), ""); err != nil {
		t.Fatalf("Spawn: %v", err)
	}
}

// TestAnUnreadableRequestIsAnswered rather than dropped. A client that sent
// something malformed and got silence would wait on a connection the daemon had
// already closed, and the failure would look like a hang.
func TestAnUnreadableRequestIsAnswered(t *testing.T) {
	client, _ := running(t)

	conn, err := net.Dial("unix", client.Path)
	if err != nil {
		t.Fatalf("dialling: %v", err)
	}
	defer func() { _ = conn.Close() }()

	if _, err := conn.Write([]byte("not json at all\n")); err != nil {
		t.Fatalf("writing: %v", err)
	}
	line, err := bufio.NewReader(conn).ReadBytes('\n')
	if err != nil {
		t.Fatalf("the daemon said nothing about a request it could not read: %v", err)
	}

	var resp Response
	if err := json.Unmarshal(line, &resp); err != nil {
		t.Fatalf("the answer is not readable either: %v", err)
	}
	if resp.Err == "" {
		t.Error("an unreadable request was answered with success")
	}
}

// TestADaemonCannotBindWhereThereIsNoRoom covers Listen's own refusals: the
// directory cannot be made, or the stale path cannot be cleared.
func TestADaemonCannotBindWhereThereIsNoRoom(t *testing.T) {
	dir, err := os.MkdirTemp("/tmp", "luna-nr-")
	if err != nil {
		t.Fatalf("making a directory: %v", err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(dir) })

	// The socket's directory taken by a file.
	blocked := filepath.Join(dir, "blocked")
	if err := os.WriteFile(blocked, []byte("a file"), 0o600); err != nil {
		t.Fatalf("setting up: %v", err)
	}
	if _, err := Listen(Options{
		Socket: filepath.Join(blocked, SocketName), Store: filepath.Join(dir, "luna.db"),
	}); err == nil {
		t.Error("a daemon bound where its directory could not be made")
	}

	// The socket's own path as a non-empty directory.
	occupied := filepath.Join(dir, SocketName)
	if err := os.MkdirAll(filepath.Join(occupied, "in the way"), 0o750); err != nil {
		t.Fatalf("setting up: %v", err)
	}
	if _, err := Listen(Options{Socket: occupied, Store: filepath.Join(dir, "other.db")}); err == nil {
		t.Error("a daemon bound over a path it could not clear")
	}
}

// TestATaskWhoseFlowChangedIsListedRatherThanDropped.
//
// A log written under a flow this build no longer has cannot be replayed, and
// that task is exactly the one somebody needs to hear about. Dropping it from the
// central view would hide the only symptom.
func TestATaskWhoseFlowChangedIsListedRatherThanDropped(t *testing.T) {
	client, dir := running(t)

	central, err := store.OpenReadOnly(filepath.Join(dir, "luna.db"))
	if err != nil {
		t.Fatalf("opening: %v", err)
	}
	t.Cleanup(func() { _ = central.Close() })
	reader := central.ForProject("test-project")
	reader.Via = client

	// A fingerprint no flow in this build has.
	if err := reader.AppendAction("GONE-1", fsm.TaskCreated{
		Kind: fsm.KindChore, Flow: "0123456789abcdef",
	}); err != nil {
		t.Fatalf("seeding: %v", err)
	}

	refs, err := central.TaskRefs()
	if err != nil {
		t.Fatalf("listing: %v", err)
	}

	var found bool
	for _, ref := range refs {
		if ref.ID != "GONE-1" {
			continue
		}
		found = true
		if _, err := central.ForProject(ref.Project).ReplayOwnFlow(ref.ID); err == nil {
			t.Error("a task naming a flow this build has no longer should not replay")
		}
	}
	if !found {
		t.Errorf("the one task that needs reporting was dropped: %+v", refs)
	}
}

// TestAnUnreadableAnswerIsReportedAsItself. A daemon from a different build could
// answer something this one cannot parse, and reporting that as "the request
// failed" would send whoever reads it looking in the wrong place.
func TestAnUnreadableAnswerIsReportedAsItself(t *testing.T) {
	dir, err := os.MkdirTemp("/tmp", "luna-ua-")
	if err != nil {
		t.Fatalf("making a directory: %v", err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(dir) })

	socket := filepath.Join(dir, SocketName)
	listener, err := net.Listen("unix", socket)
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
		_, _ = bufio.NewReader(conn).ReadBytes('\n')
		_, _ = conn.Write([]byte("this is not a response\n"))
	}()

	_, err = Client{Path: socket}.Do(Request{Op: "ping"})

	if err == nil {
		t.Fatal("an unreadable answer was taken as success")
	}
	if !strings.Contains(err.Error(), "not readable") {
		t.Errorf("the failure is reported as something else: %v", err)
	}
}

// TestAnAnswerThatNeverArrivesIsReported. A daemon that accepts a connection and
// says nothing leaves the client reading a socket that will never speak — and a
// command that waits forever tells nobody anything (INV-5).
func TestAnAnswerThatNeverArrivesIsReported(t *testing.T) {
	dir, err := os.MkdirTemp("/tmp", "luna-na-")
	if err != nil {
		t.Fatalf("making a directory: %v", err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(dir) })

	socket := filepath.Join(dir, SocketName)
	listener, err := net.Listen("unix", socket)
	if err != nil {
		t.Fatalf("listening: %v", err)
	}
	t.Cleanup(func() { _ = listener.Close() })

	go func() {
		conn, err := listener.Accept()
		if err != nil {
			return
		}
		_ = conn.Close() // accepted, then hung up without answering
	}()

	if _, err := (Client{Path: socket}).Do(Request{Op: "ping"}); err == nil {
		t.Fatal("a connection that said nothing was taken as success")
	}
}

// TestSpawnReportsWhatItCannotStart. A daemon that could not be launched has to
// say so: the command that needed it is about to report "no daemon", and the
// reason it could not be started is the only useful half of that.
func TestSpawnReportsWhatItCannotStart(t *testing.T) {
	// A socket under a path that is a file, so the daemon's own directory cannot
	// be made — the failure surfaces when it tries to bind rather than here, so
	// what this covers is that Spawn itself returns.
	blocked := filepath.Join(t.TempDir(), "a-file")
	if err := os.WriteFile(blocked, []byte("x"), 0o600); err != nil {
		t.Fatalf("setting up: %v", err)
	}

	client := Client{
		Path: filepath.Join(blocked, SocketName),
		Start: func() error {
			return Spawn(filepath.Join(blocked, SocketName), filepath.Join(t.TempDir(), "luna.db"), "")
		},
	}

	if _, err := client.Do(Request{Op: "ping"}); err == nil {
		t.Fatal("a daemon that cannot bind was taken as running")
	}
}

// TestAClientHangingUpWithoutAskingIsNotAnError. A connection opened and dropped
// is what a killed command leaves behind, and a daemon that treated it as a
// failure would report one for every interrupted `luna`.
func TestAClientHangingUpWithoutAskingIsNotAnError(t *testing.T) {
	client, _ := running(t)

	conn, err := net.Dial("unix", client.Path)
	if err != nil {
		t.Fatalf("dialling: %v", err)
	}
	if err := conn.Close(); err != nil {
		t.Fatalf("closing: %v", err)
	}

	// And the daemon is still there afterwards, which is the point: one dropped
	// connection does not take it down.
	if _, err := client.Do(Request{Op: "ping"}); err != nil {
		t.Errorf("the daemon did not survive a dropped connection: %v", err)
	}
}

// TestDiallingAPathThatCannotBeNamedIsReported.
//
// A long path is reached through a descriptor for the directory holding it, so a
// directory that is not there has nothing to open — and the client has to say
// that rather than passing an empty name down to the syscall.
//
// Only the client: the daemon makes the directory before it needs to name it, so
// the same branch there is one nothing ordinary reaches.
func TestDiallingAPathThatCannotBeNamedIsReported(t *testing.T) {
	gone := filepath.Join("/tmp", "nowhere-at-all", strings.Repeat("a-long-directory-name/", 6), SocketName)
	if len(gone) <= 107 {
		t.Fatalf("the fixture is short enough to be dialled directly: %d bytes", len(gone))
	}

	if _, err := (Client{Path: gone}).Do(Request{Op: "ping"}); err == nil {
		t.Error("a client dialled a path it could not name")
	}
}
