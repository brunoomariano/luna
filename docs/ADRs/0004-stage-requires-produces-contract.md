# ADR-0004: Every stage declares `requires` and `produces`

**Status:** Accepted
**Date:** 2026-08-06

## Context

Without an explicit contract per stage, what a stage receives is merely what was left over
from the accumulated context. When something goes wrong, there is no way to tell a model
error from an incomplete delivery by the previous stage.

## Decision

Every stage declares what it **requires** (`requires`) and what it **produces**
(`produces`).

This supports three checks:

1. **static, before running** — walking the stages in order, any `requires` that no
   earlier stage produces indicates a flow broken on paper, detectable without executing
   anything;
2. **on input** — the FSM does not call the agent of a stage whose `requires` is not in
   the context;
3. **on output** — the stage does not close without delivering the declared `produces`.

## Alternatives considered

- **Free accumulated context** — rejected because without a contract there is no way to
  tell "the model got it wrong" from "the model did not receive what it needed".

## Consequences

- **Positive:** a broken flow is detectable before any execution; the failure is caught
  where it is born, not two stages later when the symptom has already drifted from the
  cause.
- **Impacts:** no stage can exist without a declared contract.

## References

- Related documents: [architecture](../architecture/overview.md),
  [default stages](../architecture/stages.md)
