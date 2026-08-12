# ADR-0053: The merge is code, and the watchdog reads a clock the reducer never sees

**Status:** Accepted
**Date:** 2026-08-12

## Context

Two problems that look unrelated turn out to be the same problem, which is why
they are decided together.

**The merge.** [RFC-0002](../RFCs/rfc-0002-the-lead-is-an-agent-and-the-commit-is-the-handoff.md)
makes each role work in its own worktree and finish with a commit, so something has to
integrate them. swarm-forge, whose shape this borrows, left that step as the phrase
`merge_and_process` in a prompt — generated on every handoff and **defined nowhere**. Their
[issue #29](https://github.com/unclebob/swarm-forge/issues/29) records a Codex agent passing
the phrase to Bash and stopping on `command not found`. A fork documented the worse outcome:
`git merge -X theirs`, an agent discarding its own work in silence, and *"no consistent
conflict-resolution policy"*.

**The watchdog.** [ADR-0051](0051-one-budget-bounds-a-turn.md) removed the watchdog
interface, and the reason was sound: it asked a replayed `TaskState` whether the task looked
stuck, and a state rebuilt from the log carries no clock. Nothing but a test fake could ever
implement it.

But the failure it was meant to catch is real, and swarm-forge's own `bugs.md` records it
happening to them — a multi-hour stall where the dashboard never surfaced *"merge conflict on
acceptance/runner.clj"*. Something was blocked, nobody knew, and the information existed.

The connection is that **a blocked merge is a fact with a timestamp**. It is the first thing
a watchdog can genuinely observe, which is why the merge and the watchdog land together
rather than three phases apart.

## Decision

**Luna owns the merge, and it is code.**

`node.Merger` performs it, with three properties, all of them borrowed from what swarm-forge
built after the prose version failed:

1. **One owner, checked at the boundary.** `Merger.As` must be `LunaOwnsTheMerge`, and the
   field cannot default to it. swarm-forge enforces the same rule with `exit 3`; the point is
   that it is not a line in a prompt.
2. **The dry run is separate from the merge, and runs in a throwaway worktree.** A conflict
   becomes `merge_blocked` with the conflicted paths named, and the shared repository is
   untouched. Finding out by attempting it would leave the repository mid-merge, and
   unwinding that is a second thing to go wrong at the moment the first one already did.
3. **Nothing is resolved automatically.** No `-X theirs`, no `--ours`, no `rerere`. A
   conflict is a verdict. Resolving one is a stage with a contract, and its output re-enters
   the same deterministic path.

The verdict is produced outside the engine and arrives inside the action, which is
[ADR-0024](0024-the-reducer-is-pure-verification-runs-outside.md) applied to merging.

**The log records when each event was written, and the reducer never reads it.**

`events.at` is a unix timestamp written on append. `Replay` does not read it, and nothing in
`fsm` can: it is metadata about the log rather than part of the state. The store is the only
layer that reads a clock at all, and `Store.Now` is injectable so a watchdog can be tested
without a test sleeping for three hours.

**The watchdog is a query, not a loop.** `Store.Stalled(flow, patience)` returns what has
been stopped for longer than the patience — blocked tasks and unanswered gates alike, because
a planned pause nobody answers for six hours is indistinguishable from a stall to whoever is
waiting behind it. `luna stuck --notify` is the surface. Whatever polls decides how often,
so nothing has to stay running for a stall to be noticed.

## Alternatives considered

- **Let a model do the merge** — rejected outright. It is the failure the study named, it is
  documented in swarm-forge's own issue tracker, and `git merge` is decidable. Handing a
  decidable question to a model gives up the only real advantage Luna has over a prompt
  chain.

- **Auto-resolve with a strategy flag** — rejected. `-X theirs` makes every conflict
  disappear, which is exactly why it is dangerous: the work it silently discards is not
  reported, and the fork that tried it watched an agent throw away its own commit.

- **One call that merges and reports what happened** — rejected. The two-call shape costs an
  extra worktree and buys the guarantee that a failed integration never touches the shared
  repository. That is worth more than the checkout.

- **Refuse every conflict and ask a person** — rejected as the cheaper floor that does not
  hold. A conflict is ordinary once several agents share a base, and a system that stops on
  each one is a system somebody babysits. The `merger` role exists for this.

- **Put the timestamp in `TaskState`** — rejected, and this is the line that matters. A state
  carrying a clock makes a replay depend on when it ran, which ends the reproducibility
  ADR-0024 exists for. The watchdog reads the log; the reducer reads the state; they do not
  meet.

- **A watchdog goroutine inside Luna** — rejected. It would mean a process has to be running
  for a stall to be noticed, and the stall this guards against is one where everything has
  stopped. A query answered by whatever is already scheduled has no such gap.

- **Report only blocked tasks, not gates** — rejected. From the outside they are the same
  event: something is waiting on a person and nobody has come. The status still distinguishes
  them in the report.

## Consequences

- **Positive:** a conflict is legible. The verdict names the files, so the alert says where
  to look rather than that something is wrong somewhere.

- **Positive:** the merge is testable without an agent, and it is tested against real git
  repositories with real conflicts. A `-X theirs` added later fails three tests, one of which
  asserts on the strategy rather than the outcome.

- **Positive:** the watchdog finally has something real to watch, and a bounded patience
  means nothing waits indefinitely unseen (INV-core-8, INV-core-12).

- **Negative:** the dry run costs a worktree checkout per merge attempt. It shares the object
  database, so it is a checkout rather than a clone, and the guarantee is worth it.

- **Negative:** a log written before this carries no timestamps, so those tasks report no age
  and are never called stuck. Reporting them as stuck since the epoch would train people to
  ignore the alert, which is the way a watchdog actually dies.

- **Negative:** the schema gained a column, and `CREATE TABLE IF NOT EXISTS` cannot add one.
  `addMissingColumns` handles it, which is a migration mechanism this project did not have
  and now has to maintain. Adding a column is not a rewrite: every existing row keeps what it
  recorded and gains a default (INV-core-2).

- **Impacts:** `node.Merger`, `node.throwawayCheckout` (extracted from `CheckoutDelivered`,
  which was doing the same thing), `store.Stalled`, `store.Stuck`, `Store.Now`, the `events.at`
  column, and `luna stuck`.

## References

- Related documents:
  [RFC-0002](../RFCs/rfc-0002-the-lead-is-an-agent-and-the-commit-is-the-handoff.md)
  (phases 1b and 1c), [ADR-0024](0024-the-reducer-is-pure-verification-runs-outside.md),
  [ADR-0051](0051-one-budget-bounds-a-turn.md) (removed the watchdog this restores on a
  different mechanism), [ADR-0019](0019-inactivity-watchdog.md),
  [ADR-0023](0023-three-separate-loop-ceilings.md),
  [INV-core-8](../invariants/core.md), [INV-core-12](../invariants/core.md)
- Prior art: [references](../references.md) — swarm-forge's `squad` branch
  (`ensure-main-git-owner!`, `dry-run-merge`), and its `bugs.md`
