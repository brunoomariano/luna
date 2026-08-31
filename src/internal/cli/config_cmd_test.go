package cli

import (
	"errors"
	"strings"
	"testing"

	"github.com/brunoomariano/luna/src/internal/fsm"
	"github.com/brunoomariano/luna/src/internal/store"
)

// TestAKeyLandsInTheScopeItBelongsTo. `editor` is whose hands are on the
// keyboard and `lead_harness` is what is installed; neither was ever the
// project's, and they lived in the project's file only because that was the one
// file there was.
func TestAKeyLandsInTheScopeItBelongsTo(t *testing.T) {
	h := newHarness(t)
	h.env.Store.Project = "app-1"

	h.mustRun(t, "config", "set", "editor", "hx")
	h.mustRun(t, "config", "set", "workstream", "the-project")

	global, err := h.env.Store.Settings(store.GlobalScope)
	if err != nil {
		t.Fatalf("reading the machine's settings: %v", err)
	}
	project, err := h.env.Store.Settings("app-1")
	if err != nil {
		t.Fatalf("reading the project's settings: %v", err)
	}

	if global["editor"] != "hx" {
		t.Errorf("the editor is not the machine's: %v", global)
	}
	if project["workstream"] != "the-project" {
		t.Errorf("the workstream is not the project's: %v", project)
	}
	if _, stray := project["editor"]; stray {
		t.Error("the editor was written per project, so it has to be set again in every repository")
	}
}

// TestAProjectSettingWinsOverTheMachines is the whole point of two scopes: a
// project that names an editor means it, and one that does not falls back rather
// than being told there is none.
func TestAProjectSettingWinsOverTheMachines(t *testing.T) {
	global := map[string]string{"editor": "vim", "lead_harness": "claude"}
	project := map[string]string{"editor": "hx"}

	cfg, err := ConfigFrom(global, project)
	if err != nil {
		t.Fatalf("building the config: %v", err)
	}

	if cfg.Editor != "hx" {
		t.Errorf("the editor is %q, want the project's", cfg.Editor)
	}
	if cfg.LeadHarness != "claude" {
		t.Errorf("the lead harness is %q, want the machine's — a project that says "+
			"nothing should fall back", cfg.LeadHarness)
	}
}

// TestAValueIsParsedBeforeItIsStored.
//
// A turn budget of "fortnight" written into the database is a setting that looks
// applied, survives every run, and fails at the next stage in a place that says
// nothing about where it came from. The file refused it at parse time; so does
// this, using the same parser.
func TestAValueIsParsedBeforeItIsStored(t *testing.T) {
	h := newHarness(t)
	h.env.Store.Project = "app-1"

	if err := h.run(t, "config", "set", "turn_budget", "fortnight"); err == nil {
		t.Fatal("an unparseable duration was accepted")
	}

	project, err := h.env.Store.Settings("app-1")
	if err != nil {
		t.Fatalf("reading the settings: %v", err)
	}
	if _, stored := project["turn_budget"]; stored {
		t.Errorf("the refused value was stored anyway: %v", project)
	}
}

// TestAnUnknownKeyIsRefused rather than kept. A typo in `editor` is a setting
// that looks applied and does nothing, and the person concludes the feature is
// broken.
func TestAnUnknownKeyIsRefused(t *testing.T) {
	h := newHarness(t)
	h.env.Store.Project = "app-1"

	err := h.run(t, "config", "set", "wrokstream", "the-project")

	if !errors.Is(err, ErrUsage) {
		t.Fatalf("an unknown key answered %v, want a usage error", err)
	}
	if !strings.Contains(err.Error(), "wrokstream") {
		t.Errorf("the error does not name the key that was wrong: %v", err)
	}

	// And a key Luna writes is refused with the reason, not as a typo: `bootstrap`
	// is real, and being told it does not exist would send somebody looking for
	// the spelling.
	err = h.run(t, "config", "set", "bootstrap", "make bootstrap")
	if !errors.Is(err, ErrUsage) {
		t.Fatalf("a discovered key answered %v, want a usage error", err)
	}
	if !strings.Contains(err.Error(), "discovered by") {
		t.Errorf("the error does not say where the value comes from: %v", err)
	}
}

// TestUnsettingFallsBackRatherThanEmptying. A project that unsets its editor
// wants the machine's, not none — and the two are different answers.
func TestUnsettingFallsBackRatherThanEmptying(t *testing.T) {
	h := newHarness(t)
	h.env.Store.Project = "app-1"

	h.mustRun(t, "config", "set", "editor", "hx")
	if err := h.env.Store.PutSetting("app-1", "editor", "nano"); err != nil {
		t.Fatalf("giving the project an editor of its own: %v", err)
	}
	h.mustRun(t, "config", "unset", "editor")

	// `unset` writes at the key's own scope, which for the editor is the machine's.
	global, err := h.env.Store.Settings(store.GlobalScope)
	if err != nil {
		t.Fatalf("reading the machine's settings: %v", err)
	}
	if _, present := global["editor"]; present {
		t.Errorf("unsetting left the machine's editor in place: %v", global)
	}
}

// TestTheListingSaysWhereAValueCameFrom. A person surprised by an editor wants
// to know whether this project chose it or the machine did; a merged view answers
// what they are running under and leaves why unanswerable.
func TestTheListingSaysWhereAValueCameFrom(t *testing.T) {
	h := newHarness(t)
	h.env.Store.Project = "app-1"

	h.mustRun(t, "config", "set", "editor", "hx")
	h.mustRun(t, "config", "set", "workstream", "the-project")
	out := h.mustRun(t, "config")

	if !strings.Contains(out, "this machine") {
		t.Errorf("the listing does not say the editor is the machine's:\n%s", out)
	}
	if !strings.Contains(out, "project app-1") {
		t.Errorf("the listing does not say the workstream is this project's:\n%s", out)
	}
	if !strings.Contains(out, "lead_harness") || !strings.Contains(out, "(unset)") {
		t.Errorf("the listing hides a key nobody has set, so nobody learns it exists:\n%s", out)
	}

	// And what `setup` found, once it has found it: a person has to be able to see
	// the command Luna will run in every worktree it opens.
	if err := h.env.Store.PutSetting("app-1", "bootstrap", "make bootstrap"); err != nil {
		t.Fatalf("recording what setup found: %v", err)
	}
	out = h.mustRun(t, "config")
	if !strings.Contains(out, "make bootstrap") || !strings.Contains(out, "discovered by setup") {
		t.Errorf("the listing hides the command that will run in every worktree:\n%s", out)
	}
}

// TestAConfigSurvivesTheRoundTripIntoSettings is the migration path: a project's
// file is parsed by the parser that always parsed it, taken apart into settings,
// and has to come back as the same thing.
func TestAConfigSurvivesTheRoundTripIntoSettings(t *testing.T) {
	before, err := parseConfig(`
editor = "hx"
lead_harness = "codex"
turn_budget = "45m"
workstream = "the-project"

[profile.patient]
[profile.turbo]
`, "the file")
	if err != nil {
		t.Fatalf("parsing the file: %v", err)
	}

	global, project := SettingsOf(before, "app-1")
	after, err := ConfigFrom(global, project)
	if err != nil {
		t.Fatalf("rebuilding from settings: %v", err)
	}

	if after.Editor != before.Editor || after.LeadHarness != before.LeadHarness {
		t.Errorf("the machine's settings did not survive: %+v", after)
	}
	if after.TurnBudget != before.TurnBudget || after.Workstream != before.Workstream {
		t.Errorf("the project's settings did not survive: %+v", after)
	}
	if !sameProfiles(after.Profiles, before.Profiles) {
		t.Errorf("the profiles did not survive: %v, want %v", after.Profiles, before.Profiles)
	}
}

// TestTheShippedProfilesAreNotWrittenDown. Storing the default would freeze this
// build's set against a later one that ships another, and a project that never
// declared a profile would silently start refusing a new name.
func TestTheShippedProfilesAreNotWrittenDown(t *testing.T) {
	_, project := SettingsOf(Config{Profiles: ShippedProfiles()}, "app-1")

	if value, written := project[ProfilesKey]; written {
		t.Errorf("the shipped profiles were written down as %q", value)
	}
}

// TestConfigRefusesWhatItCannotActOn covers the shapes a person gets wrong at
// the terminal, so each answers with what was expected rather than a panic or a
// silent success.
func TestConfigRefusesWhatItCannotActOn(t *testing.T) {
	h := newHarness(t)
	h.env.Store.Project = "app-1"

	for _, args := range [][]string{
		{"config", "wat"},
		{"config", "set", "editor"},
		{"config", "unset"},
		{"config", "unset", "editor", "hx"},
		{"config", "unset", "wrokstream"},
	} {
		if err := h.run(t, args...); !errors.Is(err, ErrUsage) {
			t.Errorf("%v answered %v, want a usage error", args, err)
		}
	}
}

// TestTheListingNamesWhatTheProjectTookOver. A machine setting a project has
// overridden is still set, and a listing that showed only the winner would leave
// somebody wondering where the value they typed went.
func TestTheListingNamesWhatTheProjectTookOver(t *testing.T) {
	h := newHarness(t)
	h.env.Store.Project = "app-1"

	if err := h.env.Store.PutSetting(store.GlobalScope, "editor", "vim"); err != nil {
		t.Fatalf("setting the machine's editor: %v", err)
	}
	if err := h.env.Store.PutSetting("app-1", "editor", "hx"); err != nil {
		t.Fatalf("setting the project's editor: %v", err)
	}

	out := h.mustRun(t, "config")

	if !strings.Contains(out, "which this project overrides") {
		t.Errorf("the listing does not say the machine's editor was taken over:\n%s", out)
	}
	if !strings.Contains(out, "hx") {
		t.Errorf("the listing does not show the value in effect:\n%s", out)
	}
}

// TestAnUnreachableStoreIsNotAnEmptyConfig.
//
// Every read here answers a collection, and a failure that came back as an empty
// one would print a listing saying nothing is configured — which is the bad kind
// of wrong: the person believes their settings are gone and sets them again.
func TestAnUnreachableStoreIsNotAnEmptyConfig(t *testing.T) {
	h := newHarness(t)
	h.env.Store.Project = "app-1"
	if err := h.env.Store.Close(); err != nil {
		t.Fatalf("closing the store: %v", err)
	}

	if err := h.run(t, "config"); err == nil {
		t.Error("an unreachable store printed a listing instead of saying so")
	}
	// And a write reports the failure rather than the line it was about to print:
	// "editor = hx" from a command that stored nothing is the worst answer here.
	if err := h.run(t, "config", "set", "editor", "hx"); err == nil {
		t.Error("an unreachable store reported a setting it did not record")
	}
	if err := h.run(t, "config", "unset", "editor"); err == nil {
		t.Error("an unreachable store reported an unset it did not record")
	}
}

// TestNoProfilesMeansTheShippedOnes. An empty value is what a project that never
// declared a profile carries, and refusing every name there would break `task new
// --profile` for a project that simply said nothing.
func TestNoProfilesMeansTheShippedOnes(t *testing.T) {
	cfg, err := ConfigFrom(nil, map[string]string{ProfilesKey: ""})
	if err != nil {
		t.Fatalf("building the config: %v", err)
	}

	if !sameProfiles(cfg.Profiles, ShippedProfiles()) {
		t.Errorf("an empty list gave %v, want the shipped set", cfg.Profiles)
	}

	// And two sets of the same size with different names are not the same set —
	// which is what decides whether a project's profiles get written down at all.
	renamed := map[fsm.Profile]bool{}
	for name := range ShippedProfiles() {
		renamed[name+"-x"] = true
	}
	if sameProfiles(renamed, ShippedProfiles()) {
		t.Error("two different sets of the same size compared equal")
	}
}
