# ADR-0056: The lead is an agent, and its obedience is structural

**Status:** Accepted
**Date:** 2026-08-13

## Context

[ADR-0002](0002-hybrid-lead.md) made the lead a Go loop with a model consulted only about
failures. That shape was right, and it held: code decides the next stage, deterministically,
at zero tokens.

It also cannot be talked to. A person with a question about their running task has nowhere to
ask it, and the conversation layer that would answer has to reconstruct everything the loop
already knows. [RFC-0002](../RFCs/rfc-0002-the-lead-is-an-agent-and-the-commit-is-the-handoff.md)
makes the lead an agent for exactly that reason and no other.

Which reopens the question [ADR-0001](0001-flow-control-out-of-model.md) closes. A Go loop
cannot decide to skip a stage — it has no way to express the thought. A model does, and it
will not express it as disobedience: it will express it as helpfulness. *These two stages are
small, I ran both and saved a round trip.* Nothing in that sentence looks like a violation,
and it is the exact failure this project exists to prevent.

## Decision

**The lead becomes an agent, and nothing it says can move the flow.**

The loop stays in Luna. `luna lead <task>` reads the order from `fsm.NextOrder`, hands it to
the model, and reads the task back from the registry. The model's answer is **printed, never
parsed**. There is no path from anything it says to a transition — no verb it can emit, no
JSON shape that is interpreted, no phrase that advances a stage.

What moves the task is `luna done`, which the lead runs like any other caller and which
checks the delivery against the contract exactly as it would for a person driving by hand.
A lead that reports finishing without running it finds the loop stopping with an error,
because Luna compares the log position before and against after.

That comparison, and not a turn cap, is what makes the loop finite. **A turn cap was written
first and then removed** — no test could reach it. The lead cannot keep a stage open, because
failing it spends the retry budget and the third failure blocks (ADR-0011); a task stopped
for any reason yields an order that is not `OrderRun`, which ends the loop. A ceiling nothing
can reach is one that gets trusted without ever having held, which is worse than not having
it. The did-it-move check is strictly stronger anyway: a lead that does nothing stops the run
on its first turn rather than its hundredth.

**The brief describes the mechanism rather than forbidding things.** "Do not skip stages"
invites a model to weigh whether this is one of the times. "The order you are given is the
only stage that exists for you" leaves nothing to weigh. The brief is not what enforces this
— the closed order is (ADR-0052) — but a lead that understands the shape it is in fights it
less than one handed a list of prohibitions.

**The autonomy knob bounds the one judgement the lead has.** `--autonomy ask | retry |
decide` governs what happens after a *failure*, which is the carve-out ADR-0002 already
allowed. It never widens to choosing a stage, and every setting carries the same line about
that. It defaults to `retry` — the narrowest setting that is still useful, because retrying
is the one recovery whose bound is already in the state — and an unknown value is refused
rather than defaulted, since a typo that fell back to the permissive value would turn a
supervised run into an unattended one in silence.

**`luna run` stays.** The Go loop is the path that needs no model, and it is what the tests,
the dry run and a machine with no lead configured use.

## Alternatives considered

- **Keep the Go loop and put the conversation elsewhere** — rejected because the conversation
  is the reason for the change. Splitting them means the conversation layer reconstructs what
  the loop knows, and the two drift.

- **Let the lead call the FSM and act on the answer** — rejected as ADR-0001 with extra steps.
  The moment the model holds the answer and decides what to do with it, it holds flow control.

- **Parse the lead's reply for a verdict** — rejected, and it is the tempting one, since a
  structured reply would save the read-back. It would also be the whole vulnerability: a
  model that can emit `{"done": true}` can end a stage by saying so. Reading the registry
  costs a query and cannot be talked into anything.

- **Remove `luna run`** — rejected. The loop with no model is what makes the flow testable
  without one, and deleting the path that needs no infrastructure to keep only the path that
  needs a model is the wrong direction for a project whose gate runs offline.

- **A turn cap as a backstop** — written, then removed as unreachable. See above.

- **Default the autonomy to `decide`** — rejected. An unset knob meaning "do whatever you
  think" is a default nobody would choose on purpose.

## Consequences

- **Positive:** a person can talk to the thing running their task, which is the whole point.

- **Positive:** the guarantee survives the lead being a model, and it survives structurally.
  The test that matters feeds the lead four different ways of claiming to have skipped ahead
  and asserts that all four do nothing.

- **Positive:** the loop is finite for a reason that can be checked, rather than because a
  number bounds it.

- **Negative:** obedience is enforced by the interface's shape, not by types. A lead that
  ignores its order and runs something else in its worktree is not prevented — it is only
  unable to make Luna record that as progress. RFC-0002 said so plainly and it is still true.

- **Negative:** the happy path now costs tokens, where the Go loop cost none. `luna run` is
  still there for when that matters.

- **Negative:** two ways to drive a task. They share the order, the contract and the
  verification, so what can drift is which one people reach for — not what happens when they
  do.

- **Impacts:** `lead.Agent`, `lead.Brief`, `lead.Autonomy`, `Env.Lead`, and `luna lead`.
  ADR-0002's hybrid split is preserved in substance: code decides the stage, a model judges
  failures. What changed is which side of that line the conversation lives on.

## References

- Related documents:
  [RFC-0002](../RFCs/rfc-0002-the-lead-is-an-agent-and-the-commit-is-the-handoff.md)
  (phase 4), [ADR-0001](0001-flow-control-out-of-model.md),
  [ADR-0002](0002-hybrid-lead.md) (extended, not replaced),
  [ADR-0011](0011-failure-retry-rollback-or-block.md),
  [ADR-0043](0043-luna-chat-is-the-layer-and-the-pane-is-a-proxy.md),
  [ADR-0052](0052-the-fsm-emits-an-order-and-the-panorama-is-a-separate-question.md),
  [PRD gate-0001](../PRDs/gate/gate-0001-an-autonomy-knob-over-the-flow.md) (the knob),
  [INV-core-1](../invariants/core.md)
