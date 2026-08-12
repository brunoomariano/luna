# ADR-0046: The log records which flow it was written under, and a task can be abandoned

**Status:** Accepted
**Date:** 2026-08-12

## Context

`Advance.Flow` carries `json:"-"`, so the flow a task ran under is never written to
the log. On replay the flow is re-injected from the current build — `reduce.go` falls back
to `DefaultFlow()` whenever the field is empty, and because the field is never serialised
it is empty on every replay. That fallback is not an edge case; it is how every
reconstruction works.

The reasoning that put it there is recorded and is sound as far as it goes:

> *"Flow is configuration, so it is not recorded. Storing it would freeze a task to the
> flow it started under, and flows are meant to be editable (ADR-0017)."*

But it answers a question nobody asked. Two things were folded into one:

| | What it is | State today |
|---|---|---|
| Recording **which contract a task ran under** | history | absent |
| **Forcing** a task to continue under it | policy | correctly rejected |

[ADR-0017](0017-defaults-plus-customization-everywhere.md) requires that flows be
**editable by different users** — not that a task in flight must follow a flow edited
underneath it. Rejecting the second discarded the first along with it.

What that costs, traced through the code. `NextStage` resolves position by looking the
current stage up in the slice it was handed, so the answer comes from where a stage sits
**today**, not from where it sat when the task passed through it:

| Change to the flow | On replay of an open task | Detected? |
|---|---|---|
| **Rename** a stage | becomes the new name, `running`, no error | **no** |
| **Insert** in the middle | returns to the new stage, blocks, and re-runs a stage it had already completed | **no** |
| **Delete** / **reorder** | `illegal transition: no stage is running` | yes, but fatal and unrecoverable |

The worst case is not the replay that dies — it is the one that passes. `stageIn` returns
a `Stage{ID: id}` with an **empty contract** when the id is not in the flow, so the exit
check disappears: a `Complete` delivering nothing closes cleanly, and
[INV-core-3](../invariants/core.md) stops holding with nothing to say so.

`Store.Replay` already argues this case for the two fields it does record:

> *"The kind and the profile are not parameters: they arrive in the log's opening
> TaskCreated event. Asking a caller for what the history already holds would let the two
> disagree."*

The flow is the one input still passed alongside the history, and it is the one that most
determines how the history reads.

**Why now, while it is cheap.** Today the flow is the binary — `src/stock/stages/` is
empty and there is no loader, so changing it means recompiling, and the window is a
developer with test tasks open. That window opens for real the moment flows become
user-editable, which is planned. Apache Airflow rendered historical runs against the
current DAG for ten years and paid for it with four AIPs and a destructive migration
(`delete from serialized_dag`), leaving every run older than 3.0 with a permanently null
version. Luna can record this before there is a single log that needs it.

A second problem surfaced while designing the first. If replay refuses on divergence, a
task whose flow changed becomes permanently unreadable — and since the store has no
`UPDATE` or `DELETE` ([INV-core-2](../invariants/core.md)), there is no way to clear it.
The same dead end appears without any of this: a task blocked three weeks ago is not
terminal, so an operational rule of "do not change the flow while tasks are open" is
blocked forever by a task nobody intends to finish. Both need a way for a person to say
*this one is over*.

## Decision

**Two things, decided together because the second is what makes the first survivable.**

### 1. `TaskCreated` records a fingerprint of the flow, and replay refuses a mismatch

The fingerprint covers the flow's **identity**: each stage's id, in order, with the
artifacts it requires, produces, and produces for a human.

It does **not** cover `Role`, `Verifiers`, or the body of a `When` condition. The rule
separating them comes from what a field does on replay: `Requires` and `Produces` decide
whether a past `Complete` closed its stage, so changing them rewrites history. `Role` and
`Verifiers` are read at the moment of use — changing them alters what happens next, not
what already happened. A fingerprint that covered them would refuse a replay because
somebody edited a brief, and a check that fires on changes that do not matter is a check
people learn to work around.

On replay: equal, continue; different, **stop and name the divergence**, exactly as the
store already does for an action a build does not recognise. A log with no fingerprint
replays as before, so existing logs keep working.

**This records history; it does not impose policy.** The flow still comes from the current
build, still is editable, and `Advance.Flow` stays `json:"-"`. What is written is the
*identity* of the flow, not its content.

### 2. `luna task abandon` ends a task by human decision

A new terminal action, carrying a reason. It is how a task that will not be finished stops
counting as open — whether it became unreplayable, or was simply superseded.

It is deliberately not a delete: the log keeps everything, and abandonment is one more
fact in it ([INV-core-2](../invariants/core.md)).

**`blocked` remains non-terminal.** A block is an anomaly a person can clear with
`unblock`, and treating it as an ending would erase the difference between "this failed
and someone should look" and "this is over". Abandoning is the explicit act, not a
reclassification of a state that already exists.

## Alternatives considered

- **Record the whole flow in the log** — rejected because it would freeze the task to the
  flow it started under, which is the objection ADR-0026 raised and which stands. Storing
  the *identity* keeps the flow editable while making the divergence visible; storing the
  *content* would make the log the source of the contract, and the contract is
  configuration.

- **Warn instead of refusing** — rejected because a warning printed on every flow change
  becomes noise, and noise becomes filtered. Worse, it would keep the current behaviour —
  silently reconstructing a task into a state it never occupied — with a line of log on
  top saying so.

- **Only the operational rule, with nothing in the log** (require a stop and validate that
  no task is open before switching flows) — rejected as *insufficient*, not as wrong. The
  rule is the primary defence and is being built. But it lives outside the log, so a replay
  cannot tell whether it was followed, and the checks that matter most are the ones that
  hold when the procedure was not. It also cannot answer, months later, which contract a
  task actually ran under.

- **Migration (`luna task migrate` with a validated mapping plan)** — deferred, not
  rejected. It is the Camunda answer and it is the right one when the need is real. Erlang
  OTP is the caution: `code_change/4` is specified with unusual rigour and every one of the
  eight production implementations in the OTP tree is the identity function, while the
  design guide never mentions it. Backwards compatibility solved the real case and the
  migration mechanism went unused. Detecting the divergence is worth more than migrating
  the log, and `abandon` covers the case migration would have served.

- **Treating `blocked` as terminal** so the operational rule stops being blocked by stale
  tasks — rejected because it conflates an anomaly with an ending. `unblock` exists
  precisely because a block is recoverable.

## Consequences

- **Positive:** a log becomes self-describing about the one input that most determines how
  it reads. "Which contract did this task run under?" becomes answerable from the history
  instead of from whatever binary is installed.

- **Positive:** the class of silent corruption — rename and mid-flow insert — becomes a
  loud refusal at startup. The two that already died loudly now die with a message that
  names what changed.

- **Positive:** the operational rule gains a way to be verified. A guard that lives only in
  a procedure is a promise; with the fingerprint, the log confirms it held.

- **Negative:** a task whose flow changed stops replaying, and the only recovery is to
  abandon it. That is the intended trade — reconstructing into a state that never happened
  is worse — but it is a real cost, and it is why `abandon` ships in the same decision
  rather than later.

- **Negative:** the fingerprint's coverage is a judgement call. Excluding `Verifiers` means
  a task can replay clean after the verification of an artifact changed, so the log will
  say an artifact passed without recording what would prove it today. That is deliberate:
  evidence records what actually ran (ADR-0024), and it is the evidence, not the contract,
  that carries the claim.

- **Impacts:**
  - `fsm.TaskCreated` gains a recorded field, and the codec serialises it.
  - `Store.Replay` compares before reducing.
  - a new terminal action, its CLI command, and the status that goes with it.
  - the shipped flow's fingerprint changes whenever a stage's artifacts change, which is
    the point — and means an open test task will refuse to replay after such an edit.

## References

- Related documents: [ADR-0026](0026-the-log-records-the-gate-decision-not-the-policy.md)
  (the same distinction, applied to gates),
  [ADR-0017](0017-defaults-plus-customization-everywhere.md),
  [ADR-0024](0024-the-reducer-is-pure-verification-runs-outside.md),
  [INV-core-2](../invariants/core.md),
  [RFC-0001](../RFCs/rfc-0001-close-the-gap-between-decided-and-built.md)
