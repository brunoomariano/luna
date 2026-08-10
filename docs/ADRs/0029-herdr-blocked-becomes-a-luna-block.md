# ADR-0029: herdr's `blocked` becomes a Luna block

**Status:** Accepted
**Date:** 2026-08-10

## Context

herdr detects, by screen rules, that an agent is waiting on a human — an approval prompt, a
question, a permission dialog. It exposes that as `AgentState::Blocked`: "agent needs human
input and is blocked on a response".

Luna already has a concept with a similar name and a different meaning. A Luna **gate** is a
planned pause the flow declared in advance; a Luna **block** is an anomaly that stopped the
task and needs a person. herdr's `blocked` is neither: it is the agent asking for something
Luna did not plan for and cannot answer from the contract.

Two facts constrain the options:

- **There is no `approval.resolve` in herdr's API.** The only way to answer is
  `agent.send_keys` — read the pane text, decide which key, synthesise it.
- **herdr's `visible_blocker` can override an integration-reported state**
  (`src/detect/mod.rs:30-33`), so herdr may flip a pane to `blocked` while Luna considers
  the stage running.

## Decision

**A herdr `blocked` becomes `Block{reason}` in Luna's log. The human answers in herdr's
pane, then unblocks in Luna.**

```
herdr: pane.agent_status_changed → blocked
  ↓
Luna:  Block{reason: "the agent is asking for input"}   → notifies (INV-core-8)
  ↓
human answers in the herdr pane
  ↓
luna unblock LUNA-1 → the stage carries on
```

This treats herdr's signal as **input**, never as Luna's own gate state. The block is
honest: something happened that the flow did not foresee, a person is needed, and the log
says so. That is what `Block` already means, and it already notifies.

Gates stay what they are — declared by the flow, decided by the profile (ADR-0026),
answered through `luna gate`. The two concepts remain separate because they *are* separate:
a gate is planned, a block is not.

## Alternatives considered

- **Luna answers on the agent's behalf via `send_keys`** — rejected. With no
  `approval.resolve`, Luna would have to read the pane, interpret the prompt, and pick a
  key. That is per-vendor screen interpretation that breaks silently when a UI changes, and
  it puts Luna in the business of interpreting prose — the opposite of the project's
  premise. Worse, it would mean Luna approving something on a human's behalf without the
  human, which is the one thing a gate exists to prevent.
- **Treat `blocked` as liveness only, leave the task running** — rejected. It is the
  cheapest option and it produces a silently stuck task: nothing in the log, nothing in
  `luna gates`, no notification. That is the silent failure INV-core-8 forbids.
- **Map herdr's `blocked` onto a Luna gate** — rejected. A gate is declared by the flow and
  its wait is decided by the profile; this pause is neither declared nor profile-governed.
  Forcing it into a gate would make `luna gates` list something the flow never promised, and
  would let a profile silently skip a pause the *agent* asked for.

## Consequences

- **Positive:** no fragile screen interpretation, no key synthesis, and the pause is visible
  in the log and in notifications from the moment it happens. Reuses machinery that already
  exists and is already tested.
- **Negative / costs:** the human touches two surfaces — they answer the agent in herdr's
  pane and then unblock in Luna. That is one extra command, and it is the honest price of
  two systems that both have a legitimate claim on "waiting for a person".
- **Impacts:**
  - Luna subscribes to `pane.agent_status_changed` filtered to `blocked`;
  - the block's reason should name the pane, so a person can find what is asking;
  - because `visible_blocker` can override an integration state, Luna must tolerate a
    `blocked` arriving for a stage it believes is running — it is a fact about the pane, not
    a contradiction to resolve;
  - a future ADR may revisit this if herdr grows a structured approval channel, which would
    make programmatic resolution safe rather than brittle.

## References

- Related documents: [ADR-0022](0022-gate-carries-artifact-for-review.md),
  [ADR-0026](0026-the-log-records-the-gate-decision-not-the-policy.md),
  [ADR-0027](0027-luna-runs-under-herdr-as-a-socket-client.md),
  [invariants](../invariants/core.md)
