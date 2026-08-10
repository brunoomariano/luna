package fsm

// Verifier declares how one artifact is proven (ADR-0032).
//
// It is a declaration, never an execution. The engine holds it so the contract is
// the complete definition of done for a stage — the answer hermes-agent could not
// give, because an LLM harness cannot know what "verified" means for an arbitrary
// project and a project that declares it up front can.
//
// Running it belongs to the node layer, and the verdict comes back inside the
// action (ADR-0024). Nothing here executes, opens a file, or reads a clock.
//
// It is an interface rather than a command string because several artifacts have
// no command that could check them, and because a third kind — a reviewing model
// judging prose — is foreseeable without being wanted yet. Adding one needs its
// own ADR: a model's verdict is the self-reported completion ADR-0028 rejects,
// and the argument for a reviewer being different is real but unmade.
type Verifier interface {
	// Proves is the scope a successful run of this verifier establishes.
	Proves() Scope

	// Describe names the check for a human, in a few words.
	Describe() string

	isVerifier()
}

// Command verifies an artifact by running a real tool and reading its exit code.
type Command struct {
	// Run is the command line, executed in the task's worktree by the node layer.
	Run string

	// Scope is what a zero exit proves. Declaring it here is what stops a
	// targeted run from being read as a full one later.
	Scope Scope
}

// Proves reports the scope a passing run establishes, defaulting to targeted.
//
// The cautious direction: an unstated scope claiming the full suite would be the
// laundering ADR-0028 exists to prevent, and under-claiming only costs a stage
// that has to prove more.
func (c Command) Proves() Scope {
	if c.Scope == "" {
		return ScopeTargeted
	}
	return c.Scope
}

// Describe names the command for a human.
func (c Command) Describe() string { return c.Run }
func (Command) isVerifier()        {}

// Existence verifies nothing: the artifact was delivered and is in the store.
//
// It is the honest floor for prose — a briefing, a set of scenarios, a diagnosis.
// Recording those as a passing check would be a lie the log tells forever, so
// they carry ScopeExistence and say exactly what happened (ADR-0032).
type Existence struct{}

// Proves reports that nothing was checked beyond the artifact being there.
func (Existence) Proves() Scope { return ScopeExistence }

// Describe names the non-check for a human.
func (Existence) Describe() string { return "delivered" }
func (Existence) isVerifier()      {}

// VerifierFor returns how the stage proves an artifact, defaulting to existence.
//
// The default is deliberate and its risk is known: an artifact that should have
// been checked and was never given a verifier closes on existence alone. That is
// what the static check warns about, so the floor is a choice rather than an
// oversight.
func VerifierFor(stage Stage, artifact Artifact) Verifier {
	if v, ok := stage.Verifiers[artifact]; ok && v != nil {
		return v
	}
	return Existence{}
}
