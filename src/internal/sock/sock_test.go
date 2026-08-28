package sock

import (
	"net"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// TestAShortPathIsUsedAsItIs. Most sockets are nowhere near the limit, and
// reaching them through `/proc` would be indirection nobody asked for.
func TestAShortPathIsUsedAsItIs(t *testing.T) {
	path := filepath.Join("/tmp", "short.sock")

	got, done, err := Short(path)
	if err != nil {
		t.Fatalf("Short: %v", err)
	}
	defer done()

	if got != path {
		t.Errorf("a short path was rewritten to %q", got)
	}
}

// TestALongPathBindsThroughTheDescriptor is the workaround itself, driven
// against a real socket because binding is what the limit refuses.
//
// AF_UNIX caps an address at 108 bytes and fails both bind and connect with
// `invalid argument` — a message naming neither the path nor the limit, which is
// how this was missed twice.
func TestALongPathBindsThroughTheDescriptor(t *testing.T) {
	deep := filepath.Join("/tmp", strings.Repeat("a-long-directory-name/", 6))
	if err := os.MkdirAll(deep, 0o750); err != nil {
		t.Fatalf("digging: %v", err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(filepath.Join("/tmp", "a-long-directory-name")) })

	path := filepath.Join(deep, "x.sock")
	if len(path) <= PathLimit {
		t.Fatalf("the fixture is not long enough: %d bytes", len(path))
	}

	// Without the workaround, this is the failure.
	if _, err := net.Listen("unix", path); err == nil {
		t.Fatal("a path past the limit bound anyway — the fixture proves nothing")
	}

	bindable, done, err := Short(path)
	if err != nil {
		t.Fatalf("Short: %v", err)
	}
	defer done()

	listener, err := net.Listen("unix", bindable)
	if err != nil {
		t.Fatalf("a long path must still bind, got %v", err)
	}
	t.Cleanup(func() { _ = listener.Close() })

	// And the socket is really at the long path, rather than somewhere the
	// descriptor happened to point.
	if _, err := os.Stat(path); err != nil {
		t.Errorf("the socket is not where it was asked for: %v", err)
	}
}

// TestADirectoryThatCannotBeOpenedIsReportedAsItself. The short name refers to an
// open descriptor, so a directory that is gone has no descriptor to refer to —
// and saying "invalid argument" there would repeat the message this exists to
// replace.
func TestADirectoryThatCannotBeOpenedIsReportedAsItself(t *testing.T) {
	gone := filepath.Join("/tmp", "nowhere", strings.Repeat("a-long-directory-name/", 6), "x.sock")
	if len(gone) <= PathLimit {
		t.Fatalf("the fixture is short enough to be used as it is: %d bytes", len(gone))
	}

	_, _, err := Short(gone)

	if err == nil {
		t.Fatal("a path whose directory does not exist was shortened anyway")
	}
	if !strings.Contains(err.Error(), "socket's directory") {
		t.Errorf("the failure is reported as something else: %v", err)
	}
}
