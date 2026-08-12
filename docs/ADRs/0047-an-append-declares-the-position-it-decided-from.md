# ADR-0047: An append declares the position it decided from

**Status:** Accepted
**Date:** 2026-08-12

## Context

Luna is a CLI with no daemon and a per-directory store, so nothing stops two `luna`
processes from running against the same `.luna/luna.db`. That is not a hypothetical
arrangement: [ADR-0012](0012-gate-suspends-and-frees-the-slot.md) describes a gate that
frees the slot and is later approved from somewhere else, and
[ADR-0003](0003-parallelism-between-tasks.md) makes parallelism between tasks the point of
the design.

The lead writes by reading first. `record` replays the task, applies the action to that
state as a dry run to check it is legal, and then appends. Between the replay and the
append there is a window, and nothing closed it: `appendTx` computed the next sequence
inside its own transaction, which prevents a duplicate sequence and nothing else.

Two problems, both reproduced by probe before anything was changed.

**A decision made against a state that has moved still lands.** Two processes replay the
same task, both see `ready`, both validate an `Advance` against that snapshot, and both
write. The second is accepted because it was checked against a state that was true when it
was read. The log then holds two decisions taken from one state, and every replay after
that fails:

```
FINAL REPLAY: replaying LUNA-1 at seq 3: illegal transition: a gate is pending on "discovery"
```

There is no repair. The store has no `UPDATE` and no `DELETE` by design
([INV-core-2](../invariants/core.md)), so the task is unreadable for good — and because
`luna gates` replays every task, one poisoned log used to take the whole listing with it.

**Concurrent appends mostly failed.** With a plain `sql.Open`, twenty concurrent appends
lost nineteen to `SQLITE_BUSY`. Each loss surfaced as an error from `lead.Run`, which
aborts the task **without recording a block** — a task that stops and is not in
`luna gates` is the silent failure [INV-core-8](../invariants/core.md) forbids.

The second problem turned out to have three causes, and finding them in order matters
because the first two look like the whole fix and are not:

1. no WAL and no busy handler — the pragmas;
2. `database/sql` pooling two connections per store, which race for the write lock without
   either going through the busy handler;
3. **a deferred `BEGIN`** — the one that actually mattered. A deferred transaction takes a
   read lock and asks to upgrade it on first write, and SQLite refuses that upgrade with
   `SQLITE_BUSY` *immediately*, deliberately skipping the busy handler, because two readers
   both waiting to upgrade would deadlock. With the pragmas and the pool fixed, eight of
   twenty still failed.

## Decision

**An append that follows a read declares the position it read from, and lands only if the
log still ends there.**

`AppendActionAt(taskID, after, action)` takes the sequence the caller's state was replayed
from. Inside the write transaction the store compares it against the log's last sequence
and refuses with `ErrConcurrentWrite` when they differ. `lead.record` passes `state.Seq` —
the same state its dry run validated against.

The unconditional `Append` stays for callers that did not read first: creating a task,
storing a handoff blob. Passing a position they never read would be a fiction.

**And the write path takes the write lock up front.** `BEGIN IMMEDIATE`, WAL,
`busy_timeout(5000)`, and one connection per store. Together these turn contention into a
wait: twenty concurrent appends across two handles on one file now lose none.

That `state.Seq` equals the log's last sequence is the invariant this rests on — every
applied action is exactly one event. It is pinned by a test rather than assumed, because if
it ever stopped holding, every conditional append would be refused against a position
nobody could reach.

## Alternatives considered

- **A lock file, or a lock table in SQLite** — rejected as heavier and worse. It would
  serialise commands that do not conflict (two tasks advancing independently), and a
  crashed process leaves a stale lock someone has to clear by hand. The conditional append
  refuses exactly the writes that actually conflict.

- **A daemon owning the store** — rejected for this problem, and it is worth recording why
  since it is the obvious answer. Luna already runs under herdr, which is a resident
  process; a second one would mean two supervisors with their own view of what is alive.
  More to the point, the cost is out of proportion: the conflict here is one comparison
  inside a transaction that already exists.

- **Retrying on conflict, inside the store** — rejected because the store cannot know
  whether the decision is still valid. The action was chosen against a state that has since
  moved; replaying and re-deciding is the caller's business, and the caller is the lead,
  which has the flow and the profile. Silently retrying would be the store making a flow
  decision.

- **`SetMaxOpenConns(1)` alone** — considered and kept, but it is not sufficient and it was
  briefly mistaken for the fix. It serialises one store's own writers and does nothing for
  a second process, which is the case that matters.

## Consequences

- **Positive:** the failure mode with no recovery is gone. A stale decision is refused with
  a message naming where the task was and where it is, instead of corrupting a log that
  cannot be repaired.

- **Positive:** contention became a wait. The `SQLITE_BUSY` path that aborted tasks without
  recording a block does not fire.

- **Negative:** `ErrConcurrentWrite` is a new failure the lead does not yet handle — it
  propagates as an error from `luna run`. Refusing loudly is strictly better than the
  silent corruption it replaces, but re-reading and re-deciding is the obvious next step
  and is not built.

- **Negative:** `beginImmediate` issues `ROLLBACK; BEGIN IMMEDIATE` on a transaction
  `database/sql` has already opened, because the package offers no way to ask for anything
  but a deferred `BEGIN`. It works and is tested, but it is a wart, and a driver that
  changes how it handles the statement would break it quietly.

- **Impacts:** every writer that reads first uses the conditional form — `lead.record`,
  the gate answers, and `unblock`. Two deliberately do not: `task new` has nothing to have
  read, and `task abandon` is the one command that must work when replay fails
  ([ADR-0046](0046-the-log-records-which-flow-it-was-written-under.md)), so demanding a
  position it could not obtain would defeat it.

## References

- Related documents: [ADR-0025](0025-pure-go-sqlite-and-blobs-in-the-same-database.md),
  [ADR-0046](0046-the-log-records-which-flow-it-was-written-under.md),
  [INV-core-2](../invariants/core.md), [INV-core-8](../invariants/core.md)
