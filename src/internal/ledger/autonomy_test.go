package ledger_test

import (
	"testing"

	"github.com/brunoomariano/luna/src/internal/ledger"
)

func TestTheDefaultIsTheCarefulOne(t *testing.T) {
	var unset ledger.Autonomy

	if unset.Clears(ledger.Semi) || unset.Clears(ledger.Auto) {
		t.Error("an unset autonomy cleared a gate; the mode nobody chose must clear nothing")
	}
	if ledger.Manual.Clears(ledger.Semi) {
		t.Error("manual cleared a gate whose floor is semi")
	}
}

func TestAModeClearsEveryGateAtOrBelowIt(t *testing.T) {
	cases := []struct {
		mode  ledger.Autonomy
		floor ledger.Autonomy
		want  bool
	}{
		{ledger.Manual, ledger.Manual, true},
		{ledger.Manual, ledger.Semi, false},
		{ledger.Manual, ledger.Auto, false},
		{ledger.Semi, ledger.Semi, true},
		{ledger.Semi, ledger.Auto, false},
		{ledger.Auto, ledger.Semi, true},
		{ledger.Auto, ledger.Auto, true},
	}
	for _, c := range cases {
		if got := c.mode.Clears(c.floor); got != c.want {
			t.Errorf("%s clearing a %s gate: got %v, want %v", c.mode, c.floor, got, c.want)
		}
	}
}

// A typo must not hand a gate to a machine, in either direction.
func TestAnUnknownModeOrFloorClearsNothing(t *testing.T) {
	typo := ledger.Autonomy("automatic")

	if typo.Clears(ledger.Manual) {
		t.Error("an unknown mode cleared a gate")
	}
	if ledger.Auto.Clears(ledger.Autonomy("supervised")) {
		t.Error("an unknown floor was cleared")
	}
	if typo.Valid() {
		t.Error("an unknown mode reported itself valid")
	}
}

func TestParseAutonomyNamesTheAlternatives(t *testing.T) {
	for _, want := range []ledger.Autonomy{ledger.Manual, ledger.Semi, ledger.Auto} {
		got, err := ledger.ParseAutonomy(string(want))
		if err != nil || got != want {
			t.Errorf("parsing %q: got %q, %v", want, got, err)
		}
	}

	_, err := ledger.ParseAutonomy("9")
	if err == nil {
		t.Fatal("a numeric autonomy was accepted; the scale it came from is gone")
	}
	for _, mode := range []string{"manual", "semi", "auto"} {
		if !contains(err.Error(), mode) {
			t.Errorf("the refusal does not name %q: %v", mode, err)
		}
	}
}

// INV-5: auto means running without asking, not running without thinking.
func TestEveryModeStillBlocksOnMissingInformation(t *testing.T) {
	for _, mode := range []ledger.Autonomy{ledger.Manual, ledger.Semi, ledger.Auto} {
		if !mode.Blocks() {
			t.Errorf("%s suppressed a block; a fleet that cannot stop produces expensive noise", mode)
		}
	}
}

func contains(haystack, needle string) bool {
	for i := 0; i+len(needle) <= len(haystack); i++ {
		if haystack[i:i+len(needle)] == needle {
			return true
		}
	}
	return false
}
