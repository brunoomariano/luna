package fsm

import (
	"reflect"
	"testing"
)

// TestEveryStageFieldIsClassified is the guard the history-versus-policy rule
// exists to install.
//
// Every field of Stage is either history — read inside the reducer, so it decides
// what a past event meant — or policy, read only at the moment of use. The
// fingerprint covers the first kind and must not cover the second.
//
// The classification is worth freezing rather than trusting to review because the
// failure is silent in both directions. A history field left out means a flow
// change rewrites the past with nothing noticing, which is the bug the flow
// fingerprint was written for. A policy field let in means every replay is
// refused because somebody edited a brief, and a check that fires on changes
// that do not matter is one people learn to route around.
//
// Camunda paid for the first direction: mapping a catch event preserved the
// subscription under the old message name, and instances waited forever with no
// incident raised. Their own conclusion is the reason this test is by reflection
// rather than by a hand-kept list — the validation was not too small, it was
// looking at the wrong boundary.
func TestEveryStageFieldIsClassified(t *testing.T) {
	// history: read inside Reduce, so it changes what a past event meant.
	// policy:  read outside it, so it changes only what happens next.
	classified := map[string]string{
		"ID":               "history", // NextStage and stageIn resolve by it
		"Requires":         "history", // the entry check decides whether an Advance entered
		"Produces":         "history", // the exit check decides whether a Complete closed
		"ProducesForHuman": "history", // the same check, and INV-3
		"When":             "history", // AppliesTo decides which stages a task walks
		"Verifiers":        "history", // partly: the scope it declares, never its command
		"Gate":             "history", // gateFor decides whether a past Advance suspended
		"Review":           "history", // decides whether a past finding was legal, and where it went
		"Guard":            "policy",  // the node matches it; the reducer reads only whether it fired
		"Role":             "policy",  // only internal/node reads it
		"Context":          "policy",  // read when the agent starts; it changes cost, not what delivering meant
		"Memory":           "policy",  // the same: what the agent knows walking in, not what it owes walking out
	}

	stage := reflect.TypeOf(Stage{})
	for i := range stage.NumField() {
		name := stage.Field(i).Name
		if _, ok := classified[name]; !ok {
			t.Errorf("Stage.%s is not classified as history or policy — decide which it is "+
				"and record it in docs/decisions.md before adding it, because leaving it out of the "+
				"fingerprint silently rewrites the past and putting it in refuses replays "+
				"for changes that do not matter", name)
		}
	}

	// The other direction: a field removed from Stage should not leave a stale
	// entry here claiming to classify something that no longer exists.
	for name := range classified {
		if _, ok := stage.FieldByName(name); !ok {
			t.Errorf("Stage.%s is classified here and no longer exists on the type", name)
		}
	}
}

// TestTheFingerprintReactsToEveryHistoryField ties the classification above to
// what the fingerprint actually does.
//
// The list is easy to keep and easy to lie about: a field can be marked history
// and never reach the digest. This changes one field at a time and insists the
// digest moves — so the classification is a claim the code has to honour.
func TestTheFingerprintReactsToEveryHistoryField(t *testing.T) {
	base := []Stage{{
		ID:               "only",
		Requires:         []Artifact{TaskID},
		Produces:         []Artifact{"a"},
		ProducesForHuman: []Artifact{"report"},
		When:             NotChore,
		Gate:             &GateSpec{Kind: GateConfirm, Reason: "confirm something"},
		Review:           &ReviewSpec{SendsBackTo: "only", Invalidates: []Artifact{"a"}},
		Verifiers:        map[Artifact]Verifier{"a": Command{Run: "make test", Scope: ScopeTargeted}},
		Role:             "somebody",
	}}
	want := Fingerprint(base)

	changes := map[string]func(*Stage){
		"ID":               func(s *Stage) { s.ID = "renamed" },
		"Requires":         func(s *Stage) { s.Requires = nil },
		"Produces":         func(s *Stage) { s.Produces = []Artifact{"different"} },
		"ProducesForHuman": func(s *Stage) { s.ProducesForHuman = nil },
		"When":             func(s *Stage) { s.When = NotDocs },
		// The kind, not the reason: rewording the prompt a person reads cannot
		// change whether a past Advance suspended, so only the kind and the
		// artifact reach the digest.
		"Gate": func(s *Stage) {
			s.Gate = &GateSpec{Kind: GateReviewArtifact, Reason: "confirm something"}
		},
		"Review": func(s *Stage) {
			s.Review = &ReviewSpec{SendsBackTo: "elsewhere", Invalidates: []Artifact{"a"}}
		},
		"Verifiers": func(s *Stage) {
			s.Verifiers = map[Artifact]Verifier{"a": Command{Run: "make test", Scope: ScopeFull}}
		},
	}

	for field, change := range changes {
		altered := base[0]
		change(&altered)
		if got := Fingerprint([]Stage{altered}); got == want {
			t.Errorf("Stage.%s is classified as history and the fingerprint did not move when "+
				"it changed — a flow edited there would replay as though nothing happened", field)
		}
	}

	// And what is deliberately excluded must not move it, or every reworded prompt
	// and renamed Makefile target becomes a refused replay.
	unmoved := map[string]func(*Stage){
		"Role, which only internal/node reads": func(s *Stage) {
			s.Role = "somebody-else"
		},
		"a gate's reason, which is prose a person reads at the moment they are asked": func(s *Stage) {
			s.Gate = &GateSpec{Kind: GateConfirm, Reason: "reworded entirely"}
		},
		"a verifier's command, since evidence records what actually ran": func(s *Stage) {
			s.Verifiers = map[Artifact]Verifier{"a": Command{Run: "go test ./...", Scope: ScopeTargeted}}
		},
		// The intuition runs the other way on these two, which is why they are
		// asserted rather than assumed. They decide who is asked at a gate opening
		// now; a gate answered last week replays from its recorded GateDecision and
		// not from the policy that produced it. Including them would
		// strand every open task the moment a project declared criticality, for a
		// change that cannot alter how one past event reads.
		"a gate's criticality, which decides who is asked and not what happened": func(s *Stage) {
			s.Gate = &GateSpec{Kind: GateConfirm, Reason: "confirm something", Criticality: 3}
		},
		"a gate's judgement criteria, for the same reason": func(s *Stage) {
			s.Gate = &GateSpec{
				Kind:   GateConfirm,
				Reason: "confirm something",
				Judge:  []string{"the contract states what is forbidden"},
			}
		},
	}

	for what, change := range unmoved {
		altered := base[0]
		change(&altered)
		if got := Fingerprint([]Stage{altered}); got != want {
			t.Errorf("%s is excluded on purpose and the fingerprint moved: %s want %s", what, got, want)
		}
	}
}

// TestTheFingerprintIsStableAcrossRuns rules out the nondeterminism that would
// enter through the back door.
//
// Verifiers is a map, and Go randomises map iteration. A digest built by ranging
// over it would differ between two calls in the same process, and every replay
// would be refused against a flow that had not changed — the reducer's purity
// would be intact and the log unreadable anyway.
func TestTheFingerprintIsStableAcrossRuns(t *testing.T) {
	flow := DefaultFlow()
	first := Fingerprint(flow)

	for range 50 {
		if got := Fingerprint(flow); got != first {
			t.Fatalf("the same flow fingerprinted as %s and then %s", first, got)
		}
	}
}
