package lead

import (
	"strings"
	"testing"

	"github.com/brunoomariano/luna/src/internal/fsm"
)

// TestTheJudgingBriefCarriesTheThreeMeasuredRules is not a wording test.
//
// Each rule closed a failure that was measured: the same model, same artifact
// and same criteria went from approving a criterion-violating delivery 3/3 to
// rejecting it 3/3 on the instruction alone. A brief that lost one of them would
// pass every other test here and quietly restore the failure.
func TestTheJudgingBriefCarriesTheThreeMeasuredRules(t *testing.T) {
	gate := &fsm.GateSpec{
		Kind:  fsm.GateReviewArtifact,
		Judge: []string{"the JSON output stays a bare array"},
	}

	brief := JudgingBrief(gate, "the artifact says accept", "/tmp/delivered")

	for what, want := range map[string]string{
		"do not trust the artifact's own verdict": "Do not trust the artifact's own verdict",
		"answer criterion by criterion":           "criterion by criterion",
		"quote the line that settles it":          "quote the line",
		"verify rather than believe":              "Verify rather than believe",
		"deferring is a correct answer":           "CANNOT-DECIDE is a correct answer",
	} {
		if !strings.Contains(brief, want) {
			t.Errorf("the brief no longer says %s", what)
		}
	}

	// The criteria themselves have to be in it, or there is nothing to check
	// against.
	if !strings.Contains(brief, "the JSON output stays a bare array") {
		t.Error("the declared criterion did not reach the brief")
	}
	if !strings.Contains(brief, "/tmp/delivered") {
		t.Error("the checkout did not reach the brief, so \"verify\" has no referent")
	}
}

// TestWithNoCheckoutTheBriefDemandsUnsupportedRatherThanTrust covers the case
// the checklist measurement exposed.
//
// An artifact that proves a criterion by citing an irrelevant passing check was
// approved 3/3 with the criterion marked "met". Without a checkout there is no
// way to catch that, so the brief has to say what an unverifiable claim is worth
// rather than leaving the model to decide.
func TestWithNoCheckoutTheBriefDemandsUnsupportedRatherThanTrust(t *testing.T) {
	gate := &fsm.GateSpec{Kind: fsm.GateConfirm, Judge: []string{"a criterion"}}

	brief := JudgingBrief(gate, "", "")

	if !strings.Contains(brief, "UNSUPPORTED") {
		t.Error("with nothing to verify against, the brief does not say a claim is unsupported")
	}
	if strings.Contains(brief, "checked out at") {
		t.Error("the brief points at a checkout that does not exist")
	}
}

// TestOnlyALeadingApprovalIsAnApproval is the parser's whole job.
//
// The dangerous case is the last one: a reply that defers and then says it would
// approve under other circumstances. A parser scanning the whole text for
// "approve" finds it there, and would approve on reasoning that concluded the
// opposite.
func TestOnlyALeadingApprovalIsAnApproval(t *testing.T) {
	cases := map[string]Judgement{
		"APPROVE":                                      JudgedApprove,
		"approve\n\n1. met":                            JudgedApprove,
		"  APPROVE — every criterion is met":           JudgedApprove,
		"REJECT\n\n1. violated":                        JudgedReject,
		"CANNOT-DECIDE":                                JudgedCannotDecide,
		"CANNOT DECIDE\n\nnothing settles criterion 2": JudgedCannotDecide,
		"":                         JudgedCannotDecide,
		"it depends what you mean": JudgedCannotDecide,

		// The one that matters: it leads with a defer and mentions approving in
		// its working.
		"CANNOT-DECIDE\n\nI would approve if criterion 2 held": JudgedCannotDecide,

		// And the mirror: reasoning that mentions rejecting under an approval.
		"APPROVE\n\n2. met — I would reject if it had changed the array": JudgedApprove,
	}

	for said, want := range cases {
		if got := ReadJudgement(said); got != want {
			t.Errorf("%q read as %v, want %v", said, got, want)
		}
	}
}

// TestAnythingUnreadableFallsToAPerson states the default from the other side.
//
// Every path this package cannot conclude from ends at a person, and the zero
// value is what enforces it: a Judgement nobody set is CANNOT-DECIDE, so a bug
// that forgot to assign one cannot approve a gate.
func TestAnythingUnreadableFallsToAPerson(t *testing.T) {
	var unset Judgement

	if unset != JudgedCannotDecide {
		t.Error("the zero judgement is not the one that asks a person")
	}
}

// TestAnApprovalItsOwnBodyRetractsIsNotAnApproval is a regression test written
// from a real harness reply, caught by the swarm bench rather than by reasoning.
//
// Asked to judge a gate whose artifact never arrived, the model opened with
// APPROVE, worked both criteria to UNSUPPORTED, and corrected itself in the body.
// It then named the exact danger: "if this gate's harness reads only the first
// line, it just got a false approval from me." It had — this parser read the
// first line and nothing else.
//
// The failure this restores is the one the PRD measured and killed: a model
// answering on tone before it has worked the problem.
func TestAnApprovalItsOwnBodyRetractsIsNotAnApproval(t *testing.T) {
	// Quoted from the run, trimmed to what matters.
	real := `APPROVE

**Criterion 1 — The plan names the file: UNSUPPORTED**

I have no artifact. There is no plan text in front of me.

**Correction to my first line: the answer is CANNOT-DECIDE, not APPROVE.**

Both criteria are unsupported, so this goes to a person.`

	if got := ReadJudgement(real); got != JudgedCannotDecide {
		t.Errorf("a reply that retracts its own approval read as %v, want cannot-decide", got)
	}

	for _, retraction := range []string{
		"APPROVE\n\nactually, CANNOT-DECIDE — nothing settles criterion 2",
		"APPROVE\n\nCorrection to my first line: I cannot check this",
		"APPROVE\n\nmy first line was wrong",
		"APPROVE\n\nDisregard my first line; the artifact is missing",
	} {
		if got := ReadJudgement(retraction); got != JudgedCannotDecide {
			t.Errorf("%q read as %v, want cannot-decide", retraction, got)
		}
	}
}

// TestAWorkedApprovalIsStillAnApproval guards the other direction.
//
// The retraction check must not turn every reasoned approval into a defer: a
// model that approves and then explains itself has approved, and a parser that
// found doubt in ordinary prose would make the knob unusable.
func TestAWorkedApprovalIsStillAnApproval(t *testing.T) {
	for _, worked := range []string{
		"APPROVE\n\n1. met — §2 names calc.py and subtract\n2. met — nothing extra promised",
		"APPROVE\n\nBoth criteria are met. I checked each against the plan.",
		"APPROVE\n\n1. met. 2. met — I would reject if it had promised more.",
	} {
		if got := ReadJudgement(worked); got != JudgedApprove {
			t.Errorf("a worked approval read as %v, want approve:\n%s", got, worked)
		}
	}
}

// TestAGateWithNoArtifactSaysSo covers the situation that produced the
// retraction, at its source.
//
// A `confirm` gate carries no payload — only `review-artifact` does — so criteria
// declared on one arrive with nothing to read. A model handed criteria and no
// artifact reaches for a verdict anyway; naming the situation is what makes
// CANNOT-DECIDE the first line rather than the third paragraph.
func TestAGateWithNoArtifactSaysSo(t *testing.T) {
	gate := &fsm.GateSpec{Kind: fsm.GateConfirm, Judge: []string{"a criterion"}}

	brief := JudgingBrief(gate, "", "")

	if !strings.Contains(brief, "no artifact attached") {
		t.Error("the brief does not say the artifact is missing")
	}
	if !strings.Contains(brief, "CANNOT-DECIDE. Say so on the first line") {
		t.Error("the brief does not tell it what the answer is when there is nothing to read")
	}

	// And with one attached, that instruction must be absent — it would tell a
	// model to defer on a gate it can actually answer.
	withArtifact := JudgingBrief(gate, "the plan changes calc.py", "")
	if strings.Contains(withArtifact, "no artifact attached") {
		t.Error("a gate carrying an artifact was told there was none")
	}
}

// TestCriteriaAboutTheArtifactAreAnsweredByReadingIt is the fix for a gate that
// could not be approved by anybody but a person, at any autonomy.
//
// The brief tells the lead not to believe a claim, which is right: the rule
// exists because a stage once approved a defect it had itself named. But with no
// checkout it said *every* criterion whose evidence is a claim is UNSUPPORTED —
// and the shipped gate's criteria are all judgements about the text of an
// artifact that is attached to the brief. So all four came back unsupported, the
// lead always declined, and `nightly` stopped at the one gate Luna ships.
//
// A criterion the gate declares as readable is answered by reading the artifact.
// One it does not is still unsupported without something to run.
func TestCriteriaAboutTheArtifactAreAnsweredByReadingIt(t *testing.T) {
	gate := &fsm.GateSpec{
		Kind:          fsm.GateReviewArtifact,
		Artifact:      "contract",
		Judge:         []string{"the contract states what is forbidden", "the suite passes"},
		ReadableJudge: []string{"the contract states what is forbidden"},
	}

	brief := JudgingBrief(gate, "the contract says: X is forbidden", "")

	if !strings.Contains(brief, "answered by reading the artifact") {
		t.Errorf("a readable criterion must be named as answerable, brief was:\n%s", brief)
	}
	if !strings.Contains(brief, "the contract states what is forbidden") {
		t.Error("the readable criterion must still be listed")
	}
	if !strings.Contains(brief, "the suite passes") {
		t.Error("the criterion needing proof must still be listed")
	}
}

// TestAGateWithNoReadableCriteriaStillDemandsProof keeps the escape hatch shut.
//
// Declaring nothing readable must leave the old behaviour exactly as it was: a
// gate that says nothing about its criteria gets the rule that protects it.
func TestAGateWithNoReadableCriteriaStillDemandsProof(t *testing.T) {
	gate := &fsm.GateSpec{Kind: fsm.GateConfirm, Judge: []string{"the suite passes"}}

	brief := JudgingBrief(gate, "", "")

	if strings.Contains(brief, "answered by reading the artifact") {
		t.Errorf("a gate declaring nothing readable must not gain a way to approve on prose:\n%s", brief)
	}
	if !strings.Contains(brief, "UNSUPPORTED") {
		t.Error("the rule that protects an unverifiable criterion must still be there")
	}
}
