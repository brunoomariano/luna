# ADR-0034: The watchdog delegates detection and owns the verdict

**Status:** Accepted
**Date:** 2026-08-10

## Context

`Watchdog` has been an interface since wave 6, consulted after every transition,
with no implementation. [INV-core-8](../invariants/core.md) carries an acceptance
criterion that is not met: a watchdog that exists but was never exercised does not
satisfy the invariant, it only describes it.

The failure it exists for is the one the design calls the most expensive: a fleet that
stops talking stops in silence (ADR-0019). Not a node that fails — that is reported and
handled — but one that is alive and accomplishing nothing.

The study found two mechanisms for it, and they catch different pathologies. herdr's
two-phase liveness demands an observed state change within a bounded window after a
prompt, then waits unbounded for the agent to settle; it keys on a **monotonic counter,
not a clock**. hermes-agent's guardrail hashes tool results and flags the same call
returning the same bytes — which catches an agent spinning, something a heartbeat
cannot see because a spinning agent keeps heartbeating.

## Decision

**herdr detects; Luna decides. The limits come from the profile.**

Detection is delegated. `agent.prompt` with the embedded wait already implements the
two-phase check, and a stall arrives as `agent_prompt_stalled` rather than as something
Luna computed. That keeps the clock outside the engine: what crosses the boundary is a
fact, and turning fact into transition is the lead's job (ADR-0024).

The verdict is Luna's, and it is a **block with a named reason**:

```
Block{reason: "the agent did not react to the prompt in stage \"build\""}
```

Not a failure and not a timeout. multica learned this in production and routes a tripped
watchdog to `blocked`, never `failed`, precisely so the three situations stay
distinguishable — they warrant different human responses, and collapsing them costs the
distinction at the moment it matters. It also must not spend the retry budget: a stall
says nothing about the stage's quality, and ADR-0011 keeps those apart for that reason.

The budgets live **in the profile**, beside the gate policy:

```toml
[profile.nightly]
waits = []
idle_budget = "30m"
tool_budget = "2h"
```

A profile that waits at no gate has no human watching, which makes the watchdog its only
net — so the profile that most needs a tight budget is exactly the one that already
declares how supervised the run is. Same decision, same place. Profiles are already
configurable (ADR-0026), so this needs no new mechanism.

Two budgets rather than one, from multica: an idle budget when nothing is in flight, and
a much larger one while a tool is running, because a real build legitimately runs silent
for many minutes. And **no wall-clock cap** — multica removed theirs after it killed
legitimate long runs. Progress, not elapsed time.

### What this deliberately does not cover

Delegating to `agent_prompt_stalled` catches *the agent did not react*. It does not catch
*the task is circling between stages without progressing* — build → qa → build with the
same result each round.

That gap is already covered by a different mechanism: the three loop ceilings (ADR-0023)
count rounds, no-progress and oscillation, and open a gate when one is spent. Building a
second detector for the same pathology now would mean calibrating it against no data.
When loops actually run (a later wave), the result-hash approach from hermes-agent is the
recorded candidate — computed from Luna's log, so it works across stages rather than
within a turn, and survives a restart.

## Alternatives considered

- **Computing no-progress in Luna from the log** — rejected for this wave, not on
  principle. It is strictly more capable than what herdr gives: cross-stage rather than
  per-turn, restart-proof, and keyed on content rather than on silence. It is also new
  detection to write and calibrate, for a pathology the loop ceilings already answer.
  Deferred with its design recorded rather than dismissed.
- **Both layers now** — rejected as scope. Two sources of truth to calibrate in the wave
  that has the least production exposure, when one of them duplicates an existing
  mechanism.
- **Consulting the Judge, as the lead does today for a stall** — rejected. It spends
  tokens to diagnose something the log already states plainly, and it puts a model in the
  loop on an infrastructure event, which is the opposite of ADR-0002's hybrid rule: the
  model is for judgement, not for reading a status.
- **Opening a gate instead of blocking** — rejected. A gate is declared by the flow and
  governed by the profile (ADR-0013); a stall is neither. Worse, under `nightly` the gate
  would not wait, so the profile with no human watching would sail past the one signal
  built for it.
- **Fixed constants in code** — rejected. It is simpler and defensible, but it gives
  `nightly` and `interactive` the same net, when the whole point of the profile is that
  they are supervised differently.

## Consequences

- **Positive:** INV-core-8's acceptance criterion becomes meetable — the watchdog is
  exercised by a test that makes the agent stop reacting. No clock enters the engine, and
  the three failure situations stay distinguishable in the log.
- **Negative / costs:** Luna inherits herdr's detection quality, including its blind
  spots. A stall that herdr misreads is a stall Luna misses, and the mitigation is the
  loop ceilings rather than anything in this layer.
- **Impacts:**
  - the `herdr` package surfaces `agent_prompt_stalled` as a distinct condition, not a
    generic error, the same way it distinguishes a lost socket (ADR-0033);
  - profile parsing grows two duration keys, with the same fail-closed rule as the rest:
    a malformed budget must mean the cautious value, never none;
  - a task whose profile declares no budgets uses the shipped defaults, so an old profile
    keeps working;
  - the lead stops routing `Stalled` through `handleFailure` and records the block
    directly — a stall is a decision, not a judgement call.

## References

- Related documents: [ADR-0019](0019-inactivity-watchdog.md),
  [ADR-0011](0011-failure-retry-rollback-or-block.md),
  [ADR-0023](0023-three-separate-loop-ceilings.md),
  [ADR-0026](0026-the-log-records-the-gate-decision-not-the-policy.md),
  [ADR-0027](0027-luna-runs-under-herdr-as-a-socket-client.md),
  [invariants](../invariants/core.md)
