# ADR-0067: there is no registry, and a task carries what it is about

**Status:** Accepted
**Date:** 2026-08-17

## Context

[ADR-0054](0054-the-registry-is-beads-and-the-flow-is-not.md) adopted beads as the place a
task lives: its status, its current stage, and — the part that mattered — the statement of
work an agent is briefed with.
[ADR-0065](0065-the-registry-is-a-projection-and-the-log-is-the-state.md) then demoted it to
a projection: the log is the state, and nothing Luna decides is ever read back from `bd`.

That left the dependency in an odd place. beads decided nothing, and `luna task new` still
failed without it.

An audit of what production actually called found **four call sites**, and only one of them
carried weight:

| what | who reads it |
|---|---|
| `--about/--design/--acceptance` → the agent's briefing | **every stage** |
| the checks that answer a gate | the lead, when judging |
| status and stage, mirrored out | nobody in Luna |
| `Blocked()` across checkouts | `luna stuck` |

The middle one was barely reachable: declaring a check meant `bd update --metadata` with
hand-written JSON, so in practice no task ever declared one. The third was write-only. The
statement was the whole of the value.

### What the audit of beads itself found

Four open issues, each verified against the repository (`gastownhall/beads`, 26k stars,
actively committed to):

- [#4767](https://github.com/gastownhall/beads/issues/4767) — `bd close` **reports success
  and does not persist** under concurrent agentic load. Eight workers, seven of eight closes
  lost. Open since 14/07, one comment.
- [#4331](https://github.com/gastownhall/beads/issues/4331) — concurrent mutation **reverts
  field edits already committed**. Labelled `data-loss`, no comments.
- [#3884](https://github.com/gastownhall/beads/issues/3884) — `bd export` is lossy: a
  rebuild took a 51 GB database to 53 MB.
- [#4635](https://github.com/gastownhall/beads/issues/4635) — `bd init` runs `git init` and
  **commits without confirmation**; in `$HOME` it can overwrite a `~/CLAUDE.md`.

The first is the one that decides this. **It is exactly Luna's usage pattern** — concurrent
agents closing tasks — and the guard ADR-0054 built (`--if-status`) protects against a write
that *loses a race*, not against a write that returns success and stores nothing.

Latency was measured rather than assumed: 95 ms per `bd show` against 0.08 ms to replay a
whole task, ~1150×. [#5397](https://github.com/gastownhall/beads/issues/5397) explains it —
the embedded Dolt engine is opened per query — and the daemon proposed to fix it was
withdrawn, with triage replying that features are frozen.

## Decision

**Luna has no registry. What a person says about a task is recorded in the task's own log.**

Three actions carry it, and the last is the one that answers the objection ADR-0054 raised:

- `TaskCreated` gains a `Statement` — description, design, acceptance;
- `StatementRevised` records a correction to it;
- `GateChecksDeclared` records the commands that answer one gate.

`internal/registry` is deleted. `luna run` reads the statement out of the replayed state
rather than calling `bd`, and the mechanical half of a gate reads its checks from the same
place.

Two commands replace what needed a second tool:

```
luna task new <id> --about <what> --design <how> --acceptance <done when>
luna task statement <id> --about ...          # a correction
luna gate checks <id> --on <gate> --run <cmd> # repeatable
```

### Why a frozen copy is no longer the objection

`fsm/state.go` used to say the statement was deliberately *not* recorded, because a copy in
the log would go stale while still looking authoritative — a person edits the tracker, and
the log would not know.

**Recording the revision answers that.** An edit is an event, so the log carries the current
statement *and* how it got there. That is strictly more than the registry offered, where an
edit overwrote its own history and left nothing to read.

### What is lost, and what is not

- **A consolidated view of every task.** `bd list` was a panel; there is no replacement
  today. `luna gates`, `luna stuck` and `luna status` answer per-question rather than
  per-inventory. A daemon over a central store is where this comes back, and it is not in
  this decision.
- **`luna stuck` across checkouts.** It asked the registry for tasks blocked in *another*
  clone — the one question a single repository's log cannot answer. What replaces it is a
  central store rather than a central tracker: one `LUNA_STORE` shared between checkouts
  makes every task local to the same log.
- **Nothing about dependencies.** beads' strongest feature is its dependency graph and
  `ready` computation. Luna never used it — no reference to `depends` or `blocked_by`
  existed anywhere in the codebase.
- **`--adopt` is gone.** It read a task written directly in beads. It also **discarded the
  statement** and returned only the title, so the feature it advertised was never built.

## Consequences

- **Luna runs in a repository it does not own.** This is the practical win: a fork of
  somebody else's project gets a `.luna/luna.db` and nothing else. No `bd init` writing
  `AGENTS.md`, `CLAUDE.md`, `.claude/`, `.codex/`, `.cursor/` and a `.git` into a tree that
  already has its own.
- **`bd` need not be installed.** Verified: every command works with it off the `PATH`.
- **The statement reaches the agent 1150× faster**, and from the same replay every other
  decision already comes from.
- **Declaring a gate check is a command.** The mechanical half of
  [RFC-0006](../RFCs/rfc-0006-a-gate-checks-what-it-can-and-a-knob-says-who-judges-the-rest.md)
  was built and left unreachable behind hand-written JSON; it now has a surface.
- **476 lines of production code and one external dependency are gone.**
- **ADR-0054 and ADR-0065 are superseded.** Their reasoning stands for what they decided at
  the time — 0065's "the log is the state" is what made this removable at all.

## Alternatives considered

- **Keep beads as an optional projection.** The first plan, and it survives one question:
  what is the projection *for*? Its only consumer was a person running `bd list`, and
  keeping a dependency, an init that writes eight files into a third-party repository, and a
  write path with an open data-loss issue — to keep one listing — is not a trade worth
  making. If the view is wanted back, a daemon over the store gives it without the tracker.

- **Fork or vendor beads.** Rejected on scope: the value taken from it was three text fields
  and a list of commands. Owning a Dolt-backed issue tracker to hold them is not a smaller
  problem than holding them in a log that already exists.

- **Use beads as a Go library.** There is a public `beads.go`, but its own doc says *"most
  extensions should use direct SQL queries against bd's database"*, and the official Go
  extension example [does not compile](https://github.com/gastownhall/beads/issues/5574).

- **Keep the statement out of the log, read from a plain file instead.** A `.luna/about.toml`
  would avoid the dependency and keep the "edited by a person" property. Rejected because it
  reintroduces exactly what ADR-0054 got wrong — state in two places, one of which nothing
  can replay — for a file nobody asked for.

## References

- [ADR-0054](0054-the-registry-is-beads-and-the-flow-is-not.md) — superseded: the registry
  was beads
- [ADR-0065](0065-the-registry-is-a-projection-and-the-log-is-the-state.md) — superseded:
  the registry was a projection, which is what made it removable
- [ADR-0024](0024-the-reducer-is-pure-verification-runs-outside.md) — an observation becomes
  an action before it reaches the reducer, which is what these three are
- [ADR-0047](0047-an-append-declares-the-position-it-decided-from.md) — the concurrency
  guarantee the log gives by construction, and the one beads' `--if-status` was reaching for
- [RFC-0006](../RFCs/rfc-0006-a-gate-checks-what-it-can-and-a-knob-says-who-judges-the-rest.md)
  — the two halves of a gate; the mechanical half now has a command
- [INV-core-2](../invariants/core.md) — the log is append-only, which is why a revision is an
  event rather than an edit
