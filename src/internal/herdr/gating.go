package herdr

import (
	"fmt"
	"sort"
	"strings"

	"github.com/brunoomariano/luna/src/internal/fsm"
)

// Harness is how one agent kind lets Luna take a capability away.
//
// Four official harnesses, four mechanisms, and no two vocabularies alike
// (ADR-0042). What a role declares is the capability; translating it into a flag
// is this table's job, which is what lets a project move `reviewer` from one
// agent to another without rewriting the role.
type Harness struct {
	// Kind is the agent as herdr names it (ADR-0031).
	Kind string

	// Deny turns denied capabilities into arguments for this harness. A nil
	// result with no error means the harness expresses the denial somewhere other
	// than argv — opencode writes it into an agent file.
	Deny func(denied []fsm.Capability) ([]string, error)

	// Precise reports whether the harness can deny one capability without denying
	// the rest. codex cannot: `-s read-only` stops all writing and takes no tool
	// names, which is exactly right for a reviewer and cannot express "no Edit,
	// but Bash is fine".
	Precise bool
}

// harnesses is the closed table of what Luna knows how to gate.
//
// Closed on purpose: guessing that an agent supports denial and being wrong fails
// open — a reviewer that can edit, with nothing in the log saying the denial did
// not take. An agent absent from this table stops the stage instead.
//
// The order is the house preference: claude, pi, codex, opencode.
var harnesses = []Harness{
	{
		Kind:    "claude",
		Precise: true,
		Deny: func(denied []fsm.Capability) ([]string, error) {
			// Space-separated, capitalised: `--disallowed-tools Edit Write`.
			args := make([]string, 0, len(denied)+1)
			args = append(args, "--disallowed-tools")
			for _, capability := range sorted(denied) {
				args = append(args, string(capability))
			}
			return args, nil
		},
	},
	{
		Kind:    "pi",
		Precise: true,
		Deny: func(denied []fsm.Capability) ([]string, error) {
			// Comma-separated and lower case: `--exclude-tools edit,write`.
			names := make([]string, 0, len(denied))
			for _, capability := range sorted(denied) {
				names = append(names, strings.ToLower(string(capability)))
			}
			return []string{"--exclude-tools", strings.Join(names, ",")}, nil
		},
	},
	{
		Kind: "codex",
		// Coarse: the sandbox denies writing wholesale rather than by name.
		Precise: false,
		Deny: func(denied []fsm.Capability) ([]string, error) {
			role := fsm.Role{ToolsDeny: denied}
			if !role.DeniesWriting() {
				return nil, fmt.Errorf(
					"codex denies writing wholesale and cannot deny %s alone (try claude or pi)",
					list(denied),
				)
			}
			return []string{"-s", "read-only"}, nil
		},
	},
	{
		Kind: "opencode",
		// Not precise, and not gateable from the command line at all. opencode
		// denies through `permission` in its own config file, and Luna writes no
		// such file — so a role gated here would start fully capable.
		//
		// It refuses rather than returning no arguments. That is the whole reason
		// the table is closed (ADR-0042): a harness that cannot deny must say so,
		// because the alternative is an ungated reviewer with nothing in the log
		// saying the denial did not take.
		//
		// Measured against opencode 1.17.7, the config route is worse than absent:
		// an invalid *value* exits 1 with a clear error, while a mistyped *key*
		// exits 0 in silence and the whole permission block disappears. Wiring
		// this up needs a check that the denial took effect, not just that a file
		// was written.
		Deny: func(denied []fsm.Capability) ([]string, error) {
			return nil, fmt.Errorf(
				"opencode denies %s through its config file, which Luna does not write yet "+
					"(try claude or pi, or codex for a read-only role)",
				list(denied),
			)
		},
	},
}

// HarnessFor returns what Luna knows about gating this agent kind.
func HarnessFor(kind string) (Harness, bool) {
	for _, harness := range harnesses {
		if harness.Kind == kind {
			return harness, true
		}
	}
	return Harness{}, false
}

// SupportedHarnesses names the agents Luna can gate, in preference order.
func SupportedHarnesses() []string {
	kinds := make([]string, 0, len(harnesses))
	for _, harness := range harnesses {
		kinds = append(kinds, harness.Kind)
	}
	return kinds
}

// gateArgs is what to pass the harness so the role starts without what it must
// not have.
//
// A role that denies nothing needs no arguments and no harness support — most
// roles are ungated, and requiring a table entry for them would restrict the
// whole flow to four agents for no reason.
//
// A role that does deny something and names an agent Luna cannot gate stops the
// stage. The message names the harness and the alternatives, because this refusal
// will read as a bug the first time someone meets it (ADR-0041).
func gateArgs(role fsm.Role) ([]string, error) {
	if !role.Gated() {
		return nil, nil
	}

	harness, ok := HarnessFor(role.Agent)
	if !ok {
		return nil, fmt.Errorf(
			"role denies %s but Luna cannot gate %q — supported: %s",
			list(role.ToolsDeny), role.Agent, strings.Join(SupportedHarnesses(), ", "),
		)
	}
	return harness.Deny(role.ToolsDeny)
}

// sorted returns the capabilities in a stable order, so the same role produces
// the same command line every run — a flag order that varies makes two identical
// runs look different in a log.
func sorted(denied []fsm.Capability) []fsm.Capability {
	stable := append([]fsm.Capability{}, denied...)
	sort.Slice(stable, func(i, j int) bool { return stable[i] < stable[j] })
	return stable
}

func list(denied []fsm.Capability) string {
	names := make([]string, 0, len(denied))
	for _, capability := range sorted(denied) {
		names = append(names, string(capability))
	}
	return strings.Join(names, ", ")
}
