package fsm

import (
	"fmt"
	"strings"
)

// MaxTaskIDLen is how long a task id may be, and the number is not arbitrary.
//
// The id ends up inside an agent name, which herdr constrains to
// `[a-z][a-z0-9_-]{0,31}` — 32 characters, verified against a running server
// (ADR-0036). Luna builds that name as `luna-<id>-<stage>`, so the budget is:
//
//	32 - len("luna-") - len("-") - len(longest stage) = 32 - 5 - 1 - 12 = 14
//
// The longest shipped stage is `architecture`. A flow with a longer stage name
// would shrink this, which is why AuditFlowNames exists to say so rather than
// letting the truncation happen quietly.
const MaxTaskIDLen = agentNameLimit - len("luna-") - len("-") - longestShippedStage

// agentNameLimit is herdr's cap on an agent name: `[a-z][a-z0-9_-]{0,31}`, so 32
// characters including the first (ADR-0036).
const agentNameLimit = 32

// longestShippedStage is len("architecture"), the longest stage id in
// DefaultFlow(). A flow that brings a longer one shrinks the room left for a task
// id, which is what AuditFlowNames reports rather than letting it truncate.
const longestShippedStage = 12

// ErrInvalidTaskID is returned for an id Luna cannot carry through the system.
var ErrInvalidTaskID = fmt.Errorf("invalid task id")

// ValidateTaskID refuses an id that cannot survive what is done with it.
//
// A task id is not only a key. It becomes a directory name — `wt-<repo>-<id>`,
// joined onto a path (ADR-0037) — and part of an agent name in herdr. Both have
// requirements, and neither validated them: an id containing `..` composed a path
// somewhere else entirely, and a long one was silently truncated until two stages
// of the same task produced the same agent name and prompted each other's pane.
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
			"the id has to fit inside an agent name (%s)",
			ErrInvalidTaskID, id, len(id), MaxTaskIDLen, agentNameBudget)
	}

	for _, r := range id {
		if !idRune(r) {
			return fmt.Errorf("%w: %q contains %q — only letters, digits, %q and %q are allowed, "+
				"because the id becomes a directory name and part of an agent name",
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

// agentNameBudget explains the limit in the error rather than making the reader
// find this file.
const agentNameBudget = "luna- + id + - + stage, within herdr's 32-character limit"

// idRune reports whether a character may appear in a task id.
//
// Letters keep both cases: `LUNA-1` is what a tracker gives you, and refusing it
// would push every user into transcribing ids by hand. The agent name lowercases
// what it needs; the log keeps what was typed.
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
