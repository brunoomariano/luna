package lead

import (
	"context"
	"strings"
	"testing"

	"github.com/brunoomariano/luna/src/internal/fsm"
)

// TestTheCeilingBriefCarriesTheHistory is why the lead is asked at all.
//
// The decision turns on reading the counters: three rounds that changed nothing
// is a different situation from three that each moved something, and a brief that
// summarised instead of showing them would be asking for an opinion rather than a
// reading.
func TestTheCeilingBriefCarriesTheHistory(t *testing.T) {
	brief := CeilingBrief(fsm.LoopCounters{
		Rounds:       4,
		NoProgress:   2,
		Oscillation:  1,
		Visited:      []fsm.StageID{"build", "verify", "build"},
		LastProgress: "abc1234",
	})

	for what, want := range map[string]string{
		"the round count":     "rounds:      4",
		"rounds that stalled": "no progress: 2",
		"the oscillation":     "oscillation: 1",
		"the path walked":     "build → verify → build",
		"what last changed":   "abc1234",
	} {
		if !strings.Contains(brief, want) {
			t.Errorf("the brief does not carry %s", what)
		}
	}

	// And it has to bound what is being asked. A lead that read this as an
	// invitation to pick a stage would be deciding the flow — the one thing Luna
	// exists to prevent.
	if !strings.Contains(brief, "you may not choose a") {
		t.Error("the brief does not say the flow is not the lead's to decide")
	}
}

// TestAnEmptyLoopStillRendersAReadableBrief covers the counters at zero, which is
// what a ceiling reached on rounds alone looks like.
func TestAnEmptyLoopStillRendersAReadableBrief(t *testing.T) {
	brief := CeilingBrief(fsm.LoopCounters{Rounds: 3})

	if strings.Contains(brief, "path:") {
		t.Error("a loop that visited nothing rendered an empty path line")
	}
	if strings.Contains(brief, "last change:") {
		t.Error("a loop with nothing to compare rendered an empty change line")
	}
	if !strings.Contains(brief, "BLOCK") || !strings.Contains(brief, "ASK") {
		t.Error("the brief does not offer both endings")
	}
}

// TestOnlyALeadingAskReachesAPerson is the parser, and its asymmetry is inverted
// from a gate's on purpose.
//
// At a gate the cheap direction is asking a person. Here asking is what costs: a
// loop nobody assessed and nobody stops is the one that burns tokens forever, so
// anything unreadable blocks (INV-5).
func TestOnlyALeadingAskReachesAPerson(t *testing.T) {
	cases := map[string]CeilingVerdict{
		"ASK":                                   CeilingAsk,
		"ask\n\nit was converging":              CeilingAsk,
		"  ASK — three rounds, each smaller":    CeilingAsk,
		"BLOCK":                                 CeilingBlock,
		"BLOCK\n\nnothing changed for 3 rounds": CeilingBlock,
		"":                                      CeilingBlock,
		"it depends on what you mean":           CeilingBlock,

		// Reasoning that mentions asking is not a request to ask.
		"BLOCK\n\nI would ASK if it had been converging": CeilingBlock,
	}

	for said, want := range cases {
		if got := ReadCeiling(said); got != want {
			t.Errorf("%q read as %v, want %v", said, got, want)
		}
	}
}

// TestTheUnreadableCeilingBlocks states the default from the other side: the zero
// value is the safe ending, so a bug that forgot to assign one cannot leave a
// loop running.
func TestTheUnreadableCeilingBlocks(t *testing.T) {
	var unset CeilingVerdict

	if unset != CeilingBlock {
		t.Error("the zero ceiling verdict is not the one that stops the task")
	}
}

// TestWithNoModelASpentCeilingBlocks is the guard for an unattended run.
//
// `luna run` needs no model. A ceiling reached with none must stop and notify
// rather than carry on, which is what an absent decision produces.
func TestWithNoModelASpentCeilingBlocks(t *testing.T) {
	l := &Lead{}

	if got := l.decideCeiling(context.Background(), fsm.TaskState{}); got != fsm.GateDecisionAbsent {
		t.Errorf("a run with no model recorded %q, want the absent decision that blocks", got)
	}
}

// TestALeadThatCannotBeReachedBlocks covers the model being configured and
// failing.
//
// Blocking is the conservative reading: it stops and notifies rather than
// spending another round on a loop nobody assessed.
func TestALeadThatCannotBeReachedBlocks(t *testing.T) {
	l := &Lead{Ask: func(context.Context, string) (string, error) {
		return "", context.DeadlineExceeded
	}}

	if got := l.decideCeiling(context.Background(), fsm.TaskState{}); got != fsm.GateDecisionAbsent {
		t.Errorf("an unreachable model recorded %q, want the absent decision that blocks", got)
	}
}

// TestTheLeadCanSendASpentCeilingToAPerson is the other half: a loop that was
// converging is a question worth asking, and the lead is what turns that reading
// into the decision the reducer acts on.
func TestTheLeadCanSendASpentCeilingToAPerson(t *testing.T) {
	var sawHistory string
	l := &Lead{Ask: func(_ context.Context, prompt string) (string, error) {
		sawHistory = prompt
		return "ASK\n\nit was getting smaller each round", nil
	}}

	state := fsm.TaskState{Loop: fsm.LoopCounters{Rounds: 3, NoProgress: 0}}

	if got := l.decideCeiling(context.Background(), state); got != fsm.GateDecisionWaited {
		t.Errorf("the lead asked for a person and the decision recorded %q", got)
	}
	if !strings.Contains(sawHistory, "rounds:      3") {
		t.Error("the lead was asked without the history it is supposed to read")
	}
}

// TestALeadThatSaysBlockBlocks completes the pair: the other reading of the same
// history, arriving through the same path.
//
// Together with TestTheLeadCanSendASpentCeilingToAPerson this covers both
// answers the brief offers, which is what makes "the lead decides" more than a
// phrase — a decision with only one reachable outcome is not a decision.
func TestALeadThatSaysBlockBlocks(t *testing.T) {
	l := &Lead{Ask: func(context.Context, string) (string, error) {
		return "BLOCK\n\nthree rounds and nothing changed", nil
	}}

	state := fsm.TaskState{Loop: fsm.LoopCounters{Rounds: 3, NoProgress: 3}}

	if got := l.decideCeiling(context.Background(), state); got != fsm.GateDecisionAbsent {
		t.Errorf("the lead said block and the decision recorded %q, want the absent "+
			"decision the reducer turns into a block", got)
	}
}
