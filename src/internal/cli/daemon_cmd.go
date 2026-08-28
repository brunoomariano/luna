package cli

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"os"
	"os/signal"
	"path/filepath"
	"syscall"

	"github.com/brunoomariano/luna/src/internal/daemon"
	"github.com/brunoomariano/luna/src/internal/node"
)

// DaemonSocketFor binds one daemon to one database. The stable suffix prevents
// a daemon serving another file — including one from an older build — from
// answering this database's ping.
func DaemonSocketFor(database string) string {
	sum := sha256.Sum256([]byte(filepath.Clean(database)))
	name := "daemon-" + hex.EncodeToString(sum[:4]) + ".sock"
	return filepath.Join(node.SocketDir(), name)
}

// daemonCommand runs the daemon in the foreground until it is told to stop.
//
// Foreground because the thing that backgrounds it is whatever started it — a
// person with `&`, a service manager, or `daemon.Spawn` when a command found
// nobody listening. A process that daemonises itself is one whose failures happen
// somewhere nobody is looking.
func daemonCommand(env Env, args []string) error {
	opts, err := daemonOptions(args)
	if err != nil {
		return err
	}
	server, err := daemon.Listen(opts)
	if err != nil {
		return err
	}
	fmt.Fprintf(env.Out, "luna daemon listening on %s\n", opts.Socket)

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

func daemonOptions(args []string) (daemon.Options, error) {
	home, err := node.DataHome()
	if err != nil {
		return daemon.Options{}, err
	}
	opts := daemon.Options{
		Store: filepath.Join(home, "luna.db"), LegacyRoot: filepath.Join(home, "projects"),
	}
	if chosen := os.Getenv("LUNA_STORE"); chosen != "" {
		opts.Store = chosen
		opts.LegacyRoot = ""
	}
	for i := 0; i+1 < len(args); i += 2 {
		if err := daemonFlag(&opts, args[i], args[i+1]); err != nil {
			return daemon.Options{}, err
		}
	}
	if len(args)%2 != 0 {
		return daemon.Options{}, fmt.Errorf("%w: %s needs a value", ErrUsage, args[len(args)-1])
	}
	opts.Store, err = absoluteStore(opts.Store)
	if err != nil {
		return daemon.Options{}, err
	}
	if opts.Socket == "" {
		opts.Socket = DaemonSocketFor(opts.Store)
	}
	return opts, nil
}

func daemonFlag(opts *daemon.Options, name, value string) error {
	switch name {
	case "--socket":
		opts.Socket = value
	case "--store":
		opts.Store = value
	case "--legacy-root":
		opts.LegacyRoot = value
	default:
		return fmt.Errorf("%w: unknown daemon flag %q", ErrUsage, name)
	}
	return nil
}

func absoluteStore(database string) (string, error) {
	absolute, err := filepath.Abs(database)
	if err != nil {
		return "", fmt.Errorf("resolving the central store %q: %w", database, err)
	}
	return absolute, nil
}
