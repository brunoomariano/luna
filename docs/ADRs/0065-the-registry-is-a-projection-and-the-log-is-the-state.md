# ADR-0065: The registry is a projection; the log is the state

**Status:** Superseded by [ADR-0067](0067-there-is-no-registry-and-a-task-carries-what-it-is-about.md)
**Date:** 2026-08-17

## Context

[ADR-0054](0054-the-registry-is-beads-and-the-flow-is-not.md) made beads the registry: it
answers "what is happening across every checkout", which one SQLite file per repository
cannot. It lists four things that move into it — status, the current stage, provenance, and
what the task is about.

An audit of the ADRs against the code found that three of those four are written by nothing.
`registry.Move`, `EnterStage`, `RecordCommit` and `Commits` exist, are tested, and have no
production caller. Only `Create`, `Task` and `Blocked` are reachable. Luna reads the
registry and never writes to it, so `Move`'s `--if-status` guard — the one ADR-0054 says has
no unguarded sibling — has never been exercised outside a test.

Finding it raised the question that this ADR exists to answer, asked plainly:

> if we record the stage in beads, do we still need the log and replay?

It is worth answering in a decision rather than in a conversation, because the honest answer
constrains what writing to beads is allowed to mean.

## Decision

**The registry is a projection of the log. The log is the state, and where the two disagree
the log wins.**

Luna writes to beads so a person can ask one question across many checkouts. It never reads
back from beads to decide anything: every transition is `Reduce(state, action)` over events
replayed from the log, exactly as before.

Concretely — Luna writes the task's status and its current stage, and those writes are
derived from a state the log already produced. A write that fails is reported and does not
fail the task, for the same reason a worktree that will not go away does not: the work
happened, and the projection lagging is a smaller fact than the work.

### Why the log cannot be replaced by the registry

The two answer different questions, and only one of them sustains the design.

| | beads | the log |
|---|---|---|
| where the task is now | yes | yes |
| what each artifact's evidence was | no | yes |
| which gate stopped it, and **who answered** | no | yes |
| the commit each stage delivered | no | yes |
| the flow it was born under | no | yes |
| how many rounds a loop ran | no | yes |

Three things break if the registry becomes the state, and none is hypothetical:

- **`Reduce` stops being possible.** It is `(state, action) → state`. With no history there
  is no state to reduce, and a task becomes a row that is overwritten — which is what
  [INV-core-2](../invariants/core.md) forbids in as many words: the store is append-only,
  there is no `UPDATE`.
- **The audit dies.** `checked`, `judged` and `waited` are distinguishable because every
  advance records who answered ([RFC-0006](../RFCs/rfc-0006-a-gate-checks-what-it-can-and-a-knob-says-who-judges-the-rest.md)).
  A label saying `luna:stage:spec` cannot say that the lead judged the previous gate under a
  knob of 7.
- **The fingerprint stops meaning anything.** [ADR-0046](0046-the-log-records-which-flow-it-was-written-under.md)
  refuses a replay when the flow moved under a task. With no replay, a task opened under an
  older flow carries on silently under the new one.

## Consequences

- **beads may lag, and that is allowed.** A projection that is briefly stale is a projection;
  a state that is briefly stale is a bug. This is what makes a failed write reportable rather
  than fatal.
- **Nothing reads the projection back.** `registry.Task` is still read — for the statement of
  work and the gate checks — but those are what a *person* wrote there, not what Luna
  projected. Reading Luna's own writes back would make the two states race.
- **`Move`'s guard finally runs.** ADR-0054 gave it `--if-status` and no unguarded sibling
  precisely so two leads cannot both act on one state. Until now nothing exercised it.
- **The registry stays optional.** A project with no beads keeps working, because the
  projection is not on any decision path.

## Alternatives considered

- **Keep the registry read-only and drop the unwired methods.** Consistent, and ADR-0058
  would demand exactly that of code with no caller. Rejected because the question ADR-0054
  was written for — what is blocked in another checkout — is answered today only for
  `blocked`, and only because `Beads.Blocked` queries beads' own status rather than anything
  Luna wrote. A task Luna moved through eight stages looks untouched from outside.

- **Make the registry the state and drop the log.** Rejected on the three grounds above. It
  is worth recording as rejected rather than unasked, because it is the natural question once
  a second store exists, and the answer is not obvious from either side alone.

- **Write everything ADR-0054 lists, including provenance.** `RecordCommit`/`Commits` are
  left unwired for now: the commit is already the handoff and lives in the log, and a second
  copy in beads would be a projection nobody has asked to query. This ADR does not delete
  them, and the next person to want cross-checkout provenance has the method waiting.

## References

- [ADR-0054](0054-the-registry-is-beads-and-the-flow-is-not.md) — the registry is beads
- [ADR-0046](0046-the-log-records-which-flow-it-was-written-under.md) — the log records its
  own flow
- [ADR-0058](0058-what-had-no-caller-is-either-wired-or-gone.md) — what has no caller is
  either wired or gone
- [INV-core-2](../invariants/core.md) — state is never overwritten
- [RFC-0006](../RFCs/rfc-0006-a-gate-checks-what-it-can-and-a-knob-says-who-judges-the-rest.md)
  — who answered a gate, recorded per advance
