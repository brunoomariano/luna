# ADR-0059: Luna reads the review, and a spent ceiling stops the task

**Status:** Accepted
**Date:** 2026-08-13

## Context

[ADR-0041](0041-the-reviewer-cannot-write-and-its-report-is-the-handoff.md) specified how a
review sends work back, and specified it well:

> **Luna reads the report and decides the transition.** A report carrying a `[BLOCKING]`
> finding produces `ReviewFinding{Aligned: true}` […] The agent never emits the action. It
> reports; the code decides — which is INV-core-1 applied to the one place where letting the
> model decide would look most reasonable.

The reducer handled the action. The codec serialised it. **Nothing produced it.** It is
item B7 of [RFC-0001](../RFCs/rfc-0001-close-the-gap-between-decided-and-built.md), open
since the audit that found it, and the last place in the engine where a decision had no
production path.

The consequence went further than a missing feature. With no emitter, no review could return
work — so the three loop ceilings of [ADR-0023](0023-three-separate-loop-ceilings.md) counted
rounds that never happened. `Loop.NoProgress` was never incremented, and INV-core-8's own
text notes it as a known hole.

Wiring the emitter made that reachable, and the first run **did not terminate**. Measured:
eight rounds against a ceiling of four, `Oscillation` climbing to seven against a limit of
two, the counters passing every limit while the task looped between `build` and
`code-review` forever.

The cause is one line. A spent ceiling opened a gate *only if the profile said someone was
waiting*:

```go
if reason := ceilingHit(...); reason != "" && gateWaits(...) { … }
```

Under `nightly` — the profile whose whole purpose is that nobody is watching — `gateWaits`
is false, so the ceiling was computed, found to be blown, and discarded.

## Decision

**Luna reads the review report, and the report's shape is the contract.**

`fsm.ReadReport` parses the tagged findings ADR-0041 describes — `[BLOCKING]`,
`[SHOULD-FIX]`, `[NIT]`, `[UNCERTAIN]` — each with its stable id. `fsm.Blocks` says whether
any of them sends work back: exactly one `[BLOCKING]`, and nothing else counts. A pile of
`[SHOULD-FIX]` is not a `[BLOCKING]`; severity is the reviewer's judgement expressed once per
finding, and summing them would let the engine overrule it by arithmetic.

The parser is permissive about the prose around a tag — bullets, numbering, bold, an id or
none — and strict about the tag itself. An unrecognised tag is **not a finding**: it does not
degrade to "not blocking", it produces nothing, and the report stays in the context where a
person can see what was written.

Which report to read comes from the stage's own declaration (`ProducesForHuman`), not a name
the engine keeps. The four shipped review stages produce `qa_report`, `review_report`,
`mutation_report` and an architecture assessment; a hardcoded name would have worked for one
and silently skipped three.

**And a spent ceiling stops the task, whichever way the gate was answered.**

Where somebody is waiting, it opens a gate — ADR-0023's reasoning, unchanged: failing to
converge is a decision worth taking with the history in view, not a node anomaly.

Where nobody is waiting, it **blocks**, which is the ending that notifies. That is the
narrow revision. ADR-0023 preferred a gate to a block because there is a decision to take;
that holds wherever there is someone to take it, and where there is not, the choice is
between blocking and looping forever. INV-core-8 already names which one is allowed:

> *no infinite retry, which is the loop that does not converge and burns tokens*

## Alternatives considered

- **The reviewer calls `luna review-finding --aligned`** — rejected by ADR-0041 and still
  rejected. It is the direct route and it hands a transition to a model; nothing would stop
  a false `--aligned` and nothing would detect one.

- **Leave the ceiling passing under `nightly`** — rejected. It is defensible on the surface:
  `nightly` stops at nothing and a ceiling is a decision point, so with nobody to decide the
  flow carries on. But "stops at nothing" cannot include the ceiling that exists to stop a
  loop burning tokens, and the measured behaviour is a task that never ends. The profile
  chooses how much a person is asked; it does not get to choose whether the task terminates.

- **Block only when no decision was recorded** — rejected. It preserves the older test and
  closes half the hole, and the half it leaves open is precisely the path measured looping:
  the lead records `passed` under `nightly` on every round.

- **Count `[SHOULD-FIX]` towards blocking above some threshold** — rejected. Severity is the
  reviewer's call, made once per finding; a threshold is the engine second-guessing it with
  arithmetic.

- **Treat `[UNCERTAIN]` as blocking** — rejected. It is a statement about confidence, not a
  severity between two others (ADR-0041). A reviewer that is unsure has not found a defect,
  and blocking on doubt would fire the ceilings on it.

## Consequences

- **Positive:** a `[BLOCKING]` finding does what ADR-0041 said it would, two months after it
  said so. INV-core-7's separation now has an effect as well as a mechanism.

- **Positive:** the loop ceilings have something that can reach them, so ADR-0023 stops
  being a description of behaviour nothing exercised.

- **Positive:** an unattended run terminates. There is a test that loops a review forever and
  insists the task stops, and it hangs when the fix is reverted — which is how the defect was
  found in the first place.

- **Negative:** ADR-0023's "a ceiling opens a gate, it does not block" is now true only when
  someone is waiting. The distinction it drew is real and is preserved where it applies;
  saying it holds universally would be describing a system that loops forever.

- **Negative:** a review report is parsed with a regular expression, so a reviewer who
  invents its own format is read as having found nothing. That fails towards carrying on
  rather than towards blocking, which is the safer direction for a parser but does mean a
  malformed report is a silent pass — visible only because the report is in the context.

- **Negative:** `Loop.NoProgress` still has no feeder. Rounds and oscillation are counted
  from the transitions themselves; "produced no functional change" needs someone to compare
  two deliveries, which is [PRD node-0002](../PRDs/node/node-0002-no-progress-detection-and-the-shape-of-timeouts.md).

- **Impacts:** `fsm.ReadReport`, `fsm.Blocks`, `fsm.Finding`, `fsm.ReviewedArtifact`,
  `lead.readReview`, and the ceiling branch of `reviewFinding`. RFC-0001's B7 closes with
  this.

## References

- Related documents:
  [ADR-0041](0041-the-reviewer-cannot-write-and-its-report-is-the-handoff.md) (implemented
  here), [ADR-0023](0023-three-separate-loop-ceilings.md) (revised in one part),
  [ADR-0026](0026-the-log-records-the-gate-decision-not-the-policy.md) (the recorded decision
  still decides *how* it stops), [ADR-0020](0020-review-finding-invalidates-green.md),
  [RFC-0001](../RFCs/rfc-0001-close-the-gap-between-decided-and-built.md) (item B7),
  [INV-core-7](../invariants/core.md), [INV-core-8](../invariants/core.md)
