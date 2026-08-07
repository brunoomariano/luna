# ADR-0015: The core knows no issue tracker

**Status:** Accepted
**Date:** 2026-08-06

## Context

A task can come from an issue tracker, from another tracker, or from nowhere at all.
Integrating natively with one of them would make the core dependent on that tool.

## Decision

The FSM **knows no issue tracker**. A task enters through a creation command of its own or
through an **import adapter**, which is a command separate from the core.

## Alternatives considered

- **Native integration with Plane** — rejected because it would tie the system to a
  specific tool. A task can come from anywhere, or from nowhere at all.

## Consequences

- **Positive:** swapping or adding a task origin does not touch the core.
- **Impacts:** each supported origin requires its own import adapter.

## References

- Related documents: [architecture](../architecture/overview.md)
