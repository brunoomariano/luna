package herdr

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"net"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"
)

// fakeServer is a herdr-shaped socket: one exchange per connection, newline
// delimited JSON, string ids.
//
// It answers by method name from a table the test sets. Every behaviour it
// imitates was read off a live herdr 0.8.0 — the tests below exist because each
// one was first learned by being refused by the real thing.
type fakeServer struct {
	mu       sync.Mutex
	replies  map[string]string
	requests []request

	// once holds replies used for a single call, so a test can make the same
	// method fail and then succeed — the startup races are exactly that shape.
	once map[string][]string
}

func newFakeServer(t *testing.T) (*fakeServer, string) {
	t.Helper()

	server := &fakeServer{
		replies: map[string]string{"ping": `{"id":"1","result":{"type":"pong"}}`},
		once:    map[string][]string{},
	}

	path := filepath.Join(t.TempDir(), "h.sock")
	listener, err := net.Listen("unix", path)
	if err != nil {
		t.Fatalf("listening: %v", err)
	}
	t.Cleanup(func() { _ = listener.Close() })

	go func() {
		for {
			conn, err := listener.Accept()
			if err != nil {
				return
			}
			server.serve(conn)
		}
	}()

	return server, path
}

func (f *fakeServer) serve(conn net.Conn) {
	defer func() { _ = conn.Close() }()

	line, err := bufio.NewReader(conn).ReadBytes('\n')
	if err != nil {
		return
	}

	var req request
	if err := json.Unmarshal(line, &req); err != nil {
		return
	}

	f.mu.Lock()
	f.requests = append(f.requests, req)

	var reply string
	var ok bool
	if queued := f.once[req.Method]; len(queued) > 0 {
		reply, ok = queued[0], true
		f.once[req.Method] = queued[1:]
	} else {
		reply, ok = f.replies[req.Method]
	}
	f.mu.Unlock()

	if !ok {
		reply = `{"id":"1","error":{"code":"unknown_method","message":"no such method"}}`
	}
	_, _ = conn.Write([]byte(reply + "\n"))
}

// sent returns every request the server received for a method.
func (f *fakeServer) sent(method string) []request {
	f.mu.Lock()
	defer f.mu.Unlock()

	var found []request
	for _, r := range f.requests {
		if r.Method == method {
			found = append(found, r)
		}
	}
	return found
}

func (f *fakeServer) reply(method, body string) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.replies[method] = body
}

func (f *fakeServer) replyOnce(method string, bodies ...string) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.once[method] = bodies
}

// One line, because the protocol is one JSON object per line: a reply with a
// newline in it is two malformed messages.
const worktreeReply = `{"id":"1","result":{"type":"worktree_created","workspace":{"workspace_id":"w1"},"root_pane":{"pane_id":"w1:p1","cwd":"/tmp/wt"},"worktree":{"path":"/tmp/wt"}}}`

func dialFake(t *testing.T, path string) *Client {
	t.Helper()

	client, err := Dial(path)
	if err != nil {
		t.Fatalf("dialling the fake: %v", err)
	}
	t.Cleanup(func() { _ = client.Close() })
	return client
}

// TestOpenWorktreeReadsHerdrsFieldNames covers the shape of the reply.
//
// herdr answers `workspace_id` and `pane_id`, not `id`. Decoding the wrong field
// yields an empty workspace that fails much later, somewhere unrelated — which is
// why this asserts the values rather than the absence of an error.
func TestOpenWorktreeReadsHerdrsFieldNames(t *testing.T) {
	server, path := newFakeServer(t)
	server.reply("worktree.create", worktreeReply)

	runner := NewRunner(dialFake(t, path), "/repo", time.Minute)
	ws, err := runner.OpenWorktree(context.Background(), WorktreeSpec{TaskID: "LUNA-1"})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if ws.ID != "w1" {
		t.Errorf("want the workspace id, got %q", ws.ID)
	}
	if ws.RootPane != "w1:p1" {
		t.Errorf("want the root pane id, got %q", ws.RootPane)
	}
	if ws.Path != "/tmp/wt" {
		t.Errorf("want the checkout path, got %q", ws.Path)
	}
}

// TestOpenWorktreeSendsTheRepository covers the parameter herdr requires.
//
// A worktree is cut from a repository: herdr refuses the call outside a git work
// tree, so the checkout has to travel with it.
func TestOpenWorktreeSendsTheRepository(t *testing.T) {
	server, path := newFakeServer(t)
	server.reply("worktree.create", worktreeReply)

	runner := NewRunner(dialFake(t, path), "/some/repo", time.Minute)
	if _, err := runner.OpenWorktree(context.Background(), WorktreeSpec{TaskID: "LUNA-1"}); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	sent := server.sent("worktree.create")
	if len(sent) != 1 {
		t.Fatalf("want one create, got %d", len(sent))
	}

	params, _ := json.Marshal(sent[0].Params)
	for _, want := range []string{`"cwd":"/some/repo"`, `"branch":"luna/LUNA-1"`} {
		if !strings.Contains(string(params), want) {
			t.Errorf("want %s in the params, got %s", want, params)
		}
	}
}

// TestOpenWorktreeSendsAnAbsoluteRepository covers what herdr means by "worktree
// path must be absolute": the `cwd`, not the `path`.
//
// `luna run` defaults the repository to ".", which is what a CLI run from inside
// the checkout naturally holds. Every other test here passes "/repo" — already
// absolute — so the whole suite was green while the real command could not open a
// single worktree. Measured against a live server (ADR-0036): an absolute `path`
// with a relative `cwd` is refused, and absolutising `cwd` is what fixes it.
func TestOpenWorktreeSendsAnAbsoluteRepository(t *testing.T) {
	server, path := newFakeServer(t)
	server.reply("worktree.create", worktreeReply)

	runner := NewRunner(dialFake(t, path), ".", time.Minute)
	if _, err := runner.OpenWorktree(context.Background(), WorktreeSpec{TaskID: "LUNA-1"}); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	sent := server.sent("worktree.create")
	if len(sent) != 1 {
		t.Fatalf("want one create, got %d", len(sent))
	}

	var params struct {
		CWD string `json:"cwd"`
	}
	raw, _ := json.Marshal(sent[0].Params)
	if err := json.Unmarshal(raw, &params); err != nil {
		t.Fatalf("reading the params back: %v", err)
	}
	if !filepath.IsAbs(params.CWD) {
		t.Errorf("herdr refuses a relative cwd; got %q", params.CWD)
	}
}

// TestAnExistingWorktreeIsReopened covers running a task twice.
//
// The second `luna run` finds the worktree its first run made. Treating that as a
// failure would make every resumed task block on its first stage.
func TestAnExistingWorktreeIsReopened(t *testing.T) {
	server, path := newFakeServer(t)
	server.reply("worktree.create", `{"id":"1","error":{"code":"worktree_exists","message":"branch already checked out"}}`)
	server.reply("worktree.open", worktreeReply)

	runner := NewRunner(dialFake(t, path), "/repo", time.Minute)
	ws, err := runner.OpenWorktree(context.Background(), WorktreeSpec{TaskID: "LUNA-1"})
	if err != nil {
		t.Fatalf("an existing worktree is resumable, not fatal: %v", err)
	}
	if ws.ID != "w1" {
		t.Errorf("want the reopened workspace, got %q", ws.ID)
	}
	opened := server.sent("worktree.open")
	if len(opened) != 1 {
		t.Fatal("it must fall back to opening the existing one")
	}

	// `open` takes **exactly one** of path or branch, where `create` takes both.
	// Passing create's parameters straight through is refused with
	// `invalid_request`, and a live herdr is the only thing that said so — this
	// test passed for weeks without looking at what it sent (ADR-0036).
	params, err := json.Marshal(opened[0].Params)
	if err != nil {
		t.Fatalf("re-encoding the params: %v", err)
	}
	if strings.Contains(string(params), `"branch"`) {
		t.Errorf("worktree.open was sent a branch alongside a path, which herdr "+
			"refuses: %s", params)
	}
	if !strings.Contains(string(params), `"path"`) {
		t.Errorf("worktree.open was sent neither path nor branch: %s", params)
	}
}

// An agent starts inside the sandbox, through `pane.run` rather than
// `agent.start` (ADR-0069). `agent.start` takes a `kind` from a closed set
// compiled into herdr, so there is nowhere to put a wrapper — measured: a custom
// kind is refused with `unsupported_agent_kind`.

// TestStartAgentWaitsForThePaneToReachItsPrompt covers a race a live herdr showed
// and a fake never would.
//
// A freshly created worktree's pane is not at its shell prompt yet, and herdr
// refuses with `agent_pane_busy`. The identical call succeeds seconds later. Not
// retrying meant every task blocked on its first stage.
func TestStartAgentWaitsForThePaneToReachItsPrompt(t *testing.T) {
	server, path := newFakeServer(t)
	server.replyOnce(
		"agent.list",
		`{"id":"1","result":{"type":"agent_list","agents":[]}}`,
		`{"id":"1","result":{"type":"agent_list","agents":[{"pane_id":"w1:p1"}]}}`,
	)
	server.replyOnce(
		"pane.send_text",
		`{"id":"1","error":{"code":"agent_pane_busy","message":"not an available shell"}}`,
		`{"id":"1","result":{"type":"ok"}}`,
	)

	runner := &socketRunner{client: dialFake(t, path), Repo: "/repo", Settle: time.Minute}
	pane, err := runner.StartAgent(context.Background(), Workspace{RootPane: "w1:p1"}, "claude", "luna-1", nil)
	if err != nil {
		t.Fatalf("a busy pane is a wait, not a failure: %v", err)
	}
	if pane != "w1:p1" {
		t.Errorf("want the pane, got %q", pane)
	}
}

// TestStartAgentRunsTheAgentInsideTheSandbox is the whole point of the change.
//
// Luna talks to herdr over a socket, so an agent started by herdr is a child of
// the herdr server and inherits nothing from Luna's process — wrapping `luna run`
// contained Luna and left the agent free. The wrapper has to be on the agent's
// own command line, and `HERDR_AGENT` is herdr's documented way to keep detecting
// it through one.
func TestStartAgentRunsTheAgentInsideTheSandbox(t *testing.T) {
	server, path := newFakeServer(t)
	// Empty first — nothing to reuse — then present, which is the agent herdr
	// detects a moment after the command runs.
	server.replyOnce(
		"agent.list",
		`{"id":"1","result":{"type":"agent_list","agents":[]}}`,
		`{"id":"1","result":{"type":"agent_list","agents":[{"pane_id":"w1:p1"}]}}`,
	)
	server.reply("pane.send_text", `{"id":"1","result":{"type":"ok"}}`)

	runner := &socketRunner{client: dialFake(t, path), Repo: "/repo", Settle: time.Minute}
	if _, err := runner.StartAgent(context.Background(), Workspace{RootPane: "w1:p1"},
		"claude", "luna-1", []string{"--permission-mode", "bypassPermissions"}); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	sent, _ := json.Marshal(server.sent("pane.send_text")[0].Params)
	for _, want := range []string{"HERDR_AGENT=claude", "ai-jail", "claude", "bypassPermissions"} {
		if !strings.Contains(string(sent), want) {
			t.Errorf("the command does not carry %q: %s", want, sent)
		}
	}
}

// TestAnAgentAlreadyInThePaneIsReused covers a resumed stage.
//
// `worktree.open` returns the pane a stopped run left behind — measured against a
// live herdr, with `already_open: true` and the same pane id. Starting a second
// agent on top of the first would strand the one that was working, and pane.run
// has no name to collide with, so the check has to happen before the start.
func TestAnAgentAlreadyInThePaneIsReused(t *testing.T) {
	server, path := newFakeServer(t)
	server.reply("agent.list", `{"id":"1","result":{"type":"agent_list","agents":[{"pane_id":"w1:p1"}]}}`)

	runner := &socketRunner{client: dialFake(t, path), Repo: "/repo", Settle: time.Minute}
	pane, err := runner.StartAgent(context.Background(), Workspace{RootPane: "w1:p1"}, "claude", "luna-1", nil)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if pane != "w1:p1" {
		t.Errorf("want the pane the agent already runs in, got %q", pane)
	}
	if got := len(server.sent("pane.send_text")); got != 0 {
		t.Errorf("a second agent was started on top of the first (%d runs)", got)
	}
}

// TestPromptSubmitsAndWaitsInOneCall covers the shape herdr requires.
//
// `wait` is an object, not a flag — a bare `true` is refused outright. One call
// rather than two is also what avoids the race between submitting and arming the
// wait, and what produces `agent_prompt_stalled` when nothing reacts.
func TestPromptSubmitsAndWaitsInOneCall(t *testing.T) {
	server, path := newFakeServer(t)
	server.reply("agent.prompt", `{"id":"1","result":{"type":"agent_prompted","agent":{"agent_status":"idle"}}}`)

	runner := &socketRunner{client: dialFake(t, path), Repo: "/repo", Settle: 90 * time.Second}
	status, err := runner.Prompt(context.Background(), "luna-1", "do the thing")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if status != StatusIdle {
		t.Errorf("want the settled status, got %q", status)
	}

	sent := server.sent("agent.prompt")
	if len(sent) != 1 {
		t.Fatalf("want one prompt, got %d", len(sent))
	}

	params, _ := json.Marshal(sent[0].Params)
	body := string(params)
	if !strings.Contains(body, `"wait":{`) {
		t.Errorf("wait must be an object, got %s", body)
	}
	if !strings.Contains(body, `"timeout_ms":90000`) {
		t.Errorf("the profile's budget must reach herdr, got %s", body)
	}
	for _, want := range []string{"idle", "blocked", "done", "unknown"} {
		if !strings.Contains(body, want) {
			t.Errorf("the wait should accept %q, got %s", want, body)
		}
	}
}

// TestPromptWaitsForTheAgentToBecomeReady covers the second startup race.
//
// herdr registers the agent before marking it interactive, so a prompt sent
// straight after `agent.start` is refused with `agent_not_ready`.
func TestPromptWaitsForTheAgentToBecomeReady(t *testing.T) {
	server, path := newFakeServer(t)
	server.replyOnce(
		"agent.prompt",
		`{"id":"1","error":{"code":"agent_not_ready","message":"not an active named agent"}}`,
		`{"id":"1","result":{"type":"agent_prompted","agent":{"agent_status":"done"}}}`,
	)

	runner := &socketRunner{client: dialFake(t, path), Repo: "/repo", Settle: time.Minute}
	status, err := runner.Prompt(context.Background(), "luna-1", "go")
	if err != nil {
		t.Fatalf("an agent still starting is a wait, not a failure: %v", err)
	}
	if status != StatusDone {
		t.Errorf("want the settled status, got %q", status)
	}
	if got := len(server.sent("agent.prompt")); got != 2 {
		t.Errorf("want one retry, got %d attempts", got)
	}
}

// TestAPromptWithNoStatusIsUnknown covers herdr answering without naming a state.
//
// Unknown is the honest reading, and it still triggers verification — it just
// claims nothing about the outcome (ADR-0028).
func TestAPromptWithNoStatusIsUnknown(t *testing.T) {
	server, path := newFakeServer(t)
	server.reply("agent.prompt", `{"id":"1","result":{"type":"agent_prompted"}}`)

	runner := &socketRunner{client: dialFake(t, path), Repo: "/repo", Settle: time.Minute}
	status, err := runner.Prompt(context.Background(), "luna-1", "go")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if status != StatusUnknown {
		t.Errorf("an unnamed state is unknown, got %q", status)
	}
}

// TestEveryRequestCarriesAStringID covers the first thing a live herdr refused.
//
// It rejects an integer id outright. A fake that accepted anything agreed with
// the bug, which is why this asserts on the bytes.
func TestEveryRequestCarriesAStringID(t *testing.T) {
	server, path := newFakeServer(t)
	server.reply("worktree.create", worktreeReply)

	runner := NewRunner(dialFake(t, path), "/repo", time.Minute)
	if _, err := runner.OpenWorktree(context.Background(), WorktreeSpec{TaskID: "LUNA-1"}); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	for _, req := range server.requests {
		if req.ID == "" {
			t.Errorf("%s went out without an id", req.Method)
		}
	}
}

// TestTheRetryPredicatesReadCodesNotMessages covers what decides a retry.
//
// Each predicate matches herdr's error code, so a reworded message cannot quietly
// stop a retry from happening — and an unrelated error carrying the same words in
// its text cannot start one.
func TestTheRetryPredicatesReadCodesNotMessages(t *testing.T) {
	cases := []struct {
		name  string
		match func(error) bool
		code  string
	}{
		{"paneBusy", paneBusy, "agent_pane_busy"},
		{"notReady", notReady, "agent_not_ready"},
	}

	for _, c := range cases {
		if !c.match(&apiError{Code: c.code, Message: "whatever herdr says today"}) {
			t.Errorf("%s must match its own code", c.name)
		}
		if c.match(&apiError{Code: "something_else", Message: c.code}) {
			t.Errorf("%s must not match a message that merely mentions the code", c.name)
		}
		if c.match(errors.New(c.code)) {
			t.Errorf("%s must not match a plain error carrying the text", c.name)
		}
	}
}

// TestRetryStopsWhenTheCallerCancels covers the escape hatch.
//
// A run being cancelled must not be held up waiting between attempts: the person
// asked it to stop, and the startup race is not worth finishing for.
func TestRetryStopsWhenTheCallerCancels(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	attempts := 0
	err := retry(ctx, 5, time.Minute, func() error {
		attempts++
		return &apiError{Code: "agent_pane_busy", Message: "busy"}
	}, paneBusy)

	if !errors.Is(err, context.Canceled) {
		t.Errorf("want the cancellation reported, got %v", err)
	}
	if attempts != 1 {
		t.Errorf("want it to stop after the first attempt, got %d", attempts)
	}
}

// TestRetryGivesUpAndReportsTheLastRefusal covers the bounded end.
//
// A pane still busy after every attempt is a real problem, and the error that
// surfaces has to be herdr's own rather than a generic timeout.
func TestRetryGivesUpAndReportsTheLastRefusal(t *testing.T) {
	attempts := 0
	err := retry(context.Background(), 3, time.Millisecond, func() error {
		attempts++
		return &apiError{Code: "agent_pane_busy", Message: "still not a shell"}
	}, paneBusy)

	if attempts != 3 {
		t.Errorf("want every attempt used, got %d", attempts)
	}
	if err == nil || !strings.Contains(err.Error(), "still not a shell") {
		t.Errorf("want herdr's own refusal, got %v", err)
	}
}

// TestPromptReportsAStallRatherThanRetryingIt covers the boundary between the two
// waits.
//
// `agent_not_ready` is a startup race worth retrying; `agent_prompt_stalled` is
// the agent having been given the prompt and not reacting, which is a fact for
// the lead to act on (ADR-0034).
func TestPromptReportsAStallRatherThanRetryingIt(t *testing.T) {
	server, path := newFakeServer(t)
	server.reply("agent.prompt", `{"id":"1","error":{"code":"agent_prompt_stalled","message":"no observed state change"}}`)

	runner := &socketRunner{client: dialFake(t, path), Repo: "/repo", Settle: time.Minute}
	_, err := runner.Prompt(context.Background(), "luna-1", "go")

	if err == nil {
		t.Fatal("a stall must be reported")
	}
	if !Stalled(err) {
		t.Errorf("want it recognised as a stall, got %v", err)
	}
	if got := len(server.sent("agent.prompt")); got != 1 {
		t.Errorf("a stall is not a startup race and is not retried, got %d attempts", got)
	}
}

// TestAWorktreeThatCannotBeMadeIsReported covers the failure that is not
// resumable.
//
// herdr refusing outside a git work tree is a configuration mistake, not an
// existing worktree, so it must surface rather than fall through to reopening
// something that was never there.
func TestAWorktreeThatCannotBeMadeIsReported(t *testing.T) {
	server, path := newFakeServer(t)
	server.reply("worktree.create", `{"id":"1","error":{"code":"invalid_request","message":"require a workspace inside a Git work tree"}}`)

	runner := NewRunner(dialFake(t, path), "/not/a/repo", time.Minute)
	_, err := runner.OpenWorktree(context.Background(), WorktreeSpec{TaskID: "LUNA-1"})

	if err == nil {
		t.Fatal("a repository that is not one must be reported")
	}
	if !strings.Contains(err.Error(), "LUNA-1") {
		t.Errorf("the error should name the task, got %v", err)
	}
	if len(server.sent("worktree.open")) != 0 {
		t.Error("a real refusal must not fall through to reopening")
	}
}

// TestTheCheckoutPathFallsBackToThePanesDirectory covers the reply that names one
// and not the other.
//
// The path is where verification runs (ADR-0035), so an empty one would run the
// checks in the wrong directory rather than failing outright — the worst shape a
// bug can take.
func TestTheCheckoutPathFallsBackToThePanesDirectory(t *testing.T) {
	server, path := newFakeServer(t)
	server.reply("worktree.create", `{"id":"1","result":{"type":"worktree_created","workspace":{"workspace_id":"w1"},"root_pane":{"pane_id":"w1:p1","cwd":"/from/the/pane"},"worktree":{}}}`)

	runner := NewRunner(dialFake(t, path), "/repo", time.Minute)
	ws, err := runner.OpenWorktree(context.Background(), WorktreeSpec{TaskID: "LUNA-1"})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if ws.Path != "/from/the/pane" {
		t.Errorf("want the pane's directory when the worktree names none, got %q", ws.Path)
	}
}

// TestAPromptFailureThatIsNotARaceIsReported covers the error that must not be
// retried.
//
// A target herdr does not know is a mistake in the call, not a startup race, and
// retrying it would bury the reason under a wait.
func TestAPromptFailureThatIsNotARaceIsReported(t *testing.T) {
	server, path := newFakeServer(t)
	server.reply("agent.prompt", `{"id":"1","error":{"code":"agent_not_found","message":"no such agent"}}`)

	runner := &socketRunner{client: dialFake(t, path), Repo: "/repo", Settle: time.Minute}
	_, err := runner.Prompt(context.Background(), "luna-1", "go")

	if err == nil {
		t.Fatal("an unknown target must be reported")
	}
	if got := len(server.sent("agent.prompt")); got != 1 {
		t.Errorf("a real refusal is not retried, got %d attempts", got)
	}
}

// TestAPromptWithNoBudgetStillHasADeadline covers the zero value.
//
// A runner built without a budget must not wait forever: an unbounded wait is the
// silent stall INV-core-8 forbids.
func TestAPromptWithNoBudgetStillHasADeadline(t *testing.T) {
	server, path := newFakeServer(t)
	server.reply("agent.prompt", `{"id":"1","result":{"type":"agent_prompted","agent":{"agent_status":"idle"}}}`)

	runner := &socketRunner{client: dialFake(t, path), Repo: "/repo"}
	if _, err := runner.Prompt(context.Background(), "luna-1", "go"); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	sent := server.sent("agent.prompt")
	params, _ := json.Marshal(sent[0].Params)
	if strings.Contains(string(params), `"timeout_ms":0`) {
		t.Errorf("an unset budget must still produce a deadline, got %s", params)
	}
}

// TestTheWorktreeFollowsTheHouseNaming covers where a task's checkout lives.
//
// `../wt-<repo>-<id>`, a sibling of the repository. herdr's own default puts it
// under a directory of its own, which is fine for herdr and wrong here: this
// project's worktrees follow one convention regardless of what created them, so
// `ls ../wt-*` finds every one.
func TestTheWorktreeFollowsTheHouseNaming(t *testing.T) {
	server, path := newFakeServer(t)
	server.reply("worktree.create", worktreeReply)

	runner := NewRunner(dialFake(t, path), "/home/someone/repos/api", time.Minute)
	if _, err := runner.OpenWorktree(context.Background(), WorktreeSpec{TaskID: "LUNA-1"}); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	params, _ := json.Marshal(server.sent("worktree.create")[0].Params)
	if !strings.Contains(string(params), `"path":"/home/someone/repos/wt-api-LUNA-1"`) {
		t.Errorf("want the sibling path, got %s", params)
	}
}

// TestTheCheckoutIsASiblingNeverAChild covers the guardrail that matters most.
//
// A checkout nested inside the repository is caught by every recursive walk the
// repository does to itself, and one inside a tool's directory is caught by that
// tool's cleanup.
func TestTheCheckoutIsASiblingNeverAChild(t *testing.T) {
	got, err := checkoutPath("/home/someone/repos/api", "LUNA-1")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if strings.HasPrefix(got, "/home/someone/repos/api/") {
		t.Errorf("the checkout must not live inside the repository, got %q", got)
	}
	if filepath.Dir(got) != "/home/someone/repos" {
		t.Errorf("want a sibling of the repository, got %q", got)
	}
}

// TestARelativeRepositoryStillResolves covers `--repo .`, which is what someone
// running from inside their checkout will type.
func TestARelativeRepositoryStillResolves(t *testing.T) {
	got, err := checkoutPath(".", "LUNA-1")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if !filepath.IsAbs(got) {
		t.Errorf("herdr needs an absolute path, got %q", got)
	}
	if !strings.Contains(filepath.Base(got), "-LUNA-1") {
		t.Errorf("the task id must survive the resolution, got %q", got)
	}
}

// TestHerdrsAnswerWinsOverTheRequestedPath covers reopening.
//
// A worktree made before this convention existed lives somewhere else, and the
// verification has to run where the checkout actually is (ADR-0035) rather than
// where it would be created today.
func TestHerdrsAnswerWinsOverTheRequestedPath(t *testing.T) {
	server, path := newFakeServer(t)
	server.reply("worktree.create", `{"id":"1","error":{"code":"worktree_exists","message":"already checked out"}}`)
	server.reply("worktree.open", `{"id":"1","result":{"type":"worktree_created","workspace":{"workspace_id":"w1"},"root_pane":{"pane_id":"w1:p1"},"worktree":{"path":"/somewhere/older"}}}`)

	runner := NewRunner(dialFake(t, path), "/repo", time.Minute)
	ws, err := runner.OpenWorktree(context.Background(), WorktreeSpec{TaskID: "LUNA-1"})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if ws.Path != "/somewhere/older" {
		t.Errorf("want where the checkout actually is, got %q", ws.Path)
	}
}

// TestARepositoryThatNamesNothingIsRefused covers the guardrail's own edge.
//
// The filesystem root has no name to build a sibling from, and `wt--LUNA-1` at
// the root is not a checkout anyone meant to create. Refusing beats guessing.
func TestARepositoryThatNamesNothingIsRefused(t *testing.T) {
	if _, err := checkoutPath("/", "LUNA-1"); err == nil {
		t.Error("the filesystem root does not name a repository")
	}
}

// TestOpenWorktreeRefusesAnUnusableRepository covers the path resolution failing
// before anything is asked of herdr.
//
// A repository that cannot be resolved is a mistake in the command, and reporting
// it here keeps herdr from being blamed for it.
func TestOpenWorktreeRefusesAnUnusableRepository(t *testing.T) {
	server, path := newFakeServer(t)
	server.reply("worktree.create", worktreeReply)

	runner := NewRunner(dialFake(t, path), "/", time.Minute)
	_, err := runner.OpenWorktree(context.Background(), WorktreeSpec{TaskID: "LUNA-1"})

	if err == nil {
		t.Fatal("an unusable repository must be reported")
	}
	if len(server.sent("worktree.create")) != 0 {
		t.Error("nothing should have been asked of herdr")
	}
}

// TestWithoutTheSandboxLunaRefusesToStartAnAgent is the decision, asserted where
// somebody would meet it.
//
// The alternative is a fallback to an uncontained agent, which is what shipped
// before and is worse than a refusal: the agent runs holding the permissions this
// passes it — `bypassPermissions`, every check off — on the strength of a sandbox
// that is not there, and nothing on screen says so (INV-core-7, ADR-0069).
func TestWithoutTheSandboxLunaRefusesToStartAnAgent(t *testing.T) {
	// An empty PATH is how the sandbox is made absent: `jailed` looks it up, so
	// this is the same condition as a machine that never installed it.
	t.Setenv("PATH", "")

	_, err := jailed("claude", nil)

	if !errors.Is(err, ErrNoSandbox) {
		t.Fatalf("want ErrNoSandbox, got %v", err)
	}
	if !strings.Contains(err.Error(), jailBinary) {
		t.Errorf("the refusal must name what is missing, got %v", err)
	}
}

// TestTheJailedCommandCarriesEveryPart. Each of the three does something, and
// dropping any one fails in a way that looks like something else: no
// HERDR_AGENT and herdr reports no agent at all; no wrapper and the containment
// is gone while the permissions stay; no args and the agent stops to ask.
func TestTheJailedCommandCarriesEveryPart(t *testing.T) {
	command, err := jailed("claude", []string{"--permission-mode", "bypassPermissions"})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	want := "HERDR_AGENT=claude " + jailBinary + " claude --permission-mode bypassPermissions"
	if command != want {
		t.Errorf("the command line is wrong:\n got %s\nwant %s", command, want)
	}
}

// TestAnAgentThatNeverAppearsIsReported. herdr detects an agent by what is on
// the screen, so one that fails to boot leaves the pane at a shell prompt and
// herdr reporting nothing. Prompting into that is refused with
// `agent_not_found`, several seconds later and with no clue why — so the wait
// gives up here and names the pane instead.
func TestAnAgentThatNeverAppearsIsReported(t *testing.T) {
	server, path := newFakeServer(t)
	server.reply("agent.list", `{"id":"1","result":{"type":"agent_list","agents":[]}}`)
	server.reply("pane.send_text", `{"id":"1","result":{"type":"ok"}}`)

	runner := &socketRunner{client: dialFake(t, path), Repo: "/repo", Settle: time.Minute}
	ctx, cancel := context.WithTimeout(context.Background(), 250*time.Millisecond)
	defer cancel()

	_, err := runner.StartAgent(ctx, Workspace{RootPane: "w1:p1"}, "claude", "luna-1", nil)

	if err == nil {
		t.Fatal("an agent that never appeared must be reported")
	}
	if !strings.Contains(err.Error(), "w1:p1") {
		t.Errorf("the error must name the pane, got %v", err)
	}
}

// TestAnUnreadableAgentListStartsRatherThanFails. Not knowing whether a pane
// holds an agent is not the same as knowing it holds none — but the next move is
// the same either way, and failing the stage over a listing that did not answer
// would stop work that could have run.
func TestAnUnreadableAgentListStartsRatherThanFails(t *testing.T) {
	server, path := newFakeServer(t)
	server.reply("agent.list", `{"id":"1","error":{"code":"whatever","message":"not answering"}}`)
	server.reply("pane.send_text", `{"id":"1","result":{"type":"ok"}}`)

	runner := &socketRunner{client: dialFake(t, path), Repo: "/repo", Settle: time.Minute}
	ctx, cancel := context.WithTimeout(context.Background(), 250*time.Millisecond)
	defer cancel()

	_, _ = runner.StartAgent(ctx, Workspace{RootPane: "w1:p1"}, "claude", "luna-1", nil)

	if got := len(server.sent("pane.send_text")); got != 1 {
		t.Errorf("an unreadable listing stopped the start, got %d runs", got)
	}
}
