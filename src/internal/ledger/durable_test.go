package ledger_test

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/brunoomariano/luna/src/internal/ledger"
)

// diskDir answers with a directory on real storage, or skips.
//
// Not t.TempDir(): on this machine — and on any systemd default — /tmp is itself
// tmpfs, so the obvious temporary directory is exactly what the guard refuses.
// That is the guard being right, and it is worth stating because the mistake is
// easy to make in the other direction and would have this test "fixed" by
// weakening what it guards.
func diskDir(t *testing.T) string {
	t.Helper()
	home, err := os.UserHomeDir()
	if err != nil {
		t.Skipf("no home directory to test against: %v", err)
	}
	dir, err := os.MkdirTemp(home, ".luna-durable-test-")
	if err != nil {
		t.Skipf("cannot write under home: %v", err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(dir) })

	// Home itself can be tmpfs — that is the situation inside a sandbox, and a
	// test asserting "disk is accepted" there would be asserting nothing.
	if err := ledger.RequireDurable(filepath.Join(dir, "probe")); err != nil {
		t.Skipf("home is not durable here, so there is nothing to contrast against: %v", err)
	}
	return dir
}

// tmpfsDir answers with a directory on a real in-memory filesystem, or skips.
//
// A real mount rather than a fake: the whole value of this guard is that it asks
// the kernel instead of trusting something Luna computed, so a test that fakes
// the answer tests the opposite of what is being guarded. /dev/shm is tmpfs on
// Linux and needs no privilege.
func tmpfsDir(t *testing.T) string {
	t.Helper()
	dir, err := os.MkdirTemp("/dev/shm", "luna-durable-")
	if err != nil {
		t.Skipf("no writable tmpfs to test against: %v", err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(dir) })
	return dir
}

func TestALedgerOnDiskIsAccepted(t *testing.T) {
	path := filepath.Join(diskDir(t), "luna", "ledger.jsonl")

	if err := ledger.RequireDurable(path); err != nil {
		t.Fatalf("a ledger on disk was refused: %v", err)
	}
	if _, err := os.Stat(filepath.Dir(path)); err != nil {
		t.Errorf("the directory was not created: %v", err)
	}
}

// The failure this exists for: inside a sandbox with a tmpfs $HOME, the write
// succeeds, reports success, and evaporates.
func TestALedgerInMemoryIsRefused(t *testing.T) {
	path := filepath.Join(tmpfsDir(t), "ledger.jsonl")

	err := ledger.RequireDurable(path)
	if err == nil {
		t.Fatal("a ledger on tmpfs was accepted; every write to it would be lost in silence")
	}
	if !errors.Is(err, ledger.ErrNotDurable) {
		t.Errorf("the refusal is not recognisable as a durability failure: %v", err)
	}
}

// A refusal that does not say what to do costs a debugging session. This one
// names the sandbox and the flag, because that is the situation it happens in.
func TestTheRefusalSaysHowToFixIt(t *testing.T) {
	dir := tmpfsDir(t)

	err := ledger.RequireDurable(filepath.Join(dir, "ledger.jsonl"))
	if err == nil {
		t.Fatal("expected a refusal")
	}
	for _, want := range []string{dir, "rw-map"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("the refusal does not mention %q: %v", want, err)
		}
	}
}

// The guard runs before the first write, so it has to work on a ledger that does
// not exist yet.
func TestADirectoryThatDoesNotExistYetIsCreatedAndChecked(t *testing.T) {
	path := filepath.Join(diskDir(t), "deep", "nested", "ledger.jsonl")

	if err := ledger.RequireDurable(path); err != nil {
		t.Fatalf("a first run could not make its own ledger: %v", err)
	}
}

// A wired guard is not a called guard. INV-5 records this shape twice: a
// dependency asserted to be *set* while the path that should use it never called
// it. Both writing verbs are held to calling this one, by writing where a write
// would be lost and demanding the refusal.
func TestTheWritingVerbsRefuseBeforeTheirFirstAppend(t *testing.T) {
	l := ledger.Ledger{Path: filepath.Join(tmpfsDir(t), "ledger.jsonl")}

	err := l.Append(ledger.Entry{Run: "MAX-2", Event: ledger.EventPhase})
	if err == nil {
		t.Fatal("Append wrote to a ledger in memory; every line would be lost in silence")
	}
	if !errors.Is(err, ledger.ErrNotDurable) {
		t.Errorf("Append failed for some other reason: %v", err)
	}
	if _, statErr := os.Stat(l.Path); statErr == nil {
		t.Error("the refused line was written anyway")
	}
}
