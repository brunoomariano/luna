package fsm

// RoleName is what a stage calls the kind of worker it needs.
//
// The engine holds the name and nothing else. What the name *means* — which agent
// runs it, what it is told, what it can do — is configuration, resolved outside
// the reducer the same way a profile is (ADR-0040). That is what lets a project
// put a different model behind `reviewer` than behind `implementer` without the
// engine growing a list of agents.
type RoleName string

// Role is what a project decides a role means.
//
// It is a declaration, never an execution. Nothing here starts a process or reads
// a file; the node layer does that, and what comes back arrives inside an action
// (ADR-0024).
type Role struct {
	// Agent is the harness kind that runs this role — one of the 21 herdr knows
	// (ADR-0031). Two roles naming different agents is the cheapest independence
	// available before real tool gating exists.
	Agent string

	// Brief is what the agent is told about being this role. It is instruction,
	// not enforcement: a restriction that lives only here is the violation
	// INV-core-7 names, and closing that gap needs tool denial the harness
	// applies before the agent starts.
	Brief string

	// Skills are the capability bundles this role loads.
	Skills []string
}

// Mechanical reports whether a stage runs without an agent at all.
//
// `setup` is a worktree, `verify` is the pipeline, `commit` is git. Luna already
// runs commands with a real exit code (ADR-0035), so those stages produce their
// artifact and their evidence with no model in the loop — the project's premise
// applied to the stages where it is easiest to forget (ADR-0040).
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
