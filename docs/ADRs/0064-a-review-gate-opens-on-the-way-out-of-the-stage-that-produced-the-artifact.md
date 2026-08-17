# ADR-0064: A review gate opens on the way out of the stage that produced the artifact

**Status:** Accepted
**Date:** 2026-08-17

## Context

[ADR-0022](0022-gate-carries-artifact-for-review.md) gave a gate the power to carry what a
stage produced, so a person can approve it, edit it, or send it back. Its own wording says
when that has to happen:

> **reject** — the artifact does not enter; the stage that produced it runs again, with the
> rejection in context.

"The stage that produced it" is a stage that has already run. But `gateFor` attaches a gate
on **entry** to a stage, in `advance`, and `contract` is what `spec` *produces* — so the
gate opens before the artifact exists, and asks a person to review nothing.

This was found while executing RFC-0001 and recorded there rather than fixed:

> That is why `PendingGate.Payload` was never populated: at the moment the gate opens there
> is nothing to put in it. […] This is larger than the item it was found under: it changes
> when a gate opens, which is a transition, so it needs its own ADR.

It has been measured twice since, both times on real runs. `luna gate show` prints the
artifact's name and a blank line. And with [RFC-0006](../RFCs/rfc-0006-a-gate-checks-what-it-can-and-a-knob-says-who-judges-the-rest.md)
the cost grew: the lead is now asked to judge these gates, and it correctly refuses —
*"there is no artifact attached to this gate"* — so a capability that was measured 6/6 in
isolation has never once judged a real artifact in a real run. The knob turns and nothing
downstream of it can do its job.

Two more things the entry timing makes wrong, both visible in the code as it stands:

- `GateReject` sets `state.Stage = gate.Stage`, meaning "run this stage". On entry that is
  the stage about to start, so rejecting an unwritten artifact re-runs the stage that was
  never going to produce it in the first place. The ADR-0022 sentence describes the other
  stage entirely.
- `withPayload` fills from evidence and finds nothing, which is why it carries the
  documented caveat about the timing rather than being a plain read.

## Decision

**A `review-artifact` gate opens when the stage that produces its artifact closes, not when
the next stage begins.**

Concretely:

- `advance` opens a gate only for the kinds that ask about what is *ahead* — a `confirm`
  before a stage runs is a question about that stage, and it stays where it is;
- `complete` opens a `review-artifact` gate for the stage that just closed, after the exit
  check and the evidence have landed, so the payload is read from evidence that exists;
- `GateReject` sends the work back to the stage the gate came out of, which is now the
  stage that produced the artifact — the sentence ADR-0022 wrote.

The gate is declared where it always was: in the stage file, beside the stage it belongs
to ([ADR-0049](0049-a-stage-declares-its-gate-and-what-a-review-costs.md)). What changes is
which transition consults it, not who owns it.

## Consequences

- **The payload is populated.** The artifact exists by then, so `luna gate show` prints what
  a person is being asked about, and the lead is given something to judge (RFC-0006). This
  is the half that was measured missing.
- **A rejection re-runs the right stage.** `state.Stage` after a reject is the stage that
  produced the artifact rather than the one that was about to consume it.
- **A gate now sits between `stage_done` and the next `advance`.** The status a task waits
  in is unchanged — `awaiting_gate` either way — so `luna gates`, the slot release
  (INV-core-10) and the notification path are untouched.
- **Replay is unaffected.** What a past advance recorded is what it replays as
  ([ADR-0026](0026-the-log-records-the-gate-decision-not-the-policy.md)); this changes which
  transition computes a *new* decision. A task mid-flight when the binary changes sees the
  new timing from its next stage onwards, which is the same property every flow edit has.
- **The two kinds diverge, and that is the point.** `confirm` asks about work not yet done;
  `review-artifact` asks about work already done. Opening both at the same moment was what
  made one of them meaningless.

## Alternatives considered

- **Leave the timing and fill the payload from somewhere else** — the RFC-0001 stopgap,
  which carried the payload "when the artifact exists" and therefore never. Rejected: the
  artifact cannot exist at that moment by construction, so this is not a fix that was
  incomplete, it is one that cannot work.

- **Open the gate on entry to the stage *after* the producer** — which is what happens today
  for `spec`, since `build` follows it. Rejected because it is a coincidence of ordering
  rather than a rule: a producer whose consumer is conditional, or last in the flow, gets no
  gate at all. The gate belongs to the stage that produced the artifact.

- **Make the gate a stage of its own.** Rejected as the shape ADR-0049 already turned down
  for gates: it would put the decision in the flow's ordering rather than beside the thing
  it governs, and a project that reordered stages would move its gates by accident.

## References

- [ADR-0022](0022-gate-carries-artifact-for-review.md) — the gate carries the artifact, and
  a rejection re-runs the stage that produced it
- [ADR-0026](0026-the-log-records-the-gate-decision-not-the-policy.md) — the decision is
  history; the policy is not
- [ADR-0049](0049-a-stage-declares-its-gate-and-what-a-review-costs.md) — a stage declares
  its own gate
- [ADR-0063](0063-a-gate-waits-because-a-stage-declared-something-to-answer-it-with.md) — a
  gate waits because a stage declared something to answer it with
- [RFC-0001](../RFCs/rfc-0001-close-the-gap-between-decided-and-built.md) — where the defect
  was found and recorded
- [RFC-0006](../RFCs/rfc-0006-a-gate-checks-what-it-can-and-a-knob-says-who-judges-the-rest.md)
  — what the missing payload costs now that the lead judges
