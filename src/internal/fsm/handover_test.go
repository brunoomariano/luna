package fsm_test

import (
	"strings"
	"testing"

	"github.com/brunoomariano/luna/src/internal/fsm"
)

// TestHandoverChangesTheFingerprint is the difference between this and a path,
// and the reason it is worth a test of its own.
//
// A path changes where a delivery is looked for, so ADR-0070 kept it out. Handing
// an artifact to the store changes what delivering *means* — the artifact is no
// longer in the commit at all — so a log written under one rule must not replay
// under the other.
func TestHandoverChangesTheFingerprint(t *testing.T) {
	committed := []fsm.Stage{{
		ID: "spec", Produces: []fsm.Artifact{"contract"},
		Verifiers: map[fsm.Artifact]fsm.Verifier{"contract": fsm.Existence{}},
	}}
	handed := []fsm.Stage{{
		ID: "spec", Produces: []fsm.Artifact{"contract"},
		Verifiers: map[fsm.Artifact]fsm.Verifier{"contract": fsm.Existence{Handover: true}},
	}}

	if fsm.Fingerprint(committed) == fsm.Fingerprint(handed) {
		t.Error("handing an artifact to the store is a different obligation and must change the fingerprint")
	}
}

// TestAPathStillDoesNotChangeTheFingerprint pins the other half, so the two
// fields do not quietly converge.
func TestAPathStillDoesNotChangeTheFingerprint(t *testing.T) {
	bare := []fsm.Stage{{
		ID: "verify", ProducesForHuman: []fsm.Artifact{"dod_checked"},
		Verifiers: map[fsm.Artifact]fsm.Verifier{"dod_checked": fsm.Existence{}},
	}}
	withPath := []fsm.Stage{{
		ID: "verify", ProducesForHuman: []fsm.Artifact{"dod_checked"},
		Verifiers: map[fsm.Artifact]fsm.Verifier{"dod_checked": fsm.Existence{Path: "reports/"}},
	}}

	if fsm.Fingerprint(bare) != fsm.Fingerprint(withPath) {
		t.Error("a path says where a delivery is looked for, not what is owed (ADR-0070)")
	}
}

// TestHandoverDescribesItself covers what the brief shows the agent.
func TestHandoverDescribesItself(t *testing.T) {
	if got := (fsm.Existence{Handover: true}).Describe(); got != "handed over to Luna" {
		t.Errorf("Describe: got %q, want %q", got, "handed over to Luna")
	}
}

// TestHandoverProvesNothingMoreThanExistence pins that the destination does not
// raise the scope: a row in the store is no more proven than a file was.
func TestHandoverProvesNothingMoreThanExistence(t *testing.T) {
	if got := (fsm.Existence{Handover: true}).Proves(); got != fsm.ScopeExistence {
		t.Errorf("Proves: got %q, want %q", got, fsm.ScopeExistence)
	}
}

// TestACommandAndAHandoverAreRefused: a command runs over the commit, and an
// artifact handed to Luna is not in it.
func TestACommandAndAHandoverAreRefused(t *testing.T) {
	_, err := fsm.ParseStage(`
id       = "spec"
role     = "specifier"
requires = ["approach"]
produces = ["contract"]

[verify.contract]
run      = "make check"
scope    = "targeted"
handover = "store"
`, "spec.toml")
	if err == nil {
		t.Fatal("a command over an artifact that is not committed must be refused")
	}
	if !strings.Contains(err.Error(), "not in it") {
		t.Errorf("the refusal must say why, got %q", err)
	}
}

// TestAPathAndAHandoverAreRefused: a path is where it lives in the commit.
func TestAPathAndAHandoverAreRefused(t *testing.T) {
	_, err := fsm.ParseStage(`
id       = "qa"
role     = "qa"
requires = ["ci_green"]
produces_for_human = ["qa_report"]

[verify.qa_report]
kind     = "existence"
path     = "reports/"
handover = "store"
`, "qa.toml")
	if err == nil {
		t.Fatal("a path on an artifact that is not committed must be refused")
	}
	if !strings.Contains(err.Error(), "not committed") {
		t.Errorf("the refusal must say why, got %q", err)
	}
}

// TestAnUnknownHandoverDestinationIsRefused stops a typo from parsing as "not the
// store" and silently putting the artifact back in the commit.
func TestAnUnknownHandoverDestinationIsRefused(t *testing.T) {
	_, err := fsm.ParseStage(`
id       = "spec"
role     = "specifier"
requires = ["approach"]
produces = ["contract"]

[verify.contract]
kind     = "existence"
handover = "stoer"
`, "spec.toml")
	if err == nil {
		t.Fatal("an unknown handover destination must be refused, not ignored")
	}
	if !strings.Contains(err.Error(), "stoer") {
		t.Errorf("the refusal must name what was written, got %q", err)
	}
}

// TestAHandoverParsesIntoTheVerifier is the happy path through the loader.
func TestAHandoverParsesIntoTheVerifier(t *testing.T) {
	stage, err := fsm.ParseStage(`
id       = "spec"
role     = "specifier"
requires = ["approach"]
produces = ["contract"]

[verify.contract]
kind     = "existence"
handover = "store"
`, "spec.toml")
	if err != nil {
		t.Fatalf("parsing: %v", err)
	}

	existence, ok := fsm.VerifierFor(stage, "contract").(fsm.Existence)
	if !ok {
		t.Fatalf("the verifier must be an existence check, got %T", fsm.VerifierFor(stage, "contract"))
	}
	if !existence.Handover {
		t.Error("handover = \"store\" must reach the verifier")
	}
}
