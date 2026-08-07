# ADR-0024: The reducer is pure; verification runs outside it

**Status:** Accepted
**Date:** 2026-08-07

## Context

Two rules the project already holds collide at the stage's exit check.

[INV-core-4](../invariants/core.md) requires that delivery be verified **by running
the real tool**: the test runs, the commit resolves to exactly one object, the file is
on disk. Checking that a field came filled in validates the appearance of the delivery,
not the delivery.

The engine, meanwhile, is a reducer: `f(state, action) → state`. That shape is what the
12-factor reading in [references](../references.md) calls a stateless reducer, and it is
what makes a transition testable without infrastructure and reproducible from the
append-only log ([INV-core-2](../invariants/core.md)).

A pure function cannot run a test suite. Something has to give, and picking the wrong
side costs either the guarantee or the testability.

## Decision

**Verification runs outside the reducer, and its verdict enters as part of the action.**

The lead runs the tool, collects what came back, and hands the reducer a `Complete`
action carrying both what was delivered and the evidence for it. The reducer decides
what that means — the stage closes, or it does not and the task blocks.

```go
// lead — impure: runs the real tool
verdict := verifier.Check(stage, worktree)

// reducer — pure: decides with the verdict in hand
state = Reduce(state, Complete{
    Delivered: verdict.Produced,
    Evidence:  verdict.Evidence,
})
```

**The evidence is kept, not just the verdict.** The action carries what the tool
actually reported — `go test ./... → ok, 31 tests`, a content hash, a resolved commit
SHA — and that goes into the handoff. INV-core-2 makes the history the audit trail; an
audit that records *that* a stage closed but not *on what grounds* answers half the
question anyone asks six months later.

The boundary this draws: `internal/fsm` never touches a process or the filesystem.
Running things belongs to `internal/node`, and the engine consumes results.

## Alternatives considered

- **Passing a verifier into the reducer** (`Reduce(state, action, verifier)`) — rejected.
  It reads more directly, but the reducer stops being `f(state, action)`: every test
  needs a verifier fake, and a transition gains a way to fail that has nothing to do
  with the state machine — a slow disk, a flaky network. The thing that decides the
  flow should not be able to fail for reasons outside the flow.
- **Trusting the agent's structured output** — rejected, and it is the alternative
  ADR-0005 already refused. An agent reporting `tests_green: true` is the field coming
  back filled in, which is what INV-core-4 names as the violation.
- **Verifying inside the node and reporting only a boolean** — rejected for the same
  reason the evidence is kept: it satisfies the check while discarding what makes the
  audit trail worth having.

## Consequences

- **Positive:** the reducer stays pure — testable with a fabricated verdict, no
  infrastructure, no clock, deterministic. INV-core-4 is satisfied because the tool does
  run; it just runs on the other side of the boundary.
- **Negative / costs:** a verdict can be fabricated. Nothing in the type system stops a
  caller from constructing `Complete{Delivered: everything}` without running anything —
  the guarantee now rests on there being exactly one path that builds that action, and
  on that path being the verifier.
- **Impacts:**
  - `internal/node` gains the responsibility of running verification and reporting
    evidence;
  - the handoff carries evidence per artifact, so the store has to hold it
    (ADR-0010);
  - the reducer's exit check compares what was delivered against **both** `produces` and
    `produces_for_human` ([INV-core-11](../invariants/core.md)).

## References

- Related documents: [architecture](../architecture/overview.md),
  [invariants](../invariants/core.md), [references](../references.md)
