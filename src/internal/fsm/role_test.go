package fsm

import (
	"strings"
	"testing"
)

// TestTheShippedFlowLeavesNoStageWithoutAnAgentItNeeds is the static check
// AuditRoles exists for.
//
// A stage that produces judgement and names no role runs mechanically: it
// delivers nothing and looks like it worked. Catching that here means it fails in
// `make ci` rather than after an afternoon of a task producing nothing.
func TestTheShippedFlowLeavesNoStageWithoutAnAgentItNeeds(t *testing.T) {
	if gaps := AuditRoles(DefaultFlow()); len(gaps) > 0 {
		for _, gap := range gaps {
			t.Errorf("stage %q produces %v and names no role", gap.Stage, gap.Produces)
		}
	}
}

// TestAStageThatProducesJudgementNeedsARole covers the detection itself.
func TestAStageThatProducesJudgementNeedsARole(t *testing.T) {
	writing := Stage{ID: "spec", Produces: []Artifact{"contract"}}
	if !writing.NeedsRole() {
		t.Error("a stage that writes a contract needs someone to write it")
	}

	// The same stage with a role is fine.
	writing.Role = "specifier"
	if writing.NeedsRole() {
		t.Error("a stage that names a role does not need one")
	}
}

// TestAMechanicalStageNeedsNoRole covers the other direction, which is the whole
// reason the mechanical path exists.
func TestAMechanicalStageNeedsNoRole(t *testing.T) {
	for _, stage := range []Stage{
		{ID: "setup", Produces: []Artifact{"worktree"}},
		{ID: "commit", Produces: []Artifact{"commit_sha"}},
	} {
		if !stage.Mechanical() {
			t.Errorf("%q names no role, so it is mechanical", stage.ID)
		}
		if stage.NeedsRole() {
			t.Errorf("%q produces nothing that needs judgement", stage.ID)
		}
	}
}

// TestAHumanReportStillNeedsARole covers the artifact that revealed the rule.
//
// `verify` produces ci_green from a command and dod_checked from a person's
// judgement. The artifact that needs an agent decides for the stage — a mixed
// stage is not mechanical.
func TestAHumanReportStillNeedsARole(t *testing.T) {
	mixed := Stage{
		ID:               "verify",
		Produces:         []Artifact{"ci_green"},
		ProducesForHuman: []Artifact{"dod_checked"},
	}

	if !mixed.NeedsRole() {
		t.Error("a checklist is judgement even when the pipeline beside it is not")
	}
}

// TestAuditRolesNamesTheStageAndWhatItOwes covers the report itself.
//
// The shipped flow has no gaps, so this builds one: a message that only said "a
// stage is missing a role" would cost a search through the flow to find which.
func TestAuditRolesNamesTheStageAndWhatItOwes(t *testing.T) {
	broken := []Stage{
		{ID: "setup", Produces: []Artifact{"worktree"}},                  // mechanical, fine
		{ID: "spec", Produces: []Artifact{"contract"}},                   // needs a role
		{ID: "diagnose", ProducesForHuman: []Artifact{"min_case"}},       // needs one too
		{ID: "build", Role: "implementer", Produces: []Artifact{"code"}}, // has one
	}

	gaps := AuditRoles(broken)

	if len(gaps) != 2 {
		t.Fatalf("want the two stages that need a role, got %+v", gaps)
	}
	if gaps[0].Stage != "spec" || gaps[1].Stage != "diagnose" {
		t.Errorf("want the gaps in flow order, got %+v", gaps)
	}
	// The report has to say what the stage owes, or finding the fix means reading
	// the flow again.
	if len(gaps[0].Produces) != 1 || gaps[0].Produces[0] != "contract" {
		t.Errorf("want the artifact that needs judgement, got %v", gaps[0].Produces)
	}
	// A human-read artifact counts: nobody downstream would miss it, which is
	// exactly why the check has to (INV-core-11).
	if len(gaps[1].Produces) != 1 || gaps[1].Produces[0] != "min_case" {
		t.Errorf("want the human-read artifact reported, got %v", gaps[1].Produces)
	}
}

// TestAuditRolesIgnoresAStageThatOnlyRunsCommands covers the silence that is
// correct.
func TestAuditRolesIgnoresAStageThatOnlyRunsCommands(t *testing.T) {
	mechanical := []Stage{
		{ID: "setup", Produces: []Artifact{"worktree"}},
		{ID: "commit", Produces: []Artifact{"commit_sha"}},
	}

	if gaps := AuditRoles(mechanical); len(gaps) != 0 {
		t.Errorf("git needs no agent, got %+v", gaps)
	}
}

// TestGatedReportsWhetherAnythingWasDenied covers the question the node asks
// before starting an agent.
func TestGatedReportsWhetherAnythingWasDenied(t *testing.T) {
	if (Role{Agent: "claude"}).Gated() {
		t.Error("a role that denies nothing is not gated")
	}
	if !(Role{Agent: "claude", ToolsDeny: []Capability{CapEdit}}).Gated() {
		t.Error("a role that denies anything is gated")
	}
}

// TestDeniesWritingNeedsBothCapabilities covers the question the coarse harnesses
// can answer.
//
// codex denies writing wholesale and takes no tool names, so a role that denies
// both maps onto it exactly and one that denies only Edit does not — it could
// still create a file, which is not "cannot change the work it judges"
// (ADR-0042).
func TestDeniesWritingNeedsBothCapabilities(t *testing.T) {
	cases := []struct {
		denied []Capability
		want   bool
	}{
		{[]Capability{CapEdit, CapWrite}, true},
		{[]Capability{CapWrite, CapEdit}, true}, // order does not matter
		{[]Capability{CapEdit}, false},
		{[]Capability{CapWrite}, false},
		{nil, false},
	}

	for _, c := range cases {
		if got := (Role{ToolsDeny: c.denied}).DeniesWriting(); got != c.want {
			t.Errorf("denying %v: want %v, got %v", c.denied, c.want, got)
		}
	}
}

// TestParseCapabilityRefusesWhatItDoesNotKnow covers the direction a typo fails
// in.
//
// A misspelled capability that parsed would leave the role running with the tool
// it was supposed to lose, and nothing saying so — the failure INV-core-7 cares
// about most.
func TestParseCapabilityRefusesWhatItDoesNotKnow(t *testing.T) {
	for _, known := range KnownCapabilities() {
		got, err := ParseCapability(string(known))
		if err != nil || got != known {
			t.Errorf("%q must parse: got %q, %v", known, got, err)
		}
	}

	// Case matters: the role file holds the capability, and `edit` is a harness's
	// spelling rather than the capability's name (ADR-0042).
	for _, bad := range []string{"Edt", "edit", "", "Delete"} {
		if _, err := ParseCapability(bad); err == nil {
			t.Errorf("%q must be refused", bad)
		}
	}
}

// TestTheCapabilityErrorNamesTheAlternatives covers the house rule that an error
// says what arrived and what was wanted.
func TestTheCapabilityErrorNamesTheAlternatives(t *testing.T) {
	_, err := ParseCapability("Edt")

	if err == nil {
		t.Fatal("a typo must be reported")
	}
	for _, want := range []string{"Edt", "Edit", "Write"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("the error should mention %q, got %v", want, err)
		}
	}
}
