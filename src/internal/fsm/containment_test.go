package fsm

import (
	"strings"
	"testing"
)

// TestOnlySetupRunsOutsideTheSandbox is what INV-4 asks for in as many words: an
// exception that is not pinned is one the next stage inherits by accident.
//
// It reads every shipped flow rather than the default one, because a lean flow
// adding the key would be exactly the quiet spread this refuses.
func TestOnlySetupRunsOutsideTheSandbox(t *testing.T) {
	for _, name := range FlowNames() {
		flow, err := FlowNamed(name)
		if err != nil {
			t.Fatalf("FlowNamed(%q): %v", name, err)
		}
		for _, gap := range AuditContainment(flow) {
			t.Errorf("flow %q: stage %q runs outside the sandbox, and the exemption "+
				"belongs to %q alone", name, gap.Stage, TheUncontainedStage)
		}
	}
}

// TestTheExemptionIsRefusedForAnyOtherStage drives the check itself, since the
// shipped flows are correct and a check nothing exercises proves nothing.
func TestTheExemptionIsRefusedForAnyOtherStage(t *testing.T) {
	gaps := AuditContainment([]Stage{
		{ID: TheUncontainedStage, Agent: "claude", Uncontained: true},
		{ID: "forge", Agent: "claude", Uncontained: true},
		{ID: "review", Agent: "claude"},
	})

	if len(gaps) != 1 {
		t.Fatalf("want the one stage that is not the exception, got %+v", gaps)
	}
	if gaps[0].Stage != "forge" {
		t.Errorf("the wrong stage was reported: %q", gaps[0].Stage)
	}
}

// TestSetupIsTheStageThatRunsUncontained is the other direction: the exemption
// exists and is used, so a change that quietly removed it would be caught too.
func TestSetupIsTheStageThatRunsUncontained(t *testing.T) {
	stage := stageIn(DefaultFlow(), TheUncontainedStage)

	if !stage.Uncontained {
		t.Errorf("%q is meant to run uncontained and does not", TheUncontainedStage)
	}
	// And it is the stage that reads the sandbox's own configuration, which is the
	// whole reason it cannot be run under it.
	if !strings.Contains(stage.Brief, ".ai-jail") {
		t.Errorf("%q runs uncontained and does not read the containment it reports on",
			TheUncontainedStage)
	}
}

// TestAContainmentFlagIsTrueOrFalseAndNothingElse. `1`, `yes` and `on` are all
// things somebody might write, and accepting some of them means the rest fail
// silently as false — which for an exemption from containment is the wrong
// direction to be quiet in.
func TestAContainmentFlagIsTrueOrFalseAndNothingElse(t *testing.T) {
	for _, written := range []string{"1", "yes", "on", "True", ""} {
		_, err := ParseStage("id = \"setup\"\nuncontained = "+written+"\n", "setup.toml")
		if err == nil {
			t.Errorf("`uncontained = %s` was accepted", written)
		}
	}

	stage, err := ParseStage("id = \"setup\"\nuncontained = true\n", "setup.toml")
	if err != nil {
		t.Fatalf("`uncontained = true`: %v", err)
	}
	if !stage.Uncontained {
		t.Error("`uncontained = true` did not take")
	}
}
