package fsm

import "testing"

// goFlow was the flow before it moved into files.
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
			Role:     "maker",
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
			ID:   "plan",
			Role: "maker",
			// Scenarios, approach and contract in one stage. The two it replaces
			// were 61% of the first measured task's cost, and `contract` — the
			// artifact the second existed to produce — was required by no stage.
			Gate: &GateSpec{
				Kind: GateReviewArtifact, Artifact: "contract",
				Reason: "review the plan and its contract",
			},
			Requires: []Artifact{"briefing", "kind"},
			Produces: []Artifact{"scenarios", "approach", "contract"},
			Verifiers: map[Artifact]Verifier{
				"scenarios": Existence{Handover: true},
				"approach":  Existence{Handover: true},
				"contract":  Existence{Handover: true},
			},
		},
		{
			ID:   "build",
			Role: "maker",
			// `contract` can be required now that it comes from an unconditional
			// stage. While `spec` was conditional, requiring its output would have
			// stalled every chore and docs task.
			Requires: []Artifact{"scenarios", "approach", "contract", "worktree"},
			Produces: []Artifact{"code", "tests_green"},
			Verifiers: map[Artifact]Verifier{
				// Targeted rather than full: build runs the tests it touched, and
				// claiming the whole suite here would launder a targeted run into
				// a full one. `verify` is the stage that earns ScopeFull.
				"tests_green": Command{Run: "make test", Scope: ScopeTargeted},
				// `code` has no command that proves it — the compiler is part of
				// `make test`, and "the diff is non-empty" proves nothing about it.
				// It closes on existence, and that is now said rather than defaulted.
				"code": Existence{},
			},
		},
		{
			ID:       "refactor",
			Role:     "maker",
			Requires: []Artifact{"code", "tests_green"},
			Produces: []Artifact{"code", "tests_green"},
			Verifiers: map[Artifact]Verifier{
				// The stage rewrites code that was already green, so the green is
				// earned again rather than inherited (INV-1).
				"tests_green": Command{Run: "make test", Scope: ScopeTargeted},
				"code":        Existence{},
			},
		},
		{
			ID: "verify",
			// The pipeline is a command and the checklist is a judgement, so this
			// stage has both — and a role, because the artifact that needs one
			// decides.
			Role:             "critic",
			Requires:         []Artifact{"code", "scenarios"},
			Produces:         []Artifact{"ci_green"},
			ProducesForHuman: []Artifact{"dod_checked"},
			Verifiers: map[Artifact]Verifier{
				// The one artifact in the flow that earns ScopeFull: `make ci` is
				// the whole gate, and INV-1 wants it run rather than claimed.
				"ci_green": Command{Run: "make ci", Scope: ScopeFull},
				// A checklist a person reads. Recording it as a passing check would
				// be a lie about what ran.
				"dod_checked": Existence{Handover: true},
			},
		},
		{
			ID: "review",
			Review: &ReviewSpec{
				SendsBackTo: "build",
				// The green attested to code that no longer exists.
				Invalidates: []Artifact{"ci_green", "tests_green"},
			},
			// One stage where four stood: identical review block, identical
			// verifier, identical handover, four conditions. They read the same
			// code and the same green, so running them apart paid to ingest one
			// diff four times. The lenses live in the role's brief.
			Role:             "critic",
			Requires:         []Artifact{"code", "ci_green"},
			ProducesForHuman: []Artifact{"review_report"},
			Verifiers:        map[Artifact]Verifier{"review_report": Existence{Handover: true}},
			When:             NotChore,
		},
	}
}

// TestTheStockIsTheFlowTheEngineShipped is the acceptance criterion for moving
// the flow out of Go.
//
// The fingerprint covers everything that decides how a past event reads — stage
// ids and order, the artifacts required and produced, the condition's name, the
// gate, the review, and the scope each artifact must be proven to. If
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
	// It has moved three times, all deliberately. Integration left Luna's scope,
	// so `commit` went, and `discovery` went with it (a task is always about the
	// current repository). Then `refactor` gained `tests_green`: it rewrites code
	// that was already green, so the green is earned again rather than inherited,
	// and until then a stage whose whole purpose is rewriting working code closed
	// without running anything (INV-1).
	//
	// The third is this one: twelve stages became eight and twelve roles became
	// three. `scenarios` and `spec` merged into `plan`, and the four review
	// stages into `review`. The argument is cost measured on a real task —
	// the two planning stages were 61% of it — and the fact that both merges
	// joined stages that already shared a role's denial, a session and their
	// input. No task was open when it changed.
	//
	// Any other change to this constant is a flow change that has to be argued
	// for, because every open task's log was written under the old one.
	const shipped = "973859a43a216809"
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

// TestAProjectsFlowReplacesTheShippedOne is what makes "a project brings its own
// flow" real: the stock a project edited is what Luna runs, not the one it was
// built with.
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
