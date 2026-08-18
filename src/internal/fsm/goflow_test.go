package fsm

import "testing"

// goFlow was the flow before RFC-0003 moved it into files.
//
// It is kept as the fixture that proves the move changed nothing: a test
// compares its fingerprint against the parsed stock's, so a stage file that
// drifts from what the engine shipped fails rather than quietly becoming the new
// truth. It is not called in production and is not meant to be.
func goFlow() []Stage {
	return []Stage{
		{
			ID:       "setup",
			Requires: []Artifact{TaskID},
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
			Verifiers:        map[Artifact]Verifier{"min_case": Existence{Handover: true}},
			When:             IsBug,
		},
		{
			ID:       "scenarios",
			Role:     "gherkin",
			Gate:     &GateSpec{Kind: GateConfirm, Reason: "approve the plan"},
			Requires: []Artifact{"briefing", "kind"},
			Produces: []Artifact{"scenarios", "approach"},
			Verifiers: map[Artifact]Verifier{
				"scenarios": Existence{Handover: true},
				"approach":  Existence{Handover: true},
			},
		},
		{
			ID:   "spec",
			Role: "specifier",
			Gate: &GateSpec{
				Kind: GateReviewArtifact, Artifact: "contract", Reason: "review the contract",
			},
			Requires:  []Artifact{"approach"},
			Produces:  []Artifact{"contract"},
			Verifiers: map[Artifact]Verifier{"contract": Existence{Handover: true}},
			When:      IsFeatureOrBug,
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
			Produces: []Artifact{"code", "tests_green"},
			Verifiers: map[Artifact]Verifier{
				// The stage rewrites code that was already green, so the green is
				// earned again rather than inherited (ADR-0020, INV-core-4).
				"tests_green": Command{Run: "make test", Scope: ScopeTargeted},
				"code":        Existence{},
			},
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
				"dod_checked": Existence{Handover: true},
			},
		},
		{
			ID: "qa",
			Review: &ReviewSpec{
				SendsBackTo: "build",
				// The green attested to code that no longer exists (ADR-0020).
				Invalidates: []Artifact{"ci_green", "tests_green"},
			},
			Role:             "qa",
			Requires:         []Artifact{"ci_green", "briefing"},
			ProducesForHuman: []Artifact{"qa_report"},
			Verifiers:        map[Artifact]Verifier{"qa_report": Existence{Handover: true}},
			When:             NotChore,
		},
		{
			ID: "code-review",
			Review: &ReviewSpec{
				SendsBackTo: "build",
				// The green attested to code that no longer exists (ADR-0020).
				Invalidates: []Artifact{"ci_green", "tests_green"},
			},
			Role:             "reviewer",
			Requires:         []Artifact{"code", "ci_green"},
			ProducesForHuman: []Artifact{"review_report"},
			Verifiers:        map[Artifact]Verifier{"review_report": Existence{Handover: true}},
			When:             NotDocs,
		},
		{
			ID: "harden",
			Review: &ReviewSpec{
				SendsBackTo: "build",
				// The green attested to code that no longer exists (ADR-0020).
				Invalidates: []Artifact{"ci_green", "tests_green"},
			},
			Role:             "hardener",
			Requires:         []Artifact{"tests_green", "code"},
			ProducesForHuman: []Artifact{"mutation_report"},
			Verifiers:        map[Artifact]Verifier{"mutation_report": Existence{Handover: true}},
			When:             IsFeatureOrBug,
		},
		{
			ID: "architecture",
			Review: &ReviewSpec{
				SendsBackTo: "build",
				// The green attested to code that no longer exists (ADR-0020).
				Invalidates: []Artifact{"ci_green", "tests_green"},
			},
			Role:             "architect",
			Requires:         []Artifact{"code"},
			ProducesForHuman: []Artifact{"arch_report"},
			Verifiers:        map[Artifact]Verifier{"arch_report": Existence{Handover: true}},
			// Unlike the others, this condition is not about the nature of the
			// task: whether the change touched the structure is only knowable
			// after looking at what build produced.
			When: TouchedStructure,
		},
	}
}

// TestTheStockIsTheFlowTheEngineShipped is the acceptance criterion for moving
// the flow out of Go (RFC-0003).
//
// The fingerprint covers everything that decides how a past event reads — stage
// ids and order, the artifacts required and produced, the condition's name, the
// gate, the review, and the scope each artifact must be proven to (ADR-0046). If
// the parsed stock and the Go literals agree on it, the move changed nothing
// that could refuse a replay, which is the only guarantee that matters to a task
// already open.
func TestTheStockIsTheFlowTheEngineShipped(t *testing.T) {
	want := Fingerprint(goFlow())
	got := Fingerprint(DefaultFlow())

	if got != want {
		t.Fatalf("the stock does not match the flow the engine shipped:\n"+
			"  files: %s\n  Go:    %s\n"+
			"every task already open was written under the second one", got, want)
	}

	// And the recorded value, so a change to *both* is still caught. Two things
	// drifting together is exactly what a comparison between them cannot see.
	//
	// It has moved twice, both deliberately. ADR-0062 removed `commit` (Luna does
	// not integrate) and `discovery` went with it (a task is always about the
	// current repository). Then `refactor` gained `tests_green`: it rewrites code
	// that was already green, so the green is earned again rather than inherited,
	// and until then a stage whose whole purpose is rewriting working code closed
	// without running anything (INV-core-4, ADR-0020).
	//
	// Any other change to this constant is a flow change that has to be argued
	// for, because every open task's log was written under the old one.
	const shipped = "18464f834de0e0fd"
	if got != shipped {
		t.Errorf("fingerprint = %s, want %s — the shipped flow changed, and every "+
			"open task's log was written under the old one", got, shipped)
	}
}

// TestTheStockHasEveryStage guards the count as well as the digest: a file that
// failed to load would change the fingerprint, but so would a dozen other
// things, and this says which.
func TestTheStockHasEveryStage(t *testing.T) {
	stock := DefaultFlow()
	engine := goFlow()

	if len(stock) != len(engine) {
		t.Fatalf("the stock has %d stages, the engine shipped %d", len(stock), len(engine))
	}
	for i := range engine {
		if stock[i].ID != engine[i].ID {
			t.Errorf("stage %d: stock has %q, the engine shipped %q", i, stock[i].ID, engine[i].ID)
		}
	}
}

// TestAProjectsFlowReplacesTheShippedOne is what makes ADR-0017 real: the stock
// a project edited is what Luna runs, not the one it was built with.
//
// UseFlow is a package-level value set once at startup, which is a trade worth
// testing rather than trusting — the alternative was threading the flow through
// fifteen call sites that would all pass the same thing.
func TestAProjectsFlowReplacesTheShippedOne(t *testing.T) {
	shipped := Fingerprint(DefaultFlow())
	t.Cleanup(func() { UseFlow(nil) })

	own := []Stage{{
		ID:        "only",
		Role:      "implementer",
		Requires:  []Artifact{TaskID},
		Produces:  []Artifact{"code"},
		Verifiers: map[Artifact]Verifier{"code": Existence{}},
	}}
	UseFlow(own)

	got := DefaultFlow()
	if len(got) != 1 || got[0].ID != "only" {
		t.Fatalf("the project's flow did not take: %d stages", len(got))
	}
	if Fingerprint(got) == shipped {
		t.Error("a different flow produced the shipped fingerprint, so a task " +
			"written under one would replay under the other")
	}

	// And putting it back restores the shipped one, so a process that never sets
	// a flow is unaffected.
	UseFlow(nil)
	if Fingerprint(DefaultFlow()) != shipped {
		t.Error("clearing the project's flow did not restore the shipped one")
	}
}
