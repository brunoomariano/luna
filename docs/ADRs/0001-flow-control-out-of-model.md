# ADR-0001: Flow control leaves the model

**Status:** Accepted
**Date:** 2026-08-06

## Context

A workflow written in prose is a **suggestion** to the model, not a guarantee. The model
follows it almost always — and it is the "almost" that costs dearly. Four failure modes
were observed in real execution:

- **instruction reinterpretation** — an agent instructed to *run* a command decided that
  running meant *printing it*;
- **abandoned loop** — repeats the same thing 100 times and on the 101st does something
  else, or simply stops;
- **role erosion** — in a long session with compaction, the reviewer starts implementing
  and the implementer starts reviewing;
- **silently broken chain** — the agent decides it is not worth passing along, and nobody
  notices.

## Decision

The state machine decides the next stage; the model works inside it. Flow control is
code, not prose.

## Alternatives considered

- **Keep the flow in documentation and trust the model's adherence** — rejected because
  it is the current state of the problem, and it fails in the four known modes
  (reinterpretation, abandoned loop, role erosion, broken chain).

## Consequences

- **Positive:** the flow becomes a guarantee, not a suggestion. The four failure modes no
  longer depend on the model's goodwill.
- **Impacts:** it is the premise of the entire project — every later decision is measured
  against it.

## References

- Related documents: [architecture](../architecture/overview.md),
  [references](../references.md)
