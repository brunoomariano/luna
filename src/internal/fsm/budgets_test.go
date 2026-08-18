package fsm

import (
	"strings"
	"testing"
	"time"
)

// TestTheTurnBudgetIsTwoHours pins the shipped default to the case that applies.
//
// A turn may contain a build, and thirty minutes — the old idle value — was a
// wrong answer no configuration could fix, because the field that would have fixed
// it was read and discarded (ADR-0051).
func TestTheTurnBudgetIsTwoHours(t *testing.T) {
	if got := DefaultBudgets().Turn; got != 2*time.Hour {
		t.Errorf("a turn may contain a build; want 2h, got %s", got)
	}
}

// TestParseBudgetRefusesWhatItCannotRead covers the config path.
//
// A malformed budget stops rather than reverting quietly: someone who wrote
// `idle_budget = "30"` believes they tightened the watchdog, and a silent
// fallback would leave them believing it.
func TestParseBudgetRefusesWhatItCannotRead(t *testing.T) {
	good, err := ParseBudget("45m")
	if err != nil {
		t.Fatalf("a plain duration must parse: %v", err)
	}
	if good != 45*time.Minute {
		t.Errorf("want 45m, got %s", good)
	}

	for _, bad := range []string{"30", "", "soon", "-5m", "0s"} {
		if _, err := ParseBudget(bad); err == nil {
			t.Errorf("%q must be refused", bad)
		}
	}
}

// TestTheBudgetErrorSaysWhatWasExpected covers the house rule that an error names
// the invalid value and the wanted one.
func TestTheBudgetErrorSaysWhatWasExpected(t *testing.T) {
	_, err := ParseBudget("30")

	if err == nil {
		t.Fatal("a bare number is not a duration")
	}
	for _, want := range []string{`"30"`, "30m"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("the error should mention %s, got %v", want, err)
		}
	}
}
