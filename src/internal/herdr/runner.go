package herdr

import (
	"context"
	"errors"
	"fmt"
	"os/exec"
	"path/filepath"
	"strings"
	"time"
)

// socketRunner drives herdr over its socket. It is the real Runner; the node is
// tested against a fake, and this is what a live run uses.
//
// Every method here is a translation: Luna's vocabulary in, herdr's method names
// out, and nothing but observed fact back. No decision is taken in this file.
//
// The shapes below were read off a running herdr 0.8.0 (protocol 19) rather than
// from documentation. Two things that only a live server tells you: the request
// id is a string, and every call gets its own connection because herdr hangs up
// after answering.
type socketRunner struct {
	client *Client

	// Repo is the checkout worktrees are cut from. herdr refuses to make one
	// outside a git work tree, so this has to be a real repository — and an
	// absolute path, which NewRunner is what guarantees.
	Repo string

	// Settle bounds how long a prompt waits for the agent to stop working. It is
	// the budget the profile decided (ADR-0034), handed down by the caller.
	Settle time.Duration

	// retryWait and bootSettle are the startup waits, overridable so a test can
	// exercise the retry logic without sleeping through it.
	//
	// They are fields rather than the constants they default to because the
	// constants are measured against a real herdr — eight seconds for a cold
	// claude to answer its first prompt — and a test driving a fake socket waits
	// that long for nothing. Measured: three tests spent 18 of the suite's 28
	// seconds asleep, and no amount of CI hardware recovers a `time.Sleep`.
	//
	// Zero means the measured default, so a production caller that sets neither
	// gets exactly what it got before these existed. Nothing outside a test sets
	// them (RNF: the timings a real run uses stay the measured ones).
	retryWait   time.Duration
	bootSettle  time.Duration
	dialogPause time.Duration
}

// waits report the startup timings, defaulting to what was measured against a
// live herdr.
func (r *socketRunner) paneWait() time.Duration {
	if r.retryWait > 0 {
		return r.retryWait
	}
	return paneSettleWait
}

func (r *socketRunner) agentBoot() time.Duration {
	if r.bootSettle > 0 {
		return r.bootSettle
	}
	return agentBootSettle
}

// dialogWait is the pause after the trust-dialog Enter, overridable so a test
// does not sit through the real one.
func (r *socketRunner) dialogWait() time.Duration {
	if r.dialogPause > 0 {
		return r.dialogPause
	}
	return dialogSettle
}

// NewRunner builds a Runner backed by a herdr socket.
//
// The repository is absolutised here rather than at each call: herdr answers
// `worktree path must be absolute` to a relative `cwd`, and `luna run` defaults
// it to ".", which is the natural thing for a CLI run from inside the checkout.
// Failing to resolve it leaves the caller's value alone — herdr's refusal names
// the problem better than a path this could invent (ADR-0036).
func NewRunner(client *Client, repo string, settle time.Duration) Runner {
	if absolute, err := filepath.Abs(repo); err == nil {
		repo = absolute
	}
	return &socketRunner{client: client, Repo: repo, Settle: settle}
}

// worktreeCreated is herdr's answer: the whole home for a task at once.
type worktreeCreated struct {
	Workspace struct {
		WorkspaceID string `json:"workspace_id"`
	} `json:"workspace"`
	RootPane struct {
		PaneID string `json:"pane_id"`
		CWD    string `json:"cwd"`
	} `json:"root_pane"`
	Worktree struct {
		Path string `json:"path"`
	} `json:"worktree"`
}

// OpenWorktree creates the worktree a stage works in and returns where it lives.
//
// herdr answers workspace, tab, root pane and worktree in one call, which is why
// one worktree maps cleanly onto one workspace (ADR-0027).
//
// The checkout is per task *and* role, branched from the base the order carried
// (ADR-0055). `base` is a parameter herdr's `worktree.create` already takes —
// checked against the binary, like every other fact about this protocol
// (ADR-0036) — so branching from the previous stage's commit costs nothing but
// passing it.
//
// An existing branch is reopened rather than treated as a failure: a stage that
// was retried has its worktree, and the second attempt must resume it.
func (r *socketRunner) OpenWorktree(_ context.Context, w WorktreeSpec) (Workspace, error) {
	label := w.TaskID
	if w.Role != "" {
		label = w.TaskID + "-" + string(w.Role)
	}

	path, err := checkoutPath(r.Repo, label)
	if err != nil {
		return Workspace{}, err
	}

	params := map[string]any{
		"cwd":    r.Repo,
		"branch": w.Branch(),
		"path":   path,
		"label":  label,
		"focus":  false,
	}
	// Sent only when there is one: an empty base would ask herdr to branch from
	// a ref named "", where omitting it means the repository's own head.
	if w.Base != "" {
		params["base"] = w.Base
	}

	var created worktreeCreated
	err = r.client.Call("worktree.create", params, &created)
	if err != nil && reusable(err) {
		// `worktree.open` takes **exactly one** of path or branch, where `create`
		// takes both — sending create's parameters straight through is refused
		// with `invalid_request`. Found by running it against a live server:
		// resuming a task is the second run, so every test that opened a worktree
		// once was happy (ADR-0036).
		//
		// The path identifies it, because that is what create was told to make.
		err = r.client.Call("worktree.open", map[string]any{
			"cwd":   r.Repo,
			"path":  path,
			"label": label,
			"focus": false,
		}, &created)
	}
	if err != nil {
		return Workspace{}, fmt.Errorf("opening the worktree for %s: %w", label, err)
	}

	// Prefer what herdr confirmed over what was asked for: reopening an existing
	// worktree returns wherever it actually is, which may predate this convention.
	checkout := created.Worktree.Path
	if checkout == "" {
		checkout = created.RootPane.CWD
	}
	if checkout == "" {
		checkout = path
	}
	return Workspace{
		ID:       created.Workspace.WorkspaceID,
		RootPane: created.RootPane.PaneID,
		Path:     checkout,
	}, nil
}

// CloseWorktree removes the checkout when the stage is over.
//
// `--force` because the tree will not be clean: the agent's build artifacts and
// anything it did not commit are still there, and that is exactly what should
// not survive. What survives is the commit (INV-core-6).
//
// A workspace with no id is not an error. It is a stage that failed before herdr
// gave one back, and there is nothing to remove.
func (r *socketRunner) CloseWorktree(_ context.Context, ws Workspace) error {
	if ws.ID == "" {
		return nil
	}

	// `workspace_id`, not `workspace`. The CLI's flag is `--workspace` and the
	// socket API's field is not, which is the exact class of mismatch ADR-0036
	// exists for — and this one was found by running it against a live server,
	// where every test against a fake had been happy.
	params := map[string]any{"workspace_id": ws.ID, "force": true}
	if err := r.client.Call("worktree.remove", params, nil); err != nil {
		return fmt.Errorf("removing the worktree at %s: %w", ws.Path, err)
	}
	return nil
}

// StartAgent puts an agent into the workspace's root pane.
//
// The kind must be one herdr supports — 21 of them in 0.8.0 (ADR-0031) — and the
// pane must already be sitting at an interactive shell prompt, which the worktree
// call just produced. herdr blocks until the agent is detected and ready, which
// removes a race the node would otherwise have to handle itself.
func (r *socketRunner) StartAgent(ctx context.Context, ws Workspace, kind, name string, args []string) (string, error) {
	// An agent already running in this worktree's pane is this same stage's, and
	// it is reused rather than restarted: `worktree.open` returns the pane a
	// resumed stage left behind, so a retry after a stall finds the agent that was
	// already working instead of losing its context (INV-core-5, ADR-0006).
	//
	// Checked before starting rather than after being refused, because pane.run
	// has no name to collide with — it would happily start a second agent on top
	// of the first.
	if pane, running := r.agentIn(ws.RootPane); running {
		return pane, nil
	}

	command, err := jailed(kind, ws.ArtifactSocket, args)
	if err != nil {
		return "", fmt.Errorf("starting %q for %s: %w", kind, name, err)
	}

	// A freshly created worktree's pane is not at its prompt yet, and herdr
	// refuses to run in one that is still busy. Observed against a live 0.8.0:
	// the identical call fails and then succeeds seconds later with nothing else
	// changed. Retrying briefly is the difference between a working run and a
	// block on the first stage of every task.
	if err := retry(ctx, paneSettleAttempts, r.paneWait(), func() error {
		// `pane.send_text` rather than a "run a command" method, because herdr has
		// none: the protocol's verbs are typed at panes and agents, and the way to
		// start a process is to type at the shell that is already there. The
		// newline is what submits it — measured, since text without one sits at
		// the prompt and nothing runs.
		return r.client.Call("pane.send_text", map[string]any{
			"pane_id": ws.RootPane,
			"text":    command + "\n",
		}, nil)
	}, paneBusy); err != nil {
		return "", fmt.Errorf("starting %q as %q in %s: %w", kind, name, ws.RootPane, err)
	}

	// pane.run returns as soon as the command is sent; the agent is interactive
	// some seconds later. Waiting here rather than letting the first prompt race
	// it is the same reason Prompt and its wait are one call.
	if err := r.awaitAgent(ctx, ws.RootPane); err != nil {
		return "", fmt.Errorf("waiting for %q in %s: %w", kind, ws.RootPane, err)
	}
	return ws.RootPane, nil
}

// jailed is the command line that starts an agent inside the sandbox.
//
//	HERDR_AGENT=claude ai-jail claude --permission-mode bypassPermissions
//
// Three parts, each load-bearing. `ai-jail` is the containment, and it wraps the
// *agent* — which is the whole point: Luna talks to herdr over a socket, so an
// agent started through `agent.start` is a child of the herdr server and inherits
// nothing from Luna's own process. Wrapping `luna run` contained Luna and left
// the agent free (ADR-0069).
//
// `HERDR_AGENT` is herdr's own answer to a wrapper hiding the real process — it
// names which screen manifest to detect with, and without it herdr sees `ai-jail`
// and reports no agent at all. The pattern is herdr's, documented with `fence`
// and `nono` as its examples.
//
// This is why `agent.start` cannot be used: its `kind` is a closed set compiled
// into herdr, so there is nowhere to put a wrapper. Measured — a custom kind is
// refused with `unsupported_agent_kind`, and a new one needs a herdr binary
// update rather than a local manifest.
// artifactSocketEnv is the variable `luna artifact` reads to find Luna. It is
// duplicated from the cli package rather than imported, because the dependency
// runs that way: the CLI builds this node, not the reverse.
const artifactSocketEnv = "LUNA_ARTIFACT_SOCKET"

func jailed(kind, socket string, args []string) (string, error) {
	if _, err := exec.LookPath(jailBinary); err != nil {
		return "", fmt.Errorf(
			"%w: %s is not on PATH, and Luna runs every agent inside it — an agent "+
				"outside a sandbox would hold the permissions this passes it (INV-core-7)",
			ErrNoSandbox, jailBinary,
		)
	}

	command := fmt.Sprintf("HERDR_AGENT=%s", kind)

	// The socket the agent hands artifacts over through, in its environment rather
	// than only in the brief. The brief is prose an agent may paraphrase; the
	// variable is what `luna artifact put` actually reads (RFC-0008).
	//
	// It is inside the worktree, so it survives the jail — which is the whole
	// reason the socket is placed there and not beside the log.
	if socket != "" {
		command += fmt.Sprintf(" %s=%s", artifactSocketEnv, socket)
	}

	command += " " + jailBinary + " " + inJail(kind, args)
	return command, nil
}

// inJail is the command the sandbox runs for a harness.
//
// For claude it is not the bare binary: inside a jail whose $HOME starts empty,
// ~/.claude.json does not exist and claude opens its folder-trust dialog instead
// of a prompt — measured: the pane sat at "Is this a project you trust?" while
// herdr reported the agent settled, and the stage closed having delivered
// nothing, intermittently, because the brief's trailing newline sometimes
// confirmed the dialog by accident.
//
// The trust file is written **only when none exists**. The first version wrote
// unconditionally, and that was measured destroying the real ~/.claude.json:
// a jail may bind the user's own claude config into its home read-write, and
// the overwrite went straight through to the file outside — OAuth included.
// `[ -s ]` is the guard: a config that is already there, whatever brought it,
// is the user's and is not Luna's to replace. The dialog can still appear for
// a worktree path that config has never trusted; that is a stall the retry
// handles, and it is strictly better than touching a file Luna does not own.
func inJail(kind string, args []string) string {
	command := kind
	for _, arg := range args {
		command += " " + arg
	}
	if kind != "claude" {
		return command
	}
	return `sh -c '[ -s ~/.claude.json ] || printf "{\"projects\":{\"%s\":{\"hasTrustDialogAccepted\":true}}}" "$PWD" > ~/.claude.json; exec ` +
		command + `'`
}

// agentIn reports the agent herdr sees in a pane, if any.
func (r *socketRunner) agentIn(pane string) (string, bool) {
	var listed struct {
		Agents []struct {
			PaneID string `json:"pane_id"`
		} `json:"agents"`
	}
	if err := r.client.Call("agent.list", map[string]any{}, &listed); err != nil {
		// Not knowing is not the same as knowing there is none, but the caller's
		// next move either way is to start one — and starting into a pane that
		// already has an agent is what the check exists to avoid, not an error to
		// fail the stage over.
		return "", false
	}

	for _, agent := range listed.Agents {
		if agent.PaneID == pane {
			return pane, true
		}
	}
	return "", false
}

// awaitAgent waits until herdr reports an agent in the pane.
//
// herdr detects an agent by what is on the screen, so a wrapped one appears a
// moment after the command runs rather than when the call returns. Prompting
// before that is refused with `agent_not_found`.
//
// Detection is not readiness. herdr reports `idle` as soon as it recognises the
// screen, while the agent behind it is still booting — and `agent.prompt`'s wait
// "first requires an observed state change within 5000ms; otherwise it returns
// agent_prompt_stalled". A freshly started claude takes longer than that to react
// to its first prompt, so the stage stalled on every run: the prompt was never
// submitted, and the failure read as an agent that would not answer.
//
// The settle here is what closes that window. It is a fixed wait rather than a
// poll because there is nothing to poll for: `interactive_ready` is absent from a
// `pane.send_text` agent, which is exactly the field that would have said so.
func (r *socketRunner) awaitAgent(ctx context.Context, pane string) error {
	if err := retry(ctx, agentStartAttempts, r.paneWait(), func() error {
		if _, running := r.agentIn(pane); running {
			return nil
		}
		return errNoAgentYet
	}, func(err error) bool { return errors.Is(err, errNoAgentYet) }); err != nil {
		return err
	}

	select {
	case <-time.After(r.agentBoot()):
	case <-ctx.Done():
		return ctx.Err()
	}

	// One bare Enter before the first prompt. In a jail whose home does not
	// already trust the worktree, claude boots into its folder-trust dialog and
	// sits there while herdr reports it settled — measured from a live pane, and
	// the cause of a stage closing with nothing delivered. The dialog's default
	// is "Yes, I trust this folder" and Enter confirms it; at a claude that is
	// already at its prompt, an empty Enter does nothing. Sent here, after the
	// boot settle, so the dialog has had time to render — and never as part of
	// the brief, whose newline reaching the dialog by accident is exactly the
	// race that made this fail intermittently.
	// Best effort: a pane that cannot receive text will fail the prompt right
	// after this with an error that names the real problem, and failing the start
	// over the nudge would turn a maybe-dialog into a certain block.
	_ = r.client.Call("pane.send_text", map[string]any{
		"pane_id": pane,
		"text":    "\n",
	}, nil)

	// A moment for the screen to move past the dialog, so the brief lands at the
	// prompt rather than racing the transition.
	select {
	case <-time.After(r.dialogWait()):
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}

// Prompt submits the stage's brief and waits for the agent to settle.
//
// One call, not two. herdr's own documentation says the combined form exists to
// avoid the race between submitting and arming the wait — the agent can finish in
// between — and it is also what produces `agent_prompt_stalled` when nothing
// reacts at all, which the node translates for the lead (ADR-0034).
//
// `blocked` is among the states waited for: an agent asking a person has stopped,
// and Luna needs to hear about it rather than wait out the budget (ADR-0029).
func (r *socketRunner) Prompt(ctx context.Context, pane, text string) (AgentStatus, error) {
	deadline := r.Settle
	if deadline <= 0 {
		deadline = 30 * time.Minute
	}

	var settled struct {
		Agent struct {
			AgentStatus AgentStatus `json:"agent_status"`
		} `json:"agent"`
		AgentStatus AgentStatus `json:"agent_status"`
	}
	// The agent is registered before it is interactive, so a prompt sent straight
	// after `agent.start` can be refused with `agent_not_ready`. Same shape as the
	// pane race above: the identical call succeeds moments later.
	err := retry(ctx, paneSettleAttempts, r.paneWait(), func() error {
		return r.client.Call("agent.prompt", map[string]any{
			"target": pane,
			"text":   text,
			// wait is an object, not a flag: herdr refuses a bare `true` outright.
			// Submitting and waiting in one call is what avoids the race between the
			// two, and what produces `agent_prompt_stalled` when nothing reacts.
			"wait": map[string]any{
				"until":      []AgentStatus{StatusIdle, StatusDone, StatusBlocked, StatusUnknown},
				"timeout_ms": deadline.Milliseconds(),
			},
		}, &settled)
	}, notReady)
	if err != nil {
		return "", err
	}

	if settled.Agent.AgentStatus != "" {
		return settled.Agent.AgentStatus, nil
	}
	if settled.AgentStatus != "" {
		return settled.AgentStatus, nil
	}
	// herdr answered without naming a state. Unknown is the honest reading, and
	// it still triggers verification — it just claims nothing (ADR-0028).
	return StatusUnknown, nil
}

// checkoutPath is where a task's worktree lives: `../wt-<repo>-<id>`, a sibling
// of the repository.
//
// herdr would otherwise put it under its own directory, which is fine for herdr
// and wrong here — this project's worktrees follow one convention regardless of
// what made them, so `ls ../wt-*` finds every one and a person can clean up
// without knowing which tool created what.
//
// A sibling rather than a child on purpose: a checkout nested inside the
// repository, or inside a tool's directory, gets caught by that tool's own
// cleanup and by every recursive walk the repository does to itself.
//
// This briefly had a second form, under `.luna/wt`, for when Luna itself ran
// inside the sandbox and could not see a sibling. That is gone with ADR-0069:
// the sandbox wraps the *agent* now, so Luna reads the tree from outside it and
// the agent works in it as its own cwd. Both reach it, and there is one layout
// again.
func checkoutPath(repo, taskID string) (string, error) {
	absolute, err := filepath.Abs(repo)
	if err != nil {
		return "", fmt.Errorf("resolving the repository path %q: %w", repo, err)
	}

	name := filepath.Base(absolute)
	if name == "." || name == string(filepath.Separator) {
		return "", fmt.Errorf("%q does not name a repository directory", repo)
	}

	return filepath.Join(filepath.Dir(absolute), "wt-"+name+"-"+taskID), nil
}

// reusable reports whether herdr refused because the worktree is already there,
// which is a resumable situation rather than a failure.
func reusable(err error) bool {
	lowered := strings.ToLower(err.Error())
	return strings.Contains(lowered, "exists") || strings.Contains(lowered, "already")
}

// How long to keep trying a pane that is not yet at its shell prompt.
//
// The numbers are small on purpose: this covers a startup race of a few seconds,
// not an unavailable herdr. A pane that is still busy after this is a real
// problem, and the error says so rather than being retried forever.
const (
	paneSettleAttempts = 6
	paneSettleWait     = 2 * time.Second
	startTimeout       = 30 * time.Second

	// agentStartAttempts is longer than paneSettleAttempts because it waits for a
	// process to boot and paint a screen rather than for a shell prompt: measured
	// at four to eight seconds for claude, against under two for a pane.
	agentStartAttempts = 15

	// agentBootSettle is the pause between herdr recognising an agent's screen and
	// that agent being able to react to a prompt. herdr's wait needs a state change
	// within 5000ms, and a cold claude takes longer — measured: the first prompt
	// stalled every time, the second answered.
	agentBootSettle = 8 * time.Second

	// dialogSettle is the pause after the trust-dialog Enter, so the brief lands
	// at a prompt rather than racing the screen transition.
	dialogSettle = 2 * time.Second
)

// jailBinary is the sandbox every agent runs inside.
//
// Named rather than configurable, deliberately: which sandbox holds the boundary
// is not a per-project preference but the thing INV-core-7 rests on, and a
// project that could swap it for `cat` would be a project with no boundary.
const jailBinary = "ai-jail"

// ErrNoSandbox is returned when the sandbox is not installed.
//
// Luna refuses rather than falling back to an uncontained agent. The fallback
// exists — it is what shipped before this — and it is worse than a refusal: the
// agent runs with permissions granted on the assumption of a sandbox that is not
// there, and nothing on screen says so (ADR-0069).
var ErrNoSandbox = errors.New("no sandbox")

// errNoAgentYet is the retry signal while herdr has not yet detected the agent.
var errNoAgentYet = errors.New("herdr reports no agent in the pane yet")

// notReady reports herdr refusing a prompt to an agent it has registered but not
// yet marked interactive — the same startup race as paneBusy, one layer up.
func notReady(err error) bool {
	var api *apiError
	if errors.As(err, &api) {
		return api.Code == "agent_not_ready"
	}
	return false
}

// paneBusy reports herdr's refusal to start an agent in a pane that is not yet a
// shell. It is the one error worth retrying, so it is matched by code rather than
// by message text.
func paneBusy(err error) bool {
	var api *apiError
	if errors.As(err, &api) {
		return api.Code == "agent_pane_busy"
	}
	return false
}

// retry runs an operation again while a specific condition holds.
//
// It takes a predicate rather than retrying every failure: retrying blindly would
// turn a genuine refusal into a long wait ending in the same refusal, and would
// hide a misconfigured agent kind behind a timeout.
func retry(ctx context.Context, attempts int, wait time.Duration, op func() error, again func(error) bool) error {
	var err error
	for attempt := range attempts {
		if err = op(); err == nil || !again(err) {
			return err
		}
		if attempt == attempts-1 {
			break
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(wait):
		}
	}
	return err
}
