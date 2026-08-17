# ADR-0066: herdr's panes are a view, and Luna does not react to them

**Status:** Accepted
**Date:** 2026-08-17

## Context

[ADR-0027](0027-luna-runs-under-herdr-as-a-socket-client.md) adopted herdr as the place
agents run, and listed what Luna would learn by observing it: a pane closed behind Luna's
back, a `pane.move` interrupting a wait, an agent going `blocked` between stages. An audit
found none of it built — Luna learns herdr's state only as the return value of its own
`agent.prompt`. [RFC-0007](../RFCs/rfc-0007-luna-hears-herdr-instead-of-only-asking-it.md)
was written to close that, with the protocol measured against a running server.

Two questions in it turned out to be the whole decision:

- should a worktree removed mid-stage block the task?
- is `pane.exited` distinguishable from a stage ending normally?

The answers were **no**, and **it must be** — and the reason given for the second is what
this ADR records:

> The situation of the panes in herdr must not determine Luna's actions. We can even
> recreate the structure in herdr after closing everything there. They run in parallel
> without affecting what Luna does.

That is not a decision about two events. It is a statement about which of the two systems
owns the truth, and it settles a boundary that ADR-0027 left ambiguous.

## Decision

**herdr's runtime state is a view. Luna never takes an action because that view changed.**

What Luna owns is the log: the commit each stage delivered, the evidence, the gate decisions,
the base. That is the state, and it lives in a repository — not in a pane, a workspace, or a
worktree.

So herdr may be closed entirely, restarted, its workspaces removed and recreated, and a task
resumes from the log with a fresh checkout. A pane is where an agent *was* running; it is
never where the task *is*.

Concretely:

- Luna does not subscribe to `pane.closed`, `pane.exited`, `pane.moved`, `workspace.closed`
  or `worktree.removed`, and reacts to none of them.
- The `blocked` status still becomes a Luna block, exactly as
  [ADR-0029](0029-herdr-blocked-becomes-a-luna-block.md) says — but only where it already
  does: as the answer to a prompt Luna is waiting on. An agent that blocks while nothing is
  waiting is a pane in a state, not a fact about the task.
- What Luna learns about herdr, it learns by asking. A call that fails because the pane is
  gone is a transport failure, handled where transport failures already are
  ([ADR-0033](0033-losing-herdr-blocks-the-task.md)).

### Why the failure path is enough

Every case the observation was meant to catch surfaces anyway, one call later, and the
recovery already exists:

| the world changed | how Luna finds out | what happens |
|---|---|---|
| the pane is gone | the next `agent.start` or `agent.prompt` fails | the stage retries, and herdr recreates what it needs |
| the worktree is gone | it is recreated on the next attempt — the branch survives | nothing is lost; the commit is the handoff (ADR-0055) |
| the agent blocked, then somebody answered it | the wait Luna is in returns | as designed |
| the agent blocked with nobody waiting | Luna asks on its next call | the pane is a view, so this is not an event |

The one case where reacting late costs something — a stage verified against a worktree that
had been removed — was fixed by verifying against the delivered commit instead, which is a
correction to Luna and not a reason to watch herdr.

## Consequences

- **The event stream is not needed.** RFC-0007 is obsolete: its three motivating cases are
  either not Luna's to react to, or already covered. The protocol measurement in it is worth
  keeping for whoever wants events for a *view* — `luna status` showing which pane a stage is
  in, for instance — which is a different feature with no transition in it.
- **herdr becomes genuinely replaceable.** ADR-0030 keeps its vocabulary inside one package;
  this makes the weaker claim true as well — nothing about herdr's lifecycle is load-bearing,
  so a different runner has less to reproduce than the protocol suggests.
- **A closed herdr is recoverable by construction.** Not because a recovery path was written,
  but because there is nothing to recover: the task's state was never there.
- **ADR-0027's observation list stands unbuilt, on purpose.** It is recorded in
  `docs/architecture/decisions-as-built.md` as deliberate rather than pending.
- **A pane that leaks is a person's problem, not a task's.** herdr accumulating stale panes
  after a long run is worth fixing in herdr; it will not stop a task.

## Alternatives considered

- **Subscribe and react, as RFC-0007 proposed.** Rejected on the boundary rather than on
  cost: it would make Luna's transitions depend on a UI's lifecycle, and the first
  consequence is the worst one — a person tidying their terminal would block a task that was
  working. The failure path already reports every case that matters, one call later, and
  retries through most of them.

- **Subscribe and only report, never act.** Tempting, and it is what a future `luna status`
  might want. Rejected *now* because it is a second stream of facts with no consumer, and
  because "report but never act" is a discipline that erodes: the first person to want a
  block from an event has every mechanism already in place.

- **Keep the request-time `blocked` check and nothing else** — which is what shipped, and is
  what this ADR confirms rather than changes. Naming it as a decision is the point: it was
  the *absence* of the other half, and now it is the whole of it.

## References

- [ADR-0027](0027-luna-runs-under-herdr-as-a-socket-client.md) — Luna runs under herdr as a
  socket client
- [ADR-0029](0029-herdr-blocked-becomes-a-luna-block.md) — a blocked agent becomes a Luna
  block, at request time
- [ADR-0030](0030-the-node-boundary-keeps-herdr-replaceable.md) — the node boundary keeps
  herdr replaceable
- [ADR-0033](0033-losing-herdr-blocks-the-task.md) — losing herdr blocks the task
- [ADR-0055](0055-one-worktree-per-task-and-role-branched-from-the-last-delivery.md) — the
  commit is the handoff, and the worktree is disposable
- [RFC-0007](../RFCs/rfc-0007-luna-hears-herdr-instead-of-only-asking-it.md) — obsoleted by
  this, with its protocol measurement kept
