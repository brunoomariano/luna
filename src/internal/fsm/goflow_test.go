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
			ID:        "intake",
			Role:      "maker",
			Requires:  []Artifact{TaskID, "worktree"},
			Produces:  []Artifact{"briefing"},
			Verifiers: map[Artifact]Verifier{"briefing": Existence{Handover: true}},
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
			Requires: []Artifact{"briefing"},
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
			// The command half, in front of the judgement half and with no role, so
			// a red pipeline stops the task before a model is paid to read code the
			// compiler has not accepted.
			ID:       "pipeline",
			Requires: []Artifact{"code"},
			Produces: []Artifact{"ci_green"},
			Verifiers: map[Artifact]Verifier{
				// The one artifact in the flow that earns ScopeFull: `make ci` is
				// the whole gate, and INV-1 wants it run rather than claimed.
				"ci_green": Command{Run: "make ci", Scope: ScopeFull},
			},
		},
		{
			ID: "verify",
			// The checklist is a judgement, so this half keeps the role.
			Role: "critic",
			// The contract as well, because this stage is the one asked whether the
			// delivery honours it — and it was not given it. `ci_green` too, which
			// is what makes the split enforce something rather than merely reorder
			// two files: the entry check refuses this stage until the pipeline passed.
			Requires:         []Artifact{"code", "scenarios", "contract", "ci_green"},
			ProducesForHuman: []Artifact{"dod_checked"},
			Verifiers: map[Artifact]Verifier{
				// A checklist a person reads. Recording it as a passing check would
				// be a lie about what ran.
				"dod_checked": Existence{Handover: true},
			},
		},
		{
			ID: "audit",
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
			ProducesForHuman: []Artifact{"audit_report"},
			Verifiers:        map[Artifact]Verifier{"audit_report": Existence{Handover: true}},
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
	// The fourth is `verify` gaining `contract` among its inputs. The argument is
	// a gap measured on TALLY-7: the contract required a test pinning one of its
	// own decisions, the test was never written, `build` closed green — correctly,
	// since the *stage* contract asks for `code` and `tests_green` and both
	// arrived — and `verify` then reported that all three decisions were pinned by
	// a test. Nothing in the flow compared the document against what was built,
	// and the one stage positioned to do it had not been handed the document. No
	// task was open when it changed.
	//
	// Any other change to this constant is a flow change that has to be argued
	// for, because every open task's log was written under the old one.
	const shipped = "99fa3a6a6b436a79"
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
