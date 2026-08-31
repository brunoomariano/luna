package daemon

import (
	"os"
	"path/filepath"
	"testing"
	"time"
)

// TestTheDaemonStopsWhenItsDatabaseIsDeleted is a regression test for nineteen
// processes found alive on one machine.
//
// A daemon outlives the command that started it, deliberately, and nothing else
// ever tells it to stop. The suite starts one per end-to-end case in a temporary
// directory; the directory goes when the case ends and the daemon stays, holding
// a socket and a database nobody can reach. They accumulate for the life of the
// machine.
func TestTheDaemonStopsWhenItsDatabaseIsDeleted(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "luna.db")

	server, err := Listen(Options{
		Socket: filepath.Join(dir, "daemon.sock"),
		Store:  path,
		// Small, so the test measures the behaviour rather than the interval.
		StoreCheck: 10 * time.Millisecond,
	})
	if err != nil {
		t.Fatalf("starting the daemon: %v", err)
	}
	t.Cleanup(func() { _ = server.Close() })

	select {
	case <-server.Gone():
		t.Fatal("the daemon gave up on a database that is still there")
	case <-time.After(50 * time.Millisecond):
	}

	if err := os.Remove(path); err != nil {
		t.Fatalf("removing the database: %v", err)
	}

	select {
	case <-server.Gone():
	case <-time.After(2 * time.Second):
		t.Error("the database was deleted and the daemon went on serving it")
	}
}

// TestClosingReleasesTheWatcher. The watcher shares the wait group Close waits
// on, so a Close that did not release it would hang — which is worse than the
// leak this fixes, because it is the ordinary path.
func TestClosingReleasesTheWatcher(t *testing.T) {
	dir := t.TempDir()
	server, err := Listen(Options{
		Socket:     filepath.Join(dir, "daemon.sock"),
		Store:      filepath.Join(dir, "luna.db"),
		StoreCheck: time.Hour,
	})
	if err != nil {
		t.Fatalf("starting the daemon: %v", err)
	}

	closed := make(chan error, 1)
	go func() { closed <- server.Close() }()

	select {
	case err := <-closed:
		if err != nil {
			t.Errorf("closing: %v", err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("Close blocked, so the watcher is holding the wait group open")
	}

	// And twice, because a daemon told to stop while its database is going away
	// can reach this from both directions.
	if err := server.Close(); err != nil {
		t.Errorf("closing twice: %v", err)
	}
}

// TestASettingCrossesTheSocket. The daemon is the only writer, so a setting a
// command records has to reach the database through it — and come back to the
// next command that asks.
func TestASettingCrossesTheSocket(t *testing.T) {
	dir := t.TempDir()
	socket := filepath.Join(dir, "daemon.sock")
	server, err := Listen(Options{Socket: socket, Store: filepath.Join(dir, "luna.db")})
	if err != nil {
		t.Fatalf("starting the daemon: %v", err)
	}
	t.Cleanup(func() { _ = server.Close() })

	client := Client{Path: socket}
	if err := client.SetSetting("app-1", "workstream", "the-project"); err != nil {
		t.Fatalf("recording a setting: %v", err)
	}
	if err := client.SetSetting("", "editor", "hx"); err != nil {
		t.Fatalf("recording a machine setting: %v", err)
	}

	project, err := client.Settings("app-1")
	if err != nil {
		t.Fatalf("reading the project's settings: %v", err)
	}
	if project["workstream"] != "the-project" {
		t.Errorf("the project's settings came back as %v", project)
	}
	if _, stray := project["editor"]; stray {
		t.Errorf("a machine setting leaked into a project's scope: %v", project)
	}

	global, err := client.Settings("")
	if err != nil {
		t.Fatalf("reading the machine's settings: %v", err)
	}
	if global["editor"] != "hx" {
		t.Errorf("the machine's settings came back as %v", global)
	}

	// A scope nobody has configured answers an empty map rather than an error:
	// "nothing is set here" is an ordinary answer, and every project starts there.
	empty, err := client.Settings("app-2")
	if err != nil {
		t.Fatalf("reading an unconfigured project: %v", err)
	}
	if len(empty) != 0 {
		t.Errorf("an unconfigured project answered %v", empty)
	}
}
