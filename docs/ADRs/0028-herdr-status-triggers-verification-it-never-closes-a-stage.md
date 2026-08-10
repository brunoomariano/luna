# ADR-0028: herdr's status triggers verification; it never closes a stage

**Status:** Accepted
**Date:** 2026-08-10

## Context

With Luna running under herdr (ADR-0027), the cheapest possible node layer would read
herdr's `AgentStatus` and close the stage when it reads `idle`. The agent stopped moving,
so the work must be done.

It is also how every system in the wave 5 study fails. Not one of the five verifies what an
agent delivered: swarm-forge stamps `completed_at` and moves a file, multica defaults to
`completed` and greps the model's prose for a multilingual list of the word "done",
hermes-agent built a real verification ledger and wired it to a *nudge* asking the model to
please verify. Completion is the model's assertion, recorded as fact.

Reading `idle` as done would reproduce that exactly, with one aggravation: the assertion
would now come from a screen scraper rather than the model, which makes it *look* objective.

herdr's own semantics say not to. Its five statuses are `Idle | Working | Blocked | Done |
Unknown`, and:

- `Idle` means "prompt visible, nothing happening" (`src/detect/mod.rs:12`) — not that the
  work succeeded;
- `Done` is a UI concept, "the same underlying idle state after background work finishes,
  **until that tab is focused**" — it decays to `Idle` when a human looks at the tab;
- `Unknown` "does not prove successful completion", in herdr's own documentation;
- worst, a *known* agent matching no detection rule at all falls back to `Idle`
  (`src/detect/manifest.rs:527`), so a vendor UI change silently reads as *finished*.

## Decision

**herdr's status is a trigger. It is never a verdict.**

Reaching `idle` means one thing to Luna: *it is now worth running the verification*. The
node layer then runs the real tool — the test, the build, the file check, the commit
resolution — and only that result produces a transition:

```
herdr: idle
  ↓ (trigger only)
Luna:  run the stage's real verification
  ↓
  passed  → Complete{delivered, evidence}
  failed  → Fail
  no evidence at all → Block
```

A stage closes on evidence of observed effect, never on a status. This is ADR-0024 applied
to the one place where a plausible shortcut exists.

Three corollaries, each closing a specific hole the study found:

1. **`Unknown` and `Idle` without evidence stop the stage.** They never close it. Failing
   closed is the rule everywhere (ADR-0029).
2. **An unresolved operation means stalled, never done.** From agent-of-empires: "a genuine
   hang (no final text, or a tool call never resolved) still reports `Stalled`, never faked
   as done." This is a structural check that costs no trust in the model.
3. **Evidence carries scope.** `{kind, scope, status, exit_code, canonical_command}` with
   `scope ∈ {targeted, full}` and no upgrade path. hermes-agent's ledger "never upgrades
   targeted checks into repo green", and Luna's current `map[Artifact]string` cannot express
   that distinction at all — one test passing and a green suite are indistinguishable in the
   log today.

## Alternatives considered

- **`idle` closes the stage, verification runs afterwards** — rejected. It is faster to
  build and it is precisely the self-reported completion the study documented five times
  over. The `fail-to-Idle` default makes it worse than the general case: the failure mode
  arrives through a vendor's UI change, silently, on a path nobody is watching.
- **Trusting `Done` specifically, since it sounds terminal** — rejected on herdr's own
  definition. It is a not-yet-seen flag, not an outcome, and it decays to `Idle` when a
  human focuses the tab. A status that changes because someone looked at it cannot be a
  verdict.
- **Asking the agent to self-report completion in a structured form** — rejected. It is
  hermes-agent's `kanban_complete(summary, artifacts)`, where claimed artifacts are
  existence-checked only for scratch paths and model-authored metadata is fed to the next
  worker as ground truth. An unverified claim becoming a downstream premise is the specific
  damage Luna's contract exists to prevent.

## Consequences

- **Positive:** the failure mode shared by all five studied systems becomes impossible by
  construction. A stage cannot close without evidence, and the evidence names its own scope,
  so a targeted check can never masquerade as a full one.
- **Negative / costs:** every stage needs a real verification command, and stages whose
  output is genuinely unverifiable (prose, a plan) need an explicit answer rather than an
  implicit pass. Running the verification costs time that trusting a status would not.
- **Impacts:**
  - `Evidence` grows from `map[Artifact]string` to a structured record with `scope`;
  - the reducer gains a staleness rule — evidence recorded before the last edit to the paths
    it covers is stale, not passed. It is one comparison and a pure function of the log
    (hermes-agent's `last_edit_at > created_at`);
  - the node layer, not the engine, runs the verification and reports the verdict inward
    (ADR-0024);
  - `luna task show` can state not just that a stage closed but on what grounds, and at what
    scope.

## References

- Related documents: [ADR-0024](0024-the-reducer-is-pure-verification-runs-outside.md),
  [ADR-0027](0027-luna-runs-under-herdr-as-a-socket-client.md),
  [ADR-0029](0029-herdr-blocked-becomes-a-luna-block.md),
  [invariants](../invariants/core.md)
