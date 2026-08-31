package cli

import (
	"strings"
	"testing"
	"time"

	"github.com/brunoomariano/luna/src/internal/agent"
	"github.com/brunoomariano/luna/src/internal/fsm"
)

// TestProfileNamesAreListedSorted covers the message someone sees after a typo.
func TestProfileNamesAreListedSorted(t *testing.T) {
	cfg := from(t, map[string]string{ProfilesKey: "zulu,alpha"})

	names := cfg.ProfileNames()
	if len(names) != 2 || names[0] != "alpha" || names[1] != "zulu" {
		t.Errorf("want the names sorted, got %v", names)
	}
}

// TestTheTurnBudgetIsProjectWide covers where the clock lives now. It is a
// property of the project, not of a profile: profiles stopped deciding
// anything, and how long a suite takes is a fact about the repository rather
// than about who answers a gate.

func TestTheTurnBudgetIsProjectWide(t *testing.T) {
	cfg := from(t, map[string]string{"turn_budget": "45m"})

	if got := cfg.Turn(); got != 45*time.Minute {
		t.Errorf("want the configured turn budget, got %s", got)
	}
}

// TestAProjectWithNoBudgetStillHasAWatchdog covers the direction that matters.
//
// Falling back to no limit would mean a project that never set one has tasks
// that hang forever.
func TestAProjectWithNoBudgetStillHasAWatchdog(t *testing.T) {
	cfg := from(t, map[string]string{"editor": "vi"})

	if got := cfg.Turn(); got != fsm.DefaultBudgets().Turn {
		t.Errorf("want the shipped budget, got %s", got)
	}
}

// TestAMalformedBudgetIsRefused covers the fail-loud rule.
//
// Someone who wrote `turn_budget = "30"` believes they tightened the watchdog. A
// silent fallback would leave them believing it.
func TestAMalformedBudgetIsRefused(t *testing.T) {
	_, err := ConfigFrom(nil, map[string]string{"turn_budget": "30"})

	if err == nil {
		t.Fatal("a budget that is not a duration must be reported")
	}
	if !strings.Contains(err.Error(), "turn_budget") {
		t.Errorf("the error should name the setting, got %v", err)
	}
	if !strings.Contains(err.Error(), "30m") {
		t.Errorf("the error should show the expected shape, got %v", err)
	}
}

// TestEveryStageTheShippedFlowRunsCarriesWhatItNeeds is the check that replaced
// the role catalogue's own.
//
// A stage used to name a role that a table resolved, and the failure mode was a
// stage naming one nothing defined. With the brief absorbed, the same gap is a
// stage shipping with no agent or no brief — the file is the only place either
// can come from now, so this is what "it resolves" means.
func TestEveryStageTheShippedFlowRunsCarriesWhatItNeeds(t *testing.T) {
	for _, stage := range fsm.DefaultFlow() {
		if stage.Mechanical() {
			continue
		}
		if stage.Agent == "" {
			t.Errorf("stage %q ships without an agent to run it", stage.ID)
		}
		if stage.Brief == "" {
			t.Errorf("stage %q ships without a brief, so its agent is told nothing", stage.ID)
		}
	}
}

// TestTheOldInterpreterKeyNamesItsReplacement is what a rename owes the people
// who wrote the old name down.
//
// Falling through to the unknown-key error would say the setting is not
// recognised and not that it moved, and the person would then have to find out
// which name replaced it — which is the search the rename was meant to end.
func TestTheOldInterpreterKeyNamesItsReplacement(t *testing.T) {
	err := CheckConfigKey("interpreter")

	if err == nil {
		t.Fatal("the old spelling was accepted silently")
	}
	if !strings.Contains(err.Error(), "is now `lead_harness`") {
		t.Errorf("the refusal does not name the replacement: %v", err)
	}
	// And not merely because the replacement appears in a list of every key: the
	// point is being told what this one became.
	if strings.Contains(err.Error(), "expected") {
		t.Errorf("the refusal fell through to the unknown-key message: %v", err)
	}
}

// TestTheLeadHarnessIsReadUnderItsOwnName is the other half: the new spelling
// works, so the refusal above is a rename rather than a removal.
func TestTheLeadHarnessIsReadUnderItsOwnName(t *testing.T) {
	cfg := from(t, map[string]string{"lead_harness": "codex"})

	if cfg.LeadHarness != "codex" {
		t.Errorf("lead_harness = %q, want codex", cfg.LeadHarness)
	}
}

// TestTheRolesThatWriteAreNotGated covers the other side.
//
// Denying the implementer would stop the task rather than protect it — gating is
// for the roles that judge, not for every role.
func TestTheStagesThatWriteAreNotGated(t *testing.T) {
	for _, stage := range fsm.DefaultFlow() {
		if stage.Mechanical() || len(stage.Produces) == 0 {
			continue
		}
		// A stage that produces code has to be able to write it. Denying a tool
		// here would not make the delivery safer — it would make it impossible,
		// and the stage would fail for a reason nothing in the flow explains.
		if stage.Gated() && containsArtifact(stage.Produces, "code") {
			t.Errorf("stage %q produces code and must keep its tools, got %v", stage.ID, stage.ToolsDeny)
		}
	}
}

// containsArtifact reports whether the list names the artifact.
func containsArtifact(list []fsm.Artifact, want fsm.Artifact) bool {
	for _, a := range list {
		if a == want {
			return true
		}
	}
	return false
}

// TestEveryGatedRoleShipsOnAHarnessThatCanGateIt guards the seam between the two
// tables.
//
// A role denying tools on an agent Luna cannot gate stops the task. Shipping that
// combination by default would mean every review stage fails on a fresh install.
func TestEveryGatedStageShipsOnAHarnessThatCanGateIt(t *testing.T) {
	for _, name := range fsm.FlowNames() {
		flow, err := fsm.FlowNamed(name)
		if err != nil {
			t.Fatalf("FlowNamed(%q): %v", name, err)
		}

		for _, stage := range flow {
			if !stage.Gated() {
				continue
			}
			if !agent.CanGate(stage.Agent) {
				t.Errorf("flow %q stage %q denies tools on %q, which Luna cannot gate",
					name, stage.ID, stage.Agent)
			}
		}
	}
}

// TestAnUnknownCapabilityIsRefused covers the direction a typo fails in.
//
// A misspelled capability would leave the role running with the tool it was
// supposed to lose, and nothing saying so — which is the failure the separation cares
// about most.
func TestAnUnknownCapabilityIsRefused(t *testing.T) {
	_, err := fsm.ParseStage("id = \"audit\"\ntools_deny = [\"Edt\"]\n", "audit.toml")

	if err == nil {
		t.Fatal("a misspelled capability must stop the load")
	}
	if !strings.Contains(err.Error(), "Edt") {
		t.Errorf("the error should name what was typed, got %v", err)
	}
	if !strings.Contains(err.Error(), "Edit") {
		t.Errorf("the error should name what was expected, got %v", err)
	}
}

// TestEveryProfileKeyIsRefused. A profile holds nothing but its name since
// Whether a gate waits is the stage's declaration, who answers is the
// knob, and the watchdog's clock is project-wide.
//
// Refused rather than ignored, and that is the whole of it: a config that loads
// and decides nothing is the silent kind of wrong, because the file keeps reading
// like supervision while nothing supervises.
//
// The retired keys used to be refused one by one, each naming where its setting
// had moved. That is worth writing for a config somebody already has, and this
// project has no released version and so no such config.
func TestEveryProfileKeyIsRefused(t *testing.T) {
	for _, key := range []string{"waits", "turn_budget", "idle_budget", "tool_budget", "anything"} {
		err := assignProfile(&Config{}, "p", key, "whatever", "a stock profile")

		if err == nil {
			t.Errorf("%q inside a profile was accepted", key)
			continue
		}
		if !strings.Contains(err.Error(), key) {
			t.Errorf("the refusal must name what arrived, got %v", err)
		}
		if !strings.Contains(err.Error(), "only its name") {
			t.Errorf("the refusal must say a profile holds no settings, got %v", err)
		}
	}
}

// TestANonPositiveBudgetIsNotABudget covers the guard on the value the parser
// accepts. A budget of zero or less means "call it stuck immediately", which is
// never what anybody meant to write.
func TestANonPositiveBudgetIsNotABudget(t *testing.T) {
	for _, budget := range []time.Duration{0, -time.Second} {
		cfg := Config{TurnBudget: budget}

		if got := cfg.Turn(); got != fsm.DefaultBudgets().Turn {
			t.Errorf("a budget of %s must fall back rather than fire at once, got %s", budget, got)
		}
	}
}

func TestTheMachineCanSelectCodexAsItsLeadHarness(t *testing.T) {
	cfg := from(t, map[string]string{"lead_harness": "codex"})
	if cfg.LeadHarness != "codex" {
		t.Errorf("want Codex as the lead harness, got %q", cfg.LeadHarness)
	}
}

func TestAnUnknownLeadHarnessIsRefusedBeforeItIsStored(t *testing.T) {
	_, err := ConfigFrom(nil, map[string]string{"lead_harness": "claude --model sonnet"})
	if err == nil {
		t.Fatal("a value the closed harness table cannot execute must be refused")
	}
	if !strings.Contains(err.Error(), "claude --model sonnet") || !strings.Contains(err.Error(), "codex") {
		t.Errorf("the error must name the invalid and expected harnesses, got %v", err)
	}
}

// from builds a config the way a command gets one: out of the settings the daemon
// holds for this project.
func from(t *testing.T, settings map[string]string) Config {
	t.Helper()

	cfg, err := ConfigFrom(nil, settings)
	if err != nil {
		t.Fatalf("building the config from %v: %v", settings, err)
	}
	return cfg
}
