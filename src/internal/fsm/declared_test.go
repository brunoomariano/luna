package fsm

import "testing"

func names(artifacts []Artifact) string {
	var joined string
	for i, a := range artifacts {
		if i > 0 {
			joined += ","
		}
		joined += string(a)
	}
	return joined
}

// TestTheAgentSaysWhatItDelivered is the ordinary case: the line the brief asks
// for, in a commit message with a subject and a body around it.
func TestTheAgentSaysWhatItDelivered(t *testing.T) {
	got := ReadDelivered(`chore(tally-d89): verify — the delivery checked

All four done-when clauses met, make ci green.

Delivered: ci_green, dod_checked
`)

	if want := "ci_green,dod_checked"; names(got) != want {
		t.Errorf("got %q, want %q", names(got), want)
	}
}

// TestTheDeclarationIsFoundWhateverSurroundsIt. A commit message arrives with
// bullets, indentation, quoting and bold, and none of that changes what was
// delivered.
func TestTheDeclarationIsFoundWhateverSurroundsIt(t *testing.T) {
	for _, line := range []string{
		"Delivered: repos",
		"  - **Delivered:** repos",
		"> delivered:repos",
		"DELIVERED:   repos  ",
	} {
		if got := ReadDelivered(line); names(got) != "repos" {
			t.Errorf("%q → %q, want repos", line, names(got))
		}
	}
}

// TestSpacesSeparateTooCoversTheHurriedAgent. The brief asks for commas; an
// agent that writes spaces has still said what it produced, and refusing to read
// that would block a stage over punctuation.
func TestSpacesSeparateTooCoversTheHurriedAgent(t *testing.T) {
	if got := ReadDelivered("Delivered: code tests_green"); names(got) != "code,tests_green" {
		t.Errorf("got %q, want code,tests_green", names(got))
	}
}

// TestNoDeclarationReadsAsNothing is the fallback that keeps this from being a
// new way to fail. Every agent that ran before this existed wrote no such line.
func TestNoDeclarationReadsAsNothing(t *testing.T) {
	if got := ReadDelivered("chore: a commit message with no declaration in it"); got != nil {
		t.Errorf("got %q, want nothing", names(got))
	}
}

// TestProseAboutDeliveryIsNotADeclaration. The keyword has to open the line, or
// a sentence mentioning delivery becomes a list of artifacts — and the contract
// check would then block a stage that did everything right, over a comma in a
// commit message.
func TestProseAboutDeliveryIsNotADeclaration(t *testing.T) {
	for _, prose := range []string{
		"The work was delivered: all of it, in one commit",
		"Everything the contract asked for was delivered: see below",
	} {
		if got := ReadDelivered(prose); got != nil {
			t.Errorf("%q was read as a declaration of %q", prose, names(got))
		}
	}
}
