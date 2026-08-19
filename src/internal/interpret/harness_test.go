package interpret

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// TestAFencedAnswerIsStillAnAnswer covers the shape models actually produce.
//
// Asking for JSON and getting it wrapped in a markdown fence is common enough
// that refusing it would turn a correct answer into a failed turn.
func TestAFencedAnswerIsStillAnAnswer(t *testing.T) {
	cases := map[string]string{
		"bare":            `{"command":["gates"]}`,
		"fenced":          "```json\n{\"command\":[\"gates\"]}\n```",
		"fenced, no lang": "```\n{\"command\":[\"gates\"]}\n```",
		"padded":          "  \n {\"command\":[\"gates\"]}  \n",
	}

	for name, answer := range cases {
		intent, err := decodeIntent(answer)
		if err != nil {
			t.Errorf("%s: %v", name, err)
			continue
		}
		if len(intent.Command) != 1 || intent.Command[0] != "gates" {
			t.Errorf("%s: want the command, got %+v", name, intent)
		}
	}
}

// TestAReplyIsAValidAnswer covers the interpreter answering without a command.
//
// "Nothing is waiting" needs no round trip, and neither does "I cannot tell which
// task you mean" — which is what the prompt asks for instead of a guessed id.
func TestAReplyIsAValidAnswer(t *testing.T) {
	intent, err := decodeIntent(`{"reply":"nothing is waiting"}`)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if len(intent.Command) != 0 {
		t.Errorf("a reply runs nothing, got %v", intent.Command)
	}
	if intent.Reply != "nothing is waiting" {
		t.Errorf("want the reply, got %q", intent.Reply)
	}
}

// TestAnAnswerThatIsNeitherIsRefused covers the empty object.
//
// A model that answers `{}` has said nothing, and treating that as a valid intent
// would run an empty command or print an empty line.
func TestAnAnswerThatIsNeitherIsRefused(t *testing.T) {
	for _, answer := range []string{`{}`, `{"command":[]}`, `{"reply":""}`} {
		if _, err := decodeIntent(answer); err == nil {
			t.Errorf("%s names neither a command nor a reply and must be refused", answer)
		}
	}
}

// TestProseIsNotACommand covers a model that ignored the instruction.
//
// It must not parse into something runnable — the failure has to be visible as a
// failure, which is what lets Interpret fall back to showing the person what came
// back.
func TestProseIsNotACommand(t *testing.T) {
	if _, err := decodeIntent("Sure! I think you want to approve LUNA-1."); err == nil {
		t.Error("prose must not decode into a command")
	}
}

// TestAnUnsupportedInterpreterIsRefused covers the closed table.
//
// An agent Luna guesses at fails in a way nobody sees until a person is already
// talking to it, so the refusal names what would work instead.
func TestAnUnsupportedInterpreterIsRefused(t *testing.T) {
	_, err := Harness{Agent: "gemini"}.Ask(context.Background(), "anything")

	if err == nil {
		t.Fatal("an agent Luna cannot interpret with must be refused")
	}
	if !strings.Contains(err.Error(), "gemini") {
		t.Errorf("the error should name the agent, got %v", err)
	}
	for _, supported := range interpreters() {
		if !strings.Contains(err.Error(), supported) {
			t.Errorf("the error should list %q, got %v", supported, err)
		}
	}
}

// TestTheInterpretersAreListedInPreferenceOrder covers the message someone reads
// after a typo.
//
// Ranging over a map would list them differently each time, which makes an error
// nobody can match against the documentation.
func TestTheInterpretersAreListedInPreferenceOrder(t *testing.T) {
	want := []string{"claude", "pi", "codex", "opencode"}

	got := interpreters()
	if strings.Join(got, ",") != strings.Join(want, ",") {
		t.Errorf("want the house preference order %v, got %v", want, got)
	}
}

// TestTheDefaultIsTheFirstPreference covers an unconfigured interpreter.
func TestTheDefaultIsTheFirstPreference(t *testing.T) {
	if DefaultInterpreter != interpreters()[0] {
		t.Errorf("the default must be the first preference, got %q", DefaultInterpreter)
	}
	if _, ok := nonInteractive[DefaultInterpreter]; !ok {
		t.Error("the default must be one Luna can actually run")
	}
}

// TestEveryInterpreterHasANonInteractiveMode is the fact this whole design rests
// on, and it was learned from the tools rather than from documentation.
func TestEveryInterpreterHasANonInteractiveMode(t *testing.T) {
	for _, agent := range interpreters() {
		args, ok := nonInteractive[agent]
		if !ok || len(args) == 0 {
			t.Errorf("%q must know how to answer once and exit", agent)
		}
	}
}

// TestTheTurnIsBounded covers the deadline.
//
// A person is waiting, so a model that has not answered is not about to — and an
// unbounded wait is the silent stall INV-5 forbids.
func TestTheTurnIsBounded(t *testing.T) {
	if Timeout <= 0 {
		t.Error("a turn must have a deadline")
	}

	// `false` exits non-zero immediately, which proves the error path reports the
	// harness rather than hanging.
	_, err := Harness{Agent: "claude", Deadline: time.Millisecond}.Ask(context.Background(), "x")
	if err == nil {
		t.Error("a deadline that cannot be met must be reported")
	}
}

// TestThePromptNamesWhatTheModelMayNotDecide covers the instruction that shapes
// the answer.
//
// It is not what enforces the boundary — the command being one a person could
// have typed is — but a prompt that did not say so would invite the
// model to try.
func TestThePromptNamesWhatTheModelMayNotDecide(t *testing.T) {
	prompt := interpretPrompt("approve it", `{"waiting":[]}`)

	for _, want := range []string{
		"which stage runs",   // flow control stays out of the model
		"Never guess a task", // an approval on the wrong task is the worst misread
		"gate approve",       // the commands it may choose from
	} {
		if !strings.Contains(prompt, want) {
			t.Errorf("the prompt should say %q, got:\n%s", want, prompt)
		}
	}
	// It has to carry the state, or the model answers about nothing.
	if !strings.Contains(prompt, `{"waiting":[]}`) {
		t.Error("the prompt must carry what Luna reports")
	}
}

// TestPhrasingIsToldNotToInvent covers the other direction.
func TestPhrasingIsToldNotToInvent(t *testing.T) {
	prompt := phrasePrompt("how is it", []string{"task", "show", "LUNA-1"}, "blocked: no worktree")

	if !strings.Contains(prompt, "Do not invent") {
		t.Error("phrasing must be told to stay with what the output says")
	}
	if !strings.Contains(prompt, "scope matters") {
		t.Error("phrasing must be told that a full check and mere existence differ")
	}
	if !strings.Contains(prompt, "blocked: no worktree") {
		t.Error("the prompt must carry what Luna answered")
	}
}

// TestAHarnessErrorIsReadable covers what a person sees when the model fails.
func TestAHarnessErrorIsReadable(t *testing.T) {
	long := strings.Repeat("x", 500)

	if got := firstLine("first\nsecond\nthird"); got != "first" {
		t.Errorf("want the first line, got %q", got)
	}
	if got := firstLine(long); len(got) != 200 {
		t.Errorf("want a bounded message, got %d chars", len(got))
	}
	if got := firstLine("  padded  "); got != "padded" {
		t.Errorf("want it trimmed, got %q", got)
	}
}

// fakeHarness puts a script on PATH under one of the official names, so the whole
// spawn-and-read path runs without a model and without tokens.
//
// A real subprocess rather than a mocked one: the thing under test is what
// happens when an external program answers, and mocking exec would assert the
// code I wrote rather than the behaviour it has.
func fakeHarness(t *testing.T, answers string) {
	t.Helper()

	dir := t.TempDir()
	script := filepath.Join(dir, "claude")

	// It ignores its arguments and prints the answer: the prompt's content is
	// tested separately, and what matters here is the reading.
	body := "#!/usr/bin/env sh\ncat <<'ANSWER'\n" + answers + "\nANSWER\n"
	if err := os.WriteFile(script, []byte(body), 0o755); err != nil { //nolint:gosec // a test fixture
		t.Fatalf("writing the fake: %v", err)
	}

	t.Setenv("PATH", dir+string(os.PathListSeparator)+os.Getenv("PATH"))
}

// TestInterpretReadsACommandBack covers the whole path: spawn, read, decode.
func TestInterpretReadsACommandBack(t *testing.T) {
	fakeHarness(t, `{"command":["gate","approve","LUNA-1"]}`)

	intent, err := Harness{}.Interpret("approve it", `{"waiting":[]}`)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if strings.Join(intent.Command, " ") != "gate approve LUNA-1" {
		t.Errorf("want the command the harness named, got %v", intent.Command)
	}
}

// TestAModelThatWillNotAnswerInJSONIsStillHeard covers the turn that would
// otherwise be lost.
//
// A model ignoring the instruction is a turn that says so, not a crash: the
// person can rephrase, and being shown what came back is more use than an error
// about JSON.
func TestAModelThatWillNotAnswerInJSONIsStillHeard(t *testing.T) {
	fakeHarness(t, "I think you want to approve LUNA-1, but I am not sure.")

	intent, err := Harness{}.Interpret("approve it", "{}")
	if err != nil {
		t.Fatalf("prose is not an error: %v", err)
	}

	if len(intent.Command) != 0 {
		t.Errorf("prose must not become a command, got %v", intent.Command)
	}
	if !strings.Contains(intent.Reply, "not sure") {
		t.Errorf("the person must hear what came back, got %q", intent.Reply)
	}
}

// TestPhraseAnswersInWords covers the other direction.
func TestPhraseAnswersInWords(t *testing.T) {
	fakeHarness(t, "LUNA-1 is waiting at the contract gate.")

	answer, err := Harness{}.Phrase("how is it", []string{"task", "show", "LUNA-1"}, "awaiting_gate")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if !strings.Contains(answer, "contract gate") {
		t.Errorf("want what the harness said, got %q", answer)
	}
}

// TestAnEmptyInterpretationIsAnError covers the silent-success trap.
//
// This is the bug `opencode --print` was: not a flag opencode has, and instead of
// refusing it printed a banner and exited 0. Nothing errored, the empty answer
// became an empty reply, and the person got a blank line with nothing anywhere
// saying the invocation was wrong.
//
// Note that Phrase does the opposite with silence — it falls back to Luna's own
// output — because there it has something to fall back to. Here it does not.
func TestAnEmptyInterpretationIsAnError(t *testing.T) {
	fakeHarness(t, "")

	_, err := Harness{}.Interpret("what is waiting", "nothing waiting")
	if err == nil {
		t.Fatal("a harness that exits 0 saying nothing has not answered")
	}
	// The message has to name the invocation, because the fix is almost always
	// the argv rather than anything the person typed.
	if !strings.Contains(err.Error(), "--print") {
		t.Errorf("the error should show how the harness was called, got %q", err)
	}
}

// TestAnEmptyPhrasingFallsBackToTheOutput covers a harness that says nothing.
//
// An empty answer is worse than a raw one: the person asked something and would
// be told nothing at all.
func TestAnEmptyPhrasingFallsBackToTheOutput(t *testing.T) {
	fakeHarness(t, "")

	answer, err := Harness{}.Phrase("how is it", []string{"gates"}, "nothing waiting")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if answer != "nothing waiting" {
		t.Errorf("want Luna's own output rather than silence, got %q", answer)
	}
}

// TestAHarnessThatFailsIsReported covers the non-zero exit.
func TestAHarnessThatFailsIsReported(t *testing.T) {
	dir := t.TempDir()
	script := filepath.Join(dir, "claude")
	body := "#!/usr/bin/env sh\necho 'no credentials' >&2\nexit 3\n"
	if err := os.WriteFile(script, []byte(body), 0o755); err != nil { //nolint:gosec // a test fixture
		t.Fatalf("writing the fake: %v", err)
	}
	t.Setenv("PATH", dir+string(os.PathListSeparator)+os.Getenv("PATH"))

	_, err := Harness{}.Interpret("anything", "{}")

	if err == nil {
		t.Fatal("a harness that exits non-zero must be reported")
	}
	if !strings.Contains(err.Error(), "no credentials") {
		t.Errorf("the person must hear why, got %v", err)
	}
}
