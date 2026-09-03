package contract_test

import (
	"strings"
	"testing"

	"github.com/brunoomariano/luna/src/internal/contract"
)

// The contract a phase is held to, in the shape a conductor writes it.
const forge = `
phase    = "forge"
requires = ["scenarios", "approach"]
produces = ["code", "ci_green"]
produces_for_human = ["delivery_summary"]

[verify.ci_green]
run   = "make ci"
scope = "full"

[verify.code]
kind = "existence"

[verify.delivery_summary]
kind = "existence"

[loop]
converges_on = ["ci_green"]
max_rounds   = 4
no_progress  = 2
oscillation  = 2
`

func parse(t *testing.T, source string) contract.Contract {
	t.Helper()
	c, err := contract.Parse(source, "<test>")
	if err != nil {
		t.Fatalf("parsing: %v", err)
	}
	return c
}

func refuses(t *testing.T, source, want string) {
	t.Helper()
	c, err := contract.Parse(source, "<test>")
	if err == nil {
		err = c.Lint()
	}
	if err == nil {
		t.Fatalf("expected a refusal mentioning %q, got none", want)
	}
	if !strings.Contains(err.Error(), want) {
		t.Fatalf("expected a refusal mentioning %q, got: %v", want, err)
	}
}

func TestAContractCarriesWhatThePhaseOwesAndHowEachIsProven(t *testing.T) {
	c := parse(t, forge)

	if c.Phase != "forge" {
		t.Errorf("phase: got %q, want forge", c.Phase)
	}
	if got := strings.Join(c.Owed(), ","); got != "ci_green,code,delivery_summary" {
		t.Errorf("owed: got %q", got)
	}

	ci, ok := c.VerifierFor("ci_green")
	if !ok {
		t.Fatal("ci_green is owed and has no verifier")
	}
	command, isCommand := ci.(contract.Command)
	if !isCommand {
		t.Fatalf("ci_green: got %T, want a command", ci)
	}
	if command.Run != "make ci" || command.Proves() != contract.ScopeFull {
		t.Errorf("ci_green: got %q at %s", command.Run, command.Proves())
	}
	if err := c.Lint(); err != nil {
		t.Errorf("a well-formed contract was refused: %v", err)
	}
}

// The distinction INV-3 keeps: a for-human artifact is checked on the way out
// like any other, and no later phase will ask for it.
func TestAnArtifactForAHumanIsOwedAndCheckedLikeAnyOther(t *testing.T) {
	c := parse(t, forge)

	if _, ok := c.ForHuman["delivery_summary"]; !ok {
		t.Fatal("delivery_summary is not recorded as written for a human")
	}
	if _, ok := c.Produces["delivery_summary"]; ok {
		t.Error("delivery_summary leaked into produces, where a later phase could require it")
	}
	if _, ok := c.VerifierFor("delivery_summary"); !ok {
		t.Error("a for-human artifact has no verifier, so nothing checks it on the way out")
	}
}

// Scope is the honesty knob, and the default is the cautious direction.
func TestACommandWithNoDeclaredScopeClaimsOnlyWhatItTouched(t *testing.T) {
	c := parse(t, `
phase    = "verify"
produces = ["tests_green"]

[verify.tests_green]
run = "make test"
`)
	v, _ := c.VerifierFor("tests_green")
	if got := v.Proves(); got != contract.ScopeTargeted {
		t.Errorf("an unstated scope proved %q; under-claiming is the safe direction", got)
	}
}

func TestScopeNeverUpgrades(t *testing.T) {
	if contract.ScopeExistence.Satisfies(contract.ScopeFull) {
		t.Error("existence satisfied a demand for full — that is laundering")
	}
	if !contract.ScopeFull.Satisfies(contract.ScopeTargeted) {
		t.Error("full should satisfy a demand for targeted")
	}
	if contract.Scope("thorough").Satisfies(contract.ScopeExistence) {
		t.Error("an unknown scope satisfied a demand; a typo must not outrank the floor")
	}
	if contract.ScopeFull.Satisfies(contract.Scope("thorough")) {
		t.Error("an unknown demand was satisfied; nothing meets a demand nobody can read")
	}
}

func TestAnArtifactWithNoVerifierIsRefused(t *testing.T) {
	refuses(t, `
phase    = "plan"
produces = ["approach"]
`, "no verifier")
}

func TestAContractThatDemandsNothingIsRefused(t *testing.T) {
	refuses(t, `phase = "plan"`, "nothing owed")
}

func TestAContractWithNoPhaseIsRefused(t *testing.T) {
	refuses(t, `
produces = ["code"]

[verify.code]
kind = "existence"
`, "no phase named")
}

// The floor of a loop is its whole value: converging on an artifact nobody
// delivers is a ceiling that never fires.
func TestALoopThatConvergesOnSomethingThePhaseNeverProducesIsRefused(t *testing.T) {
	refuses(t, `
phase    = "forge"
produces = ["code"]

[verify.code]
kind = "existence"

[loop]
converges_on = ["ci_green"]
`, "does not produce")
}

func TestALoopWithNoMechanicalFloorIsRefused(t *testing.T) {
	refuses(t, `
phase    = "forge"
produces = ["code"]

[verify.code]
kind = "existence"

[loop]
max_rounds = 4
`, "converges_on is empty")
}

func TestAnArtifactOwedOnBothSidesIsRefused(t *testing.T) {
	refuses(t, `
phase    = "forge"
produces = ["report"]
produces_for_human = ["report"]

[verify.report]
kind = "existence"
`, "one or the other")
}

// A check left behind by a rename looks like it is running and is not.
func TestACheckForAnArtifactTheContractDoesNotOweIsRefused(t *testing.T) {
	refuses(t, `
phase    = "forge"
produces = ["code"]

[verify.code]
kind = "existence"

[verify.ci_green]
run = "make ci"
`, "does not produce")
}

func TestAVerifierThatBothRunsAndClaimsNothingIsRefused(t *testing.T) {
	refuses(t, `
phase    = "forge"
produces = ["ci_green"]

[verify.ci_green]
run  = "make ci"
kind = "existence"
`, "a command proves it, or nothing does")
}

func TestAnExistenceCheckCannotClaimAScope(t *testing.T) {
	refuses(t, `
phase    = "plan"
produces = ["approach"]

[verify.approach]
scope = "full"
`, "scope with nothing to run")
}

func TestAnUnknownScopeIsRefused(t *testing.T) {
	refuses(t, `
phase    = "forge"
produces = ["ci_green"]

[verify.ci_green]
run   = "make ci"
scope = "thorough"
`, "not one of full, targeted, existence, human")
}

func TestAnUnknownKeyIsRefusedRatherThanIgnored(t *testing.T) {
	refuses(t, `
phase   = "forge"
agent   = "claude"
produces = ["code"]
`, "unknown key")
}

func TestAnUnknownSectionIsRefused(t *testing.T) {
	refuses(t, `
phase = "forge"

[gate]
kind = "confirm"
`, "unknown section")
}

// Lint reports every fault at once: a contract is written by hand at a gate, and
// one error per run turns a five-minute correction into five rounds of it.
func TestLintReportsEveryFaultAtOnce(t *testing.T) {
	c, err := contract.Parse(`
produces = ["code", "ci_green"]

[verify.code]
kind = "existence"
`, "<test>")
	if err != nil {
		t.Fatalf("parsing: %v", err)
	}
	err = c.Lint()
	if err == nil {
		t.Fatal("expected refusals")
	}
	for _, want := range []string{"no phase named", "ci_green"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("the report is missing %q: %v", want, err)
		}
	}
}

// A command is a string, and `#` inside it is not a comment.
func TestACommentInsideACommandIsNotStripped(t *testing.T) {
	c := parse(t, `
phase    = "verify"
produces = ["counted"]

[verify.counted]
run = "git log --format=%h # not a comment"   # this one is
`)
	v, _ := c.VerifierFor("counted")
	if got := v.Describe(); got != "git log --format=%h # not a comment" {
		t.Errorf("the command was truncated at a quoted #: %q", got)
	}
}

func TestAListMaySpanSeveralLines(t *testing.T) {
	c := parse(t, `
phase    = "forge"
requires = [
  "scenarios",
  "approach",
]
produces = ["code"]

[verify.code]
kind = "existence"
`)
	if got := strings.Join(c.Requires, ","); got != "scenarios,approach" {
		t.Errorf("requires: got %q", got)
	}
}

func TestAnUnclosedListIsRefusedRatherThanTruncated(t *testing.T) {
	refuses(t, `
phase    = "forge"
produces = [
  "code",
`, "has to close with ]")
}

// A path is checked against the commit; it does not raise what is claimed.
func TestADeclaredPathDoesNotRaiseWhatAnExistenceCheckClaims(t *testing.T) {
	c := parse(t, `
phase    = "plan"
produces = ["scenarios"]

[verify.scenarios]
kind = "existence"
path = "tests/"
`)
	v, _ := c.VerifierFor("scenarios")
	if v.Proves() != contract.ScopeExistence {
		t.Errorf("a path raised the claim to %q", v.Proves())
	}
	if got := v.Describe(); !strings.Contains(got, "tests/") {
		t.Errorf("describe lost the path: %q", got)
	}
}

// A contract usually arrives on a pipe, so an error has to name the pipe and the
// line rather than a file that does not exist.
func TestAnErrorNamesWhereTheContractCameFromAndWhichLine(t *testing.T) {
	_, err := contract.Parse("phase = \"forge\"\nthis line has no equals sign\n", "<stdin>")
	if err == nil {
		t.Fatal("expected a refusal")
	}
	if !strings.Contains(err.Error(), "<stdin>:2") {
		t.Errorf("the error does not point at the line it read: %v", err)
	}
}

func TestALoopLimitHasToBeAWholeNumber(t *testing.T) {
	refuses(t, `
phase    = "forge"
produces = ["code"]

[verify.code]
kind = "existence"

[loop]
converges_on = ["code"]
max_rounds   = "four"
`, "whole number")
}

func TestANegativeLoopLimitIsRefused(t *testing.T) {
	refuses(t, `
phase    = "forge"
produces = ["code"]

[verify.code]
kind = "existence"

[loop]
converges_on = ["code"]
max_rounds   = -1
`, "negative")
}

func TestAnUnknownKeyInALoopOrAVerifierIsRefused(t *testing.T) {
	refuses(t, `
phase    = "forge"
produces = ["code"]

[verify.code]
kind = "existence"

[loop]
converges_on = ["code"]
patience     = 4
`, "unknown key")

	refuses(t, `
phase    = "forge"
produces = ["code"]

[verify.code]
kind    = "existence"
timeout = "10m"
`, "unknown key")
}

func TestAVerifierThatDeclaresBothRunAndPathIsRefused(t *testing.T) {
	refuses(t, `
phase    = "forge"
produces = ["code"]

[verify.code]
run  = "make ci"
path = "src/"
`, "belongs to an existence check")
}

func TestAnUnknownVerifierKindIsRefused(t *testing.T) {
	refuses(t, `
phase    = "forge"
produces = ["code"]

[verify.code]
kind = "reviewed-by-a-model"
`, "the only kind is")
}

func TestAnArtifactWithNoNameIsRefused(t *testing.T) {
	c := contract.Contract{
		Phase:    "forge",
		Produces: map[string]contract.Verifier{"": contract.Existence{}},
	}
	if err := c.Lint(); err == nil {
		t.Fatal("an artifact with no name was accepted")
	}
}

func TestACommandVerifierWithNothingToRunIsRefused(t *testing.T) {
	c := contract.Contract{
		Phase:    "forge",
		Produces: map[string]contract.Verifier{"ci_green": contract.Command{Run: "   "}},
	}
	err := c.Lint()
	if err == nil || !strings.Contains(err.Error(), "nothing to run") {
		t.Fatalf("got %v", err)
	}
}

func TestAnInvalidScopeOnABuiltCommandIsRefusedByLint(t *testing.T) {
	c := contract.Contract{
		Phase:    "forge",
		Produces: map[string]contract.Verifier{"ci_green": contract.Command{Run: "make ci", Scope: "thorough"}},
	}
	err := c.Lint()
	if err == nil || !strings.Contains(err.Error(), "not one of") {
		t.Fatalf("got %v", err)
	}
}

func TestAKeyOutsideAnySectionIsRefused(t *testing.T) {
	refuses(t, "phase = \"forge\"\nproduces = [\"code\"]\nthis is not a key-value line\n", "expected key = value")
}

func TestAnEmptySectionNameIsRefused(t *testing.T) {
	refuses(t, "phase = \"forge\"\n[]\n", "a section needs a name")
}

func TestDescribeNamesWhatAnExistenceCheckLooksFor(t *testing.T) {
	if got := (contract.Existence{}).Describe(); got != "delivered" {
		t.Errorf("got %q", got)
	}
	if got := (contract.Existence{Path: "docs/"}).Describe(); got != "delivered under docs/" {
		t.Errorf("got %q", got)
	}
}

// Both verifier kinds satisfy the interface, and nothing outside this package can
// implement it — a third kind is a decision to record, not a type to declare.
func TestOnlyTheTwoDeclaredKindsAreVerifiers(t *testing.T) {
	kinds := []contract.Verifier{
		contract.Command{Run: "make ci"},
		contract.Existence{},
	}
	for _, v := range kinds {
		if v.Describe() == "" {
			t.Errorf("%T describes itself as nothing", v)
		}
		if !v.Proves().Valid() {
			t.Errorf("%T proves an unknown scope %q", v, v.Proves())
		}
	}
}

func TestAnEmptyListIsReadAsNoEntries(t *testing.T) {
	c := parse(t, `
phase    = "forge"
requires = []
produces = ["code"]

[verify.code]
kind = "existence"
`)
	if len(c.Requires) != 0 {
		t.Errorf("an empty list produced %d entries", len(c.Requires))
	}
}

func TestAListThatIsNotAListIsRefused(t *testing.T) {
	refuses(t, `
phase    = "forge"
produces = "code"
`, "expected a list")
}

func TestProducesForHumanAcceptsSeveralArtifacts(t *testing.T) {
	c := parse(t, `
phase = "review"
produces_for_human = ["review_report", "min_case"]

[verify.review_report]
kind = "existence"

[verify.min_case]
kind = "existence"
`)
	if len(c.ForHuman) != 2 {
		t.Errorf("got %d for-human artifacts, want 2", len(c.ForHuman))
	}
	if len(c.Produces) != 0 {
		t.Errorf("a for-human artifact leaked into produces: %v", c.Produces)
	}
}
