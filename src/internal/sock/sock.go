// Package sock carries the one thing every unix socket in Luna has to work
// around.
//
// It is a package rather than a helper in whichever package needed it first,
// because two of them need it and the second found out the expensive way: the
// handover socket was fixed for this limit and the daemon's was not, so a runtime
// directory long enough to trip it answered `connect: invalid argument` — a
// message naming neither the path nor the limit, which is exactly how the first
// occurrence was missed.
package sock

import (
	"fmt"
	"os"
	"path/filepath"
)

// PathLimit is what AF_UNIX gives a socket path: 108 bytes on Linux, including
// the terminating NUL. A path at or past it fails bind *and* connect with
// `invalid argument`.
const PathLimit = 107

// Short returns a name for the socket that fits, and a function to release it.
//
// A path already short enough is returned as it is. A longer one is reached
// through `/proc/self/fd`, which names the open directory rather than the path to
// it — the descriptor is a handful of bytes whatever the real path costs.
//
// The returned closer has to outlive the bind or the connect: the descriptor is
// what the short name refers to, and closing it early makes the name meaningless.
func Short(path string) (string, func(), error) {
	if len(path) <= PathLimit {
		return path, func() {}, nil
	}

	dir, err := os.Open(filepath.Dir(path))
	if err != nil {
		return "", nil, fmt.Errorf("opening the socket's directory for %s: %w", path, err)
	}
	return fmt.Sprintf("/proc/self/fd/%d/%s", dir.Fd(), filepath.Base(path)),
		func() { _ = dir.Close() }, nil
}
