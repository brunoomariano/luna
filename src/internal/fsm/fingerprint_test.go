package fsm

import "testing"

// TestFingerprintCoversWhatChangesHistory is the decision of ADR-0046 as a test.
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
				{ID: "first", Role: "somebody-else", Requires: []Artifact{TaskID}, Produces: []Artifact{"a"}},
				{ID: "second", Requires: []Artifact{"a"}, Produces: []Artifact{"b"}, ProducesForHuman: []Artifact{"report"}},
			},
			alike: true,
		},
		{
			what: "a changed verifier — evidence records what ran, so the contract's claim is not history",
			flow: []Stage{
				{
					ID: "first", Requires: []Artifact{TaskID}, Produces: []Artifact{"a"},
					Verifiers: map[Artifact]Verifier{"a": Command{Run: "make test"}},
				},
				{ID: "second", Requires: []Artifact{"a"}, Produces: []Artifact{"b"}, ProducesForHuman: []Artifact{"report"}},
			},
			alike: true,
		},
	}

	want := Fingerprint(base)
	for _, c := range changes {
		got := Fingerprint(c.flow)
		if c.alike && got != want {
			t.Errorf("%s: should not change the fingerprint, got %s want %s", c.what, got, want)
		}
		if !c.alike && got == want {
			t.Errorf("%s: should change the fingerprint, both are %s", c.what, got)
		}
	}
}

// TestAnEmptyFingerprintMatchesAnything covers the compatibility rule.
//
// Adding a field to the log's opening event must not make every task already in
// the store unreadable, so a log that predates the field replays as before.
func TestAnEmptyFingerprintMatchesAnything(t *testing.T) {
	var old FlowFingerprint

	if !old.Matches(DefaultFlow()) {
		t.Error("a log written before the field existed must still replay")
	}
	if !old.Matches(nil) {
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
