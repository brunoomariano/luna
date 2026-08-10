package cli

import (
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/brunoomariano/luna/src/internal/fsm"
	"github.com/brunoomariano/luna/src/internal/lead"
)

// TestAProjectCanDefineItsOwnProfile covers what ADR-0017 promised and ADR-0026
// delivered: a profile that is not one of the three.
func TestAProjectCanDefineItsOwnProfile(t *testing.T) {
	cfg := load(t, `
[profile.paranoid]
waits = ["confirm", "confirm-write", "review-artifact", "loop-ceiling"]
`)

	policy, ok := cfg.Profile("paranoid")
	if !ok {
		t.Fatalf("want the configured profile, got %v", cfg.ProfileNames())
	}

	for _, gate := range []fsm.GateKind{
		fsm.GateConfirm, fsm.GateConfirmWrite, fsm.GateReviewArtifact, fsm.GateLoopCeiling,
	} {
		if !policy.Waits(gate) {
			t.Errorf("paranoid was configured to wait for %s", gate)
		}
	}
}

// TestConfiguringOneProfileReplacesTheShippedSet covers the substitution rule.
//
// A config that names profiles defines the whole set. The alternative — merging
// with the shipped three — would leave someone unable to remove `nightly` from a
// repository where an unattended run is not acceptable.
func TestConfiguringOneProfileReplacesTheShippedSet(t *testing.T) {
	cfg := load(t, `
[profile.supervised]
waits = ["confirm-write"]
`)

	if _, ok := cfg.Profile("nightly"); ok {
		t.Error("a config that names profiles replaces the shipped set")
	}
	if _, ok := cfg.Profile("supervised"); !ok {
		t.Errorf("want the configured profile, got %v", cfg.ProfileNames())
	}
}

// TestRedefiningAShippedProfileIsAllowed covers adjusting rather than extending.
func TestRedefiningAShippedProfileIsAllowed(t *testing.T) {
	cfg := load(t, `
[profile.turbo]
waits = ["confirm", "confirm-write"]
`)

	policy, ok := cfg.Profile("turbo")
	if !ok {
		t.Fatal("turbo was redefined, not removed")
	}
	if !policy.Waits(fsm.GateConfirm) {
		t.Error("the redefined turbo waits for a plain confirm; the shipped one does not")
	}
}

// TestAProfileThatWaitsForNothingIsWritable covers the empty section.
//
// `[profile.yolo]` with no `waits` is a legitimate thing to write — it is what
// nightly is — so it must not be mistaken for a section someone forgot to finish.
func TestAProfileThatWaitsForNothingIsWritable(t *testing.T) {
	cfg := load(t, "[profile.yolo]\n")

	policy, ok := cfg.Profile("yolo")
	if !ok {
		t.Fatalf("an empty section still declares the profile, got %v", cfg.ProfileNames())
	}
	if policy.Waits(fsm.GateConfirmWrite) {
		t.Error("a profile with no waits stops at nothing")
	}
}

// TestAMisspelledGateKindIsAnError covers the closed list of kinds.
//
// The failure it prevents is the quiet one: `confirm-writes` would parse, apply,
// and wait for nothing, and nobody would learn why until an unattended run wrote
// something it should have asked about.
func TestAMisspelledGateKindIsAnError(t *testing.T) {
	_, err := LoadConfig(writeConfig(t, `
[profile.careful]
waits = ["confirm-writes"]
`))

	if err == nil {
		t.Fatal("a misspelled gate kind must be reported")
	}
	if !strings.Contains(err.Error(), "confirm-writes") {
		t.Errorf("the error should name what was typed, got %v", err)
	}
	if !strings.Contains(err.Error(), "confirm-write") {
		t.Errorf("the error should name what was expected, got %v", err)
	}
}

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
waits = ["confirm"]
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

// TestShippedProfilesMatchTheEnginesPolicy guards the seam between the two.
//
// The engine owns what the shipped profiles do; the config expresses them as
// policies. If those drift, a project that redefines nothing would silently get
// different gating than one that never wrote a config at all.
func TestShippedProfilesMatchTheEnginesPolicy(t *testing.T) {
	for name, policy := range ShippedProfiles() {
		for gate := range knownGateKinds {
			if policy.Waits(gate) != fsm.ShippedPolicy(name, gate) {
				t.Errorf("%s + %s: the config policy and the engine's disagree", name, gate)
			}
		}
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

// TestConfigSatisfiesTheLeadsPolicy guards the seam the whole design rests on.
//
// The lead asks a GatePolicy; the config is what answers. If the two ever drift
// apart, the decision would stop reaching the log and every replay would fall
// back to recomputing — silently, and looking correct.
func TestConfigSatisfiesTheLeadsPolicy(t *testing.T) {
	var policy lead.GatePolicy = load(t, `
[profile.paranoid]
waits = ["confirm"]
`)

	if !policy.Waits("paranoid", fsm.GateConfirm) {
		t.Error("the configured profile waits at a confirm")
	}
	if policy.Waits("paranoid", fsm.GateConfirmWrite) {
		t.Error("it was not configured to wait at the write")
	}
}

// TestADeletedProfileFallsBackToTheCautiousAnswer covers a task still running
// under a profile someone removed.
//
// The two failure modes are not symmetric: waiting too often stops a task that
// would have carried on, while waiting too little lets an unsupervised run write
// something nobody approved.
func TestADeletedProfileFallsBackToTheCautiousAnswer(t *testing.T) {
	cfg := load(t, "[profile.paranoid]\nwaits = [\"confirm\"]\n")

	for _, gate := range []fsm.GateKind{
		fsm.GateConfirm, fsm.GateConfirmWrite, fsm.GateReviewArtifact, fsm.GateLoopCeiling,
	} {
		if !cfg.Waits("deleted-last-week", gate) {
			t.Errorf("a profile the config no longer defines must not run free (%s)", gate)
		}
	}
}

// TestANestedProfileNameIsRejected covers the dotted-name refusal.
//
// `[profile.a.b]` names no profile this config can hold. Trimming it to "a" or
// "b" would silently define a profile nobody wrote.
func TestANestedProfileNameIsRejected(t *testing.T) {
	_, err := LoadConfig(writeConfig(t, "[profile.team.paranoid]\nwaits = []\n"))

	if err == nil {
		t.Fatal("a dotted profile name must be reported")
	}
	if !strings.Contains(err.Error(), "team.paranoid") {
		t.Errorf("the error should name what was typed, got %v", err)
	}
}

// TestAnEmptyListIsAProfileThatWaitsForNothing covers `waits = []`.
//
// It is how the shipped nightly is written, so it has to parse as a real profile
// rather than as an omission.
func TestAnEmptyListIsAProfileThatWaitsForNothing(t *testing.T) {
	cfg := load(t, "[profile.headless]\nwaits = []\n")

	policy, ok := cfg.Profile("headless")
	if !ok {
		t.Fatalf("want the profile declared, got %v", cfg.ProfileNames())
	}
	if policy.Waits(fsm.GateConfirmWrite) {
		t.Error("an empty list waits for nothing")
	}
}

// TestATrailingCommaInAListIsTolerated covers the skipped empty entry.
//
// The list is hand-parsed, and a trailing comma is the one piece of TOML slack
// worth keeping: it is what someone leaves behind after deleting a gate kind.
func TestATrailingCommaInAListIsTolerated(t *testing.T) {
	cfg := load(t, "[profile.careful]\nwaits = [\"confirm\", ]\n")

	policy, ok := cfg.Profile("careful")
	if !ok {
		t.Fatalf("want the profile, got %v", cfg.ProfileNames())
	}
	if !policy.Waits(fsm.GateConfirm) {
		t.Error("the entry before the trailing comma still counts")
	}
}

// TestAProfileCanTightenItsWatchdog covers the third decision of ADR-0034: the
// budgets live beside the gate policy, in the same section.
func TestAProfileCanTightenItsWatchdog(t *testing.T) {
	cfg := load(t, `
[profile.nightly]
waits = []
idle_budget = "10m"
tool_budget = "45m"
`)

	budgets := cfg.Budgets("nightly")
	if budgets.Idle != 10*time.Minute {
		t.Errorf("want the configured idle budget, got %s", budgets.Idle)
	}
	if budgets.Tool != 45*time.Minute {
		t.Errorf("want the configured tool budget, got %s", budgets.Tool)
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
	cfg := load(t, "[profile.careful]\nwaits = [\"confirm\"]\n")

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
	cfg := load(t, "[profile.paranoid]\nwaits = [\"confirm\"]\n")

	if got := cfg.Budgets("deleted-last-week"); got != fsm.DefaultBudgets() {
		t.Errorf("an undefined profile still gets a budget, got %+v", got)
	}
}

// TestAMalformedBudgetIsRefused covers the fail-loud rule.
//
// Someone who wrote `idle_budget = "30"` believes they tightened the watchdog. A
// silent fallback would leave them believing it.
func TestAMalformedBudgetIsRefused(t *testing.T) {
	_, err := LoadConfig(writeConfig(t, "[profile.p]\nidle_budget = \"30\"\n"))

	if err == nil {
		t.Fatal("a budget that is not a duration must be reported")
	}
	if !strings.Contains(err.Error(), "idle_budget") {
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
	for _, want := range []string{"waits", "idle_budget", "tool_budget"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("the error should list %q, got %v", want, err)
		}
	}
}
