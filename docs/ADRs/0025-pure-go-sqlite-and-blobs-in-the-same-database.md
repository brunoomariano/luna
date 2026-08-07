# ADR-0025: Pure-Go SQLite, with the blobs in the same database

**Status:** Accepted
**Date:** 2026-08-07

## Context

[ADR-0010](0010-append-only-sqlite-and-content-addressed-store.md) settled that state
lives in append-only SQLite and that handoff snapshots live in a content-addressed
store. Building either one forces two choices it left open, and both interact with
decisions already taken.

**Which SQLite.** This is the project's first external dependency — `go.mod` has none
today. The mature option, `mattn/go-sqlite3`, binds the real C library through CGO.
That collides with [ADR-0009](0009-go.md), which chose Go for a single binary with no
runtime on the target machine: with CGO, cross-compiling needs a C toolchain and the
binary stops being static.

**Where the blobs go.** A content store can live in the filesystem
(`.luna/blobs/ab/cdef…`) or as rows in the same database. ADR-0010 also says
transitions are atomic, and atomicity across two systems is something you build rather
than something you get.

## Decision

**`modernc.org/sqlite`** — SQLite transpiled to Go, no CGO.

**Blobs as rows in the same database**, keyed by their sha256.

```sql
CREATE TABLE events (
    task_id  TEXT    NOT NULL,
    seq      INTEGER NOT NULL,   -- ordering within the task
    action   TEXT    NOT NULL,
    payload  TEXT    NOT NULL,
    PRIMARY KEY (task_id, seq)
);

CREATE TABLE blobs (
    sha256   TEXT PRIMARY KEY,
    content  BLOB NOT NULL
);
```

Writing an event and the blob it points at is one transaction:

```sql
BEGIN;
  INSERT OR IGNORE INTO blobs (sha256, content) VALUES (?, ?);
  INSERT INTO events (task_id, seq, action, payload) VALUES (?, ?, ?, ?);
COMMIT;
```

There is no `UPDATE` and no `DELETE` anywhere in the write path (INV-core-2). Current
state is not stored; it is replayed from the events through `Reduce`, which
[ADR-0024](0024-the-reducer-is-pure-verification-runs-outside.md) keeps pure precisely
so that replaying is deterministic.

## Alternatives considered

- **`mattn/go-sqlite3`** — rejected. It is faster and more battle-tested, and on the
  scale this store operates at — tens of events per task — neither advantage is
  reachable. What it costs is the property ADR-0009 was chosen for: `go install` stops
  working without a C toolchain, and `GOOS=darwin go build` from Linux stops working at
  all. Paying that for performance nobody will observe is the wrong trade.
- **Blobs in the filesystem** — rejected. It handles large artifacts better and can be
  inspected with ordinary tools, but it makes atomicity a problem to solve rather than
  one to inherit: a process that dies between writing the blob and recording the event
  leaves either an orphan file or a dangling reference. The handoff carries pointers and
  a snapshot, not the whole worktree, so the size argument does not weigh much here.
- **Storing current state alongside the log** — rejected for now. It would make reads
  cheap, but derived state in an append-only store is a cache, and a cache that drifts
  is a store that lies. If replay ever becomes slow enough to matter, a periodic
  snapshot with the event sequence it was taken at is the shape to reach for — and it
  deserves its own ADR, because the failure mode is a snapshot that disagrees with the
  log it came from.

## Consequences

- **Positive:** `CGO_ENABLED=0` keeps working, so the binary stays static and
  cross-compiles. Atomicity across state and content comes from one transaction rather
  than from careful ordering. One file per store means backup is a copy.
- **Negative / costs:** `modernc.org/sqlite` is a transpilation, not the C original — a
  larger dependency, and one that will lag upstream SQLite. Large artifacts are read
  fully into memory, which is fine for a handoff pointer and would not be for a
  worktree.
- **Impacts:**
  - `go.mod` gains its first dependency, so `govulncheck` and `depguard` start having
    something to guard;
  - `depguard` must allow the driver in `internal/store` while continuing to deny it in
    `internal/fsm` — the engine stays free of I/O;
  - replaying the whole log on every read is accepted for now; the moment it is not,
    the snapshot question above comes back.

## References

- Related documents: [ADR-0009](0009-go.md), [ADR-0010](0010-append-only-sqlite-and-content-addressed-store.md),
  [ADR-0024](0024-the-reducer-is-pure-verification-runs-outside.md),
  [invariants](../invariants/core.md)
