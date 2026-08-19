package node

import (
	"context"
	"os/exec"
	"strings"
	"testing"
)

// TestLandPointsTheTaskBranchAtWhatItDelivered is the promise the task branch
// makes and the swarm bench found unkept.
//
// A task ran all twelve stages with real agents, recorded the right commit as
// its base, and left `luna/<task>` on the seed — the work was on a role branch
// and nothing said which. `done` has to mean ready to integrate, and this is
// what makes it true.
func TestLandPointsTheTaskBranchAtWhatItDelivered(t *testing.T) {
	dir := repo(t)

	// The branch `setup` would have made, stranded where the task opened.
	run(t, dir, "git", "branch", TaskBranch("LUNA-1"))
	stranded := strings.TrimSpace(output(t, dir, "git", "rev-parse", TaskBranch("LUNA-1")))

	// A later stage delivers.
	write(t, dir, "delivered.txt", "what the last stage produced")
	run(t, dir, "git", "add", ".")
	run(t, dir, "git", "commit", "-m", "harden: the last stage")
	delivered := strings.TrimSpace(output(t, dir, "git", "rev-parse", "HEAD"))

	if err := Land(context.Background(), dir, "LUNA-1", delivered); err != nil {
		t.Fatalf("landing: %v", err)
	}

	landed := strings.TrimSpace(output(t, dir, "git", "rev-parse", TaskBranch("LUNA-1")))
	if landed != delivered {
		t.Errorf("the branch points at %s, want the delivered commit %s", short(landed), short(delivered))
	}
	if landed == stranded {
		t.Error("the branch is still where the task opened")
	}
}

// TestLandingIsIdempotent covers a task landed twice — a run resumed after a
// block reaches the end again, and pointing the same ref at the same commit has
// to be a no-op rather than a failure.
func TestLandingIsIdempotent(t *testing.T) {
	dir := repo(t)
	head := strings.TrimSpace(output(t, dir, "git", "rev-parse", "HEAD"))

	for i := range 2 {
		if err := Land(context.Background(), dir, "LUNA-1", head); err != nil {
			t.Fatalf("landing, attempt %d: %v", i+1, err)
		}
	}

	if got := strings.TrimSpace(output(t, dir, "git", "rev-parse", TaskBranch("LUNA-1"))); got != head {
		t.Errorf("the branch moved: %s", short(got))
	}
}

// TestLandingNothingIsNotAFailure covers a task that reached the end having
// committed nothing.
//
// It happens: a flow whose stages are all mechanical, or one that produces no
// code. Forcing the branch to an empty ref would be worse than leaving it, and
// reporting an error would turn a finished task into a failed one.
func TestLandingNothingIsNotAFailure(t *testing.T) {
	dir := repo(t)

	if err := Land(context.Background(), dir, "LUNA-1", ""); err != nil {
		t.Errorf("landing an empty commit reported an error: %v", err)
	}
	if branchExists(t, dir, "LUNA-1") {
		t.Error("a branch was created for a task that delivered nothing")
	}
}

// TestLandingRefusesACommitThatDoesNotResolve is the guard against pointing a
// branch at nothing.
//
// A ref that names a commit nobody has is worse than no ref: it reads like an
// answer to "where is the work" and leads nowhere.
func TestLandingRefusesACommitThatDoesNotResolve(t *testing.T) {
	dir := repo(t)

	err := Land(context.Background(), dir, "LUNA-1", "0000000000000000000000000000000000000000")
	if err == nil {
		t.Fatal("a commit that does not exist was accepted")
	}
	if !strings.Contains(err.Error(), "does not resolve") {
		t.Errorf("the error does not say what is wrong: %v", err)
	}
	if branchExists(t, dir, "LUNA-1") {
		t.Error("the branch was created anyway")
	}
}

// TestTheTaskBranchIsTheNameSetupMade keeps the two halves agreeing.
//
// `setup` creates `luna/<task>` through herdr, and this points it. If the two
// ever disagreed on the name, landing would create a second branch and leave the
// first stranded — which is the bug this whole file exists to close, arriving by
// a different door.
func TestTheTaskBranchIsTheNameSetupMade(t *testing.T) {
	if got := TaskBranch("LUNA-1"); got != "luna/LUNA-1" {
		t.Errorf("the task branch is %q, and herdr.WorktreeSpec.Branch makes luna/LUNA-1", got)
	}
}

// branchExists asks git rather than Luna, which is the point: the test is about
// what the repository ended up with.
func branchExists(t *testing.T, dir, taskID string) bool {
	t.Helper()

	cmd := exec.Command("git", "rev-parse", "--verify", TaskBranch(taskID))
	cmd.Dir = dir
	return cmd.Run() == nil
}
