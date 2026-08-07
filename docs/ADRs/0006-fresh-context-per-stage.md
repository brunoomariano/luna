# ADR-0006: Fresh context at every stage

**Status:** Accepted
**Date:** 2026-08-06

## Context

Role erosion is one of the known failure modes (see
[ADR-0001](0001-flow-control-out-of-model.md)): in a long session with compaction, the
reviewer starts implementing and the implementer starts reviewing. Keeping the agent
process alive between stages is cheaper and preserves context, but it is exactly the
vector of that degradation.

## Decision

Every stage starts with a clean context, **even when the role is the same**. There is no
long session to degrade.

Every handoff payload is prefixed with an instruction to re-read the role and the rules —
clean context and re-injected rule are two defenses on the same flank.

## Alternatives considered

- **Keep the process alive between stages** — cheaper and preserves context, but rejected
  for being the vector of role erosion: long context degrades adherence.

## Consequences

- **Accepted consequence:** the handoff becomes the **only** bridge between stages. If
  something necessary is not in it, the agent starts blind — and that is why the stage
  contract (see [ADR-0004](0004-stage-requires-produces-contract.md)) stops being
  optional.
- **Negative / costs:** we give up the savings of reusing an already warm process.

## References

- Related documents: [architecture](../architecture/overview.md),
  [references](../references.md)
