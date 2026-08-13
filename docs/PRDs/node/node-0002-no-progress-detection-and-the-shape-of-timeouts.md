# PRD node-0002: No-progress detection, and the shape of timeouts

**Status:** IMPLEMENTED
**Last reviewed:** 2026-08-12
**Source issue:** —
**RFC:** —

> **The first half is built.** Its blocker was named here — *"nothing emits a
> `ReviewFinding` today, so there are no rounds to count"* — and that emitter
> landed with [ADR-0059](../../ADRs/0059-luna-reads-the-review-and-a-spent-ceiling-stops-the-task.md).
> `Loop.NoProgress` is fed and its ceiling fires
> ([ADR-0061](../../ADRs/0061-the-commit-is-the-progress-signal.md)).
>
> **The second half is not, and deliberately.** Luna still has one timer, and the
> PRD's own argument is that a second question appears in production rather than
> at the desk. Nothing has asked for it yet. The trap it records — an event
> timeout rearmed by a keepalive holding a stalled agent alive forever — is worth
> keeping written down for whoever adds the second one.

## Overview

Two related things the watchdog cannot do today: notice that an agent is busy and
achieving nothing, and express the different kinds of "too long" that turn out to be
different questions.

## Problem

### Progress, not elapsed time

`budgets.go` carries the lesson and does not act on it:

> *"Deliberately absent: a wall-clock cap. multica removed theirs after it killed legitimate
> long runs, and the lesson is that **progress, not elapsed time, is the signal worth acting
> on**."*

What exists measures time until the agent settles. An agent that runs commands continuously,
producing the same result each round, never settles and never exceeds anything — it simply
costs money until a person notices.

`Loop.NoProgress` exists for exactly this and **is never incremented**. One of the three
ceilings of [ADR-0023](../../ADRs/0023-three-separate-loop-ceilings.md) is decorative, and it
is the one that would catch a task circling without converging — which
[ADR-0034](../../ADRs/0034-the-watchdog-delegates-detection-and-owns-the-verdict.md) admits
the watchdog does not cover, on the grounds that the loop ceilings do.

**Why it cannot be built yet.** `NoProgress` counts rounds of a convergence loop, and
nothing emits a `ReviewFinding` today — so there are no rounds to count. Building the
detector before the emitter would produce a fifth piece of machinery nobody reaches, which is
the failure RFC-0001 exists to stop repeating.

**The reference.** hermes-agent detects no-progress by **hashing the result** of each tool
call: same hash as the previous round means nothing changed. It is cheap, needs no model, and
is the mechanism ADR-0034 deferred *"until loops actually run"*.

### One timeout is probably not enough

Erlang/OTP arrived at **three** timers, after twenty years on the problem and three public
iterations, because they turn out to be three different questions:

| Timer | Question | Cancelled by |
|---|---|---|
| event timeout | how long without a signal | any event |
| state timeout | how long in this state | changing state |
| generic timeout | how long until a named deadline | nothing; several run at once |

Luna has one, and after this round it is honest about what it measures: the whole turn. That
is enough today. What OTP suggests is that a second question appears in production rather
than at the desk — most likely *"how long has this stage been open"*, independent of whether
signals arrive.

The trap OTP names is worth recording now, because it is easy to walk into: an **event
timeout** is reset by any activity, so a health check or a keepalive can hold a stalled agent
alive forever. Luna does not have that today — the deadline is a single one per prompt and
nothing rearms it — and a future timer that rearms on activity would introduce it.

## Goal

Notice an agent that is working and achieving nothing, and have a vocabulary for "too long"
that does not collapse different questions into one number.

## Expected behavior

### Main flow

1. A loop round that produces the same observable result as the previous one is counted as
   no progress.
2. Crossing the declared ceiling opens a gate rather than blocking — not converging is a
   decision to make with the history in view, not a node failure
   ([ADR-0023](../../ADRs/0023-three-separate-loop-ceilings.md)).

### Edge cases

- A round that legitimately produces the same result — a formatter run twice.
- A result that differs only in something meaningless: a timestamp, a temporary path, an
  ordering that varies between runs.
- The first round, which has nothing to compare against.

### Error handling

- Failing to compute the signal must not stop the task. This is an observation; a detector
  that blocks work when it cannot observe is worse than one that stays quiet.
- Whatever the ceiling decides has to reach the log. A round that counted as no progress is a
  fact the audit wants ([INV-core-2](../../invariants/core.md)).

## Requirements

### Functional

- RF1: detect that a loop round produced no change, without asking a model.
- RF2: feed `Loop.NoProgress`, so the ceiling that already exists starts firing.
- RF3: record what was compared, so a person can tell a real stall from a false one.

### Non-functional

- RNF1: the comparison happens outside the reducer and arrives as data
  ([ADR-0024](../../ADRs/0024-the-reducer-is-pure-verification-runs-outside.md)).
- RNF2: no wall-clock cap, for the reason `budgets.go` already records.

## System impacts

- **Affected services:** `internal/node` or `internal/herdr` for the observation,
  `internal/fsm` for the counter that already exists.
- **Data/persistence:** the count is already part of the state; what is new is what feeds it.
- **Observability:** "this task made no progress for two rounds" is exactly what a person
  wants to see before deciding.

## Rollout plan

- Feature flag? no — the ceiling is configurable and an unset one changes nothing.
- Rollback strategy: stop feeding the counter.

## Open questions

- [x] **What is hashed?** Nothing is. The signal is the **commit the round delivered**,
      which is exact where a hash would be an approximation — and it only became available
      because the handoff moved to git first (ADR-0055). Two rounds delivering the same sha
      delivered the same work.
- [x] **How is meaningless variation excluded?** It does not arise. The question assumed a
      hash over content, where a timestamp makes a diff never match itself; a commit either
      is the previous one or is not. This is the clearest case in the whole rewrite of an
      earlier decision dissolving a later problem rather than constraining it.
- [ ] **Does the same signal serve the watchdog?** No-progress within a loop and a stalled
      agent mid-stage are different questions with possibly the same answer, and merging them
      early is how one timer becomes three later.
- [ ] **Which second timer, if any?** OTP's state timeout maps to "this stage has been open
      too long", which is narrower than the wall-clock cap multica removed — it is per stage
      and resets on transition. Worth reaching for only when a real run needs it.

## References

- Issue: —
- Related ADRs: [ADR-0019](../../ADRs/0019-inactivity-watchdog.md),
  [ADR-0023](../../ADRs/0023-three-separate-loop-ceilings.md),
  [ADR-0034](../../ADRs/0034-the-watchdog-delegates-detection-and-owns-the-verdict.md),
  [ADR-0024](../../ADRs/0024-the-reducer-is-pure-verification-runs-outside.md)
- Blocked by: `ReviewFinding` having no emitter
  ([RFC-0001](../../RFCs/rfc-0001-close-the-gap-between-decided-and-built.md), phase 4)
