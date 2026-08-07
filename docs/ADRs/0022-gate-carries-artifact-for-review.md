# ADR-0022: The gate carries the artifact, and the human can adjust it

**Status:** Accepted
**Date:** 2026-08-07

## Context

Until now the gate was only a **pause**: the task suspended, the human said yes, the task
resumed (see [ADR-0012](0012-gate-suspends-and-frees-the-slot.md)). What was being approved
stayed implicit — the human would have to go look outside, in the worktree or in the logs.

The `spec` case exposes the hole. It produces the `contract` and has the `approve-spec`
gate, but **no stage declared `contract` in `requires`**. The most deliberate artifact of
the flow — the one that exists precisely for the human to review before construction — was
produced and forgotten.

And "approve" is not the only answer that makes sense there. A spec that is nearly right
deserves neither a yes nor a no: it deserves **an adjustment**. If the human edits the
contract outside the system, the FSM proceeds with the version it knows, and the edit is
lost or — worse — `build` uses one thing while the record says another.

## Decision

A gate can **carry the artifact** the stage produced, and the human has three answers:

- **approve** — the artifact enters the context as is;
- **adjust** — the human edits; the **edited version** is the one that enters the context,
  and the adjustment is recorded in the handoff;
- **reject** — the artifact does not enter; the stage that produced it runs again, with the
  rejection in context.

The `contract` stops being a leaf: it becomes a `requires` of `build`, in the version that
came out of the gate. When `spec` does not enter the flow (`chore`, `docs`), `build` does
not require it.

This gives three shapes of gate, by increasing richness: **confirmation** (yes/no),
**artifact for review** (this ADR) and **flow decision** (a loop ceiling was blown).

## Alternatives considered

- **The gate keeps only signaling; the human opens the artifact outside** — rejected because
  it does not model the "adjust". The human would edit something the FSM already considers
  produced and closed, creating a divergence between the real artifact and what the handoff
  records.
- **A dedicated human review stage** — rejected because it would duplicate the mechanism:
  the gate is already the stopping point for a human decision, and a stage that calls no
  agent has no work of its own (see [stages](../architecture/stages.md), "what is not a
  stage").
- **Leave the `contract` as an audit artifact** (`produces_for_human`, see
  [ADR-0021](0021-produces-for-human-is-a-separate-contract-field.md)) — rejected because it
  inverts the intent: the spec exists **to** guide the construction. A contract that `build`
  does not consume is a decorative document.

## Consequences

- **Positive:** human review starts happening **inside** the system, with a trace. The
  adjustment stays in the handoff, so one can tell afterwards that the spec that was built
  was not the spec that was generated — and what changed.
- **Negative / costs:** the gate stops being a boolean and gains a payload and response
  states. It is more mechanism at the point where before there was only a pause.
- **Impacts:**
  - `build` now requires `contract` when `spec` entered the flow — the static check has to
    understand conditional `requires`, which was not necessary before;
  - the rejection creates a rollback to the previous stage. Unlike the review rollback (see
    [ADR-0020](0020-review-finding-invalidates-green.md)), this one happens **before** the
    artifact enters the context, so there is no green to invalidate;
  - the handoff now carries the post-gate version of the artifact, not the one produced by
    the stage.

## References

- Related documents: [default stages](../architecture/stages.md),
  [architecture](../architecture/overview.md)
