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
	"github.com/brunoomariano/luna/src/internal/daemon"
	"github.com/brunoomariano/luna/src/internal/node"
	"github.com/brunoomariano/luna/src/internal/store"
)

var spawnDaemon = daemon.Spawn

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
type storeOpening struct {
	repo    string
	path    string
	chosen  bool
	project node.Project
}

func openStore(ctx context.Context) (*store.Store, cli.Config, string, error) {
	opening, err := resolveStoreOpening(ctx)
	if err != nil {
		return nil, cli.Config{}, "", err
	}
	client, err := connectDaemon(opening)
	if err != nil {
		return nil, cli.Config{}, "", err
	}

	s, err := store.OpenReadOnly(opening.path)
	if err != nil {
		return nil, cli.Config{}, "", err
	}
	s.Project = opening.project.Key
	s.Via = client

	// The settings come from the daemon, not from the file that seeded them. A
	// project whose file was imported and then changed through `luna config` reads
	// the change; one that still has the file on disk reads what was imported.
	cfg, err := settingsConfig(client, opening.project.Key)
	if err != nil {
		return nil, cli.Config{}, "", err
	}
	return s, cfg, opening.path, nil
}

func settingsConfig(client daemon.Client, project string) (cli.Config, error) {
	global, err := client.Settings("")
	if err != nil {
		return cli.Config{}, err
	}
	own, err := client.Settings(project)
	if err != nil {
		return cli.Config{}, err
	}
	return cli.ConfigFrom(global, own)
}

func resolveStoreOpening(ctx context.Context) (storeOpening, error) {
	cwd, err := os.Getwd()
	if err != nil {
		return storeOpening{}, err
	}
	repo, err := node.Root(ctx, cwd)
	if err != nil {
		return storeOpening{}, err
	}

	path, chosen, err := storePath(ctx)
	if err != nil {
		return storeOpening{}, err
	}
	if err := node.EnsureDurable(repo, filepath.Dir(path)); err != nil {
		return storeOpening{}, err
	}

	project, err := node.IdentifyProject(ctx, cwd)
	if err != nil {
		return storeOpening{}, err
	}
	return storeOpening{repo: repo, path: path, chosen: chosen, project: project}, nil
}

func connectDaemon(opening storeOpening) (daemon.Client, error) {
	legacyRoot := ""
	if !opening.chosen {
		legacyRoot = filepath.Join(filepath.Dir(opening.path), "projects")
	}
	client := daemon.Client{
		Path: cli.DaemonSocketFor(opening.path),
		Start: func() error {
			return spawnDaemon(cli.DaemonSocketFor(opening.path), opening.path, legacyRoot)
		},
	}
	if _, err := client.Do(daemon.Request{Op: "ping"}); err != nil {
		return daemon.Client{}, err
	}
	if err := importCheckoutStore(client, opening); err != nil {
		return daemon.Client{}, err
	}
	return client, nil
}

func importCheckoutStore(client daemon.Client, opening storeOpening) error {
	if opening.chosen {
		return nil
	}
	legacy := filepath.Join(opening.repo, ".luna", "luna.db")
	if _, err := os.Stat(legacy); err == nil {
		return client.ImportLegacy(opening.project.Key, legacy)
	} else if !errors.Is(err, os.ErrNotExist) {
		return fmt.Errorf("checking legacy store %s: %w", legacy, err)
	}
	return nil
}

func run(args []string) error {
	ctx := context.Background()
	if handled, err := runWithoutStore(args); handled {
		return err
	}

	s, cfg, path, err := openStore(ctx)
	if err != nil {
		return err
	}
	defer func() { _ = s.Close() }()

	root, err := currentRoot(ctx, s, path)
	if err != nil {
		return err
	}

	// Nothing is read from the project here any more. The flows are the ones
	// embedded in this binary, and a repository cannot override them — which is
	// what keeps every project on the same contract.
	return cli.Run(environment(s, cfg, root), args)
}

func runWithoutStore(args []string) (bool, error) {
	if len(args) == 0 {
		return false, nil
	}
	env := cli.Env{Out: os.Stdout, Err: os.Stderr, In: os.Stdin}
	switch args[0] {
	case "help", "-h", "--help", "version", "daemon":
		return true, cli.Run(env, args)
	case "artifact":
		return true, runInsideAStage(args)
	default:
		return false, nil
	}
}

func currentRoot(ctx context.Context, s *store.Store, path string) (string, error) {
	cwd, err := os.Getwd()
	if err != nil {
		return "", fmt.Errorf("finding the working directory: %w", err)
	}
	root, err := node.Root(ctx, cwd)
	if err != nil {
		return "", err
	}
	if node.InsideAWorktree(ctx, cwd) {
		fmt.Fprintf(os.Stderr, "note: this is a worktree; project %s shares task state in %s\n",
			s.Project, path)
	}
	return root, nil
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
		Store:       s,
		GlobalStore: s,
		Config:      cfg,
		Out:         os.Stdout,
		Err:         os.Stderr,
		In:          os.Stdin,
		Edit:        cli.Editor(cfg),
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
		// because it needs the repository and the flow first.
		// Injected like the rest so a solo run — which has no model to fake — can
		// be driven by a test at all.
		Node: cli.StageRunner,
	}
}

// storePath is the central log: LUNA_STORE if set, otherwise the one database
// under Luna's data home. Project identity scopes rows, not files.
//
// LUNA_STORE still wins, because a person who names a path means it.
func storePath(ctx context.Context) (path string, chosen bool, err error) {
	if fromEnv := os.Getenv("LUNA_STORE"); fromEnv != "" {
		path, err := filepath.Abs(fromEnv)
		if err != nil {
			return "", true, fmt.Errorf("resolving LUNA_STORE %q: %w", fromEnv, err)
		}
		return path, true, nil
	}

	cwd, err := os.Getwd()
	if err != nil {
		return "", false, fmt.Errorf("finding the working directory: %w", err)
	}
	path, err = node.DefaultPath(ctx, cwd)
	return path, false, err
}
