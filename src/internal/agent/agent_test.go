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
		// The sandbox's own flags come first and the sandbox consumes them. The
		// fake does the same, because `sh` would otherwise read a leading
		// `--network` as one of its own options and refuse to run at all — which
		// is a fact about /bin/sh, not about what Luna built.
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

// fakeSandbox stands in for `ai-jail`: it takes its own flags, then execs what
// follows. `/bin/sh` was used for this and could not — Luna passes the sandbox
// `--network`, and sh reads a leading long option as one of its own and refuses
// to start. That made every one of these tests assert that the sandbox is
// invoked with no flags of its own, which is exactly the assumption that let a
// networkless agent ship.
func fakeSandbox(t *testing.T) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "fake-sandbox")
	// `--env` and `--map` take a value, and the fake has to consume it the way
	// ai-jail does: stopping at the first argument that is not a flag would leave
	// the value in front of the command and exec the variable's *name*.
	script := "#!/bin/sh\n" +
		"while [ \"${1#--}\" != \"$1\" ]; do\n" +
		"  case \"$1\" in --env|--map) shift 2 ;; *) shift ;; esac\n" +
		"done\n" +
		"exec \"$@\"\n"
	if err := os.WriteFile(path, []byte(script), 0o755); err != nil {
		t.Fatalf("writing the fake sandbox: %v", err)
	}
	return path
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

// codexSuccess is the JSONL shape measured from codex-cli 0.151.0. Cached input
// is included in input_tokens on that wire, so the parser must separate it
// before the common Spend type adds the columns together.
const codexSuccess = `{"type":"thread.started","thread_id":"01a059dd-3645-7d01-ba07-f5587c11480e"}
{"type":"turn.started"}
{"type":"item.completed","item":{"id":"item_0","type":"agent_message","text":"done"}}
{"type":"turn.completed","usage":{"input_tokens":120,"cached_input_tokens":80,"cache_write_input_tokens":10,"output_tokens":7,"reasoning_output_tokens":3}}`

// TestRunReportsWhatTheAgentSaidAndWhatItCost is the whole reason this package
// replaced the terminal transport: the answer and the bill arrive together, in
// one parse, with nothing read off a screen.
func TestRunReportsWhatTheAgentSaidAndWhatItCost(t *testing.T) {
	fake := newFakeHarness(t, success, 0)
	h := Harness{Sandbox: fakeSandbox(t), Binary: fake.path()}

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

// TestCodexReportsTheReplySessionAndUsage pins the second transport to output
// from the installed CLI rather than treating --agent as a display-only flag.
func TestCodexReportsTheReplySessionAndUsage(t *testing.T) {
	fake := newFakeHarness(t, codexSuccess, 0)
	h := Harness{Sandbox: fakeSandbox(t), Binary: fake.path()}

	got, err := h.Run(context.Background(), Call{Kind: "codex", Dir: t.TempDir(), Prompt: "build it"})
	if err != nil {
		t.Fatalf("Run: %v", err)
	}

	if got.Text != "done" || got.Session != "01a059dd-3645-7d01-ba07-f5587c11480e" {
		t.Errorf("the Codex result was not preserved: %+v", got)
	}
	if got.Turns != 1 {
		t.Errorf("want one completed Codex turn, got %d", got.Turns)
	}
	if got.Usage.InputTokens != 30 || got.Usage.CacheRead != 80 || got.Usage.CacheWrite != 10 ||
		got.Usage.OutputTokens != 7 {
		t.Errorf("Codex usage counted a cached token twice: %+v", got.Usage)
	}
	if got.Usage.CostUSD != 0 || got.Usage.Model != "" {
		t.Errorf("Codex did not report cost or model, so Luna must not invent them: %+v", got.Usage)
	}
	if got.Usage.CostReported {
		t.Error("Codex's JSONL has no USD cost, but Luna marked one as reported")
	}
}

func TestCodexRefusesAStreamThatDoesNotProveACompletedTurn(t *testing.T) {
	for name, stream := range map[string]string{
		"empty":        "",
		"malformed":    `{"type":"thread.started"`,
		"unfinished":   `{"type":"thread.started","thread_id":"s-1"}`,
		"error":        `{"type":"error","message":"model unavailable"}`,
		"turn failure": `{"type":"turn.failed","error":{"message":"model unavailable"}}`,
	} {
		t.Run(name, func(t *testing.T) {
			_, err := parseCodex([]byte(stream))
			if err == nil {
				t.Fatal("an incomplete Codex stream was accepted")
			}
		})
	}
}

func TestCodexUsageNeverInventsNegativeUncachedInput(t *testing.T) {
	var event codexEvent
	event.Usage.Input = 5
	event.Usage.Cached = 7
	event.Usage.CacheWrite = 3

	if got := codexUsage(event); got.InputTokens != 0 {
		t.Errorf("overlapping Codex counters produced %d uncached tokens", got.InputTokens)
	}
}

// TestCodexGetsTheRoleAndTheWritableSandboxMode covers the two inputs that a
// Claude-shaped adapter would silently lose: Codex has no append-system flag,
// and Luna's outer jail is what makes its no-sandbox mode safe.
func TestCodexGetsTheRoleAndTheWritableSandboxMode(t *testing.T) {
	fake := newFakeHarness(t, codexSuccess, 0)
	h := Harness{Sandbox: fakeSandbox(t), Binary: fake.path()}

	_, err := h.Run(context.Background(), Call{
		Kind: "codex", Dir: t.TempDir(), Prompt: "the task handoff",
		System: "You build and commit the delivery.",
	})
	if err != nil {
		t.Fatalf("Run: %v", err)
	}

	argv := fake.argv(t)
	for _, want := range []string{
		"exec", "--json", "--dangerously-bypass-approvals-and-sandbox",
		"already being orchestrated by Luna", "only luna artifact put/get",
	} {
		if !strings.Contains(argv, want) {
			t.Errorf("Codex did not receive %q:\n%s", want, argv)
		}
	}
	stdin := fake.stdin(t)
	if !strings.Contains(stdin, "You build and commit") || !strings.Contains(stdin, "the task handoff") {
		t.Errorf("Codex did not receive both the role and handoff:\n%s", stdin)
	}
}

// TestACodexReviewerIsReadOnly is the real gating mechanism for Codex. It takes
// no tool names: the pair Edit+Write becomes a read-only sandbox.
func TestACodexReviewerIsReadOnly(t *testing.T) {
	fake := newFakeHarness(t, codexSuccess, 0)
	h := Harness{Sandbox: fakeSandbox(t), Binary: fake.path()}

	_, err := h.Run(context.Background(), Call{
		Kind: "codex", Dir: t.TempDir(), Prompt: "review it", Deny: []string{"Edit", "Write"},
	})
	if err != nil {
		t.Fatalf("Run: %v", err)
	}

	argv := fake.argv(t)
	if !strings.Contains(argv, "read-only") || strings.Contains(argv, "dangerously-bypass") {
		t.Errorf("a Codex reviewer was not made read-only:\n%s", argv)
	}
}

func TestCodexRefusesAPartialWriteDenial(t *testing.T) {
	err := CheckGating("codex", []string{"Edit"})
	if err == nil {
		t.Fatal("Codex denies all writing and cannot honestly deny Edit alone")
	}
	if !strings.Contains(err.Error(), "Edit") || !strings.Contains(err.Error(), "Edit and Write") {
		t.Errorf("the refusal must name the invalid and expected capabilities, got %v", err)
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
	h := Harness{Sandbox: fakeSandbox(t), Binary: fake.path()}

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
	h := Harness{Sandbox: fakeSandbox(t), Binary: fake.path()}

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
	h := Harness{Sandbox: fakeSandbox(t), Binary: fake.path()}

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
	h := Harness{Sandbox: fakeSandbox(t), Binary: fake.path()}

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
	h := Harness{Sandbox: fakeSandbox(t), Binary: fake.path()}

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
	h := Harness{Sandbox: fakeSandbox(t), Binary: fake.path()}

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
	h := Harness{Sandbox: fakeSandbox(t), Binary: fake.path()}

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
	h := Harness{Sandbox: fakeSandbox(t), Binary: fake.path()}

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
	h := Harness{Sandbox: fakeSandbox(t), Binary: fake.path()}

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
	if !CanGate("codex") {
		t.Error("codex is in the table and must be gateable")
	}
	if CanGate("gpt-cli") {
		t.Error("an unlisted harness must not be reported as gateable")
	}
	if CanGate("") {
		t.Error("an unnamed harness must not be reported as gateable")
	}
}

func TestOnlyClaudeReportsUSDSpend(t *testing.T) {
	if !ReportsCost("claude") {
		t.Error("Claude reports total_cost_usd")
	}
	if ReportsCost("codex") {
		t.Error("Codex JSONL reports tokens but no USD cost")
	}
}

func TestKnownAnswersFromTheMeasuredHarnessTable(t *testing.T) {
	if !Known("claude") || !Known("codex") {
		t.Error("both measured harnesses must be selectable")
	}
	if Known("opencode") || Known("") {
		t.Error("an unmeasured or empty harness must be refused")
	}
}

func TestACodexLiveCallResumesTheNamedThread(t *testing.T) {
	fake := newFakeHarness(t, codexSuccess, 0)
	h := Harness{Sandbox: fakeSandbox(t), Binary: fake.path()}

	_, err := h.Run(context.Background(), Call{
		Kind: "codex", Dir: t.TempDir(), Prompt: "carry on",
		Context: Live, Session: "01a059dd-3645-7d01-ba07-f5587c11480e",
	})
	if err != nil {
		t.Fatalf("Run: %v", err)
	}

	argv := fake.argv(t)
	if !strings.Contains(argv, "exec") || !strings.Contains(argv, "resume") ||
		!strings.Contains(argv, "01a059dd-3645-7d01-ba07-f5587c11480e") {
		t.Errorf("the Codex thread was not resumed:\n%s", argv)
	}
}

// TestMemoryWrapsTheCallInsideTheSandbox covers the composition order, which is
// the part that matters: contained first, then remembered. Reversed, a call that
// must not escape could.
func TestMemoryWrapsTheCallInsideTheSandbox(t *testing.T) {
	fake := newFakeHarness(t, success, 0)
	h := Harness{Sandbox: fakeSandbox(t), Binary: fake.path()}

	// The wrapper stands in for ai-memory: it records what it was asked to launch
	// and runs it, which is what the real one does with native arguments.
	// `ai-memory run <harness> <native args...>`: drop the wrapper's own two
	// arguments and run the harness with the rest, byte-for-byte.
	// `ai-memory run --workstream <name> <harness> <native args...>`: drop the
	// wrapper's own four arguments and run the harness with the rest.
	wrapper := filepath.Join(t.TempDir(), "fake-memory")
	script := "#!/bin/sh\necho \"$@\" > \"$AI_MEMORY_ARGV\"\nshift 4\nexec \"" + fake.path() + "\" \"$@\"\n"
	if err := os.WriteFile(wrapper, []byte(script), 0o755); err != nil {
		t.Fatalf("writing the fake wrapper: %v", err)
	}
	original := memoryWrapper
	memoryWrapper = wrapper
	defer func() { memoryWrapper = original }()

	seen := filepath.Join(t.TempDir(), "wrapper-argv")
	t.Setenv("AI_MEMORY_ARGV", seen)

	_, err := h.Run(context.Background(), Call{
		Kind: "claude", Dir: t.TempDir(), Prompt: "go", Workstream: "nightly",
	})
	if err != nil {
		t.Fatalf("Run: %v", err)
	}

	// The harness's own flags survive the wrapper, which forwards native
	// arguments byte-for-byte — so the call is still non-interactive.
	if argv := fake.argv(t); !strings.Contains(argv, "-p") {
		t.Errorf("the wrapper swallowed the harness's non-interactive flag:\n%s", argv)
	}

	// And the workstream reached the wrapper by name. Without it the wrapper falls
	// back to whichever workstream the machine was pointing at, which is the
	// contamination naming one exists to prevent.
	asked, err := os.ReadFile(seen)
	if err != nil {
		t.Fatalf("reading what the wrapper was asked: %v", err)
	}
	if !strings.Contains(string(asked), "--workstream nightly") {
		t.Errorf("the wrapper was not told which workstream: %s", asked)
	}
}

// TestASelectedWorkstreamThatIsMissingIsOpenedOnlyWhenAsked covers the recovery
// and its guard.
//
// Selecting is tried first because that is the ordinary case and costs one
// launch. Creating is the fallback, and only for a task that asked — otherwise a
// typo in a name opens a second ledger instead of stopping the stage. Measured
// against ai-memory 1.32.1, which answers 404 for a name that does not exist and
// 409 for one that does, both before the agent starts.
func TestASelectedWorkstreamThatIsMissingIsOpenedOnlyWhenAsked(t *testing.T) {
	fake := newFakeHarness(t, success, 0)

	// A wrapper that refuses to select and accepts to create, the way the real one
	// does for a workstream that is not there yet.
	wrapper := filepath.Join(t.TempDir(), "fussy-memory")
	script := "#!/bin/sh\n" +
		"echo \"$@\" >> \"$AI_MEMORY_ARGV\"\n" +
		"if [ \"$2\" = \"--workstream\" ]; then\n" +
		"  echo \"server returned 404 Not Found: {\\\"error\\\":\\\"not found: managed workstream '$3'\\\"}\" >&2\n" +
		"  exit 1\n" +
		"fi\n" +
		"shift 4\nexec \"" + fake.path() + "\" \"$@\"\n"
	if err := os.WriteFile(wrapper, []byte(script), 0o755); err != nil {
		t.Fatalf("writing the fake wrapper: %v", err)
	}
	original := memoryWrapper
	memoryWrapper = wrapper
	defer func() { memoryWrapper = original }()

	h := Harness{Sandbox: fakeSandbox(t), Binary: fake.path()}

	// A task that did not ask: the stage stops rather than opening a ledger.
	refused := filepath.Join(t.TempDir(), "refused-argv")
	t.Setenv("AI_MEMORY_ARGV", refused)
	if _, err := h.Run(context.Background(), Call{
		Kind: "claude", Dir: t.TempDir(), Prompt: "go", Workstream: "typo",
	}); err == nil {
		t.Error("a missing workstream nobody asked to create was opened anyway")
	}
	if asked, _ := os.ReadFile(refused); strings.Contains(string(asked), "--new") {
		t.Errorf("a task that did not ask still tried to create one: %s", asked)
	}

	// A task that did: selecting fails, creating follows, the agent runs.
	opened := filepath.Join(t.TempDir(), "opened-argv")
	t.Setenv("AI_MEMORY_ARGV", opened)
	if _, err := h.Run(context.Background(), Call{
		Kind: "claude", Dir: t.TempDir(), Prompt: "go",
		Workstream: "brand-new", MayCreateWorkstream: true,
	}); err != nil {
		t.Fatalf("a task that asked for a new workstream could not open one: %v", err)
	}

	asked, err := os.ReadFile(opened)
	if err != nil {
		t.Fatalf("reading what the wrapper was asked: %v", err)
	}
	// Selected first, created second: the order is what keeps the ordinary case
	// to one launch.
	if !strings.Contains(string(asked), "--workstream brand-new") ||
		!strings.Contains(string(asked), "--new brand-new") {
		t.Errorf("the wrapper was not asked to select and then to create: %s", asked)
	}
}

// TestACallWithNoWorkstreamGetsNoWrapper. Empty means no memory at all, and it
// must not become "whatever workstream this machine was last pointing at".
func TestACallWithNoWorkstreamGetsNoWrapper(t *testing.T) {
	fake := newFakeHarness(t, success, 0)
	h := Harness{Sandbox: fakeSandbox(t), Binary: fake.path()}

	_, err := h.Run(context.Background(), Call{Kind: "claude", Dir: t.TempDir(), Prompt: "go"})
	if err != nil {
		t.Fatalf("Run: %v", err)
	}

	if argv := fake.argv(t); strings.Contains(argv, memoryWrapper) {
		t.Errorf("a call that asked for no memory got the wrapper:\n%s", argv)
	}
}

// TestTheSandboxIsAskedToAllowTheNetwork is a regression test for a stage that
// could never have worked.
//
// The agent is contained by the jail's filesystem boundary, which is what INV-4
// rests on. The network is not part of that boundary: the model is on the other
// side of it, so a harness with no network is a harness that cannot be an agent.
//
// Without the flag, `claude -p` inside the jail starts, opens its TLS bundle and
// blocks forever on a connection it is not allowed to make — no output, no
// error, no exit. Nothing in this suite could see it, because nothing asserted
// what the sandbox was actually asked to do; a real run found it, and the
// default two-hour budget meant it found it slowly.
func TestTheSandboxIsAskedToAllowTheNetwork(t *testing.T) {
	fake := newFakeHarness(t, `{"result":"done","session_id":"s1"}`, 0)
	h := Harness{Sandbox: fake.path(), Binary: "claude"}

	if _, err := h.Run(context.Background(), Call{Kind: "claude", Prompt: "x"}); err != nil {
		t.Fatalf("running: %v", err)
	}

	argv := fake.argv(t)
	if !strings.Contains(argv, "--network") {
		t.Errorf("the sandbox was not asked to allow the network, so the agent could "+
			"never reach a model. argv was:\n%s", argv)
	}

	// And it comes before the harness: a flag after the binary is the harness's
	// argument, not the sandbox's, and `claude --network` is not a thing.
	network := strings.Index(argv, "--network")
	harness := strings.Index(argv, "claude")
	if network > harness {
		t.Errorf("--network must be the sandbox's argument, not the harness's:\n%s", argv)
	}
}

// TestTheSandboxIsAskedToPassWorktreeMetadata is a regression test for five
// stages that billed $3.33 and delivered nothing.
//
// A stage's checkout is a git worktree, and a worktree's `.git` is not a
// directory — it is a one-line pointer into the main repository's
// `.git/worktrees/`, which is outside the jail. Without `--worktree` the jail
// does not carry that metadata across, so every git command the agent runs
// answers `fatal: not a git repository: (null)`.
//
// The agent reads and edits fine, does the work and cannot deliver it. Luna
// then reads the worktree from outside the jail, finds HEAD still on the base,
// and records a delivery at the commit the stage started from. That is TALLY-5:
// five consecutive stages, no error anywhere, no commit anywhere.
//
// The flag is off by default in ai-jail 1.19.0.
func TestTheSandboxIsAskedToPassWorktreeMetadata(t *testing.T) {
	fake := newFakeHarness(t, `{"result":"done","session_id":"s1"}`, 0)
	h := Harness{Sandbox: fake.path(), Binary: "claude"}

	if _, err := h.Run(context.Background(), Call{Kind: "claude", Prompt: "x"}); err != nil {
		t.Fatalf("running: %v", err)
	}

	argv := fake.argv(t)
	if !strings.Contains(argv, "--worktree") {
		t.Errorf("the sandbox was not asked to pass worktree metadata, so the agent's "+
			"git could not see a repository and the stage could not commit. argv was:\n%s", argv)
	}

	// The sandbox's argument, not the harness's: `claude --worktree` is not a
	// thing, and a flag after the binary is silently the wrong one's.
	if strings.Index(argv, "--worktree") > strings.Index(argv, "claude") {
		t.Errorf("--worktree must be the sandbox's argument, not the harness's:\n%s", argv)
	}
}

// TestAStaleSessionFallsBackToAFreshOne keeps a persisted session from turning
// into a broken stage.
//
// A session id now lives in the log, so it can outlive the conversation it names
// — a task resumed days later, a harness that pruned its history. The harness
// answers that with plain text on stdout ("No conversation found with session
// ID: …") rather than JSON, so the reply does not parse and the stage would fail
// for a reason that is nobody's fault.
//
// Starting clean is the honest recovery, and the result says `fresh` so the cost
// column does not claim a resumption that did not happen.
func TestAStaleSessionFallsBackToAFreshOne(t *testing.T) {
	fake := newSessionAwareHarness(t)
	h := Harness{Sandbox: fakeSandbox(t), Binary: fake.path()}

	result, err := h.Run(context.Background(), Call{
		Kind:    "claude",
		Prompt:  "x",
		Context: Live,
		Session: "gone-a-long-time",
	})
	if err != nil {
		t.Fatalf("a stale session is a recovery, not a failure: %v", err)
	}
	if result.Text == "" {
		t.Error("the retry must produce the answer the fresh call gave")
	}

	argv := fake.argvAll(t)
	if strings.Count(argv, "--resume") > 1 {
		t.Errorf("the retry must not ask to resume again:\n%s", argv)
	}
}

func TestCodexCallsALostThreadStale(t *testing.T) {
	err := errors.New("thread/resume failed: no rollout found for thread id 00000000-0000-0000-0000-000000000000")
	if !staleSession(err) {
		t.Error("Codex's measured missing-thread diagnostic must trigger a fresh retry")
	}
}

// sessionAwareHarness refuses a --resume the way the real one does — plain text
// on stdout, exit 1 — and answers a call without one normally.
//
// Measured against claude 2.1.237: a session it no longer has produces
// "No conversation found with session ID: …" and no JSON at all, which is why
// the fallback cannot key off the exit code alone.
type sessionAwareHarness struct{ dir string }

func newSessionAwareHarness(t *testing.T) sessionAwareHarness {
	t.Helper()
	dir := t.TempDir()
	argv := filepath.Join(dir, "argv-all")
	script := "#!/bin/sh\n" +
		"printf '%s\\n' \"$@\" >> " + argv + "\n" +
		"for a in \"$@\"; do\n" +
		"  if [ \"$a\" = \"--resume\" ]; then\n" +
		"    echo 'No conversation found with session ID: gone-a-long-time'\n" +
		"    exit 1\n" +
		"  fi\n" +
		"done\n" +
		"cat >/dev/null\n" +
		`echo '{"result":"done fresh","session_id":"s-new","num_turns":2}'` + "\n"
	path := filepath.Join(dir, "session-aware")
	if err := os.WriteFile(path, []byte(script), 0o755); err != nil {
		t.Fatalf("writing the session-aware harness: %v", err)
	}
	return sessionAwareHarness{dir: dir}
}

func (f sessionAwareHarness) path() string { return filepath.Join(f.dir, "session-aware") }

func (f sessionAwareHarness) argvAll(t *testing.T) string {
	t.Helper()
	body, err := os.ReadFile(filepath.Join(f.dir, "argv-all"))
	if err != nil {
		t.Fatalf("the harness recorded no arguments: %v", err)
	}
	return string(body)
}

// TestAnUncontainedCallNeedsNoSandbox is the runtime half of INV-4's exception.
//
// Every other call is refused without a sandbox, deliberately — "Luna does not
// start an agent outside one". The one stage that reads the sandbox's own
// configuration cannot be run under it, so that refusal has to know the
// difference rather than being turned off globally.
func TestAnUncontainedCallNeedsNoSandbox(t *testing.T) {
	h := Harness{} // no sandbox configured at all

	_, err := h.Run(context.Background(), Call{Kind: "claude", Dir: t.TempDir()})
	if err == nil {
		t.Fatal("a contained call ran with no sandbox configured")
	}
	if !strings.Contains(err.Error(), "sandbox") {
		t.Errorf("the refusal must say what is missing, got %v", err)
	}

	// The exempt call gets past the refusal and fails on the harness instead,
	// which is the next thing missing rather than the same thing again.
	_, err = h.Run(context.Background(), Call{
		Kind: "claude", Dir: t.TempDir(), Uncontained: true,
	})
	if err != nil && strings.Contains(err.Error(), "no sandbox configured") {
		t.Errorf("an uncontained call was refused for want of a sandbox: %v", err)
	}
}

// TestTheSandboxIsAskedToExposeTheSocketDirectory pins what Luna asks for, since
// the handover depends on it and the flag is the only thing that makes it work.
//
// Read-only, and that is the measurement rather than a preference: Landlock
// permits connect() on an inode it can merely see, so the agent reaches the
// socket without being able to write into the directory holding it.
func TestTheSandboxIsAskedToExposeTheSocketDirectory(t *testing.T) {
	with := sandboxArgs(Call{Reachable: "/run/user/1000/luna"})

	var mapped bool
	for i, arg := range with {
		if arg == "--map" && i+1 < len(with) && with[i+1] == "/run/user/1000/luna" {
			mapped = true
		}
		if arg == "--rw-map" {
			t.Error("the socket directory is exposed read-write, and read-only is enough")
		}
	}
	if !mapped {
		t.Errorf("the socket directory is not exposed: %v", with)
	}

	// And a call with nothing to reach asks for nothing, rather than mapping an
	// empty path.
	for _, arg := range sandboxArgs(Call{}) {
		if arg == "--map" {
			t.Error("a call with no socket asked for a mapping anyway")
		}
	}
}

// TestTheSandboxIsAskedToCarryTheWorkstreamEnvironment is a regression test for
// AVG-1's `intake`, the first contained stage of a real task.
//
// The workstream wrapper runs inside the jail, and the jail does not carry the
// host environment across. Without these two names `ai-memory run` starts against
// its own default of 127.0.0.1:49374 with no token and the server answers
// `401 Unauthorized: auth required` — before the agent starts, so the stage burns
// its retries and blocks the task without ever calling a model.
//
// Nothing in this suite could see it: `setup` runs uncontained and inherits the
// environment normally, so the first stage of every task worked and the second
// was the one that died.
func TestTheSandboxIsAskedToCarryTheWorkstreamEnvironment(t *testing.T) {
	fake := newFakeHarness(t, `{"result":"done","session_id":"s1"}`, 0)
	h := Harness{Sandbox: fake.path(), Binary: "claude"}

	call := Call{Kind: "claude", Prompt: "x", Workstream: "w"}
	if _, err := h.Run(context.Background(), call); err != nil {
		t.Fatalf("running: %v", err)
	}

	argv := fake.argv(t)
	for _, name := range memoryEnv {
		// argv comes back one argument per line, so the flag and its value are
		// matched together rather than separately: `--env` somewhere and the name
		// somewhere else would pass while naming a different variable.
		pair := "--env\n" + name
		if !strings.Contains(argv, pair) {
			t.Errorf("the sandbox was not asked to carry %s, so the workstream wrapper "+
				"inside it cannot authenticate. argv was:\n%s", name, argv)
			continue
		}
		// The sandbox's argument, not the wrapper's: after the binary it would be
		// read by ai-memory, which has no such flag.
		if strings.Index(argv, pair) > strings.Index(argv, memoryWrapper) {
			t.Errorf("--env %s must be the sandbox's argument, not the wrapper's:\n%s", name, argv)
		}
	}
}

// TestTheSandboxCarriesTheHandoverSocketVariable is a regression test for what a
// real task did instead of failing.
//
// Luna sets LUNA_ARTIFACT_SOCKET on the process it starts — which is the jail,
// not the agent inside it — and the jail carries no environment across. So every
// contained stage was told to hand its work over through a socket whose name it
// was never given, and `luna artifact put` refused with "LUNA_ARTIFACT_SOCKET is
// not set".
//
// It did not surface as a failure because a capable model routes around it: on
// AVG-1 the intake agent guessed the path from the task and the stage id and got
// through, but not before committing the briefing into `.luna/` as a fallback —
// reintroducing the exact leak the store handover exists to prevent. The plan
// agent spent nine turns on the same search and ran out before delivering any of
// the three artifacts it owed.
//
// The name is forwarded, never the value: argv is world-readable, and the value
// is already in the jail's own environment for it to copy.
func TestTheSandboxCarriesTheHandoverSocketVariable(t *testing.T) {
	const socket = "/run/user/1000/luna/handover/T-1-plan.sock"

	args := sandboxArgs(Call{
		Env:       []string{"LUNA_ARTIFACT_SOCKET=" + socket},
		Reachable: filepath.Dir(socket),
	})

	var carried bool
	for i, arg := range args {
		if arg == "--env" && i+1 < len(args) && args[i+1] == "LUNA_ARTIFACT_SOCKET" {
			carried = true
		}
		if strings.Contains(arg, socket) && arg != filepath.Dir(socket) {
			t.Errorf("the socket path is in argv, where anyone on the machine can read it: %q", arg)
		}
	}
	if !carried {
		t.Errorf("the agent was not told where to hand its work over, so `luna artifact put` "+
			"cannot reach Luna and the stage delivers nothing: %v", args)
	}
}
