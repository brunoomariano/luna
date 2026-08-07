# ADR-0016: CLI first

**Status:** Accepted
**Date:** 2026-08-06

## Context

The FSM still has to prove its value. Any interaction surface built before that is work
invested on a design that may change.

## Decision

The interface is **CLI first**. A visual interface comes in when the flow stabilizes.

## Alternatives considered

- **A visual interface from the start** — rejected for being too much work before the FSM
  proves its value.

## Consequences

- **Positive:** the effort stays concentrated on the core while the design still changes.
- **Impacts:** interaction with gates and task tracking happen in the terminal.

## References

- Related documents: [architecture](../architecture/overview.md)
