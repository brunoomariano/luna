package fsm

// Verifier declares how one artifact is proven.
//
// It is a declaration, never an execution. The engine holds it so the contract is
// the complete definition of done for a stage — the answer hermes-agent could not
// give, because an LLM harness cannot know what "verified" means for an arbitrary
// project and a project that declares it up front can.
//
// Running it belongs to the node layer, and the verdict comes back inside the
// action. Nothing here executes, opens a file, or reads a clock.
//
// It is an interface rather than a command string because several artifacts have
// no command that could check them, and because a third kind — a reviewing model
// judging prose — is foreseeable without being wanted yet. Adding one needs its
// own decision recorded: a model's verdict is the self-reported completion a
// status-is-never-a-verdict rule rejects, and the argument for a reviewer being
// different is real but unmade.
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
// The cautious direction: an unstated scope claiming the full suite would be a
// targeted run laundered into a full one, and under-claiming only costs a stage
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
// they carry ScopeExistence and say exactly what happened.
type Existence struct {
	// Path is where the artifact lives, as a directory inside the repository.
	//
	// Declared, the delivery is checked against the commit: `git ls-tree <sha>
	// <path>` either finds a file there or does not, and the answer comes from git
	// rather than from the agent. Undeclared, nothing is checked and the agent's
	// word is the whole record — which is every artifact today.
	//
	// A directory rather than a filename: the agent names the file, which is what
	// swarm-forge's `features/` does and what keeps a contract from having to
	// predict a name it cannot know. A fixed filename would give the agent less to
	// get wrong and give the contract more to be wrong about.
	//
	// Deliberately not part of the flow fingerprint: where an artifact lives is
	// not whether the stage closed, and freezing every open task to move a
	// directory would be disproportionate.
	Path string

	// Handover says the artifact is handed over through Luna's store rather than
	// through the commit.
	//
	// The contract, the scenarios and the audit reports are scaffolding: they exist
	// so the next stage or a person can decide something, and committing them puts
	// working notes into the delivered history of somebody else's repository. An
	// artifact declared this way is written with `luna artifact put`, and git never
	// sees it.
	//
	// Unlike Path, this **is** part of the flow fingerprint. A path changes where a
	// delivery is looked for; this changes what delivering *means* — an artifact
	// that used to be a file in a commit and is now a row in the store is a
	// different obligation, and a log written under the old rule cannot be replayed
	// under the new one.
	Handover bool
}

// Proves reports what an existence check proves, which is that the artifact is
// there and nothing more. A declared path does not raise it: a file in the right
// directory is still just a file.
func (Existence) Proves() Scope { return ScopeExistence }

// Describe names the check for a human, which is a non-check until a path says
// otherwise.
func (e Existence) Describe() string {
	switch {
	case e.Handover:
		return "handed over to Luna"
	case e.Path != "":
		return "delivered under " + e.Path
	default:
		return "delivered"
	}
}

func (Existence) isVerifier() {}

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
