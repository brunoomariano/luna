package cli

import (
	"strings"
	"testing"

	"github.com/brunoomariano/luna/src/internal/fsm"
)

// TestTheKnobDefaultsToJudgingNothing is the default, the rollback, and what
// makes the whole feature additive.
func TestTheKnobDefaultsToJudgingNothing(t *testing.T) {
	h := newHarness(t)
	h.mustRun(t, "task", "new", "LUNA-1")

	out := h.mustRun(t, "autonomy", "LUNA-1")

	if !strings.Contains(out, "autonomy 0") {
		t.Errorf("a new task did not start at 0:\n%s", out)
	}
	if !strings.Contains(out, "goes to a person") {
		t.Errorf("the default does not say every gate asks:\n%s", out)
	}
}

// TestTheKnobSurvivesTheProcess is why this is an action and not configuration.
//
// The value has to come back from the log, so that a run where the lead judged
// three gates can be read afterwards — and so that two commands cannot disagree
// about what the setting was.
func TestTheKnobSurvivesTheProcess(t *testing.T) {
	h := newHarness(t)
	h.mustRun(t, "task", "new", "LUNA-1")
	h.mustRun(t, "autonomy", "LUNA-1", "7", "trusting the checks here")

	// A separate command, replaying from the log rather than from memory.
	out := h.mustRun(t, "autonomy", "LUNA-1")

	if !strings.Contains(out, "autonomy 7") {
		t.Errorf("the knob did not come back from the log:\n%s", out)
	}
	if !strings.Contains(out, "needing autonomy 7 or less") {
		t.Errorf("the reading does not say what 7 reaches:\n%s", out)
	}
}

// TestMovingTheKnobIsRefusedOutOfRange covers the surface a person types at.
//
// The three old names are refused rather than aliased: a value meaning "knob 5"
// would authorise the lead to judge gates up to criticality 5 without the word
// "gate" appearing anywhere.
func TestMovingTheKnobIsRefusedOutOfRange(t *testing.T) {
	for _, value := range []string{"-1", "11", "ask", "retry", "decide", "high", "5.5"} {
		h := newHarness(t)
		h.mustRun(t, "task", "new", "LUNA-1")

		if err := h.run(t, "autonomy", "LUNA-1", value); err == nil {
			t.Errorf("autonomy %s was accepted", value)
		}

		// And the refusal leaves the setting where it was, rather than half-applying.
		if out := h.mustRun(t, "autonomy", "LUNA-1"); !strings.Contains(out, "autonomy 0") {
			t.Errorf("a refused %s moved the knob anyway:\n%s", value, out)
		}
	}
}

// TestTheMostAutonomousSettingStillSaysItMayAsk is the counter-intuitive half,
// and the one a person will otherwise be surprised by.
//
// Knob 10 does not mean "never asks". It means the lead may judge every gate,
// and a lead judging honestly will sometimes conclude it cannot.
func TestTheMostAutonomousSettingStillSaysItMayAsk(t *testing.T) {
	h := newHarness(t)
	h.mustRun(t, "task", "new", "LUNA-1")
	h.mustRun(t, "autonomy", "LUNA-1", "10")

	out := h.mustRun(t, "autonomy", "LUNA-1")

	if !strings.Contains(out, "cannot decide") {
		t.Errorf("the widest setting does not warn that it may still ask:\n%s", out)
	}
}

// TestAnOpenGateKeepsTheAnswerItOpenedWith covers the promise the reducer makes,
// from the surface that has to explain it.
func TestAnOpenGateKeepsTheAnswerItOpenedWith(t *testing.T) {
	state := fsm.TaskState{
		ID:     "LUNA-1",
		Status: fsm.StatusAwaitingGate,
		Gate:   &fsm.PendingGate{Kind: fsm.GateConfirm, Stage: "scenarios"},
	}

	moved, err := fsm.Reduce(state, fsm.SetKnob{Knob: fsm.KnobAll})
	if err != nil {
		t.Fatalf("moving the knob with a gate open: %v", err)
	}

	if moved.Gate == nil {
		t.Fatal("raising the knob took an open gate away from the person looking at it")
	}
	if moved.Knob != fsm.KnobAll {
		t.Errorf("the knob did not move: %d", moved.Knob)
	}
}

// TestAutonomyNeedsATaskThatExists covers the two ways the command is misused.
func TestAutonomyNeedsATaskThatExists(t *testing.T) {
	h := newHarness(t)

	if err := h.run(t, "autonomy"); err == nil {
		t.Error("autonomy with no task id was accepted")
	}
	if err := h.run(t, "autonomy", "LUNA-NOPE"); err == nil {
		t.Error("autonomy on a task that does not exist was accepted")
	}
}

// TestMovingTheKnobSaysAnOpenGateIsUnaffected is the message that answers the
// question a person asks next: "I raised it, why did it still ask me?"
//
// It drives the task to a real gate rather than asserting conditionally on
// whether one happened to open — a test that only checks the message when a gate
// exists passes just as happily when no gate ever does.
func TestMovingTheKnobSaysAnOpenGateIsUnaffected(t *testing.T) {
	h := newHarness(t)
	h.mustRun(t, "task", "new", "LUNA-1", "--simulated")

	var opened bool
	for range 12 {
		if err := h.run(t, "lead", "LUNA-1", "--dry-run"); err != nil {
			break
		}
		if strings.Contains(h.mustRun(t, "status", "LUNA-1"), string(fsm.StatusAwaitingGate)) {
			opened = true
			break
		}
	}
	if !opened {
		t.Fatal("the task never reached a gate, so this test asserts nothing")
	}

	out := h.mustRun(t, "autonomy", "LUNA-1", "10")

	if !strings.Contains(out, "still goes to a person") {
		t.Errorf("a gate was open and the message did not say it is unaffected:\n%s", out)
	}
}
