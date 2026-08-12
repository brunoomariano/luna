# ADR-0049: A stage declares its gate, and what a review costs

**Status:** Accepted
**Date:** 2026-08-12

## Context

[ADR-0048](0048-a-field-read-by-the-reducer-is-history.md) classified every field of `Stage`
as history or policy, by asking whether the reducer reads it. Doing that turned up a
category the question could not reach: **behaviour that belongs to a stage and was not a
field at all.**

Four things were hardcoded inside the reducer, keyed on the shipped stage ids:

- `gateFor` — a `switch` naming `discovery`, `scenarios`, `spec` and `commit`, deciding
  which gate each opens;
- `reviewStages` — a map of four ids, deciding who may send work back;
- `state.Stage = "build"` — the destination of that rollback, as a literal;
- `{"ci_green", "tests_green"}` — the artifacts a rollback invalidates, also literal.

All four are read inside `Reduce`, so by ADR-0048's own rule they are history. None of them
could be fingerprinted, versioned or migrated, because none of them was data.

The cost lands on [ADR-0017](0017-defaults-plus-customization-everywhere.md), which promises
a flow can be replaced. It could — and the replacement got **no gates**, could not send work
back from any stage, and if it somehow did, the work went to a stage called `build` and the
engine deleted artifacts called `ci_green` and `tests_green`. A flow that cannot pause for a
person is not a flow anyone would choose; it was what the code did.

There is a sharper version of the same problem. [INV-core-7](../invariants/core.md) says
whoever writes does not review, and the engine enforced it by consulting a list of four
names. Renaming `code-review` in a custom flow removed the protection **silently** — the
kind of erosion the invariant exists to prevent, sitting in the code that enforces it.

## Decision

**A stage declares its own gate and its own review behaviour.**

```go
Gate   *GateSpec   // Kind, Reason, Artifact
Review *ReviewSpec // SendsBackTo, Invalidates
```

`gateFor` becomes a field read. `reviewStages` disappears — a stage may send work back if
and only if it declares a `Review`, which is INV-core-7 stated by the contract rather than
by a list the engine keeps beside it. The rollback's destination and the artifacts it
invalidates come from that same declaration.

`ReviewFinding` gains a `Flow` field, `json:"-"` like the ones on `Advance` and `Complete`
and for the same reason: the stage's declaration is configuration, so it is supplied on
replay rather than recorded ([ADR-0026](0026-the-log-records-the-gate-decision-not-the-policy.md)).

Both new fields are **history** under ADR-0048, and the fingerprint covers them — with two
deliberate exclusions:

- **the gate's `Reason`** is prose a person reads at the moment they are asked. Rewording
  "confirm the repositories" cannot change whether a past `Advance` suspended.
- **nothing else**. `Kind` and `Artifact` decide the suspension and what was reviewed;
  `SendsBackTo` and `Invalidates` decide where a past finding moved the task and which
  artifacts left the context.

## Alternatives considered

- **Leave it and document the limitation** — rejected because it makes ADR-0017 false in a
  way nobody would discover until they wrote a custom flow and found it had no gates. A
  promise that only holds for the shipped flow is not the promise.

- **Move only the gate, leave the review behaviour** — rejected as half a fix that leaves
  the worse half. The gate's absence in a custom flow is obvious the first time you run one;
  a review that silently stops being allowed, or sends work to a stage that does not exist,
  is the failure that hides.

- **Keep `reviewStages` and add the gate as a field** — rejected because the list is the
  part that erodes INV-core-7. An invariant enforced by a hardcoded set of names is enforced
  only for the names someone remembered to write down.

- **Put the gate's reason in the fingerprint too** — rejected as the noise trap ADR-0048
  named. A refused replay because somebody improved the wording of a prompt teaches people
  to work around the check.

## Consequences

- **Positive:** a custom flow is now a flow. It can pause for a person, review its own work,
  and declare what a rejection costs.

- **Positive:** INV-core-7's engine-side enforcement moved from a list of names to the
  contract, so renaming a stage cannot quietly remove it.

- **Positive:** four pieces of behaviour that were invisible to the fingerprint became
  visible to it, which is ADR-0046 covering ground it could not reach before.

- **Negative:** `Stage` grew two fields, and both are pointers — so "no gate" and "no
  review" are `nil` rather than zero values, which is the one shape in the struct where
  forgetting a nil check is possible. The reducer's two reads are guarded and tested; a
  third reader would have to remember.

- **Negative:** the shipped flow's fingerprint changes again, one ADR after it changed for
  conditions and scopes. Any task open across either change has to be abandoned. Both were
  cheap now and expensive later, which is why they are landing together rather than being
  spread out.

- **Impacts:** `src/stock/` will have to express a gate and a review spec in configuration.
  Both are plain data — a kind, a reason, an artifact, a stage name, a list — which is far
  easier to put in a TOML file than the `switch` they replaced.

## References

- Related documents: [ADR-0048](0048-a-field-read-by-the-reducer-is-history.md),
  [ADR-0046](0046-the-log-records-which-flow-it-was-written-under.md),
  [ADR-0022](0022-gate-carries-artifact-for-review.md),
  [ADR-0020](0020-review-finding-invalidates-green.md),
  [ADR-0017](0017-defaults-plus-customization-everywhere.md),
  [INV-core-7](../invariants/core.md)
