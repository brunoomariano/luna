package cli

import (
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/brunoomariano/luna/src/internal/fsm"
	"github.com/brunoomariano/luna/src/internal/herdr"
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
	if !strings.Contains(err.Error(), "wait") || !strings.Contains(err.Error(), "waits") {
		t.Errorf("the error should name what arrived and what was expected, got %v", err)
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
	if _, ok := cfg.Profile("paranoid"); !ok {
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
	cfg := load(t, "[role.gherkin]\nskills = [\"scenarios\", ]\n")

	role, ok := cfg.Roles["gherkin"]
	if !ok {
		t.Fatal("want the role the config defined")
	}
	if len(role.Skills) != 1 || role.Skills[0] != "scenarios" {
		t.Errorf("the entry before the trailing comma still counts, got %q", role.Skills)
	}
}

// TestAProfileCanTightenItsWatchdog covers what a profile still decides: how
// long the node waits on an agent that is not reacting (ADR-0034).
//
// It is all a profile decides now — which gates wait is the stage's declaration
// and the knob's (ADR-0063).
func TestAProfileCanTightenItsWatchdog(t *testing.T) {
	cfg := load(t, `
[profile.nightly]
turn_budget = "45m"
`)

	budgets := cfg.Budgets("nightly")
	if budgets.Turn != 45*time.Minute {
		t.Errorf("want the configured turn budget, got %s", budgets.Turn)
	}

	// The gates in the same section still work: the two settings coexist rather
	// than one shadowing the other.
	if _, ok := cfg.Profile("nightly"); !ok {
		t.Error("the profile is still defined")
	}
}

// TestAProfileWithNoBudgetsGetsTheShippedOnes covers the ordinary case — every
// profile written before this feature existed.
func TestAProfileWithNoBudgetsGetsTheShippedOnes(t *testing.T) {
	cfg := load(t, "[profile.careful]\n")

	if got := cfg.Budgets("careful"); got != fsm.DefaultBudgets() {
		t.Errorf("want the shipped budgets, got %+v", got)
	}
}

// TestADeletedProfileStillHasAWatchdog covers the direction that matters.
//
// A task whose profile was removed must keep its net: falling back to no limit
// would mean deleting a profile silently turns its running tasks into ones that
// hang forever (ADR-0034).
func TestADeletedProfileStillHasAWatchdog(t *testing.T) {
	cfg := load(t, "[profile.paranoid]\n")

	if got := cfg.Budgets("deleted-last-week"); got != fsm.DefaultBudgets() {
		t.Errorf("an undefined profile still gets a budget, got %+v", got)
	}
}

// TestAMalformedBudgetIsRefused covers the fail-loud rule.
//
// Someone who wrote `turn_budget = "30"` believes they tightened the watchdog. A
// silent fallback would leave them believing it.
func TestAMalformedBudgetIsRefused(t *testing.T) {
	_, err := LoadConfig(writeConfig(t, "[profile.p]\nturn_budget = \"30\"\n"))

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

// TestAnUnknownProfileKeyNamesTheAlternatives covers the message someone sees
// after a typo, now that a section accepts three keys.
func TestAnUnknownProfileKeyNamesTheAlternatives(t *testing.T) {
	_, err := LoadConfig(writeConfig(t, "[profile.p]\nidle_budgets = \"30m\"\n"))

	if err == nil {
		t.Fatal("an unknown key must be reported")
	}
	for _, want := range []string{"waits", "turn_budget"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("the error should list %q, got %v", want, err)
		}
	}
}

// TestAProjectCanDefineARole covers what ADR-0040 makes configurable.
func TestAProjectCanDefineARole(t *testing.T) {
	cfg := load(t, `
[role.reviewer]
agent  = "codex"
brief  = "You review. You do not write."
skills = ["code-review", "security"]
`)

	role, ok := cfg.Role("reviewer")
	if !ok {
		t.Fatal("want the configured role")
	}
	if role.Agent != "codex" {
		t.Errorf("want the configured agent, got %q", role.Agent)
	}
	if role.Brief != "You review. You do not write." {
		t.Errorf("want the configured brief, got %q", role.Brief)
	}
	if len(role.Skills) != 2 {
		t.Errorf("want both skills, got %v", role.Skills)
	}
}

// TestNamingOneRoleKeepsTheOthers covers the difference from profiles.
//
// A config that names profiles replaces the whole set, because a project may want
// `nightly` gone. Roles are the opposite: the flow names roles the config never
// mentions, and deleting them would leave a stage with nothing to run.
func TestNamingOneRoleKeepsTheOthers(t *testing.T) {
	cfg := load(t, "[role.reviewer]\nagent = \"codex\"\n")

	if role, _ := cfg.Role("reviewer"); role.Agent != "codex" {
		t.Errorf("the named role is replaced, got %q", role.Agent)
	}
	if _, ok := cfg.Role("implementer"); !ok {
		t.Error("naming one role must not delete the others")
	}
}

// TestEveryRoleTheShippedFlowNamesResolves is the check that keeps the two in
// step.
//
// A stage whose role resolves to nothing stops the task, so a role added to the
// flow without a default is a task that dies on that stage.
func TestEveryRoleTheShippedFlowNamesResolves(t *testing.T) {
	cfg := Config{Roles: ShippedRoles()}

	for _, stage := range fsm.DefaultFlow() {
		if stage.Mechanical() {
			continue
		}
		role, ok := cfg.Role(fsm.RoleName(stage.Role))
		if !ok {
			t.Errorf("stage %q names role %q, which ships with nothing", stage.ID, stage.Role)
			continue
		}
		if role.Agent == "" {
			t.Errorf("role %q ships without an agent to run it", stage.Role)
		}
	}
}

// TestAnUnknownRoleKeyIsRefused covers the strict parsing, for the same reason it
// applies to profiles: a misspelled `agent` would leave the role resolving to
// nothing and the stage stopping for a reason nobody could see.
func TestAnUnknownRoleKeyIsRefused(t *testing.T) {
	_, err := LoadConfig(writeConfig(t, "[role.reviewer]\nagnet = \"codex\"\n"))

	if err == nil {
		t.Fatal("an unknown key inside a role must be reported")
	}
	if !strings.Contains(err.Error(), "agnet") {
		t.Errorf("the error should name what was typed, got %v", err)
	}
	if !strings.Contains(err.Error(), "agent") {
		t.Errorf("the error should name what was expected, got %v", err)
	}
}

// TestAnUnknownSectionKindIsRefused covers the header now that two kinds exist.
func TestAnUnknownSectionKindIsRefused(t *testing.T) {
	_, err := LoadConfig(writeConfig(t, "[roles.reviewer]\nagent = \"codex\"\n"))

	if err == nil {
		t.Fatal("a mistyped section must be reported")
	}
	if !strings.Contains(err.Error(), "role.<name>") {
		t.Errorf("the error should say what a section looks like, got %v", err)
	}
}

// TestRolesAndProfilesCoexist covers a file that declares both.
func TestRolesAndProfilesCoexist(t *testing.T) {
	cfg := load(t, `
editor = "hx"

[profile.paranoid]

[role.reviewer]
agent = "codex"
`)

	if cfg.Editor != "hx" {
		t.Errorf("want the root setting, got %q", cfg.Editor)
	}
	if _, ok := cfg.Profile("paranoid"); !ok {
		t.Error("want the profile")
	}
	if role, _ := cfg.Role("reviewer"); role.Agent != "codex" {
		t.Errorf("want the role, got %q", role.Agent)
	}
}

// TestAMalformedRoleSkillListIsRefused covers the array inside a role section.
func TestAMalformedRoleSkillListIsRefused(t *testing.T) {
	_, err := LoadConfig(writeConfig(t, "[role.reviewer]\nskills = \"code-review\"\n"))

	if err == nil {
		t.Fatal("skills that are not a list must be reported")
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

// TestEveryReviewRoleShipsUnableToWrite is INV-core-7 as a test rather than a
// description.
//
// The invariant names the failure directly: "a role whose restriction exists only
// as text in the prompt". Every role that judges someone else's work has to start
// without the tools to change it.
func TestEveryReviewRoleShipsUnableToWrite(t *testing.T) {
	roles := ShippedRoles()

	for _, name := range []fsm.RoleName{"qa", "reviewer", "hardener", "architect"} {
		role, ok := roles[name]
		if !ok {
			t.Errorf("%q is a review role and must ship", name)
			continue
		}
		if !role.Gated() {
			t.Errorf("%q reviews other people's work and must not be able to change it", name)
			continue
		}
		// Both capabilities: a role that could still create a file has not been
		// stopped from changing the work it is judging.
		if !role.DeniesWriting() {
			t.Errorf("%q must be denied both Edit and Write, got %v", name, role.ToolsDeny)
		}
	}
}

// TestTheRolesThatWriteAreNotGated covers the other side.
//
// Denying the implementer would stop the task rather than protect it — gating is
// for the roles that judge, not for every role.
func TestTheRolesThatWriteAreNotGated(t *testing.T) {
	roles := ShippedRoles()

	for _, name := range []fsm.RoleName{"implementer", "cleaner", "specifier"} {
		if role := roles[name]; role.Gated() {
			t.Errorf("%q produces work and must keep its tools, got %v", name, role.ToolsDeny)
		}
	}
}

// TestEveryGatedRoleShipsOnAHarnessThatCanGateIt guards the seam between the two
// tables.
//
// A role denying tools on an agent Luna cannot gate stops the task. Shipping that
// combination by default would mean every review stage fails on a fresh install.
func TestEveryGatedRoleShipsOnAHarnessThatCanGateIt(t *testing.T) {
	for name, role := range ShippedRoles() {
		if !role.Gated() {
			continue
		}
		if _, ok := herdr.HarnessFor(role.Agent); !ok {
			t.Errorf("role %q denies tools on %q, which Luna cannot gate", name, role.Agent)
		}
	}
}

// TestAProjectCanDenyToolsOnItsOwnRole covers the configured path.
func TestAProjectCanDenyToolsOnItsOwnRole(t *testing.T) {
	cfg := load(t, `
[role.auditor]
agent      = "pi"
tools_deny = ["Edit", "Write"]
`)

	role, ok := cfg.Role("auditor")
	if !ok {
		t.Fatal("want the configured role")
	}
	if !role.DeniesWriting() {
		t.Errorf("want both capabilities denied, got %v", role.ToolsDeny)
	}
}

// TestAnUnknownCapabilityIsRefused covers the direction a typo fails in.
//
// A misspelled capability would leave the role running with the tool it was
// supposed to lose, and nothing saying so — which is the failure INV-core-7 cares
// about most.
func TestAnUnknownCapabilityIsRefused(t *testing.T) {
	_, err := LoadConfig(writeConfig(t, "[role.auditor]\ntools_deny = [\"Edt\"]\n"))

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

// TestTheOldBudgetNamesAreRefusedWithTheirReason covers the rename of ADR-0051.
//
// Neither name behaved as it said: Luna cannot tell a tool in flight from an agent
// thinking, so the idle window was bounding whole turns and the tool one was read
// and discarded. Quietly mapping them onto the new name would carry that wrong
// mental model forward, which is why they are refused and told why.
func TestTheOldBudgetNamesAreRefusedWithTheirReason(t *testing.T) {
	for _, old := range []string{"idle_budget", "tool_budget"} {
		_, err := LoadConfig(writeConfig(t, "[profile.p]\n"+old+" = \"30m\"\n"))
		if err == nil {
			t.Errorf("%s no longer exists and must be refused", old)
			continue
		}
		if !strings.Contains(err.Error(), "turn_budget") {
			t.Errorf("%s: the refusal must name what to use instead, got %v", old, err)
		}
	}
}

// TestTheOldWaitsKeyIsRefusedWithItsReason covers the config a person already
// has on disk.
//
// Ignoring it would be the silent kind of wrong: the file would keep reading like
// supervision and decide nothing, so the person would believe gates were waiting
// for reasons that no longer exist (ADR-0063).
func TestTheOldWaitsKeyIsRefusedWithItsReason(t *testing.T) {
	_, err := LoadConfig(writeConfig(t, "[profile.paranoid]\nwaits = [\"confirm\"]\n"))
	if err == nil {
		t.Fatal("a profile still listing gates was accepted")
	}

	// The message has to say what replaced it, or the person is left with a
	// refusal and no way forward.
	for _, want := range []string{"waits", "declares checks", "luna autonomy"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("the error does not mention %q: %v", want, err)
		}
	}
}
