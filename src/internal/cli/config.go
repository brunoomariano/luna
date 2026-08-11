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

	// Interpreter is which official harness `luna chat` asks to understand what a
	// person said. Empty means the house default (ADR-0044).
	Interpreter string

	// Profiles are the gate policies this project defines, by name. A project
	// that names none inherits the three shipped ones; naming one that already
	// exists replaces it, which is what makes `turbo` adjustable rather than
	// merely extendable (ADR-0017).
	Profiles map[fsm.Profile]Policy

	// Roles are what each role name resolves to: the agent that runs it, what it
	// is told, and the skills it loads. A project that names none inherits the
	// shipped set; naming one replaces just that one, because a flow names roles
	// the config never mentions (ADR-0040).
	Roles map[fsm.RoleName]fsm.Role
}

// sectionKind is which `[...]` block the parser is inside.
type sectionKind int

const (
	sectionNone sectionKind = iota
	sectionProfile
	sectionRole
)

// sectionRef is the section currently open, and what it names.
type sectionRef struct {
	kind sectionKind
	name string
}

// Policy is everything one profile decides: which gates stop the task, and how
// long the watchdog waits before calling it stuck.
//
// The two live together because they answer the same question from opposite
// ends. A profile that waits at no gate has nobody watching, which makes the
// watchdog its only net — so the profile that most needs a short budget is
// exactly the one that already declares how supervised the run is (ADR-0034).
type Policy struct {
	// Gates is a set rather than a list so a duplicate in the config is harmless,
	// and so the question the resolver asks — does this kind wait — is a lookup.
	Gates map[fsm.GateKind]bool

	// Budgets bound how long the node waits on an agent that is not reacting.
	Budgets fsm.Budgets
}

// Waits reports whether a gate of this kind stops the task under this policy.
func (p Policy) Waits(gate fsm.GateKind) bool { return p.Gates[gate] }

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
func ShippedProfiles() map[fsm.Profile]Policy {
	profiles := map[fsm.Profile]Policy{}
	for _, name := range fsm.ShippedProfiles() {
		gates := map[fsm.GateKind]bool{}
		for gate := range knownGateKinds {
			if fsm.ShippedPolicy(name, gate) {
				gates[gate] = true
			}
		}
		profiles[name] = Policy{Gates: gates, Budgets: fsm.DefaultBudgets()}
	}
	return profiles
}

// Profile resolves a name to the policy that decides its gates.
//
// A name with no policy is reported rather than defaulted, because the caller has
// to tell the two situations apart: creating a task under an unknown profile is a
// mistake worth refusing, while replaying a task whose profile was since deleted
// is ordinary and must still work.
func (c Config) Profile(name fsm.Profile) (Policy, bool) {
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

// Budgets reports how long the watchdog waits under this profile.
//
// A profile the configuration no longer defines falls back to the shipped
// defaults rather than to no limit at all. The direction matters: a task whose
// profile was deleted must still have a net, or removing a profile would silently
// turn its running tasks into ones that hang forever (ADR-0034).
func (c Config) Budgets(profile fsm.Profile) fsm.Budgets {
	policy, ok := c.Profile(profile)
	if !ok {
		return fsm.DefaultBudgets()
	}
	return policy.Budgets.Resolve()
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
		return Config{Profiles: ShippedProfiles(), Roles: ShippedRoles()}, nil
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
	cfg := Config{
		Profiles: map[fsm.Profile]Policy{},
		Roles:    map[fsm.RoleName]fsm.Role{},
	}
	var section sectionRef

	for number, raw := range strings.Split(content, "\n") {
		line := strings.TrimSpace(stripComment(raw))
		if line == "" {
			continue
		}
		where := fmt.Sprintf("%s:%d", path, number+1)

		if header, ok := sectionName(line); ok {
			parsed, err := openSection(&cfg, header, where)
			if err != nil {
				return Config{}, err
			}
			section = parsed
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
	// editor does not silently cost someone their `--profile nightly`. Roles work
	// the same way: declaring one role must not delete the other eleven, because a
	// flow names roles the config never mentions.
	if len(cfg.Profiles) == 0 {
		cfg.Profiles = ShippedProfiles()
	}
	cfg.Roles = withShippedRoles(cfg.Roles)
	return cfg, nil
}

// openSection declares what a `[...]` header opens.
//
// An empty section still declares the thing: `[profile.yolo]` with no `waits` is
// a profile that stops at nothing, and `[role.scout]` with no agent is a role
// someone is about to fill in. Both are legitimate to write, and refusing them
// would make the file order-dependent.
func openSection(cfg *Config, header, where string) (sectionRef, error) {
	parsed, err := parseSection(header, where)
	if err != nil {
		return sectionRef{}, err
	}

	// Every kind is named rather than relying on a default: when a third section
	// is added, this is the place that has to decide about it instead of silently
	// declaring nothing.
	switch parsed.kind {
	case sectionNone:
		return sectionRef{}, fmt.Errorf("%s: section [%s] names nothing", where, header)
	case sectionProfile:
		cfg.Profiles[fsm.Profile(parsed.name)] = Policy{
			Gates:   map[fsm.GateKind]bool{},
			Budgets: fsm.DefaultBudgets(),
		}
	case sectionRole:
		cfg.Roles[fsm.RoleName(parsed.name)] = fsm.Role{}
	}
	return parsed, nil
}

// assign places one setting, in the root or inside a section.
func assign(cfg *Config, section sectionRef, key, value, where string) error {
	switch section.kind {
	case sectionProfile:
		return assignProfile(cfg, section.name, key, value, where)
	case sectionRole:
		return assignRole(cfg, section.name, key, value, where)
	default:
		return assignRoot(cfg, key, value, where)
	}
}

// assignRole places one setting inside a `[role.<name>]` section.
func assignRole(cfg *Config, name, key, value, where string) error {
	role := cfg.Roles[fsm.RoleName(name)]

	switch key {
	case "agent":
		role.Agent = strings.Trim(value, `"`)
	case "brief":
		role.Brief = strings.Trim(value, `"`)
	case "skills":
		skills, err := parseStringArray(value, where)
		if err != nil {
			return err
		}
		role.Skills = skills
	case "tools_deny":
		denied, err := parseCapabilities(value, where, name)
		if err != nil {
			return err
		}
		role.ToolsDeny = denied
	default:
		// An unknown key is an error rather than a warning, for the same reason it
		// is in a profile: a misspelled `agent` would leave the role resolving to
		// nothing and the stage running with whatever the fallback is.
		return fmt.Errorf("%s: unknown setting %q in [role.%s] (expected agent, brief, skills, tools_deny)",
			where, key, name)
	}

	cfg.Roles[fsm.RoleName(name)] = role
	return nil
}

// parseCapabilities reads `tools_deny`, refusing a name Luna does not know.
//
// A typo here fails open — the role runs with the tool it was supposed to lose,
// and nothing says so. That is the direction INV-core-7 cares about most, which
// is why an unknown capability stops the load rather than being skipped.
func parseCapabilities(value, where, role string) ([]fsm.Capability, error) {
	names, err := parseStringArray(value, where)
	if err != nil {
		return nil, err
	}

	denied := make([]fsm.Capability, 0, len(names))
	for _, name := range names {
		capability, err := fsm.ParseCapability(name)
		if err != nil {
			return nil, fmt.Errorf("%s: [role.%s]: %w", where, role, err)
		}
		denied = append(denied, capability)
	}
	return denied, nil
}

// assignProfile places one setting inside a `[profile.<name>]` section.
func assignProfile(cfg *Config, section, key, value, where string) error {
	policy := cfg.Profiles[fsm.Profile(section)]

	switch key {
	case "waits":
		gates, err := parseGates(value, section, where)
		if err != nil {
			return err
		}
		policy.Gates = gates
	case "idle_budget", "tool_budget":
		budget, err := fsm.ParseBudget(strings.Trim(value, `"`))
		if err != nil {
			return fmt.Errorf("%s: %s in [profile.%s]: %w", where, key, section, err)
		}
		if key == "idle_budget" {
			policy.Budgets.Idle = budget
		} else {
			policy.Budgets.Tool = budget
		}
	default:
		// An unknown key is an error rather than a warning, for the same reason a
		// misspelled gate kind is: the profile would parse, apply, and wait for
		// nothing, and nobody would learn why until an unattended run wrote
		// something it should have asked about.
		return fmt.Errorf("%s: unknown setting %q in [profile.%s] (expected waits, idle_budget, tool_budget)",
			where, key, section)
	}

	cfg.Profiles[fsm.Profile(section)] = policy
	return nil
}

// parseGates reads the `waits` list and refuses a gate kind nobody declared.
func parseGates(value, section, where string) (map[fsm.GateKind]bool, error) {
	names, err := parseStringArray(value, where)
	if err != nil {
		return nil, err
	}

	gates := map[fsm.GateKind]bool{}
	for _, name := range names {
		kind := fsm.GateKind(name)
		if !knownGateKinds[kind] {
			return nil, fmt.Errorf("%s: unknown gate kind %q in [profile.%s] (%s)", where, name, section, gateKindList())
		}
		gates[kind] = true
	}
	return gates, nil
}

func assignRoot(cfg *Config, key, value, where string) error {
	switch key {
	case "editor":
		cfg.Editor = strings.Trim(value, `"`)
		return nil
	case "interpreter":
		cfg.Interpreter = strings.Trim(value, `"`)
		return nil
	default:
		// An unknown key is an error rather than a warning: a typo in `editor`
		// would otherwise leave the setting silently unapplied, and the person
		// would conclude the feature does not work.
		return fmt.Errorf("%s: unknown setting %q (expected editor, interpreter)", where, key)
	}
}

// sectionName reports the name inside `[...]`, if the line is a section header.
func sectionName(line string) (string, bool) {
	if !strings.HasPrefix(line, "[") || !strings.HasSuffix(line, "]") {
		return "", false
	}
	return strings.TrimSpace(line[1 : len(line)-1]), true
}

// parseSection reads a section header into the kind it opens and the thing it
// names.
//
// Two kinds exist and an unknown one is refused: a mistyped header would
// otherwise swallow every setting under it, and the file would parse into
// something nobody wrote.
func parseSection(header, where string) (sectionRef, error) {
	kind, name, found := strings.Cut(header, ".")
	if !found || name == "" {
		return sectionRef{}, fmt.Errorf("%s: unknown section [%s] (expected [profile.<name>] or [role.<name>])", where, header)
	}
	if strings.Contains(name, ".") {
		return sectionRef{}, fmt.Errorf("%s: names hold no dots, got %q", where, name)
	}

	name = strings.Trim(name, `"`)
	switch kind {
	case "profile":
		return sectionRef{kind: sectionProfile, name: name}, nil
	case "role":
		return sectionRef{kind: sectionRole, name: name}, nil
	default:
		return sectionRef{}, fmt.Errorf("%s: unknown section [%s] (expected [profile.<name>] or [role.<name>])", where, header)
	}
}

// ShippedRoles is what each role in the default flow resolves to before a project
// says otherwise.
//
// Every role names the same agent kind today, which is honest: the independence
// that matters is the one INV-core-7 asks for — the reviewer not HAVING Edit —
// and that needs tool denial rather than a different vendor. Naming different
// agents here is available to a project and is not pretended to be a substitute.
func ShippedRoles() map[fsm.RoleName]fsm.Role {
	const agent = "claude"

	// Whoever writes does not review, and the reviewer cannot write: both
	// capabilities go, because a role that could still create a file has not been
	// stopped from changing the work it is judging.
	noWriting := []fsm.Capability{fsm.CapEdit, fsm.CapWrite}

	return map[fsm.RoleName]fsm.Role{
		"scout":        {Agent: agent, Brief: "You find which repositories the task touches. You do not change them."},
		"analyst":      {Agent: agent, Brief: "You turn a request into a briefing the next stage can act on."},
		"investigator": {Agent: agent, Brief: "You find the root cause and the smallest case that shows it."},
		"gherkin":      {Agent: agent, Brief: "You write the scenarios and the approach. You do not implement them."},
		"specifier":    {Agent: agent, Brief: "You write the contract: what is required and what is produced."},
		"implementer":  {Agent: agent, Brief: "You make the scenarios pass. You do not review your own work."},
		"cleaner":      {Agent: agent, Brief: "You improve the code without changing what it does."},
		"verifier":     {Agent: agent, Brief: "You check the delivery against the scenarios it promised."},
		// The four review roles start without the tools they must not use. The
		// brief says the same thing, and the brief is not what enforces it — a
		// restriction that lives only in the prompt is the violation INV-core-7
		// names (ADR-0041).
		"qa":        {Agent: agent, Brief: "You look for what the tests do not cover. You report; you do not fix.", ToolsDeny: noWriting},
		"reviewer":  {Agent: agent, Brief: "You review. You report findings; you do not edit.", ToolsDeny: noWriting},
		"hardener":  {Agent: agent, Brief: "You look for what breaks under load, attack, or absence.", ToolsDeny: noWriting},
		"architect": {Agent: agent, Brief: "You judge whether the shape still holds. You report; you do not edit.", ToolsDeny: noWriting},
	}
}

// withShippedRoles fills in the roles a project did not name.
//
// Naming one role must not delete the other eleven: a flow names roles the config
// never mentions, and a stage whose role vanished would have nothing to run.
func withShippedRoles(configured map[fsm.RoleName]fsm.Role) map[fsm.RoleName]fsm.Role {
	roles := ShippedRoles()
	for name, role := range configured {
		roles[name] = role
	}
	return roles
}

// Role resolves a name to what runs it.
//
// A name with no role is reported rather than defaulted: a stage whose role does
// not resolve must stop loudly, because the alternative is running it with some
// fallback agent and calling the result the reviewer's opinion.
func (c Config) Role(name fsm.RoleName) (fsm.Role, bool) {
	role, ok := c.Roles[name]
	return role, ok
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
