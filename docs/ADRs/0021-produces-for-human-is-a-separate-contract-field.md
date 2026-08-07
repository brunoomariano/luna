# ADR-0021: What only the human reads is a contract field of its own

**Status:** Accepted
**Date:** 2026-08-07

## Context

While mapping the contract's data graph, seven artifacts showed up as **leaves**:
`dod_checked`, `min_case`, `qa_report`, `review_report`, `mutation_report`, `arch_report`
and — before [ADR-0022](0022-gate-carries-artifact-for-review.md) — the `contract`. They are
produced and never declared in the `requires` of any stage.

This creates a silent asymmetry. The static check (see
[ADR-0004](0004-stage-requires-produces-contract.md)) walks the stages accumulating what
each one produces and flagging any `requires` with no producer. It **has no way to flag the
opposite**: a `produces` that nobody consumes goes unnoticed, and nothing in the contract
distinguishes "artifact the flow needs" from "report a person will read".

The practical consequence: the QA report is as mandatory as the code, but the contract does
not know that. If the stage closed without producing it, no check would complain — because
no stage downstream would miss it.

## Decision

A stage declares **three** fields, not two:

```toml
[stage.qa]
requires           = ["ci_green", "briefing"]
produces           = []                 # the flow consumes nothing from qa
produces_for_human = ["qa_report"]      # but the report is mandatory
```

- **`produces`** — consumed by some stage downstream. Enters the static check.
- **`produces_for_human`** — read by a person. **Checked on output** exactly like
  `produces` (the stage does not close without delivering it), but **exempt from the static
  check**: nobody consuming it is not a defect.

Generation is **configurable per stage**: an audit artifact can be turned off when it is not
justified, and the contract records that choice instead of leaving it implicit.

An artifact can migrate from `produces_for_human` to `produces` when some stage starts
consuming it — that is what happened with the `contract`.

## Alternatives considered

- **Keep a single field and relax the static check to warn instead of failing** — rejected
  because it loses the ability to configure generation per stage, and because it turns a
  real semantic distinction ("who consumes this?") into a warning one learns to ignore.
- **Mark the item inside `produces` (`consumed_by_flow: false`)** — rejected for verbosity:
  the marking would repeat on every artifact, instead of living in the field that already
  expresses it.
- **Not producing what nobody consumes** — rejected outright. The QA report and the
  architecture assessment are part of the stage's value; the flow not consuming them does
  not make them disposable, it makes them **destined for another reader**.

## Consequences

- **Positive:** the report's mandatory nature becomes verifiable — the stage does not close
  without it. The static check stays strict for what the flow consumes, with no false
  positives on what it does not.
- **Negative / costs:** one more field in every stage's contract, and one more rule for
  whoever writes a new stage to understand.
- **Impacts:** the output check now considers both fields; the static one, only the first.
  The handoff records both, because the audit artifact also needs to be locatable later (see
  [architecture](../architecture/overview.md), "how the human sees what is waiting").

## References

- Related documents: [default stages](../architecture/stages.md),
  [architecture](../architecture/overview.md), [invariants](../invariants/core.md)
