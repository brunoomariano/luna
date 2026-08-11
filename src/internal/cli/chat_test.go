package cli

import (
	"bufio"
	"errors"
	"strings"
	"testing"
)

// scriptedInterpreter answers with whatever the test queued, and records what it
// was shown. Named rather than inline because it stands in for the model, which
// is the one thing this loop must not need in order to be tested.
type scriptedInterpreter struct {
	intents []Intent
	err     error

	saidToInterpret []string
	stateShown      []string
	phrased         [][]string
}

func (s *scriptedInterpreter) Interpret(said, state string) (Intent, error) {
	s.saidToInterpret = append(s.saidToInterpret, said)
	s.stateShown = append(s.stateShown, state)

	if s.err != nil {
		return Intent{}, s.err
	}
	if len(s.intents) == 0 {
		return Intent{Reply: "nothing queued"}, nil
	}

	next := s.intents[0]
	s.intents = s.intents[1:]
	return next, nil
}

func (s *scriptedInterpreter) Phrase(_ string, command []string, output string) (string, error) {
	s.phrased = append(s.phrased, command)
	return "answered: " + strings.TrimSpace(output), nil
}

// chatting runs a conversation over scripted input and returns what was printed.
func chatting(t *testing.T, h *harness, interpreter Interpreter, confirm func(*bufio.Scanner, string) bool, said ...string) string {
	t.Helper()

	var out strings.Builder
	env := h.env
	env.Out = &out
	env.In = strings.NewReader(strings.Join(append(said, "exit"), "\n") + "\n")

	if err := Chat(env, interpreter, confirm); err != nil {
		t.Fatalf("chatting: %v", err)
	}
	return out.String()
}

// TestApprovingAGateAsksFirst is the one exception ADR-0043 carves out.
//
// Approving is not a command that happens to write: it is the statement "a human
// looked", recorded indistinguishably from the person having read the artifact. A
// layer approving on its own reading is a model saying a human approved.
func TestApprovingAGateAsksFirst(t *testing.T) {
	h := newHarness(t)
	h.mustRun(t, "task", "new", "LUNA-1")

	asked := ""
	interpreter := &scriptedInterpreter{intents: []Intent{
		{Command: []string{"gate", "approve", "LUNA-1"}},
	}}

	chatting(t, h, interpreter, func(_ *bufio.Scanner, command string) bool {
		asked = command
		return false // the person says no
	}, "approve it")

	if asked == "" {
		t.Fatal("approving must ask before it happens")
	}
	if !strings.Contains(asked, "gate approve LUNA-1") {
		t.Errorf("the person must see the real command, got %q", asked)
	}
	// Refused means nothing ran, so nothing was phrased.
	if len(interpreter.phrased) != 0 {
		t.Error("a refused approval must not run")
	}
}

// TestARefusedApprovalLeavesTheGateAlone covers the log, which is what matters.
func TestARefusedApprovalLeavesTheGateAlone(t *testing.T) {
	h := newHarness(t)
	h.mustRun(t, "task", "new", "LUNA-1")

	before, err := h.env.Store.Events("LUNA-1")
	if err != nil {
		t.Fatalf("reading: %v", err)
	}

	chatting(t, h, &scriptedInterpreter{intents: []Intent{
		{Command: []string{"gate", "approve", "LUNA-1"}},
	}}, func(*bufio.Scanner, string) bool { return false }, "approve it")

	after, err := h.env.Store.Events("LUNA-1")
	if err != nil {
		t.Fatalf("reading: %v", err)
	}
	if len(after) != len(before) {
		t.Errorf("a refused approval must not reach the log, got %d events", len(after)-len(before))
	}
}

// TestEverythingElseRunsWithoutAsking covers the other side of the decision.
//
// The log is append-only and a misread intent costs a wasted run; confirming
// everything turns a conversation into a form.
func TestEverythingElseRunsWithoutAsking(t *testing.T) {
	h := newHarness(t)
	h.mustRun(t, "task", "new", "LUNA-1", "--profile", "nightly")

	asked := false
	interpreter := &scriptedInterpreter{intents: []Intent{
		{Command: []string{"run", "LUNA-1", "--dry-run"}},
	}}

	chatting(t, h, interpreter, func(*bufio.Scanner, string) bool { asked = true; return true }, "run it")

	if asked {
		t.Error("only approving a gate asks; everything else runs")
	}
	if len(interpreter.phrased) != 1 {
		t.Errorf("the command must have run, got %d", len(interpreter.phrased))
	}
}

// TestNeedsConfirmationNamesOnlyApproval covers the rule directly, since it is
// the one place the layer's authority is deliberately limited.
func TestNeedsConfirmationNamesOnlyApproval(t *testing.T) {
	cases := map[bool][][]string{
		true: {
			{"gate", "approve", "LUNA-1"},
		},
		false: {
			{"gate", "reject", "LUNA-1"},
			{"gate", "adjust", "LUNA-1"},
			{"gate", "show", "LUNA-1"},
			{"run", "LUNA-1"},
			{"unblock", "LUNA-1"},
			{"task", "show", "LUNA-1"},
			{"gates"},
			{},
		},
	}

	for want, commands := range cases {
		for _, command := range commands {
			if got := (Intent{Command: command}).NeedsConfirmation(); got != want {
				t.Errorf("%v: want confirmation=%v, got %v", command, want, got)
			}
		}
	}
}

// TestTheLayerSeesWhatLunaReports covers how it reads state.
//
// It reads what a person at a terminal would read. Giving it the store directly
// would hand it authority nobody at a terminal has, which is what makes the
// boundary checkable rather than a matter of prompt discipline.
func TestTheLayerSeesWhatLunaReports(t *testing.T) {
	h := newHarness(t)
	h.mustRun(t, "task", "new", "LUNA-1")

	interpreter := &scriptedInterpreter{intents: []Intent{{Reply: "ok"}}}
	chatting(t, h, interpreter, func(*bufio.Scanner, string) bool { return true }, "how are things")

	if len(interpreter.stateShown) != 1 {
		t.Fatalf("want the state shown once, got %d", len(interpreter.stateShown))
	}
	// It is the machine-readable form, which is the contract the layer reads
	// rather than prose meant for a person (ADR-0043).
	if !strings.Contains(interpreter.stateShown[0], `"waiting"`) {
		t.Errorf("want the structured view, got %q", interpreter.stateShown[0])
	}
}

// TestAnAnswerWithNoCommandIsJustSaid covers the interpreter answering from what
// it already knows, without a round trip.
func TestAnAnswerWithNoCommandIsJustSaid(t *testing.T) {
	h := newHarness(t)

	out := chatting(t, h, &scriptedInterpreter{intents: []Intent{
		{Reply: "nothing is waiting"},
	}}, func(*bufio.Scanner, string) bool { return true }, "anything pending?")

	if !strings.Contains(out, "nothing is waiting") {
		t.Errorf("want the reply, got %q", out)
	}
}

// TestARefusedCommandIsAnAnswerNotACrash covers what happens when Luna says no.
//
// The interpreter phrases a refusal the same way it phrases a result; hiding it
// would leave the person told nothing.
func TestARefusedCommandIsAnAnswerNotACrash(t *testing.T) {
	h := newHarness(t)

	interpreter := &scriptedInterpreter{intents: []Intent{
		{Command: []string{"task", "show", "never-created"}},
	}}
	out := chatting(t, h, interpreter, func(*bufio.Scanner, string) bool { return true }, "how is never-created")

	if len(interpreter.phrased) != 1 {
		t.Error("a refusal is still an answer to phrase")
	}
	if !strings.Contains(out, "never-created") {
		t.Errorf("the person must hear what went wrong, got %q", out)
	}
}

// TestAFailedTurnDoesNotEndTheConversation covers the interpreter itself failing.
//
// Dropping someone out of the conversation for one misread sentence is worse than
// telling them what went wrong.
func TestAFailedTurnDoesNotEndTheConversation(t *testing.T) {
	h := newHarness(t)

	interpreter := &scriptedInterpreter{err: errors.New("could not read that")}
	out := chatting(t, h, interpreter, func(*bufio.Scanner, string) bool { return true }, "gibberish", "more gibberish")

	if !strings.Contains(out, "could not read that") {
		t.Errorf("the person must hear what failed, got %q", out)
	}
	// Both turns were attempted: the first failure did not end the session.
	if len(interpreter.saidToInterpret) != 2 {
		t.Errorf("want both turns attempted, got %d", len(interpreter.saidToInterpret))
	}
}

// TestChatNeedsAnInterpreter covers Luna hosting no model.
//
// Saying so beats pretending to work: a chat with no interpreter would read every
// line and answer nothing.
func TestChatNeedsAnInterpreter(t *testing.T) {
	h := newHarness(t)

	err := h.run(t, "chat")

	if err == nil {
		t.Fatal("chat without an interpreter must say so")
	}
	if !strings.Contains(err.Error(), "interpreter") {
		t.Errorf("the error should name what is missing, got %v", err)
	}
}

// TestBlankLinesAreIgnored covers someone pressing enter.
func TestBlankLinesAreIgnored(t *testing.T) {
	h := newHarness(t)

	interpreter := &scriptedInterpreter{intents: []Intent{{Reply: "ok"}}}
	chatting(t, h, interpreter, func(*bufio.Scanner, string) bool { return true }, "", "  ", "something")

	if len(interpreter.saidToInterpret) != 1 {
		t.Errorf("blank lines are not turns, got %d", len(interpreter.saidToInterpret))
	}
}

// TestChatTakesNoArguments covers the surface of the command itself.
func TestChatTakesNoArguments(t *testing.T) {
	h := newHarness(t)
	h.env.Interpret = &scriptedInterpreter{}

	if err := h.run(t, "chat", "something"); err == nil {
		t.Error("chat takes no arguments and must say so")
	}
}

// TestChatConfirmsThroughTheTerminal covers the prompt a person actually sees.
//
// The confirmation is the whole of the layer's limit, so what it asks and how it
// reads the answer are worth pinning: anything but an explicit yes leaves the
// gate alone.
func TestChatConfirmsThroughTheTerminal(t *testing.T) {
	cases := map[string]bool{
		"y": true, "yes": true, "Y": true, "YES": true,
		"n": false, "no": false, "": false, "sure": false,
	}

	for answer, want := range cases {
		h := newHarness(t)
		h.mustRun(t, "task", "new", "LUNA-1")
		h.env.Interpret = &scriptedInterpreter{intents: []Intent{
			{Command: []string{"gate", "approve", "LUNA-1"}},
		}}

		var out strings.Builder
		env := h.env
		env.Out = &out
		env.In = strings.NewReader("approve it\n" + answer + "\nexit\n")

		if err := chatCommand(env, nil); err != nil {
			t.Fatalf("%q: %v", answer, err)
		}

		asked := strings.Contains(out.String(), "gate approve LUNA-1")
		if !asked {
			t.Errorf("%q: the person must be shown the command", answer)
		}
		left := strings.Contains(out.String(), "left alone")
		if left == want {
			t.Errorf("%q: want approved=%v, got left alone=%v", answer, want, left)
		}
	}
}

// TestChatNeedsSomethingToReadFrom covers a headless run.
//
// Failing loudly beats blocking forever on a terminal nobody is at.
func TestChatNeedsSomethingToReadFrom(t *testing.T) {
	h := newHarness(t)
	env := h.env
	env.In = nil

	if err := Chat(env, &scriptedInterpreter{}, func(*bufio.Scanner, string) bool { return true }); err == nil {
		t.Error("chat with no input must say so rather than hang")
	}
}
