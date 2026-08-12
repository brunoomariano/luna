package registry

import (
	"context"
	"errors"
	"os/exec"
	"strings"
	"testing"
)

// The tests in this file drive the real `bd`. Everything else in the package
// runs against a fake, which proves the adapter's logic and proves nothing about
// whether the adapter matches the binary.
//
// That distinction is not theoretical here. Writing this adapter turned up two
// facts no amount of reading would have given: `provenance record --ref-kind
// git-sha` insists on a full 40-character lowercase hex and refuses a short sha,
// and `provenance log` takes its issue positionally rather than through
// `--issue` like `record` does. Both were found by running it.

// realBeads prepares a repository with an initialised registry, or skips.
//
// Skipping rather than failing when bd is absent: it is a runtime dependency
// pinned in mise.toml, and a contributor who has not run `mise install` should
// see the rest of the suite pass rather than a failure about a missing binary.
// `make doctor` is where a missing bd is reported.
func realBeads(t *testing.T) *Beads {
	t.Helper()

	if _, err := exec.LookPath("bd"); err != nil {
		t.Skip("bd is not installed — `mise install` (see mise.toml)")
	}

	dir := t.TempDir()
	for _, args := range [][]string{
		{"init", "--initial-branch=main"},
		{"config", "user.email", "test@example.invalid"},
		{"config", "user.name", "Test"},
	} {
		cmd := exec.Command("git", args...)
		cmd.Dir = dir
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("git %s: %v: %s", strings.Join(args, " "), err, out)
		}
	}

	init := exec.Command("bd", "init", "--prefix", "luna")
	init.Dir = dir
	if out, err := init.CombinedOutput(); err != nil {
		t.Skipf("bd init did not work here: %v: %s", err, out)
	}

	return New(dir)
}

// TestTheRegistryRoundTripsAgainstTheRealBinary is the whole point of this file:
// create a task, move it, record what it delivered, and read all of it back.
func TestTheRegistryRoundTripsAgainstTheRealBinary(t *testing.T) {
	b := realBeads(t)
	ctx := context.Background()

	id, err := b.Create(ctx, "add authentication")
	if err != nil {
		t.Fatalf("Create: %v", err)
	}

	if err := b.EnterStage(ctx, id, "build"); err != nil {
		t.Fatalf("EnterStage: %v", err)
	}
	if err := b.Move(ctx, id, StatusOpen, StatusInProgress); err != nil {
		t.Fatalf("Move: %v", err)
	}

	task, err := b.Task(ctx, id)
	if err != nil {
		t.Fatalf("Task: %v", err)
	}
	if task.Status != StatusInProgress {
		t.Errorf("status = %q, want in_progress", task.Status)
	}
	if task.Stage() != "build" {
		t.Errorf("stage = %q, want build", task.Stage())
	}
}

// TestTheGuardActuallyGuards is the one that would be worthless against a fake.
// The claim is that bd refuses a write whose precondition no longer holds, and
// only bd can settle it.
func TestTheGuardActuallyGuards(t *testing.T) {
	b := realBeads(t)
	ctx := context.Background()

	id, err := b.Create(ctx, "a task two leads both decide about")
	if err != nil {
		t.Fatalf("Create: %v", err)
	}

	// The first mover wins.
	if err := b.Move(ctx, id, StatusOpen, StatusInProgress); err != nil {
		t.Fatalf("the first move failed: %v", err)
	}

	// The second decided from the state the first has already left.
	err = b.Move(ctx, id, StatusOpen, StatusClosed)
	if !errors.Is(err, ErrLostTheRace) {
		t.Fatalf("err = %v, want ErrLostTheRace — the guard did not hold", err)
	}

	// And nothing was written: bd's own help says so, and this checks it.
	task, err := b.Task(ctx, id)
	if err != nil {
		t.Fatalf("Task: %v", err)
	}
	if task.Status != StatusInProgress {
		t.Errorf("status = %q — the losing write landed anyway", task.Status)
	}
}

// TestProvenanceIsAppendOnlyAndIdempotent covers the property that makes this a
// replacement for the append-only log rather than merely a place to put things.
func TestProvenanceIsAppendOnlyAndIdempotent(t *testing.T) {
	b := realBeads(t)
	ctx := context.Background()

	id, err := b.Create(ctx, "a task that delivers twice")
	if err != nil {
		t.Fatalf("Create: %v", err)
	}

	sha := strings.Repeat("a", 40)
	if err := b.RecordCommit(ctx, id, sha, "build"); err != nil {
		t.Fatalf("RecordCommit: %v", err)
	}
	// The same delivery reported again — a retry, or a lead that crashed after
	// writing and before noticing.
	if err := b.RecordCommit(ctx, id, sha, "build"); err != nil {
		t.Fatalf("recording the same commit twice: %v", err)
	}

	got, err := b.Commits(ctx, id)
	if err != nil {
		t.Fatalf("Commits: %v", err)
	}
	if len(got) != 1 {
		t.Fatalf("got %d deliveries, want 1 — recording twice must record once", len(got))
	}
	if got[0].Commit != sha {
		t.Errorf("commit = %q, want the sha handed in", got[0].Commit)
	}
	if got[0].Stage != "build" {
		t.Errorf("stage = %q, want build — the payload did not survive", got[0].Stage)
	}
}

// TestAShortShaIsRefused records a fact measured from the binary rather than
// assumed: `--ref-kind git-sha` demands a full 40-character lowercase hex.
//
// The refusal is worth keeping rather than working around. An abbreviated commit
// is ambiguous in a repository large enough for it to matter, and the handoff
// depends on the base being exactly one commit.
func TestAShortShaIsRefused(t *testing.T) {
	b := realBeads(t)
	ctx := context.Background()

	id, err := b.Create(ctx, "a task with a short sha")
	if err != nil {
		t.Fatalf("Create: %v", err)
	}

	if err := b.RecordCommit(ctx, id, "abc1234", "build"); err == nil {
		t.Fatal("a seven-character sha was accepted as a git-sha ref")
	}
}

func TestAnUnknownTaskIsUnknownToTheRealBinaryToo(t *testing.T) {
	b := realBeads(t)

	if _, err := b.Task(context.Background(), "luna-nothing"); !errors.Is(err, ErrNoSuchTask) {
		t.Fatalf("err = %v, want ErrNoSuchTask", err)
	}
}

// TestBlockedTasksAreFoundByQueryingTheRealRegistry closes the loop on the
// reason the registry moved at all.
func TestBlockedTasksAreFoundByQueryingTheRealRegistry(t *testing.T) {
	b := realBeads(t)
	ctx := context.Background()

	stuck, err := b.Create(ctx, "a task that will block")
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	if _, err := b.Create(ctx, "a task that will not"); err != nil {
		t.Fatalf("Create: %v", err)
	}

	if err := b.EnterStage(ctx, stuck, "verify"); err != nil {
		t.Fatalf("EnterStage: %v", err)
	}
	if err := b.Move(ctx, stuck, StatusOpen, StatusBlocked); err != nil {
		t.Fatalf("Move: %v", err)
	}

	blocked, err := b.Blocked(ctx)
	if err != nil {
		t.Fatalf("Blocked: %v", err)
	}
	if len(blocked) != 1 {
		t.Fatalf("got %d blocked tasks, want 1", len(blocked))
	}
	if blocked[0].ID != stuck {
		t.Errorf("blocked task = %q, want %q", blocked[0].ID, stuck)
	}
	if blocked[0].Stage() != "verify" {
		t.Errorf("stage = %q, want verify — the label did not survive the query", blocked[0].Stage())
	}
}
