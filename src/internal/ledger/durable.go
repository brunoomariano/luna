// Package ledger is the one record Luna keeps, outside every checkout.
//
// One line per event, appended and never rewritten. Where a run stands is its
// most recent line — read by tailing, not by folding every line from the
// beginning. See INV-2 for why replay was dropped and what that costs.
package ledger

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"syscall"
)

// ErrNotDurable is returned when the ledger's directory would not survive the
// process. See INV-4.
var ErrNotDurable = errors.New("the ledger is not on durable storage")

// tmpfsMagic identifies a tmpfs mount to statfs(2). It is the kernel's own
// constant, from <linux/magic.h>. It lives here rather than coming from
// golang.org/x/sys because Luna ships with no dependencies, and a guard that
// pulls in a module to read two integers is a supply chain bought for nothing.
const tmpfsMagic = 0x01021994

// ramfsMagic is tmpfs's older sibling. A ramfs mount fails the same way and is
// cheap to name, so a machine that uses one is not left with a silent hole.
const ramfsMagic = 0x858458f6

// RequireDurable refuses to let a caller write where a write would not survive.
//
// Luna does not build the environment it runs in. A person composes it before the
// agent starts — sandbox, durable memory, both, neither — so Luna inherits
// whatever that produced and cannot know it from the inside. Under a sandbox that
// gives the process a tmpfs $HOME, a ledger under $XDG_DATA_HOME is writable, is
// written, reports success, and evaporates. Both sides of that agree with each
// other and with nobody else, which is the worst failure available: it is silent.
//
// The signal is the filesystem rather than a heuristic about the environment.
// Three earlier signals were tried in this project for a related guard — the
// filesystem type read from a path, "a repository with no files", ".git with no
// objects" — and each either passed when it should have failed or broke a
// legitimate case. Asking the kernel what is mounted is the boring one that works.
//
// The directory is created first: statfs on a path that does not exist answers
// about nothing, and a first run has to be able to make its own ledger.
func RequireDurable(path string) error {
	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0o750); err != nil {
		return fmt.Errorf("making room for the ledger at %s: %w", dir, err)
	}

	var fs syscall.Statfs_t
	if err := syscall.Statfs(dir, &fs); err != nil {
		return fmt.Errorf("checking whether %s is durable: %w", dir, err)
	}

	switch int64(fs.Type) { //nolint:unconvert // Type is int64 on some arches and uint32 on others
	case tmpfsMagic, ramfsMagic:
		return fmt.Errorf("%w: %s is in memory, so anything written there is gone when this process is."+
			" If you are inside a sandbox, the ledger's directory has to be mapped read-write into it —"+
			" for ai-jail, that is `rw_maps` in the config, or `--rw-map %s` on the command line",
			ErrNotDurable, dir, dir)
	}
	return nil
}
