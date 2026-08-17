package cli

import (
	"context"
	"errors"
	"testing"

	"github.com/brunoomariano/luna/src/internal/fsm"
)

// The seam between the two halves of RFC-0006: what a task declared runs, and the
// lead judges the rest. The declaration used to live in beads' metadata and is
// replayed from the task's own log now (ADR-0067) — these are the same guarantees
// asserted against the new source.

// declaring opens a task and declares the checks that answer one of its gates,
// through the real commands rather than by writing events by hand.
func declaring(t *testing.T, gate fsm.GateKind, checks ...string) *harness {
	t.Helper()

	h := newHarness(t)
	h.mustRun(t, "task", "new", "LUNA-1")

	args := []string{"gate", "checks", "LUNA-1", "--on", string(gate)}
	for _, check := range checks {
		args = append(args, "--run", check)
	}
	h.mustRun(t, args...)
	return h
}

// TestTheSeamRunsWhatTheTaskDeclared is the mechanical half arriving at the gate.
func TestTheSeamRunsWhatTheTaskDeclared(t *testing.T) {
	h := declaring(t, fsm.GateConfirm, "true")

	got := checkGateWith(h.env.Store, t.TempDir())(context.Background(), "LUNA-1", fsm.GateConfirm)

	if !got.Passed || got.Rejected || got.Unrunnable {
		t.Errorf("a passing check reported %+v", got)
	}
}

// TestTheSeamRejectsOnANonZeroExit covers the answer that needs no model.
func TestTheSeamRejectsOnANonZeroExit(t *testing.T) {
	h := declaring(t, fsm.GateConfirm, "exit 1")

	got := checkGateWith(h.env.Store, t.TempDir())(context.Background(), "LUNA-1", fsm.GateConfirm)

	if !got.Rejected || got.Passed {
		t.Errorf("a failing check reported %+v", got)
	}
}

// TestTheSeamRunsEveryCheckInOrder. `--run` repeats, and a parser that kept only
// the last one would run a third of what was asked while looking like it worked.
func TestTheSeamRunsEveryCheckInOrder(t *testing.T) {
	h := declaring(t, fsm.GateConfirm, "true", "exit 1", "true")

	got := checkGateWith(h.env.Store, t.TempDir())(context.Background(), "LUNA-1", fsm.GateConfirm)

	if !got.Rejected {
		t.Errorf("a failure among several checks reported %+v, want rejected", got)
	}
}

// TestTheSeamIsSilentAboutAGateNobodyDeclared is what keeps this additive.
//
// It is every task that exists today: nothing declared at all. The gate has to
// reach the judgement half with nothing decided — not approved, and not treated
// as broken.
func TestTheSeamIsSilentAboutAGateNobodyDeclared(t *testing.T) {
	for name, declare := range map[string]func(*testing.T) *harness{
		"nothing declared": func(t *testing.T) *harness {
			h := newHarness(t)
			h.mustRun(t, "task", "new", "LUNA-1")
			return h
		},
		"a different gate": func(t *testing.T) *harness {
			return declaring(t, fsm.GateReviewArtifact, "true")
		},
		"declared but empty": func(t *testing.T) *harness {
			return declaring(t, fsm.GateConfirm)
		},
	} {
		t.Run(name, func(t *testing.T) {
			h := declare(t)

			got := checkGateWith(h.env.Store, t.TempDir())(context.Background(), "LUNA-1", fsm.GateConfirm)

			if got.Passed || got.Rejected || got.Unrunnable {
				t.Errorf("an undeclared gate reported %+v", got)
			}
			// And the decision it leads to is a person, not an approval.
			if answer := fsm.ResolveGate(nil, got, fsm.KnobAll); answer != fsm.AnswerPerson {
				t.Errorf("an undeclared gate resolved to %v", answer)
			}
		})
	}
}

// TestALogThatCannotBeReadIsNotAnApproval is the distinction that keeps a broken
// read from opening gates.
//
// "Nothing was declared" and "the task could not be read" are different facts,
// and one of them approves a gate. A task whose flow changed under it no longer
// replays, which is the reachable version of that.
func TestALogThatCannotBeReadIsNotAnApproval(t *testing.T) {
	h := newHarness(t)
	stale := []fsm.Stage{{ID: "gone", Requires: []fsm.Artifact{fsm.TaskID}, Produces: []fsm.Artifact{"x"}}}
	if err := h.env.Store.AppendAction("LUNA-1", fsm.TaskCreated{
		Kind: fsm.KindChore, Flow: fsm.Fingerprint(stale),
	}); err != nil {
		t.Fatalf("seeding: %v", err)
	}

	got := checkGateWith(h.env.Store, t.TempDir())(context.Background(), "LUNA-1", fsm.GateConfirm)

	if !got.Unrunnable {
		t.Errorf("an unreadable log reported %+v, want unrunnable", got)
	}
	if answer := fsm.ResolveGate(nil, got, fsm.KnobAll); answer != fsm.AnswerPerson {
		t.Errorf("an unreadable log resolved to %v, want a person", answer)
	}
}

// TestAGateNobodyCanNameIsRefused covers the person's own typo.
//
// Storing it as typed would leave a declaration in the log answering a gate that
// does not exist — silence at the moment they were trying to stop being asked.
func TestAGateNobodyCanNameIsRefused(t *testing.T) {
	h := newHarness(t)
	h.mustRun(t, "task", "new", "LUNA-1")

	if err := h.run(t, "gate", "checks", "LUNA-1", "--on", "aprove-plan", "--run", "true"); err == nil {
		t.Fatal("a gate kind that does not exist was accepted")
	}
	if h.replay(t, "LUNA-1").GateChecks != nil {
		t.Error("a refused declaration reached the log anyway")
	}
}

// TestRedeclaringReplacesRatherThanAppends. A person correcting what they said is
// not adding to it — and the previous list stays in the log either way.
func TestRedeclaringReplacesRatherThanAppends(t *testing.T) {
	h := declaring(t, fsm.GateConfirm, "exit 1")
	h.mustRun(t, "gate", "checks", "LUNA-1", "--on", "confirm", "--run", "true")

	got := checkGateWith(h.env.Store, t.TempDir())(context.Background(), "LUNA-1", fsm.GateConfirm)

	if !got.Passed {
		t.Errorf("the corrected declaration did not take: %+v", got)
	}
	if checks := h.replay(t, "LUNA-1").GateChecks[fsm.GateConfirm]; len(checks) != 1 {
		t.Errorf("want the replacement alone, got %v", checks)
	}
}

// TestDeclaringChecksNeedsAGate. Without --on there is nothing to attach them to,
// and guessing would attach them to the wrong gate.
func TestDeclaringChecksNeedsAGate(t *testing.T) {
	h := newHarness(t)
	h.mustRun(t, "task", "new", "LUNA-1")

	if err := h.run(t, "gate", "checks", "LUNA-1", "--run", "true"); err == nil {
		t.Fatal("checks with no gate were accepted")
	}
}

// TestDeclaringChecksNeedsATask. The task is named in the error rather than the
// declaration landing in a log nobody opened.
func TestDeclaringChecksNeedsATask(t *testing.T) {
	h := newHarness(t)

	if err := h.run(t, "gate", "checks", "LUNA-404", "--on", "confirm", "--run", "true"); err == nil {
		t.Fatal("checks were declared against a task that does not exist")
	}
}

// TestDeclaringChecksRejectsAFlagWithNoValue. `--run` at the end of the line is a
// half-typed command, and accepting it would declare a gate answered by nothing
// while looking like it took.
func TestDeclaringChecksRejectsAFlagWithNoValue(t *testing.T) {
	h := newHarness(t)
	h.mustRun(t, "task", "new", "LUNA-1")

	if err := h.run(t, "gate", "checks", "LUNA-1", "--on", "confirm", "--run"); !errors.Is(err, ErrUsage) {
		t.Fatalf("want a usage error, got %v", err)
	}
}

// TestDeclaringChecksRejectsAnUnknownFlag covers the typo that would otherwise be
// read as a command to run.
func TestDeclaringChecksRejectsAnUnknownFlag(t *testing.T) {
	h := newHarness(t)
	h.mustRun(t, "task", "new", "LUNA-1")

	if err := h.run(t, "gate", "checks", "LUNA-1", "--on", "confirm", "--exec", "make ci"); !errors.Is(err, ErrUsage) {
		t.Fatalf("want a usage error, got %v", err)
	}
}
