package fsm

import "testing"

// TestScopeSatisfiesIsOneDirectional is the rule that stops a targeted check from
// becoming a green suite (ADR-0028).
//
// The study found that laundering everywhere evidence was recorded at all, and
// the one project that refused it wrote the refusal into its own source.
func TestScopeSatisfiesIsOneDirectional(t *testing.T) {
	cases := []struct {
		have, want Scope
		ok         bool
	}{
		// A full run answers a narrower requirement.
		{ScopeFull, ScopeTargeted, true},
		// The reverse is the laundering, and it is refused.
		{ScopeTargeted, ScopeFull, false},
		// Existence proves nothing about a check that was supposed to run.
		{ScopeExistence, ScopeFull, false},
		{ScopeExistence, ScopeTargeted, false},
		// A person who looked outranks a command that inspected.
		{ScopeHuman, ScopeFull, true},
		{ScopeHuman, ScopeExistence, true},
		// Identity always holds.
		{ScopeFull, ScopeFull, true},
		{ScopeExistence, ScopeExistence, true},
	}

	for _, c := range cases {
		if got := c.have.Satisfies(c.want); got != c.ok {
			t.Errorf("%s satisfying %s: want %v, got %v", c.have, c.want, c.ok, got)
		}
	}
}

// TestDeliveredSeparatesNothingFromAFailedCheck covers the distinction that lets
// a stage report "it built and the tests failed" instead of "nothing happened".
func TestDeliveredSeparatesNothingFromAFailedCheck(t *testing.T) {
	var absent Evidence
	if absent.Delivered() {
		t.Error("zero evidence means the artifact never arrived")
	}

	failed := Evidence{Scope: ScopeFull, Verdict: VerdictFailed}
	if !failed.Delivered() {
		t.Error("a failed check still means something was produced")
	}
	if failed.Passing() {
		t.Error("delivered is not passing")
	}
}

// TestStaleIsNotPassing covers why staleness is a verdict rather than a deletion.
func TestStaleIsNotPassing(t *testing.T) {
	e := Evidence{Scope: ScopeFull, Verdict: VerdictStale, Command: "make ci"}

	if e.Passing() {
		t.Error("stale evidence does not close a stage")
	}
	if !e.Delivered() {
		t.Error("the record stays so the audit can see what was invalidated")
	}
}

// TestEvidenceRendersWhatItProves covers the CLI line: an audit that says a stage
// closed but not on what grounds answers half the question (ADR-0024).
func TestEvidenceRendersWhatItProves(t *testing.T) {
	ran := Evidence{Scope: ScopeFull, Verdict: VerdictPassed, Command: "go test ./...", ExitCode: 0}
	if got := ran.String(); got != "passed (full) go test ./... → 0" {
		t.Errorf("want the command and its exit code, got %q", got)
	}

	// Nothing ran, so there is no command to name — and the scope says so.
	if got := Exists(3).String(); got != "passed (existence)" {
		t.Errorf("want the bare form, got %q", got)
	}
}

// TestApprovedIsHumanScoped covers the gate path.
//
// A person accepting an artifact is a different fact from a check that ran, and
// collapsing the two would let the audit claim a verification happened.
func TestApprovedIsHumanScoped(t *testing.T) {
	e := Approved("the contract a human fixed", 4)

	if e.Scope != ScopeHuman {
		t.Errorf("want human scope, got %q", e.Scope)
	}
	if !e.Passing() {
		t.Error("an approved artifact carries on")
	}
	if e.Detail != "the contract a human fixed" {
		t.Errorf("the accepted version is what the next stage consumes, got %q", e.Detail)
	}
	if e.RecordedAt != 4 {
		t.Errorf("the log position is what staleness compares against, got %d", e.RecordedAt)
	}
}

// TestVerifierForDefaultsToExistence covers the honest floor of ADR-0032, and the
// fact that a declared verifier wins over it.
func TestVerifierForDefaultsToExistence(t *testing.T) {
	stage := Stage{
		ID:       "build",
		Produces: []Artifact{"code", "tests_green"},
		Verifiers: map[Artifact]Verifier{
			"tests_green": Command{Run: "go test ./...", Scope: ScopeFull},
		},
	}

	if got := VerifierFor(stage, "tests_green"); got.Proves() != ScopeFull {
		t.Errorf("a declared verifier is used, got %q", got.Proves())
	}
	if got := VerifierFor(stage, "code"); got.Proves() != ScopeExistence {
		t.Errorf("an undeclared artifact falls to existence, got %q", got.Proves())
	}
	if got := VerifierFor(Stage{ID: "empty"}, "anything"); got.Proves() != ScopeExistence {
		t.Errorf("a stage with no verifiers at all still answers, got %q", got.Proves())
	}
}

// TestACommandWithNoScopeProvesTheCautiousOne covers the default direction.
//
// An unstated scope claiming the full suite would be the laundering; under-
// claiming only costs a stage that has to prove more.
func TestACommandWithNoScopeProvesTheCautiousOne(t *testing.T) {
	if got := (Command{Run: "go test ./pkg/"}).Proves(); got != ScopeTargeted {
		t.Errorf("an unstated scope is targeted, got %q", got)
	}
	if got := (Command{Run: "make ci", Scope: ScopeFull}).Proves(); got != ScopeFull {
		t.Errorf("a stated scope is honoured, got %q", got)
	}
}

// TestVerifiersDescribeThemselves covers the text a human reads.
func TestVerifiersDescribeThemselves(t *testing.T) {
	if got := (Command{Run: "make ci"}).Describe(); got != "make ci" {
		t.Errorf("a command describes itself by what it runs, got %q", got)
	}
	if got := (Existence{}).Describe(); got != "delivered" {
		t.Errorf("existence says what it means, got %q", got)
	}
}
