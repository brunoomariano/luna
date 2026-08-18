package herdr

import (
	"context"
	"errors"
	"fmt"
	"io"
	"strings"

	"github.com/brunoomariano/luna/src/internal/fsm"
	"github.com/brunoomariano/luna/src/internal/lead"
)

// AgentStatus is what herdr reports about a pane's occupant.
//
// Two of these are traps, and naming them here is half of why this package
// exists. `Idle` means "prompt visible, nothing happening" — not that the work
// succeeded. `Done` is a UI flag that decays to `Idle` once a human focuses the
// tab. Neither is a verdict, and neither closes a stage (ADR-0028).
type AgentStatus string

// The five statuses herdr reports. Two of them are traps, which is why they are
// spelled out here rather than passed through as opaque strings.
const (
	StatusIdle    AgentStatus = "idle"
	StatusWorking AgentStatus = "working"
	StatusBlocked AgentStatus = "blocked"
	StatusDone    AgentStatus = "done"
	StatusUnknown AgentStatus = "unknown"
)

// Settled reports whether the agent stopped moving.
//
// It is the trigger for verification and nothing more. `Unknown` counts as
// settled because the agent is not working — but it carries no claim about the
// outcome, which is why the verdict still comes from running the real check.
func (s AgentStatus) Settled() bool {
	return s == StatusIdle || s == StatusDone || s == StatusUnknown
}

// stallCode is what herdr calls it on the wire.
const stallCode = "agent_prompt_stalled"

// Stalled reports whether an error from herdr is the stall condition.
//
// It reads herdr's error code rather than matching on message text: the code is
// the contract, and a reworded message must not silently stop being detected.
func Stalled(err error) bool {
	var api *apiError
	if errors.As(err, &api) {
		return api.Code == stallCode
	}
	return errors.Is(err, lead.ErrStalled)
}

// Runner is the part of herdr the node actually needs.
//
// An interface rather than *Client so the node can be tested against a fake
// socket without a running herdr — the same reason lead.Node exists. It is
// deliberately small: opening a worktree, closing it, starting an agent and
// prompting it are the whole of what driving a stage requires.
type Runner interface {
	// OpenWorktree creates the worktree a stage works in and returns the
	// workspace that holds it. The binding anchors on the workspace because pane
	// ids move.
	//
	// One worktree per task *and* role, branched from the base (ADR-0055). The
	// role is what makes "whoever writes does not review" a property of the
	// filesystem rather than a line in a brief (INV-core-7), and the base is what
	// makes the handoff the previous stage's commit rather than a description of
	// it (INV-core-6).
	OpenWorktree(ctx context.Context, w WorktreeSpec) (Workspace, error)

	// CloseWorktree removes it again.
	//
	// A worktree that outlives its stage is the failure swarm-forge's own fork
	// documented: a role branch that is never reset accumulates divergence that
	// "compounds at every hop", so feature N faces N-1 features of drift.
	CloseWorktree(ctx context.Context, ws Workspace) error

	// StartAgent puts an agent into a pane in that workspace and waits until it
	// is interactive. The kind must be one herdr knows (ADR-0031); the name is
	// herdr-wide and must be unique, so it carries the task and the stage.
	//
	// args are passed through to the agent itself, which is how a denied
	// capability reaches it (ADR-0042).
	StartAgent(ctx context.Context, ws Workspace, kind, name string, args []string) (string, error)

	// Prompt submits text and waits for the agent to settle, returning the status
	// it settled at. Prompt and wait are one call because two would race.
	Prompt(ctx context.Context, pane, text string) (AgentStatus, error)
}

// Prover runs the checks that prove an artifact.
//
// It is deliberately not part of Runner: verification is not a herdr operation
// (ADR-0035). herdr creates the worktree and hosts the agent; the check runs
// against a real exit code somewhere else, which is what keeps the evidence the
// tool's answer rather than something scraped off a screen.
type Prover interface {
	Prove(ctx context.Context, v fsm.Verifier, seq int) (fsm.Evidence, error)
}

// Workspace is herdr's home for one stage: the worktree, its workspace and the
// pane the agent runs in.
type Workspace struct {
	ID       string
	RootPane string
	Path     string

	// ArtifactSocket is where the agent hands artifacts over, when the stage
	// declares any that are not committed. Empty means the stage owes none, and
	// the agent gets no writer it has no use for (RFC-0008).
	ArtifactSocket string
}

// WorktreeSpec is what a stage needs a checkout to be.
//
// It is a struct rather than three parameters because the three travel together
// and one of them is easy to leave out: a call that forgot the base would branch
// from whatever the repository is on, silently discarding every stage before it
// — and it would look like it worked.
type WorktreeSpec struct {
	TaskID string
	Role   fsm.RoleName

	// Base is the commit to branch from — the previous stage's delivery. Empty
	// means the repository's own head, which is the first stage of a task.
	Base string
}

// Branch is where this stage's work lives.
//
// Named after the task and the role so a checkout is findable without consulting
// Luna, and so two roles on one task cannot land on the same branch.
//
// The role is joined with "-" rather than "/", which is not cosmetic: git refs
// are a directory tree, so `luna/<task>/<role>` makes `luna/<task>` a directory
// and the roleless form of the same task can no longer exist. A mechanical stage
// following any agent stage — `setup`, which is stage 2 of every task — died on
// `cannot lock ref`. One "/" keeps Luna's branches in their own namespace; a
// second one would put every task's stages in a namespace of their own, which is
// what collides.
func (w WorktreeSpec) Branch() string {
	if w.Role == "" {
		return "luna/" + w.TaskID
	}
	return "luna/" + w.TaskID + "-" + string(w.Role)
}

// Node runs a stage inside herdr. It satisfies lead.Node (ADR-0030).
type Node struct {
	Runner Runner

	// Roles resolves a stage's role to what runs it. A stage whose role does not
	// resolve stops loudly rather than running under some fallback agent and
	// having the result called that role's opinion (ADR-0040).
	Roles func(fsm.RoleName) (fsm.Role, bool)

	// Prompt builds what the agent is told for a stage. Injected rather than
	// built here so the wording is configuration, not code.
	Prompt func(state fsm.TaskState, stage fsm.Stage) string

	// Delivered reads the commit a stage left in its worktree and the message it
	// left with it. Injected rather than called directly so this package keeps no
	// git dependency, the same way Prove keeps out the shell.
	//
	// The commit becomes the next stage's base. The message is where the agent
	// declares which artifacts it produced, which is the only thing that stops the
	// exit check comparing the stage's promises against a copy of themselves.
	//
	// Nil means no commit is ever reported, and the base never moves — which is
	// what happened before this existed: nine stages closed on a real run and
	// seven of them branched from the pre-task commit, so the reviewer reviewed a
	// tree with none of the implementer's work in it.
	Delivered func(ctx context.Context, worktree string) (commit, message string, err error)

	// Prove runs the contract's checks over a named commit.
	//
	// It takes the commit rather than the worktree, because the worktree is
	// removed when the stage ends (ADR-0055) and a retry then had nowhere to run
	// — measured on the swarm bench, on a delivery that was itself green. The
	// commit outlives the tree, and the repository the node was configured with
	// is what the checkout is cut from (ADR-0035).
	Prove func(commit string) Prover

	// Artifacts opens the socket a stage's agent hands artifacts over through, for
	// the artifacts the contract says are not committed (RFC-0008).
	//
	// Injected for the same reason as Delivered and Prove: this package talks to
	// herdr, and the store is somebody else's dependency. Nil means no socket is
	// opened, which is every stage whose contract declares no handover — and every
	// caller that has not wired one, where the agent simply has nowhere to put
	// something it was never asked for.
	Artifacts func(taskID, worktree, stage string, seq int) (io.Closer, string, error)

	// socket is where the current stage's agent hands artifacts over, remembered
	// between opening it and telling the agent about it.
	socket string

	// Stored reports the hash of what a stage handed over, or an error if it
	// handed over nothing. It is what replaces the agent's word for an artifact
	// that is not in the commit (RFC-0008).
	Stored func(taskID, stage, artifact string) (hash string, err error)

	// Warn reports something that went wrong beside the work rather than in it —
	// a worktree that would not be removed. Nil discards, because a node with no
	// reporter configured should still run a stage.
	Warn func(format string, args ...any)

	// There is no Merge field, and its absence is the decision rather than an
	// omission: Luna does not integrate. A task ends on its own branch and moving
	// that work anywhere else is a manual act (ADR-0062), so nothing here brings
	// a commit back into a shared branch.
}

// warn reports a problem that must not fail the stage.
func (n *Node) warn(format string, args ...any) {
	if n.Warn != nil {
		n.Warn(format, args...)
	}
}

// runMechanical performs a stage with no agent in it.
//
// It verifies and nothing else. `setup` is a worktree and there is no longer a
// stage that integrates — the work stays on the task's own branch (ADR-0062).
func (n *Node) runMechanical(ctx context.Context, state fsm.TaskState, stage fsm.Stage, ws Workspace) (lead.Result, error) {
	return n.verify(ctx, ws, state, stage)
}

// Run drives one stage and reports what it delivered.
//
// The shape is the whole of ADR-0028: herdr's status decides only *when* to
// verify, and the verdict decides what happened. A stage that settles without
// passing verification comes back with failing evidence, and the reducer turns
// that into a block — this layer never decides a transition.
func (n *Node) Run(ctx context.Context, state fsm.TaskState, stage fsm.Stage) (lead.Result, error) {
	result, err := n.conduct(ctx, state, stage)
	// herdr going away is the machinery breaking, not the stage failing. Marking it
	// here rather than at each return keeps the lead from importing this package to
	// tell the two apart (ADR-0030, ADR-0033).
	if errors.Is(err, ErrGone) {
		return result, fmt.Errorf("%w: %w", lead.ErrInfrastructure, err)
	}
	return result, err
}

// conduct is Run without the error classification, so the wrapper above has one
// place to mark what came from the transport rather than from the stage.
func (n *Node) conduct(ctx context.Context, state fsm.TaskState, stage fsm.Stage) (lead.Result, error) {
	ws, err := n.Runner.OpenWorktree(ctx, WorktreeSpec{
		TaskID: state.ID,
		Role:   fsm.RoleName(stage.Role),
		Base:   state.Base,
	})
	if err != nil {
		return lead.Result{}, err
	}

	// The worktree lasts exactly as long as the stage. What survives is the
	// commit, which is the handoff — so the next role starts from the artifact
	// and never from a directory somebody else was working in (ADR-0055).
	//
	// A failure to clean up does not fail the stage: the work is committed by
	// then, and turning "the stage delivered" into "the stage failed" because a
	// directory would not go away would lose the more important of the two. It
	// is reported rather than swallowed.
	defer func() {
		if err := n.Runner.CloseWorktree(context.WithoutCancel(ctx), ws); err != nil {
			n.warn("could not remove the worktree for %s at %s: %v", state.ID, stage.ID, err)
		}
	}()

	// A mechanical stage runs no agent at all: `setup` is a worktree, `commit` is
	// git, and paying a model to run those buys nothing and can lose something
	// (ADR-0040). The verification still runs, so the stage still has to prove
	// what it produced.
	if stage.Mechanical() {
		return n.runMechanical(ctx, state, stage, ws)
	}

	role, err := n.role(stage)
	if err != nil {
		return lead.Result{}, err
	}

	// Opened before the agent starts, so the socket is listening by the time the
	// first `luna artifact put` runs, and closed with the stage. A stage that
	// declares no handover opens nothing: there is no reason to expose a writer to
	// an agent that owes nothing through it.
	socket, err := n.serveArtifacts(state, ws, stage)
	if err != nil {
		return lead.Result{}, err
	}
	if socket != nil {
		defer func() {
			if err := socket.Close(); err != nil {
				n.warn("could not close the artifact socket for %s at %s: %v", state.ID, stage.ID, err)
			}
		}()
		ws.ArtifactSocket = n.socket
	}

	if err := n.runAgent(ctx, ws, state, stage, role); err != nil {
		return lead.Result{}, err
	}

	// The agent settled, whatever that means. The status said it stopped; only
	// the verifier says whether the stage delivered.
	return n.verify(ctx, ws, state, stage)
}

// runAgent starts the stage's agent, prompts it, and waits for it to settle.
//
// Split from conduct so the worktree's lifetime and the agent's turn read
// separately — the first is about what survives the stage, the second about what
// happens inside it.
func (n *Node) runAgent(ctx context.Context, ws Workspace, state fsm.TaskState, stage fsm.Stage, role fsm.Role) error {
	// The statement arrives replayed, in the state the caller handed in. It used
	// to be read fresh from the registry here, so that an edit made mid-run
	// reached the next stage; now an edit *is* an event, so the replay already has
	// it and there is nothing left to go and ask (ADR-0067).
	name := agentName(state.ID, stage.ID)

	// A gated role starts without what it must not have — the tool is absent
	// rather than discouraged (ADR-0018). A harness Luna cannot gate stops the
	// stage instead of running an ungated review (ADR-0041).
	args, err := gateArgs(role)
	if err != nil {
		return fmt.Errorf("stage %q: %w", stage.ID, err)
	}

	pane, err := n.Runner.StartAgent(ctx, ws, role.Agent, name, args)
	if err != nil {
		return err
	}

	// The prompt targets the pane. An agent started through `pane.run` has no
	// herdr-side name to be reached by — measured: `agent.prompt` resolves a pane
	// id and answers, and only an unknown *name* is refused (ADR-0069).
	status, err := n.Runner.Prompt(ctx, pane, n.prompt(state, stage, role))
	if err != nil {
		// A stall is translated here so nothing above this package has to read
		// herdr's error codes. What crosses the boundary is Luna's vocabulary
		// (ADR-0030), and the lead decides what a stall means (ADR-0034).
		if Stalled(err) {
			return fmt.Errorf("%w: the agent did not react in stage %q", lead.ErrStalled, stage.ID)
		}
		return err
	}

	// A blocked agent is asking a person for something the flow did not foresee.
	// It is reported as an error so the lead escalates it, and the reason names
	// the pane so someone can find what is asking (ADR-0029).
	if status == StatusBlocked {
		return fmt.Errorf("the agent in pane %s is asking for input", pane)
	}
	return nil
}

// verify runs each artifact's verifier and turns the results into evidence.
//
// Every owed artifact gets a record, including the ones nothing checked: an
// artifact verified by existence says so in its scope rather than borrowing the
// appearance of a passing test (ADR-0032).
func (n *Node) verify(ctx context.Context, ws Workspace, state fsm.TaskState, stage fsm.Stage) (lead.Result, error) {
	owed := append(append([]fsm.Artifact{}, stage.Produces...), stage.ProducesForHuman...)
	result := lead.Result{
		Delivered: owed,
		Evidence:  make(map[fsm.Artifact]fsm.Evidence, len(owed)),
	}

	// Read before the worktree is closed, because that is the only moment it can
	// be read: the tree is removed as soon as the stage ends, and what survives is
	// the commit it is being asked for.
	if err := n.readHandover(ctx, ws, state, stage, &result); err != nil {
		return lead.Result{}, err
	}

	// After result.Commit is read, because that is what gets verified: the commit
	// the stage delivered, reached from the repository rather than from a tree
	// that is about to be removed.
	prover := n.prover(result.Commit)
	for _, artifact := range owed {
		verifier := fsm.VerifierFor(stage, artifact)

		// An artifact handed to Luna is not in the commit, so the commit is the
		// wrong place to look for it — neither the assumption nor the agent's
		// `Delivered:` line can vouch for it. The store answers instead, and it
		// answers with a hash, which is the location INV-core-11 asks the handoff
		// to carry.
		if existence, ok := verifier.(fsm.Existence); ok && existence.Handover {
			evidence, delivered := n.proveHandover(state, stage, artifact)
			result.Evidence[artifact] = evidence
			result.Delivered = claimOnly(result.Delivered, artifact, delivered)
			continue
		}

		evidence, err := prover.Prove(ctx, verifier, state.Seq)
		if err != nil {
			return lead.Result{}, err
		}
		result.Evidence[artifact] = evidence
	}
	return result, nil
}

// claimOnly makes an artifact's presence in the delivered list match what the
// store said, whatever the assumption or the agent's declaration claimed.
//
// Both directions matter. The list starts as everything owed, so an artifact
// nobody handed over arrives already claimed and has to be removed — that was a
// stage closing on work the store never saw, caught by its own test. And an
// agent's `Delivered:` line replaces the list wholesale, so an artifact it did
// hand over may be missing and has to be added back.
func claimOnly(delivered []fsm.Artifact, artifact fsm.Artifact, wasDelivered bool) []fsm.Artifact {
	kept := delivered[:0]
	for _, a := range delivered {
		if a != artifact {
			kept = append(kept, a)
		}
	}
	if wasDelivered {
		kept = append(kept, artifact)
	}
	return kept
}

// proveHandover asks the store whether the agent handed the artifact over.
//
// This is the same correction ADR-0070 made for a path, one step further: the
// agent's word is replaced by a witness. There the witness is git; here it is
// Luna's own store, which is stronger — Luna wrote the row itself, so there is
// nothing to take on trust.
//
// The evidence carries the content's hash, which is what makes an audit able to
// say *which* version satisfied the check rather than that something did.
func (n *Node) proveHandover(state fsm.TaskState, stage fsm.Stage, artifact fsm.Artifact) (fsm.Evidence, bool) {
	if n.Stored == nil {
		// Nothing to ask. Recording a pass here would be the self-report ADR-0028
		// refuses, so it fails and says why.
		return fsm.Evidence{
			Scope:      fsm.ScopeExistence,
			Verdict:    fsm.VerdictFailed,
			Detail:     fmt.Sprintf("%s is handed over to Luna and no store is configured to receive it", artifact),
			RecordedAt: state.Seq,
		}, false
	}

	hash, err := n.Stored(state.ID, string(stage.ID), string(artifact))
	if err != nil {
		return fsm.Evidence{
			Scope:      fsm.ScopeExistence,
			Verdict:    fsm.VerdictFailed,
			Detail:     fmt.Sprintf("%s was not handed over: %v", artifact, err),
			RecordedAt: state.Seq,
		}, false
	}

	return fsm.Evidence{
		Scope:      fsm.ScopeExistence,
		Verdict:    fsm.VerdictPassed,
		Detail:     fmt.Sprintf("handed over to Luna, %s", hash),
		RecordedAt: state.Seq,
	}, true
}

// readHandover reads what the stage committed and what its agent declared.
//
// A tree that cannot be read stops the stage. Carrying on would record an empty
// commit, which reads as "delivered nothing" — the base would not move and the
// verification would fall back to the repository's own HEAD, so the stage would
// pass having checked somebody else's work (ADR-0068).
//
// The declaration replaces the assumption: without it the exit check compares
// `owed` against `owed` and always agrees — measured: `verify` owed
// `dod_checked`, committed a file called `verification`, and closed green.
//
// Only a stage that runs an agent can declare anything, and that condition is
// the whole subtlety. A worktree's HEAD is the previous stage's commit, which
// already carries somebody else's declaration, so a mechanical stage reads a
// message no one wrote for it. Both mechanical stages were measured doing
// exactly that: `setup` reported `repos` from `discovery`, and `commit` reported
// `review_report` from `code-review`. "Did this stage commit?" was the first
// attempt and is not enough: by `commit` the base is two stages back, so HEAD
// differs from it and the inherited message passes anyway. Both conditions,
// because they catch different halves — an agent that worked and committed
// nothing leaves HEAD on the base, and the base's message is the previous
// stage's declaration.
//
// An agent that declared nothing falls back to the assumption, because every
// agent that ran before this existed wrote no such line and a stage must not
// start failing over the shape of a commit message.
func (n *Node) readHandover(ctx context.Context, ws Workspace, state fsm.TaskState, stage fsm.Stage, result *lead.Result) error {
	if n.Delivered == nil {
		return nil
	}

	commit, message, err := n.Delivered(ctx, ws.Path)
	if err != nil {
		return err
	}
	result.Commit = commit

	if !stage.Mechanical() && commit != state.Base {
		if declared := fsm.ReadDelivered(message); len(declared) > 0 {
			result.Delivered = declared
		}
	}
	return nil
}

// prover is what proves this stage's artifacts, defaulting to one that runs
// nothing. The default keeps a zero Node usable — a stage still closes, on
// existence evidence, which is the truth about what a Node with no prover proved.
func (n *Node) prover(commit string) Prover {
	if n.Prove != nil {
		return n.Prove(commit)
	}
	return existenceOnly{}
}

// existenceOnly records delivery without running anything.
type existenceOnly struct{}

func (existenceOnly) Prove(_ context.Context, v fsm.Verifier, seq int) (fsm.Evidence, error) {
	return fsm.Evidence{Scope: v.Proves(), Verdict: fsm.VerdictPassed, RecordedAt: seq}, nil
}

// contained reports whether something is confining this process, defaulting to
// no. The default is the conservative one on purpose: an unset field must not be

// statement reads what the task is about, and reports nothing rather than
// failing when it cannot.
//
// A registry that is down is a worse brief, not a stopped stage: everything the
// contract requires is still in the state, and that is what every stage ran on
// before a statement existed at all.
func (n *Node) prompt(state fsm.TaskState, stage fsm.Stage, role fsm.Role) string {
	if n.Prompt != nil {
		return n.Prompt(state, stage)
	}
	return brief(state, stage, role)
}

// brief is what an agent is told when it starts.
//
// It has to carry the handoff, because the agent is new: it did not run the
// previous stage and has no memory of it. What crosses is pointers and the
// contract — never a prose summary of what happened, which would degrade at every
// hop (INV-core-6).
//
// The body is generated here rather than written by an agent, which is what stops
// one stage from injecting narrative into the next.
func brief(state fsm.TaskState, stage fsm.Stage, role fsm.Role) string {
	var b strings.Builder

	if role.Brief != "" {
		b.WriteString(role.Brief)
		b.WriteString("\n\n")
	}

	fmt.Fprintf(&b, "Task %s (%s), stage %s.\n", state.ID, state.Context.Kind, stage.ID)
	fmt.Fprintf(&b, "Worktree: the directory you are in.\n")

	// The base is where the previous stage's work actually is. Without it an agent
	// told to read `approach` has a noun and no location, and goes looking around
	// the filesystem for it — which is how a stage ends up stopped on its own
	// harness's permission prompt rather than working.
	if state.Base != "" {
		fmt.Fprintf(&b, "Base commit: %s — everything produced so far is in it.\n", state.Base)
	}

	if state.Statement.Stated() {
		fmt.Fprintf(&b, "\nWhat the task is about:\n")
		for _, part := range []struct{ label, text string }{
			{"", state.Statement.Description},
			{"Design: ", state.Statement.Design},
			{"Done when: ", state.Statement.Acceptance},
		} {
			if part.text != "" {
				fmt.Fprintf(&b, "  %s%s\n", part.label, part.text)
			}
		}
	}

	if len(stage.Requires) > 0 {
		fmt.Fprintf(&b, "\nAlready produced, and yours to read:\n")
		for _, artifact := range stage.Requires {
			fmt.Fprintf(&b, "  - %s%s\n", artifact, provenance(state, artifact))
		}
	}

	writeHandover(&b, stage)
	return b.String()
}

// provenance names how a required artifact was proven, so the agent knows whether
// it is reading something checked or something merely delivered (ADR-0032).
func provenance(state fsm.TaskState, artifact fsm.Artifact) string {
	evidence, ok := state.Evidence[artifact]
	if !ok || !evidence.Delivered() {
		return ""
	}
	return fmt.Sprintf(" (%s)", evidence.Scope)
}

// howProven names the check an artifact will face, so the agent knows what it is
// being held to before it starts rather than after it fails.
//
// A declared path is told for the stronger reason: it is not only what the
// artifact is checked against, it is the one thing the agent has to get right for
// the check to find anything. An agent that writes the correct content in the
// wrong directory fails a check it was never shown (ADR-0070).
func howProven(stage fsm.Stage, artifact fsm.Artifact) string {
	switch verifier := fsm.VerifierFor(stage, artifact).(type) {
	case fsm.Command:
		return fmt.Sprintf(" — checked by `%s`", verifier.Describe())
	case fsm.Existence:
		if verifier.Path == "" {
			return ""
		}
		return fmt.Sprintf(" — write it under `%s`, which is where it is looked for", verifier.Path)
	default:
		return ""
	}
}

// role resolves the stage's role, refusing rather than falling back.
//
// A role that does not resolve is a configuration mistake, and running the stage
// on some default agent would produce work attributed to a role nobody defined —
// which is worse than stopping, because it looks like it worked.
func (n *Node) role(stage fsm.Stage) (fsm.Role, error) {
	if n.Roles == nil {
		return fsm.Role{}, fmt.Errorf("stage %q names role %q and no roles are configured", stage.ID, stage.Role)
	}

	role, ok := n.Roles(fsm.RoleName(stage.Role))
	if !ok {
		return fsm.Role{}, fmt.Errorf("stage %q names role %q, which resolves to nothing", stage.ID, stage.Role)
	}
	if role.Agent == "" {
		return fsm.Role{}, fmt.Errorf("role %q names no agent to run it", stage.Role)
	}
	return role, nil
}

// agentName is what herdr calls this stage's agent.
//
// Two constraints, both learned from a live herdr rather than from documentation.
// Names are unique across the whole server, not per workspace, so a second task
// reusing one is refused with `agent_name_taken` — naming it after the task makes
// that impossible and keeps the pane findable by anyone who knows the task id.
// And the name must match `[a-z][a-z0-9_-]{0,31}`, so a task id like "LUNA-1" has
// to be folded rather than passed through.
func agentName(taskID string, stage fsm.StageID) string {
	var b strings.Builder
	b.WriteString("luna-")

	for _, r := range strings.ToLower(taskID + "-" + string(stage)) {
		switch {
		case r >= 'a' && r <= 'z', r >= '0' && r <= '9', r == '-', r == '_':
			b.WriteRune(r)
		default:
			b.WriteRune('-')
		}
		// The cap is herdr's, and it should never be reached: task ids are
		// validated against it (fsm.MaxTaskIDLen) and a flow with stage names long
		// enough to squeeze them is reported by fsm.AuditFlowNames. This stays as
		// the floor, because sending a name herdr refuses would fail the stage —
		// but truncating is why two stages of one task once produced the same name,
		// and reuse() then prompted the wrong pane.
		if b.Len() >= agentNameLimit {
			break
		}
	}
	return b.String()
}

// agentNameLimit is what herdr accepts for an agent name, verified against a
// running server: `[a-z][a-z0-9_-]{0,31}` (ADR-0036).
const agentNameLimit = 32

// join lists artifacts the way the brief asks the agent to write them back.
func join(artifacts []fsm.Artifact) string {
	names := make([]string, 0, len(artifacts))
	for _, a := range artifacts {
		names = append(names, string(a))
	}
	return strings.Join(names, ", ")
}

// writeHandover states what the stage owes and how to hand it over.
//
// Split from brief because the two answer different questions — what the stage
// is, and what leaving it looks like — and because together they were the most
// complex function in the package without deciding anything.
func writeHandover(b *strings.Builder, stage fsm.Stage) {
	owed := append(append([]fsm.Artifact{}, stage.Produces...), stage.ProducesForHuman...)
	if len(owed) > 0 {
		fmt.Fprintf(b, "\nThis stage does not close until it delivers:\n")
		for _, artifact := range owed {
			fmt.Fprintf(b, "  - %s%s\n", artifact, howProven(stage, artifact))
		}
	}

	// An artifact handed to Luna is not committed, so the instruction that follows
	// — "commit it or it is not delivered" — is wrong for it and has to be said
	// separately. An agent told to commit the contract will commit the contract.
	if handed := handedOverBy(stage); len(handed) > 0 {
		fmt.Fprintf(b, "\nHand these over to Luna instead of committing them, with `luna artifact put <name> < file`:\n")
		for _, artifact := range handed {
			fmt.Fprintf(b, "  - %s\n", artifact)
		}
		fmt.Fprintf(b, "Read what an earlier stage handed over with `luna artifact get <name>`. "+
			"These are working documents, not part of the repository — do not commit them.\n")
	}

	// The handoff, said out loud. ADR-0055 makes the commit the handoff and
	// ADR-0058 makes it the snapshot, and neither was ever told to the agent: the
	// first full run closed six stages across five branches and left the
	// repository byte-identical to where it started.
	fmt.Fprintf(b, "\nHand the work over by committing it to this worktree's branch. "+
		"Work that is not committed is not delivered — the next stage reads your commit, not this directory.\n")

	// Asked for by name, because the exit check reads it. An agent that produced
	// the right thing under a different name closed the stage green until this
	// line existed.
	if len(owed) > 0 {
		fmt.Fprintf(b, "End the commit message with a line naming what you delivered, using the names above:\n")
		fmt.Fprintf(b, "  Delivered: %s\n", join(owed))
	}
}

// serveArtifacts opens the handover socket when the stage's contract asks for one.
//
// The condition is the contract rather than configuration: an artifact declared
// `handover = "store"` is one the agent cannot commit, so the socket is the only
// way it can deliver at all. A stage with none opens nothing, which keeps the
// writer out of reach of an agent that owes nothing through it.
func (n *Node) serveArtifacts(state fsm.TaskState, ws Workspace, stage fsm.Stage) (io.Closer, error) {
	if n.Artifacts == nil || !handsOver(stage) {
		return nil, nil //nolint:nilnil // "no socket needed" is not an error
	}

	closer, path, err := n.Artifacts(state.ID, ws.Path, string(stage.ID), state.Seq)
	if err != nil {
		return nil, fmt.Errorf("opening the artifact socket for stage %q: %w", stage.ID, err)
	}
	n.socket = path
	return closer, nil
}

// handsOver reports whether any artifact the stage owes is handed to Luna rather
// than committed.
func handsOver(stage fsm.Stage) bool {
	return len(handedOverBy(stage)) > 0
}

// handedOverBy lists the artifacts a stage owes through Luna rather than through
// the commit, in declaration order.
func handedOverBy(stage fsm.Stage) []fsm.Artifact {
	owed := append(append([]fsm.Artifact{}, stage.Produces...), stage.ProducesForHuman...)

	var handed []fsm.Artifact
	for _, artifact := range owed {
		if existence, ok := fsm.VerifierFor(stage, artifact).(fsm.Existence); ok && existence.Handover {
			handed = append(handed, artifact)
		}
	}
	return handed
}
