# ADR-0020: An aligned finding invalidates the green when going back to build

**Status:** Accepted
**Date:** 2026-08-06

## Context

When a review stage — `qa`, `code-review`, `harden` or `architecture` — finds a problem,
the model decides by **alignment with the task**: an aligned finding goes back to `build`;
a finding outside the scope becomes a new task and the flow continues (see
[ADR-0014](0014-conditional-stages.md) and
[architecture/stages](../architecture/stages.md)).

Going back to `build` creates a problem that is not obvious. The review stages only enter
**after** `verify` has produced `ci_green`. If the flow goes back to `build`, the code
changes — but `ci_green` stays in the context, attesting to a state that no longer exists.

The stage contract (see [ADR-0004](0004-stage-requires-produces-contract.md)) checks that
the `requires` **is present** in the context. It has no way of knowing that a present value
went stale. Without explicit invalidation, the second pass through the review stages would
find `ci_green` satisfied by a run predating the current code — and the contract would
approve the entry, because from its point of view the input is there.

That would defeat the central guarantee: validation is done by **running the tool** (see
[ADR-0005](0005-validate-output-by-running-the-tool.md)), precisely so as not to trust an
attestation. A stale `ci_green` is an attestation disguised as verification.

## Decision

When an aligned finding sends the flow back to `build`, the transition **removes `ci_green`
from the context**. The green must be re-earned by a new run of `verify` over the new code.

Invalidation is part of the transition, not the agent's responsibility: no stage "remembers"
to invalidate what its own change made stale.

## Alternatives considered

- **Keep `ci_green` and trust that `verify` runs again along the way** — rejected because
  the contract only requires presence, not freshness. `verify` takes part in the loop and
  would be re-executed on the happy path, but the flow does not *guarantee* that: one stage
  condition changing would be enough for the new code to reach `code-review` with the old
  green.
- **Tag `ci_green` with the hash of the code that produced it** and compare it on entry —
  rejected for now: it solves the same problem with more mechanism, and would require
  extending the stage contract from presence to validity. It is recorded as the natural
  evolution if other products come to need dependency-based invalidation.

## Consequences

- **Positive:** it prevents a stale verification from validating new code. It is what keeps
  the second pass through the review stages honest.
- **Negative / costs:** every rollback to `build` pays for a re-execution of `verify`, even
  when the change was minimal. That is the cost accepted for not trusting an old green.
- **Impacts:** it establishes that **a transition may invalidate earlier products**. Today
  the only case is `ci_green`; if other products with a freshness dependency appear, the
  hash alternative comes back to the table.

## References

- Prototype: `prototypes/fsm-flow.html`, the `REVIEW_FINDING` case of the `LunaFSM` reducer
  — the behavior is implemented and drivable through the page's scenarios.
- Related documents: [architecture](../architecture/overview.md),
  [stages](../architecture/stages.md), [invariants](../invariants/core.md)
