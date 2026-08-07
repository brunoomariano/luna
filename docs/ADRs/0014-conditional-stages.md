# ADR-0014: Conditional stages

**Status:** Accepted
**Date:** 2026-08-06

## Context

The default flow includes heavy review stages — QA, code review, mutation testing,
architecture review. Running all of them on any change, regardless of size, is possible,
but it has a cost.

## Decision

Stages are **conditional**: heavy review stages do not run on a trivial task. Each stage
declares the condition under which it enters the flow.

## Alternatives considered

- **Run everything always** — rejected because mutation testing on a one-line change is
  ceremony, and ceremony trains the human to ignore the process.

## Consequences

- **Positive:** the cost of review follows the size of the change; the process does not
  lose credibility through excess ritual.
- **Impacts:** each stage must declare its condition, in addition to the
  `requires`/`produces` contract.

## References

- Related documents: [default stages](../architecture/stages.md),
  [architecture](../architecture/overview.md)
