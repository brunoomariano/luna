# ADR-0012: A gate suspends and frees the slot

**Status:** Accepted
**Date:** 2026-08-06

## Context

A gate stops the task and waits for a human decision. With N tasks in parallel (see
[ADR-0003](0003-parallelism-between-tasks.md)), the shape of that wait decides how many
resources sit idle while the human decides.

## Decision

The gate does a short wait in the terminal; with no answer, it **suspends** the task and
frees the slot. Another task uses the resource while you decide, and approval resumes from
the exact point.

## Alternatives considered

- **A live agent waiting** — rejected because with N tasks in parallel there would be N
  idle processes, with aging context.

## Consequences

- **Positive:** the resource is not held hostage to human latency; no context ages while
  waiting.
- **Impacts:** it requires the suspended state to be resumable from the exact point — which
  the append-only store (see
  [ADR-0010](0010-append-only-sqlite-and-content-addressed-store.md)) supports.

## References

- Related documents: [architecture](../architecture/overview.md),
  [references](../references.md)
