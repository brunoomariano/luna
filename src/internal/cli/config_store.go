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

// ConfigKeys is every key a person may set, in the order `luna config` prints
// them: the machine's first, then the project's.
func ConfigKeys() []string {
	return []string{"editor", "lead_harness", "turn_budget", "workstream", ProfilesKey}
}

// DiscoveredKeys are settings Luna writes and a person does not.
//
// `bootstrap` is the project's own preparation command, and `setup` finds it by
// reading the project — the Makefile, the README, whatever names it. It was a key
// somebody typed, and typing it is what made two sources for one fact: the stage
// reported the command it had found and Luna ran whatever the file said. Shown in
// the listing, because a person has to be able to see what will run in their
// worktrees; refused by `set`, because the answer comes from the repository.
func DiscoveredKeys() []string { return []string{"bootstrap"} }

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

// renamedKeys are the spellings that used to work, and what each became.
//
// `interpreter` named the harness the lead asks when it judges a gate, and was
// called after a command that no longer exists.
var renamedKeys = map[string]string{"interpreter": "lead_harness"}

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
	// Renamed keys answer with the new name rather than falling through to "not
	// recognised" — which is true and sends the person looking for a spelling
	// instead of telling them the one that replaced it.
	if replacement, renamed := renamedKeys[key]; renamed {
		return fmt.Errorf("%w: `%s` is now `%s`", ErrUsage, key, replacement)
	}
	for _, found := range DiscoveredKeys() {
		if key == found {
			return fmt.Errorf("%w: %q is discovered by `setup` from the project itself, not set here",
				ErrUsage, key)
		}
	}
	return fmt.Errorf("%w: unknown setting %q (expected %s)",
		ErrUsage, key, strings.Join(ConfigKeys(), ", "))
}
