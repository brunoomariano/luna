package fsm

import (
	"strings"
	"testing"
)

// TestAFlowWithNoContextKeyIsFresh pins the default. Every flow written before
// the setting existed must keep behaving the way it did, or the demotion of the
// fresh-context rule would have changed what those flows do.
func TestAFlowWithNoContextKeyIsFresh(t *testing.T) {
	var unset StageContext
	if !unset.Fresh() {
		t.Error("a stage that never mentions context must start clean")
	}
	if !ContextFresh.Fresh() {
		t.Error("fresh must read as fresh")
	}
	if ContextLive.Fresh() {
		t.Error("live must not read as fresh")
	}
}

// TestAnUnknownContextIsRefusedRatherThanDefaulted covers the typo. Silently
// meaning "fresh" would look like a setting that was applied, and the whole
// point of the setting is to measure which value serves.
func TestAnUnknownContextIsRefusedRatherThanDefaulted(t *testing.T) {
	_, err := ParseStageContext("liv", "stages/070-build.toml:4")
	if err == nil {
		t.Fatal("want a refusal for an unknown context, got acceptance")
	}
	for _, want := range []string{"liv", "fresh", "live", "070-build.toml"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("want the refusal to name %q, got %q", want, err)
		}
	}
}

// TestBothContextValuesParse covers the two spellings a flow may use, and the
// empty key that every flow written before the setting existed has.
func TestBothContextValuesParse(t *testing.T) {
	for _, tc := range []struct {
		value string
		want  StageContext
	}{
		{"fresh", ContextFresh},
		{"live", ContextLive},
		{"", ContextFresh},
	} {
		got, err := ParseStageContext(tc.value, "stages/070-build.toml:4")
		if err != nil {
			t.Errorf("ParseStageContext(%q): %v", tc.value, err)
			continue
		}
		if got != tc.want {
			t.Errorf("ParseStageContext(%q) = %q, want %q", tc.value, got, tc.want)
		}
	}
}

// TestAnEmptyFlowHasNoChainToBreak covers the degenerate input. A flow with no
// stages is a different problem, reported by a different check.
func TestAnEmptyFlowHasNoChainToBreak(t *testing.T) {
	if gaps := AuditContextChain(nil); gaps != nil {
		t.Errorf("want no gaps for an empty flow, got %+v", gaps)
	}
}

// TestAReviewerCannotContinueTheImplementersSession is the half of the old
// fresh-context rule that survived its demotion.
//
// A reviewer inheriting the session that wrote the code would be reading its own
// reasoning rather than the delivery. Verification at the exit catches a bad
// artifact; it cannot catch a review that agreed with itself, because that
// failure looks exactly like a stage that went well.
func TestAReviewerCannotContinueTheImplementersSession(t *testing.T) {
	flow := []Stage{
		{ID: "build", Role: "implementer"},
		{ID: "code-review", Role: "reviewer", Context: ContextLive},
	}

	gaps := AuditContextChain(flow)
	if len(gaps) != 1 {
		t.Fatalf("want the crossing refused, got %d gaps", len(gaps))
	}
	if gaps[0].Stage != "code-review" || gaps[0].From != "build" {
		t.Errorf("want code-review named as continuing build, got %+v", gaps[0])
	}
	if gaps[0].Role != "implementer" {
		t.Errorf("want the report to name the role being inherited, got %q", gaps[0].Role)
	}
}

// TestOneRoleAcrossTwoStagesMayContinue is the case the setting exists for: the
// same role carrying on, which is where the cost saving lives.
func TestOneRoleAcrossTwoStagesMayContinue(t *testing.T) {
	flow := []Stage{
		{ID: "build", Role: "implementer"},
		{ID: "refactor", Role: "implementer", Context: ContextLive},
	}

	if gaps := AuditContextChain(flow); len(gaps) != 0 {
		t.Errorf("want one role continuing to be allowed, got %+v", gaps)
	}
}

// TestTheFirstStageHasNothingToContinue covers the flow that asks to resume
// before anything has run. The harness would start a fresh conversation and
// report success, so the stage would silently lose what it asked to keep.
func TestTheFirstStageHasNothingToContinue(t *testing.T) {
	flow := []Stage{{ID: "setup", Role: "implementer", Context: ContextLive}}

	gaps := AuditContextChain(flow)
	if len(gaps) != 1 {
		t.Fatalf("want the first stage refused, got %d gaps", len(gaps))
	}
	if gaps[0].From != "" {
		t.Errorf("want no predecessor named, got %q", gaps[0].From)
	}
}

// TestTheShippedFlowAsksForNothingItCannotHave runs the audit over what actually
// ships. A rule nothing exercises is a rule nobody has tested.
func TestTheShippedFlowAsksForNothingItCannotHave(t *testing.T) {
	if gaps := AuditContextChain(DefaultFlow()); len(gaps) != 0 {
		t.Errorf("the shipped flow breaks the context chain: %+v", gaps)
	}
}

// TestContextDoesNotMoveTheFingerprint is the classification asserted against
// the thing it protects.
//
// A fingerprint records what delivering means, so a replay against a changed
// flow is refused. Switching a stage between fresh and live changes what the
// call costs and how the agent is briefed — not what the stage owes. A task
// halfway through must be able to switch without its log becoming unreplayable,
// which is the same reasoning that keeps an artifact's path out.
func TestContextDoesNotMoveTheFingerprint(t *testing.T) {
	fresh := []Stage{
		{ID: "build", Role: "implementer", Produces: []Artifact{"code"}},
		{ID: "refactor", Role: "implementer", Requires: []Artifact{"code"}},
	}
	live := []Stage{
		{ID: "build", Role: "implementer", Produces: []Artifact{"code"}},
		{ID: "refactor", Role: "implementer", Requires: []Artifact{"code"}, Context: ContextLive},
	}

	if Fingerprint(fresh) != Fingerprint(live) {
		t.Errorf("switching a stage to live moved the fingerprint (%s vs %s), "+
			"which would refuse the replay of every task already running",
			Fingerprint(fresh), Fingerprint(live))
	}
}

// TestMemoryIsOffUnlessAStageSaysOtherwise pins the default, and the reason for
// it: a shared project memory that every stage of every task writes to fills
// with the transient, so reading is the cheap half and writing is the part worth
// declaring.
func TestMemoryIsOffUnlessAStageSaysOtherwise(t *testing.T) {
	var unset StageMemory
	if unset.Enabled() {
		t.Error("a stage that never mentions memory must not run with it")
	}
	if !MemoryOn.Enabled() {
		t.Error("a stage that asked for memory must get it")
	}
	if MemoryOff.Enabled() {
		t.Error("off must read as off")
	}
}

// TestAnUnknownMemoryValueIsRefused covers the typo, for the reason the context
// key refuses one: silently meaning "off" looks like a setting that was applied,
// and a stage briefed without the project's history would give no sign of it.
func TestAnUnknownMemoryValueIsRefused(t *testing.T) {
	for _, value := range []string{"on ", "yes", "true"} {
		if _, err := ParseStageMemory(value, "stages/030-intake.toml:5"); err == nil {
			t.Errorf("ParseStageMemory(%q) was accepted", value)
		}
	}

	for _, tc := range []struct {
		value string
		want  StageMemory
	}{{"on", MemoryOn}, {"off", MemoryOff}, {"", MemoryOff}} {
		got, err := ParseStageMemory(tc.value, "stages/030-intake.toml:5")
		if err != nil {
			t.Errorf("ParseStageMemory(%q): %v", tc.value, err)
			continue
		}
		if got != tc.want {
			t.Errorf("ParseStageMemory(%q) = %q, want %q", tc.value, got, tc.want)
		}
	}
}

// TestAStageFileCanDeclareContextAndMemory covers both keys reaching the stage
// through the loader, which is the only way a project sets either.
func TestAStageFileCanDeclareContextAndMemory(t *testing.T) {
	stage, err := ParseStage(`
id       = "refactor"
role     = "implementer"
requires = ["code"]
produces = ["clean"]
context  = "live"
memory   = "on"

[verify.clean]
kind = "existence"
`, "stages/080-refactor.toml")
	if err != nil {
		t.Fatalf("ParseStage: %v", err)
	}

	if stage.Context != ContextLive {
		t.Errorf("want the declared context, got %q", stage.Context)
	}
	if !stage.Memory.Enabled() {
		t.Errorf("want the declared memory, got %q", stage.Memory)
	}
}
