package daemon

import (
	"bufio"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"os"
	"os/exec"
	"syscall"
	"time"

	"github.com/brunoomariano/luna/src/internal/sock"
)

// ErrNoDaemon is a daemon that is not there and could not be started.
var ErrNoDaemon = errors.New("no daemon is listening and one could not be started")

// Client talks to the daemon, starting one if there is none.
type Client struct {
	// Path is the socket to reach.
	Path string

	// Store is the log this client's appends are about.
	//
	// Carried on the client rather than passed per call, because it is a property
	// of where the command is running: one process serves one project, and the
	// daemon serves whichever ones ask.
	Store string

	// Start launches a daemon. Injected so a test can drive the client without a
	// second process, and so the one place that spawns anything is visible.
	//
	// Nil means never start one, which is what a caller inside a sandbox wants:
	// there, failing to connect is the correct answer and spawning a daemon that
	// writes to a tmpfs is the failure this whole design removes.
	Start func() error
}

// Do sends one request, starting a daemon first if nothing answers.
//
// The retry is the whole of the auto-start: connect, and if there is nobody
// there, start one and connect again. A second failure is reported rather than
// retried — a daemon that will not come up does not come up on the third try
// either, and a loop here is the invisible wait INV-5 refuses.
func (c Client) Do(req Request) (Response, error) {
	resp, err := c.once(req)
	if err == nil {
		return resp, nil
	}

	// Only a connection that could not be made means there is nobody there. A
	// daemon that answered badly, or refused the request, has to be reported as
	// itself — reporting it as "no daemon" sends whoever reads it looking for a
	// process that is running.
	if !errors.Is(err, syscall.ENOENT) && !errors.Is(err, syscall.ECONNREFUSED) {
		return Response{}, err
	}
	if c.Start == nil {
		return Response{}, fmt.Errorf("%w: %s", ErrNoDaemon, c.Path)
	}
	if startErr := c.Start(); startErr != nil {
		return Response{}, fmt.Errorf("%w: %w", ErrNoDaemon, startErr)
	}
	if err := c.waitFor(); err != nil {
		return Response{}, err
	}
	return c.once(req)
}

// once is one connection, one request, one answer.
func (c Client) once(req Request) (Response, error) {
	dialable, done, err := sock.Short(c.Path)
	if err != nil {
		return Response{}, err
	}
	defer done()

	conn, err := net.DialTimeout("unix", dialable, 2*time.Second)
	if err != nil {
		return Response{}, fmt.Errorf("reaching the daemon at %s: %w", c.Path, err)
	}
	defer func() { _ = conn.Close() }()

	if err := encode(conn, req); err != nil {
		return Response{}, err
	}
	line, err := bufio.NewReader(conn).ReadBytes('\n')
	if err != nil {
		return Response{}, fmt.Errorf("reading the daemon's answer: %w", err)
	}

	var resp Response
	if err := json.Unmarshal(line, &resp); err != nil {
		return Response{}, fmt.Errorf("the daemon's answer is not readable: %w", err)
	}
	if resp.Err != "" {
		return Response{}, errors.New(resp.Err)
	}
	return resp, nil
}

// waitFor blocks until a freshly started daemon is listening.
//
// Bounded, and short: a daemon that has not bound its socket in two seconds is
// not starting. Polling rather than a signal because the thing being waited on is
// a socket appearing, and a socket appearing is what a connect tests.
func (c Client) waitFor() error {
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		if _, err := c.once(Request{Op: "ping"}); err == nil {
			return nil
		}
		time.Sleep(20 * time.Millisecond)
	}
	return fmt.Errorf("%w: it did not answer within two seconds", ErrNoDaemon)
}

// Spawn starts a detached daemon running this same binary.
//
// The same binary rather than a name on the PATH: a `luna` that started some
// other `luna` would be a version skew nobody asked for, and the one running is
// the one the person meant.
//
// Detached, so the CLI that started it can exit. Its output goes nowhere on
// purpose — a daemon writing to the terminal of whichever command happened to
// start it is noise attached to the wrong process.
func Spawn(socket string) error {
	self, err := os.Executable()
	if err != nil {
		return fmt.Errorf("finding this binary to start a daemon: %w", err)
	}

	cmd := exec.Command(self, "daemon", "--socket", socket) //nolint:gosec // this binary, by path
	cmd.Stdin, cmd.Stdout, cmd.Stderr = nil, nil, nil
	// The root, and not the socket's directory: that one is the daemon's to create
	// and does not exist yet on a first run — `exec` needs `Dir` to be there and
	// fails before the process starts. Not the caller's directory either, which is
	// often a stage's worktree and is deleted when the stage ends.
	cmd.Dir = string(os.PathSeparator)
	if err := cmd.Start(); err != nil {
		return fmt.Errorf("starting a daemon: %w", err)
	}
	// Released rather than waited on: this process is about to run a command and
	// exit, and the daemon outlives it.
	return cmd.Process.Release()
}

// AppendEvent satisfies store.Appender, which is how every command in the CLI
// keeps calling `Append` while the daemon is the only process writing.
func (c Client) AppendEvent(taskID string, after int, action, payload string) error {
	_, err := c.Do(Request{
		Op: "append", Store: c.Store, TaskID: taskID,
		Action: action, Payload: payload, After: after,
	})
	return err
}
