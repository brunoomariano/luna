# Architecture: the decisions, as built

> Where the **recorded decisions** and the **code** differ today, and why. Living
> document: keep it truthful. See the standards in [docs/README.md](../README.md).

## Overview

An ADR is immutable: it says why we decided something *at that moment*, and it is
never edited to match what happened next ([docs/ADRs/README.md](../ADRs/README.md)).
That is the right rule, and it has a cost — a reader who takes the ADRs as a
description of the system will be wrong in a handful of specific places.

This page is that handful. It exists because an audit of all 62 accepted ADRs
against `src/` on 2026-08-17 found them, and leaving the findings in a chat log
would have meant auditing again next time.

Two things it is not. It is not a decision — nothing here overrides an ADR, and a
real change of mind becomes a new ADR. And it is not a to-do list: everything
below is either deliberate or recorded elsewhere as work.

## What the audit found

Every accepted ADR has its decision built, or has a successor that revised it.
**None is recorded and unimplemented.** What follows are the divergences worth
knowing about before reading the record.

### Revised by a later ADR — the mechanism changed, the intent held

| ADR | What it says | What the code does |
|---|---|---|
| [0018](../ADRs/0018-tool-gating-by-pretooluse-hook.md) | tools are denied by a `PreToolUse` hook | now marked superseded — [ADR-0042](../ADRs/0042-four-harnesses-four-ways-to-deny-a-tool.md) denies through each harness's own flags |
| [0026](../ADRs/0026-the-log-records-the-gate-decision-not-the-policy.md) | the profile's `waits` list decides which gates stop | the recorded-decision half is load-bearing; the config half went with [ADR-0063](../ADRs/0063-a-gate-waits-because-a-stage-declared-something-to-answer-it-with.md). `ShippedPolicy` survives as the replay fallback and nothing else |
| [0034](../ADRs/0034-the-watchdog-delegates-detection-and-owns-the-verdict.md) | herdr detects, Luna decides, limits come from the profile | detection and verdict are exactly as written; the two budgets became one turn budget ([ADR-0051](../ADRs/0051-one-budget-bounds-a-turn.md)) |
| [0007](../ADRs/0007-handoff-carries-pointers-and-snapshot.md) · [0010](../ADRs/0010-append-only-sqlite-and-content-addressed-store.md) · [0025](../ADRs/0025-pure-go-sqlite-and-blobs-in-the-same-database.md) | the handoff carries a content-addressed snapshot | one gap, three ADRs. The snapshot is the git commit (RFC-0002, [ADR-0055](../ADRs/0055-one-worktree-per-task-and-role-branched-from-the-last-delivery.md)); the `blobs` table is kept unwritten so old logs still decode |
| [0053](../ADRs/0053-the-merge-is-code-and-the-watchdog-reads-a-clock-the-reducer-never-sees.md) | the merge is Luna's, with a dry run and no auto-resolution | the watchdog half is built; the merge left with [ADR-0062](../ADRs/0062-luna-does-not-integrate-a-task-ends-on-its-own-branch.md) and its code was removed |

### Corrected in code, and the correction is only in a comment

**The role branch is `luna/<task>-<role>`, not `luna/<task>/<role>`.**
[ADR-0055](../ADRs/0055-one-worktree-per-task-and-role-branched-from-the-last-delivery.md)
writes the slash form. It cannot work: a ref named `luna/LUNA-1/build` makes
`luna/LUNA-1` a directory, and the roleless branch `setup` creates then fails with
`cannot lock ref`. `internal/herdr/node.go` records the measurement at the point
where the name is built.

The substance of the ADR — one worktree per task *and* role, branched from the last
delivery, removed when the stage ends — is built as written.

### Two things worth not misreading

**Agents are not reused across stages.** `internal/herdr/runner.go` reuses an agent
when herdr says the name is taken, and the comment used to read as though the agent
belonged to the task. It does not: `agentName` includes the stage, so the reuse path
only ever finds an agent *this same stage* started — a retry after a stall, or a
resumed run. A later stage asks for a different name and gets a fresh agent, which is
what [INV-core-5](../invariants/core.md) and
[ADR-0006](../ADRs/0006-fresh-context-per-stage.md) require. The comment now says so.

**`ADR-0060`'s equivalence proof is gone, and something better replaced it.** It
proved the parsed stock and the retired Go literals fingerprint identically. The
literals were deleted once the move landed, so nothing recomputed the digest and the
shipped fingerprint was unpinned. `TestTheShippedFlowFingerprintIsPinned` now writes
it down, which is the guarantee the ADR was reaching for — an accidental stock edit
strands every open task, and this is what notices.

## Known unbuilt, recorded elsewhere

Not divergences — work that is named and not done.

- **`events.subscribe`** ([ADR-0027](../ADRs/0027-luna-runs-under-herdr-as-a-socket-client.md),
  [ADR-0029](../ADRs/0029-herdr-blocked-becomes-a-luna-block.md)). Luna learns herdr's
  state only as the return value of its own `agent.prompt`. A pane closed behind Luna's
  back, or an agent that goes `blocked` between stages, is invisible.
- **The second timer** (PRD node-0002). Deliberately deferred; the PRD says why.
- **`registry.RecordCommit` / `Commits`** ([ADR-0065](../ADRs/0065-the-registry-is-a-projection-and-the-log-is-the-state.md)).
  Left unwired on purpose: the commit is the handoff and lives in the log.

## References

- [docs/ADRs/README.md](../ADRs/README.md) — why an ADR is immutable
- [ADR-0058](../ADRs/0058-what-had-no-caller-is-either-wired-or-gone.md) — what has no
  caller is either wired or gone, which is what this audit applied
