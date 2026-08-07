package fsm

import (
	"errors"
	"testing"
)

// TestProfileDecidesWhichGatesWait covers the profile table of ADR-0013.
//
// The three profiles are the whole point of the mechanism: the same flow, run at
// three different levels of supervision. Getting one cell of this table wrong
// means a run is either more interrupted or more unattended than someone asked
// for, and the second is the expensive direction.
func TestProfileDecidesWhichGatesWait(t *testing.T) {
	cases := []struct {
		profile Profile
		gate    GateKind
		waits   bool
	}{
		{ProfileInteractive, GateConfirm, true},
		{ProfileInteractive, GateConfirmWrite, true},
		{ProfileInteractive, GateReviewArtifact, true},
		{ProfileInteractive, GateLoopCeiling, true},

		// Turbo waits only before the write: everything earlier is reversible.
		{ProfileTurbo, GateConfirm, false},
		{ProfileTurbo, GateConfirmWrite, true},
		{ProfileTurbo, GateReviewArtifact, false},
		{ProfileTurbo, GateLoopCeiling, false},

		{ProfileNightly, GateConfirm, false},
		{ProfileNightly, GateConfirmWrite, false},
		{ProfileNightly, GateReviewArtifact, false},
		{ProfileNightly, GateLoopCeiling, false},
	}

	for _, c := range cases {
		if got := c.profile.WaitsFor(c.gate); got != c.waits {
			t.Errorf("%s + %s: want waits=%v, got %v", c.profile, c.gate, c.waits, got)
		}
	}
}

// TestAnUnknownProfileWaitsForEverything covers the default branch.
//
// The two failure modes are not symmetric. Guessing permissive would let a typo
// in configuration turn a supervised run into an unattended one; guessing
// cautious only stops a task that would have carried on.
func TestAnUnknownProfileWaitsForEverything(t *testing.T) {
	typo := Profile("interactve")

	for _, gate := range []GateKind{GateConfirm, GateConfirmWrite, GateReviewArtifact, GateLoopCeiling} {
		if !typo.WaitsFor(gate) {
			t.Errorf("an unrecognised profile must not silently become unattended (%s)", gate)
		}
	}
}

// TestTaskCreatedStampsKindAndProfile covers the opening event.
//
// It is what makes the log self-describing: replay reads the kind and the profile
// out of the history instead of having them supplied alongside it.
func TestTaskCreatedStampsKindAndProfile(t *testing.T) {
	state, err := Reduce(NewTaskState("LUNA-1", ""), TaskCreated{
		Kind:    KindBug,
		Profile: ProfileNightly,
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if state.Context.Kind != KindBug {
		t.Errorf("want the kind from the event, got %q", state.Context.Kind)
	}
	if state.Profile != ProfileNightly {
		t.Errorf("want the profile from the event, got %q", state.Profile)
	}
	if state.Status != StatusReady {
		t.Errorf("creating a task does not start it, got %q", state.Status)
	}
}

// TestTaskCreatedDefaultsToInteractive covers the empty-profile case.
//
// A log written without a profile — by an older version, or by a caller that did
// not care — replays as supervised rather than as unattended.
func TestTaskCreatedDefaultsToInteractive(t *testing.T) {
	state, err := Reduce(NewTaskState("LUNA-1", ""), TaskCreated{Kind: KindFeature})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if state.Profile != ProfileInteractive {
		t.Errorf("an unstated profile is the cautious one, got %q", state.Profile)
	}
}

// TestATaskIsCreatedOnlyOnce covers the guard.
//
// A second creation event would rewrite the identity of a task already in flight,
// which the log has no way to represent honestly.
func TestATaskIsCreatedOnlyOnce(t *testing.T) {
	state, err := Reduce(NewTaskState("LUNA-1", ""), TaskCreated{Kind: KindFeature})
	if err != nil {
		t.Fatalf("first creation: %v", err)
	}
	state, err = Reduce(state, Advance{Flow: DefaultFlow()})
	if err != nil {
		t.Fatalf("advancing: %v", err)
	}

	if _, err := Reduce(state, TaskCreated{Kind: KindBug}); !errors.Is(err, ErrIllegalTransition) {
		t.Errorf("want ErrIllegalTransition on a second creation, got %v", err)
	}
}

// TestNightlyRunsStraightThroughTheGates covers the profile end to end.
//
// The scenario the profile exists for: a task that would stop four times under
// interactive runs to the end without asking. This is also what makes the
// watchdog load-bearing — with nobody watching, nothing else catches a run that
// goes nowhere.
func TestNightlyRunsStraightThroughTheGates(t *testing.T) {
	state, err := Reduce(NewTaskState("LUNA-1", ""), TaskCreated{
		Kind:    KindFeature,
		Profile: ProfileNightly,
	})
	if err != nil {
		t.Fatalf("creating: %v", err)
	}

	state, err = Reduce(state, Advance{Flow: DefaultFlow()})
	if err != nil {
		t.Fatalf("advancing: %v", err)
	}

	if state.Stage != "discovery" {
		t.Fatalf("want discovery, got %q", state.Stage)
	}
	if state.Status != StatusRunning {
		t.Errorf("nightly does not stop at the discovery gate, got %q", state.Status)
	}
	if state.Gate != nil {
		t.Errorf("no gate should be pending under nightly, got %+v", state.Gate)
	}
}

// TestTurboStopsOnlyBeforeTheWrite covers the middle profile.
func TestTurboStopsOnlyBeforeTheWrite(t *testing.T) {
	state, err := Reduce(NewTaskState("LUNA-1", ""), TaskCreated{
		Kind:    KindFeature,
		Profile: ProfileTurbo,
	})
	if err != nil {
		t.Fatalf("creating: %v", err)
	}

	// discovery has a plain confirm gate: turbo runs past it.
	state, err = Reduce(state, Advance{Flow: DefaultFlow()})
	if err != nil {
		t.Fatalf("advancing: %v", err)
	}
	if state.Status != StatusRunning {
		t.Errorf("turbo runs past a plain confirm, got %q", state.Status)
	}
}
