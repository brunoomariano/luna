# ADR-0054: The registry is beads, and the flow is not

**Status:** Accepted
**Date:** 2026-08-12

## Context

Luna's store is an append-only SQLite log, one file per worktree, with a pure reducer over
it. It works, and the last seven rounds of hardening made it genuinely solid: a flow
fingerprint so a replay refuses a log written under a different contract, a history-versus-
policy classification frozen by reflection, closed enums, a golden corpus, `BEGIN IMMEDIATE`
for concurrent writers.

Every one of those exists to defend **one choice** — that Luna keeps its own registry — and
none of them advances what Luna is for.

Two costs made the choice worth revisiting.

**The query nobody could answer.** "What is waiting on a person, across everything" meant
replaying every log in every worktree. `AwaitingGate` does exactly that, and it is O(tasks ×
events) by construction. A tracker answers it with a `WHERE`.

**One registry per worktree is not a registry.** The whole direction of
[RFC-0002](../RFCs/rfc-0002-the-lead-is-an-agent-and-the-commit-is-the-handoff.md) is several
agents working in several worktrees on one task. A store that lives *inside* a worktree
cannot be the thing they coordinate through.

[beads](https://github.com/steveyegge/beads) already exists, is already in
[references](../references.md), and — measured against version 1.2.1 rather than read about —
turns out to have the two properties this needed most: an **append-only provenance log**
(`bd provenance`, no update, no delete, idempotent on a deterministic id) and a
**compare-and-swap guard** (`--if-status`, exit 13 when the precondition no longer holds).

## Decision

**beads holds the task; Luna holds the flow.**

`internal/registry` adapts `bd` over its CLI. What moves:

- **status** — open, in_progress, blocked, closed;
- **the current stage** — as a namespaced label, `luna:stage:<id>`;
- **what each stage delivered** — as a provenance event binding a git sha to the task;
- **the query** — "what is blocked" is `bd list --status blocked`.

What does not move, and must not:

- **which stage comes next.** That is the FSM, in code, and it is the entire premise
  ([ADR-0001](0001-flow-control-out-of-model.md), INV-core-1). beads has no concept of a
  stage and is not being taught one — the label is a string it stores, not a state it
  interprets.
- **the contract and the verification.** What a stage owes and whether it delivered it stay
  in `fsm`.

**The concurrency guard is not optional.** `Move` takes the status the caller saw and passes
it as `--if-status`; there is no unguarded sibling. It is the same guarantee
[ADR-0047](0047-an-append-declares-the-position-it-decided-from.md) gave — a decision taken
against a state that has since changed must not be written — reached through a different
mechanism, and it matters more now than it did: a central registry means there can be more
than one lead.

**Shelling out rather than importing.** beads is a separate product with its own release
cycle, and linking it in would put its module graph in Luna's build. Its CLI speaks JSON and
is its documented surface, which is the reasoning that already put herdr behind a command
([ADR-0027](0027-luna-runs-under-herdr-as-a-socket-client.md)).

**It is pinned, and the tests drive the real binary.** `mise.toml` names 1.2.1, and
`make cover` runs through `mise exec` so the pinned one is what CI sees. The adapter has a
fake for its logic and a separate file that drives `bd` itself — because the fake proves the
logic and proves nothing about whether the adapter matches the binary. Writing it turned up
two facts no amount of reading would have: `--ref-kind git-sha` demands a full 40-character
lowercase hex and refuses a short sha, and `provenance log` takes its issue positionally
while `provenance record` takes `--issue`.

## Alternatives considered

- **Keep the local store and add beads alongside** — rejected because two registries
  disagree, and they disagree at the worst moment. It would also keep every piece of
  machinery this retires.

- **Import beads as a Go module** — rejected. It would tie Luna's build to beads' module
  graph and its release cadence, for a boundary crossed a handful of times per stage.

- **Model the 14 stages as beads sub-issues** — rejected, and `references.md` already said
  so before this was written: it inflates the graph by a factor of fourteen and buys nothing,
  since nothing queries "which tasks are in build". Worse, it would put the flow's shape in
  the tracker, which is exactly the line this decision draws.

- **Keep the flow fingerprint** — dropped with the log it protected. It answered "was this
  log written under a different contract", and a registry holds state rather than a sequence
  of transitions to re-apply. The concern does not disappear; it stops having this shape.

- **An unguarded `SetStatus` for convenience** — rejected. It is the API that gets used, and
  its failure is silent: two leads decide from the same state and both write.

## Consequences

- **Positive:** the cross-cutting queries are queries. "What is blocked everywhere" stops
  being a replay of every log in every worktree (INV-core-12).

- **Positive:** one registry for many worktrees, which is what phase 3 needs. A per-worktree
  store could not have been the thing several agents coordinate through.

- **Positive:** the audit trail keeps its shape. Provenance is append-only by construction and
  idempotent, so a delivery reported twice records once (INV-core-2) — and there is a test
  against the real binary that reports the same commit twice and insists on one row.

- **Negative:** **replay is gone.** A tracker holds state, not the sequence of actions that
  produced it. "What did the reducer see at step four" stops being answerable, and the
  reproducibility that came free from a pure reducer over an event log becomes something git
  and beads provide less precisely. RFC-0002 lists this as the trade and it is the real cost.

- **Negative:** Luna gains a runtime dependency that has to be installed. `make doctor`
  reports it, `mise.toml` pins it, and the tests skip rather than fail without it — but a
  deployment without `bd` has no registry.

- **Negative:** work is discarded. The fingerprint, the enum closures, the golden corpus and
  the optimistic-append machinery were correct and tested, and they defended a store that is
  being replaced. Saying so plainly is better than pretending it carries over.

- **Impacts:** `internal/registry` is new. `internal/store` is superseded and retires when
  the CLI moves onto the registry; ADR-0046, ADR-0048 and ADR-0050 lose their subject at
  that point and become `Superseded by` this.

## References

- Related documents:
  [RFC-0002](../RFCs/rfc-0002-the-lead-is-an-agent-and-the-commit-is-the-handoff.md)
  (phase 2), [ADR-0001](0001-flow-control-out-of-model.md),
  [ADR-0025](0025-pure-go-sqlite-and-blobs-in-the-same-database.md) (the store this replaces),
  [ADR-0047](0047-an-append-declares-the-position-it-decided-from.md) (the same guarantee,
  another mechanism), [ADR-0027](0027-luna-runs-under-herdr-as-a-socket-client.md) (shelling out to
  a neighbouring product), [INV-core-2](../invariants/core.md),
  [INV-core-12](../invariants/core.md)
- Prior art: [references](../references.md) — beads
