package node

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
)

// Root finds the repository the log belongs to, starting from dir.
//
// The answer is the **main** repository, never the worktree the caller happens
// to be standing in. That distinction is the whole reason this function exists:
// a stage runs in an ephemeral worktree that is deleted when it ends,
// so a log resolved from the working directory would be created inside something
// designed to be thrown away — and the task it recorded would vanish with it.
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

// DefaultPath is where the log lives for the project containing dir.
//
// One log per **project**, outside every checkout of it. It was one per
// repository, in the main checkout, and both halves of that changed for the same
// reason: a task belongs to a project rather than to a directory, so a worktree
// opened to review one has to see it, and a second clone is the same work.
// `IdentifyProject` is what decides which project a directory is in.
//
// Outside the checkout because Luna writing into a repository is a change nobody
// asked for — and because a log inside a checkout is a log an agent working in
// that checkout can reach. Projects stay separate from one another: a directory
// each, not one file with a column, so two projects cannot list each other's
// gates through a query somebody got wrong.
//
// It names the store's file from the node package rather than the other way
// round, because finding it means running git — and running a process belongs
// here (.golangci.yaml enforces that boundary).
func DefaultPath(ctx context.Context, dir string) (string, error) {
	project, err := IdentifyProject(ctx, dir)
	if err != nil {
		return "", err
	}
	home, err := DataHome()
	if err != nil {
		return "", err
	}
	return filepath.Join(home, "projects", project.Key, "luna.db"), nil
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
