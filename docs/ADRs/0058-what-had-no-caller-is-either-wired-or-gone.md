# ADR-0058: What had no caller is either wired or gone

**Status:** Accepted
**Date:** 2026-08-13

## Context

`make deadcode` reported twenty-eight unreachable functions. Reading the list was
uncomfortable, because most of them were mine — written in this same rewrite, tested, given
an ADR, and never called from anywhere but their own tests.

The three groups, and what each one means:

**Built and never wired.** `internal/registry` — the whole beads adapter, with an ADR saying
the registry had moved — had **zero** importers. `node.Merger` likewise: the deterministic
merge, with tests against real conflicts, reachable from nothing. Both are exactly the gap
this project has a rule about: *an ADR records a decision, never progress*, and a decision
that never reached a caller is a decision the code does not make.

**Written with the engine and never called.** `AuditContract`, `AuditRoles` and
`AuditFlowNames` are the contract's static check — the one `AGENTS.md` describes as what
protects the contract. They were called from their own tests and nowhere else, which means
Luna's flow was audited in Luna's test suite while a project's own flow was audited by
nothing. `luna flow check`, the command whose name says it checks the flow, only ever asked
whether tasks were in flight.

**Outlived its purpose.** The content store (`PutBlob`, `Blob`, `BlobCount`,
`AppendWithBlob`) existed so a handoff could carry a snapshot ([ADR-0025](0025-pure-go-sqlite-and-blobs-in-the-same-database.md)).
[RFC-0002](../RFCs/rfc-0002-the-lead-is-an-agent-and-the-commit-is-the-handoff.md) made the
handoff the commit, and git stores content better than a table of blobs.

## Decision

**Wire what should exist; delete what should not.**

- **The registry is wired.** `luna stuck` asks it for tasks blocked in *other* checkouts —
  the one question a per-repository log genuinely cannot answer, and the reason the registry
  became central. It is additive: the local log stays authoritative for anything it knows,
  a task both know is listed once with the log's richer entry, and a registry that is missing
  or unreachable degrades the listing rather than failing it. A watchdog that stops watching
  because a tracker is down stops watching exactly when something is wrong.

- **The merge is wired.** The mechanical stage that closes a task's work integrates the
  role's branch, through `node.Merger`. A conflict blocks the stage with the file named,
  which is what the watchdog can then see.

- **The static check is wired.** `luna flow check` runs the three audits and reports gaps
  before it reports what is in flight. The two questions are different: a flow can have every
  task finished and still be broken on paper.

- **The content store is deleted**, and with it `hashOf` and the `content` parameter that
  threaded through `appendTx` — every caller passed nil once the blob writers went. The
  `blobs` table and the `events.blob` column **stay**: dropping them would rewrite logs
  written before this, and the log is the audit trail (INV-core-2). An empty table costs
  nothing; a migration that discards history costs the thing the store exists for.

**And the docs that had gone stale are corrected** — a `Runner` described as four operations
that had five, a store described as "the log plus the content store", a `lead` package
documented as a Go loop after the agent landed beside it, an `Event.Blob` whose comment
described a snapshot nothing writes any more.

## Alternatives considered

- **Delete the registry and the merger instead** — rejected. They implement decisions that
  were made deliberately and are not being revisited; the defect was that they were not
  connected, not that they were wrong.

- **Leave them and note it in the RFC** — rejected, and this was the tempting one. It is
  precisely the state the 2026-08-11 audit found — seven decisions with no production path —
  and the rule that came out of that audit exists so the answer here is not "later".

- **Run the audits as a CI test rather than in the command** — rejected. It would cover
  Luna's own flow and leave a project unable to check its own, which is the gap that made the
  functions dead in the first place.

- **Drop the `blobs` table and the `blob` column** — rejected. A schema change that makes an
  existing log decode into something it was not is a rewrite of history wearing a migration's
  clothes.

## Consequences

- **Positive:** the dead-code report is down from twenty-eight entries to one, and that one
  (`store.Open`, the read-only constructor) is documented as deliberate.

- **Positive:** `luna stuck` now sees across checkouts, and `luna flow check` checks the flow.
  Both are what their names always claimed.

- **Positive:** coverage rose by deleting code rather than by adding tests, which is the
  cheaper direction and the honest one — the removed branches were unreachable, so tests for
  them would have been theatre.

- **Negative:** `luna stuck` now shells out to `bd` on every run. It degrades to a warning
  when `bd` is missing, which is what a project that has not adopted beads sees.

- **Negative:** three functions moved from `internal/store` to `internal/node` because
  finding a repository root means running git, and the store is forbidden from doing that.
  The boundary is right; the fact that a linter rule is what surfaced it is worth admitting.

- **Impacts:** `internal/registry` (wired into `luna stuck`), `node.Merger` (wired into the
  closing mechanical stage), `fsm.Audit*` (wired into `luna flow check`), and the removal of
  the content store from `internal/store`. [ADR-0025](0025-pure-go-sqlite-and-blobs-in-the-same-database.md)'s
  content store is superseded; its choice of a pure-Go SQLite driver is not, and stands.

## References

- Related documents:
  [RFC-0002](../RFCs/rfc-0002-the-lead-is-an-agent-and-the-commit-is-the-handoff.md),
  [ADR-0025](0025-pure-go-sqlite-and-blobs-in-the-same-database.md) (content store retired
  here), [ADR-0053](0053-the-merge-is-code-and-the-watchdog-reads-a-clock-the-reducer-never-sees.md),
  [ADR-0054](0054-the-registry-is-beads-and-the-flow-is-not.md),
  [ADR-0057](0057-the-log-lives-in-the-main-repository-and-luna-is-its-only-writer.md),
  [INV-core-2](../invariants/core.md), [INV-core-3](../invariants/core.md),
  [INV-core-12](../invariants/core.md)
