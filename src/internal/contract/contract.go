// Package contract is what a phase owes and how each debt is proven.
//
// A contract arrives on stdin, written by whoever conducts the work. Luna never
// holds one: a contract on disk inside a checkout is a file somebody has to keep
// in step with the flow that produced it, and the flow does not live here any
// more.
//
// Nothing in this package executes. Running a verifier belongs to internal/verify,
// and the verdict travels back as evidence.
package contract

import (
	"fmt"
	"sort"
	"strings"
)

// Scope is how much a passing check establishes.
//
// It is the honesty knob of the whole design: a check that ran the whole gate and
// one that ran a single file both exit zero, and only the scope tells them apart.
// There is no upgrade path — see Satisfies.
type Scope string

const (
	// ScopeFull means the project's own gate ran, whole.
	ScopeFull Scope = "full"

	// ScopeTargeted means only what the change touched was run.
	ScopeTargeted Scope = "targeted"

	// ScopeExistence means the artifact is there and nothing more is claimed. It
	// is the honest floor for prose — a briefing, a plan, a report.
	ScopeExistence Scope = "existence"

	// ScopeHuman means a person looked and said so.
	ScopeHuman Scope = "human"
)

// rank orders the scopes by how much they claim. Unknown scopes have no rank and
// are refused rather than ordered, so a typo cannot sort itself above `full`.
var rank = map[Scope]int{
	ScopeExistence: 1,
	ScopeHuman:     2,
	ScopeTargeted:  3,
	ScopeFull:      4,
}

// Valid reports whether the scope is one Luna knows.
func (s Scope) Valid() bool {
	_, known := rank[s]
	return known
}

// Satisfies reports whether evidence at this scope answers a demand for `want`.
//
// Refused from both sides when either scope is unknown: an unrecognised demand
// cannot be met by anything, and unrecognised evidence meets nothing. Guessing in
// either direction is how `existence` would launder itself into `full`.
func (s Scope) Satisfies(want Scope) bool {
	have, ok := rank[s]
	if !ok {
		return false
	}
	need, ok := rank[want]
	if !ok {
		return false
	}
	return have >= need
}

// Verifier declares how one artifact is proven. It is a declaration, never an
// execution.
type Verifier interface {
	// Proves is the scope a successful run establishes.
	Proves() Scope

	// Describe names the check for a person, in a few words.
	Describe() string

	isVerifier()
}

// Command proves an artifact by running a real tool and reading its exit code.
type Command struct {
	// Run is the command line, executed over what the phase delivered.
	Run string

	// Scope is what a zero exit proves.
	Scope Scope
}

// Proves reports the scope a passing run establishes, defaulting to targeted.
//
// The cautious direction: an unstated scope read as `full` would launder a
// targeted run, while under-claiming only costs a phase that has to prove more.
func (c Command) Proves() Scope {
	if c.Scope == "" {
		return ScopeTargeted
	}
	return c.Scope
}

// Describe names the command for a person.
func (c Command) Describe() string { return c.Run }
func (Command) isVerifier()        {}

// Existence proves that an artifact was delivered, and claims nothing else.
type Existence struct {
	// Path is a directory inside the repository the artifact must appear under.
	//
	// Declared, the delivery is checked against the commit with git, so the answer
	// comes from git rather than from the agent. Undeclared, nothing is checked
	// and the agent's word is the whole record.
	//
	// A directory rather than a filename: the agent names the file, which keeps a
	// contract from having to predict a name it cannot know.
	Path string
}

// Proves reports what an existence check proves, which is that the artifact is
// there. A declared path does not raise it — a file in the right directory is
// still just a file.
func (Existence) Proves() Scope { return ScopeExistence }

// Describe names the check for a person.
func (e Existence) Describe() string {
	if e.Path != "" {
		return "delivered under " + e.Path
	}
	return "delivered"
}

func (Existence) isVerifier() {}

// Contract is one phase's obligations.
type Contract struct {
	// Phase names the phase this contract belongs to. It is carried into the
	// ledger so a record says what was being proven.
	Phase string

	// Requires names artifacts this phase was handed.
	//
	// Read by nothing, including `lint` — which this comment claimed for the whole
	// redesign, as did INV-3. It is documentation a person reads. Checking it would
	// mean deciding from the ledger whether a phase may start, and that is flow
	// control; see INV-3 for why it stays unenforced rather than being built.
	Requires []string

	// Produces maps each owed artifact to how it is proven. An artifact with no
	// verifier is refused by Lint rather than defaulted: a contract that does not
	// say how something is proven is the ceremony this project exists against.
	Produces map[string]Verifier

	// ForHuman names artifacts written for a person to read. They are checked on
	// the way out like anything else, and no later phase will ask for them.
	ForHuman map[string]Verifier

	// Loop, when set, is the convergence floor for a phase that repeats.
	Loop *Loop
}

// Loop is the mechanical floor a repeating phase moves within.
//
// The verdict on a round belongs to the model — no exit code tells "this round
// fixed something" from "this round traded one failure for another". What the
// model may not do is leave: ConvergesOn names what has to be passing, and a
// caller that reports convergence without it is refused.
type Loop struct {
	// ConvergesOn names the artifacts that must be passing before the loop may be
	// declared converged.
	ConvergesOn []string

	// MaxRounds is the ceiling on rounds. Zero means no ceiling, which is a
	// choice a contract has to make deliberately.
	MaxRounds int

	// NoProgress is how many rounds without progress end the loop.
	NoProgress int

	// Oscillation is how many oscillating rounds end the loop. A regression
	// counts here rather than against NoProgress, because the two ask different
	// questions.
	Oscillation int
}

// Owed lists every artifact the phase must deliver, produced and for-human alike,
// in a stable order so two runs of the same contract report in the same sequence.
func (c Contract) Owed() []string {
	owed := make([]string, 0, len(c.Produces)+len(c.ForHuman))
	for name := range c.Produces {
		owed = append(owed, name)
	}
	for name := range c.ForHuman {
		owed = append(owed, name)
	}
	sort.Strings(owed)
	return owed
}

// VerifierFor answers how an owed artifact is proven.
func (c Contract) VerifierFor(artifact string) (Verifier, bool) {
	if v, ok := c.Produces[artifact]; ok {
		return v, true
	}
	v, ok := c.ForHuman[artifact]
	return v, ok
}

// Lint reports every way a contract is unusable, rather than the first.
//
// All of them at once because a contract is written by hand at a gate: reporting
// one error per run turns a five-minute correction into five rounds of it.
func (c Contract) Lint() error {
	var faults []string

	if strings.TrimSpace(c.Phase) == "" {
		faults = append(faults, "no phase named: a record with no phase says nothing about what was proven")
	}

	if len(c.Produces) == 0 && len(c.ForHuman) == 0 {
		faults = append(faults, "nothing owed: a contract that demands nothing cannot fail, and a check that cannot fail is ceremony")
	}

	faults = append(faults, lintVerifiers("produces", c.Produces)...)
	faults = append(faults, lintVerifiers("produces_for_human", c.ForHuman)...)
	faults = append(faults, c.lintOverlap()...)
	faults = append(faults, c.lintLoop()...)

	if len(faults) == 0 {
		return nil
	}
	sort.Strings(faults)
	return fmt.Errorf("contract is unusable:\n  - %s", strings.Join(faults, "\n  - "))
}

// lintVerifiers checks one side of the contract.
func lintVerifiers(section string, set map[string]Verifier) []string {
	var faults []string
	for name, v := range set {
		switch {
		case strings.TrimSpace(name) == "":
			faults = append(faults, section+": an artifact with no name cannot be looked for")
		case v == nil:
			faults = append(faults, fmt.Sprintf("%s.%s: no verifier — say how it is proven, or say `kind = \"existence\"` and claim nothing", section, name))
		}
		command, isCommand := v.(Command)
		if !isCommand {
			continue
		}
		if strings.TrimSpace(command.Run) == "" {
			faults = append(faults, fmt.Sprintf("%s.%s: a command verifier with nothing to run", section, name))
		}
		if command.Scope != "" && !command.Scope.Valid() {
			faults = append(faults, fmt.Sprintf("%s.%s: scope %q is not one of full, targeted, existence, human", section, name, command.Scope))
		}
	}
	return faults
}

// lintOverlap refuses an artifact owed on both sides.
//
// The two are checked identically and differ only in whether a later phase may
// ask for it, so an artifact in both is a question with two answers.
func (c Contract) lintOverlap() []string {
	var faults []string
	for name := range c.Produces {
		if _, both := c.ForHuman[name]; both {
			faults = append(faults, fmt.Sprintf("%q is owed as both produces and produces_for_human — it is one or the other", name))
		}
	}
	return faults
}

// lintLoop refuses a loop whose floor names something the phase never produces.
//
// That is the whole value of the floor: a loop converging on an artifact nobody
// delivers would be a ceiling that never fires, which reads as protection and is
// not.
func (c Contract) lintLoop() []string {
	if c.Loop == nil {
		return nil
	}
	var faults []string
	if len(c.Loop.ConvergesOn) == 0 {
		faults = append(faults, "loop: converges_on is empty — a loop with no mechanical floor is left for the model to decide, which is what the floor exists to prevent")
	}
	for _, name := range c.Loop.ConvergesOn {
		if _, owed := c.VerifierFor(name); !owed {
			faults = append(faults, fmt.Sprintf("loop.converges_on names %q, which this phase does not produce", name))
		}
	}
	for _, limit := range []struct {
		name  string
		value int
	}{
		{"max_rounds", c.Loop.MaxRounds},
		{"no_progress", c.Loop.NoProgress},
		{"oscillation", c.Loop.Oscillation},
	} {
		if limit.value < 0 {
			faults = append(faults, fmt.Sprintf("loop.%s is negative (%d)", limit.name, limit.value))
		}
	}
	return faults
}
