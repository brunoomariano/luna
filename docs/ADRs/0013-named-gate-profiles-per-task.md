# ADR-0013: Named gate profiles, chosen per task

**Status:** Superseded by [ADR-0063](0063-a-gate-waits-because-a-stage-declared-something-to-answer-it-with.md)
**Date:** 2026-08-06

## Context

Not every task deserves the same level of human supervision. What remains is deciding which
dimension governs which gates stop.

## Decision

Which gates stop is decided by a **named profile, chosen per task** — for example
`interactive` (every gate waits for a human), `turbo` (only the write waits) and `nightly`
(nothing waits).

## Alternatives considered

- **Profile per task type** — rejected because the type does not predict the risk: a
  critical bug may deserve more gating than a trivial feature.
- **Profile per repository** — rejected because it does not distinguish a risky task from a
  trivial one within the same code.

## Consequences

- **Positive:** the level of supervision follows the task's real risk, and not a category
  that merely approximates it.
- **Impacts:** the profile becomes a task parameter, chosen at intake.

## References

- Related documents: [architecture](../architecture/overview.md)
