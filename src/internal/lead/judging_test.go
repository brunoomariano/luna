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
