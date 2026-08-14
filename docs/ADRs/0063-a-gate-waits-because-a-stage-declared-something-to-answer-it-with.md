# ADR-0063: A gate waits because a stage declared something to answer it with

**Status:** Accepted
**Date:** 2026-08-14

## Context

Two controls decide how supervised a run is, and a person has to set both.

**The profile** ([ADR-0013](0013-named-gate-profiles-per-task.md)) is a list of gate
**kinds** that wait: `interactive` waits at four, `turbo` at one, `nightly` at none. **The
knob** ([RFC-0006](../RFCs/rfc-0006-a-gate-checks-what-it-can-and-a-knob-says-who-judges-the-rest.md))
is a 0–10 scale saying who *answers* a gate that waited — the declared checks, the lead, or
a person.

They answer different questions, so this is not duplicated logic. It is a duplicated
**control**: two places where somebody tunes the same thing, and a person who sets one and
forgets the other gets a run that is not what they asked for.

Worse, the profile has quietly stopped meaning what it says. Measured against the shipped
flow as it stands:

| | |
|---|---|
| kinds the profiles name | `confirm`, `confirm-write`, `review-artifact`, `loop-ceiling` |
| kinds the flow actually opens | `confirm` ×1, `review-artifact` ×1 |

`confirm-write` left with [ADR-0062](0062-luna-does-not-integrate-a-task-ends-on-its-own-branch.md).
`turbo` waited for exactly that one kind — so **`turbo` and `nightly` are now the same
profile**, and nothing said so. A control whose values have silently collapsed into each
other is worse than no control, because it still reads as a choice.

The deeper problem is the same one [ADR-0049](0049-a-stage-declares-its-gate-and-what-a-review-costs.md)
already fixed once. Deciding by *gate kind* means the decision lives away from the stage it
governs: a project that renames a stage, or writes its own, gets whatever the kind list
happens to say — and the failure is silence.

## Decision

**Profiles are removed. A gate waits when the stage declared something to answer it with,
and the knob decides who answers.**

```
gate opens
   ↓
did the stage declare checks or judgement criteria?
   ├── no  → nobody was ever going to be asked; carry on
   └── yes → it waits, and the knob (RFC-0006) decides who answers
```

The two questions now have one home each: **the stage file** says whether a gate is
answerable at all, and **the knob** says who answers it. Neither repeats the other.

This also makes the decision travel with the thing it governs. A project that writes its own
stage declares its gates' criteria in the same file, and a renamed stage keeps them — which
is what ADR-0049 established for gates and is extended here to whether they wait.

### The loop ceiling is decided by the lead, from the history

One gate is not declared by any stage: `loop-ceiling` is raised by the reducer itself when a
loop stops converging ([ADR-0023](0023-three-separate-loop-ceilings.md)). There is no stage
file to write criteria in, so the rule above would never stop for it — and a ceiling nobody
answers is the infinite loop [INV-core-8](../invariants/core.md) exists to forbid.

**The lead decides what a spent ceiling means, reading the history: block the task, or put
it in front of a person.** Not a fixed rule, because the two endings are right in different
situations and the history is what separates them — a loop that produced nothing for three
rounds is a block, while one that is converging slowly is a question worth asking.

This is the [ADR-0002](0002-hybrid-lead.md) carve-out exactly as written: the lead decides
what to do about a **failure**, never which stage comes next. A spent ceiling is a failure
to converge, and choosing between block and ask is choosing a response to it.

With no lead configured — `luna run` needs no model — a spent ceiling **blocks**. That is
the conservative half and it matches ADR-0059: an unattended run that cannot ask must not
carry on looping.

## Consequences

- **One control.** A person sets the knob and nothing else. The failure mode where two
  settings disagree is gone because there is no second setting.
- **The decision is where the thing is.** Whether a gate waits is written beside the gate,
  in the stage file a project owns and edits.
- **A gate with nothing declared never waits.** This is the reversal worth stating plainly:
  under profiles, a gate with no criteria still stopped under `interactive`. It no longer
  does. A project that wants to be asked about a gate now says so by declaring what it wants
  asked — which is more work, and produces a gate that can say what it is for.
- **`turn_budget` moves out.** It lives in the profile today and has nothing to do with
  gates: it is the watchdog's clock (ADR-0034, ADR-0051). It becomes ordinary configuration.
- **`Profile` stays in the state and stops deciding.** It is history: every task in the
  store carries one, and replay reproduces the run as it happened
  ([ADR-0026](0026-the-log-records-the-gate-decision-not-the-policy.md)). Removing the field
  would make old logs unreadable, so it is kept and read by nothing — the same shape
  `lead.Autonomy` took when the knob absorbed it.
- **`GateWaited` keeps its meaning.** What a past advance recorded still replays exactly as
  before; this changes who computes a *new* decision, never how an old one reads.

## Alternatives considered

- **Keep both, and document the split.** Costs nothing and leaves the redundancy, including
  two profiles that are now identical. Rejected: the collapse of `turbo` into `nightly` is
  evidence that a control nobody can see through is a control nobody maintains.

- **Fold the profile into the knob as extra positions** — say, a knob that also decides
  whether a gate stops. Rejected because it puts *which gates matter* back in a central
  scale, which is the ADR-0049 failure again: a custom stage would inherit a number decided
  somewhere else.

- **Keep profiles for `turn_budget` alone.** Rejected as a name that survives its meaning: a
  "profile" that no longer decides anything about gates would still read like it does, which
  is how `turbo` got hollow in the first place.

- **A fixed rule for the loop ceiling** — always block, or always ask. Rejected in both
  directions. Always blocking discards ADR-0023's argument that a ceiling is a decision
  worth taking with the history in view; always asking strands an unattended run on a loop
  that was never going to converge.

## References

- [ADR-0013](0013-named-gate-profiles-per-task.md) — the profiles this supersedes
- [ADR-0023](0023-three-separate-loop-ceilings.md) — why a ceiling is a gate
- [ADR-0026](0026-the-log-records-the-gate-decision-not-the-policy.md) — the decision is
  history, the policy is not
- [ADR-0049](0049-a-stage-declares-its-gate-and-what-a-review-costs.md) — a stage declares
  its own gate
- [ADR-0059](0059-luna-reads-the-review-and-a-spent-ceiling-stops-the-task.md) — a spent
  ceiling must not resolve itself
- [RFC-0006](../RFCs/rfc-0006-a-gate-checks-what-it-can-and-a-knob-says-who-judges-the-rest.md)
  — the knob
- [INV-core-8](../invariants/core.md) — no infinite retry
