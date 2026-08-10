# Decision records (ADR)

This folder holds the project's **Architecture Decision Records**: the record of
*why* we took each relevant technical or product decision, in the context of the
moment it was taken. See the general standards in [docs/README.md](../README.md).

## What an ADR is

An ADR captures a single significant decision — a structural, contract, process or
product choice whose *why* is worth preserving. It is not documentation of the
current state (that is [architecture](../architecture/)); it is the dated record of
the decision that led to it.

Record an ADR when the decision:
- affects the structure, a contract or a boundary of the system;
- has real alternatives that were discarded;
- would be costly or confusing to reverse without understanding the original reason.

Do not record an ADR for trivial choices, or ones reversible at no cost.

## Convention

- **Immutable.** An accepted ADR is **never edited**. It is the record of a moment.
  Did the decision change? Create a new ADR (see below).
- **Numbered.** Files follow `NNNN-title-in-kebab.md`, with `NNNN` sequential and
  zero-padded from `0001`. The `0000-template.md` is the model and does not count as
  a decision.
- **Status.** Every ADR carries a status at the top:
  - `Proposed` — under discussion, not decided yet.
  - `Accepted` — the decision in force.
  - `Superseded by [ADR-NNNN](NNNN-title.md)` — revised by a later ADR.
  - `Rejected` — a proposal evaluated and not adopted (kept for the record).

## How to add an ADR

1. Copy `0000-template.md` to `NNNN-title.md`, using the next free number.
2. Fill in context, decision and consequences. Start at `Proposed`.
3. When the call is made, change the status to `Accepted`.

## How to supersede a decision

1. Create a **new** ADR describing the revised decision and the reason for the change.
2. In the old ADR, change the status to
   `Superseded by [ADR-NNNN](NNNN-title.md)` — **that is the only edit allowed** in an
   accepted ADR.
3. Update the living [architecture](../architecture/) to reflect the new state.

## Index

<!-- List the ADRs here as they are created, from newest to oldest. -->
<!-- - [ADR-0001](0001-title.md) — <title> — `Accepted` -->

- [ADR-0033](0033-losing-herdr-blocks-the-task.md) — Losing herdr blocks the task — `Accepted`
- [ADR-0032](0032-the-contract-declares-how-each-artifact-is-verified.md) — The contract declares how each artifact is verified — `Accepted`
- [ADR-0031](0031-agents-start-through-herdrs-allowlist.md) — Agents start through herdr's allowlist — `Accepted`
- [ADR-0030](0030-the-node-boundary-keeps-herdr-replaceable.md) — The node boundary keeps herdr replaceable — `Accepted`
- [ADR-0029](0029-herdr-blocked-becomes-a-luna-block.md) — herdr's `blocked` becomes a Luna block — `Accepted`
- [ADR-0028](0028-herdr-status-triggers-verification-it-never-closes-a-stage.md) — herdr's status triggers verification; it never closes a stage — `Accepted`
- [ADR-0027](0027-luna-runs-under-herdr-as-a-socket-client.md) — Luna runs under herdr as a socket client — `Accepted`
- [ADR-0026](0026-the-log-records-the-gate-decision-not-the-policy.md) — The log records the gate decision, not the policy that produced it — `Accepted`
- [ADR-0025](0025-pure-go-sqlite-and-blobs-in-the-same-database.md) — Pure-Go SQLite, with the blobs in the same database — `Accepted`
- [ADR-0024](0024-the-reducer-is-pure-verification-runs-outside.md) — The reducer is pure; verification runs outside it — `Accepted`
- [ADR-0023](0023-three-separate-loop-ceilings.md) — The loop has three ceilings counted separately — `Accepted`
- [ADR-0022](0022-gate-carries-artifact-for-review.md) — The gate carries the artifact, and the human can adjust it — `Accepted`
- [ADR-0021](0021-produces-for-human-is-a-separate-contract-field.md) — What only the human reads is a contract field of its own — `Accepted`
- [ADR-0020](0020-review-finding-invalidates-green.md) — An aligned finding invalidates the green when going back to build — `Accepted`
- [ADR-0019](0019-inactivity-watchdog.md) — The inactivity watchdog watches the work, not the window — `Accepted`
- [ADR-0018](0018-tool-gating-by-pretooluse-hook.md) — Tool gating is a blocking hook, not an instruction to the agent — `Accepted`
- [ADR-0017](0017-defaults-plus-customization-everywhere.md) — Defaults + customization, everywhere — `Accepted`
- [ADR-0016](0016-cli-first.md) — CLI first — `Accepted`
- [ADR-0015](0015-core-knows-no-issue-tracker.md) — The core knows no issue tracker — `Accepted`
- [ADR-0014](0014-conditional-stages.md) — Conditional stages — `Accepted`
- [ADR-0013](0013-named-gate-profiles-per-task.md) — Named gate profiles, chosen per task — `Accepted`
- [ADR-0012](0012-gate-suspends-and-frees-the-slot.md) — A gate suspends and frees the slot — `Accepted`
- [ADR-0011](0011-failure-retry-rollback-or-block.md) — Failure — retry up to 2, then block with a notice — `Accepted`
- [ADR-0010](0010-append-only-sqlite-and-content-addressed-store.md) — Append-only SQLite + content-addressed store — `Accepted`
- [ADR-0009](0009-go.md) — Go as the implementation language — `Accepted`
- [ADR-0008](0008-system-generated-payload.md) — The payload is generated by the system — `Accepted`
- [ADR-0007](0007-handoff-carries-pointers-and-snapshot.md) — The handoff carries pointers and a snapshot, not a summary — `Accepted`
- [ADR-0006](0006-fresh-context-per-stage.md) — Fresh context at every stage — `Accepted`
- [ADR-0005](0005-validate-output-by-running-the-tool.md) — Output is validated by running the tool — `Accepted`
- [ADR-0004](0004-stage-requires-produces-contract.md) — Every stage declares `requires` and `produces` — `Accepted`
- [ADR-0003](0003-parallelism-between-tasks.md) — Parallelism between tasks, not within — `Accepted`
- [ADR-0002](0002-hybrid-lead.md) — The lead is hybrid, not purely deterministic — `Accepted`
- [ADR-0001](0001-flow-control-out-of-model.md) — Flow control leaves the model — `Accepted`
