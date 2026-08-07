# ADR-0011: Failure — retry up to 2, then block with a notice

**Status:** Accepted
**Date:** 2026-08-06

## Context

When a node fails, someone decides what to do. The two extremes are known: insist
indefinitely, or call the human on the first failure. Neither one serves.

## Decision

When a node fails, the model evaluates and chooses between two exits:

- **try again**, up to 2 times, with the error in context;
- **block and notify the human**.

Once the attempts are exhausted, the task goes to `blocked` and notifies. There is no
silent death: every blocked task notifies.

## Alternatives considered

- **Infinite retry with backoff** — rejected because it is the loop that does not converge
  and burns tokens.
- **Escalate on the first failure** — rejected because it turns the human into the
  correction loop for things that one attempt would solve.
- **Going back to the previous stage as a third exit** — rejected. The idea was to escalate
  the response (retry → go back → block), but the middle step does not hold up:
  - **it duplicates a path with the review rollback.** Going back from `verify` to `build`
    is the same transition an aligned finding makes — except outside the invalidation of
    `ci_green` (see [ADR-0020](0020-review-finding-invalidates-green.md)). Two paths to the
    same place, one of them forgetting to invalidate the green;
  - **what it would solve is already covered.** A missing input is a `requires` violation,
    caught at the stage's entry (see
    [ADR-0004](0004-stage-requires-produces-contract.md)); a transient failure is what the
    retry handles; bad upstream work is what the review stages exist to find;
  - **it is not mechanizable without deciding more things.** How many stages to go back?
    Are the already produced artifacts invalidated? Without those answers, it would be an
    open decision disguised as a closed option.

## Consequences

- **Positive:** the attempt ceiling prevents the loop that does not converge; the mandatory
  notice prevents the silently dead task. With two exits instead of three, going back to a
  previous stage has **a single path** — the review one, which invalidates what needs to be
  invalidated.
- **Negative / costs:** a failure that the previous stage would cause goes to `blocked`
  instead of being fixed on its own. That is deliberate: the human decides whether the case
  deserves a rollback, instead of the FSM guessing how many stages to step back.
- **Impacts:** it depends on the judgment layer of the hybrid lead (see
  [ADR-0002](0002-hybrid-lead.md)) to choose between the two exits.

## References

- Related documents: [architecture](../architecture/overview.md),
  [default stages](../architecture/stages.md)
