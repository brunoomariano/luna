package fsm

import (
	"strings"
	"testing"
)

// TestAShortfallSaysWhyRatherThanWhat is the block that cost somebody a manual
// investigation.
//
// A stage that never earned an artifact because a command came back non-zero and
// one that simply did not produce the thing were reported as the same block:
// "declared [a b] and did not deliver [b]". That names what is absent and never
// why, so whoever read it went and ran the suite by hand in a parallel worktree
// to find out — which is where they discovered the check had failed on the
// machine rather than on the work.
//
// The evidence was there the whole time. `askAgain` absorbs it before blocking;
// it just never said any of it.
func TestAShortfallSaysWhyRatherThanWhat(t *testing.T) {
	state := atStage(t, KindChore, "build")
	// The node proves every owed artifact and delivers only what passed, so a
	// failed check arrives as evidence for an artifact that is not in Delivered.
	failing := map[Artifact]Evidence{
		"tests_green": {
			Scope: ScopeTargeted, Verdict: VerdictFailed,
			Command: "make test", ExitCode: 2,
			Detail: "make: *** No rule to make target 'dist/index.js'",
		},
	}

	// Spend the retry budget: a shortfall asks again before it blocks.
	for range 4 {
		state, _ = Reduce(state, Complete{Delivered: []Artifact{"code"}, Evidence: failing})
		if state.Status == StatusBlocked {
			break
		}
	}

	if state.Status != StatusBlocked {
		t.Fatalf("the retry budget never ran out, so this measures nothing: %q", state.Status)
	}
	// A check that ran and failed is `failed-check`, not `contract`: one is fixed
	// by making the work right and the other by making the machine right, and a
	// person who cannot tell them apart pays for the wrong one.
	if state.BlockedBy != BlockCheck {
		t.Errorf("a failed check blocked as %q", state.BlockedBy)
	}
	for _, want := range []string{"tests_green", "make test", "exited 2", "dist/index.js"} {
		if !strings.Contains(state.Blocked, want) {
			t.Errorf("the block does not carry %q:\n%s", want, state.Blocked)
		}
	}
}

// TestAShortfallWithNothingToShowStillNamesWhatIsMissing. A stage that produced
// nothing and ran nothing has no output to carry, and the honest answer is the
// contract: it owed these and delivered those.
func TestAShortfallWithNothingToShowStillNamesWhatIsMissing(t *testing.T) {
	state := atStage(t, KindChore, "build")

	for range 4 {
		state, _ = Reduce(state, Complete{Delivered: []Artifact{"code"}})
		if state.Status == StatusBlocked {
			break
		}
	}

	if state.BlockedBy != BlockContract {
		t.Errorf("a stage that ran no check blocked as %q", state.BlockedBy)
	}
	if !strings.Contains(state.Blocked, "tests_green") {
		t.Errorf("the block does not name what is missing:\n%s", state.Blocked)
	}
}
