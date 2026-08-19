package agent

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// fakeHarness is a script standing in for a real harness: it writes a canned
// reply on stdout and records the argument list it was given.
//
// A script rather than an inline stub because the thing under test is how Luna
// builds and runs a command line. A fake that skipped the process would test
// the struct literal and not the transport.
type fakeHarness struct {
	dir string
}

func newFakeHarness(t *testing.T, reply string, exit int) fakeHarness {
	t.Helper()
	dir := t.TempDir()

	// The script records argv and stdin so a test can assert what the agent was
	// actually asked, then prints the reply the harness would have printed.
	script := "#!/bin/sh\n" +
		"printf '%s\\n' \"$@\" > " + filepath.Join(dir, "argv") + "\n" +
		"cat > " + filepath.Join(dir, "stdin") + "\n" +
		"cat <<'REPLY'\n" + reply + "\nREPLY\n" +
		"exit " + itoa(exit) + "\n"

	path := filepath.Join(dir, "fake-harness")
	if err := os.WriteFile(path, []byte(script), 0o755); err != nil {
		t.Fatalf("writing the fake harness: %v", err)
	}
	return fakeHarness{dir: dir}
}

func itoa(n int) string {
	if n == 0 {
		return "0"
	}
	return string(rune('0' + n))
}

func (f fakeHarness) path() string { return filepath.Join(f.dir, "fake-harness") }
func (f fakeHarness) argv(t *testing.T) string {
	t.Helper()
	body, err := os.ReadFile(filepath.Join(f.dir, "argv"))
	if err != nil {
		t.Fatalf("the harness recorded no arguments: %v", err)
	}
	return string(body)
}

func (f fakeHarness) stdin(t *testing.T) string {
	t.Helper()
	body, err := os.ReadFile(filepath.Join(f.dir, "stdin"))
	if err != nil {
		t.Fatalf("the harness recorded no stdin: %v", err)
	}
	return string(body)
}

// success is the shape a real harness returns, trimmed to the fields Luna reads.
const success = `{"result":"done","session_id":"s-1","is_error":false,"subtype":"success",
"num_turns":3,"total_cost_usd":0.25,
"usage":{"input_tokens":10,"output_tokens":20,"cache_read_input_tokens":30,"cache_creation_input_tokens":40},
"modelUsage":{"claude-opus-5":{"inputTokens":10}}}`

// TestRunReportsWhatTheAgentSaidAndWhatItCost is the whole reason this package
// replaced the terminal transport: the answer and the bill arrive together, in
// one parse, with nothing read off a screen.
func TestRunReportsWhatTheAgentSaidAndWhatItCost(t *testing.T) {
	fake := newFakeHarness(t, success, 0)
	h := Harness{Sandbox: "/bin/sh", Binary: fake.path()}

	got, err := h.Run(context.Background(), Call{Kind: "claude", Dir: t.TempDir(), Prompt: "build it"})
	if err != nil {
		t.Fatalf("Run: %v", err)
	}

	if got.Text != "done" {
		t.Errorf("want the agent's reply %q, got %q", "done", got.Text)
	}
	if got.Session != "s-1" {
		t.Errorf("want session %q, got %q", "s-1", got.Session)
	}
	if got.Turns != 3 {
		t.Errorf("want 3 turns, got %d", got.Turns)
	}
	if got.Usage.CostUSD != 0.25 {
		t.Errorf("want cost 0.25, got %v", got.Usage.CostUSD)
	}
	if got.Usage.Model != "claude-opus-5" {
		t.Errorf("want the model that answered, got %q", got.Usage.Model)
	}
	if got.Elapsed <= 0 {
		t.Error("want a measured duration, got zero")
	}
}

// TestTotalCountsCachedTokens covers the field that decides whether the fresh
// versus live comparison is honest. Leaving cache reads out would make a resumed
// session look free when it is billed.
func TestTotalCountsCachedTokens(t *testing.T) {
	u := Usage{InputTokens: 1, OutputTokens: 2, CacheRead: 4, CacheWrite: 8}
	if got := u.Total(); got != 15 {
		t.Errorf("want every token counted (15), got %d", got)
	}
}

// TestTheAgentIsNeverStartedWithoutASandbox is containment as a refusal rather
// than an intention. A blank field must not become "run it uncontained".
func TestTheAgentIsNeverStartedWithoutASandbox(t *testing.T) {
	fake := newFakeHarness(t, success, 0)
	h := Harness{Binary: fake.path()} // no Sandbox

	_, err := h.Run(context.Background(), Call{Kind: "claude", Dir: t.TempDir(), Prompt: "go"})
	if err == nil {
		t.Fatal("want a refusal without a sandbox, got a run")
	}
	if !strings.Contains(err.Error(), "sandbox") {
		t.Errorf("want the refusal to name the sandbox, got %q", err)
	}
	if _, statErr := os.Stat(filepath.Join(fake.dir, "argv")); statErr == nil {
		t.Error("the harness ran despite the refusal")
	}
}

// TestAnUnknownHarnessIsRefusedRatherThanGuessed covers why the table is closed:
// a flag another harness silently ignores produces an ungated agent and a report
// that it was gated.
func TestAnUnknownHarnessIsRefusedRatherThanGuessed(t *testing.T) {
	h := Harness{Sandbox: "/bin/sh"}

	_, err := h.Run(context.Background(), Call{Kind: "gpt-cli", Dir: t.TempDir()})
	if !errors.Is(err, ErrNoHarness) {
		t.Fatalf("want ErrNoHarness, got %v", err)
	}
	if !strings.Contains(err.Error(), "gpt-cli") {
		t.Errorf("want the refusal to name the harness asked for, got %q", err)
	}
}

// TestALiveCallWithoutASessionIsRefused covers the one way `live` can be wrong.
// Without the session the harness would start a fresh conversation and report
// success, so the stage would silently lose the context it asked to keep.
func TestALiveCallWithoutASessionIsRefused(t *testing.T) {
	fake := newFakeHarness(t, success, 0)
	h := Harness{Sandbox: "/bin/sh", Binary: fake.path()}

	_, err := h.Run(context.Background(), Call{Kind: "claude", Dir: t.TempDir(), Context: Live})
	if err == nil {
		t.Fatal("want a refusal for a live call with no session, got a run")
	}
	if !strings.Contains(err.Error(), "session") {
		t.Errorf("want the refusal to name the missing session, got %q", err)
	}
}

// TestDeniedToolsReachTheCommandLine is the gating that replaced a brief asking
// the model nicely. The tool is removed from the request, so there is nothing
// left to talk it out of.
func TestDeniedToolsReachTheCommandLine(t *testing.T) {
	fake := newFakeHarness(t, success, 0)
	h := Harness{Sandbox: "/bin/sh", Binary: fake.path()}

	_, err := h.Run(context.Background(), Call{
		Kind: "claude", Dir: t.TempDir(), Prompt: "review it",
		Deny: []string{"Edit", "Write"},
	})
	if err != nil {
		t.Fatalf("Run: %v", err)
	}

	argv := fake.argv(t)
	for _, want := range []string{"--disallowedTools", "Edit", "Write"} {
		if !strings.Contains(argv, want) {
			t.Errorf("want %q on the command line, got:\n%s", want, argv)
		}
	}
}

// TestALiveCallResumesTheNamedSession pins the mechanism behind `context =
// "live"`: continuing costs a flag and the session id, nothing more.
func TestALiveCallResumesTheNamedSession(t *testing.T) {
	fake := newFakeHarness(t, success, 0)
	h := Harness{Sandbox: "/bin/sh", Binary: fake.path()}

	_, err := h.Run(context.Background(), Call{
		Kind: "claude", Dir: t.TempDir(), Prompt: "carry on",
		Context: Live, Session: "s-99",
	})
	if err != nil {
		t.Fatalf("Run: %v", err)
	}

	argv := fake.argv(t)
	if !strings.Contains(argv, "--resume") || !strings.Contains(argv, "s-99") {
		t.Errorf("want the session resumed on the command line, got:\n%s", argv)
	}
}

// TestAFreshCallResumesNothing is the inverse, and the one that would fail
// silently: a stray --resume would continue somebody else's conversation and
// still look like a working stage.
func TestAFreshCallResumesNothing(t *testing.T) {
	fake := newFakeHarness(t, success, 0)
	h := Harness{Sandbox: "/bin/sh", Binary: fake.path()}

	_, err := h.Run(context.Background(), Call{
		Kind: "claude", Dir: t.TempDir(), Prompt: "start",
		Context: Fresh, Session: "s-should-be-ignored",
	})
	if err != nil {
		t.Fatalf("Run: %v", err)
	}

	if argv := fake.argv(t); strings.Contains(argv, "--resume") {
		t.Errorf("a fresh call resumed a session:\n%s", argv)
	}
}

// TestThePromptTravelsOnStdin covers the reason it is not an argument: a brief
// carrying a contract runs to thousands of characters, and an argument list has
// a limit a long stage would find on its own.
func TestThePromptTravelsOnStdin(t *testing.T) {
	fake := newFakeHarness(t, success, 0)
	h := Harness{Sandbox: "/bin/sh", Binary: fake.path()}

	brief := strings.Repeat("the contract says a great deal. ", 500)
	_, err := h.Run(context.Background(), Call{Kind: "claude", Dir: t.TempDir(), Prompt: brief})
	if err != nil {
		t.Fatalf("Run: %v", err)
	}

	if got := fake.stdin(t); !strings.Contains(got, "the contract says") {
		t.Error("the brief did not reach the agent on stdin")
	}
	if argv := fake.argv(t); strings.Contains(argv, "the contract says") {
		t.Error("the brief was passed as an argument, which a long one would overflow")
	}
}

// TestAnAgentReportedFailureIsAnError covers the harness's own error channel.
// The process exits zero and says it failed inside the JSON, so reading the exit
// code alone would record a failure as a delivery.
func TestAnAgentReportedFailureIsAnError(t *testing.T) {
	const failed = `{"result":"the tool was not available","is_error":true,` +
		`"subtype":"error_during_execution","session_id":"s-2","total_cost_usd":0.01}`

	fake := newFakeHarness(t, failed, 0) // exit 0, failure in the body
	h := Harness{Sandbox: "/bin/sh", Binary: fake.path()}

	got, err := h.Run(context.Background(), Call{Kind: "claude", Dir: t.TempDir(), Prompt: "go"})
	if err == nil {
		t.Fatal("want an error when the agent reports failure, got success")
	}
	if !strings.Contains(err.Error(), "error_during_execution") {
		t.Errorf("want the reported reason, got %q", err)
	}
	// The cost is still real and still worth recording: a failed call is billed.
	if got.Usage.CostUSD != 0.01 {
		t.Errorf("want the failed call's cost kept, got %v", got.Usage.CostUSD)
	}
}

// TestUnreadableOutputNamesWhatCameBack covers the harness that prints something
// other than the agreed shape — a banner, a stack trace, an upgrade notice.
func TestUnreadableOutputNamesWhatCameBack(t *testing.T) {
	fake := newFakeHarness(t, "not json at all", 0)
	h := Harness{Sandbox: "/bin/sh", Binary: fake.path()}

	_, err := h.Run(context.Background(), Call{Kind: "claude", Dir: t.TempDir(), Prompt: "go"})
	if err == nil {
		t.Fatal("want an error for unreadable output, got success")
	}
	if !strings.Contains(err.Error(), "not json at all") {
		t.Errorf("want the error to carry what came back, got %q", err)
	}
}

// TestABudgetBoundsTheCall covers the ceiling that replaced the turn budget the
// terminal transport needed. A harness that never returns must not hold a stage
// forever.
func TestABudgetBoundsTheCall(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "slow-harness")
	if err := os.WriteFile(path, []byte("#!/bin/sh\nsleep 30\n"), 0o755); err != nil {
		t.Fatalf("writing the slow harness: %v", err)
	}
	h := Harness{Sandbox: "/bin/sh", Binary: path}

	started := time.Now()
	_, err := h.Run(context.Background(), Call{
		Kind: "claude", Dir: dir, Prompt: "go", Budget: 200 * time.Millisecond,
	})
	if err == nil {
		t.Fatal("want the budget to end the call, got success")
	}
	if elapsed := time.Since(started); elapsed > 10*time.Second {
		t.Errorf("the budget did not bound the call: took %s", elapsed)
	}
}

// TestTheHarnessKindNamesTheBinary covers the production path, where nothing
// overrides the executable: the kind is the command, so a role saying "claude"
// runs claude and not whatever a test last set.
func TestTheHarnessKindNamesTheBinary(t *testing.T) {
	h := Harness{Sandbox: "/bin/sh"} // no Binary override

	spec, binary, err := h.resolve(Call{Kind: "claude"})
	if err != nil {
		t.Fatalf("resolve: %v", err)
	}
	if binary != "claude" {
		t.Errorf("want the kind to name the binary, got %q", binary)
	}
	if spec.binary != "claude" {
		t.Errorf("want the table entry for claude, got %q", spec.binary)
	}
}

// TestSilenceIsAnErrorRatherThanAnEmptyDelivery covers the harness that exits
// zero having printed nothing. Reading that as an empty success would close a
// stage on an agent that never ran.
func TestSilenceIsAnErrorRatherThanAnEmptyDelivery(t *testing.T) {
	fake := newFakeHarness(t, "", 0)
	h := Harness{Sandbox: "/bin/sh", Binary: fake.path()}

	_, err := h.Run(context.Background(), Call{Kind: "claude", Dir: t.TempDir(), Prompt: "go"})
	if err == nil {
		t.Fatal("want an error when the harness says nothing, got success")
	}
	if !strings.Contains(err.Error(), "nothing") {
		t.Errorf("want the error to say the harness returned nothing, got %q", err)
	}
}

// TestAMissingSandboxIsASetupFailure separates "not installed" from "the call
// went wrong". Retrying a binary that is not there only spends the retry budget
// on a machine that will keep not having it.
func TestAMissingSandboxIsASetupFailure(t *testing.T) {
	fake := newFakeHarness(t, success, 0)
	h := Harness{Sandbox: "/nonexistent/ai-jail", Binary: fake.path()}

	_, err := h.Run(context.Background(), Call{Kind: "claude", Dir: t.TempDir(), Prompt: "go"})
	if !errors.Is(err, ErrNoHarness) {
		t.Fatalf("want ErrNoHarness for a missing sandbox, got %v", err)
	}
	if !strings.Contains(err.Error(), "ai-jail") {
		t.Errorf("want the refusal to name the sandbox it looked for, got %q", err)
	}
}

// TestTheRoleReachesTheAgentAsASystemPrompt covers how a role is carried now.
// The brief used to be typed into a session; it is an argument the harness
// appends to its own system prompt.
func TestTheRoleReachesTheAgentAsASystemPrompt(t *testing.T) {
	fake := newFakeHarness(t, success, 0)
	h := Harness{Sandbox: "/bin/sh", Binary: fake.path()}

	_, err := h.Run(context.Background(), Call{
		Kind: "claude", Dir: t.TempDir(), Prompt: "review it",
		System: "You review. You report findings; you do not edit.",
	})
	if err != nil {
		t.Fatalf("Run: %v", err)
	}

	argv := fake.argv(t)
	if !strings.Contains(argv, "--append-system-prompt") {
		t.Errorf("want the role appended to the system prompt, got:\n%s", argv)
	}
	if !strings.Contains(argv, "you do not edit") {
		t.Errorf("want the brief itself on the command line, got:\n%s", argv)
	}
}

// TestLongDiagnosticsKeepTheirEnd covers which half of a harness's stderr is
// worth reporting. The reason is at the end; the head is the banner.
func TestLongDiagnosticsKeepTheirEnd(t *testing.T) {
	long := strings.Repeat("banner ", 200) + "the actual reason"
	if got := tail(long); !strings.Contains(got, "the actual reason") {
		t.Error("the tail dropped the end, which is where the reason is")
	}
	if got := tail(long); len(got) > 500 {
		t.Errorf("want the diagnostics trimmed, got %d bytes", len(got))
	}
	if got := tail("short"); got != "short" {
		t.Errorf("want a short message left whole, got %q", got)
	}
}

// TestOneModelIsNamedAndTwoAreNot covers the field's honesty rule: it says what
// answered, and half an answer would be worse than none.
func TestOneModelIsNamedAndTwoAreNot(t *testing.T) {
	type entry = struct {
		InputTokens int `json:"inputTokens"`
	}

	if got := oneModel(map[string]entry{"claude-opus-5": {}}); got != "claude-opus-5" {
		t.Errorf("want the single model named, got %q", got)
	}
	if got := oneModel(map[string]entry{"a": {}, "b": {}}); got != "" {
		t.Errorf("want no model named when two answered, got %q", got)
	}
	if got := oneModel(nil); got != "" {
		t.Errorf("want no model named when none is reported, got %q", got)
	}
}

// TestCanGateAnswersFromTheClosedTable covers the question a role's
// configuration asks before a task runs: can Luna actually withhold what this
// role must not have?
//
// An unlisted harness answers false rather than being guessed at. Guessing fails
// open — a flag another harness silently ignores produces an ungated agent and a
// report that it was gated.
func TestCanGateAnswersFromTheClosedTable(t *testing.T) {
	if !CanGate("claude") {
		t.Error("claude is in the table and must be gateable")
	}
	if CanGate("gpt-cli") {
		t.Error("an unlisted harness must not be reported as gateable")
	}
	if CanGate("") {
		t.Error("an unnamed harness must not be reported as gateable")
	}
}
