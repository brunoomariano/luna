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
	"path/filepath"

	"github.com/brunoomariano/luna/src/internal/agent"
	"github.com/brunoomariano/luna/src/internal/cli"
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

// insideAStage reports whether the command is one an agent runs from within a
// stage's sandbox, where the log is deliberately unreachable.
//
// The agent hands its work to Luna through a socket and Luna is the only writer,
// so these need nothing the sandbox denies them.
func insideAStage(args []string) bool {
	return len(args) > 0 && args[0] == "artifact"
}

// runInsideAStage answers without resolving the store at all, because resolving
// it is exactly what fails inside the sandbox.
//
// Measured: an agent produced its contract, could not deliver it, and reported
// that `luna` exited 1 on every subcommand because the log directory was outside
// its mount. The guard doing the refusing was working as designed and aimed at
// the wrong command.
func runInsideAStage(args []string) error {
	return cli.Run(cli.Env{Out: os.Stdout, Err: os.Stderr, In: os.Stdin}, args)
}

// openStore resolves the log, refuses an unreachable one, reads the config and
// opens the database — the prologue every command outside a stage shares.
//
// Grouped because the order is the meaning: the guard runs before anything is
// opened, since opening is what hides the problem. A log on a filesystem the
// caller cannot really reach answers every read and write and keeps none of
// them, so the failure has to be caught while there is still nothing to lose.
func openStore(ctx context.Context) (s *store.Store, cfg cli.Config, path string, err error) {
	cwd, err := os.Getwd()
	if err != nil {
		return nil, cli.Config{}, "", err
	}
	repo, err := node.Root(ctx, cwd)
	if err != nil {
		return nil, cli.Config{}, "", err
	}

	var chosen bool
	path, chosen, err = storePath(ctx)
	if err != nil {
		return nil, cli.Config{}, "", err
	}
	// A log left in the checkout by an older build is moved rather than ignored,
	// because ignoring it would silently start an empty one beside a task somebody
	// has open.
	//
	// Never when the path was chosen with `LUNA_STORE`. Somebody naming a location
	// is not asking for a log somewhere else to be moved into it, and doing it
	// anyway would take a repository's real log away during a test that only meant
	// to point at a scratch file — which is exactly what happened the first time
	// this was written without the check.
	if !chosen {
		if err := node.AdoptLogInRepo(repo, path); err != nil {
			return nil, cli.Config{}, "", err
		}
	}
	if err := node.EnsureDurable(repo, filepath.Dir(path)); err != nil {
		return nil, cli.Config{}, "", err
	}

	// The config is read before the store is opened: a malformed config should
	// report itself rather than being discovered halfway through a command.
	cfg, err = cli.LoadConfig(cli.ConfigPath(repo))
	if err != nil {
		return nil, cli.Config{}, "", err
	}

	s, err = store.OpenAs(path, store.LunaOwnsTheLog)
	if err != nil {
		return nil, cli.Config{}, "", err
	}
	return s, cfg, path, nil
}

func run(args []string) error {
	ctx := context.Background()

	if insideAStage(args) {
		return runInsideAStage(args)
	}

	s, cfg, _, err := openStore(ctx)
	if err != nil {
		return err
	}
	defer func() { _ = s.Close() }()

	// The registry answers the one question the log cannot: what is happening in
	// another checkout. It is resolved from the working directory
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
	// the state they are changing is shared.
	if node.InsideAWorktree(ctx, cwd) {
		fmt.Fprintf(os.Stderr, "note: this is a worktree; the log and registry are %s\n", root)
	}

	// Nothing is read from the project here any more. The flows are the ones
	// embedded in this binary, and a repository cannot override them — which is
	// what keeps every project on the same contract.
	return cli.Run(environment(s, cfg, root), args)
}

// environment assembles what every command is given.
//
// Extracted from run so a test can assert on it. That is not a style preference:
// `Lead` was declared, read in two places and set by nothing but tests, so
// `luna lead` reported "no lead is configured" on every real machine and a knob
// raised past a gate's autonomy floor quietly sent it to a person. Both failed
// safe, neither said why, and no test could see it while this was a literal
// inside a function that also opens a database.
func environment(s *store.Store, cfg cli.Config, root string) cli.Env {
	// The harness the lead asks when it judges a gate. It is not the one that
	// runs a stage: that one is built per stage in the node layer, inside the
	// sandbox, from the stage's own agent.
	harness := agent.Harness{Kind: cfg.LeadHarness}

	return cli.Env{
		Store:  s,
		Config: cfg,
		Out:    os.Stdout,
		Err:    os.Stderr,
		In:     os.Stdin,
		Edit:   cli.Editor(cfg),
		// Resolved on demand rather than at startup: it runs git, and most commands
		// are given an id and never ask.
		Where: func() (node.Identity, error) {
			cwd, err := os.Getwd()
			if err != nil {
				return node.Identity{}, err
			}
			return node.Identify(context.Background(), cwd)
		},
		// Luna hosts no model: judging a gate goes out to an official harness run
		// non-interactively, the same way a stage does.
		Lead: harness.Ask,

		// Pointing a finished task's branch. Injected like the rest so a test can
		// watch it happen: the landing broke twice without any test seeing it, once
		// with the field unwired and once with it wired and no loop calling it.
		Land: func(ctx context.Context, taskID, commit string) error {
			return node.Land(ctx, ".", taskID, commit)
		},

		// A block is only a block once someone knows. An external terminal
		// multiplexer already owns a notification layer and is already what a
		// person is looking at, so this delegates rather than growing a transport
		// of its own.
		Notify: node.NewNotifier().Blocked,

		// What runs a stage: a contained agent in its own worktree, built per run
		// because it needs the repository, the flow and the role table first.
		// Injected like the rest so a solo run — which has no model to fake — can
		// be driven by a test at all.
		Node: cli.StageRunner,
	}
}

// storePath is where the log lives: LUNA_STORE if set, else `.luna/luna.db` in
// the **main** repository containing the working directory.
//
// The main repository and not the working directory, which is the correction a
// real run forced. A stage runs in an ephemeral worktree that is deleted when the
// stage ends, so resolving from the cwd put a second log inside
// something built to be thrown away — and the task it recorded went with it.
// That was measured, not theorised: running from a worktree produced two
// `.luna/luna.db` files with the task visible in only one.
//
// LUNA_STORE still wins, because a person who names a path means it.
func storePath(ctx context.Context) (path string, chosen bool, err error) {
	if fromEnv := os.Getenv("LUNA_STORE"); fromEnv != "" {
		return fromEnv, true, nil
	}

	cwd, err := os.Getwd()
	if err != nil {
		return "", false, fmt.Errorf("finding the working directory: %w", err)
	}
	path, err = node.DefaultPath(ctx, cwd)
	return path, false, err
}
