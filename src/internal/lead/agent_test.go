package lead

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

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

// TestConductingAStageIsNotBoundedByTheQuestionTimeout separates two uses of one
// boundary that need very different amounts of time.
//
// `Ask` bounds a question: judging a gate, answering a ceiling. Two minutes is
// right there — a model that has not answered is not about to, and somebody is
// waiting. But `luna lead` puts the same `Ask` behind the lead *conducting a
// stage*: starting an agent, waiting for it to work, reporting what it produced.
// That is the shape `turn_budget` already describes, and it defaults to hours.
//
// Measured on TALLY-4: `setup` closed, and the next turn died with "claude did
// not answer within 2m0s" while the agent was still working.
func TestConductingAStageIsNotBoundedByTheQuestionTimeout(t *testing.T) {
	var got time.Duration
	agent := &Agent{
		Ask: func(ctx context.Context, _ string) (string, error) {
			deadline, ok := ctx.Deadline()
			if ok {
				got = time.Until(deadline)
			}
			return "carried out", nil
		},
		Knob:   fsm.Knob(9),
		Budget: 90 * time.Minute,
	}

	if _, err := agent.Conduct(context.Background(), fsm.Order{Kind: fsm.OrderRun, Stage: "build"}); err != nil {
		t.Fatalf("conducting: %v", err)
	}

	if got < time.Hour {
		t.Errorf("conducting a stage got %s to work in — the question timeout, not the "+
			"stage budget", got)
	}
}

// TestTheBriefSaysWhatDeliveredTakes closes a gap a real run walked straight
// into.
//
// The brief said `--delivered <what it produced>`, which reads as an invitation
// to describe the work. On TALLY-4 the lead passed a sentence — "BRIEFING.md —
// intake briefing for the --avg flag; kind=feature (justified against…" — and
// Luna refused it, correctly, as a stage that owed `briefing` and `kind` and
// delivered neither.
//
// The order already answers this: it carries `produces=briefing,kind`. What was
// missing was the brief saying that is the list, verbatim.
func TestTheBriefSaysWhatDeliveredTakes(t *testing.T) {
	brief := Brief(AutonomyDecide)

	if !strings.Contains(brief, "produces") {
		t.Error("the brief does not tell the lead where the delivered names come from")
	}
	for _, want := range []string{"verbatim", "not a description"} {
		if !strings.Contains(brief, want) {
			t.Errorf("the brief leaves %q open, which is how a sentence got passed as a "+
				"list of artifacts:\n%s", want, brief)
		}
	}
}

// TestTheBriefNamesTheCommandThatStartsTheAgent closes the other half of the
// gap `luna work` fills.
//
// The brief has always said "the agent you start does the work". Naming no way
// to start one left the lead to do it with its own tools, which is what happened
// on TALLY-4 — three stages conducted by hand, nothing recorded as spend, and a
// gate whose artifact was never handed over.
func TestTheBriefNamesTheCommandThatStartsTheAgent(t *testing.T) {
	brief := Brief(AutonomyDecide)

	if !strings.Contains(brief, "luna work") {
		t.Errorf("the brief tells the lead to start an agent and does not say how:\n%s", brief)
	}
	if !strings.Contains(brief, "The agent you start does the work") {
		t.Error("the rule the command serves went missing")
	}
}

// TestTheLoopDoesNotReportAStageWorkAlreadyClosed keeps the brief in step with
// what the commands do.
//
// `luna work` records the evidence its verifiers produced, which means it closes
// the stage. The brief still had `luna done` after it, so the lead ran a command
// that could only fail — "no running stage to finish" — and read the failure as
// a missing transition. Measured on TALLY-5, where the lead stopped and
// escalated rather than reaching for `luna run`, which was the right call about
// the wrong problem.
//
// `luna done` stays in the brief for what it is still for: a stage whose
// contract asks for nothing a command can prove, reported by hand.
func TestTheLoopDoesNotReportAStageWorkAlreadyClosed(t *testing.T) {
	brief := Brief(AutonomyDecide)

	work := strings.Index(brief, "luna work")
	done := strings.Index(brief, "luna done")
	if work < 0 {
		t.Fatal("the brief lost the command that starts the agent")
	}
	if done > 0 && done < work {
		t.Error("the brief still tells the lead to report before it works the stage")
	}
	if !strings.Contains(brief, "closes the stage") {
		t.Errorf("the brief does not say that work closes the stage, which is what "+
			"made the lead run `luna done` into a refusal:\n%s", brief)
	}
}
