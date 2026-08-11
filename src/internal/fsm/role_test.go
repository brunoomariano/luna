package fsm

import "testing"

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
