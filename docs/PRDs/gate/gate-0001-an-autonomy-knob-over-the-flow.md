# PRD gate-0001: An autonomy knob over the flow

**Status:** NOT IMPLEMENTED
**Last reviewed:** 2026-08-12
**Source issue:** —
**RFC:** —

> Recorded so it is not lost, not to be built yet. Nothing here is decided: the open
> questions at the end are the point, and answering them is the work.

## Overview

One control that says how much of the flow runs without a person — from every gate asking,
through the lead answering the ones it can, to none stopping at all.

## Problem

Gates are the points where a person decides. Today the answer to "does this gate wait?" is a
profile, and profiles are named bundles: `interactive` stops at everything,
`turbo` and `nightly` stop at less
([ADR-0013](../../ADRs/0013-named-gate-profiles-per-task.md), [ADR-0026](../../ADRs/0026-the-log-records-the-gate-decision-not-the-policy.md)).

That works and is the right shape for *which* gates wait. What it does not express is *how
much judgement Luna is allowed to exercise on the ones that do*. A profile can skip a gate
or keep it; it cannot say "read the artifact, decide if it is obviously fine, and only ask
me when it is not".

The gap shows up as two things a person actually wants and cannot say:

- **"Ask me about everything"** — supervised work, every step confirmed. Roughly today's
  `interactive`.
- **"Handle what you can, ask me about the rest"** — the lead reads the gate's artifact,
  forms a judgement, answers it, and carries on; a person is involved only where the
  judgement is not safe to make.

The second is not a profile with fewer gates. It is a gate that is *answered by a model
rather than skipped*, which is a different act with different consequences for the audit.

## Goal

Let a person set how autonomous a run is, in one place, without editing which gates exist.

## Expected behavior

### Main flow

1. A run carries an autonomy setting — the knob.
2. At each gate, the setting decides among: a person answers, the lead answers, or nobody is
   asked.
3. Whatever answered is recorded. A gate answered by the lead and a gate answered by a
   person are the same transition and **must not** look the same in the log.

### Edge cases

- A gate the lead answers wrongly, discovered later.
- A gate whose artifact the lead cannot read or understand.
- A knob raised or lowered mid-run.
- The most autonomous setting meeting a gate that should never be automatic — the write
  confirmation, most likely.

### Error handling

- The lead being unable to judge falls back to asking a person. Failing towards the human is
  the direction that cannot silently do the wrong thing.
- The log must distinguish who answered, always. Evidence already carries `ScopeHuman` for
  "a person looked", and a model's answer is a different claim
  ([ADR-0032](../../ADRs/0032-the-contract-declares-how-each-artifact-is-verified.md)).

## Requirements

### Functional

- RF1: one setting, per run, controlling how much is answered without a person.
- RF2: at least three positions — everything asks; the lead answers what it can; nothing
  stops.
- RF3: the log records **who** answered each gate, not only what was answered.
- RF4: a gate the lead cannot decide falls back to a person rather than to a default.

### Non-functional

- RNF1: the knob must not become flow control by the model. The lead answering *a gate* is
  not the lead choosing *the next stage* — [INV-core-1](../../invariants/core.md) is not
  negotiable, and this is the decision most at risk of eroding it.
- RNF2: the setting is configuration; what it decided is history
  ([ADR-0026](../../ADRs/0026-the-log-records-the-gate-decision-not-the-policy.md)).

## System impacts

- **Affected services:** `internal/fsm` (the gate decision), `internal/lead` (whatever does
  the answering), `internal/cli` (setting it).
- **Data/persistence:** `GateWaited` already records the decision as a tri-state. Recording
  *who answered* is either a fourth value or a second field — an open question.
- **Observability:** a run where the lead answered six gates should be reviewable
  afterwards, which means the answers and their reasons have to be findable.

## Rollout plan

- Feature flag? no — the most conservative position is the current behaviour, so an unset
  knob changes nothing.
- Rollback strategy: configuration.

## Open questions

These are why this is a PRD and not an RFC. None is decided.

- [ ] **Is this a knob or a profile field?** Profiles already decide which gates wait, and a
      second control over the same gates could be one concept split in two. Against that:
      "which gates matter" and "how much judgement is allowed" really are different
      questions, and one dimension cannot express both.
- [ ] **Where is the line against INV-core-1?** The invariant already carves out the hybrid
      lead: a model decides *what to do about a failure*
      ([ADR-0002](../../ADRs/0002-hybrid-lead.md)) without choosing the happy path. A lead
      that answers gates is arguably the same carve-out — and arguably the beginning of the
      erosion the whole project exists to prevent. This question decides whether the feature
      is built at all.
- [ ] **Which gates are never automatable?** `confirm-write` is the obvious candidate: a
      commit is the one irreversible act in the flow. If some gates are exempt at every
      setting, that exemption belongs in the gate's definition, not in the knob.
- [ ] **How is a lead-answered gate recorded?** It cannot look like a human approval —
      that would be the falsification ADR-0043 refuses for the conversation layer, arriving
      by another door. Options: a fourth `GateWaited` value, a separate field naming the
      answerer, or evidence with a scope that is neither `human` nor a command's verdict.
- [ ] **Does the lead need the artifact, or the reasoning?** A review-artifact gate carries
      content to read. Answering it well may need more context than the gate holds, and
      fetching more is a step towards the lead having opinions about the work rather than
      about the flow.
- [ ] **What happens to a wrong automatic answer?** A human approval is final by
      construction. If the lead can be wrong, is there a way to notice and reverse — and
      does that need a new action, given the log cannot be rewritten
      ([INV-core-2](../../invariants/core.md))?
- [ ] **Does the knob change mid-run?** Raising autonomy halfway means gates before and
      after were decided under different rules, which the log must be able to explain.

## References

- Issue: —
- Related ADRs: [ADR-0002](../../ADRs/0002-hybrid-lead.md),
  [ADR-0013](../../ADRs/0013-named-gate-profiles-per-task.md),
  [ADR-0022](../../ADRs/0022-gate-carries-artifact-for-review.md),
  [ADR-0026](../../ADRs/0026-the-log-records-the-gate-decision-not-the-policy.md),
  [ADR-0043](../../ADRs/0043-luna-chat-is-the-layer-and-the-pane-is-a-proxy.md)
- Related invariants: [INV-core-1](../../invariants/core.md),
  [INV-core-2](../../invariants/core.md), [INV-core-10](../../invariants/core.md)
