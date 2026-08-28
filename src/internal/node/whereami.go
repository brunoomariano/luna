package node

import (
	"context"
	"fmt"
	"path/filepath"
	"strings"
)

// Identity is what a directory says about itself.
//
// It exists so a command does not have to be told what the caller is already
// standing in. Every field comes from git or from the path — nothing is read
// from a file Luna wrote, which is the point: a checkout describes itself, and a
// marker file would be a second thing to keep in step with the first.
type Identity struct {
	// Repo is the main repository, never the worktree the caller happens to be
	// in. Resolved with `--git-common-dir` for the reason Root explains.
	Repo string

	// RemoteURL is `origin`, empty when there is none. It is what identifies the
	// *project* across clones, where Repo identifies one checkout of it.
	RemoteURL string

	// Worktree is the checkout the caller is standing in, which equals Repo when
	// that is the main one.
	Worktree string

	// Linked reports a worktree that is not the main checkout — a stage's, in
	// Luna's own naming, or anyone else's.
	Linked bool

	// Branch is the checked-out branch, empty on a detached HEAD.
	Branch string

	// TaskID and Stage are what the checkout says it is working on, empty when
	// the branch does not name a task.
	TaskID string
	Stage  string
}

// Identify reads what a directory can say about itself without opening anything.
//
// A directory that is not a repository is not an error, and neither is one with
// no task: both are ordinary, and refusing them would make every command that
// wants to be helpful about the current directory fail everywhere else.
func Identify(ctx context.Context, dir string) (Identity, error) {
	root, err := Root(ctx, dir)
	if err != nil {
		return Identity{}, err
	}

	id := Identity{Repo: root, Worktree: root}
	if top, err := git(ctx, dir, "rev-parse", "--path-format=absolute", "--show-toplevel"); err == nil {
		id.Worktree = filepath.Clean(top)
		id.Linked = id.Worktree != filepath.Clean(root)
	}
	if url, err := git(ctx, dir, "remote", "get-url", "origin"); err == nil {
		id.RemoteURL = strings.TrimSpace(url)
	}
	if branch, err := git(ctx, dir, "rev-parse", "--abbrev-ref", "HEAD"); err == nil {
		id.Branch = strings.TrimSpace(branch)
	}

	id.TaskID, id.Stage = fromBranch(id.Branch)
	return id, nil
}

// fromBranch reads the task and stage out of `luna/<task>/<stage>`.
//
// The branch is the source rather than the directory name, and the choice is not
// arbitrary: a worktree can be moved or created by hand at any path, and the
// branch travels with the work. The directory is the cross-check rather than the
// authority — see Agrees.
//
// Anything that is not exactly three parts is somebody else's branch, which is
// the ordinary case in a repository people also work in normally.
func fromBranch(branch string) (task, stage string) {
	parts := strings.Split(branch, "/")
	if len(parts) != 3 || parts[0] != "luna" || parts[1] == "" || parts[2] == "" {
		return "", ""
	}
	return parts[1], parts[2]
}

// Agrees reports whether the directory name says the same thing as the branch,
// and what differs when it does not.
//
// Two sources for one fact is usually a defect, and here it is deliberate: they
// are written at different moments by different code, so a disagreement means
// something moved that should not have — a worktree renamed, a branch switched
// inside a checkout Luna made, a leftover directory reused for another task.
//
// It reports rather than resolves. Which of the two is right depends on what
// happened, and guessing would be the silent failure INV-5 names: the caller is
// told, and a person decides.
func (i Identity) Agrees() (bool, string) {
	if i.TaskID == "" || !i.Linked {
		// Nothing to cross-check: either the branch names no task, or this is the
		// main checkout, whose directory was never named after one.
		return true, ""
	}

	want, err := WorktreePath(i.Repo, i.TaskID, i.Stage)
	if err != nil {
		return true, ""
	}
	if filepath.Clean(want) == i.Worktree {
		return true, ""
	}
	return false, fmt.Sprintf(
		"the branch says task %q stage %q, which belongs in %s, and this is %s",
		i.TaskID, i.Stage, want, i.Worktree)
}
