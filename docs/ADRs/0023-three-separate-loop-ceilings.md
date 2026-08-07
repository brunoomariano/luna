# ADR-0023: The loop has three ceilings counted separately

**Status:** Accepted
**Date:** 2026-08-07

## Context

The convergence loop (`build` → `refactor` → `verify`) needs a ceiling, otherwise it is the
loop that does not converge and burns tokens — the same reason that rejected infinite retry
in [ADR-0011](0011-failure-retry-rollback-or-block.md).

The prototype uses **a single counter** (`loop`), incremented both by node failure and by
review round, and reset at every stage that closes. It is a prototype simplification: the
original design foresaw three distinct limits, which the simplification merged.

A single counter hides the difference between pathologies that call for different answers: a
loop that spins four times **making progress** is healthy and should continue; one that
spins twice **without producing functional change** is stuck; one that alternates between
the same two states is undoing its own work.

## Decision

Three ceilings, counted and configured separately per loop:

| Ceiling | Counts | Detects |
|---|---|---|
| `max_rounds` | total rounds | the loop that does not end |
| `no_progress_rounds` | consecutive rounds with no functional change | the loop that spins without producing |
| `oscillation_rounds` | consecutive rounds alternating between the same states | the loop that undoes what it just did |

Once **any one** is blown, the loop **opens a gate** — it does not block. The distinction
matters: a loop that does not converge is not a node failure, it is a decision to be taken,
and the human has the history to take it.

The loop round counter is **separate from the failure retry counter** (ADR-0011). They are
distinct in nature: retry answers a node that broke; a loop round answers work that has not
converged yet. Merging them would make a transient failure consume the convergence budget.

`no_progress_rounds` comes from the damper observed in SwarmForge (see
[references](../references.md)): *"produced no functional change"* as a stopping condition,
instead of just counting rounds.

## Alternatives considered

- **A single counter, as in the prototype** — rejected because it forces one ceiling to
  arbitrate three different situations. Too low, it kills a healthy loop that was making
  progress; too high, it lets the stuck loop spin up to the limit.
- **Only `max_rounds`** — rejected because counting rounds does not distinguish progress
  from stagnation. Four productive rounds and four identical rounds add up to the same
  number.
- **Blocking instead of opening a gate** — rejected because failing to converge is not a
  node anomaly, and `blocked` is the anomaly state. A loop that hit the ceiling has useful
  information for the human to decide whether to continue, abort or change course.

## Consequences

- **Positive:** each pathology is detected by the signal that characterizes it. The gate
  instead of the block keeps the decision with whoever can take it.
- **Negative / costs:** it requires defining what counts as "functional change" — the same
  question SwarmForge answers coarsely (a manifest-only change does not count). It is
  calibration that only use resolves.
- **Impacts:** the loop state stops being an integer and comes to carry the three counts
  plus enough to detect oscillation (the last visited states).

## References

- Related documents: [default stages](../architecture/stages.md),
  [references](../references.md)
