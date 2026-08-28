package node

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
)

// ErrTwoLogs is a project with a log in both places.
var ErrTwoLogs = errors.New("the project has a log in the repository and outside it")

// AdoptLogInRepo moves a log an older build left inside the checkout.
//
// The log used to live at `<repo>/.luna/luna.db` and now lives under
// `$XDG_DATA_HOME`. Leaving the old one where it is would start an empty log
// beside a task somebody has open, and every command would answer "no such task"
// about work that is right there — the silent kind of wrong this project spends
// most of its guards on.
//
// Moved rather than copied, so there is exactly one afterwards. A copy leaves two
// logs that drift, and the one still in the checkout is the one an agent working
// in that checkout can reach.
//
// Nothing happens in the ordinary case: no old log, or the new one already there.
func AdoptLogInRepo(repo, to string) error {
	from := filepath.Join(repo, ".luna", "luna.db")
	if _, err := os.Stat(from); err != nil {
		return nil
	}

	// Both present is not a case to resolve. One of them holds work and only the
	// person can say which, so the refusal names both and stops.
	if _, err := os.Stat(to); err == nil {
		return fmt.Errorf("%w: %s and %s — only one can be the log, and which holds "+
			"the work is not something Luna can tell. Move or remove the one you do "+
			"not want", ErrTwoLogs, from, to)
	}

	if err := os.MkdirAll(filepath.Dir(to), 0o750); err != nil {
		return fmt.Errorf("creating %s: %w", filepath.Dir(to), err)
	}
	if err := os.Rename(from, to); err != nil {
		// Across filesystems a rename fails and a copy would be needed. Reported
		// rather than attempted: a half-copied log is worse than a refused move,
		// and the person can move it with one command.
		return fmt.Errorf("moving the log out of the checkout (%s → %s): %w — "+
			"move it by hand and run again", from, to, err)
	}

	// The write-ahead log and its index travel with the database. Left behind they
	// are read by nothing and look like state somebody should keep.
	for _, suffix := range []string{"-wal", "-shm"} {
		_ = os.Rename(from+suffix, to+suffix)
	}
	return nil
}
