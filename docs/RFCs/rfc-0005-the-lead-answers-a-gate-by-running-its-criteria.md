# RFC-0005: The lead answers a gate by running its criteria

**Status:** DRAFT
**Last reviewed:** 2026-08-14
**Source issue:** —
**PRD:** [gate-0001](../PRDs/gate/gate-0001-an-autonomy-knob-over-the-flow.md)

## Motivation

A profile decides *which* gates wait, and it can only say yes or no: `waits` is a list of
gate kinds, so a gate either stops for a person or is skipped. What a person cannot say is
the thing they actually want in between — **"handle what you can, ask me about the rest"**.

[PRD gate-0001](../PRDs/gate/gate-0001-an-autonomy-knob-over-the-flow.md) recorded that gap
and left the obvious answer open: let a model read the gate's artifact and decide. That
answer was measured on 2026-08-13 against real artifacts the flow's own agents produced, and
it fails in the dangerous direction — 3/3 approvals of a delivery that violated an
acceptance criterion, **two of them naming the contradiction in their own reasoning before
approving**. A criterion-by-criterion checklist fixed that case (6/6) and then failed a
third artifact that proved a criterion by citing a passing command which did not test it.

Both failures share a cause: the lead was reading a **claim about** the work instead of
checking the work. That is the mistake the project already refuses everywhere else —
[INV-core-4](../invariants/core.md) does not ask whether the tests passed, it runs them.

Two things have changed since the PRD was written, and together they make the narrow version
buildable:

- **The task now carries acceptance criteria.** `registry.Work.Acceptance` reaches the state
  as `fsm.Statement.Acceptance` and is already in every brief. There is a declared standard
  to check against, which there was not before.
- **`confirm-write` is gone.** [ADR-0062](../ADRs/0062-luna-does-not-integrate-a-task-ends-on-its-own-branch.md)
  removed the merge, and with it the one gate whose real question — *"do I want this in my
  repository now?"* — was never a check. What remains are three gates about work in progress.

## Technical proposal

### Overview (guide-level)

The lead does not judge. It **runs what can be run**, and the outcome of running decides:

```
gate opens
   ↓
autonomy knob says the lead may answer?      no → a person answers (today's behaviour)
   ↓ yes
split the task's acceptance criteria
   ├── criteria that are commands  → run them
   └── criteria that are not       → carry them, unevaluated
   ↓
every command passed AND nothing is left unevaluated  → the lead approves
any command failed                                    → the lead rejects
something could not be evaluated                      → a person answers,
                                                        with the run results attached
```

The third branch is the common one and the point of the design. Real criteria are mixed —
measured, from the task that ran the full flow:

```
1. 'tally --json' emits valid JSON containing every word and its count.   prose
2. Without the flag the output is byte-identical to today.                prose
3. There is a test for both.                                              prose
4. 'make ci' is green.                                                    command
```

One of four is mechanically checkable. So the lead runs `make ci`, and the person is asked
about the other three **with that result already in hand** — which is strictly better than
today, where they are asked with nothing.

### Detail (reference-level)

- **The knob moves to the task.** `lead.Autonomy` exists (`ask`/`retry`/`decide`) and bounds
  what the lead does about a **failure**. Gates need the same knob at the gate decision,
  which happens in `Advance` — so the level has to be available *before* the gate fires, and
  has to be changeable mid-run.

  This is already how the design works: `Advance.GateDecision` is recorded per advance and
  the policy that produced it is not, precisely so a policy can change without rewriting how
  past events replay ([ADR-0026](../ADRs/0026-the-log-records-the-gate-decision-not-the-policy.md)).
  A task starts at a default level and a person raises or lowers it in flight; each gate is
  answered under whatever held at that moment, and the log says which.

- **`GateWaited` grows a third value.** Today: `waited` (a person answered) and `passed`
  (nobody was asked). A lead-answered gate is neither, and recording it as `waited` would
  make the audit say a person looked when none did.

- **The evidence gets its own scope.** `GateApprove` writes `ScopeHuman`, documented as
  *"a person's judgement, from a gate"*. A lead's answer is a different claim and needs a
  distinct scope. Note this does **not** stop a task finishing: no stage in the flow requires
  `human` — the declared scopes are `full` ×2, `targeted` ×1 and `existence` everywhere else
  — so the new scope satisfies every contract exactly as `human` does.

- **The criteria come from the registry.** `fsm.Statement.Acceptance` is already on the state
  and already in the brief. Deciding which lines are commands is the one genuinely new piece
  of parsing.

- **Impacted modules:** `internal/fsm` (`GateWaited`, `Scope`, `gateWaits`),
  `internal/lead` (the answering), `internal/node` (running a criterion — `fsm.Command` and
  `node.Shell` already do this for verifiers), `internal/cli` (setting the knob in flight).

## Alternatives considered

- **The lead reads the artifact and decides** — the PRD's original shape. Rejected on
  measurement, not principle: 3/3 approvals of a criterion-violating delivery, with the
  contradiction named in the reasoning. Confidence was 0.62–0.72, so a confidence threshold
  would not have caught it either.

- **The lead checks criteria against the artifact, quoting evidence** — better (6/6 on the
  stated defect) and still rejected: an artifact citing a passing command that does not test
  the criterion was approved 3/3 with the criterion marked "met". It verifies that a quote
  exists, not that the quote is about the criterion.

- **A second model that must agree** — not measured, and rejected as a direction rather than
  a result: two readers of the same claim share the same failure mode, and neither runs
  anything.

- **Keep it to the profile, with a fourth profile like `assisted`** — rejected because the
  profile answers "which gates matter" and this answers "how much judgement is allowed".
  One dimension cannot express both, and `waits` is a list of kinds with no room for a third
  outcome.

## Drawbacks

- **It only covers criteria that are commands.** On the one real task measured, that is one
  of four. The feature's reach is bounded by how mechanically a person writes acceptance
  criteria — and nothing here makes them write better ones.

- **It may push people to write criteria that are commands**, which is good when it makes a
  vague criterion concrete and bad when it makes a real requirement disappear because it
  could not be phrased as a shell line.

- **Deciding what is a command is parsing prose**, and it will be wrong sometimes. Failing
  towards "not a command" is safe (the person is asked); failing the other way runs
  something the criterion did not mean.

- **A third `GateWaited` value and a new scope are permanent additions** to two closed
  enums that replay depends on.

## Impact and migration

- **Data/persistence:** old logs replay unchanged — an absent `GateDecision` already falls
  back to the shipped policy, and the new value simply never appears in them.
- **Compatibility:** an unset knob keeps today's behaviour exactly, so this is additive.
- **Observability:** `luna status` and the log must distinguish who answered each gate.
  A run where the lead answered three gates has to be reviewable afterwards.
- **Rollback surface:** configuration — unset the knob and no gate is lead-answered.

## Rollout plan (phased)

1. **Phase 1 — the record.** The third `GateWaited` value and the new evidence scope, with
   nothing producing them yet. This is the part that touches replay, and it lands alone.
2. **Phase 2 — running a criterion.** Split `Statement.Acceptance` into commands and prose,
   and run the commands in the delivered checkout. Report only; no gate is answered.
3. **Phase 3 — the knob answers gates.** The autonomy level reaches the gate decision and
   the lead approves, rejects, or falls through to a person with results attached.
4. **Phase 4 — in flight.** Changing the level mid-run, and `luna status` showing what
   answered each gate.

- Feature flag? No — the unset knob is the flag.
- Rollback strategy: stop setting it.

## Open questions

- [ ] **What marks a criterion as a command?** A backtick, a leading `$`, a declared field
      in beads, or a heuristic? The measured criteria used backticks (`` `make ci` ``) but
      that is one sample, and a heuristic that guesses wrong in the permissive direction
      runs something nobody asked for.
- [ ] **Where does the criterion run?** The delivered checkout is the honest answer
      (INV-core-4), and it is what `node.CheckoutDelivered` already builds for verifiers.
      Worth confirming that a gate can reach one at the moment it opens.
- [ ] **What if a criterion cannot run at all?** Not "failed" — the command was missing, or
      the checkout would not build. That is the same distinction `Shell.Prove` already draws
      between a failing check and a check that could not run, and it must fall through to a
      person rather than count as a rejection.
- [ ] **Does the lead need a model at all for this?** Phases 1–3 as described are code:
      split, run, compare. If nothing here requires judgement, "the lead answers" may be the
      wrong name for it — and the autonomy knob may be bounding something that is not a
      model decision.
- [ ] **How is the level changed in flight?** A command, a file the lead re-reads, a signal?
      And what does it mean for a gate that is already open when it changes?

## References

- PRD: [gate-0001](../PRDs/gate/gate-0001-an-autonomy-knob-over-the-flow.md) — the
  measurements this route is built on
- Related ADRs:
  [ADR-0062](../ADRs/0062-luna-does-not-integrate-a-task-ends-on-its-own-branch.md) (removed
  `confirm-write`), [ADR-0013](../ADRs/0013-named-gate-profiles-per-task.md) (profiles),
  [ADR-0026](../ADRs/0026-the-log-records-the-gate-decision-not-the-policy.md) (why the knob
  can change in flight), [ADR-0002](../ADRs/0002-hybrid-lead.md) (the carve-out this sits
  beside), [ADR-0032](../ADRs/0032-the-contract-declares-how-each-artifact-is-verified.md)
  (scopes)
- Invariants: [INV-core-1](../invariants/core.md) — answering a gate changes `Status` and
  `Evidence`, never `Stage`, so the next stage stays the state machine's decision;
  [INV-core-4](../invariants/core.md) — the reason this runs criteria instead of reading
  about them.
