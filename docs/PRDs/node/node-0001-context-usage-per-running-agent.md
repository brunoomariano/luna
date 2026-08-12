# PRD node-0001: Context usage per running agent

**Status:** NOT IMPLEMENTED
**Last reviewed:** 2026-08-12
**Source issue:** —
**RFC:** —

> Recorded so it is not lost, not to be built yet. Nothing here is decided: the open
> questions at the end are the point, and answering them is the work.

## Overview

Show how much context each running agent has consumed, so a person watching a fleet can
tell which sessions are close to their limit — and so Luna can eventually act on it before
the limit is reached.

## Problem

An agent runs inside a harness session with a finite context window. Today Luna knows
nothing about how full that window is. Two consequences follow.

**A person cannot see it.** With several tasks in flight there is no way to answer "which
of these is about to run out?" short of opening each pane and reading it.

**Luna cannot act on it.** A session that exhausts its context does not fail cleanly: it
degrades. The prior study named this failure mode — under prolonged compaction, agents lose
the identity of the role, and [INV-core-5](../../invariants/core.md) exists because of it.
Luna already attacks the cause by giving each stage a fresh context, but a single stage can
still be long enough to run out, and nothing notices.

The engine cannot ask this question itself: the reducer is pure, and token counts come from
outside ([ADR-0024](../../ADRs/0024-the-reducer-is-pure-verification-runs-outside.md)).
Whatever this becomes, it enters as observed fact through the node layer.

## Goal

Make context consumption visible per running agent, and make it possible to define a
threshold at which Luna does something about it rather than waiting for the degradation.

## Expected behavior

### Main flow

1. While an agent runs, its context usage is observable — some measure of how much of the
   window is spent.
2. `luna task show` and any fleet-level view report it for the agents currently running.
3. If a threshold is configured and crossed, Luna takes the declared action instead of
   letting the session degrade silently.

### Edge cases

- A harness that does not report usage: the answer is "unknown", said plainly, never
  guessed. The same posture as an evidence scope — a measurement that did not happen is not
  a measurement of zero.
- A session already over the threshold when Luna first looks.
- Usage that shrinks, because the harness compacted on its own.

### Error handling

- Losing the ability to measure must not stop the task. This is an observation, not a
  gate — unless a policy is explicitly configured to make it one.
- A threshold that fires must leave a trace in the log. A handoff that happened because a
  context filled up is exactly the kind of fact the audit exists to hold
  ([INV-core-2](../../invariants/core.md)).

## Requirements

### Functional

- RF1: report context usage per running agent, or say it is unknown.
- RF2: surface it where a person is already looking, rather than in a new place.
- RF3: allow a threshold to be configured, with a declared action when crossed.

### Non-functional

- RNF1: measuring must not depend on the engine — it is observed fact arriving through the
  node layer.
- RNF2: an unavailable measurement degrades to "unknown", never to a default number.

## System impacts

- **Affected services:** `internal/node` and the herdr boundary — whatever reports usage
  reports it there. Possibly `internal/herdr` if the multiplexer can answer it.
- **Data/persistence:** if a threshold action is taken, it is an event in the log. Whether
  usage *itself* is logged is an open question — a number sampled continuously is telemetry,
  not history, and the log is not a metrics store.
- **Observability:** this is the observability item; the question is which surface.

## Rollout plan

- Feature flag? probably not — reporting is additive, and the threshold is opt-in by being
  unset.
- Rollback strategy: the reporting is read-only; the threshold policy is configuration.

## Open questions

These are why this is a PRD and not an RFC. None is decided.

- [ ] **Can the harnesses even answer it?** Four official harnesses
      ([ADR-0042](../../ADRs/0042-four-harnesses-four-ways-to-deny-a-tool.md)), and this
      needs a per-harness answer measured against the binaries — not read from docs. The
      project has been bitten twice by trusting documentation over a running tool
      (ADR-0036, and `opencode --print`).
- [ ] **Or does herdr already know?** Luna runs under it, and it owns the pane. If the
      answer lives there, this is an observation Luna subscribes to rather than a
      measurement it takes.
- [ ] **What is the unit?** Tokens, percentage of window, messages? A percentage is
      comparable across harnesses and a token count is not — but a percentage needs a window
      size the harness may not report either.
- [ ] **What does crossing the threshold *do*?** The obvious answer is a handoff, and it is
      not obviously right. Luna's handoff carries pointers and a snapshot, never prose
      ([INV-core-6](../../invariants/core.md)), and a mid-stage handoff has no contract to
      satisfy — the stage has not produced what it owes. Options worth weighing: fail the
      stage and let retry give it a fresh context; open a gate; block and notify; or a
      mid-stage handoff that would need its own contract shape.
- [ ] **Does it belong in the log?** A sampled number is telemetry. The *decision* taken
      because of it is history. Those may be different records with different lifetimes.
- [ ] **Is this per agent or per stage?** [INV-core-5](../../invariants/core.md) gives every
      stage a fresh context, so a full window means one stage ran long — which may be worth
      knowing as a property of the stage rather than of the agent.

## References

- Issue: —
- Related ADRs: [ADR-0024](../../ADRs/0024-the-reducer-is-pure-verification-runs-outside.md),
  [ADR-0034](../../ADRs/0034-the-watchdog-delegates-detection-and-owns-the-verdict.md),
  [ADR-0042](../../ADRs/0042-four-harnesses-four-ways-to-deny-a-tool.md)
- Related invariants: [INV-core-5](../../invariants/core.md),
  [INV-core-6](../../invariants/core.md), [INV-core-8](../../invariants/core.md)
