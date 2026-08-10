# ADR-0027: Luna runs under herdr as a socket client

**Status:** Accepted
**Date:** 2026-08-10

## Context

Wave 5 is the node layer — the boundary that actually runs an agent. Everything above it is
settled: the engine is a pure reducer, the log is the state, and a stage's contract is
checked on entry and exit. What was not settled is the part that touches the world.

A study of five orchestrators (swarm-forge, herdr, multica, hermes-agent, agent-of-empires)
found that none of them verifies what an agent delivered, and that building the surrounding
machinery — PTY hosting, process supervision, liveness detection, terminal rendering — is
most of the work and none of the differentiation.

herdr already does that part well. It hosts agents in real PTYs, blocks on launch until the
agent is confirmed present, implements two-phase liveness keyed on a monotonic counter, and
renders the whole thing in a TUI. What it has no concept of is a *task*: no queue, no stage,
no ordering, no notion that something comes next.

That is exactly the complement of what Luna is.

## Decision

**Luna runs under herdr. Luna decides and verifies; herdr executes and shows.**

The two are separate programs talking over herdr's Unix socket (newline-delimited JSON).
Agents live in herdr's panes, one git worktree per task, and Luna drives them from outside.

The division:

| Luna owns | herdr owns |
|---|---|
| which stage runs now | the pane and its PTY |
| the stage contract, entry and exit | worktree and workspace topology |
| what counts as evidence | agent liveness observation |
| which gates wait | the `blocked` screen signal |
| the append-only log and replay | the TUI and notifications |
| task identity across restarts | layout persistence |

**No flow-control decision crosses the socket in the herdr → Luna direction.** What comes
back is observed fact — a status changed, a pane exited, a worktree was removed. Turning a
fact into a transition is the lead's job, and the verdict enters through the action
(ADR-0024).

Luna is a plain socket client, not a herdr plugin. A herdr plugin is a manifest plus a
subprocess spawned per invocation, capped at 32 in flight with 64KB of captured output, and
`src/app/api/plugins/runtime.rs` has no kill, restart or health path at all. A resident
daemon would hold one of those slots forever, mute and unsupervised. Luna holds one
`events.subscribe` connection for observation and short request connections for commands.

The coupling is deliberate and temporary in nature: it buys a working system now, and the
node boundary (ADR-0030) keeps the exit cheap.

## Alternatives considered

- **Luna as a herdr plugin** — rejected on the runtime's own shape. Plugins are
  fire-and-forget subprocesses; actions and panes are manifest-only ("runtime action
  registration and runtime argv pane creation are not part of v1"), and there is no managed
  storage. The model does not host a resident process. The inverse composes fine: Luna may
  later ship a thin manifest whose actions call *into* Luna.
- **Building the terminal layer inside Luna** — rejected for wave 5. It is the majority of
  the work, none of it is the thesis, and herdr's version is better than a first attempt
  would be. Revisit only when leaving herdr is the actual goal.
- **Embedding the FSM into herdr as a fork** — rejected. It would put flow control inside a
  program whose premise is that a script or a model drives it, and Luna's whole argument is
  that flow control belongs in code that owns it.

## Consequences

- **Positive:** wave 5 stops being "build a terminal supervisor" and becomes "drive one".
  Luna gets PTY hosting, launch confirmation, liveness and a UI for free, and stays focused
  on the FSM.
- **Negative / costs:** a running herdr becomes a dependency for the normal path, and its
  API is a moving target — the socket contract advises checking protocol version and
  handling unknown fields. Two surfaces exist for a human: they answer an agent in herdr's
  pane, and they answer Luna's gates in Luna.
- **Impacts:**
  - Luna must subscribe to `pane.closed`, `pane.exited`, `workspace.closed` and
    `worktree.removed` and treat them as external events entering the log — a person can
    close a pane behind Luna's back, and that is ordinary, not a failure;
  - the task ↔ workspace binding lives in Luna's log, because herdr's metadata tokens are
    display-only and do not survive its restart (`restore.rs:421` rebuilds them empty);
  - the binding anchors on the **workspace**, not the pane: `pane.move` across workspaces
    assigns a new id and kills in-flight waits;
  - `worktree.remove` never deletes the branch, so branch cleanup stays Luna's;
  - herdr puts no timeout on its git subprocesses, so delegating worktree creation inherits
    that hang.

## References

- Related documents: [ADR-0024](0024-the-reducer-is-pure-verification-runs-outside.md),
  [ADR-0028](0028-herdr-status-triggers-verification-it-never-closes-a-stage.md),
  [ADR-0030](0030-the-node-boundary-keeps-herdr-replaceable.md),
  [architecture/overview](../architecture/overview.md)
