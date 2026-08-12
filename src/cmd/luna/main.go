// Command luna is the CLI.
//
// It does as little as a main should: work out where the store lives, open it,
// hand the arguments to the cli package, and turn an error into an exit code.
package main

import (
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"

	"github.com/brunoomariano/luna/src/internal/cli"
	"github.com/brunoomariano/luna/src/internal/herdr"
	"github.com/brunoomariano/luna/src/internal/interpret"
	"github.com/brunoomariano/luna/src/internal/store"
)

func main() {
	os.Exit(exitCode(run(os.Args[1:]), os.Stderr))
}

// exitCode turns an error into a status, and is separate from main so it can be
// tested: main itself calls os.Exit, which a test cannot survive.
//
// Usage errors and runtime failures exit differently so a script can tell "you
// typed it wrong" from "it went wrong".
func exitCode(err error, stderr io.Writer) int {
	if err == nil {
		return 0
	}
	fmt.Fprintln(stderr, err)

	if errors.Is(err, cli.ErrUsage) {
		return 2
	}
	return 1
}

func run(args []string) error {
	path, err := storePath()
	if err != nil {
		return err
	}

	// The config is read before the store is opened: a malformed config should
	// report itself rather than being discovered halfway through a command.
	cfg, err := cli.LoadConfig(cli.ConfigPath(path))
	if err != nil {
		return err
	}

	s, err := store.Open(path)
	if err != nil {
		return err
	}
	defer func() { _ = s.Close() }()

	return cli.Run(cli.Env{
		Store:  s,
		Config: cfg,
		Out:    os.Stdout,
		Err:    os.Stderr,
		In:     os.Stdin,
		Edit:   cli.Editor(cfg),
		// Luna hosts no model: the interpreter is one of the official harnesses
		// run non-interactively (ADR-0044).
		Interpret: interpret.Harness{Agent: cfg.Interpreter},
		// A block is only a block once someone knows. herdr already owns a
		// notification layer and is already what a person is looking at, so this
		// delegates rather than growing a transport of its own (INV-core-8).
		Notify: herdr.NewNotifier().Blocked,
	}, args)
}

// storePath is where the log lives: LUNA_STORE if set, else .luna/luna.db under
// the working directory. Per-directory rather than per-user because tasks belong
// to a project, and two projects sharing one log would list each other's gates.
func storePath() (string, error) {
	if fromEnv := os.Getenv("LUNA_STORE"); fromEnv != "" {
		return fromEnv, nil
	}

	cwd, err := os.Getwd()
	if err != nil {
		return "", fmt.Errorf("finding the working directory: %w", err)
	}
	return filepath.Join(cwd, ".luna", "luna.db"), nil
}
