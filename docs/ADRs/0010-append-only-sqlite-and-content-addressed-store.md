# ADR-0010: Append-only SQLite + content-addressed store

**Status:** Accepted
**Date:** 2026-08-06

## Context

The FSM state must survive a restart and serve as the task's audit trail. The reference
system uses files as state — the file's location is the state, and every transition is a
rename. It is elegant and inspectable, but our state has relations and queries.

## Decision

The FSM state lives in **append-only SQLite**, without `UPDATE`: the history is the audit
trail. Transitions are atomic — killing the process and starting it again rebuilds the
exact state, because it was never only in memory. Handoff snapshots live in a
**content-addressed store**.

## Alternatives considered

- **Files as state** (what the reference system does) — elegant and inspectable, but
  rejected because it gets expensive when the state has relations and queries.

## Consequences

- **Positive:** safe restart through atomic transitions; the transition log is a complete
  audit trail, with no extra effort.
- **Impacts:** no write path may use `UPDATE` — it is a permanent rule of the store.

## References

- Related documents: [architecture](../architecture/overview.md),
  [references](../references.md)
