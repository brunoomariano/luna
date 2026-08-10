package cli

import (
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/brunoomariano/luna/src/internal/fsm"
)

// Config is what a project can set for itself, read from `.luna/config.toml`.
//
// It exists because a person's `$EDITOR` serves their git, not necessarily this
// project: a repository whose contracts are long markdown may want a different
// editor than the one that opens commit messages. Everything here is optional —
// a project with no config file behaves exactly as before.
type Config struct {
	// Editor overrides $EDITOR for `luna gate adjust`. It may carry arguments,
	// like `code --wait`.
	Editor string

	// Profiles are the gate policies this project defines, by name. A project
	// that names none inherits the three shipped ones; naming one that already
	// exists replaces it, which is what makes `turbo` adjustable rather than
	// merely extendable (ADR-0017).
	Profiles map[fsm.Profile]GatePolicy
}

// GatePolicy is the set of gate kinds that stop a task under one profile.
//
// It is a set rather than a list so a duplicate in the config is harmless, and so
// the question the resolver actually asks — does this kind wait — is a lookup
// rather than a scan.
type GatePolicy map[fsm.GateKind]bool

// Waits reports whether a gate of this kind stops the task under this policy.
func (p GatePolicy) Waits(gate fsm.GateKind) bool { return p[gate] }

// knownGateKinds is what a profile may name. A closed list, because a typo in a
// gate kind would otherwise define a profile that silently waits for nothing —
// the failure mode a supervised run can least afford.
var knownGateKinds = map[fsm.GateKind]bool{
	fsm.GateConfirm:        true,
	fsm.GateConfirmWrite:   true,
	fsm.GateReviewArtifact: true,
	fsm.GateLoopCeiling:    true,
}

// ShippedProfiles is the policy each built-in profile carries, expressed the same
// way a configured one is. They are defaults, not special cases (ADR-0026).
func ShippedProfiles() map[fsm.Profile]GatePolicy {
	profiles := map[fsm.Profile]GatePolicy{}
	for _, name := range fsm.ShippedProfiles() {
		policy := GatePolicy{}
		for gate := range knownGateKinds {
			if fsm.ShippedPolicy(name, gate) {
				policy[gate] = true
			}
		}
		profiles[name] = policy
	}
	return profiles
}

// Profile resolves a name to the policy that decides its gates.
//
// A name with no policy is reported rather than defaulted, because the caller has
// to tell the two situations apart: creating a task under an unknown profile is a
// mistake worth refusing, while replaying a task whose profile was since deleted
// is ordinary and must still work.
func (c Config) Profile(name fsm.Profile) (GatePolicy, bool) {
	policy, ok := c.Profiles[name]
	return policy, ok
}

// Waits reports whether a gate stops a task on this profile, which is what the
// lead asks before recording an advance (ADR-0026).
//
// A profile the configuration does not define falls back to the cautious answer
// rather than to nothing. It happens when a profile is deleted while tasks are
// still running under it, and the two failure modes are not symmetric: waiting
// too often stops a task that would have carried on, while waiting too little
// lets an unsupervised run write something nobody approved.
func (c Config) Waits(profile fsm.Profile, gate fsm.GateKind) bool {
	policy, ok := c.Profile(profile)
	if !ok {
		return fsm.ShippedPolicy(fsm.ProfileInteractive, gate)
	}
	return policy.Waits(gate)
}

// ProfileNames lists the profiles this project offers, sorted, for error messages
// that tell someone what they could have typed.
func (c Config) ProfileNames() []string {
	names := make([]string, 0, len(c.Profiles))
	for name := range c.Profiles {
		names = append(names, string(name))
	}
	sort.Strings(names)
	return names
}

// LoadConfig reads the project's configuration.
//
// A missing file is not an error: it is the ordinary case, and returning the
// shipped defaults keeps every caller from having to distinguish "no file" from
// "empty file".
func LoadConfig(path string) (Config, error) {
	content, err := os.ReadFile(path) //nolint:gosec // the path comes from the CLI, not from input
	if os.IsNotExist(err) {
		return Config{Profiles: ShippedProfiles()}, nil
	}
	if err != nil {
		return Config{}, fmt.Errorf("reading %s: %w", path, err)
	}

	return parseConfig(string(content), path)
}

// parseConfig reads the subset of TOML this file needs: `key = value`, string
// arrays, and `[profile.<name>]` sections, plus comments and blank lines.
//
// Still hand-rolled rather than a dependency. The earlier note here said the
// trade would flip the moment the file grew sections and arrays, and it has —
// but the grammar is closed, not open: these are the only two shapes the config
// will hold, and a TOML library would bring datetimes, nested tables and inline
// arrays that nothing here accepts. The engine's single external dependency is a
// line worth keeping; if the config ever takes a shape not listed above, that is
// the point to replace this rather than extend it.
func parseConfig(content, path string) (Config, error) {
	cfg := Config{Profiles: map[fsm.Profile]GatePolicy{}}
	section := ""

	for number, raw := range strings.Split(content, "\n") {
		line := strings.TrimSpace(stripComment(raw))
		if line == "" {
			continue
		}
		where := fmt.Sprintf("%s:%d", path, number+1)

		if name, ok := sectionName(line); ok {
			profile, err := profileSection(name, where)
			if err != nil {
				return Config{}, err
			}
			section = profile
			// An empty section still declares the profile: `[profile.yolo]` with no
			// `waits` is a profile that stops at nothing, which is a thing someone
			// may legitimately want to write.
			cfg.Profiles[fsm.Profile(profile)] = GatePolicy{}
			continue
		}

		key, value, found := strings.Cut(line, "=")
		if !found {
			return Config{}, fmt.Errorf("%s: expected key = value, got %q", where, line)
		}
		key = strings.TrimSpace(key)
		value = strings.TrimSpace(value)

		if err := assign(&cfg, section, key, value, where); err != nil {
			return Config{}, err
		}
	}

	// A file that names no profiles still gets the shipped ones, so setting an
	// editor does not silently cost someone their `--profile nightly`.
	if len(cfg.Profiles) == 0 {
		cfg.Profiles = ShippedProfiles()
	}
	return cfg, nil
}

// assign places one setting, in the root or inside a profile section.
func assign(cfg *Config, section, key, value, where string) error {
	if section == "" {
		return assignRoot(cfg, key, value, where)
	}

	if key != "waits" {
		// An unknown key is an error rather than a warning, for the same reason a
		// misspelled gate kind is: the profile would parse, apply, and wait for
		// nothing, and nobody would learn why until an unattended run wrote
		// something it should have asked about.
		return fmt.Errorf("%s: unknown setting %q in [profile.%s] (expected waits)", where, key, section)
	}

	gates, err := parseStringArray(value, where)
	if err != nil {
		return err
	}

	policy := GatePolicy{}
	for _, gate := range gates {
		kind := fsm.GateKind(gate)
		if !knownGateKinds[kind] {
			return fmt.Errorf("%s: unknown gate kind %q in [profile.%s] (%s)", where, gate, section, gateKindList())
		}
		policy[kind] = true
	}
	cfg.Profiles[fsm.Profile(section)] = policy
	return nil
}

func assignRoot(cfg *Config, key, value, where string) error {
	switch key {
	case "editor":
		cfg.Editor = strings.Trim(value, `"`)
		return nil
	default:
		// An unknown key is an error rather than a warning: a typo in `editor`
		// would otherwise leave the setting silently unapplied, and the person
		// would conclude the feature does not work.
		return fmt.Errorf("%s: unknown setting %q", where, key)
	}
}

// sectionName reports the name inside `[...]`, if the line is a section header.
func sectionName(line string) (string, bool) {
	if !strings.HasPrefix(line, "[") || !strings.HasSuffix(line, "]") {
		return "", false
	}
	return strings.TrimSpace(line[1 : len(line)-1]), true
}

// profileSection validates a section header and returns the profile it names.
//
// Only `[profile.<name>]` exists. Refusing anything else keeps a mistyped header
// from quietly swallowing every setting under it.
func profileSection(name, where string) (string, error) {
	prefix, profile, found := strings.Cut(name, ".")
	if !found || prefix != "profile" || profile == "" {
		return "", fmt.Errorf("%s: unknown section [%s] (expected [profile.<name>])", where, name)
	}
	if strings.Contains(profile, ".") {
		return "", fmt.Errorf("%s: profile names hold no dots, got %q", where, profile)
	}
	return strings.Trim(profile, `"`), nil
}

// parseStringArray reads `["a", "b"]` on a single line.
//
// Single-line only, which is the shape the config's one array takes. A multi-line
// array is refused with a message saying so, rather than parsed halfway and
// silently truncated to the first line.
func parseStringArray(value, where string) ([]string, error) {
	if !strings.HasPrefix(value, "[") {
		return nil, fmt.Errorf(`%s: expected a list like ["confirm"], got %q`, where, value)
	}
	if !strings.HasSuffix(value, "]") {
		return nil, fmt.Errorf("%s: a list has to close on the same line, got %q", where, value)
	}

	inner := strings.TrimSpace(value[1 : len(value)-1])
	if inner == "" {
		return nil, nil
	}

	var items []string
	for _, item := range strings.Split(inner, ",") {
		item = strings.TrimSpace(item)
		if item == "" {
			continue
		}
		if !strings.HasPrefix(item, `"`) || !strings.HasSuffix(item, `"`) || len(item) < 2 {
			return nil, fmt.Errorf("%s: list entries are quoted strings, got %s", where, item)
		}
		items = append(items, item[1:len(item)-1])
	}
	return items, nil
}

// stripComment drops a trailing `#` comment, leaving one inside quotes alone —
// an editor command may legitimately contain a hash.
func stripComment(line string) string {
	quoted := false
	for i, r := range line {
		switch {
		case r == '"':
			quoted = !quoted
		case r == '#' && !quoted:
			return line[:i]
		}
	}
	return line
}

func gateKindList() string {
	kinds := make([]string, 0, len(knownGateKinds))
	for kind := range knownGateKinds {
		kinds = append(kinds, string(kind))
	}
	sort.Strings(kinds)
	return strings.Join(kinds, ", ")
}

// ConfigPath is where a project's configuration lives, next to its store.
func ConfigPath(storePath string) string {
	return filepath.Join(filepath.Dir(storePath), "config.toml")
}
