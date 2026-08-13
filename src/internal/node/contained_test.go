package node

import (
	"os"
	"path/filepath"
	"testing"
)

// The strings are the real ones, read off this machine: the first from the host,
// the second from inside `ai-jail bash`.
const (
	hostUIDMap = "         0          0 4294967295\n"
	jailUIDMap = "      1000          0          1\n"
)

func TestTheHostIsNotContained(t *testing.T) {
	if remapped(hostUIDMap) {
		t.Error("the host's own uid_map was read as a sandbox")
	}
}

func TestAUserNamespaceIsContained(t *testing.T) {
	if !remapped(jailUIDMap) {
		t.Error("ai-jail's uid_map was read as the bare host")
	}
}

// TestASeveralRangeMappingIsContained. A container runtime may map more than one
// range; a map that is not the host's single identity line is a namespace.
func TestASeveralRangeMappingIsContained(t *testing.T) {
	if !remapped("         0       1000          1\n         1     100000      65536\n") {
		t.Error("a multi-range mapping was read as the bare host")
	}
}

// TestAnUnreadableMapIsNotTreatedAsContained is the safe direction, and it is the
// one worth a test of its own: answering "contained" on a file it could not read
// would hand `bypassPermissions` to an agent on a plain host.
func TestAnUnreadableMapIsNotTreatedAsContained(t *testing.T) {
	uidMapPath = filepath.Join(t.TempDir(), "there-is-no-such-file")
	t.Cleanup(func() { uidMapPath = "/proc/self/uid_map" })

	if Contained() {
		t.Error("an unreadable uid_map was treated as a sandbox")
	}
}

// TestGarbageIsNotTreatedAsContained. Same direction, different cause: a map in a
// shape this does not understand must not become a grant.
func TestGarbageIsNotTreatedAsContained(t *testing.T) {
	path := filepath.Join(t.TempDir(), "uid_map")
	if err := os.WriteFile(path, []byte("not a mapping at all\n"), 0o600); err != nil {
		t.Fatalf("writing the fake map: %v", err)
	}
	uidMapPath = path
	t.Cleanup(func() { uidMapPath = "/proc/self/uid_map" })

	if Contained() {
		t.Error("an unparseable uid_map was treated as a sandbox")
	}
}

// TestContainedReadsTheRealFile covers the one line the others stub out.
func TestContainedReadsTheRealFile(t *testing.T) {
	if _, err := os.ReadFile(uidMapPath); err != nil {
		t.Skipf("no %s here: %v", uidMapPath, err)
	}
	// The value depends on where the suite runs, so the assertion is that it
	// agrees with the file rather than a fixed answer.
	raw, _ := os.ReadFile(uidMapPath)
	if Contained() != remapped(string(raw)) {
		t.Error("Contained disagrees with the file it reads")
	}
}
