// Command luna is the CLI.
//
// It does as little as a main should: work out where the store lives, open it,
// hand the arguments to the cli package, and turn an error into an exit code.
package main

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"

	"github.com/brunoomariano/luna/src/internal/cli"
	"github.com/brunoomariano/luna/src/internal/fsm"
	"github.com/brunoomariano/luna/src/internal/herdr"
	"github.com/brunoomariano/luna/src/internal/interpret"
	"github.com/brunoomariano/luna/src/internal/node"
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
	ctx := context.Background()

	path, err := storePath(ctx)
	if err != nil {
		return err
	}

	// The config is read before the store is opened: a malformed config should
	// report itself rather than being discovered halfway through a command.
	cfg, err := cli.LoadConfig(cli.ConfigPath(path))
	if err != nil {
		return err
	}

	s, err := store.OpenAs(path, store.LunaOwnsTheLog)
	if err != nil {
		return err
	}
	defer func() { _ = s.Close() }()

	// The registry answers the one question the log cannot: what is happening in
	// another checkout (ADR-0054). It is resolved from the working directory
	// rather than from the log's path, because LUNA_STORE may point anywhere and
	// `bd` discovers its own database from the repository it is run in.
	//
	// Always constructed, never probed: a missing `bd` is reported by the command
	// that needed it rather than by every command that did not.
	cwd, err := os.Getwd()
	if err != nil {
		return fmt.Errorf("finding the working directory: %w", err)
	}
	root, err := node.Root(ctx, cwd)
	if err != nil {
		return err
	}

	// Running from inside a worktree works — the log resolves to the main
	// repository either way, which is the point of resolving it there. Saying so
	// matters anyway: a stage's worktree is deleted when the stage ends, and
	// someone who believes they are working in an isolated checkout should know
	// the state they are changing is shared (ADR-0057).
	if node.InsideAWorktree(ctx, cwd) {
		fmt.Fprintf(os.Stderr, "note: this is a worktree; the log and registry are %s\n", root)
	}

	// A project's own stages replace the shipped ones, if it has any (RFC-0003).
	// Decided here because this is where the repository is known: the engine may
	// not read a filesystem (ADR-0024), and the flow has to be settled before any
	// command reads it.
	stockDir := cli.StockDir(path)
	if files, ok := cli.ProjectStock(stockDir); ok {
		flow, err := fsm.LoadFlow(files, "stages")
		if err != nil {
			return fmt.Errorf("%s: %w", stockDir, err)
		}
		fsm.UseFlow(flow)
	}

	return cli.Run(environment(s, stockDir, cfg, root), args)
}

// environment assembles what every command is given.
//
// Extracted from run so a test can assert on it. That is not a style preference:
// `Lead` was declared, read in two places and set by nothing but tests, so
// `luna lead` reported "no lead is configured" on every real machine and a knob
// raised past a gate's criticality quietly sent it to a person. Both failed
// safe, neither said why, and no test could see it while this was a literal
// inside a function that also opens a database.
func environment(s *store.Store, stockDir string, cfg cli.Config, root string) cli.Env {
	// One harness, asked two ways: for an intent when a person types, and
	// directly when the lead conducts or judges (ADR-0043, ADR-0044).
	harness := interpret.Harness{Agent: cfg.Interpreter}

	return cli.Env{
		Store:  s,
		Stock:  stockDir,
		Config: cfg,
		Out:    os.Stdout,
		Err:    os.Stderr,
		In:     os.Stdin,
		Edit:   cli.Editor(cfg),
		// Luna hosts no model: the interpreter is one of the official harnesses
		// run non-interactively (ADR-0044).
		Interpret: harness,
		// The same harness, asked directly rather than for an intent. It is what
		// `luna lead` conducts with and what judges a gate the knob reached
		// (ADR-0043, RFC-0006).
		Lead: harness.Ask,

		// A block is only a block once someone knows. herdr already owns a
		// notification layer and is already what a person is looking at, so this
		// delegates rather than growing a transport of its own (INV-core-8).
		Notify: herdr.NewNotifier().Blocked,
	}
}

// storePath is where the log lives: LUNA_STORE if set, else `.luna/luna.db` in
// the **main** repository containing the working directory.
//
// The main repository and not the working directory, which is the correction
// (ADR-0057). A stage runs in an ephemeral worktree that is deleted when the
// stage ends (ADR-0055), so resolving from the cwd put a second log inside
// something built to be thrown away — and the task it recorded went with it.
// That was measured, not theorised: running from a worktree produced two
// `.luna/luna.db` files with the task visible in only one.
//
// LUNA_STORE still wins, because a person who names a path means it.
func storePath(ctx context.Context) (string, error) {
	if fromEnv := os.Getenv("LUNA_STORE"); fromEnv != "" {
		return fromEnv, nil
	}

	cwd, err := os.Getwd()
	if err != nil {
		return "", fmt.Errorf("finding the working directory: %w", err)
	}
	return node.DefaultPath(ctx, cwd)
}
