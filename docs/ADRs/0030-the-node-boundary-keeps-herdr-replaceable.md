# ADR-0030: The node boundary keeps herdr replaceable

**Status:** Accepted
**Date:** 2026-08-10

## Context

Luna is born coupled to herdr (ADR-0027) because that buys a working system now. The
intention was stated up front: as Luna matures it may run against something else — a plain
CLI, or a TUI of its own inspired by herdr. Coupling that is cheap to enter and expensive to
leave is a bad trade even when entering it is right.

The relevant fact is that the seam already exists. `src/internal/lead/lead.go` defines:

```go
type Node interface {
    Run(ctx context.Context, state fsm.TaskState, stage fsm.Stage) (Result, error)
}
```

It names no transport, no vendor and no herdr. It exists because the lead had to be built
and tested without a network, a process or an agent, and twelve tests currently inject named
fakes through the `Lead.Node` field. Removing it would not "reduce indirection" — it would
delete the seam that keeps the suite runnable and take the coverage floor with it.

## Decision

**All herdr knowledge lives in one package, `src/internal/herdr/`, and that package's
entry point implements `lead.Node`.**

```go
// today
lead.Lead{ Node: herdr.New(socketPath) }

// tomorrow, if Luna leaves
lead.Lead{ Node: cli.New(...) }   // or tui.New(...)

// in tests, unchanged
lead.Lead{ Node: &deliveringNode{} }
```

The package owns the socket: the newline-delimited JSON framing, `events.subscribe`, the
command calls, the pane and workspace bookkeeping, and the translation from herdr's
vocabulary into Luna's. Nothing outside it mentions a pane, a workspace or an
`AgentStatus`.

No new abstraction is introduced. The interface being satisfied is the one that already
exists and that the tests already use; herdr simply becomes its first real implementation.

The engine's isolation is unchanged and enforced mechanically: `depguard` already denies
`net/http` and `os/exec` inside `src/internal/fsm/**`, and the new package must not appear
in the engine's import graph either.

## Alternatives considered

- **Deleting the interface and calling herdr directly from the lead** — rejected on cost,
  not on taste. The twelve lead tests would need a live herdr socket or would become
  integration tests; the 95% coverage floor would fall; and `AGENTS.md` requires an external
  boundary to be faked with a *named* fake, which is precisely what the interface enables.
  The indirection it would remove is indirection the tests depend on.
- **A second abstraction layer over `Node` ("Terminal", "Host")** — rejected as the opposite
  error. One interface at the boundary is enough. A generic terminal abstraction would be
  designed against a single known implementation, which is how abstractions end up shaped
  like the thing they were supposed to hide.
- **Keeping herdr calls spread across lead and CLI** — rejected. It would put socket
  knowledge in three places and make "what does Luna require of herdr" unanswerable without
  reading everything.

## Consequences

- **Positive:** leaving herdr is writing one new implementation of an existing interface,
  not a refactor. The engine and the lead never learn what a pane is. The question "what
  exactly do we depend on herdr for" has a single answer: the public surface of one package.
- **Negative / costs:** the `Node` interface must stay expressive enough for a non-herdr
  implementation to satisfy honestly. If a herdr-specific concept leaks into `Result` or
  into the interface's shape, the boundary erodes without anything failing.
- **Impacts:**
  - `Result` stays vocabulary-neutral — artifacts and evidence, never pane ids;
  - the herdr package owns the reprojection of the task ↔ workspace binding after a herdr
    restart, since its metadata does not survive one (ADR-0027);
  - a fake herdr at the socket level becomes possible for integration tests, separately from
    the `Node` fakes already used in unit tests;
  - if a second implementation is ever written, the pair should be exercised against the
    same lead tests — that is the check that the boundary held.

## References

- Related documents: [ADR-0027](0027-luna-runs-under-herdr-as-a-socket-client.md),
  [ADR-0031](0031-agents-start-through-herdrs-allowlist.md),
  [ADR-0024](0024-the-reducer-is-pure-verification-runs-outside.md)
