package cli

import (
	"os"
	"path/filepath"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/brunoomariano/luna/src/internal/daemon"
	"github.com/brunoomariano/luna/src/internal/node"
)

// TestTheDaemonSocketIsNotWhereAContainedAgentCanReach is the placement, and it
// is the whole of what keeps an agent out of the log.
//
// The handover directory is the one thing the sandbox is asked to expose. A
// daemon socket inside it would be reachable by every contained agent, and an
// agent that can reach it can append to the log — which is not corrupting a file,
// it is fabricating history.
func TestTheDaemonSocketIsNotWhereAContainedAgentCanReach(t *testing.T) {
	t.Setenv("XDG_RUNTIME_DIR", t.TempDir())

	socket := DaemonSocket()

	if strings.HasPrefix(socket, node.HandoverDir()) {
		t.Errorf("the daemon socket is inside the directory a jail is given: %s", socket)
	}
	if filepath.Dir(socket) != node.SocketDir() {
		t.Errorf("the daemon socket is not beside the handover directory: %s", socket)
	}
}

// TestTheDaemonRunsUntilItIsToldToStop covers the command itself: it binds, it
// says where, and it closes on a signal rather than being killed with its stores
// open and its socket left behind.
func TestTheDaemonRunsUntilItIsToldToStop(t *testing.T) {
	dir, err := os.MkdirTemp("/tmp", "luna-dc-")
	if err != nil {
		t.Fatalf("making a directory: %v", err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(dir) })

	h := newHarness(t)
	socket := filepath.Join(dir, daemon.SocketName)

	done := make(chan error, 1)
	go func() { done <- daemonCommand(h.env, []string{"--socket", socket}) }()

	client := daemon.Client{Path: socket}
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		if _, err := client.Do(daemon.Request{Op: "ping"}); err == nil {
			break
		}
		time.Sleep(20 * time.Millisecond)
	}
	if _, err := client.Do(daemon.Request{Op: "ping"}); err != nil {
		t.Fatalf("the daemon never came up: %v", err)
	}

	if err := syscall.Kill(syscall.Getpid(), syscall.SIGTERM); err != nil {
		t.Fatalf("signalling: %v", err)
	}
	select {
	case err := <-done:
		if err != nil {
			t.Errorf("the daemon did not stop cleanly: %v", err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("the daemon did not stop when told")
	}

	if !strings.Contains(h.out.String(), socket) {
		t.Errorf("the daemon did not say where it is listening:\n%s", h.out.String())
	}
}

// TestAnUnknownDaemonFlagIsRefused, for the reason every unknown flag is: a
// misspelled `--socket` would start a daemon somewhere nobody is looking.
func TestAnUnknownDaemonFlagIsRefused(t *testing.T) {
	h := newHarness(t)

	err := daemonCommand(h.env, []string{"--sockett", "/tmp/x.sock"})

	if err == nil {
		t.Fatal("a misspelled flag started a daemon anyway")
	}
	if !strings.Contains(err.Error(), "--sockett") {
		t.Errorf("the refusal does not name what was written: %v", err)
	}
}
