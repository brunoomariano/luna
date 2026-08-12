package fsm

// DefaultFlow is the flow Luna ships with — the 14 stages of
// docs/architecture/stages.md.
//
// It is not mandatory: stages can be disabled, edited or replaced, and new ones
// created (ADR-0017). What does not change is the contract — every stage declares
// what it requires and what it produces.
//
// Order is significant: AuditContract checks precedence, not existence.
func DefaultFlow() []Stage {
	return []Stage{
		{
			ID:       "discovery",
			Role:     "scout",
			Requires: []Artifact{TaskID},
			Produces: []Artifact{"repos"},
		},
		{
			ID:       "setup",
			Requires: []Artifact{"repos"},
			Produces: []Artifact{"worktree"},
		},
		{
			ID:       "intake",
			Role:     "analyst",
			Requires: []Artifact{TaskID, "worktree"},
			Produces: []Artifact{"briefing", "kind"},
		},
		{
			ID:               "diagnose",
			Role:             "investigator",
			Requires:         []Artifact{"briefing"},
			Produces:         []Artifact{"root_cause"},
			ProducesForHuman: []Artifact{"min_case"},
			When:             IsBug,
		},
		{
			ID:       "scenarios",
			Role:     "gherkin",
			Requires: []Artifact{"briefing", "kind"},
			Produces: []Artifact{"scenarios", "approach"},
		},
		{
			ID:       "spec",
			Role:     "specifier",
			Requires: []Artifact{"approach"},
			Produces: []Artifact{"contract"},
			When:     IsFeatureOrBug,
		},
		{
			ID:   "build",
			Role: "implementer",
			// ADR-0022 calls for `contract` here, required only when `spec` entered
			// the flow. It stays out until the conditional-requires mechanism is
			// chosen — declaring it without that mechanism would stall every
			// `chore` or `docs` task, since `spec` is skipped in those.
			// See the note in docs/architecture/stages.md.
			Requires: []Artifact{"scenarios", "approach", "worktree"},
			Produces: []Artifact{"code", "tests_green"},
			Verifiers: map[Artifact]Verifier{
				// Targeted rather than full: build runs the tests it touched, and
				// claiming the whole suite here would be the laundering ADR-0028
				// rejects. `verify` is the stage that earns ScopeFull.
				"tests_green": Command{Run: "make test", Scope: ScopeTargeted},
				// `code` has no command that proves it — the compiler is part of
				// `make test`, and "the diff is non-empty" proves nothing about it.
				// It closes on existence, and that is now said rather than defaulted.
				"code": Existence{},
			},
		},
		{
			ID:       "refactor",
			Role:     "cleaner",
			Requires: []Artifact{"code", "tests_green"},
			Produces: []Artifact{"code"},
		},
		{
			ID: "verify",
			// The pipeline is a command and the checklist is a judgement, so this
			// stage has both — and a role, because the artifact that needs one
			// decides (ADR-0040).
			Role:             "verifier",
			Requires:         []Artifact{"code", "scenarios"},
			Produces:         []Artifact{"ci_green"},
			ProducesForHuman: []Artifact{"dod_checked"},
			Verifiers: map[Artifact]Verifier{
				// The one artifact in the flow that earns ScopeFull: `make ci` is
				// the whole gate, and INV-core-4 wants it run rather than claimed.
				"ci_green": Command{Run: "make ci", Scope: ScopeFull},
				// A checklist a person reads. Recording it as a passing check would
				// be the lie ADR-0032 names.
				"dod_checked": Existence{},
			},
		},
		{
			ID:               "qa",
			Role:             "qa",
			Requires:         []Artifact{"ci_green", "briefing"},
			ProducesForHuman: []Artifact{"qa_report"},
			When:             NotChore,
		},
		{
			ID:               "code-review",
			Role:             "reviewer",
			Requires:         []Artifact{"code", "ci_green"},
			ProducesForHuman: []Artifact{"review_report"},
			When:             NotDocs,
		},
		{
			ID:               "harden",
			Role:             "hardener",
			Requires:         []Artifact{"tests_green", "code"},
			ProducesForHuman: []Artifact{"mutation_report"},
			When:             IsFeatureOrBug,
		},
		{
			ID:               "architecture",
			Role:             "architect",
			Requires:         []Artifact{"code"},
			ProducesForHuman: []Artifact{"arch_report"},
			// Unlike the others, this condition is not about the nature of the
			// task: whether the change touched the structure is only knowable
			// after looking at what build produced.
			When: TouchedStructure,
		},
		{
			ID:       "commit",
			Requires: []Artifact{"ci_green", "code"},
			Produces: []Artifact{"commit_sha"},
			Verifiers: map[Artifact]Verifier{
				// INV-core-4 names this one literally: the commit resolves to
				// exactly one object and that object is a commit. `^{commit}`
				// makes git fail rather than answer for a tag or a tree, and
				// --verify makes an ambiguous name an error instead of a guess.
				"commit_sha": Command{Run: "git rev-parse --verify HEAD^{commit}", Scope: ScopeFull},
			},
		},
	}
}
