package fsm

// isFeatureOrBug is the condition governing the stages whose cost only pays off
// when there is new behaviour or a defect to fix.
func isFeatureOrBug(c TaskContext) bool { return c.Kind == KindFeature || c.Kind == KindBug }

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
			Requires:         []Artifact{"briefing"},
			Produces:         []Artifact{"root_cause"},
			ProducesForHuman: []Artifact{"min_case"},
			When:             func(c TaskContext) bool { return c.Kind == KindBug },
		},
		{
			ID:       "scenarios",
			Role:     "gherkin",
			Requires: []Artifact{"briefing", "kind"},
			Produces: []Artifact{"scenarios", "approach"},
		},
		{
			ID:       "spec",
			Requires: []Artifact{"approach"},
			Produces: []Artifact{"contract"},
			When:     isFeatureOrBug,
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
		},
		{
			ID:       "refactor",
			Role:     "cleaner",
			Requires: []Artifact{"code", "tests_green"},
			Produces: []Artifact{"code"},
		},
		{
			ID:               "verify",
			Requires:         []Artifact{"code", "scenarios"},
			Produces:         []Artifact{"ci_green"},
			ProducesForHuman: []Artifact{"dod_checked"},
		},
		{
			ID:               "qa",
			Role:             "qa",
			Requires:         []Artifact{"ci_green", "briefing"},
			ProducesForHuman: []Artifact{"qa_report"},
			When:             func(c TaskContext) bool { return c.Kind != KindChore },
		},
		{
			ID:               "code-review",
			Role:             "reviewer",
			Requires:         []Artifact{"code", "ci_green"},
			ProducesForHuman: []Artifact{"review_report"},
			When:             func(c TaskContext) bool { return c.Kind != KindDocs },
		},
		{
			ID:               "harden",
			Role:             "hardener",
			Requires:         []Artifact{"tests_green", "code"},
			ProducesForHuman: []Artifact{"mutation_report"},
			When:             isFeatureOrBug,
		},
		{
			ID:               "architecture",
			Role:             "architect",
			Requires:         []Artifact{"code"},
			ProducesForHuman: []Artifact{"arch_report"},
			// Unlike the others, this condition is not about the nature of the
			// task: whether the change touched the structure is only knowable
			// after looking at what build produced.
			When: func(c TaskContext) bool { return c.HasFact(TouchesStructure) },
		},
		{
			ID:       "commit",
			Requires: []Artifact{"ci_green", "code"},
			Produces: []Artifact{"commit_sha"},
		},
	}
}
