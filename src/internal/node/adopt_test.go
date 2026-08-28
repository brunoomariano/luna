package node

import (
	"errors"
	"os"
	"path/filepath"
	"testing"
)

// withLogInRepo writes what an older build left behind.
func withLogInRepo(t *testing.T, body string) string {
	t.Helper()

	repo := t.TempDir()
	dir := filepath.Join(repo, ".luna")
	if err := os.MkdirAll(dir, 0o750); err != nil {
		t.Fatalf("making the old log directory: %v", err)
	}
	if err := os.WriteFile(filepath.Join(dir, "luna.db"), []byte(body), 0o600); err != nil {
		t.Fatalf("writing the old log: %v", err)
	}
	return repo
}

// TestALogLeftInTheCheckoutIsMovedRatherThanIgnored.
//
// Ignoring it starts an empty log beside a task somebody has open, and every
// command then answers "no such task" about work that is right there — the silent
// kind of wrong the rest of this system spends its guards on.
func TestALogLeftInTheCheckoutIsMovedRatherThanIgnored(t *testing.T) {
	repo := withLogInRepo(t, "the work")
	to := filepath.Join(t.TempDir(), "projects", "k", "luna.db")

	if err := AdoptLogInRepo(repo, to); err != nil {
		t.Fatalf("AdoptLogInRepo: %v", err)
	}

	body, err := os.ReadFile(to)
	if err != nil {
		t.Fatalf("the log did not arrive: %v", err)
	}
	if string(body) != "the work" {
		t.Errorf("the log arrived changed: %q", body)
	}
	// Moved, not copied: two logs drift, and the one still in the checkout is the
	// one an agent working there can reach.
	if _, err := os.Stat(filepath.Join(repo, ".luna", "luna.db")); err == nil {
		t.Error("the old log is still in the checkout")
	}
}

// TestTwoLogsAreRefusedRatherThanMerged. One of them holds the work and only the
// person can say which, so the refusal names both and stops.
func TestTwoLogsAreRefusedRatherThanMerged(t *testing.T) {
	repo := withLogInRepo(t, "the old work")
	to := filepath.Join(t.TempDir(), "luna.db")
	if err := os.WriteFile(to, []byte("the new work"), 0o600); err != nil {
		t.Fatalf("writing the new log: %v", err)
	}

	err := AdoptLogInRepo(repo, to)

	if !errors.Is(err, ErrTwoLogs) {
		t.Fatalf("two logs were resolved anyway: %v", err)
	}
	// And neither was touched, because the refusal is the whole action.
	body, _ := os.ReadFile(to)
	if string(body) != "the new work" {
		t.Errorf("the refusal wrote something: %q", body)
	}
	if _, err := os.Stat(filepath.Join(repo, ".luna", "luna.db")); err != nil {
		t.Error("the refusal removed the old log")
	}
}

// TestNothingHappensWithoutAnOldLog is the ordinary case — every run after the
// first, and every project that never had one.
func TestNothingHappensWithoutAnOldLog(t *testing.T) {
	to := filepath.Join(t.TempDir(), "luna.db")

	if err := AdoptLogInRepo(t.TempDir(), to); err != nil {
		t.Fatalf("a repository with no old log was not left alone: %v", err)
	}
	if _, err := os.Stat(to); err == nil {
		t.Error("adoption created a log where there was nothing to adopt")
	}
}

// TestTheWriteAheadLogTravelsWithTheDatabase. Left behind, `-wal` and `-shm` are
// read by nothing and look like state somebody should keep.
func TestTheWriteAheadLogTravelsWithTheDatabase(t *testing.T) {
	repo := withLogInRepo(t, "the work")
	for _, suffix := range []string{"-wal", "-shm"} {
		if err := os.WriteFile(filepath.Join(repo, ".luna", "luna.db"+suffix), []byte("x"), 0o600); err != nil {
			t.Fatalf("writing %s: %v", suffix, err)
		}
	}
	to := filepath.Join(t.TempDir(), "luna.db")

	if err := AdoptLogInRepo(repo, to); err != nil {
		t.Fatalf("AdoptLogInRepo: %v", err)
	}

	for _, suffix := range []string{"-wal", "-shm"} {
		if _, err := os.Stat(to + suffix); err != nil {
			t.Errorf("%s did not travel with the database", suffix)
		}
		if _, err := os.Stat(filepath.Join(repo, ".luna", "luna.db"+suffix)); err == nil {
			t.Errorf("%s was left in the checkout", suffix)
		}
	}
}
