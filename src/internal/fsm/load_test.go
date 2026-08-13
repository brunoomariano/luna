package fsm

import (
	"strings"
	"testing"
	"testing/fstest"
)

// TestAStageFileBecomesAStage covers the shape a person writes.
func TestAStageFileBecomesAStage(t *testing.T) {
	stage, err := ParseStage(`
id                 = "build"
role               = "implementer"
when               = "is-bug"
requires           = ["scenarios", "approach"]
produces           = ["code"]
produces_for_human = ["notes"]

[verify.code]
run   = "make test"
scope = "targeted"

[gate]
kind   = "confirm"
reason = "confirm the write"

[review]
sends_back_to = "build"
invalidates   = ["ci_green"]
`, "build.toml")
	if err != nil {
		t.Fatalf("ParseStage: %v", err)
	}

	if stage.ID != "build" || stage.Role != "implementer" {
		t.Errorf("id=%q role=%q", stage.ID, stage.Role)
	}
	if stage.When.Name != "is-bug" {
		t.Errorf("when = %q, want the named condition", stage.When.Name)
	}
	if len(stage.Requires) != 2 || stage.Requires[0] != "scenarios" {
		t.Errorf("requires = %v", stage.Requires)
	}
	if stage.ProducesForHuman[0] != "notes" {
		t.Errorf("produces_for_human = %v", stage.ProducesForHuman)
	}

	command, ok := stage.Verifiers["code"].(Command)
	if !ok {
		t.Fatalf("verifier = %T, want a Command", stage.Verifiers["code"])
	}
	if command.Run != "make test" || command.Scope != ScopeTargeted {
		t.Errorf("verifier = %+v", command)
	}
	if stage.Gate == nil || stage.Gate.Kind != GateConfirm {
		t.Errorf("gate = %+v", stage.Gate)
	}
	if stage.Review == nil || stage.Review.SendsBackTo != "build" {
		t.Errorf("review = %+v", stage.Review)
	}
}

// TestAnArtifactWithNoVerifierIsRefused is the decision this format exists for.
//
// ADR-0032 says the contract declares how each artifact is verified, and until
// now the floor was a default nobody noticed — `VerifierFor` returns `Existence`
// for anything undeclared, so a stage closed on nothing having run. In a file
// the choice has to be written down.
func TestAnArtifactWithNoVerifierIsRefused(t *testing.T) {
	_, err := ParseStage(`
id       = "build"
role     = "implementer"
requires = ["scenarios"]
produces = ["code", "tests_green"]

[verify.code]
kind = "existence"
`, "build.toml")

	if err == nil {
		t.Fatal("a produced artifact with no declared verifier was accepted")
	}
	if !strings.Contains(err.Error(), "tests_green") {
		t.Errorf("the error does not name the artifact: %v", err)
	}
	if !strings.Contains(err.Error(), "existence") {
		t.Errorf("the error does not say how to declare the floor: %v", err)
	}
}

// TestExistenceIsWrittenDownRatherThanDefaulted. The floor is still available —
// what changed is that it is a choice rather than an omission.
func TestExistenceIsWrittenDownRatherThanDefaulted(t *testing.T) {
	stage, err := ParseStage(`
id       = "discovery"
role     = "scout"
requires = ["task_id"]
produces = ["repos"]

[verify.repos]
kind = "existence"
`, "discovery.toml")
	if err != nil {
		t.Fatalf("ParseStage: %v", err)
	}

	if _, ok := stage.Verifiers["repos"].(Existence); !ok {
		t.Errorf("verifier = %T, want Existence", stage.Verifiers["repos"])
	}
}

// TestAHumanFacingArtifactNeedsNoVerifier. A report is read by a person, and
// requiring a machine check on prose would be requiring the wrong thing
// (INV-core-11).
func TestAHumanFacingArtifactNeedsNoVerifier(t *testing.T) {
	_, err := ParseStage(`
id                 = "qa"
role               = "qa"
requires           = ["ci_green"]
produces_for_human = ["qa_report"]
`, "qa.toml")
	if err != nil {
		t.Fatalf("a stage producing only a report was refused: %v", err)
	}
}

// TestACommandWithNoScopeIsRefused. The scope is what stops a targeted run from
// being read as a full one later (ADR-0032); a command that has not said what it
// proves has not been declared.
func TestACommandWithNoScopeIsRefused(t *testing.T) {
	_, err := ParseStage(`
id       = "verify"
role     = "verifier"
requires = ["code"]
produces = ["ci_green"]

[verify.ci_green]
run = "make ci"
`, "verify.toml")

	if err == nil {
		t.Fatal("a command with no scope was accepted")
	}
	if !strings.Contains(err.Error(), "scope") {
		t.Errorf("the error does not say what is missing: %v", err)
	}
}

// TestTheLoaderRefusesWhatItCannotRead covers every direction a file can be
// wrong. Each one is a refusal rather than a default, because a flow that
// half-loads is worse than one that will not: the half that is missing is
// invisible until a task walks into it.
func TestTheLoaderRefusesWhatItCannotRead(t *testing.T) {
	for name, content := range map[string]string{
		"no id": `
role     = "scout"
produces = ["repos"]
[verify.repos]
kind = "existence"`,

		"unknown key": `
id       = "build"
nonsense = "what"`,

		"unknown section": `
id = "build"
[nonsense]
key = "value"`,

		"unknown condition": `
id   = "build"
when = "when-the-moon-is-full"`,

		"unknown scope": `
id       = "build"
produces = ["code"]
[verify.code]
run   = "make test"
scope = "quite-thorough"`,

		"unknown gate kind": `
id = "build"
[gate]
kind = "ask-nicely"`,

		"unknown verifier kind": `
id       = "build"
produces = ["code"]
[verify.code]
kind = "vibes"`,

		"both run and kind": `
id       = "build"
produces = ["code"]
[verify.code]
run  = "make test"
kind = "existence"`,

		"a verifier with neither": `
id       = "build"
produces = ["code"]
[verify.code]
scope = "full"`,

		"not a key = value": `
id = "build"
this is not toml`,

		"a list that does not close": `
id       = "build"
requires = ["a", "b"`,
	} {
		t.Run(name, func(t *testing.T) {
			if _, err := ParseStage(content, "stage.toml"); err == nil {
				t.Error("accepted")
			}
		})
	}
}

// TestTheErrorSaysWhichFileAndLine. Someone editing a stage file is the audience,
// and "invalid" without a position is a search rather than a fix.
func TestTheErrorSaysWhichFileAndLine(t *testing.T) {
	_, err := ParseStage("id = \"build\"\nnonsense = \"what\"\n", "070-build.toml")

	if err == nil {
		t.Fatal("an unknown key was accepted")
	}
	if !strings.Contains(err.Error(), "070-build.toml:2") {
		t.Errorf("the error does not point at the line: %v", err)
	}
}

// TestACommentDoesNotEatACommand is why comments are stripped with the quotes in
// view. `git log --format=%h # the sha` is a legitimate command, and naive
// splitting on `#` would leave a verifier proving something other than declared.
func TestACommentDoesNotEatACommand(t *testing.T) {
	stage, err := ParseStage(`
id       = "commit"
produces = ["commit_sha"]

[verify.commit_sha]
run   = "git log --format=#%h"   # the short sha
scope = "full"
`, "commit.toml")
	if err != nil {
		t.Fatalf("ParseStage: %v", err)
	}

	command, ok := stage.Verifiers["commit_sha"].(Command)
	if !ok {
		t.Fatalf("verifier = %T, want a Command", stage.Verifiers["commit_sha"])
	}
	if command.Run != "git log --format=#%h" {
		t.Errorf("run = %q — the comment ate part of the command", command.Run)
	}
}

// TestTheFlowIsOrderedByFilename. Order is significant — AuditContract checks
// precedence, not existence — so the directory has to read as the flow.
func TestTheFlowIsOrderedByFilename(t *testing.T) {
	files := fstest.MapFS{
		"stages/020-second.toml": &fstest.MapFile{Data: []byte(`
id       = "second"
requires = ["a"]
produces = ["b"]
[verify.b]
kind = "existence"`)},
		"stages/010-first.toml": &fstest.MapFile{Data: []byte(`
id       = "first"
requires = ["task_id"]
produces = ["a"]
[verify.a]
kind = "existence"`)},
		"stages/notes.md": &fstest.MapFile{Data: []byte("not a stage")},
	}

	flow, err := LoadFlow(files, "stages")
	if err != nil {
		t.Fatalf("LoadFlow: %v", err)
	}

	if len(flow) != 2 {
		t.Fatalf("loaded %d stages, want 2 — a non-toml file is not a stage", len(flow))
	}
	if flow[0].ID != "first" || flow[1].ID != "second" {
		t.Errorf("order = %s, %s — the numeric prefix is what orders them", flow[0].ID, flow[1].ID)
	}
}

// TestADirectoryWithNoStagesIsRefused. An empty flow is not a flow, and
// returning one would make every task finish immediately having done nothing.
func TestADirectoryWithNoStagesIsRefused(t *testing.T) {
	files := fstest.MapFS{"stages/README.md": &fstest.MapFile{Data: []byte("nothing here")}}

	if _, err := LoadFlow(files, "stages"); err == nil {
		t.Fatal("a directory with no stage files produced a flow")
	}
	if _, err := LoadFlow(files, "nowhere"); err == nil {
		t.Fatal("a directory that does not exist produced a flow")
	}
}

// TestOneBadFileFailsTheWholeLoad. A flow that half-loads is worse than one that
// refuses: the stage that failed to parse is missing, and nothing says so until
// a task reaches where it should have been.
func TestOneBadFileFailsTheWholeLoad(t *testing.T) {
	files := fstest.MapFS{
		"stages/010-fine.toml": &fstest.MapFile{Data: []byte(`
id       = "fine"
requires = ["task_id"]
produces = ["a"]
[verify.a]
kind = "existence"`)},
		"stages/020-broken.toml": &fstest.MapFile{Data: []byte(`id = "broken"
nonsense = true`)},
	}

	if _, err := LoadFlow(files, "stages"); err == nil {
		t.Fatal("a directory with one broken stage loaded")
	}
}

// TestTheShippedConditionsAreNamedAndClosed. A stage file names a condition; it
// does not describe one. A predicate written in a file is a flow whose past
// cannot be reconstructed, because the predicate that produced it may be gone
// (ADR-0048).
func TestTheShippedConditionsAreNamedAndClosed(t *testing.T) {
	for _, condition := range ShippedConditions() {
		got, err := ParseCondition(condition.Name)
		if err != nil {
			t.Errorf("%s is shipped and does not parse: %v", condition.Name, err)
			continue
		}
		if got.Name != condition.Name {
			t.Errorf("%s resolved to %s", condition.Name, got.Name)
		}
	}

	if _, err := ParseCondition("something-invented"); err == nil {
		t.Error("an unknown condition was accepted")
	}

	// No `when` is the unconditional stage, which is the common case.
	always, err := ParseCondition("")
	if err != nil {
		t.Fatalf("an absent condition: %v", err)
	}
	if !always.Met(NewTaskContext(KindChore)) {
		t.Error("the absent condition does not always apply")
	}
}

// TestEveryKeyOfEveryBlockIsRead. A key the loader accepts and drops is worse
// than one it refuses: the file says something the flow does not do.
func TestEveryKeyOfEveryBlockIsRead(t *testing.T) {
	stage, err := ParseStage(`
id       = "spec"
role     = "specifier"
requires = ["approach"]
produces = ["contract"]

[verify.contract]
kind = "existence"

[gate]
kind     = "review-artifact"
artifact = "contract"
reason   = "review the contract"

[review]
sends_back_to = "build"
invalidates   = ["ci_green", "tests_green"]
`, "spec.toml")
	if err != nil {
		t.Fatalf("ParseStage: %v", err)
	}

	if stage.Gate.Artifact != "contract" {
		t.Errorf("gate artifact = %q — the key was accepted and dropped", stage.Gate.Artifact)
	}
	if stage.Gate.Reason != "review the contract" {
		t.Errorf("gate reason = %q", stage.Gate.Reason)
	}
	if len(stage.Review.Invalidates) != 2 {
		t.Errorf("invalidates = %v, want both", stage.Review.Invalidates)
	}
}

// TestAVerifyKeyOutsideItsBlockIsRefused. `run = "..."` at the top of a file is
// someone who forgot the header, and silently ignoring it would leave the
// artifact proven by nothing.
func TestAVerifyKeyOutsideItsBlockIsRefused(t *testing.T) {
	for _, content := range []string{
		`id = "build"
[gate]
nonsense = "x"`,
		`id = "build"
[review]
nonsense = "x"`,
		`id       = "build"
produces = ["code"]
[verify.code]
nonsense = "x"`,
	} {
		if _, err := ParseStage(content, "stage.toml"); err == nil {
			t.Errorf("an unknown key was accepted in:\n%s", content)
		}
	}
}

// TestAnEmptyListIsAList. `requires = []` is a stage that needs nothing, which
// the first stage of a flow legitimately is.
func TestAnEmptyListIsAList(t *testing.T) {
	stage, err := ParseStage(`
id       = "start"
requires = []
produces = ["a"]
[verify.a]
kind = "existence"
`, "start.toml")
	if err != nil {
		t.Fatalf("ParseStage: %v", err)
	}
	if len(stage.Requires) != 0 {
		t.Errorf("requires = %v, want empty", stage.Requires)
	}
}

// TestAnUnquotedValueIsRead. TOML wants quotes and a person editing by hand
// forgets them; reading the value anyway is kinder than refusing over a
// punctuation mark that changes no meaning.
func TestAnUnquotedValueIsRead(t *testing.T) {
	stage, err := ParseStage(`
id       = start
produces = [a]
[verify.a]
kind = existence
`, "start.toml")
	if err != nil {
		t.Fatalf("ParseStage: %v", err)
	}
	if stage.ID != "start" {
		t.Errorf("id = %q", stage.ID)
	}
}

// TestAListThatDoesNotOpenIsRefused covers the other half of the bracket check.
func TestAListThatDoesNotOpenIsRefused(t *testing.T) {
	if _, err := ParseStage("id = \"build\"\nrequires = \"a\", \"b\"]\n", "stage.toml"); err == nil {
		t.Fatal("a list with no opening bracket was accepted")
	}
}
