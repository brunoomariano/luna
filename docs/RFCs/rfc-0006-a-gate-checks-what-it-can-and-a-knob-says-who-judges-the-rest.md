# RFC-0006: A gate checks what it can, and a knob says who judges the rest

**Status:** DONE
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
   ├── none declared       → nothing to run; straight to judgement
   ├── any exit ≠ 0        → reject. Nothing is judged: a failing command is an
   │                         objective answer, and asking a model to weigh it
   │                         would be inviting it to argue with an exit code.
   ├── a check cannot run  → a person answers, results attached
   └── all exit 0          ↓
JUDGEMENT — the criteria declared for this stage
   ├── no criteria declared → checks ran and passed? approve — they were the answer.
   │                          nothing ran either?     a person answers.
   └── criteria present     → the knob decides who judges:
         knob < gate's criticality  → a person answers
         knob ≥ gate's criticality  → the lead judges, against those criteria
                                      └── "I cannot decide" → a person answers
```

**Checks that pass are an approval, and that is the whole point.** A gate whose declared
checks all exit 0, with no judgement criteria beside them, is answered — nobody is asked.
Declaring `checks` for a gate *is* the statement that those commands answer it; a design
that ran them and then asked anyway would have made the declaration meaningless.

So the responsibility sits where it belongs: **a check that does not answer the gate is a
badly written check**, not a hole in the mechanism. `approve-plan` with
`checks = ["make fmt"]` gets approved by a formatter — which is wrong, and wrong in the
place a person can see and fix, in their own registry entry.

**A gate with neither checks nor criteria goes to a person**, which is every gate today and
what makes this additive: a project that declares nothing keeps being asked about
everything.

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

#### It is the *only* knob: `lead.Autonomy` derives from it

An autonomy knob already exists — `lead.Autonomy` with `ask`, `retry` and `decide`,
reachable as `luna lead --autonomy`. It bounds what the lead may do about a **failure**,
which is a different question from who answers a **gate**, and its own doc comment names
this PRD.

Two controls both called autonomy, one an enum and one a scale, is a worse product than one
control — a person tuning how supervised their run is should turn **one thing**. So
`lead.Autonomy` stops being set directly and becomes **derived from the knob**:

| knob | on a failure | at a gate with judgement criteria |
|---|---|---|
| `0` | retry once, then **ask** | always a person |
| `1–4` | retry once, then **ask** | the lead judges `criticality ≤ knob` |
| `5–10` | retry once, then **decide** | the lead judges `criticality ≤ knob` |

**Retry once comes first at every setting**, including `0`. It is the recovery whose bound
is already in the state, it needs no judgement to be safe, and spending a person's attention
on a failure that a second attempt would have cleared is the cheapest thing this design can
stop doing. What the knob changes is what happens when the retry is also spent: below the
midpoint the lead reports and waits; above it, the lead chooses within the carve-out
[ADR-0002](../ADRs/0002-hybrid-lead.md) already allows — and never widens to choosing a
stage.

**The knob is the only control, and its state is what decides failure behaviour.**
`AutonomyAsk`, `AutonomyRetry` and `AutonomyDecide` survive as an internal type — the
vocabulary the lead's brief is written in — but they become **derived**, computed from the
knob at the moment a failure happens. Nothing sets them: not a flag, not configuration, not
a caller. `ParseAutonomy` loses its callers along with `--autonomy`, and what replaces it
parses a number.

The asymmetry argument that shaped `ParseAutonomy` carries over and gets stronger, because
now it guards one value instead of two: an unset knob is `0`, the most supervised setting,
so a typo cannot quietly turn a watched run into an unwatched one — and there is no second
place where a different answer could be given.

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
- **The flow fingerprint does not move — measured, against this document's own
  expectation.** This section originally said the opposite: that `criticality` and `judge`
  are part of a fingerprinted gate, so populating the stock would strand every open task
  (ADR-0046), and that phase 3 therefore had to land the fields empty.

  Applying [ADR-0048](../ADRs/0048-a-field-read-by-the-reducer-is-history.md)'s rule
  literally lands the other way. A field the *reducer* reads is history; a field only the
  layer above reads is policy. These two decide **who is asked** at a gate opening now —
  they cannot change whether a past `Advance` suspended, because what replay consults is
  the recorded `GateDecision` and never the policy that produced it
  ([ADR-0026](../ADRs/0026-the-log-records-the-gate-decision-not-the-policy.md)). A gate the
  lead answered last week replays as `judged` whatever the stage file says today.

  So they are excluded from the digest, deliberately and with a test asserting it from the
  excluded side. The consequence is better than the plan: **a project can declare
  criticality on a running flow without stranding a single task.** Verified by running
  `luna flow check` before and after — the shipped flow fingerprints `a7da0f3c7ef41a06`
  both times.
- **Observability:** `luna status` and the log must say who answered each gate, and a run
  where the lead judged three gates has to be reviewable afterwards.
- **`luna lead --autonomy` goes away, and this is the one break.** Everything else here is
  additive; folding `lead.Autonomy` into the knob is not.

  **It is removed rather than aliased.** Keeping `ask`/`retry`/`decide` as names that map
  onto knob values would leave two ways to set one setting, which is the thing this fold
  exists to end — and the worse half of it, because a flag that silently means "knob 5"
  hides the gate consequences of the value it sets. A person passing `--autonomy decide`
  would be authorising the lead to judge gates up to criticality 5 without the word "gate"
  appearing anywhere.

  So the flag is refused with a message naming the knob, and `ParseAutonomy` stops being
  reachable from the command line: `lead.Autonomy` becomes a value the knob computes, never
  one a caller supplies. The failure is loud, at parse time, and says what to use instead.
- **Rollback surface:** set the knob to `0`.

## Rollout plan (phased)

> **Built, 2026-08-14.** All four phases and the seam between them. Measured end to end
> against real `bd` 1.2.1 and the built binary: a task declaring
> `{"luna_gates":{"confirm":{"checks":["test -f README.md"]}}}` records `checked` at the
> `scenarios` gate and nobody is asked, while the same flow with a check that fails records
> `waited` and stops. The `spec` gate, which declares nothing, records `waited` in both.
>
> One thing is deliberately narrower than the design above: a gate the lead **rejects** is
> recorded as `waited` rather than sent back. The reducer answers a gate at the moment it
> opens and there is no path from a judgement to a rejection that returns work — so the
> gate stays open with the lead's reasoning in front of whoever answers it. Approving is
> the only outcome the lead settles on its own, which is the conservative half.

1. **Phase 1 — the record.** `checked` and `judged` in `GateWaited`, and the scope for a
   judged gate. Nothing produces them. This is the part that touches replay and it lands
   alone.
2. **Phase 2 — the mechanical half.** `luna_gates` read from the registry, checks run in the
   delivered checkout, exit code answers. No model involved; this is RFC-0005 entire.
3. **Phase 3 — the declarations.** `criticality` and `judge` parse in `[gate]`, appear in
   `luna flow check`, and nothing consults them yet. They stay out of the fingerprint, so
   this lands in one piece rather than the two the migration note originally planned —
   populating the stock is now an ordinary edit, not a flow change.
4. **Phase 4 — the knob.** The task setting, the comparison against criticality, and the
   lead judging against declared criteria. `lead.Autonomy` becomes derived rather than set,
   and `luna lead --autonomy` is removed. Changing it in flight through a command
   that writes an event, and `luna status` showing what answered each gate.

- Feature flag? No — knob `0` is the flag, and it is the default.
- Rollback strategy: set it back to `0`.

**Phase 2 is the one worth landing first if only one lands.** It is RFC-0005 entire,
already measured, and involves no model: checks declared per gate, run in the delivered
checkout, exit code answers. A project that declares checks for one gate gets that gate
answered without a person and nothing else changes.

## Open questions

> **All answered, 2026-08-14 — the design is settled and nothing is built.** The status
> stays `DRAFT` because that is what this suite calls a document whose code does not exist
> yet; it is not a document still deciding. The rollout above is what remains.

- [x] ~~**What does the lead see when it judges?**~~ **The declared criteria, the delivered
      checkout, and a standing instruction not to trust prose.** The judging prompt is the
      design's, not the caller's: the measurement showed the same model, same artifact and
      same criteria going from 3/3 wrong to 3/3 right on the instruction alone, so leaving
      the shape to whoever writes the prompt would leave the result to chance.

      The instruction carries three rules, each earned by a measured failure:

      1. **Do not trust the artifact's own verdict.** A criterion the artifact contradicts
         is violated no matter what its verdict line says — this is what turned the
         rubber-stamp into a rejection.
      2. **Answer criterion by criterion and quote the line that settles each**, marking it
         met, violated or unsupported.
      3. **Verify rather than believe.** The checkout is there to be run against; a
         criterion whose evidence is a claim rather than a check is `unsupported`, not
         `met`. This is the one aimed at the failure the checklist did *not* catch — an
         irrelevant passing check cited as proof.

      Rule 3 is why the checkout matters to the judgement half and not only to the
      mechanical one: "verify, do not believe" is an empty instruction given to a lead with
      nothing to verify against.
- [x] ~~**Can the lead defer?**~~ **Yes, and it always falls to a person.** "I cannot decide
      from this" is a correct answer, not a failure, and the measurement showed the model
      reaching for it unprompted with sound reasoning — *"the artifact is a self-reported
      review whose claims I have not independently verified"*.

      The consequence is deliberate and worth stating: **a knob at 10 does not mean "never
      asks a person"**. It means the lead may judge every gate, and a lead that judges
      honestly will sometimes conclude it cannot. A run that stops for a question at the
      most autonomous setting is the design working, not a bug — and it is the same
      direction as `RF4`, failing towards the human.
- [x] ~~**Where do the checks run?**~~ **In a throwaway checkout of the delivered commit,
      cut from the main repository — and both halves use the same one.** Confirmed in code
      rather than assumed.

      The concern this answers is real and worth stating, because the obvious reading of it
      is wrong: *"a stage finished in its own worktree and the lead is somewhere else, so it
      cannot look at where the work happened — and nothing unvalidated may be merged."*

      **The stage's worktree is already gone, and that is deliberate.** It lives exactly as
      long as the stage and is removed when it ends; what survives is the commit, which is
      the handoff ([ADR-0055](../ADRs/0055-one-worktree-per-task-and-role-branched-from-the-last-delivery.md)).
      A reviewer's worktree that outlived its stage would make INV-core-7 a promise rather
      than a structure.

      So nothing needs to reach into another worktree, because the thing being validated is
      not a directory — it is a commit, and a commit is reachable from the main repository
      whatever tree produced it:

      ```
      main repository (.git — persistent, shared object database)
         ├── implementer's worktree   removed with its stage
         ├── reviewer's worktree      removed with its stage
         └── luna-gate-XXXX/tree      cut now, at the delivered commit,
                                      --detach, removed afterwards
      ```

      `node.CheckoutDelivered` already does exactly this for verifiers, and
      `node.Merger` does it again under `luna-merge-check-` before a merge — so "validate
      the commit, not the tree somebody worked in" is the established pattern here, and the
      gate is a third caller of it rather than a new mechanism. It costs a checkout, not a
      copy of the history.

      **Both halves get it.** The mechanical checks run with it as their working directory,
      which is INV-core-4 unchanged. The judgement half gets the same path, because the
      instruction above tells the lead to verify rather than believe and that requires
      something to verify against.

      **What this means for containment.** A gate is not a stage: it opens between stages,
      in Luna's own process, and Luna is what holds the repository path. If a judging lead
      is jailed, the checkout must be created outside and handed to it **as its `cwd`** —
      the ai-jail rule that only the working directory persists. The inverse fails silently:
      an agent creating the checkout from inside a jail would build it on tmpfs, work, lose
      everything on exit, and leave a prunable registration behind in the persistent `.git`.
- [x] ~~**How is the knob changed in flight?**~~ **A `luna` command that writes an event.**
      Re-reading configuration at each `Advance` was the simpler option and was rejected for
      what it costs: a run where three gates were answered by the lead has to be reviewable
      afterwards, and a setting that changed with no record turns "why was nobody asked
      here?" into a question the log cannot answer. The change is itself a decision, so it
      is history — the same reasoning as
      [ADR-0048](../ADRs/0048-a-field-read-by-the-reducer-is-history.md).

      **A herdr plugin pane is the surface, not a second source of truth.** It shows the
      current value and can change it, and the change it makes is the same command writing
      the same event — the pattern
      [ADR-0044](../ADRs/0044-the-proxy-is-a-plugin-pane-and-the-interpreter-is-a-harness.md)
      already set for the conversation: the pane is a proxy, never a path around the log.
- [x] ~~**What happens to a gate already open when the knob changes?**~~ **Nothing. An open
      gate keeps the answer it opened with.** If it opened needing a person, a knob raised
      afterwards does not take it away from them; only subsequent gates see the new value.

      The cost is real and accepted: raising the knob mid-run still gets you asked once
      more. The alternative is worse — a gate that is waiting for a person, and stops
      waiting because a setting moved, is a decision retroactively taken away from whoever
      was already looking at it.
- [x] ~~**What does a gate with no declared criticality mean?**~~ **It defaults to `10`.**
      The highest knob reaches it and nothing else does, so a gate that says nothing about
      how much it matters is treated as mattering most.

      Refusing to load it was the loud alternative and is wrong here specifically because
      this design is additive: every gate in the shipped stock declares no criticality
      today, and a loader that refused them would make an opt-in feature break every
      existing flow on arrival. Defaulting to the safest value keeps "declare nothing,
      change nothing" true, which is the property the whole design rests on.

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
