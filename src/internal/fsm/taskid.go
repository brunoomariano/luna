package fsm

import (
	"fmt"
	"strings"
)

// MaxTaskIDLen is how long a task id may be.
//
// The id is a component of two names, and the shorter of the two decides:
//
//   - a directory, `wt-<repo>-<id>-<role>`, a sibling of the repository
//     (node.OpenWorktree). One path component, so the filesystem's own 255-byte
//     limit applies to the whole of it — repository name and role included.
//   - a git branch, `luna/<task>/<role>` (node.stageBranch), stored as a path
//     under `.git/refs/heads/`, so the same component limit applies again.
//
// 64 is what is left over once the parts around the id are accounted for and a
// margin is kept for the repository and role names, which vary per project and
// which nothing here can measure. It is not a computed ceiling — it is a limit
// low enough that neither name can reach 255 and high enough that no tracker id
// anyone types comes close.
const MaxTaskIDLen = 64

// ErrInvalidTaskID is returned for an id Luna cannot carry through the system.
var ErrInvalidTaskID = fmt.Errorf("invalid task id")

// ValidateTaskID refuses an id that cannot survive what is done with it.
//
// A task id is not only a key. It becomes a directory name — `wt-<repo>-<id>-<role>`,
// joined onto a path — and a git branch, `luna/<task>/<role>`. Both have
// requirements, and neither validated them: an id containing `..` composed a path
// somewhere else entirely.
//
// The rule is the intersection of what both accept, which is narrow on purpose:
// an id is a short identifier a person types, and a project that wants prose has
// the briefing for it.
func ValidateTaskID(id string) error {
	if id == "" {
		return fmt.Errorf("%w: an id cannot be empty", ErrInvalidTaskID)
	}
	if len(id) > MaxTaskIDLen {
		return fmt.Errorf("%w: %q is %d characters and the limit is %d — "+
			"the id has to fit inside a directory name and a branch name (%s)",
			ErrInvalidTaskID, id, len(id), MaxTaskIDLen, taskIDBudget)
	}

	for _, r := range id {
		if !idRune(r) {
			return fmt.Errorf("%w: %q contains %q — only letters, digits, %q and %q are allowed, "+
				"because the id becomes a directory name and part of a branch name",
				ErrInvalidTaskID, id, r, '-', '_')
		}
	}

	// Checked after the character rule so the message about `..` never fires for
	// an id whose real problem is a slash.
	if strings.Contains(id, "..") {
		return fmt.Errorf("%w: %q contains %q, which would climb out of the directory "+
			"the worktree belongs in", ErrInvalidTaskID, id, "..")
	}
	return nil
}

// taskIDBudget explains the limit in the error rather than making the reader
// find this file.
const taskIDBudget = "wt-<repo>-<id>-<role> and luna/<id>/<role>, each one path component"

// idRune reports whether a character may appear in a task id.
//
// Letters keep both cases: `LUNA-1` is what a tracker gives you, and refusing it
// would push every user into transcribing ids by hand. The log keeps what was
// typed, and the names built from it carry the same case.
func idRune(r rune) bool {
	switch {
	case r >= 'a' && r <= 'z', r >= 'A' && r <= 'Z':
		return true
	case r >= '0' && r <= '9':
		return true
	case r == '-', r == '_':
		return true
	default:
		return false
	}
}
