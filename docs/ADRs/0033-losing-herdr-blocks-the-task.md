# ADR-0033: Losing herdr blocks the task

**Status:** Accepted
**Date:** 2026-08-10

## Context

With Luna running under herdr (ADR-0027), a running herdr is a dependency of the normal path.
Two things will happen in practice: someone will run `luna` with no herdr up, and a herdr will
exit while a stage is in flight.

The second is the interesting one. When the socket dies mid-stage, Luna knows what it asked
for and nothing about what happened next. The agent may have finished, may be mid-edit, may be
gone with its pane. The worktree is on disk either way, in some state Luna cannot see.

herdr's own restart behaviour makes this sharper: processes do not survive a herdr restart,
layout does, and metadata tokens do not (ADR-0027). So "herdr came back" does not mean "the
work came back".

## Decision

**A missing herdr fails loudly and stops. A lost socket becomes a `Block`.**

Starting with no herdr reachable is a refusal with a message naming the socket path that was
tried — not a retry loop, not a degraded mode.

Losing the socket mid-stage records:

```
Block{reason: "herdr went away while stage \"build\" was running"}
```

The task stops and notifies (INV-core-8). A person brings herdr back, checks what actually
happened in the worktree, and runs `luna unblock`. The stage runs again from a state the
person confirmed.

Luna does not reconnect silently and does not resume on its own.

## Alternatives considered

- **Reconnect with backoff and carry on** — rejected, and it is the tempting one. It is
  smoother day to day and it hides exactly the wrong thing: during the blind window Luna has
  no idea whether the agent finished, died, or is still writing. Reconnecting proves the
  socket is back, not that the work is intact. Resuming on that basis would let Luna record a
  transition it cannot justify from anything it observed — the same class of error as closing
  a stage on `idle` (ADR-0028).
- **Treat it as a stage failure and spend the retry budget** — rejected. Losing the socket
  says nothing about the stage's quality; retrying it as though the agent had failed would
  burn the budget on an infrastructure event and could re-run work that already succeeded.
  ADR-0011 keeps those separate for the same reason.
- **Wait indefinitely for herdr to return** — rejected. It is the silent stall INV-core-8
  exists to forbid: no log entry, no notification, a task that looks alive and is not.

## Consequences

- **Positive:** the log never contains a transition Luna could not observe. Infrastructure
  failure is visible as itself, distinguishable from a node that failed and from a stage that
  stalled — three different situations that warrant three different human responses.
- **Negative / costs:** a herdr restart requires human action to resume, even when the agent
  finished cleanly. That is the deliberate price of not guessing; a future ADR could soften it
  if the node layer gains a way to prove what happened during the gap.
- **Impacts:**
  - the `herdr` package surfaces socket loss as a distinct error the lead can recognise, not
    as a generic I/O failure;
  - the block's reason names the stage that was running, so the person knows where to look;
  - `luna unblock` already resets the retry budget (the block was the escalation), which is
    the right behaviour here too;
  - the reconnection question returns if Luna ever runs unattended for long stretches —
    `nightly` plus a herdr restart means a task waits until morning. Worth revisiting with
    evidence, not before.

## References

- Related documents: [ADR-0027](0027-luna-runs-under-herdr-as-a-socket-client.md),
  [ADR-0011](0011-failure-retry-rollback-or-block.md),
  [invariants](../invariants/core.md)
