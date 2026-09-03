package ledger_test

import (
	"strings"
	"testing"

	"github.com/brunoomariano/luna/src/internal/ledger"
)

func TestTheThreeModesAreReadBack(t *testing.T) {
	for _, want := range []ledger.Autonomy{ledger.Manual, ledger.Semi, ledger.Auto} {
		got, err := ledger.ParseAutonomy(string(want))
		if err != nil || got != want {
			t.Errorf("parsing %q: got %q, %v", want, got, err)
		}
	}
}

// The scale that preceded this had eleven values and three meanings.
func TestANumericAutonomyIsRefused(t *testing.T) {
	_, err := ledger.ParseAutonomy("9")
	if err == nil {
		t.Fatal("a numeric autonomy was accepted; the scale it came from is gone")
	}
	for _, mode := range []string{"manual", "semi", "auto"} {
		if !strings.Contains(err.Error(), mode) {
			t.Errorf("the refusal does not name %q: %v", mode, err)
		}
	}
}

// A typo recorded as an autonomy is a mode nobody can act on, sitting in an
// append-only record forever.
func TestAMisspelledModeIsRefusedRatherThanRecorded(t *testing.T) {
	for _, typo := range []string{"automatic", "Auto", "supervised", ""} {
		if _, err := ledger.ParseAutonomy(typo); err == nil {
			t.Errorf("%q was accepted as an autonomy", typo)
		}
	}
}

// What Luna does with a mode is record it: the line has to come back saying what
// went in, because the conductor reads it to decide who answers the next gate.
func TestAModeSurvivesTheRoundTripThroughTheLedger(t *testing.T) {
	l := newLedger(t)

	if err := l.Append(ledger.Entry{
		Run:      "MAX-2",
		Event:    ledger.EventAutonomy,
		Autonomy: string(ledger.Semi),
		Note:     "the user asked for it",
	}); err != nil {
		t.Fatalf("recording a mode: %v", err)
	}

	got, found, err := l.State("MAX-2")
	if err != nil || !found {
		t.Fatalf("state: found=%v err=%v", found, err)
	}
	if got.Autonomy != string(ledger.Semi) {
		t.Errorf("autonomy: got %q, want semi", got.Autonomy)
	}
	if got.Note == "" {
		t.Error("the reason the mode moved did not survive")
	}
}
