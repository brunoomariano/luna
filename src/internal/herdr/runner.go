package herdr

import (
	"context"
	"errors"
	"fmt"
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
	var started struct {
		Agent struct {
			PaneID string `json:"pane_id"`
		} `json:"agent"`
	}

	// A freshly created worktree's pane is not at its prompt yet, and herdr
	// refuses to start an agent in one that is still busy. Observed against a
	// live 0.8.0: the identical call fails and then succeeds seconds later with
	// nothing else changed. Retrying briefly is the difference between a working
	// run and a block on the first stage of every task.
	params := map[string]any{
		"name":       name,
		"kind":       kind,
		"pane_id":    ws.RootPane,
		"timeout_ms": startTimeout.Milliseconds(),
	}
	if len(args) > 0 {
		// herdr passes these through to the agent, which is how a denied
		// capability reaches it (ADR-0042). The field is `args`: `agent_args`
		// is accepted and silently ignored, which is the trap ADR-0036 names.
		params["args"] = args
	}

	err := retry(ctx, paneSettleAttempts, paneSettleWait, func() error {
		return r.client.Call("agent.start", params, &started)
	}, paneBusy)

	// The name carries the stage (agentName), so this only ever finds an agent
	// this same stage started — a retry after a stall, or a resumed run. A later
	// stage asks for a different name and gets a fresh agent, which is what
	// INV-core-5 and ADR-0006 require: no stage inherits the session of the one
	// before it.
	//
	// herdr says the name is taken by refusing it, and the refusal carries the
	// pane it is already running in.
	if err != nil && nameTaken(err) {
		return r.reuse(name, ws)
	}
	if err != nil {
		return "", fmt.Errorf("starting %q as %q in %s: %w", kind, name, ws.RootPane, err)
	}

	if started.Agent.PaneID != "" {
		return started.Agent.PaneID, nil
	}
	// herdr started it where we asked and did not echo the pane back.
	return ws.RootPane, nil
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
	err := retry(ctx, paneSettleAttempts, paneSettleWait, func() error {
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
)

// reuse finds the pane a task's agent is already running in.
//
// Reusing rather than restarting is what keeps one agent per task: a fresh agent
// per stage would lose whatever context the previous one built, and would leave
// the abandoned one holding a pane.
func (r *socketRunner) reuse(name string, ws Workspace) (string, error) {
	var listed struct {
		Agents []struct {
			Name   string `json:"name"`
			PaneID string `json:"pane_id"`
		} `json:"agents"`
	}
	if err := r.client.Call("agent.list", map[string]any{}, &listed); err != nil {
		return "", fmt.Errorf("looking for the agent already named %q: %w", name, err)
	}

	for _, agent := range listed.Agents {
		if agent.Name == name {
			return agent.PaneID, nil
		}
	}
	// herdr refused the name and then did not list it. Nothing here can resolve
	// that, and guessing a pane would prompt the wrong agent.
	return "", fmt.Errorf("herdr holds the name %q but reports no agent using it", name)
}

// nameTaken reports the name colliding with an agent this task already started.
func nameTaken(err error) bool {
	var api *apiError
	if errors.As(err, &api) {
		return api.Code == "agent_name_taken"
	}
	return false
}

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
