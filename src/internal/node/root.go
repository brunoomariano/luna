package node

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
)

// Root finds the main repository for the checkout at dir.
//
// The answer is the **main** repository, never the worktree the caller happens
// to be standing in. Project identity, config and landing all belong to that
// stable checkout even though task state now lives in the central database.
//
// The mechanism is `git rev-parse --git-common-dir`, and the choice was measured
// rather than assumed. From inside a worktree:
//
//	--show-toplevel   → /repos/wt-app-LUNA-1-reviewer   (the worktree: wrong)
//	--git-common-dir  → /repos/app/.git                 (the main repo: right)
//
// `--show-toplevel` is the obvious call and it is the wrong one. It answers
// "which checkout am I in", which is exactly the question that must not decide
// where shared state lives.
//
// A directory that is not a repository is not an error. Luna runs in plain
// directories too, and the honest answer there is the directory itself.
func Root(ctx context.Context, dir string) (string, error) {
	common, err := git(ctx, dir, "rev-parse", "--path-format=absolute", "--git-common-dir")
	if err != nil {
		// Not a repository, or no git at all. The working directory is what a
		// project without one has, and refusing here would stop Luna working
		// anywhere that is not a checkout.
		return filepath.Abs(dir)
	}

	// --git-common-dir points at the `.git` directory of the main repository;
	// the root is its parent. A bare repository has no working tree to hold a
	// worktree anyway, so the parent is the right answer in every case Luna runs.
	return filepath.Dir(common), nil
}

// DefaultPath is the one central log, independent of dir.
//
// Project identity scopes rows inside the database rather than choosing another
// file. The argument stays in the signature because callers resolve paths and
// project identity from the same directory, but moving between checkouts cannot
// move the log any more.
//
// It names the store's file from the node package rather than the other way
// round, because finding it means running git — and running a process belongs
// here (.golangci.yaml enforces that boundary).
func DefaultPath(ctx context.Context, dir string) (string, error) {
	_, _ = ctx, dir
	home, err := DataHome()
	if err != nil {
		return "", err
	}
	return filepath.Join(home, "luna.db"), nil
}

// DataHome is where Luna keeps what belongs to the person rather than to a
// repository.
//
// `$XDG_DATA_HOME` when it is set and absolute, `~/.local/share/luna` otherwise.
// A relative value is refused rather than resolved against the working directory:
// the point of this path is that it does not depend on where a command was run.
func DataHome() (string, error) {
	if set := os.Getenv("XDG_DATA_HOME"); set != "" {
		if !filepath.IsAbs(set) {
			return "", fmt.Errorf("XDG_DATA_HOME is %q, which is relative — "+
				"the log's location cannot depend on the working directory", set)
		}
		return filepath.Join(set, "luna"), nil
	}

	home, err := os.UserHomeDir()
	if err != nil {
		return "", fmt.Errorf("finding the home directory for the log: %w", err)
	}
	return filepath.Join(home, ".local", "share", "luna"), nil
}

// InsideAWorktree reports whether dir is a linked worktree rather than the main
// checkout.
//
// This is the signal Luna can actually get, and it is checked rather than
// assumed: from a linked worktree `--show-toplevel` and the parent of
// `--git-common-dir` disagree, and in the main checkout they are the same path.
// No environment variable is involved, which matters — an agent inherits and can
// unset the environment, and it cannot make git lie about where it is standing.
//
// It is not a security boundary. An agent that wants to write the log can `cd`
// out of its worktree. What it catches is the case that actually happens: an
// agent following its brief, running `luna` because that is what it was told to
// do, and appending to a log inside a directory built to be deleted.
func InsideAWorktree(ctx context.Context, dir string) bool {
	top, err := git(ctx, dir, "rev-parse", "--path-format=absolute", "--show-toplevel")
	if err != nil {
		return false
	}

	root, err := Root(ctx, dir)
	if err != nil {
		return false
	}
	return filepath.Clean(top) != filepath.Clean(root)
}
