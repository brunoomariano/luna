package fsm

import (
	"strings"
	"testing"
)

// orderFlow is a two-stage flow: one that writes and one that reviews.
func orderFlow() []Stage {
	return []Stage{
		{
			ID:       "build",
			Role:     "implementer",
			Agent:    "claude",
			Brief:    "write the code",
			Skills:   []string{"go"},
			Requires: []Artifact{TaskID},
			Produces: []Artifact{"code"},
		},
		{
			ID:               "review",
			Role:             "reviewer",
			Agent:            "codex",
			Brief:            "review it",
			ToolsDeny:        []Capability{CapEdit, CapWrite},
			Requires:         []Artifact{"code"},
			Produces:         []Artifact{"verdict"},
			ProducesForHuman: []Artifact{"report"},
		},
	}
}

func TestTheFirstOrderNamesTheFlowsFirstStage(t *testing.T) {
	state := NewTaskState("LUNA-1", KindFeature)

	order, err := NextOrder(state, orderFlow())
	if err != nil {
		t.Fatalf("NextOrder: %v", err)
	}

	if order.Kind != OrderRun {
		t.Errorf("kind = %q, want %q", order.Kind, OrderRun)
	}
	if order.Stage != "build" {
		t.Errorf("stage = %q, want build", order.Stage)
	}
	if order.Agent != "claude" {
		t.Errorf("agent = %q, want claude — the catalogue resolves the role", order.Agent)
	}
	if order.Worktree != "luna-LUNA-1-implementer" {
		t.Errorf("worktree = %q, want luna-LUNA-1-implementer", order.Worktree)
	}
	if order.Base != "" {
		t.Errorf("base = %q, want empty: nothing has been delivered yet", order.Base)
	}
}

// TestTheOrderCarriesWhatTheRoleMayNotDo is the role's denials reaching the lead.
// A denial that stays in the catalogue is a denial the harness never applies.
func TestTheOrderCarriesWhatTheRoleMayNotDo(t *testing.T) {
	state := NewTaskState("LUNA-1", KindFeature)
	state.Status = StatusStageDone
	state.Stage = "build"
	state.Context.Artifacts["code"] = true

	order, err := NextOrder(state, orderFlow())
	if err != nil {
		t.Fatalf("NextOrder: %v", err)
	}

	if order.Role != "reviewer" {
		t.Fatalf("role = %q, want reviewer", order.Role)
	}
	if len(order.Deny) != 2 {
		t.Fatalf("deny = %v, want Edit and Write", order.Deny)
	}
	if order.Worktree == WorktreeName("LUNA-1", "implementer") {
		t.Error("the reviewer got the implementer's worktree — the separation is " +
			"filesystem-deep in this design, and sharing the directory gives that away")
	}
}

// TestTheOrderOwesWhatTheContractOwes carries both kinds of product. A stage
// that owes a human-facing report and is not told so will not write one.
func TestTheOrderOwesWhatTheContractOwes(t *testing.T) {
	state := NewTaskState("LUNA-1", KindFeature)
	state.Status = StatusStageDone
	state.Stage = "build"
	state.Context.Artifacts["code"] = true

	order, _ := NextOrder(state, orderFlow())

	got := JoinArtifacts(order.Produces)
	if got != "report,verdict" {
		t.Errorf("produces = %q, want report,verdict — ProducesForHuman counts (INV-3)", got)
	}
}

// TestARunningTaskGetsTheSameOrderTwice is what makes the order safe to ask for
// again. A lead that crashed mid-stage, or a person driving by hand, must not
// advance the flow by asking.
func TestARunningTaskGetsTheSameOrderTwice(t *testing.T) {
	state := NewTaskState("LUNA-1", KindFeature)
	state.Status = StatusRunning
	state.Stage = "build"

	first, err := NextOrder(state, orderFlow())
	if err != nil {
		t.Fatalf("NextOrder: %v", err)
	}
	second, _ := NextOrder(state, orderFlow())

	if first.Stage != "build" || second.Stage != "build" {
		t.Errorf("asking twice moved the stage: %q then %q", first.Stage, second.Stage)
	}
	if first.Text() != second.Text() {
		t.Error("the same state produced two different orders")
	}
}

func TestAGateIsAnOrderToWaitNotToRun(t *testing.T) {
	state := NewTaskState("LUNA-1", KindFeature)
	state.Status = StatusAwaitingGate
	state.Stage = "build"
	state.Gate = &PendingGate{Kind: GateConfirm, Stage: "build", Reason: "about to write"}

	order, err := NextOrder(state, orderFlow())
	if err != nil {
		t.Fatalf("NextOrder: %v", err)
	}

	if order.Kind != OrderWait {
		t.Errorf("kind = %q, want %q", order.Kind, OrderWait)
	}
	if order.Agent != "" {
		t.Errorf("agent = %q, want empty: a waiting task starts nobody", order.Agent)
	}
	if !strings.Contains(order.Reason, "about to write") {
		t.Errorf("reason = %q, want the gate's own reason", order.Reason)
	}
}

// TestAGateWithNothingToSayStillSaysSomething covers the two fallbacks. A wait
// order whose reason is empty tells whoever is driving nothing at all, which is
// the silent stop INV-5 exists against.
func TestAGateWithNothingToSayStillSaysSomething(t *testing.T) {
	for _, tc := range []struct {
		name string
		gate *PendingGate
		want string
	}{
		{"no gate recorded", nil, "waiting on a person"},
		{"a gate with no reason", &PendingGate{Kind: GateConfirm, Stage: "build"}, "confirm"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			state := NewTaskState("LUNA-1", KindFeature)
			state.Status = StatusAwaitingGate
			state.Gate = tc.gate

			order, err := NextOrder(state, orderFlow())
			if err != nil {
				t.Fatalf("NextOrder: %v", err)
			}
			if !strings.Contains(order.Reason, tc.want) {
				t.Errorf("reason = %q, want it to mention %q", order.Reason, tc.want)
			}
		})
	}
}

func TestABlockedTaskSaysWhy(t *testing.T) {
	state := NewTaskState("LUNA-1", KindFeature)
	state.Status = StatusBlocked
	state.Blocked = "the build never came back"

	order, _ := NextOrder(state, orderFlow())

	if order.Kind != OrderBlocked {
		t.Errorf("kind = %q, want %q", order.Kind, OrderBlocked)
	}
	if order.Reason != "the build never came back" {
		t.Errorf("reason = %q, want the recorded block", order.Reason)
	}
}

// TestAnAbandonedTaskIsDoneButNotFinished keeps the two endings apart. An audit
// that cannot tell a task that delivered from one that was called off is missing
// the more interesting of the two.
func TestAnAbandonedTaskIsDoneButNotFinished(t *testing.T) {
	abandoned := NewTaskState("LUNA-1", KindFeature)
	abandoned.Status = StatusAbandoned

	finished := NewTaskState("LUNA-2", KindFeature)
	finished.Status = StatusDone

	a, _ := NextOrder(abandoned, orderFlow())
	f, _ := NextOrder(finished, orderFlow())

	if a.Kind != OrderDone || f.Kind != OrderDone {
		t.Fatalf("both should be done: %q and %q", a.Kind, f.Kind)
	}
	if a.Reason == f.Reason {
		t.Errorf("both endings gave the same reason %q — the log distinguishes them "+
			"and so should the order", a.Reason)
	}
}

func TestTheEndOfTheFlowIsAnOrderToStop(t *testing.T) {
	state := NewTaskState("LUNA-1", KindFeature)
	state.Status = StatusStageDone
	state.Stage = "review" // the last stage of orderFlow

	order, err := NextOrder(state, orderFlow())
	if err != nil {
		t.Fatalf("NextOrder: %v", err)
	}
	if order.Kind != OrderDone {
		t.Errorf("kind = %q, want %q", order.Kind, OrderDone)
	}
}

func TestAStageOutsideTheFlowIsRefused(t *testing.T) {
	state := NewTaskState("LUNA-1", KindFeature)
	state.Status = StatusRunning
	state.Stage = "invented"

	if _, err := NextOrder(state, orderFlow()); err == nil {
		t.Fatal("a stage the flow has never heard of produced an order instead of an error")
	}
}

// TestAStageWithNoAgentStillProducesAnOrder is the deliberate non-refusal. The
// engine says which stage runs; whether anything can run it is the node layer's
// question, and refusing here would make the flow undrivable rather than
// diagnosable.
func TestAStageWithNoAgentStillProducesAnOrder(t *testing.T) {
	state := NewTaskState("LUNA-1", KindFeature)

	flow := orderFlow()
	flow[0].Agent = ""

	order, err := NextOrder(state, flow)
	if err != nil {
		t.Fatalf("NextOrder with no agent: %v", err)
	}
	if order.Kind != OrderRun || order.Stage != "build" {
		t.Errorf("got %q on %q, want a run order on build", order.Kind, order.Stage)
	}
	if order.Agent != "" {
		t.Errorf("agent = %q, want empty — nothing defined it", order.Agent)
	}
}

// TestAMechanicalStageNamesNoRole covers the stages Luna runs itself: setup is a
// worktree, commit is git.
func TestAMechanicalStageNamesNoRole(t *testing.T) {
	flow := []Stage{{ID: "setup", Requires: []Artifact{TaskID}, Produces: []Artifact{"worktree"}}}
	state := NewTaskState("LUNA-1", KindFeature)

	order, _ := NextOrder(state, flow)

	if order.Role != "" || order.Agent != "" {
		t.Errorf("role=%q agent=%q, want both empty on a mechanical stage", order.Role, order.Agent)
	}
	if order.Worktree != "luna-LUNA-1" {
		t.Errorf("worktree = %q, want the task's own", order.Worktree)
	}
}

// TestTheBaseIsThePreviousStagesCommit is the handoff. The next agent starts from
// what was delivered, not from a description of it.
func TestTheBaseIsThePreviousStagesCommit(t *testing.T) {
	state := NewTaskState("LUNA-1", KindFeature)
	state.Status = StatusStageDone
	state.Stage = "build"
	state.Context.Artifacts["code"] = true
	state.Base = "d34db33f"

	order, _ := NextOrder(state, orderFlow())

	if order.Base != "d34db33f" {
		t.Errorf("base = %q, want the commit the previous stage delivered", order.Base)
	}
}

// TestWorkThatDidNotPassNeverBecomesTheBase is the rule the whole handoff rests
// on. A stage whose verification failed still produced a commit, and letting that
// commit become the next stage's starting point would build every later stage on
// work the contract rejected — silently, since the block is recorded and the base
// is not.
func TestWorkThatDidNotPassNeverBecomesTheBase(t *testing.T) {
	flow := []Stage{{
		ID:        "build",
		Role:      "implementer",
		Requires:  []Artifact{TaskID},
		Produces:  []Artifact{"code"},
		Verifiers: map[Artifact]Verifier{"code": Command{Run: "make test", Scope: ScopeTargeted}},
	}}

	state := NewTaskState("LUNA-1", KindFeature)
	state.Status = StatusRunning
	state.Stage = "build"
	state.Base = "old"

	after, err := Reduce(state, Complete{
		Flow:      flow,
		Delivered: []Artifact{"code"},
		Commit:    "rejected",
		Evidence: map[Artifact]Evidence{
			"code": {Verdict: VerdictFailed, Scope: ScopeTargeted, Detail: "two tests red"},
		},
	})
	if err != nil {
		t.Fatalf("Reduce: %v", err)
	}

	if after.Status != StatusBlocked {
		t.Fatalf("status = %q, want blocked: the verification failed", after.Status)
	}
	if after.Base != "old" {
		t.Errorf("base = %q, want it left at \"old\" — work that failed its own "+
			"verification must not become what the next stage builds on", after.Base)
	}
}

// TestAStageThatClosedMovesTheBase is the other direction, so the test above
// cannot be satisfied by never moving the base at all.
func TestAStageThatClosedMovesTheBase(t *testing.T) {
	flow := []Stage{{ID: "build", Requires: []Artifact{TaskID}, Produces: []Artifact{"code"}}}

	state := NewTaskState("LUNA-1", KindFeature)
	state.Status = StatusRunning
	state.Stage = "build"
	state.Base = "old"

	after, err := Reduce(state, Complete{
		Flow:      flow,
		Delivered: []Artifact{"code"},
		Commit:    "delivered",
		Evidence: map[Artifact]Evidence{
			"code": {Verdict: VerdictPassed, Scope: ScopeExistence},
		},
	})
	if err != nil {
		t.Fatalf("Reduce: %v", err)
	}
	if after.Base != "delivered" {
		t.Errorf("base = %q, want the commit the stage delivered", after.Base)
	}
}

// TestACompleteWithoutACommitLeavesTheBaseAlone covers the mechanical stages and
// the logs written before the field existed. Clearing the base would send the
// next stage back to the repository's own HEAD, discarding every stage before it.
func TestACompleteWithoutACommitLeavesTheBaseAlone(t *testing.T) {
	flow := []Stage{{ID: "build", Requires: []Artifact{TaskID}, Produces: []Artifact{"code"}}}

	state := NewTaskState("LUNA-1", KindFeature)
	state.Status = StatusRunning
	state.Stage = "build"
	state.Base = "earlier"

	after, err := Reduce(state, Complete{
		Flow:      flow,
		Delivered: []Artifact{"code"},
		Evidence:  map[Artifact]Evidence{"code": {Verdict: VerdictPassed, Scope: ScopeExistence}},
	})
	if err != nil {
		t.Fatalf("Reduce: %v", err)
	}
	if after.Base != "earlier" {
		t.Errorf("base = %q, want it left at \"earlier\"", after.Base)
	}
}

// TestTheOrderCarriesNoViewOfTheFlow is flow control staying out of the model, at
// the level of what the lead
// can see. An order that lists what comes next invites the lead to save a round
// trip by doing two stages, and the closed order is shaped against exactly that.
func TestTheOrderCarriesNoViewOfTheFlow(t *testing.T) {
	state := NewTaskState("LUNA-1", KindFeature)

	order, _ := NextOrder(state, orderFlow())
	text := order.Text()

	if strings.Contains(text, "review") {
		t.Errorf("the order for build mentions the stage after it:\n%s", text)
	}
}

func TestTheOrderRendersAsKeyValues(t *testing.T) {
	state := NewTaskState("LUNA-1", KindFeature)
	state.Status = StatusStageDone
	state.Stage = "build"
	state.Context.Artifacts["code"] = true

	text, _ := NextOrder(state, orderFlow())

	for _, want := range []string{
		"kind=run",
		"task=LUNA-1",
		"stage=review",
		"role=reviewer",
		"agent=codex",
		"worktree=luna-LUNA-1-reviewer",
		"deny=Edit,Write",
	} {
		if !strings.Contains(text.Text(), want) {
			t.Errorf("missing %q in:\n%s", want, text.Text())
		}
	}
}

// TestAMultiLineBriefStaysReadable is why the text shape is not just key=value.
// A brief is prose and a person is the reader; escaping the newlines would make
// the one field that has to be read the one field nobody can.
func TestAMultiLineBriefStaysReadable(t *testing.T) {
	flow := orderFlow()
	flow[0].Brief = "first line\nsecond line"

	state := NewTaskState("LUNA-1", KindFeature)
	text := mustOrder(t, state, flow).Text()

	if strings.Contains(text, `\n`) {
		t.Errorf("the brief was escaped rather than indented:\n%s", text)
	}
	if !strings.Contains(text, "brief=first line\n  second line") {
		t.Errorf("the brief lost its shape:\n%s", text)
	}
}

// TestAnEmptyFieldIsAbsentRatherThanBlank keeps the text shape parseable. A
// `base=` line says the base is the empty string, which is not the same as the
// task never having had one.
func TestAnEmptyFieldIsAbsentRatherThanBlank(t *testing.T) {
	state := NewTaskState("LUNA-1", KindFeature)
	text := mustOrder(t, state, orderFlow()).Text()

	for _, line := range strings.Split(text, "\n") {
		if strings.HasSuffix(line, "=") {
			t.Errorf("empty value printed as a key: %q", line)
		}
	}
}

func mustOrder(t *testing.T, state TaskState, flow []Stage) Order {
	t.Helper()
	order, err := NextOrder(state, flow)
	if err != nil {
		t.Fatalf("NextOrder: %v", err)
	}
	return order
}
