package node

import (
	"context"
	"strings"
	"testing"

	"github.com/brunoomariano/luna/src/internal/fsm"
)

// TestPassingChecksAnswerTheGate is the whole point of the mechanical half: a
// person declared that these commands answer this gate, so they do.
func TestPassingChecksAnswerTheGate(t *testing.T) {
	shell := Shell{Dir: repo(t)}

	verdict := shell.CheckGate(context.Background(), []string{"true", "test -f delivered.txt"})

	if verdict.Unrunnable != nil {
		t.Fatalf("the checks did not run: %v", verdict.Unrunnable)
	}
	if !verdict.Approves() {
		t.Errorf("passing checks did not approve: %+v", verdict.Ran)
	}
	if len(verdict.Ran) != 2 {
		t.Errorf("want both checks run, got %d", len(verdict.Ran))
	}
}

// TestAFailingCheckRejectsAndStops covers the rule that a failing command is an
// objective answer — nothing is judged after it.
func TestAFailingCheckRejectsAndStops(t *testing.T) {
	shell := Shell{Dir: repo(t)}

	verdict := shell.CheckGate(context.Background(),
		[]string{"true", "exit 3", "echo should-not-run"})

	if !verdict.Rejected() {
		t.Error("a failing check did not reject the gate")
	}
	if verdict.Approves() {
		t.Error("a rejected gate reported as approved")
	}

	// Stopping at the first failure: the third command must not have run. A gate
	// is already rejected by then, and running the rest spends minutes to reach a
	// conclusion already reached.
	if len(verdict.Ran) != 2 {
		t.Errorf("want 2 checks run before stopping, got %d: %+v", len(verdict.Ran), verdict.Ran)
	}

	failures := verdict.Failures()
	if len(failures) != 1 || failures[0].ExitCode != 3 {
		t.Errorf("want the exit code carried through, got %+v", failures)
	}
}

// TestChecksRunOverWhatWasDelivered is INV-core-4 at the gate.
//
// The gate answers for a commit that will be merged, so a check that passed only
// because of an uncommitted file would approve work nobody has. This is the same
// acceptance criterion verification already carries, and it has to hold here too
// because this is a second path to the same conclusion.
func TestChecksRunOverWhatWasDelivered(t *testing.T) {
	dir := repo(t)

	// Never committed. A check that sees this is looking at the wrong tree.
	write(t, dir, "uncommitted.txt", "not delivered")

	shell := Shell{Dir: dir}
	verdict := shell.CheckGate(context.Background(), []string{"test -f uncommitted.txt"})

	if verdict.Unrunnable != nil {
		t.Fatalf("the check did not run: %v", verdict.Unrunnable)
	}
	if verdict.Approves() {
		t.Error("a check saw an uncommitted file and approved the gate")
	}
}

// TestNoChecksApprovesNothing is the vacuous-truth guard.
//
// "Every check passed" is trivially true of an empty list, and a design that let
// that approve would turn a typo in a metadata key — `luna_gate` for
// `luna_gates` — into a gate that opens for nobody's reason at all.
func TestNoChecksApprovesNothing(t *testing.T) {
	shell := Shell{Dir: repo(t)}

	verdict := shell.CheckGate(context.Background(), nil)

	if verdict.Approves() {
		t.Error("an empty declaration approved the gate")
	}
	if verdict.Rejected() {
		t.Error("an empty declaration rejected the gate; it should decide nothing")
	}
	if len(verdict.Ran) != 0 {
		t.Errorf("nothing was declared and something ran: %+v", verdict.Ran)
	}
}

// TestACheckThatCannotRunIsNotAFailure is the distinction that keeps the log
// honest.
//
// A command killed by a deadline exits non-zero. Recording that as "the checks
// failed" would tell the audit they ran and lost, when in fact nobody ever got
// an answer — so it goes to a person instead, which is what Unrunnable means.
func TestACheckThatCannotRunIsNotAFailure(t *testing.T) {
	cancelled, stop := context.WithCancel(context.Background())
	stop()

	shell := Shell{Dir: repo(t)}
	verdict := shell.CheckGate(cancelled, []string{"true"})

	if verdict.Unrunnable == nil {
		t.Fatal("a cancelled check produced a verdict")
	}
	if verdict.Rejected() {
		t.Error("a check that never ran was recorded as a failing one")
	}
	if verdict.Approves() {
		t.Error("a check that never ran approved the gate")
	}
}

// TestTheEvidenceNamesWhatFailed covers what a person reads when the gate is
// rejected: which command, and what it said.
func TestTheEvidenceNamesWhatFailed(t *testing.T) {
	shell := Shell{Dir: repo(t)}

	verdict := shell.CheckGate(context.Background(),
		[]string{"echo the-reason-it-failed >&2; exit 1"})

	evidence := verdict.Evidence(fsm.ScopeFull, 7)

	if evidence.Passing() {
		t.Error("a rejected gate produced passing evidence")
	}
	if evidence.ExitCode != 1 {
		t.Errorf("want the exit code recorded, got %d", evidence.ExitCode)
	}
	if !strings.Contains(evidence.Detail, "the-reason-it-failed") {
		t.Errorf("the output did not reach the evidence: %q", evidence.Detail)
	}
	if evidence.RecordedAt != 7 {
		t.Errorf("want the sequence carried through, got %d", evidence.RecordedAt)
	}
}

// TestAnsweredEvidenceIsACommandsScope is the boundary between the two halves.
//
// These are real commands with real exit codes, so they carry a command's scope
// — not `judged`, which is the model's, and not `human`, which is a person's.
// Recording a command's verdict at a weaker scope would make the gate's own
// answer unable to satisfy the stage that asked for it.
func TestAnsweredEvidenceIsACommandsScope(t *testing.T) {
	shell := Shell{Dir: repo(t)}

	evidence := shell.CheckGate(context.Background(), []string{"true"}).
		Evidence(fsm.ScopeFull, 1)

	if !evidence.Passing() {
		t.Fatal("a passing check produced failing evidence")
	}
	if evidence.Scope != fsm.ScopeFull {
		t.Errorf("want the declared scope, got %q", evidence.Scope)
	}
	if !evidence.Scope.Satisfies(fsm.ScopeFull) {
		t.Error("a gate that ran the full suite could not satisfy a full requirement")
	}
}
