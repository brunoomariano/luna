# ADR-0061: The commit is the progress signal

**Status:** Accepted
**Date:** 2026-08-13

## Context

[ADR-0023](0023-three-separate-loop-ceilings.md) gave the convergence loop three ceilings:
rounds, oscillation, and **no progress**. Two were fed. The third was not — `Loop.NoProgress`
has existed since that decision and nothing ever incremented it.

It is the one that matters most for cost. `budgets.go` carries the lesson in as many words:

> *"a wall-clock cap is deliberately absent. multica removed theirs after it killed
> legitimate long runs, and the lesson is that **progress, not elapsed time, is the signal
> worth acting on**."*

So Luna measures time until an agent settles, and an agent that settles every round while
producing the same result never exceeds anything. It costs money until a person notices.
[ADR-0034](0034-the-watchdog-delegates-detection-and-owns-the-verdict.md) admitted the
watchdog does not cover that case, on the grounds that the loop ceilings do — and the
ceiling it pointed at was the decorative one.

[PRD node-0002](../PRDs/node/node-0002-no-progress-detection-and-the-shape-of-timeouts.md)
recorded why it could not be built: *"`NoProgress` counts rounds of a convergence loop, and
nothing emits a `ReviewFinding` today — so there are no rounds to count"*. That emitter
landed with [ADR-0059](0059-luna-reads-the-review-and-a-spent-ceiling-stops-the-task.md), and
the blocker went with it.

The PRD left two questions open, and they are the interesting part:

> **What is hashed?** hermes hashes tool results. Luna's equivalent could be the delivered
> artifacts, the diff of the worktree, or the evidence.
>
> **How is meaningless variation excluded?** A hash over a diff that includes a timestamp
> never matches itself, and a detector that never fires is the same as no detector.

## Decision

**The signal is the commit the round delivered.** Nothing is hashed.

`ReviewFinding` carries `Progress`, an opaque string produced outside the engine and compared
inside it: equal to the previous round's means the round produced the same work, and
`Loop.NoProgress` counts it. The lead fills it with `state.Base` — the commit the closed
stage delivered.

That answers both open questions at once, and dissolves the second rather than solving it.
A hash over content has the timestamp problem the PRD describes; a commit either **is** the
previous one or is not. The handoff is the commit (INV-core-6), so two rounds delivering the
same sha delivered the same work — exactly, not approximately.

It is worth naming why this was available: it only works because
[ADR-0055](0055-one-worktree-per-task-and-role-branched-from-the-last-delivery.md) made the
handoff a commit first. Hashing a worktree, which is what this would have needed a month
ago, has precisely the problem the PRD was worried about.

**A round that observed nothing leaves the streak alone.** Not counted, not cleared. Both
alternatives are wrong in a way that matters: counting silence fires the ceiling on a node
that could not look, and clearing it throws away a real streak because one round in the
middle could not. The PRD's error handling asks for exactly this — *"a detector that blocks
work when it cannot observe is worse than one that stays quiet"* — and the last signal is
kept, so the next round compares against the last thing actually seen.

**The comparison happens outside the reducer and arrives as data** (RNF1). The reducer
decides what two equal signals mean; it does not go looking for them
([ADR-0024](0024-the-reducer-is-pure-verification-runs-outside.md)).

**What was compared is recorded** (RF3). `Loop.LastProgress` holds the signal itself rather
than a count, because a person told two rounds made no progress wants to see what the machine
looked at before believing it.

## Alternatives considered

- **Hash the delivered artifacts** — the PRD's own first suggestion, and rejected because it
  is a worse version of what the commit already is. The artifacts are *in* the commit.

- **Hash the worktree diff** — rejected, and this is the one the PRD warned about: a diff
  carrying a timestamp, a temporary path or a varying order never matches itself, and a
  detector that never fires is the same as no detector.

- **Count a round that observed nothing as no progress** — rejected. It makes the ceiling
  fire on a node that could not compute the signal, which is the detector firing on its own
  blindness.

- **Clear the streak when a round observes nothing** — rejected for the opposite reason: a
  real streak of three would be thrown away because the fourth round could not look.

- **Compare the evidence instead** — rejected. Evidence records what the check said, not what
  the work is; two identical greens over different code are not the same delivery.

## Consequences

- **Positive:** the third ceiling fires. A loop delivering the same commit round after round
  now opens a gate rather than running to the round limit, and there is a test that walks it.

- **Positive:** ADR-0034's admitted gap closes. The watchdog still does not cover a busy
  agent achieving nothing — but the loop ceiling it deferred to now actually does.

- **Positive:** no hashing, no configuration, no heuristic about which parts of a diff are
  meaningful. The exactness is inherited from the handoff being a commit.

- **Negative:** it only sees progress *between review rounds*. An agent burning tokens inside
  a single stage without ever settling is still uncovered — that is a turn budget's job, and
  the budget bounds it.

- **Negative:** a round that legitimately delivers the same commit — a reviewer sending back
  work for a reason the implementer answers without changing code — counts as no progress. It
  takes the declared ceiling to fire, and the ceiling opens a gate rather than blocking, so
  the cost is a person being asked about something that was fine.

- **Negative:** the PRD's second half is still not built. Luna has one timer, and the PRD's
  own argument is that a second question appears in production rather than at the desk.

- **Impacts:** `ReviewFinding.Progress`, `LoopCounters.LastProgress`, `reviewFinding` in the
  reducer, and `lead.progressOf`.

## References

- Related documents:
  [PRD node-0002](../PRDs/node/node-0002-no-progress-detection-and-the-shape-of-timeouts.md)
  (its first half), [ADR-0023](0023-three-separate-loop-ceilings.md) (the ceiling this feeds),
  [ADR-0034](0034-the-watchdog-delegates-detection-and-owns-the-verdict.md) (the gap it
  closes), [ADR-0055](0055-one-worktree-per-task-and-role-branched-from-the-last-delivery.md)
  (why the commit was available), [ADR-0059](0059-luna-reads-the-review-and-a-spent-ceiling-stops-the-task.md)
  (the emitter that unblocked it), [ADR-0024](0024-the-reducer-is-pure-verification-runs-outside.md)
- Prior art: [references](../references.md) — hermes-agent, which detects no-progress by
  hashing tool results
