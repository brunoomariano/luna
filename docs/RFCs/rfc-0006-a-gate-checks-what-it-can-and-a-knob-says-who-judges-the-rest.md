# RFC-0006: A gate checks what it can, and a knob says who judges the rest

**Status:** DRAFT
**Last reviewed:** 2026-08-14
**Source issue:** —
**PRD:** [gate-0001](../PRDs/gate/gate-0001-an-autonomy-knob-over-the-flow.md)

> Supersedes [RFC-0005](rfc-0005-a-gate-is-answered-by-running-its-criteria.md), which
> covered only the mechanical half. Everything measured there still holds and is not
> repeated here — this is the design that puts the other half back, deliberately.

## Motivation

A profile decides *which* gates wait, and it can only say yes or no. What a person cannot
say is the thing they actually want:

> **"Handle what you can, ask me about the rest."**

[RFC-0005](rfc-0005-a-gate-is-answered-by-running-its-criteria.md) got half way there. It
established, on measurement, that a gate can be answered by **running** its criteria — exit
code decides, output never does — and that this needs no model at all. What it then did was
hand *everything else* to a person, which is a smaller feature than the problem deserves:
most of what a gate asks is not a command, and answering "then a human does it" for all of
it leaves the knob with almost nothing to turn.

This design keeps the mechanical half exactly as measured and adds the half it left out:
**judgement, against criteria written down in advance, done by the lead only as far up a
declared scale of criticality as a person allowed.**

## Technical proposal

### Overview (guide-level)

Every gate can carry two kinds of validation, and they coexist:

```
gate opens
   ↓
MECHANICAL — the checks declared for this gate, from the registry
   ├── any exit ≠ 0        → reject. Nothing is judged: a failing command is an
   │                         objective answer, and asking a model to weigh it
   │                         would be inviting it to argue with an exit code.
   ├── a check cannot run  → a person answers, results attached
   └── all exit 0          ↓
JUDGEMENT — the criteria declared for this stage
   ├── nothing to judge    → approve
   └── criteria present    → the knob decides who judges:
         knob < gate's criticality  → a person answers
         knob ≥ gate's criticality  → the lead judges, against those criteria
```

**Where each half is declared, and why they live apart:**

| half | lives in | because |
|---|---|---|
| mechanical checks | **the registry, per task** | *this* task must pass `make ci`; another must pass `make fmt && make typecheck`. It varies per task and is the person's statement about their own work. |
| judgement criteria | **the stage, per flow** | "no test without a docstring" is an engineering standard, true of every task. It belongs to the flow, which is the thing a project owns and edits. |
| criticality | **the stage, beside the gate** | how much a gate matters is a property of the gate. |

### Detail (reference-level)

#### The stage declares its criteria and its criticality

The gate already lives in the stage file, which `luna init` copies into `.luna/stock/` for a
project to edit. It grows two keys:

```toml
[gate]
kind        = "review-artifact"
artifact    = "contract"
reason      = "review the contract"
criticality = 7
judge = [
  "The contract states what is required and what is forbidden, with no suggestions",
  "Every acceptance criterion in the task appears as an obligation",
]
```

`criticality` is **1–10, higher is more critical** — one below the knob's range, and that is
the point: a gate exists because something about it matters, so `0` is not a criticality a
gate can have. It is a free scale rather than an enum because a project ordering its own
gates needs room between them, and because the numbers are compared, never enumerated.

Declaring it in the stage rather than in a central table is the same decision
[ADR-0049](../ADRs/0049-a-stage-declares-its-gate-and-what-a-review-costs.md) already made
for gates themselves: they were a `switch` over four stage ids in the reducer, so a project
that replaced the flow got no gates at all and a renamed review stage silently lost the
right to send work back. A central autonomy table would reproduce that exactly — a
custom stage nobody added to it would have no criticality, and the failure would be silence.

#### The registry declares the checks, per gate

Measured against bd 1.2.1: `--metadata` takes arbitrary JSON, round-trips as a `dict`, and
**merges by key** on update — so writing Luna's key leaves another tool's untouched.

```bash
bd create "..." --metadata '{"luna_gates": {
   "approve-plan": {"checks": ["make fmt", "make typecheck"]},
   "approve-spec": {"checks": ["make ci"]}
}}'
```

Keyed by gate, not global: a task may want `make ci` at one gate and something narrower at
another. Absent means no mechanical half, which is every task today.

This is declared rather than detected, and that was measured too: the two real tasks that
ran the full flow wrote the same criterion as `` 4. 'make ci' is green. `` and
`3. make ci green.` — no marker matches both, and a heuristic would have to infer that the
second is a command. A criterion is checkable **because someone said so**.

#### The knob

One setting, **on the task**, never per gate: *up to what criticality may the lead judge?*

**The knob is 0–10 and it absorbs every gate whose `criticality ≤ knob`.** Both are the
same scale, compared directly, so a project that writes `criticality = 7` next to a gate
knows exactly which setting reaches it.

- `0` — the lead judges nothing. Every gate with judgement criteria goes to a person. This
  is today's behaviour and the default, including when the setting is absent.
- `n` — the lead judges every gate whose `criticality ≤ n`.
- `10` — the lead judges all of them.

**The two ranges differ by one value, deliberately.** Criticality is **1–10**: a gate
exists because something about it matters, so there is no such thing as a gate of
criticality zero, and allowing one would create a gate that every knob setting absorbs
including the most conservative. The knob is **0–10** because "judge nothing" has to be
expressible, and it has to be what an unset value means — the same asymmetry
`ParseAutonomy` already reasons about: *"a typo that fell back to the permissive value
would turn a supervised run into an unattended one, and nothing would say so"*.

The consequence is worth stating plainly: `0` is the only setting under which no model
judges anything, and it is the floor rather than a special case.

It is a **task setting** because it is a property of the run, and per-gate configuration
would be a second place where "which gates matter" is decided, competing with the profile's
`waits`. What differs between gates is not the setting but their declared criticality.

It **changes in flight**, and the existing design already supports that:
`Advance.GateDecision` is recorded per advance and the policy that produced it is not,
precisely so a policy can change without rewriting how past events replay
([ADR-0026](../ADRs/0026-the-log-records-the-gate-decision-not-the-policy.md)). A task
starts at a default and a person raises or lowers it mid-run; each gate is answered under
whatever held at that moment.

#### What the log records

`GateWaited` has two values today — `waited` (a person answered) and `passed` (nobody was
asked). This adds **two**, because there are now two ways to be answered without a person,
and conflating them would lose the distinction that matters most:

- `checked` — the mechanical half answered it. The evidence is a command's verdict, so it
  carries `ScopeFull` or `ScopeTargeted`: scopes that already exist and already mean this.
- `judged` — the lead judged it. This needs a scope of its own: `GateApprove` writes
  `ScopeHuman`, documented as *"a person's judgement, from a gate"*, and a model's answer is
  a different claim. Recording it as `human` would make the audit say a person looked when
  none did — the falsification [ADR-0043](../ADRs/0043-luna-chat-is-the-layer-and-the-pane-is-a-proxy.md) refuses for
  the conversation layer, arriving by another door.

Neither stops a task finishing: no stage requires `human` — the declared scopes across the
flow are `full` ×2, `targeted` ×1 and `existence` everywhere else.

- **Impacted modules:** `internal/fsm` (`GateSpec`, `assignGate`, `GateWaited`, `Scope`,
  `gateWaits`), `internal/registry` (reading `luna_gates`), `internal/lead` (judging),
  `internal/node` (running a check — `fsm.Command` and `node.Shell` already do this),
  `internal/cli` (the knob, in flight), `src/stock/stages/*.toml`.

## Alternatives considered

- **The lead reads the artifact and decides** — the PRD's original shape, and **rejected on
  measurement**: 3/3 approvals of a delivery that violated an acceptance criterion, two of
  them naming the contradiction in their own reasoning before approving. This design is not
  that, and §"What makes this different" below is the argument for why.

- **Mechanical only, everything else to a person** — RFC-0005, superseded here. Safe and
  too small: most of what a gate asks is not a command, so the knob would have almost
  nothing to turn.

- **A central autonomy table** naming which gates each level absorbs — rejected for the
  reason ADR-0049 already established: a custom stage missing from the table would have no
  criticality and nothing would say so.

- **Criticality as an enum** (`low`/`medium`/`high`) — rejected because a project ordering
  its own gates needs room between them, and the values are compared rather than matched.

- **Checks in `acceptance_criteria` with a marker convention** — measured and rejected: the
  field replaces wholesale on update, so editing one criterion means resending all of them,
  and one mistake erases the rest. `--metadata` merges by key.

## What makes this different from the shape that failed

The measurement that killed free judgement is in the PRD, and this design has to answer it
rather than hope it does not apply. Three differences, and the third is the one that
matters:

1. **The criteria are declared in advance, by a person, in the flow.** The failed
   measurement gave the model an artifact and a goal and asked what it thought. Here it is
   given a list somebody wrote before the work existed, and asked about each item.

2. **The mechanical half runs first and can only reject.** Anything checkable is checked by
   a command before judgement is reached, so the model is never the thing standing between a
   failing test and an approval.

3. **The scale is the person's, and it starts at zero.** Nothing is judged by a model unless
   someone raised the knob past a number they themselves wrote next to that gate. The
   default judges nothing.

Honest about what that does not buy: the checklist shape was also measured, and it fixed the
stated-defect case (6/6) while still approving an artifact that proved a criterion with an
irrelevant passing check (3/3, criterion marked "met"). **Declared criteria do not make a
model reliable at judging prose.** What they do is bound what it is asked, make the failure
reviewable afterwards, and put the decision to allow it in a person's hands.

## Drawbacks

- **This reopens [INV-core-1](../invariants/core.md).** RFC-0005 had removed the model
  entirely; this puts it back. The invariant's letter is not violated — answering a gate
  changes `Status` and `Evidence`, never `Stage`, so the next stage stays the state
  machine's decision — but its spirit is now load-bearing on the knob's default and on
  criticality being written honestly. This is the drawback, not a footnote.

- **A wrong judgement is discoverable only afterwards.** A human approval is final by
  construction; a lead's is too, once recorded. `judged` in the log is what makes it
  reviewable, and nothing here makes it reversible.

- **Two places to look** when a gate behaves unexpectedly: the task's registry entry and the
  stage file. The split is principled — per-task versus per-flow — and it is still two.

- **Criticality is a number somebody assigns.** Nothing validates that `7` is more critical
  than `4` in any sense but the ordering, and a project that writes them carelessly gets an
  autonomy scale that does not mean what it says.

## Impact and migration

- **Data/persistence:** old logs replay unchanged — an absent `GateDecision` already falls
  back to the shipped policy, and the new values never appear in them.
- **Compatibility:** additive throughout. A stage with no `criticality` and no `judge`, a
  task with no `luna_gates`, and an unset knob together reproduce today's behaviour exactly.
- **The flow fingerprint moves.** `criticality` and `judge` are part of the gate, and the
  gate is fingerprinted — so adding them to the shipped stages changes the flow's identity
  and every open task stops replaying (ADR-0046). Landing them with no values in the stock
  avoids that; populating the stock is a deliberate flow change, as `commit` and `discovery`
  were.
- **Observability:** `luna status` and the log must say who answered each gate, and a run
  where the lead judged three gates has to be reviewable afterwards.
- **Rollback surface:** set the knob to `0`.

## Rollout plan (phased)

1. **Phase 1 — the record.** `checked` and `judged` in `GateWaited`, and the scope for a
   judged gate. Nothing produces them. This is the part that touches replay and it lands
   alone.
2. **Phase 2 — the mechanical half.** `luna_gates` read from the registry, checks run in the
   delivered checkout, exit code answers. No model involved; this is RFC-0005 entire.
3. **Phase 3 — the declarations.** `criticality` and `judge` parse in `[gate]`, appear in
   `luna flow check`, and nothing consults them yet.
4. **Phase 4 — the knob.** The task setting, the comparison against criticality, and the
   lead judging against declared criteria. Changing it in flight, and `luna status` showing
   what answered each gate.

- Feature flag? No — knob `0` is the flag, and it is the default.
- Rollback strategy: set it back to `0`.

## Open questions

- [ ] **What does the lead see when it judges?** The criteria and the artifact, certainly.
      The measurement suggests it should also be made to answer criterion by criterion and
      quote its evidence — that alone turned 3/3 wrong into 3/3 right on the stated-defect
      case. Whether that shape is mandated by the design or left to the prompt is open.
- [ ] **Can the lead defer?** "I cannot tell from this" is a correct answer and the
      measurement showed the model reaching for it unprompted, with good reasoning. If it
      can defer, a knob at 10 no longer means "never asks a person" — which may be right.
- [ ] **Where do the checks run?** The delivered checkout is the honest answer (INV-core-4)
      and `node.CheckoutDelivered` already builds one for verifiers. Worth confirming a gate
      can reach one at the moment it opens.
- [ ] **How is the knob changed in flight?** A `luna` command writing an event keeps it in
      the log where a person can see when it changed; configuration re-read at each
      `Advance` is simpler and leaves no trace of the change itself.
- [ ] **What happens to a gate already open when the knob changes?** Leaving it for the
      person is the conservative reading and probably right — but it means raising the knob
      still gets you asked once more.
- [ ] **What does a gate with no declared criticality mean?** `0` is not available — that
      value belongs to the knob and would make the gate absorbed by every setting. So the
      choice is between defaulting to `10` (no knob but the highest reaches it, safest) and
      refusing to load a gate that declares none (loudest). Safest and loudest are both
      directions this project takes elsewhere, and they disagree here.

## References

- Supersedes [RFC-0005](rfc-0005-a-gate-is-answered-by-running-its-criteria.md).
- PRD: [gate-0001](../PRDs/gate/gate-0001-an-autonomy-knob-over-the-flow.md) — the
  measurements every rejection here rests on.
- Related ADRs:
  [ADR-0049](../ADRs/0049-a-stage-declares-its-gate-and-what-a-review-costs.md) (why this
  goes in the stage), [ADR-0026](../ADRs/0026-the-log-records-the-gate-decision-not-the-policy.md)
  (why the knob can change in flight),
  [ADR-0062](../ADRs/0062-luna-does-not-integrate-a-task-ends-on-its-own-branch.md) (removed
  `confirm-write`), [ADR-0002](../ADRs/0002-hybrid-lead.md) (the carve-out this widens),
  [ADR-0043](../ADRs/0043-luna-chat-is-the-layer-and-the-pane-is-a-proxy.md) (why a judged gate cannot look human),
  [ADR-0054](../ADRs/0054-the-registry-is-beads-and-the-flow-is-not.md) (the registry the
  checks live in)
- Invariants: [INV-core-1](../invariants/core.md) — reopened deliberately, see Drawbacks;
  [INV-core-4](../invariants/core.md) — why the mechanical half runs rather than reads.
