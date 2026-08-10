package fsm

import (
	"strings"
	"testing"
	"time"
)

// TestTheToolBudgetIsTheLargerWindow is the distinction the two budgets exist for.
//
// Silence while a tool runs is a build; silence with nothing running is an agent
// that stopped. One window would either kill legitimate work or catch nothing
// (ADR-0034).
func TestTheToolBudgetIsTheLargerWindow(t *testing.T) {
	b := DefaultBudgets()

	if b.For(true) <= b.For(false) {
		t.Errorf("a tool in flight gets more room, got tool=%s idle=%s", b.For(true), b.For(false))
	}
	if b.For(false) != 30*time.Minute {
		t.Errorf("want the shipped idle budget, got %s", b.For(false))
	}
	if b.For(true) != 2*time.Hour {
		t.Errorf("want the shipped tool budget, got %s", b.For(true))
	}
}

// TestAnUnstatedBudgetFallsBackToTheDefault covers the profile that names one and
// not the other, and the one written before budgets existed.
//
// Both are ordinary, not errors: a profile keeps working when the feature grows
// under it.
func TestAnUnstatedBudgetFallsBackToTheDefault(t *testing.T) {
	partial := Budgets{Idle: 5 * time.Minute}.Resolve()

	if partial.Idle != 5*time.Minute {
		t.Errorf("a stated budget is honoured, got %s", partial.Idle)
	}
	if partial.Tool != DefaultBudgets().Tool {
		t.Errorf("an unstated one falls back, got %s", partial.Tool)
	}

	// A profile from before the feature existed behaves exactly as the defaults.
	if empty := (Budgets{}).Resolve(); empty != DefaultBudgets() {
		t.Errorf("an empty budget set is the shipped one, got %+v", empty)
	}
}

// TestANegativeBudgetIsNotABudget covers the guard on the zero value.
//
// A budget of zero or less would mean "call it stuck immediately", which is never
// what someone meant to write.
func TestANegativeBudgetIsNotABudget(t *testing.T) {
	resolved := Budgets{Idle: -time.Second, Tool: 0}.Resolve()

	if resolved != DefaultBudgets() {
		t.Errorf("a non-positive budget falls back rather than firing at once, got %+v", resolved)
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
