# ADR-0007: The handoff carries pointers and a snapshot, not a summary

**Status:** Accepted
**Date:** 2026-08-06

## Context

With fresh context at every stage (see [ADR-0006](0006-fresh-context-per-stage.md)), the
handoff is the only bridge between stages. What it carries determines whether the receiver
sees the real state or another agent's interpretation of it.

## Decision

The handoff carries **pointers** — task identifier, source stage, produced artifacts,
earlier gate decisions — and a **content-addressed snapshot**: the hash of what existed at
the moment of the handoff, so that the receiver sees exactly what the sender saw, even if
the worktree changed afterwards.

The content-addressed snapshot gives the immutability a commit would give, without tying
the transport to versioning.

## Alternatives considered

- **A prose summary of what the stage did** — rejected because it reintroduces
  interpretation into the chain, which is precisely the degradation we want to eliminate.
- **Git as the channel** — it works (it is what the reference system does), but rejected
  because it ties the transport to versioning.

## Consequences

- **Positive:** the receiver reads the real state; nobody interprets it for them.
- **Impacts:** it requires a hash-addressed content store for the snapshots.

## References

- Related documents: [architecture](../architecture/overview.md),
  [references](../references.md)
