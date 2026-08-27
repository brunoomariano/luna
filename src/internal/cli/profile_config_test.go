package cli

import (
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/brunoomariano/luna/src/internal/agent"
	"github.com/brunoomariano/luna/src/internal/fsm"
)

// TestAnUnknownSettingInsideAProfileIsAnError covers the section's key list.
func TestAnUnknownSettingInsideAProfileIsAnError(t *testing.T) {
	_, err := LoadConfig(writeConfig(t, `
[profile.careful]
wait = ["confirm"]
`))

	if err == nil {
		t.Fatal("an unknown key inside a profile must be reported")
	}
	// The error names what arrived and says where settings went, because there is
	// no list of valid keys to offer any more: a profile holds only its name.
	if !strings.Contains(err.Error(), "wait") {
		t.Errorf("the error should name what arrived, got %v", err)
	}
	if !strings.Contains(err.Error(), "only its name") {
		t.Errorf("the error should say a profile holds no settings, got %v", err)
	}
}

// TestAnUnknownSectionIsAnError covers the one section that exists.
//
// A mistyped header would otherwise swallow every setting under it, silently.
func TestAnUnknownSectionIsAnError(t *testing.T) {
	_, err := LoadConfig(writeConfig(t, "[profiles.paranoid]\nwaits = []\n"))

	if err == nil {
		t.Fatal("an unknown section must be reported")
	}
	if !strings.Contains(err.Error(), "profile.<name>") {
		t.Errorf("the error should say what a section looks like, got %v", err)
	}
}

// TestAMalformedListIsReported covers the array parser's refusals.
func TestAMalformedListIsReported(t *testing.T) {
	cases := map[string]string{
		"not a list":        `waits = "confirm"`,
		"unquoted entry":    `waits = [confirm]`,
		"unclosed on line":  `waits = ["confirm",`,
		"a bare open track": `waits = [`,
	}

	for name, line := range cases {
		if _, err := LoadConfig(writeConfig(t, "[profile.p]\n"+line+"\n")); err == nil {
			t.Errorf("%s: want an error for %q", name, line)
		}
	}
}

// TestASettingSurvivesAProfileSection covers the root/section boundary.
//
// The editor is a root setting. A parser that leaked the current section would
// either reject it after a profile block or file it under the profile.
func TestASettingSurvivesAProfileSection(t *testing.T) {
	cfg := load(t, `
editor = "hx"

[profile.paranoid]
`)

	if cfg.Editor != "hx" {
		t.Errorf("want the root setting, got %q", cfg.Editor)
	}
	if !cfg.Defines("paranoid") {
		t.Error("want the profile too")
	}
}

// TestAHashInsideQuotesIsNotAComment covers the comment stripper.
//
// An editor command may legitimately contain one, and losing everything after it
// would leave a setting that looks right in the file and is wrong in memory.
func TestAHashInsideQuotesIsNotAComment(t *testing.T) {
	cfg := load(t, `editor = "sh -c 'edit #1'" # the real comment`)

	if cfg.Editor != "sh -c 'edit #1'" {
		t.Errorf("want the hash kept inside quotes, got %q", cfg.Editor)
	}
}

// TestProfileNamesAreListedSorted covers the message someone sees after a typo.
func TestProfileNamesAreListedSorted(t *testing.T) {
	cfg := load(t, "[profile.zulu]\n[profile.alpha]\n")

	names := cfg.ProfileNames()
	if len(names) != 2 || names[0] != "alpha" || names[1] != "zulu" {
		t.Errorf("want the names sorted, got %v", names)
	}
}

func load(t *testing.T, content string) Config {
	t.Helper()

	cfg, err := LoadConfig(writeConfig(t, content))
	if err != nil {
		t.Fatalf("loading config: %v", err)
	}
	return cfg
}

func writeConfig(t *testing.T, content string) string {
	t.Helper()

	path := filepath.Join(t.TempDir(), "config.toml")
	write(t, path, content)
	return path
}

// TestANestedProfileNameIsRejected covers the dotted-name refusal.
//
// `[profile.a.b]` names no profile this config can hold. Trimming it to "a" or
// "b" would silently define a profile nobody wrote.
func TestANestedProfileNameIsRejected(t *testing.T) {
	_, err := LoadConfig(writeConfig(t, "[profile.team.paranoid]\nturn_budget = \"1h\"\n"))

	if err == nil {
		t.Fatal("a dotted profile name must be reported")
	}
	if !strings.Contains(err.Error(), "team.paranoid") {
		t.Errorf("the error should name what was typed, got %v", err)
	}
}

// TestATrailingCommaInAListIsTolerated covers the skipped empty entry.
//
// The list is hand-parsed, and a trailing comma is the one piece of TOML slack
// worth keeping: it is what someone leaves behind after deleting an entry.
func TestATrailingCommaInAListIsTolerated(t *testing.T) {
	list, err := parseStringArray(`["scenarios", ]`, "config.toml:1")
	if err != nil {
		t.Fatalf("parseStringArray: %v", err)
	}
	if len(list) != 1 || list[0] != "scenarios" {
		t.Errorf("the entry before the trailing comma still counts, got %q", list)
	}
}

// TestTheTurnBudgetIsProjectWide covers where the clock lives now. It is a
// property of the project, not of a profile: profiles stopped deciding
// anything, and how long a suite takes is a fact about the repository rather
// than about who answers a gate.

func TestTheTurnBudgetIsProjectWide(t *testing.T) {
	cfg := load(t, "turn_budget = \"45m\"\n")

	if got := cfg.Turn(); got != 45*time.Minute {
		t.Errorf("want the configured turn budget, got %s", got)
	}
}

// TestAProjectWithNoBudgetStillHasAWatchdog covers the direction that matters.
//
// Falling back to no limit would mean a project that never set one has tasks
// that hang forever.
func TestAProjectWithNoBudgetStillHasAWatchdog(t *testing.T) {
	cfg := load(t, "editor = \"vi\"\n")

	if got := cfg.Turn(); got != fsm.DefaultBudgets().Turn {
		t.Errorf("want the shipped budget, got %s", got)
	}
}

// TestAMalformedBudgetIsRefused covers the fail-loud rule.
//
// Someone who wrote `turn_budget = "30"` believes they tightened the watchdog. A
// silent fallback would leave them believing it.
func TestAMalformedBudgetIsRefused(t *testing.T) {
	_, err := LoadConfig(writeConfig(t, "turn_budget = \"30\"\n"))

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

// TestAnUnknownSectionKindIsRefused covers the header.
func TestAnUnknownSectionKindIsRefused(t *testing.T) {
	_, err := LoadConfig(writeConfig(t, "[profiles.nightly]\nturn_budget = \"1h\"\n"))

	if err == nil {
		t.Fatal("a mistyped section must be reported")
	}
	if !strings.Contains(err.Error(), "profile.<name>") {
		t.Errorf("the error should say what a section looks like, got %v", err)
	}
}

// TestASectionWithNoNameIsRefused covers the header that opens nothing.
//
// `[profile]` and `[role]` name no thing to configure, and accepting them would
// file every setting under an empty name.
func TestASectionWithNoNameIsRefused(t *testing.T) {
	for _, header := range []string{"[profile]", "[role]", "[]"} {
		if _, err := LoadConfig(writeConfig(t, header+"\n")); err == nil {
			t.Errorf("%s names nothing and must be refused", header)
		}
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
	_, err := fsm.ParseStage("id = \"audit\"\nrole = \"auditor\"\ntools_deny = [\"Edt\"]\n", "audit.toml")

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
		_, err := LoadConfig(writeConfig(t, "[profile.p]\n"+key+" = \"whatever\"\n"))

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

// TestAMalformedListSaysWhatWentWrongWithIt covers the three ways a list is
// written badly, each with its own message.
//
// `tools_deny` is the config's only list now — the profile section holds no
// A refusal that did not quote what was typed would put a person in a
// hand-written TOML file hunting a bracket, and the direction the mistake fails
// in is the dangerous one: a list that did not load is a stage that keeps the
// tool it was supposed to lose.
func TestAMalformedListSaysWhatWentWrongWithIt(t *testing.T) {
	for name, malformed := range map[string]struct{ line, says string }{
		"not a list at all": {`tools_deny = "Edit"`, `expected a list`},
		"never closed":      {`tools_deny = ["Edit"`, `has to close with ]`},
	} {
		t.Run(name, func(t *testing.T) {
			_, err := fsm.ParseStage("id = \"audit\"\nrole = \"auditor\"\n"+malformed.line+"\n", "audit.toml")

			if err == nil {
				t.Fatalf("%s was accepted as a list", name)
			}
			if !strings.Contains(err.Error(), malformed.says) {
				t.Errorf("the refusal does not say what is wrong with it, got %v", err)
			}
			// And it says where, so the fix does not need a second pass over the
			// file to find which line it meant.
			if !strings.Contains(err.Error(), "audit.toml") {
				t.Errorf("the refusal must say where it was written, got %v", err)
			}
		})
	}
}

// TestTheProjectCanNameItsOwnInterpreter covers the setting that decides which
// model `luna chat` talks to.
//
// It sits beside `editor` in the same switch, and an unrecognised key there is
// an error — so the failure this catches is the branch quietly going missing:
// the project would keep a configured interpreter in its file and get "chat
// needs an interpreter, and none is configured" with nothing pointing at why.
func TestTheProjectCanNameItsOwnInterpreter(t *testing.T) {
	cfg := load(t, `interpreter = "claude --model sonnet"`)

	if cfg.Interpreter != "claude --model sonnet" {
		t.Errorf("want the configured interpreter, got %q", cfg.Interpreter)
	}
}
