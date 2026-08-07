# ADR-0017: Defaults + customization, everywhere

**Status:** Accepted
**Date:** 2026-08-06

## Context

Luna's default flow is the design of whoever built it. Someone else will have another one —
and a tool with a fixed flow serves a single user.

## Decision

Everything that defines behavior has a **default version and a user version**: stages,
roles, gate profiles, loops and skills. The default comes installed; the user can disable,
edit or create their own.

## Alternatives considered

- **Fixed flow** — rejected because the default design is ours, and someone else will have
  another one. Without extension, the tool serves a single user.

## Consequences

- **Positive:** Luna serves flows different from the one that originated it.
- **Impacts:** each axis of behavior needs a declared extension point — from `src/stock/`
  as the default to the user configuration as the overlay.

## References

- Related documents: [architecture](../architecture/overview.md),
  [default stages](../architecture/stages.md)
