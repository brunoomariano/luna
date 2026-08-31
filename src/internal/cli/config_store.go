package cli

import (
	"fmt"
	"sort"
	"strings"

	"github.com/brunoomariano/luna/src/internal/fsm"
	"github.com/brunoomariano/luna/src/internal/store"
)

// ProfilesKey holds the profile names a project declares, comma-separated.
//
// One key rather than a section, because a profile holds only its name now: the
// file's `[profile.x]` existed to open a section that could carry settings, and
// nothing has carried one since.
const ProfilesKey = "profiles"

// machineKeys are the settings that belong to the person or the machine rather
// than to any project.
//
// `editor` is whose hands are on the keyboard and `lead_harness` is what is
// installed. Neither was ever the project's — they lived in the project's file
// only because that was the one file there was — and setting them once should not
// have to be done per repository.
var machineKeys = map[string]bool{"editor": true, "lead_harness": true}

// ScopeOf says which scope a key belongs to, given the project it was named in.
func ScopeOf(key, project string) string {
	if machineKeys[key] {
		return store.GlobalScope
	}
	return project
}

// ConfigKeys is every key that can be set, in the order `luna config` prints
// them: the machine's first, then the project's.
func ConfigKeys() []string {
	return []string{"editor", "lead_harness", "turn_budget", "workstream", ProfilesKey}
}

// ConfigFrom builds the settings a command runs under from what the daemon holds.
//
// Two layers, project over machine, and the order is the whole rule: a project
// that names an editor means it, and one that does not falls back rather than
// being told there is none.
func ConfigFrom(global, project map[string]string) (Config, error) {
	cfg := Config{Profiles: ShippedProfiles()}
	for _, layer := range []map[string]string{global, project} {
		if err := applySettings(&cfg, layer); err != nil {
			return Config{}, err
		}
	}
	return cfg, nil
}

func applySettings(cfg *Config, layer map[string]string) error {
	// Sorted, so a layer carrying two keys that disagree fails the same way twice
	// rather than by map order.
	keys := make([]string, 0, len(layer))
	for key := range layer {
		keys = append(keys, key)
	}
	sort.Strings(keys)

	for _, key := range keys {
		if key == ProfilesKey {
			cfg.Profiles = parseProfiles(layer[key])
			continue
		}
		if err := assignRoot(cfg, key, layer[key], "the daemon's settings"); err != nil {
			return err
		}
	}
	return nil
}

// parseProfiles reads the comma-separated names back. An empty list keeps the
// shipped set, which is what declaring no profiles has always meant.
func parseProfiles(value string) map[fsm.Profile]bool {
	names := map[fsm.Profile]bool{}
	for _, name := range strings.Split(value, ",") {
		name = strings.TrimSpace(name)
		if name != "" {
			names[fsm.Profile(name)] = true
		}
	}
	if len(names) == 0 {
		return ShippedProfiles()
	}
	return names
}

// SettingsOf is a config taken apart into the settings that would rebuild it,
// split by the scope each key belongs to.
//
// It exists for the one-time import of a project's `.luna/config.toml`: the file
// is parsed by the parser it was always parsed by, and what comes out is written
// through the daemon like any other setting.
func SettingsOf(cfg Config, project string) (global, projectSettings map[string]string) {
	global, projectSettings = map[string]string{}, map[string]string{}
	put := func(key, value string) {
		if value == "" {
			return
		}
		if ScopeOf(key, project) == store.GlobalScope {
			global[key] = value
			return
		}
		projectSettings[key] = value
	}

	put("editor", cfg.Editor)
	put("lead_harness", cfg.LeadHarness)
	put("workstream", cfg.Workstream)
	if cfg.TurnBudget > 0 {
		put("turn_budget", cfg.TurnBudget.String())
	}
	if names := profileNames(cfg.Profiles); names != "" {
		put(ProfilesKey, names)
	}
	return global, projectSettings
}

// profileNames renders a profile set as the value the settings hold, or empty
// when it is the shipped set — writing the default down would freeze it against
// a later build that ships another.
func profileNames(profiles map[fsm.Profile]bool) string {
	if len(profiles) == 0 || sameProfiles(profiles, ShippedProfiles()) {
		return ""
	}
	names := make([]string, 0, len(profiles))
	for name := range profiles {
		names = append(names, string(name))
	}
	sort.Strings(names)
	return strings.Join(names, ",")
}

func sameProfiles(a, b map[fsm.Profile]bool) bool {
	if len(a) != len(b) {
		return false
	}
	for name := range a {
		if !b[name] {
			return false
		}
	}
	return true
}

// CheckConfigKey refuses a key nothing reads, before it is written.
//
// The file refused an unknown key at parse time for a reason that holds here and
// harder: a typo in `editor` written into the database is a setting that looks
// applied, survives every run, and does nothing.
func CheckConfigKey(key string) error {
	for _, known := range ConfigKeys() {
		if key == known {
			return nil
		}
	}
	return fmt.Errorf("%w: unknown setting %q (expected %s)",
		ErrUsage, key, strings.Join(ConfigKeys(), ", "))
}
