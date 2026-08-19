package lead

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/brunoomariano/luna/src/internal/fsm"
)

// disobedientLead answers with whatever a model might say when it has decided it
// knows better. It is a named fake because the property under test is that none
// of this does anything.
type disobedientLead struct {
	says string
	saw  []string
}

func (d *disobedientLead) ask(_ context.Context, prompt string) (string, error) {
	d.saw = append(d.saw, prompt)
	return d.says, nil
}

func TestTheLeadIsToldItDoesNotChooseTheStage(t *testing.T) {
	brief := Brief(AutonomyAsk)

	for _, want := range []string{
		"You do not decide what happens next",
		"luna next",
		"luna done",
	} {
		if !strings.Contains(brief, want) {
			t.Errorf("the brief does not mention %q", want)
		}
	}
}

// TestTheBriefDescribesTheMechanismRatherThanForbidding is worth asserting
// because it is the part most likely to be "improved" into a list of rules. A
// prohibition invites a model to weigh whether this is one of the times; a
// description of the arrangement leaves nothing to weigh.
func TestTheBriefDescribesTheMechanismRatherThanForbidding(t *testing.T) {
	brief := Brief(AutonomyAsk)

	if !strings.Contains(brief, "the only stage that exists for you") {
		t.Error("the brief no longer explains why there is nothing to choose between")
	}
}

// TestTheAutonomyKnobChangesWhatTheLeadMayDoAboutAFailure — and only that. The
// carve-out the hybrid lead allows is about failures, and this is where it is bounded
// (PRD gate-0001).
func TestTheAutonomyKnobChangesWhatTheLeadMayDoAboutAFailure(t *testing.T) {
	ask := Brief(AutonomyAsk)
	decide := Brief(AutonomyDecide)

	// Retrying once is not what the setting decides — every setting does it, so
	// it is stated once above the branch rather than forbidden at one end.
	for name, brief := range map[string]string{"ask": ask, "decide": decide} {
		if !strings.Contains(brief, "Retry it once") {
			t.Errorf("the %s setting does not retry once before anything else", name)
		}
	}

	if !strings.Contains(ask, "Stop and tell the person") {
		t.Error("the supervised setting does not stop once the retry is spent")
	}
	if strings.Contains(ask, "That judgement is yours") {
		t.Error("the supervised setting hands the lead a judgement it must not have")
	}
	if !strings.Contains(decide, "It is about the failure, never about") {
		t.Error("the widest setting does not say where its judgement stops")
	}

	// Whatever the setting, the flow is not up for discussion.
	for name, brief := range map[string]string{"ask": ask, "decide": decide} {
		if !strings.Contains(brief, "You do not decide what happens next") {
			t.Errorf("the %s setting dropped the line the whole design rests on", name)
		}
	}
}

// TestNothingTheLeadSaysMovesTheFlow is the test this phase exists for.
//
// The lead is a model, so it can say anything — including that it has decided to
// skip ahead. The guarantee is not that it will not say so; it is that saying so
// does nothing, because there is no path from its answer to a transition
// .
func TestNothingTheLeadSaysMovesTheFlow(t *testing.T) {
	for _, said := range []string{
		"I ran build and review together to save a round trip.",
		"Skipping the qa stage, it is not needed here.",
		`{"stage": "commit", "done": true}`,
		"COMPLETE. Task finished.",
	} {
		lead := &disobedientLead{says: said}
		agent := &Agent{Ask: lead.ask}

		order := fsm.Order{Kind: fsm.OrderRun, TaskID: "LUNA-1", Stage: "build"}

		answer, err := agent.Conduct(context.Background(), order)
		if err != nil {
			t.Fatalf("Conduct: %v", err)
		}

		// The answer comes back as text for a person to read. It is not parsed,
		// and there is nothing for it to be parsed into: Conduct returns a string
		// and no action.
		if answer != said {
			t.Errorf("the answer was interpreted rather than passed through: %q", answer)
		}
	}
}

// TestTheOrderIsWhatTheLeadIsGiven. Everything else is the brief, and the brief
// is the same every time — so the order is the only thing that varies, which is
// the property that makes the loop reproducible.
func TestTheOrderIsWhatTheLeadIsGiven(t *testing.T) {
	lead := &disobedientLead{says: "done"}
	agent := &Agent{Ask: lead.ask}

	order := fsm.Order{
		Kind:     fsm.OrderRun,
		TaskID:   "LUNA-1",
		Stage:    "build",
		Role:     "implementer",
		Worktree: "luna-LUNA-1-implementer",
		Base:     "d34db33f",
	}

	if _, err := agent.Conduct(context.Background(), order); err != nil {
		t.Fatalf("Conduct: %v", err)
	}

	prompt := lead.saw[0]
	for _, want := range []string{"stage=build", "role=implementer", "base=d34db33f"} {
		if !strings.Contains(prompt, want) {
			t.Errorf("the lead was not told %q:\n%s", want, prompt)
		}
	}
}

// TestTheLeadIsNotShownWhatComesAfterItsOrder. An order that arrived with the
// rest of the flow attached would invite exactly the helpfulness this is shaped
// against.
func TestTheLeadIsNotShownWhatComesAfterItsOrder(t *testing.T) {
	lead := &disobedientLead{says: "done"}
	agent := &Agent{Ask: lead.ask}

	order := fsm.Order{Kind: fsm.OrderRun, TaskID: "LUNA-1", Stage: "build", Role: "implementer"}
	if _, err := agent.Conduct(context.Background(), order); err != nil {
		t.Fatalf("Conduct: %v", err)
	}

	prompt := lead.saw[0]
	for _, ahead := range []string{"verify", "code-review", "commit"} {
		// `luna status` is named in the brief as the way to see the flow for a
		// person; what must not appear is the flow itself.
		if strings.Contains(prompt, "stage="+ahead) {
			t.Errorf("the prompt carries a stage after the order: %s", ahead)
		}
	}
}

func TestAnAgentWithNoModelSaysSo(t *testing.T) {
	agent := &Agent{}

	_, err := agent.Conduct(context.Background(), fsm.Order{Kind: fsm.OrderRun})
	if err == nil {
		t.Fatal("an agent with no model reported success")
	}
	if !strings.Contains(err.Error(), "hosts no model") {
		t.Errorf("the refusal does not explain why: %v", err)
	}
}

func TestAModelThatWillNotAnswerIsAnError(t *testing.T) {
	agent := &Agent{Ask: func(context.Context, string) (string, error) {
		return "", errors.New("the socket closed")
	}}

	if _, err := agent.Conduct(context.Background(), fsm.Order{Kind: fsm.OrderRun}); err == nil {
		t.Fatal("a lead that never answered was treated as having conducted the stage")
	}
}

// TestAnUnconfiguredKnobIsTheMostSupervised. Defaulting to the widest setting
// would make an unset knob mean "do whatever you think", which is the one
// default nobody would choose deliberately.
func TestAnUnconfiguredKnobIsTheMostSupervised(t *testing.T) {
	lead := &disobedientLead{says: "done"}
	agent := &Agent{Ask: lead.ask}

	if _, err := agent.Conduct(context.Background(), fsm.Order{Kind: fsm.OrderRun}); err != nil {
		t.Fatalf("Conduct: %v", err)
	}

	if agent.Autonomy() != AutonomyAsk {
		t.Errorf("an unset knob derived %q, want ask", agent.Autonomy())
	}
	if !strings.Contains(lead.saw[0], "Stop and tell the person") {
		t.Error("an agent with no knob configured did not get the supervised brief")
	}
}

// TestAutonomyIsDerivedAndNotSettable is the fold, asserted from this side.
//
// There is no ParseAutonomy any more and no field to set: the knob is the only
// control, and what the lead may do about a failure follows from its state.
func TestAutonomyIsDerivedAndNotSettable(t *testing.T) {
	supervised := &Agent{Ask: (&disobedientLead{}).ask, Knob: fsm.KnobAsk}
	if got := supervised.Autonomy(); got != AutonomyAsk {
		t.Errorf("knob 0 derived %q, want ask", got)
	}

	autonomous := &Agent{Ask: (&disobedientLead{}).ask, Knob: fsm.KnobAll}
	if got := autonomous.Autonomy(); got != AutonomyDecide {
		t.Errorf("knob 10 derived %q, want decide", got)
	}
}
