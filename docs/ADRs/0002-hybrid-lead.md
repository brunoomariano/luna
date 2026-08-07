# ADR-0002: The lead is hybrid, not purely deterministic

**Status:** Accepted
**Date:** 2026-08-06

## Context

With flow control in code (see [ADR-0001](0001-flow-control-out-of-model.md)), it remains
to decide who drives the task when something goes off the rails: the node failed, the
output did not validate, a review found a problem.

There is a real case that weighs on the choice. In a run of the system that inspired this
design, the state machine had a bug and told the lead to review an already reviewed
document. **The lead refused and escalated to the human** — it had been instructed to obey
the FSM, and still recognized that the instruction made no sense.

## Decision

The lead is hybrid: **code on the happy path**, **model when something goes off the
rails**.

On the happy path the lead decides the stage, calls the agent, validates and records —
zero token cost, deterministic behavior. Outside it, a model decides what to do: try
again, go back a stage, open a gate or block.

## Alternatives considered

- **Purely code lead** — rejected because it loses the judgment that catches the FSM's own
  error. There is a real case of a lead that refused a nonsensical instruction and
  escalated.
- **Purely model lead** — rejected for the cost per task, and because it becomes
  non-deterministic again exactly where we need a guarantee.

## Consequences

- **Positive:** the deterministic layer gives the skeleton; the judgment layer catches the
  skeleton's error. The two protect each other.
- **Negative / costs:** there are two decision paths to maintain instead of one.

## References

- Related documents: [architecture](../architecture/overview.md),
  [references](../references.md)
