# RFC-0005: A gate is answered by running its criteria

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

### What decides: the exit code, never the output

A criterion passes when its command exits 0 and fails otherwise. Nothing reads the last
line, greps for "ok", or interprets what the command printed — that would be judging a claim
again, one layer down, and it is the mistake this whole route exists to avoid.

This is not a new rule. `node.Shell.Prove` already works exactly this way for artifact
verifiers, and the gate reuses it rather than growing its own:

```go
verdict := fsm.VerdictPassed
if exit != 0 {
    verdict = fsm.VerdictFailed
}
```

The output is still captured, and goes into the evidence's `Detail` for a person to read
afterwards. It informs; it does not decide.

Three states, not two — and the third is why this stays honest:

| result | meaning | answer |
|---|---|---|
| exit 0 | the check ran and passed | counts towards approving |
| exit ≠ 0 | the check ran and failed | rejects |
| **could not run** | no shell, no such directory, deadline hit | **falls through to a person** |

`Prove` already separates the third from the second, and the comment there says why:
recording it as a failure *"would tell the audit the tests ran and lost"*. A gate must draw
the same line — a criterion that could not be evaluated is not a criterion that failed.

### What this actually is

Every branch above is decided by an exit code. Nothing weighs, compares or forms an opinion,
and the three outcomes are the only three there are. **So this is code, not a model** —
and naming it "the lead answers the gate" would credit a component that does not
participate.

That reframing is worth more than it sounds, because it changes what the feature *is*:

- **It is a verifier at the gate**, the same shape `fsm.Command` and `node.Shell` already
  have for artifacts. The engine gains a gate that can carry checks, not a lead that gains
  judgement.
- **INV-core-1 stops being the question.** A model answering gates needed the invariant read
  carefully; a command's exit code answering a gate does not involve a model at all. What
  remains is the ordinary rule that verification runs outside the reducer and the verdict
  arrives in the action ([ADR-0024](../ADRs/0024-the-reducer-is-pure-verification-runs-outside.md)).
- **The knob is smaller than "autonomy".** It no longer bounds how much a model may decide;
  it says whether a gate with runnable criteria may be answered by running them. `ask` and
  `decide` are the whole range, and `lead.Autonomy` — which bounds what the lead does about
  a **failure** — stays a separate and unrelated knob.
- **The evidence scope changes meaning, for the better.** A lead-answered gate needed a new
  scope because "a model approved" is not "a person looked". A criterion-answered gate is a
  command's verdict, which is `ScopeFull` or `ScopeTargeted` — scopes that already exist and
  already mean exactly this.

What does **not** change is the third branch, which is still the common case and still hands
a person the work with the mechanical part already done.

The one thing genuinely left to a model is nothing here: deciding *which* criteria are
commands is a contract question (see the open questions), not an inference.

### Detail (reference-level)

- **One knob, on the task — never per gate.** It is a property of the run: *may a gate whose
  criteria are runnable be answered by running them?* Every gate in that task is then treated
  the same way, and what differs between them is not the setting but whether they have
  criteria to run.

  Per-gate configuration is deliberately rejected. It would be a second place where "which
  gates matter" is decided, competing with the profile's `waits` and drifting from it — and
  a person setting how supervised a run is should not have to enumerate gates to do it.

  It has **two positions**, and that is about the knob's range rather than its scope: there
  is no middle setting, because there is no behaviour in between. Either the criteria are
  run and their exit codes answer, or a person is asked. Nothing judges, so there is nothing
  to turn up halfway.

  It is not `lead.Autonomy` — that one bounds what the lead does about a **failure** and
  stays as it is, with its own three positions.

  It has to be available *before* the gate fires, which is at `Advance`, and changeable
  mid-run.

  This is already how the design works: `Advance.GateDecision` is recorded per advance and
  the policy that produced it is not, precisely so a policy can change without rewriting how
  past events replay ([ADR-0026](../ADRs/0026-the-log-records-the-gate-decision-not-the-policy.md)).
  A task starts at a default level and a person raises or lowers it in flight; each gate is
  answered under whatever held at that moment, and the log says which.

- **`GateWaited` grows a third value.** Today: `waited` (a person answered) and `passed`
  (nobody was asked). A gate answered by running its criteria is neither, and recording it
  as `waited` would make the audit say a person looked when none did. This is the one
  addition that survives the reframing above, because the *record* still has three cases
  even though the mechanism is code.

- **The evidence needs no new scope.** This was going to be the second addition, and the
  reframing removes it: a criterion that ran is a command's verdict, which is `ScopeFull` or
  `ScopeTargeted` — scopes that already exist and already mean exactly this. Only
  `GateApprove`'s current `ScopeHuman` has to stop being written when nobody human answered.

  Worth stating either way: this does not stop a task finishing. No stage requires `human`
  — the declared scopes across the flow are `full` ×2, `targeted` ×1 and `existence`
  everywhere else.

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

- [ ] **What marks a criterion as a command?** This is now the only hard question, and the
      second measurement made it harder. The two real tasks wrote the same criterion two
      different ways:

      ```
      task 1:   4. 'make ci' is green.      single quotes
      task 2:   3. make ci green.           nothing at all
      ```

      No marker matches both. A heuristic would have to infer that "make ci green" is a
      command, and inferring is exactly what must not happen: guessing permissively runs
      something nobody asked for.

      Which points at declaring it rather than detecting it — a beads field, or a convention
      the brief teaches and `luna task new` enforces. That turns a parsing problem into a
      contract, and contracts are what this project reaches for elsewhere. It also means a
      criterion is checkable **because someone said so**, not because a regex agreed.
- [ ] **Where does the criterion run?** The delivered checkout is the honest answer
      (INV-core-4), and it is what `node.CheckoutDelivered` already builds for verifiers.
      Worth confirming that a gate can reach one at the moment it opens.
- [x] ~~**What if a criterion cannot run at all?**~~ **Answered: it falls through to a
      person**, never counts as a rejection. `Shell.Prove` already draws that line and the
      gate reuses it — see the exit-code table above.
- [x] ~~**Does the lead need a model at all for this?**~~ **No.** Split, run, compare — all
      three outcomes are determined by exit codes, and none of them is a judgement. This is
      code, and calling it "the lead answering" would name it after a component that does
      not participate. See *What this actually is* below.
- [ ] **How is the setting changed in flight?** A `luna` command writing an event, or
      configuration re-read at each `Advance`? The first keeps it in the log where a person
      can see when it changed; the second is simpler and leaves no trace of the change
      itself.
- [ ] **What happens to a gate that is already open when it changes?** Raising the setting
      while a task waits could mean "answer it now with the criteria" or "this one was
      already asked, leave it". Leaving it is the conservative reading and probably right,
      but it means a person can raise the knob and still be asked once more.

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
