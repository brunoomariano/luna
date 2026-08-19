package fsm

import (
	"fmt"
	"strings"
)

// RoleName is what a stage calls the kind of worker it needs.
//
// The engine holds the name and nothing else. What the name *means* — which agent
// runs it, what it is told, what it can do — is configuration, resolved outside
// the reducer the same way a profile is. That is what lets a project
// put a different model behind `reviewer` than behind `implementer` without the
// engine growing a list of agents.
type RoleName string

// Role is what a project decides a role means.
//
// It is a declaration, never an execution. Nothing here starts a process or reads
// a file; the node layer does that, and what comes back arrives inside an action
// .
type Role struct {
	// Agent is the harness kind that runs this role, travelling to the node layer
	// as agent.Call.Kind. Two roles naming different agents is the cheapest
	// independence available before real tool gating exists.
	Agent string

	// Brief is what the agent is told about being this role. It is instruction,
	// not enforcement: a restriction that lives only here is the violation INV-4
	// names, and closing that gap needs tool denial the harness applies before the
	// agent starts.
	Brief string

	// Skills are the capability bundles this role loads.
	Skills []string

	// ToolsDeny names capabilities this role must not have — `Edit`, `Write`.
	//
	// It names what the role cannot do, never how a harness spells it: claude says
	// `Edit`, pi says `edit`, codex takes no names at all and denies writing with
	// a sandbox mode. Keeping the vocabulary out of here is what lets a project
	// move `reviewer` from one agent to another without rewriting the role, and
	// what stops a role from silently ceasing to deny anything when its agent
	// changes.
	ToolsDeny []Capability
}

// Capability is something a role may be denied.
//
// A closed set rather than free strings: a typo in a denial fails open — the role
// runs with the tool it was supposed to lose, and nothing says so. That is the
// direction the containment INV-4 requires cares about most.
type Capability string

const (
	// CapEdit is changing a file that already exists.
	CapEdit Capability = "Edit"

	// CapWrite is creating one.
	CapWrite Capability = "Write"
)

// KnownCapabilities are the ones a role may name.
func KnownCapabilities() []Capability { return []Capability{CapEdit, CapWrite} }

// CapBash is naming a shell, which Luna knows about and cannot deny.
//
// It is not in KnownCapabilities on purpose, and the omission is worth stating
// rather than leaving as an accident of the list. Denying a shell would make a
// reviewer useless — it could not run the tests it is reviewing — and denying
// `Edit` and `Write` while leaving it open does not prevent writing, only make it
// inconvenient. Real confinement is the sandbox INV-4 requires.
//
// The reason it is named here at all: `tools_deny = ["Bash"]` is a reasonable
// thing for someone to write, and the refusal should say why rather than
// listing it among values that were never heard of. Containment over what a
// process may touch is a sandbox's job, which Luna delegates.
const CapBash Capability = "Bash"

// ParseCapability turns a configured name into a capability, refusing what it
// does not know.
func ParseCapability(name string) (Capability, error) {
	for _, known := range KnownCapabilities() {
		if Capability(name) == known {
			return known, nil
		}
	}
	// A shell is refused with its own reason. Someone writing `tools_deny =
	// ["Bash"]` is asking for something coherent, and telling them it is a name
	// Luna never heard of would be answering a different question.
	if Capability(name) == CapBash {
		return "", fmt.Errorf("%q cannot be denied: a role with no shell cannot run the tests "+
			"it is reviewing, and denying Edit and Write with a shell open does not stop "+
			"writing (see INV-4). Confining what a process may touch belongs to a sandbox",
			name)
	}
	return "", fmt.Errorf("unknown capability %q (%s)", name, capabilityList())
}

// Gated reports whether this role must be started with something denied.
func (r Role) Gated() bool { return len(r.ToolsDeny) > 0 }

// DeniesWriting reports whether the role is barred from changing the worktree.
//
// It is the question the coarse harnesses can actually answer: codex denies
// writing wholesale with a sandbox mode and takes no tool names, so a role that
// denies both Edit and Write maps onto it exactly, and one that denies only Edit
// does not.
func (r Role) DeniesWriting() bool {
	denied := map[Capability]bool{}
	for _, capability := range r.ToolsDeny {
		denied[capability] = true
	}
	return denied[CapEdit] && denied[CapWrite]
}

func capabilityList() string {
	names := make([]string, 0, len(KnownCapabilities()))
	for _, capability := range KnownCapabilities() {
		names = append(names, string(capability))
	}
	return strings.Join(names, ", ")
}

// Mechanical reports whether a stage runs without an agent at all.
//
// `setup` is a worktree, `verify` is the pipeline, `commit` is git. Luna already
// runs commands with a real exit code, so those stages produce their
// artifact and their evidence with no model in the loop — the project's premise
// applied to the stages where it is easiest to forget.
func (s Stage) Mechanical() bool { return s.Role == "" }

// NeedsRole reports a stage that produces something only judgement can produce and
// names no role to produce it.
//
// It exists because the mechanical path is silent by nature: a stage that should
// have had a role runs, delivers nothing, and looks like it worked. The static
// check turns that into a failure at build time rather than a mystery at run time.
func (s Stage) NeedsRole() bool {
	if !s.Mechanical() {
		return false
	}
	for _, artifact := range append(append([]Artifact{}, s.Produces...), s.ProducesForHuman...) {
		if judgement[artifact] {
			return true
		}
	}
	return false
}

// judgement names the artifacts no command can produce.
//
// A closed list rather than a heuristic: guessing from the artifact's name would
// make adding one a silent decision about whether it needs an agent, and this is
// the check that catches exactly that.
var judgement = map[Artifact]bool{
	"repos":       true,
	"briefing":    true,
	"root_cause":  true,
	"min_case":    true,
	"scenarios":   true,
	"approach":    true,
	"contract":    true,
	"code":        true,
	"dod_checked": true,
}
