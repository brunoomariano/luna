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
		ID: "build", Requires: []Artifact{TaskID},
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

// TestABlockedStageIsStillBilled is the regression for a bug found by running a
// real task.
//
// The exit checks return early — a stage that owed two artifacts and delivered
// none is blocked before the closing path — and the spend used to be recorded
// only on the path where the stage closed. So a failed stage came out free, and
// the flow that fails most would read as the cheapest one to run.
func TestABlockedStageIsStillBilled(t *testing.T) {
	flow := []Stage{{
		ID: "scenarios", Requires: []Artifact{TaskID},
		Produces: []Artifact{"scenarios", "approach"},
	}}

	state := TaskState{
		ID: "T-1", Status: StatusRunning, Stage: "scenarios",
		Context:  NewTaskContext(KindFeature),
		Evidence: map[Artifact]Evidence{},
	}

	// The agent ran, was billed, and delivered neither artifact.
	after, err := Reduce(state, Complete{
		Delivered: nil,
		Flow:      flow,
		Spent:     Spend{InputTokens: 5000, CostUSD: 0.12, Context: "fresh"},
	})
	if err != nil {
		t.Fatalf("Reduce: %v", err)
	}

	if after.Status != StatusBlocked {
		t.Fatalf("want the stage blocked for an undelivered contract, got %q", after.Status)
	}
	if got := after.Spent["scenarios"].CostUSD; got != 0.12 {
		t.Errorf("a blocked stage lost its bill: want 0.12, got %v", got)
	}
}

// TestAStageIsBilledOnceWhenItCloses guards the other direction. Recording the
// spend before the checks and again at the end would double every stage that
// worked.
func TestAStageIsBilledOnceWhenItCloses(t *testing.T) {
	flow := []Stage{{
		ID: "build", Requires: []Artifact{TaskID},
		Produces: []Artifact{"code"},
	}}

	state := TaskState{
		ID: "T-2", Status: StatusRunning, Stage: "build",
		Context:  NewTaskContext(KindFeature),
		Evidence: map[Artifact]Evidence{},
	}

	after, err := Reduce(state, Complete{
		Delivered: []Artifact{"code"},
		Evidence:  map[Artifact]Evidence{"code": {Scope: ScopeExistence, Verdict: VerdictPassed}},
		Flow:      flow,
		Spent:     Spend{InputTokens: 100, CostUSD: 0.10},
	})
	if err != nil {
		t.Fatalf("Reduce: %v", err)
	}

	if after.Status == StatusBlocked {
		t.Fatalf("the stage should have closed: %s", after.Blocked)
	}
	if got := after.Spent["build"].CostUSD; !nearly(got, 0.10) {
		t.Errorf("want the stage billed once (0.10), got %v", got)
	}
}

// TestTheSessionSurvivesAReplay is what makes `context = "live"` more than a
// property of one process.
//
// The session id used to live only in `node.Runner.Sessions`, a map on a struct.
// Answering a gate ends the process, and the shipped flow puts a gate in the
// middle of the maker's run — so `build`, declared live, started cold because the
// id `plan` had opened was gone. Measured on TALLY-3: the flow got one resumed
// pair out of three, and the gate cost the other two.
//
// The log is where things that must outlive a process already live.
func TestTheSessionSurvivesAReplay(t *testing.T) {
	state := atStage(t, KindFeature, "plan")

	stage := stageIn(DefaultFlow(), "plan")
	owed := append(append([]Artifact{}, stage.Produces...), stage.ProducesForHuman...)

	closed, err := Reduce(state, Complete{
		Delivered: owed,
		Evidence:  passing(stage, owed),
		Flow:      DefaultFlow(),
		Commit:    "c0ffee",
		Spent:     Spend{Turns: 3, CostUSD: 0.5, Session: "sess-plan", Context: "fresh"},
	})
	if err != nil {
		t.Fatalf("closing plan: %v", err)
	}

	got := closed.Spent["plan"].Session
	if got != "sess-plan" {
		t.Errorf("the session a stage opened must be readable from the state, got %q", got)
	}
}

// TestTheLastSessionOfARoleIsWhatALiveStageContinues answers the question the
// node layer actually asks: not "what did this stage do" but "what session is
// this worker in".
//
// This is what a pack buys and a solo run cannot have. `planner` owns intake and
// plan, so its session is the later of those two; `auditor` owns verify and audit
// and has its own. A single agent carrying the whole task has one session for
// everything, which is the trade `luna lead` makes.
//
// It walks the flow rather than the map, because map order would make "the last
// one" mean whatever Go felt like.
func TestTheLastSessionOfABriefIsWhatALiveStageContinues(t *testing.T) {
	// Two stages told the same thing, and a third told something else. The briefs
	// are the fixture's own rather than the shipped ones, so the test says what it
	// is about instead of depending on which stock text happens to repeat.
	building, judging := "You build.", "You judge."
	flow := []Stage{
		{ID: "first", Agent: "claude", Brief: building},
		{ID: "second", Agent: "claude", Brief: building},
		{ID: "third", Agent: "claude", Brief: judging},
	}
	state := TaskState{
		Spent: map[StageID]Spend{
			"first":  {Session: "sess-build-1", Turns: 1},
			"second": {Session: "sess-build-2", Turns: 1},
			"third":  {Session: "sess-judge", Turns: 1},
		},
	}

	if got := state.SessionOf(flow, building); got != "sess-build-2" {
		t.Errorf("a worker continues the last session it opened, got %q", got)
	}
	// Not the builder's, which is the whole point of keeping them apart: a judge
	// continuing the builder's session reads its reasoning rather than the
	// delivery.
	if got := state.SessionOf(flow, judging); got != "sess-judge" {
		t.Errorf("a worker must not continue another's session, got %q", got)
	}
	if got := state.SessionOf(flow, "told nothing of the sort"); got != "" {
		t.Errorf("a brief that has not run has no session to continue, got %q", got)
	}
}

// TestSoloCollapsesEveryRoleOntoOne is the mode `luna lead` runs.
//
// One agent carrying the task means one worktree and one session, and both are
// keyed by the brief — so the collapse is what makes them survive from stage to
// stage. A mechanical stage is left alone: a stage that starts no agent has
// nobody to be.
func TestSoloCollapsesEveryRoleOntoOne(t *testing.T) {
	pack := DefaultFlow()
	solo := Solo(pack)

	if len(solo) != len(pack) {
		t.Fatalf("a solo flow has the same stages, got %d against %d", len(solo), len(pack))
	}

	for i, stage := range solo {
		switch {
		case pack[i].Mechanical() && stage.Agent != "":
			t.Errorf("%q starts no agent and was given %q", stage.ID, stage.Agent)
		case !pack[i].Mechanical() && stage.Brief != SoloBrief:
			t.Errorf("%q kept its own brief rather than the one solo brief", stage.ID)
		case !pack[i].Mechanical() && len(stage.ToolsDeny) != 0:
			t.Errorf("%q kept denials a single agent cannot honour: %v", stage.ID, stage.ToolsDeny)
		}
	}

	// The pack it was built from keeps its own briefs: the collapse copies rather
	// than editing a flow the caller may still be holding.
	if stageIn(pack, "build").Brief == SoloBrief {
		t.Error("collapsing wrote through the flow it was given")
	}
}

// TestSoloIsNotInTheFingerprint is what lets a task change mode mid-run.
//
// The brief is policy rather than history — the reducer never reads it, only the
// node does — so it is out of the fingerprint by the same rule that keeps gate
// criteria and verifier commands out. A task begun solo replays against a pack
// and the other way round; if it did not, choosing the mode would be a decision
// nobody could revisit.
func TestSoloIsNotInTheFingerprint(t *testing.T) {
	pack := DefaultFlow()

	if Fingerprint(Solo(pack)) != Fingerprint(pack) {
		t.Error("collapsing the roles changed the flow's identity, so a task cannot change mode")
	}
}
