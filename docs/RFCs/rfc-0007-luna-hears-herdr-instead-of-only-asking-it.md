# RFC-0007: Luna hears herdr, instead of only asking it

**Status:** DRAFT
**Last reviewed:** 2026-08-17
**Source issue:** —
**PRD:** —

> The largest gap the ADR audit found. [ADR-0027](../ADRs/0027-luna-runs-under-herdr-as-a-socket-client.md)
> and [ADR-0029](../ADRs/0029-herdr-blocked-becomes-a-luna-block.md) both assume Luna
> learns what herdr does; it never did. Everything below the protocol section is
> measured against herdr 0.8.0, protocol 19, running.

## Motivation

Luna only ever learns herdr's state as the **return value of its own call**. It sends
`agent.prompt` with an embedded wait, and whatever comes back is the entire picture. Between
one call and the next, herdr is a black box.

That is enough while Luna is the only thing acting. It stops being enough the moment
anything else does, and three of those were foreseen when the socket was adopted:

- **A person closes a pane behind Luna's back.** The next `agent.prompt` fails with a
  transport error, so a task that was working becomes an infrastructure failure with no
  explanation anybody can read.
- **An agent goes `blocked` between stages.** [ADR-0029](../ADRs/0029-herdr-blocked-becomes-a-luna-block.md)
  says a blocked agent becomes a Luna block, and it does — but only if Luna happens to be
  waiting on a prompt at that moment. An agent that asks a question while Luna is verifying,
  or between stages, waits for nobody.
- **A worktree is removed while a stage is running.** Measured this session, twice, on real
  runs: it surfaced as `chdir ...: no such file`, three retries, and a blocked task whose
  work was green.

The shape of all three is the same. **Luna finds out by failing.** The failure is honest —
nothing silently succeeds — but it is late, generic, and blames the wrong thing.

There is a fourth reason, and it is why this is worth building rather than noting: `luna
stuck` exists to answer "what needs a person", and it answers from replayed state. A task
whose agent is asking a question right now, and whose lead is not currently in a prompt,
is not stuck by any measure Luna has. It is just quiet.

## Technical proposal

### Overview (guide-level)

Luna opens a **second connection** to herdr and subscribes to the events it cares about. The
connection carries no requests: it is a stream of facts about a world Luna does not control.

```
connection 1  ──▶  worktree.create, agent.start, agent.prompt …
   (requests)  ◀──  their answers

connection 2  ──▶  events.subscribe
   (events)    ◀──  pane closed · agent blocked · worktree removed  (pushed)
```

Facts from that stream do not decide anything. They arrive as **actions the lead records**,
the same way a verification verdict does ([ADR-0024](../ADRs/0024-the-reducer-is-pure-verification-runs-outside.md))
— which is what keeps the reducer pure and the transition reproducible from the log.

### The protocol, measured

Against herdr 0.8.0, protocol 19, running. `herdr api schema --json` describes it; every
line below was also exercised over the socket, because the schema and the server have
disagreed before ([ADR-0036](../ADRs/0036-herdr-facts-learned-from-a-running-server.md)).

**Subscribing** is one call that turns the connection into a channel:

```json
{"id":"1","method":"events.subscribe",
 "params":{"subscriptions":[{"type":"worktree.created"},{"type":"worktree.removed"}]}}
→ {"id":"1","result":{"type":"subscription_started"}}
```

**Events are then pushed**, with no `id` and no request to match them to:

```json
{"data":{"type":"worktree_created","workspace":{"workspace_id":"w6M", …}}}
```

Four facts that shape the design, all measured rather than read:

1. **The connection becomes a channel.** Sending a second request on a subscribed
   connection resets it — `ConnectionResetError`, immediately. So this cannot reuse the
   request connection, and it cannot be the same `Client`.
2. **The event name is not the subscription name.** You subscribe to `worktree.created` and
   receive `"type":"worktree_created"`. A decoder keyed on the subscription string finds
   nothing, silently.
3. **Some subscriptions are targeted, some are global.** `worktree.removed` and
   `workspace.closed` subscribe with no target. `pane.agent_status_changed` is refused
   without a `pane_id` — *"missing field `pane_id`"* — so Luna must subscribe per pane, as
   it starts each agent.
4. **27 subscription types exist**, including every one ADR-0027 imagined: `pane.closed`,
   `pane.moved`, `pane.exited`, `worktree.removed`, `workspace.closed`.

### Which events Luna cares about, and what each one means

Three, and the discipline is to stop there. An event Luna cannot act on is noise that will
eventually be logged, and a log that fills with facts nobody reads is worse than one that
misses some.

| event | what it means | what Luna does |
|---|---|---|
| `pane.agent_status_changed` → `blocked` | the agent is asking a person something the flow did not foresee | `Block`, with the pane named — [ADR-0029](../ADRs/0029-herdr-blocked-becomes-a-luna-block.md) as written, now reachable between stages |
| `pane.closed` / `pane.exited` on a pane Luna is using | somebody took the agent away | `Block`, saying so — rather than the next prompt failing as transport |
| `worktree.removed` on a stage's checkout | the tree went away under a running stage | `Block`, naming the worktree |

All three become a **block with a named reason**, which is the ending INV-core-8 requires and
the one a person can act on. None of them decides a stage: that stays the FSM's
(INV-core-1).

- **Impacted modules:** `internal/herdr` (a second client and a decoder), `internal/lead`
  (recording what arrives), `internal/cli` (wiring it, and stopping it when the run ends).

## Alternatives considered

- **Poll `agent.list` between stages.** Cheap and needs no new connection. Rejected: it
  cannot see anything that happens *during* a stage, which is where two of the three cases
  live, and it turns a fact herdr already knows into a question Luna asks repeatedly.

- **Widen the existing `agent.prompt` wait.** It already waits for `blocked`, so more of the
  design could ride on it. Rejected because the window is the problem, not the mechanism: a
  prompt-shaped wait cannot cover the time when no prompt is outstanding.

- **Let events reach the reducer directly.** Simplest to write and would break the rule the
  whole engine rests on: the reducer reads no clock, no filesystem and no socket
  ([ADR-0024](../ADRs/0024-the-reducer-is-pure-verification-runs-outside.md)). An event has
  to become an action first, like every other observation.

- **Subscribe to everything and filter later.** Rejected on the same grounds as logging
  everything: 27 types with no consumer is a stream nobody reads, and the ones that matter
  are three.

## Drawbacks

- **A second connection is a second thing that can break.** It has to fail quietly: losing
  the event stream must not fail a run, because everything it reports is *also* discoverable
  the slow way. That is a real asymmetry with the request connection, where losing it means
  losing herdr ([ADR-0033](../ADRs/0033-losing-herdr-blocks-the-task.md)).
- **Events arrive concurrently with the lead's own writes.** Two writers on one log is
  exactly what `AppendActionAt` guards ([ADR-0047](../ADRs/0047-an-append-declares-the-position-it-decided-from.md)),
  so a losing event is refused rather than corrupting anything — but "refused" has to mean
  something sensible here, not a crash.
- **A pane subscription is per pane**, so Luna subscribes as it starts each agent and has
  nothing to unsubscribe with when the stage ends. Whether stale subscriptions accumulate
  over a long run is unmeasured.

## Impact and migration

- **Data/persistence:** no new action kinds if the three cases all become `Block`. Old logs
  replay unchanged.
- **Compatibility:** additive. A run with no event connection behaves exactly as today —
  which is also the fallback when the connection cannot be opened.
- **Observability:** a block whose reason came from an event should say so, or a person
  reading the log cannot tell "Luna noticed" from "Luna failed and guessed".

## Rollout plan (phased)

1. **The listener.** A second client, `events.subscribe`, a decoder for the three event
   types, and a test against a fake socket. Nothing consumes it.
2. **The seam.** The lead gains a way to receive a fact and turn it into an action — the
   shape `CheckGate` and `Land` already have.
3. **The wiring.** `luna run` opens the connection, subscribes to what it starts, and closes
   it with the run.

- Feature flag? No — an unopened connection is the flag, and it is the default until phase 3.
- Rollback: stop opening it.

## Open questions

- [ ] **Does a pane subscription need unsubscribing?** herdr closes it with the connection,
      and the connection lives as long as the run. Unmeasured over a run long enough for it
      to matter.
- [ ] **What does a losing append mean for an event?** The lead retries its own writes
      against a re-read state. An event that lost the race is still true — the pane really
      did close — so it probably wants retrying rather than dropping, and that is a
      different rule from the lead's.
- [ ] **Should `worktree.removed` block, or just be reported?** Now that a stage is verified
      against the delivered commit rather than its worktree, a tree removed mid-stage may be
      survivable rather than fatal. Measuring this is cheaper than arguing it.
- [ ] **Is `pane.exited` distinguishable from a normal end?** herdr fires it when the agent
      finishes too. Blocking on a pane that exited because the stage ended would break every
      run, so this needs the state to disambiguate.

## References

- [ADR-0027](../ADRs/0027-luna-runs-under-herdr-as-a-socket-client.md) — Luna is a socket
  client, and the events it would observe
- [ADR-0029](../ADRs/0029-herdr-blocked-becomes-a-luna-block.md) — a blocked agent becomes a
  Luna block
- [ADR-0024](../ADRs/0024-the-reducer-is-pure-verification-runs-outside.md) — the verdict
  arrives inside the action
- [ADR-0033](../ADRs/0033-losing-herdr-blocks-the-task.md) — losing herdr blocks the task
- [ADR-0036](../ADRs/0036-herdr-facts-learned-from-a-running-server.md) — measure the
  protocol, do not read it
- [ADR-0047](../ADRs/0047-an-append-declares-the-position-it-decided-from.md) — an append
  declares the position it decided from
- [INV-core-8](../invariants/core.md) — no failure is silent
