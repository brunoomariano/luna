package fsm

import (
	"errors"
	"testing"
)

// The statement used to be read from beads on every stage and deliberately left
// out of the log. These tests are what let it move in: the log has to
// carry what a person said, survive their editing it, and keep every stage
// running for a task nobody described.

func TestATaskIsBornWithWhatItIsAbout(t *testing.T) {
	stated := Statement{
		Description: "the cleanup script is zsh-only and silently does nothing under bash",
		Design:      "replace the glob with a POSIX loop",
		Acceptance:  "bash -n exits 0 and zsh -n still does",
	}

	state, err := Reduce(NewTaskState("LUNA-1", ""), TaskCreated{Kind: KindBug, Statement: stated})
	if err != nil {
		t.Fatalf("creating: %v", err)
	}

	if state.Statement != stated {
		t.Errorf("the statement did not survive creation:\n got %+v\nwant %+v", state.Statement, stated)
	}
}

func TestARevisionReplacesWhatTheTaskIsAbout(t *testing.T) {
	state := createdTask(t, Statement{Description: "make the script work"})

	revised := Statement{
		Description: "the cleanup script is zsh-only",
		Acceptance:  "bash -n exits 0",
	}
	state, err := Reduce(state, StatementRevised{Statement: revised})
	if err != nil {
		t.Fatalf("revising: %v", err)
	}

	if state.Statement != revised {
		t.Errorf("the revision did not take:\n got %+v\nwant %+v", state.Statement, revised)
	}
}

// The point of recording the revision rather than mutating a field: replaying the
// same log twice has to land on the same statement, and the last one has to win
// regardless of how many came before it.
func TestTheLastRevisionIsTheOneThatSurvivesAReplay(t *testing.T) {
	state := createdTask(t, Statement{Description: "first"})

	for _, description := range []string{"second", "third", "fourth"} {
		var err error
		state, err = Reduce(state, StatementRevised{Statement: Statement{Description: description}})
		if err != nil {
			t.Fatalf("revising to %q: %v", description, err)
		}
	}

	if got := state.Statement.Description; got != "fourth" {
		t.Errorf("the last revision did not win: got %q, want %q", got, "fourth")
	}
}

// A task nobody described is the ordinary case for anything created without
// --about, and it must not be an error: every stage still runs, the agents are
// just left with less to go on.
func TestATaskNobodyDescribedIsNotAnError(t *testing.T) {
	state, err := Reduce(NewTaskState("LUNA-1", ""), TaskCreated{Kind: KindFeature})
	if err != nil {
		t.Fatalf("creating without a statement: %v", err)
	}

	if state.Statement.Stated() {
		t.Errorf("a task created with no statement reports one: %+v", state.Statement)
	}
}

// Describing a task that does not exist yet is the one refusal, because the log
// would then hold a statement about nothing — TaskCreated is always first, and
// that is what makes the log self-describing.
func TestATaskIsDescribedAfterItIsCreated(t *testing.T) {
	_, err := Reduce(NewTaskState("LUNA-1", ""), StatementRevised{
		Statement: Statement{Description: "about a task that was never created"},
	})
	if !errors.Is(err, ErrIllegalTransition) {
		t.Errorf("describing an uncreated task: got %v, want ErrIllegalTransition", err)
	}
}

// A correction to work already finished is accepted on purpose. Refusing it would
// not stop the correction, it would only push it somewhere the log cannot see.
func TestAFinishedTaskCanStillBeCorrected(t *testing.T) {
	state := createdTask(t, Statement{Description: "what we thought it was"})
	state.Status = StatusDone

	state, err := Reduce(state, StatementRevised{Statement: Statement{Description: "what it turned out to be"}})
	if err != nil {
		t.Fatalf("correcting a finished task: %v", err)
	}

	if got := state.Statement.Description; got != "what it turned out to be" {
		t.Errorf("the correction did not take: got %q", got)
	}
	if state.Status != StatusDone {
		t.Errorf("correcting the statement moved the task: got %q, want %q", state.Status, StatusDone)
	}
}

func createdTask(t *testing.T, stated Statement) TaskState {
	t.Helper()

	state, err := Reduce(NewTaskState("LUNA-1", ""), TaskCreated{Kind: KindBug, Statement: stated})
	if err != nil {
		t.Fatalf("creating: %v", err)
	}
	return state
}

// The gate checks moved into the log for the same reason the statement did: they
// were a person's declaration about their own task, kept in a place that needed a
// second tool to write.

func TestATaskDeclaresWhatAnswersAGate(t *testing.T) {
	state := createdTask(t, Statement{})

	state, err := Reduce(state, GateChecksDeclared{Gate: GateConfirm, Checks: []string{"make ci"}})
	if err != nil {
		t.Fatalf("declaring: %v", err)
	}

	if got := state.GateChecks[GateConfirm]; len(got) != 1 || got[0] != "make ci" {
		t.Errorf("the declaration did not take: %v", got)
	}
}

// Declaring for one gate must leave the others alone: a task usually answers one
// gate mechanically and sends the rest to judgement.
func TestDeclaringForOneGateLeavesTheOthers(t *testing.T) {
	state := createdTask(t, Statement{})

	for gate, check := range map[GateKind]string{
		GateConfirm:        "make fmt",
		GateReviewArtifact: "make ci",
	} {
		var err error
		if state, err = Reduce(state, GateChecksDeclared{Gate: gate, Checks: []string{check}}); err != nil {
			t.Fatalf("declaring for %s: %v", gate, err)
		}
	}

	if len(state.GateChecks) != 2 {
		t.Errorf("want both gates declared, got %v", state.GateChecks)
	}
}

// Declaring an empty list is a person saying this gate has no mechanical answer,
// which is not the same as saying nothing — one is a decision and the other is
// silence, and only one of them is recorded.
func TestDeclaringNoChecksIsNotTheSameAsDeclaringNothing(t *testing.T) {
	state := createdTask(t, Statement{})

	state, err := Reduce(state, GateChecksDeclared{Gate: GateConfirm})
	if err != nil {
		t.Fatalf("declaring: %v", err)
	}

	checks, declared := state.GateChecks[GateConfirm]
	if !declared {
		t.Fatal("an empty declaration left no trace, so it reads as silence")
	}
	if len(checks) != 0 {
		t.Errorf("want an empty declaration, got %v", checks)
	}
}

// A reduce that wrote through the previous state's map would make replaying the
// same log twice land somewhere different — which is the property the whole
// engine rests on.
func TestDeclaringDoesNotMutateThePreviousState(t *testing.T) {
	before := createdTask(t, Statement{})
	before, err := Reduce(before, GateChecksDeclared{Gate: GateConfirm, Checks: []string{"make fmt"}})
	if err != nil {
		t.Fatalf("declaring: %v", err)
	}

	if _, err := Reduce(before, GateChecksDeclared{Gate: GateConfirm, Checks: []string{"make ci"}}); err != nil {
		t.Fatalf("redeclaring: %v", err)
	}

	if got := before.GateChecks[GateConfirm]; got[0] != "make fmt" {
		t.Errorf("the earlier state changed under the caller: %v", got)
	}
}

func TestChecksAreDeclaredForANamedGate(t *testing.T) {
	state := createdTask(t, Statement{})

	if _, err := Reduce(state, GateChecksDeclared{Checks: []string{"make ci"}}); !errors.Is(err, ErrIllegalTransition) {
		t.Errorf("declaring for no gate: got %v, want ErrIllegalTransition", err)
	}
}
