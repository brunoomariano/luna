package cli

import (
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/brunoomariano/luna/src/internal/fsm"
)

// Config is what a project and the machine are set to, built from the settings
// the daemon holds. It used to be read from `.luna/config.toml`, and nothing reads
// that file now.
//
// It exists because a person's `$EDITOR` serves their git, not necessarily this
// project: a repository whose contracts are long markdown may want a different
// editor than the one that opens commit messages. Everything here is optional —
// a project with no config file behaves exactly as before.
type Config struct {
	// Editor overrides $EDITOR for `luna gate adjust`. It may carry arguments,
	// like `code --wait`.
	Editor string

	// LeadHarness is which official harness the lead asks when it judges a gate.
	// Empty means the house default.
	//
	// It is not the harness that runs a stage: that one is the stage's own, built
	// in the node layer inside the sandbox. This one is the conductor's, and it is
	// the only model Luna itself talks to.
	//
	// It was spelled `interpreter`, after `luna chat` — a command that turned a
	// person's words into a Luna command, removed long ago. The old spelling was
	// kept to avoid breaking config files already written; that argument did not
	// survive the file leaving the repository, and a setting named after a command
	// nobody can run costs every reader a search.
	LeadHarness string

	// TurnBudget bounds how long the node waits on an agent that is not reacting.
	//
	// Ordinary project configuration rather than a property of a profile, which is
	// what was decided when profiles stopped governing gates: this is the
	// watchdog's clock and has nothing to do with who answers
	// a gate. One repository's suite takes twenty minutes and another's takes two,
	// and that is a fact about the repository.
	//
	// Zero means the shipped default, so a project with no config still has a net.
	TurnBudget time.Duration

	// Workstream is the durable memory every task in this project runs inside,
	// unless a task names another.
	//
	// Here rather than per task because it is a fact about the project: one
	// repository's work belongs in one ledger, and asking every `task new` to
	// name it would mean most of them naming it wrong eventually. A task can
	// still choose or open another, and what it chose is recorded on the task.
	//
	// Empty means the shipped default, so a project with no config still writes
	// somewhere named — never to whatever workstream the machine happened to be
	// pointing at, which is the contamination a name exists to prevent.
	Workstream string

	// Bootstrap is what makes a fresh worktree runnable — the step between
	// `git clone` and "the tests run". `make bootstrap`, `pnpm install`,
	// whatever this repository needs.
	//
	// Here rather than in a stage file because it is a fact about the repository
	// and the stages ship with the binary: one project builds with make and the
	// next with pnpm, and neither is the flow's business. Empty means none is
	// needed, which is true of a repository whose tests run from a clean checkout.
	//
	// A failure here is infrastructure, not the work. It blocks with the command
	// and its output rather than as a stage that delivered too little.
	Bootstrap string

	// Profiles are the names this project defines. A project that names none
	// inherits the three shipped ones; naming one that already exists is not an
	// error, because there is nothing left in a profile to conflict.
	//
	// A set rather than a map to settings: a profile decides nothing since
	// retired with them, and what the name is still for is validation — `task new
	// --profile` refuses one nobody defined — and history, since a task records
	// the profile it ran under.
	Profiles map[fsm.Profile]bool
}

// sectionKind is which `[...]` block the parser is inside.

// ShippedProfiles are the names the shipped stock defines, expressed the same way
// a configured one is. They are defaults, not special cases.
func ShippedProfiles() map[fsm.Profile]bool {
	profiles, err := shippedProfiles()
	if err != nil {
		panic(fmt.Sprintf("the embedded profiles do not parse, which is a broken build: %v", err))
	}

	out := make(map[fsm.Profile]bool, len(profiles))
	for name, policy := range profiles {
		out[name] = policy
	}
	return out
}

// Defines reports whether this project names the profile.
//
// A question rather than a lookup, because there is nothing to look up: a profile
// holds only its name. The answer separates two situations the callers
// must tell apart — creating a task under an unknown profile is a mistake worth
// refusing, while replaying a task whose profile was since deleted is ordinary
// and must still work, flagged rather than blocked.
func (c Config) Defines(name fsm.Profile) bool {
	return c.Profiles[name]
}

// Turn is how long the node waits on an agent that is not reacting.
//
// It takes no profile: the budget is the watchdog's clock and stopped being a
// property of a profile when profiles stopped deciding anything. A
// project that sets none gets the shipped default, so there is always a net —
// the direction that matters, because no budget means a task that hangs forever.
//
// Non-positive rather than zero: a budget of zero or less means "call it stuck
// immediately", which is never what anybody meant to write. ParseBudget refuses
// what it cannot read; this is the guard on what it can.
func (c Config) Turn() time.Duration {
	if c.TurnBudget <= 0 {
		return fsm.DefaultBudgets().Turn
	}
	return c.TurnBudget
}

// DefaultWorkstream is where a project's tasks write when nothing names another.
//
// A constant rather than an empty string, because empty means *no memory at all*
// and that is a different thing from "nobody configured it". A machine with no
// config still writes to one named ledger, which is the whole point: an unnamed
// run lands in whatever workstream the machine was last pointing at, and that is
// contamination by omission.
const DefaultWorkstream = "luna"

// Memory is the workstream this project's tasks run inside.
func (c Config) Memory() string {
	if c.Workstream == "" {
		return DefaultWorkstream
	}
	return c.Workstream
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

func assignRoot(cfg *Config, key, value, where string) error {
	switch key {
	case "editor":
		cfg.Editor = strings.Trim(value, `"`)
		return nil
	case "lead_harness":
		cfg.LeadHarness = strings.Trim(value, `"`)
		return nil
	case "interpreter":
		// Refused rather than accepted quietly. A project carrying the old spelling
		// would otherwise fall through to the unknown-key error, which says the key
		// is not recognised and not that it was renamed — and the person then has
		// to find out which name replaced it.
		return fmt.Errorf("%s: `interpreter` is now `lead_harness` — it names the harness "+
			"the lead asks when it judges a gate, and was called after a command that no "+
			"longer exists", where)
	case "bootstrap":
		cfg.Bootstrap = strings.Trim(value, `"`)
		return nil
	case "workstream":
		cfg.Workstream = strings.Trim(value, `"`)
		return nil
	case "turn_budget":
		budget, err := fsm.ParseBudget(strings.Trim(value, `"`))
		if err != nil {
			return fmt.Errorf("%s: %s: %w", where, key, err)
		}
		cfg.TurnBudget = budget
		return nil
	default:
		// An unknown key is an error rather than a warning: a typo in `editor`
		// would otherwise leave the setting silently unapplied, and the person
		// would conclude the feature does not work.
		return fmt.Errorf("%s: unknown setting %q (expected editor, lead_harness, turn_budget, "+
			"bootstrap, workstream)", where, key)
	}
}
