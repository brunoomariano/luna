package cli

import (
	"fmt"
	"os"
	"os/signal"
	"path/filepath"
	"syscall"

	"github.com/brunoomariano/luna/src/internal/daemon"
	"github.com/brunoomariano/luna/src/internal/node"
)

// DaemonSocket is where the daemon listens.
//
// Beside the handover directory rather than inside it: that directory is the one
// thing a contained agent is given, and an agent that could reach this socket
// could write the log.
func DaemonSocket() string {
	return filepath.Join(node.SocketDir(), daemon.SocketName)
}

// daemonCommand runs the daemon in the foreground until it is told to stop.
//
// Foreground because the thing that backgrounds it is whatever started it — a
// person with `&`, a service manager, or `daemon.Spawn` when a command found
// nobody listening. A process that daemonises itself is one whose failures happen
// somewhere nobody is looking.
func daemonCommand(env Env, args []string) error {
	socket := DaemonSocket()
	for i := 0; i+1 < len(args); i += 2 {
		if args[i] != "--socket" {
			return fmt.Errorf("%w: unknown flag %q (expected --socket)", ErrUsage, args[i])
		}
		socket = args[i+1]
	}

	server, err := daemon.Listen(socket)
	if err != nil {
		return err
	}
	fmt.Fprintf(env.Out, "luna daemon listening on %s\n", socket)

	// SIGTERM as well as interrupt: a service manager sends the first and a person
	// sends the second, and a daemon that only handled one would be killed rather
	// than closed in the other case — leaving its stores open and its socket
	// behind.
	stop := make(chan os.Signal, 1)
	signal.Notify(stop, os.Interrupt, syscall.SIGTERM)
	<-stop

	fmt.Fprintln(env.Out, "stopping")
	return server.Close()
}
