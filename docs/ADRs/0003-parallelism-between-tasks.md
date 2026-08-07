# ADR-0003: Parallelism between tasks, not within

**Status:** Accepted
**Date:** 2026-08-06

## Context

An agent orchestrator can parallelize along two axes: run several tasks at the same time,
or run several concurrent agents within the same task. The reference system chose the
second, with a worktree per agent and message passing between them.

## Decision

Parallelism is **between tasks**. Each task has one lead and one worktree; the stages
within it are sequential.

## Alternatives considered

- **Concurrent agents per role within the same task** — rejected because it requires a
  queue per role and message transport between live agents. That is complexity that only
  pays off when the bottleneck is the individual task — which is not our case.

## Consequences

- **Positive:** no need for a queue per role nor transport between live agents; the FSM is
  the only channel within the task.
- **Impacts:** the worktree becomes per task, not per agent.

## References

- Related documents: [architecture](../architecture/overview.md),
  [references](../references.md)
