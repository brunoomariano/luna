package fsm

import "testing"

// TestFingerprintCoversWhatChangesHistory is the flow fingerprint as a test.
//
// The fingerprint covers what decides whether a past event still means what it
// meant: stage identity, order, and the artifacts a stage requires and produces.
// It does not cover what is read at the moment of use, because changing that
// alters what happens next rather than what already happened.
func TestFingerprintCoversWhatChangesHistory(t *testing.T) {
	base := []Stage{
		{ID: "first", Requires: []Artifact{TaskID}, Produces: []Artifact{"a"}},
		{ID: "second", Requires: []Artifact{"a"}, Produces: []Artifact{"b"}, ProducesForHuman: []Artifact{"report"}},
	}

	changes := []struct {
		what  string
		flow  []Stage
		alike bool
		// like names another case this one must agree with, for the pairs whose
		// point is that they match each other rather than the base.
		like string
	}{
		{
			what:  "the same flow built twice",
			flow:  []Stage{{ID: "first", Requires: []Artifact{TaskID}, Produces: []Artifact{"a"}}, {ID: "second", Requires: []Artifact{"a"}, Produces: []Artifact{"b"}, ProducesForHuman: []Artifact{"report"}}},
			alike: true,
		},
		{
			what: "a renamed stage — the silent one, which used to replay as the new name",
			flow: []Stage{
				{ID: "first", Requires: []Artifact{TaskID}, Produces: []Artifact{"a"}},
				{ID: "second-v2", Requires: []Artifact{"a"}, Produces: []Artifact{"b"}, ProducesForHuman: []Artifact{"report"}},
			},
		},
		{
			what: "a reordered flow — same stages, different history",
			flow: []Stage{
				{ID: "second", Requires: []Artifact{"a"}, Produces: []Artifact{"b"}, ProducesForHuman: []Artifact{"report"}},
				{ID: "first", Requires: []Artifact{TaskID}, Produces: []Artifact{"a"}},
			},
		},
		{
			what: "a stage inserted mid-flow, which used to re-run finished work",
			flow: []Stage{
				{ID: "first", Requires: []Artifact{TaskID}, Produces: []Artifact{"a"}},
				{ID: "inserted", Produces: []Artifact{"x"}},
				{ID: "second", Requires: []Artifact{"a"}, Produces: []Artifact{"b"}, ProducesForHuman: []Artifact{"report"}},
			},
		},
		{
			what: "an artifact added to Produces — it decides whether a past Complete closed",
			flow: []Stage{
				{ID: "first", Requires: []Artifact{TaskID}, Produces: []Artifact{"a", "extra"}},
				{ID: "second", Requires: []Artifact{"a"}, Produces: []Artifact{"b"}, ProducesForHuman: []Artifact{"report"}},
			},
		},
		{
			what: "an artifact moved from Produces to ProducesForHuman — a different contract",
			flow: []Stage{
				{ID: "first", Requires: []Artifact{TaskID}, ProducesForHuman: []Artifact{"a"}},
				{ID: "second", Requires: []Artifact{"a"}, Produces: []Artifact{"b"}, ProducesForHuman: []Artifact{"report"}},
			},
		},
		{
			what: "a changed role — read at the moment of use, so history is untouched",
			flow: []Stage{
				{ID: "first", Requires: []Artifact{TaskID}, Produces: []Artifact{"a"}},
				{ID: "second", Requires: []Artifact{"a"}, Produces: []Artifact{"b"}, ProducesForHuman: []Artifact{"report"}},
			},
			alike: true,
		},
		{
			what: "a verifier demanding more than existence — the exit check compares scopes, " +
				"so raising the bar changes whether a past Complete closed",
			flow: []Stage{
				{
					ID: "first", Requires: []Artifact{TaskID}, Produces: []Artifact{"a"},
					Verifiers: map[Artifact]Verifier{"a": Command{Run: "make test", Scope: ScopeTargeted}},
				},
				{ID: "second", Requires: []Artifact{"a"}, Produces: []Artifact{"b"}, ProducesForHuman: []Artifact{"report"}},
			},
		},
		{
			what: "the same scope reached by a different command — how much was proven is unchanged",
			flow: []Stage{
				{
					ID: "first", Requires: []Artifact{TaskID}, Produces: []Artifact{"a"},
					Verifiers: map[Artifact]Verifier{"a": Command{Run: "go test ./...", Scope: ScopeTargeted}},
				},
				{
					ID: "second", Requires: []Artifact{"a"}, Produces: []Artifact{"b"},
					ProducesForHuman: []Artifact{"report"},
				},
			},
			// Compared against the case above rather than against base: both declare
			// targeted, so they must agree with each other.
			like: "a verifier demanding more than existence — the exit check compares scopes, " +
				"so raising the bar changes whether a past Complete closed",
		},
		{
			what: "a renamed condition — the rule is not the rule it was",
			flow: []Stage{
				{ID: "first", Requires: []Artifact{TaskID}, Produces: []Artifact{"a"}, When: NotChore},
				{ID: "second", Requires: []Artifact{"a"}, Produces: []Artifact{"b"}, ProducesForHuman: []Artifact{"report"}},
			},
		},
		{
			what: "a condition removed — the stage now enters for every task",
			flow: []Stage{
				{ID: "first", Requires: []Artifact{TaskID}, Produces: []Artifact{"a"}, When: Always},
				{ID: "second", Requires: []Artifact{"a"}, Produces: []Artifact{"b"}, ProducesForHuman: []Artifact{"report"}},
			},
			// base has no condition either: the zero value and Always are the same rule.
			alike: true,
		},
	}

	want := Fingerprint(base)
	seen := map[string]FlowFingerprint{}
	for _, c := range changes {
		got := Fingerprint(c.flow)
		seen[c.what] = got

		if c.like != "" {
			if other, ok := seen[c.like]; !ok {
				t.Errorf("%s: names a case that has not run yet", c.what)
			} else if got != other {
				t.Errorf("%s: should match %q, got %s want %s", c.what, c.like, got, other)
			}
			continue
		}
		if c.alike && got != want {
			t.Errorf("%s: should not change the fingerprint, got %s want %s", c.what, got, want)
		}
		if !c.alike && got == want {
			t.Errorf("%s: should change the fingerprint, both are %s", c.what, got)
		}
	}
}

// TestAnEmptyFingerprintMatchesOnlyAnEmptyFlow covers what the empty value means
// now that nothing writes a log without one.
//
// It used to match anything, so that a log predating the field kept replaying.
// That rule outlived its reason: every task Luna creates is stamped, so the only
// thing an unstamped task could be is one created against no flow — and letting
// that replay against the shipped flow is exactly the silent mismatch the
// fingerprint exists to refuse.
func TestAnEmptyFingerprintMatchesOnlyAnEmptyFlow(t *testing.T) {
	var none FlowFingerprint

	if none.Matches(DefaultFlow()) {
		t.Error("a task stamped with no flow must not replay against the shipped one")
	}
	if !none.Matches(nil) {
		t.Error("no flow and no fingerprint is not a disagreement")
	}
}

// TestTheEmptyFlowHasNoFingerprint keeps a caller with no flow from being told it
// disagrees with one.
func TestTheEmptyFlowHasNoFingerprint(t *testing.T) {
	if got := Fingerprint(nil); got != "" {
		t.Errorf("the empty flow fingerprints as %q, want empty", got)
	}
}

// TestTheShippedFlowMatchesItself is the case every replay depends on: the flow
// this build carries agrees with itself, so an unchanged build never refuses.
func TestTheShippedFlowMatchesItself(t *testing.T) {
	f := Fingerprint(DefaultFlow())

	if f == "" {
		t.Fatal("the shipped flow must have a fingerprint")
	}
	if !f.Matches(DefaultFlow()) {
		t.Error("the shipped flow must match itself")
	}
}

// TestTheShippedFlowFingerprintIsPinned is the guard that was missing, and the
// only one whose absence could strand every open task in silence.
//
// The fingerprint is a task's contract with the flow it was born under: a log
// whose fingerprint no longer matches refuses to replay, and the
// person is told to abandon the task. That is correct when someone changed the
// flow on purpose, and a disaster when a stage file was edited by accident —
// a reordered `requires`, a renamed artifact, a stage inserted.
//
// Nothing caught that. The two other shipped-flow tests here are reflexive —
// the empty flow has no fingerprint, and the shipped flow matches itself —
// which hold no matter what the stock says.
//
// So the value is written down. A change to this constant is a deliberate act
// with a visible diff, and it is the moment to ask what happens to the tasks
// already open. Editing the stock without touching it fails here instead.
func TestTheShippedFlowFingerprintIsPinned(t *testing.T) {
	// The shipped stock as it stands, after eight scaffolding artifacts moved out
	// of the commit and into Luna's store: the contract, the scenarios,
	// the approach, the minimal case and the four audit reports.
	//
	// That change belongs in the fingerprint where a path does not. A
	// path says where a delivery is looked for; this says the artifact is not in
	// the commit at all, which is a different thing owed — a past Complete that
	// closed on a committed file cannot be replayed against a store row.
	//
	// No open task was stranded: every store carrying the previous fingerprint held
	// only test tasks, checked before the value moved.
	//
	// Moved again when `verify` gained `contract` among its inputs. That stage is
	// the one asked whether the delivery honours the contract and was not given
	// it, so nothing in the flow ever compared the document against what was
	// built — measured on TALLY-7, where the contract required a test pinning one
	// of its own decisions, the test was never written, and verify reported that
	// all three decisions were pinned. Checked again before moving: every open
	// task was finished or abandoned.
	const pinned FlowFingerprint = "890d89d9ec69ea47"

	if got := Fingerprint(DefaultFlow()); got != pinned {
		t.Errorf("the shipped flow fingerprints %s, and this test says %s.\n\n"+
			"If the stock was changed on purpose, update the constant — and say in the "+
			"commit what happens to tasks already open, because every one of them stops "+
			"replaying.\n\n"+
			"If it was not, something edited a stage file by accident and this is the "+
			"only thing that would have noticed.", got, pinned)
	}
}

// TestEveryShippedStageIsCoveredByTheFingerprint is the other half: the digest
// has to actually read the stock, not a prefix of it.
//
// A fingerprint built from the first stage alone would be stable, pinned, and
// blind to a change anywhere else — so this asserts that touching any one stage
// moves it.
func TestEveryShippedStageIsCoveredByTheFingerprint(t *testing.T) {
	shipped := Fingerprint(DefaultFlow())

	for i := range DefaultFlow() {
		altered := DefaultFlow()
		altered[i].ID += "-renamed"

		if got := Fingerprint(altered); got == shipped {
			t.Errorf("renaming stage %d (%q) did not move the fingerprint — the digest "+
				"does not cover the whole flow", i, DefaultFlow()[i].ID)
		}
	}
}

// TestAPathDoesNotChangeTheFingerprint is the decision a declared path turned on.
//
// Where an artifact lives is not whether the stage closed. Putting it in the
// fingerprint would freeze every open task to move a directory — disproportionate
// for a field that changes what is checked, not what the stage owed. What the
// fingerprint protects is the requirement, and `Proves()` is unchanged by a path:
// a file in the right directory is still just a file.
func TestAPathDoesNotChangeTheFingerprint(t *testing.T) {
	bare := []Stage{{
		ID: "x", Produces: []Artifact{"a"},
		Verifiers: map[Artifact]Verifier{"a": Existence{}},
	}}
	pathed := []Stage{{
		ID: "x", Produces: []Artifact{"a"},
		Verifiers: map[Artifact]Verifier{"a": Existence{Path: "reports/"}},
	}}

	if Fingerprint(bare) != Fingerprint(pathed) {
		t.Errorf("declaring a path stranded every open task:\n bare   %s\n pathed %s",
			Fingerprint(bare), Fingerprint(pathed))
	}
}
