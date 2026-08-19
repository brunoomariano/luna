package fsm

import (
	"math"
	"testing"
)

// TestARetryAddsToWhatTheStageAlreadyCost is the reason spend accumulates rather
// than replaces.
//
// A stage can run more than once — a review sends work back and it runs again —
// and both calls were billed. Replacing would report the retry's price as the
// stage's price, which would make the flow that fails most look like the
// cheapest one.
func TestARetryAddsToWhatTheStageAlreadyCost(t *testing.T) {
	spent := withSpend(nil, "build", Spend{InputTokens: 100, CostUSD: 0.10, Turns: 3})
	spent = withSpend(spent, "build", Spend{InputTokens: 50, CostUSD: 0.05, Turns: 2})

	got := spent["build"]
	if got.InputTokens != 150 {
		t.Errorf("want both attempts counted (150 tokens), got %d", got.InputTokens)
	}
	if !nearly(got.CostUSD, 0.15) {
		t.Errorf("want both attempts billed (0.15), got %v", got.CostUSD)
	}
	if got.Turns != 5 {
		t.Errorf("want both attempts' turns (5), got %d", got.Turns)
	}
}

// TestAMechanicalStageRecordsNothing covers the stage that runs no agent. A row
// of zeros would suggest a measurement that never happened.
func TestAMechanicalStageRecordsNothing(t *testing.T) {
	if spent := withSpend(nil, "setup", Spend{}); spent != nil {
		t.Errorf("want no record for a stage that ran no agent, got %+v", spent)
	}
	if !(Spend{}).Zero() {
		t.Error("an empty spend must read as nothing spent")
	}
	if (Spend{CacheRead: 1}).Zero() {
		t.Error("a call billed only for cache reads still spent something")
	}
}

// TestTokensCountTheCache is what keeps the fresh-versus-live comparison honest.
// Cache reads are billed, and leaving them out would make a continued session
// look free — which is the exact comparison the field was added for.
func TestTokensCountTheCache(t *testing.T) {
	s := Spend{InputTokens: 1, OutputTokens: 2, CacheRead: 400, CacheWrite: 8}
	if got := s.Tokens(); got != 411 {
		t.Errorf("want every token counted (411), got %d", got)
	}
}

// TestTheLastAttemptDescribesHowTheStageRan covers the two fields that describe
// rather than count. A stage that switched model or context between attempts is
// better described by what it did most recently than by a blank.
func TestTheLastAttemptDescribesHowTheStageRan(t *testing.T) {
	spent := withSpend(nil, "build", Spend{InputTokens: 1, Model: "opus", Context: "fresh"})
	spent = withSpend(spent, "build", Spend{InputTokens: 1, Model: "sonnet", Context: "live"})

	got := spent["build"]
	if got.Model != "sonnet" || got.Context != "live" {
		t.Errorf("want the last attempt's description, got model %q context %q", got.Model, got.Context)
	}

	// A later attempt that reports neither must not erase what the first said.
	spent = withSpend(spent, "build", Spend{InputTokens: 1})
	if got := spent["build"]; got.Model != "sonnet" {
		t.Errorf("a silent attempt erased the model, got %q", got.Model)
	}
}

// TestTotalSpendSumsTheStages covers the number a person asks for first, and the
// reason the map is kept per stage anyway: the total cannot say which stage is
// expensive, and that is the part worth acting on.
func TestTotalSpendSumsTheStages(t *testing.T) {
	state := TaskState{Spent: map[StageID]Spend{
		"build":  {InputTokens: 100, CostUSD: 0.10},
		"verify": {InputTokens: 50, CostUSD: 0.05},
	}}

	total := state.TotalSpend()
	if total.InputTokens != 150 {
		t.Errorf("want the stages summed (150), got %d", total.InputTokens)
	}
	if !nearly(total.CostUSD, 0.15) {
		t.Errorf("want the costs summed (0.15), got %v", total.CostUSD)
	}
}

// TestSpendIsRecordedWhenAStageCloses is the accounting reaching the state
// through the only door it has: a Complete that passed its checks.
func TestSpendIsRecordedWhenAStageCloses(t *testing.T) {
	flow := []Stage{{
		ID: "build", Role: "implementer",
		Requires: []Artifact{TaskID},
		Produces: []Artifact{"code"},
	}}

	state := TaskState{
		ID: "T-1", Status: StatusRunning, Stage: "build",
		Context:  NewTaskContext(KindFeature),
		Evidence: map[Artifact]Evidence{},
	}

	after, err := Reduce(state, Complete{
		Delivered: []Artifact{"code"},
		Evidence:  map[Artifact]Evidence{"code": {Scope: ScopeExistence, Verdict: VerdictPassed}},
		Flow:      flow,
		Spent:     Spend{InputTokens: 900, CostUSD: 0.42, Context: "fresh"},
	})
	if err != nil {
		t.Fatalf("Reduce: %v", err)
	}

	got := after.Spent["build"]
	if got.CostUSD != 0.42 {
		t.Errorf("want the stage's cost recorded, got %v", got.CostUSD)
	}
	if got.Context != "fresh" {
		t.Errorf("want how the stage ran recorded, got %q", got.Context)
	}
}

// nearly compares two prices. Cost is a float because the harness reports one,
// and summing floats does not land on the decimal a person would write — 0.10
// plus 0.05 is 0.15000000000000002. A cent is far below anything worth acting
// on, so that is the tolerance.
func nearly(got, want float64) bool { return math.Abs(got-want) < 0.001 }
