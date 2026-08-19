# ADR-0071: a scratch artifact is handed over through a socket

**Status:** Accepted
**Date:** 2026-08-19

## Context

The handoff is the commit ([ADR-0055](0055-one-worktree-per-task-and-role-branched-from-the-last-delivery.md),
[INV-core-6](../invariants/core.md)), and for what the repository is for — code, tests,
project documentation — that stands. It is wrong for the rest: `contract`, `scenarios`,
`approach`, `min_case` and the four audit reports are scaffolding, produced so the next stage
or a person can decide something, and committing them puts working notes into the delivered
history of somebody else's repository.

Two invariants pointed at the gap from opposite sides. [INV-core-11](../invariants/core.md)
wants the handoff to carry each audit artifact's location, and the location existed only
inside one `git ls-tree` call before surviving as prose. [INV-core-12](../invariants/core.md)
wants a human-facing artifact locatable by command, and `luna task show` read exactly the map
those artifacts are excluded from by design ([ADR-0021](0021-produces-for-human-is-a-separate-contract-field.md))
— so `qa_report` was verified, evidenced, and listed nowhere.

[ADR-0058](0058-what-had-no-caller-is-either-wired-or-gone.md) had deleted the content store
because [RFC-0002](../RFCs/rfc-0002-the-lead-is-an-agent-and-the-commit-is-the-handoff.md)
made the commit the handoff and git stores content better than a table of blobs. That
argument was about the artifacts that belong in the repository; the question of what should
*not* be committed had not been asked, because at the time everything went to git anyway.
This is the new argument [AGENTS.md](../../AGENTS.md) requires to reopen a decision — the
purpose is different, not the verdict relitigated.

## Decision

**An artifact declared `handover = "store"` is written to Luna's store through a Unix socket
Luna opens inside the stage's worktree, and git never sees it.**

The agent's interface is `luna artifact put | get`. The CLI does not open the database — it
dials the socket, and Luna, outside the sandbox, validates, hashes and appends.

Each part was measured rather than chosen ([RFC-0008](../RFCs/rfc-0008-a-scratch-artifact-is-handed-over-through-a-socket.md)
holds the measurements):

- **The socket lives inside the worktree** because that is the only position a contained
  agent reaches: under Landlock, a socket in `$HOME`, in `/tmp`, or behind a symlink out of
  the working directory answers `ENOENT`; a real socket under the cwd connects, and the
  process behind it writes wherever it likes. It is the shape Luna already uses for herdr
  ([ADR-0027](0027-luna-runs-under-herdr-as-a-socket-client.md)).
- **Luna is the only writer.** The socket takes `put` and `get`; the agent never holds the
  log, which is the audit ([INV-core-2](../invariants/core.md)). The stage a blob is
  attributed to comes from the server, never from the request — nothing an agent says can
  credit its work to somebody else, and a request naming no artifact is refused at the
  socket, because the boundary that decides is the one that validates
  ([ADR-0028](0028-herdr-status-triggers-verification-it-never-closes-a-stage.md), one layer
  down; a real agent got an unnamed blob stored before the socket checked).
- **The key is per stage** — `(task, stage, artifact, seq)`. The pair without the stage
  collides in practice: `build` and `refactor` both produce `code`, a loop revisits a stage,
  a gate replaces an artifact with the person's version. Keyed by stage, authorship is free
  and INV-core-11's "each artifact produced" is expressible; the plain read loses nothing,
  since `luna artifact get` with no stage named returns the newest from any.
- **`handover = "store"` enters the flow fingerprint**, where `path`
  ([ADR-0070](0070-an-artifact-can-declare-where-it-lives.md)) does not. A path changes where
  a delivery is looked for; this changes what delivering *means* — a past `Complete` that
  closed on a committed file cannot replay against a store row.
- **The exit check asks the store**, and the evidence carries the content's sha256 — the
  location INV-core-11 asks the handoff to hold, and what lets `luna gate show` print the
  artifact itself instead of a hash naming it.

## Consequences

- **The delivered tree is clean.** Validated on a full cycle: twelve stages, seven artifacts
  through the socket, and the delivery holding only what the repository is for.
- **A stage that does not hand over does not close.** The store is the witness; an agent
  that closed its turn without the put blocks the stage with the artifact named — measured,
  on the same run that used to close green on nothing (RFC-0004's original failure).
- **The store grows and must be emptiable.** Deleting a task's blobs is one statement, and
  the log keeps the hashes — the content goes, the history of what was produced does not.
- **The shipped fingerprint moved** (`e29ecd95 → 18464f83`). Every store carrying the old
  one held only test tasks, checked before the move.
- **Concurrency under many simultaneous agents is unmeasured.** The socket serialises writes
  in Luna's process, WAL and the busy timeout are already set, and one-agent-at-a-time is
  proven; N-at-once is RFC-0008's remaining open question.

## Alternatives considered

- **The CLI writes the database directly.** The obvious route, measured failing in the worst
  way available: inside the sandbox the store's path resolves onto a tmpfs root, so `luna
  task new` reported `created`, exited 0, and the task never existed. Nothing was denied —
  the write and the read agreed with each other and with nobody else.
- **A scratchpad on disk in the worktree.** Survives the sandbox, but the artifact leaves
  the verifiable handoff — only the agent's word says it was written — and it dies with the
  worktree ([ADR-0055](0055-one-worktree-per-task-and-role-branched-from-the-last-delivery.md)), making a copy mandatory on the critical path. Measured: `/.luna/` is
  ignored only in this repository, so in any target repository it shows as untracked — and
  on the machine this was tested on, a global git ignore hid exactly that.
- **Expose the database through the sandbox.** Hands an agent write access to the log, and
  an agent that can write the log can rewrite its own history.
- **Key per task, stage as metadata.** Simpler reads, but the colliding writers above become
  one line of history separated only by `seq`, and reconstructing "what did `build` hand
  over" needs a join against the log that the reader has to know to make.

## References

- [RFC-0008](../RFCs/rfc-0008-a-scratch-artifact-is-handed-over-through-a-socket.md) — the
  route, and every measurement this decision rests on
- [ADR-0058](0058-what-had-no-caller-is-either-wired-or-gone.md) — deleted the previous
  content store; reopened here with a different purpose, not a relitigated verdict
- [ADR-0070](0070-an-artifact-can-declare-where-it-lives.md) — the committed artifact's
  location check, which stays for what stays in git
- [ADR-0027](0027-luna-runs-under-herdr-as-a-socket-client.md) — the socket as the surface
  that crosses containment
- [INV-core-2](../invariants/core.md), [INV-core-11](../invariants/core.md),
  [INV-core-12](../invariants/core.md)
