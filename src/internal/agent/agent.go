// Package agent runs a coding agent through its harness's non-interactive mode
// and reads back what it cost.
//
// This replaces driving the harness's terminal UI. The previous transport typed
// into a pty and read the screen to find out what had happened, which meant
// Luna owned a pile of problems that were never about the model: a pty with no
// size, a folder-trust dialog, an input-ready marker ambiguous with the shell
// prompt, boot settles tuned by hand. All of it disappears here, because a
// harness that answers on stdout has nothing to parse off a screen.
//
// One process per call. Nothing here is long-lived, so a stage that dies leaves
// no pane, no session and nothing to reap.
package agent

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"strings"
	"syscall"
	"time"
)

// Usage is what one call to an agent consumed.
//
// It comes from the harness rather than from anything Luna counts. That is the
// whole reason this type is cheap to have: the numbers are reported, not
// estimated, and the estimate was the part that would have been wrong.
type Usage struct {
	InputTokens  int     `json:"input_tokens"`
	OutputTokens int     `json:"output_tokens"`
	CacheRead    int     `json:"cache_read_input_tokens"`
	CacheWrite   int     `json:"cache_creation_input_tokens"`
	CostUSD      float64 `json:"cost_usd"`

	// Model is what actually answered, which is not always what was asked for:
	// a harness may downgrade under load or route by effort.
	Model string `json:"model,omitempty"`
}

// Result is one completed agent call.
type Result struct {
	// Text is what the agent replied. Luna prints it and does not parse it: a
	// reply that could move the flow would put flow control back in the model.
	Text string

	// Session identifies the conversation, for a later call that continues it.
	// Empty when the harness does not offer one, and then only Fresh works.
	Session string

	Usage Usage

	// Turns and Elapsed describe the shape of the call rather than its result.
	// A stage that took forty turns to deliver is worth seeing even when it
	// delivered.
	Turns   int
	Elapsed time.Duration
}

// Context says whether a call starts clean or continues an existing session.
//
// This used to be settled — every stage started fresh, because roles erode in
// long sessions and a fresh process cannot erode. The mechanism was wrong: what
// protects the flow is the check at the exit, which catches a drifted agent and
// a merely bad one alike. So it becomes a setting, and the cost difference is
// large enough to be worth measuring rather than assuming.
type Context string

const (
	// Fresh starts a new session. The safe default, and the only option for the
	// first call of a task.
	Fresh Context = "fresh"

	// Live continues the session named in Call.Session.
	Live Context = "live"
)

// Call is one request to an agent.
type Call struct {
	// Kind names the harness: "claude", "codex", "opencode", "pi".
	Kind string

	// Dir is the working directory — the stage's worktree. The agent is confined
	// to it by the sandbox, so it is also the only place it can write.
	Dir string

	// Reachable is a directory outside the worktree the sandbox is asked to expose
	// read-only. It carries the handover socket, which used to live in the
	// worktree because that was the only place a contained agent could reach.
	//
	// Passed in rather than derived here, so the package that opens the socket is
	// the one that says where it is — there is no second place to keep in step,
	// and no import of the node layer from underneath it.
	Reachable string

	// Prompt is the brief. Luna writes it; the agent never writes one for the
	// next stage.
	Prompt string

	// System is appended to the harness's own system prompt, carrying the role.
	System string

	// Context and Session decide whether this continues a conversation. Session
	// is required when Context is Live and ignored otherwise.
	Context Context
	Session string

	// Deny lists the capabilities this role must not have. The harness removes
	// the named tools from the request, so there is nothing to talk the model
	// out of — but a shell is not a tool name, and a role denied writing can
	// still write through one. Containment is the sandbox's job.
	Deny []string

	// Env is added to the agent's environment. The artifact socket travels here.
	Env []string

	// Budget bounds the call. Zero means the caller's context decides.
	Budget time.Duration

	// Workstream is the named managed workstream this call runs inside, and it is
	// the task's rather than the stage's: every agent a task starts writes to one
	// ledger, so what one stage learned is there for the next one and for the next
	// task that touches the same ground.
	//
	// Empty runs the harness directly, with no memory at all. It is not a fallback
	// to whatever workstream the machine happens to be pointing at — that is the
	// contamination this names its way out of, and an unnamed one would be it
	// arriving by a different door.
	Workstream string

	// MayCreateWorkstream lets the call create the workstream when selecting it
	// finds nothing, for a task that asked for a new one.
	//
	// Selecting is tried first and creating is the fallback, which is the right way
	// round: the ordinary case is a workstream that already exists and costs one
	// launch, and only the first call of a task that asked for a fresh one pays for
	// a second. The other end makes this safe — selecting a name that does not
	// exist is a 404 and creating one that does is a 409, both before the agent
	// starts, so neither wastes a model call and neither falls back to whatever
	// workstream the machine was pointing at.
	//
	// Off by default, and that is the point: without it a typo in a workstream
	// name stops the stage instead of quietly opening a second ledger nobody
	// meant to write to.
	MayCreateWorkstream bool
}

// memoryArgs is the wrapper that puts a call inside the task's workstream.
//
// `--workstream` selects and `--new` creates, and they are exclusive: the other
// end answers 404 for a name that does not exist and 409 for one that does.
// Measured against ai-memory 1.32.1, and both refusals arrive before the agent
// starts — which is what makes selecting-then-creating cheap enough to be the
// order rather than the other way round.
func memoryArgs(call Call, create bool) []string {
	selector := "--workstream"
	if create {
		selector = "--new"
	}
	return []string{memoryWrapper, "run", selector, call.Workstream, call.Kind}
}

// missingWorkstream reports whether a failed call failed because the workstream
// it selected is not there.
//
// Matched on the wrapper's own words rather than on an exit status, because the
// wrapper exits the same way for every reason it refuses — and creating a
// workstream because the disk was full would be worse than the disk being full.
func missingWorkstream(name, output string) bool {
	return strings.Contains(output, "not found: managed workstream") &&
		strings.Contains(output, name)
}

// memoryWrapper is the command that gives a call the project's durable memory.
//
// Not configurable, for the same reason the sandbox is not: it sits between the
// containment and the agent, and a typo in configuration should not be able to
// quietly become "no memory" or, worse, "no sandbox". A variable rather than a
// constant only so a test can point it at a script instead of invoking the real
// one.
var memoryWrapper = "ai-memory"

// ErrNoHarness is returned when the named harness is not installed. It is
// separate from a failed call because it is a setup problem, and retrying a
// missing binary only wastes the retry budget.
var ErrNoHarness = errors.New("harness not installed")

// Runner executes an agent call.
//
// An interface rather than a function so a test can substitute a named fake for
// a real process, and so a second harness is a second implementation rather
// than a branch in the middle of this one.
type Runner interface {
	Run(ctx context.Context, call Call) (Result, error)
}

// Harness runs an agent as a subprocess.
type Harness struct {
	// Sandbox wraps every command. Empty is refused rather than defaulted:
	// starting an agent uncontained because a field was left blank is the
	// failure this check exists for.
	Sandbox string

	// Binary overrides the executable looked up for a harness kind. Tests set
	// it; production leaves it empty and the kind is the name.
	Binary string

	// Kind is which harness Ask puts its question to. Empty means DefaultAsk.
	//
	// Only Ask reads it: Run takes the kind from the Call, because which harness
	// runs a stage is the role's decision and travels with the call. A question
	// has no role, so it is configured here.
	Kind string

	// Deadline bounds one Ask. Zero means AskTimeout. Run has no equivalent —
	// a stage is bounded by Call.Budget, which is per call rather than per
	// harness.
	Deadline time.Duration
}

// resolve checks a call before anything is started and answers with the harness
// that will run it and the binary to run.
//
// Separate from Run because these are the refusals: every one of them is a
// reason not to start a process, and grouping them keeps the difference between
// "this call is wrong" and "this call went wrong" visible.
func (h Harness) resolve(call Call) (harness, string, error) {
	spec, ok := harnesses[call.Kind]
	if !ok {
		return harness{}, "", fmt.Errorf("%w: %q is not a harness Luna knows (%s)",
			ErrNoHarness, call.Kind, strings.Join(known(), ", "))
	}
	if call.Context == Live && call.Session == "" {
		return harness{}, "", fmt.Errorf("a live call to %s names no session to continue", call.Kind)
	}
	if h.Sandbox == "" {
		return harness{}, "", errors.New("no sandbox configured: Luna does not start an agent outside one")
	}
	if _, err := exec.LookPath(h.Sandbox); err != nil {
		return harness{}, "", fmt.Errorf("%w: the sandbox %q: %w", ErrNoHarness, h.Sandbox, err)
	}

	binary := h.Binary
	if binary == "" {
		binary = spec.binary
	}
	return spec, binary, nil
}

// Run executes one call and returns what the agent said and what it cost.
//
// Two recoveries, and both are the same shape: something the call names has gone
// or has not arrived yet, the wrapper says so in its own words, and retrying with
// the one thing that fits is better than failing a stage for something nobody did
// wrong.
//
// A live call whose session the harness no longer has is retried fresh. The id
// lives in the log now, so it can outlive the conversation it names — a task
// picked up days later, a harness that pruned its history. The retried result
// reports Fresh, so the cost column does not claim a resumption that did not
// happen.
//
// A call selecting a workstream that is not there is retried creating it, but
// only for a task that asked for a new one. Neither refusal reaches a model, so
// the second launch costs process time and nothing else.
func (h Harness) Run(ctx context.Context, call Call) (Result, error) {
	result, err := h.runOnce(ctx, call, false)
	if err == nil {
		return result, nil
	}

	if call.Context == Live && staleSession(err) {
		fresh := call
		fresh.Context, fresh.Session = Fresh, ""
		return h.runOnce(ctx, fresh, false)
	}
	if call.MayCreateWorkstream && missingWorkstream(call.Workstream, err.Error()) {
		return h.runOnce(ctx, call, true)
	}
	return result, err
}

// staleSession reports whether a failure is the harness saying it does not have
// the conversation.
//
// Matched on what the harness prints rather than on an exit code, because the
// code is the same one a real failure uses. Measured against claude 2.1.237,
// which answers a session it has lost with plain text on stdout and no JSON at
// all — so this arrives as a parse failure carrying the message.
func staleSession(err error) bool {
	return strings.Contains(strings.ToLower(err.Error()), "no conversation found")
}

func (h Harness) runOnce(ctx context.Context, call Call, createWorkstream bool) (Result, error) {
	spec, binary, err := h.resolve(call)
	if err != nil {
		return Result{}, err
	}

	if call.Budget > 0 {
		var stop context.CancelFunc
		ctx, stop = context.WithTimeout(ctx, call.Budget)
		defer stop()
	}

	args := append([]string{binary}, spec.args(call)...)
	if call.Workstream != "" {
		// The wrapper goes between the sandbox and the harness: contained first,
		// then remembered, so a call that must not escape still cannot. It also has
		// to be this way round for the workstream to reach the agent at all — the
		// id travels to managed children as environment, and the jail clears the
		// environment on the way in.
		args = append(memoryArgs(call, createWorkstream), args[1:]...)
	}
	args = append(sandboxArgs(call.Reachable), args...)
	started := time.Now()

	// #nosec G204 — running a named binary with built arguments is what this
	// package is for. The names are not user input: the harness comes from a
	// closed table and the sandbox from configuration, both refused above when
	// unknown. What the agent then does is bounded by the sandbox, not by this
	// argument list.
	cmd := exec.CommandContext(ctx, h.Sandbox, args...)
	cmd.Dir = call.Dir
	cmd.Env = append(environ(), call.Env...)
	cmd.Stdin = strings.NewReader(call.Prompt)

	// The agent gets its own process group, and the budget kills the group
	// rather than the process.
	//
	// Without this the ceiling is decorative: the sandbox spawns the harness,
	// which spawns the model client, and killing only the direct child leaves
	// the work running while Luna reports the call bounded. Measured — a stage
	// with a 200ms budget ran for the full 30 seconds of its child.
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	cmd.Cancel = func() error {
		if cmd.Process == nil {
			return nil
		}
		return syscall.Kill(-cmd.Process.Pid, syscall.SIGKILL)
	}

	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr

	runErr := cmd.Run()

	// The harness reports its own failures inside the JSON, so the body is read
	// before the exit code is judged: a non-zero exit with a parsable answer
	// says more than the code does.
	//
	// The result is kept even when parsing returns an error alongside it. A call
	// that failed was still billed, and dropping the usage would make failures
	// look free — which is the one direction a cost record must not be wrong in.
	result, parseErr := spec.parse(stdout.Bytes())
	result.Elapsed = time.Since(started)

	switch {
	case parseErr != nil && runErr != nil:
		// Both diagnostics, because either can be the one that explains it. A
		// harness refusing a session it no longer has says so on *stdout* and
		// exits non-zero — reporting only stderr threw the reason away and left
		// "exit status 1" with nothing after the colon.
		return result, fmt.Errorf("%s failed: %w: %s: %s",
			call.Kind, runErr, tail(stderr.String()), tail(stdout.String()))
	case parseErr != nil:
		return result, fmt.Errorf("%s: %w", call.Kind, parseErr)
	case runErr != nil && result.Text == "":
		return result, fmt.Errorf("%s failed: %w: %s", call.Kind, runErr, tail(stderr.String()))
	}
	return result, nil
}

// sandboxArgs is how the sandbox is asked to contain an agent that still has to
// reach a model and commit what it built.
//
// Neither flag relaxes the containment that matters. The jail's filesystem
// boundary is what INV-4 rests on — the agent still sees only its own worktree.
//
// `--network` is what the harness needs to be an agent at all: the model is on
// the other side of it. Measured, and it cost a run to find. Without it the
// harness starts, opens its TLS bundle, and blocks forever on a connection the
// jail will not let it make: no output, no error, no exit. Luna's own budget is
// a two-hour default, so the stage simply sat there. `ai-jail claude -p`
// reproduces it in one command and `ai-jail --network claude -p` answers in two
// seconds.
//
// `--worktree` is what makes the stage's checkout a repository. A worktree's
// `.git` is a one-line pointer into the main repository's `.git/worktrees/`,
// which is outside the jail, so without the flag every git command inside
// answers `fatal: not a git repository: (null)`. The agent reads and edits
// fine, does the work, and has no way to deliver it — five stages of TALLY-5
// billed $3.33 and left HEAD on the base commit for exactly this. The flag is
// off by default in ai-jail 1.19.0; `ai-jail --network /bin/sh -c 'git log'` in
// a worktree reproduces it in one command.
func sandboxArgs(socketDir string) []string {
	if socketDir == "" {
		return []string{"--network", "--worktree"}
	}

	// Read-only, and that is not a saving — it is the point. A stage hands
	// artifacts over by connecting, and Landlock permits connect() on an inode it
	// can merely see, so the agent reaches the socket and cannot write into the
	// directory holding it. Measured against ai-jail 1.20.1, both halves.
	//
	// From Luna's flags rather than from the project's `.ai-jail`, which refuses a
	// map outside the project by design — correctly, since a repository must not
	// be able to name what gets mounted into the sandbox it runs in.
	return []string{"--network", "--worktree", "--map", socketDir}
}

// environ is the parent environment an agent inherits. Wrapped in a function so
// a test can see the same list the process gets, rather than asserting against
// whatever the machine running the test happens to export.
func environ() []string { return os.Environ() }

// tail keeps the end of a harness's diagnostics. The end is where the reason
// is; the head is the banner.
func tail(s string) string {
	s = strings.TrimSpace(s)
	const most = 400
	if len(s) <= most {
		return s
	}
	return "…" + s[len(s)-most:]
}

// harness is one harness's command line and reply format.
type harness struct {
	binary string
	args   func(Call) []string
	parse  func([]byte) (Result, error)
}

// harnesses is closed on purpose. An unlisted harness is refused rather than
// guessed at, because guessing fails open: a flag that is silently ignored
// produces an ungated agent and a report that it was gated.
var harnesses = map[string]harness{
	"claude": {
		binary: "claude",
		args:   claudeArgs,
		parse:  parseClaude,
	},
}

// CanGate reports whether Luna can start this harness without the capabilities a
// role withholds.
//
// The table is closed, so an unlisted harness answers false rather than being
// guessed at: a flag another harness silently ignores produces an ungated agent
// and a report that it was gated.
func CanGate(kind string) bool {
	_, ok := harnesses[kind]
	return ok
}

func known() []string {
	names := make([]string, 0, len(harnesses))
	for name := range harnesses {
		names = append(names, name)
	}
	return names
}

func claudeArgs(call Call) []string {
	// The prompt arrives on stdin rather than as an argument: a brief carrying a
	// contract runs to thousands of characters, and an argument list has a limit
	// that a long stage would find on its own.
	args := []string{"-p", "--output-format", "json"}

	if call.Context == Live {
		args = append(args, "--resume", call.Session)
	}
	if call.System != "" {
		args = append(args, "--append-system-prompt", call.System)
	}
	if len(call.Deny) > 0 {
		args = append(args, "--disallowedTools")
		args = append(args, call.Deny...)
	}
	// The sandbox is what contains the agent, so the harness's own permission
	// ceremony has nothing left to protect and would only stop an unattended run.
	args = append(args, "--permission-mode", "bypassPermissions")
	return args
}

// claudeReply is the subset of the harness's result object Luna reads. The
// harness reports more; naming only what is used means a field that disappears
// is caught here rather than misread downstream.
type claudeReply struct {
	Result    string  `json:"result"`
	SessionID string  `json:"session_id"`
	IsError   bool    `json:"is_error"`
	Subtype   string  `json:"subtype"`
	NumTurns  int     `json:"num_turns"`
	CostUSD   float64 `json:"total_cost_usd"`

	Usage struct {
		InputTokens  int `json:"input_tokens"`
		OutputTokens int `json:"output_tokens"`
		CacheRead    int `json:"cache_read_input_tokens"`
		CacheWrite   int `json:"cache_creation_input_tokens"`
	} `json:"usage"`

	ModelUsage map[string]struct {
		InputTokens int `json:"inputTokens"`
	} `json:"modelUsage"`
}

func parseClaude(body []byte) (Result, error) {
	body = bytes.TrimSpace(body)
	if len(body) == 0 {
		return Result{}, errors.New("the harness returned nothing")
	}

	var reply claudeReply
	if err := json.Unmarshal(body, &reply); err != nil {
		return Result{}, fmt.Errorf("%w: %s", err, tail(string(body)))
	}

	result := Result{
		Text:    reply.Result,
		Session: reply.SessionID,
		Turns:   reply.NumTurns,
		Usage: Usage{
			InputTokens:  reply.Usage.InputTokens,
			OutputTokens: reply.Usage.OutputTokens,
			CacheRead:    reply.Usage.CacheRead,
			CacheWrite:   reply.Usage.CacheWrite,
			CostUSD:      reply.CostUSD,
			Model:        oneModel(reply.ModelUsage),
		},
	}
	if reply.IsError {
		return result, fmt.Errorf("the agent reported failure (%s): %s", reply.Subtype, tail(reply.Result))
	}
	return result, nil
}

// oneModel names the model that answered when exactly one did. A call that
// crossed models reports none rather than an arbitrary pick, because the field
// exists to say what ran and half an answer would be worse than none.
func oneModel(usage map[string]struct {
	InputTokens int `json:"inputTokens"`
},
) string {
	if len(usage) != 1 {
		return ""
	}
	for name := range usage {
		return name
	}
	return ""
}
