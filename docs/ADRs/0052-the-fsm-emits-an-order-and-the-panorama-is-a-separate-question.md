# ADR-0052: The FSM emits an order, and the panorama is a separate question

**Status:** Accepted
**Date:** 2026-08-12

## Context

[RFC-0002](../RFCs/rfc-0002-the-lead-is-an-agent-and-the-commit-is-the-handoff.md) makes the
lead an agent, so that a person can talk to the thing running their task. That is the whole
reason for the change, and it reopens the question
[ADR-0001](0001-flow-control-out-of-model.md) exists to close: an agent that *decides* what
happens next is the model holding flow control.

Today the lead is a Go loop. It cannot decide to skip a stage because it has no way to
express the thought. An agent has one.

So the interface between the engine and whoever drives it has to be designed for a reader
that could disobey — which the Go loop never was. Two things follow, and the second is the
one that took a decision.

**What the engine returns.** A function that describes the situation and lets the caller work
out the move puts the decision in the caller. A function that returns the move does not.

**What else the caller can see.** This is the part that pulls in two directions. A lead that
knows the whole flow is a better conversational partner: it can say "three stages left, the
review is next". A lead that knows the whole flow can also notice that the next two stages
are cheap and run both before reporting back — saving a round trip, which is a helpful thing
to do and exactly the failure this project exists to prevent. The lead would not be
disobeying an order; it would be filling in a gap the order left.

## Decision

**`luna next` returns a closed order, and it carries no view of the flow.**

The order names the stage, the role, the agent, the worktree, the base commit, the brief,
the denied capabilities and what the stage owes. It is a literal command. The lead executes
it and reports through `luna done`; it chooses nothing about the happy path.

It comes in **both shapes** — `key=value` by default, `--json` for a caller that parses.
Text is what a person reads while driving the machine by hand, which is how phase 1 is meant
to be exercised before any agent is wired to it. The pattern follows `luna gates`.

**Reading an order is a read.** Asking twice returns the same instruction and writes nothing.
A caller that crashed mid-stage asks again and gets the stage it was on, rather than the one
after it.

**The panorama lives in `luna status`.** The whole flow, which stages were skipped and where
the task stands — everything the order deliberately omits. Asking for it is an explicit act,
separate from receiving an instruction.

That separation is the decision. The information is not withheld; it is *not bundled*. An
order that arrived with the flow attached would invite the reader to act on both, and the
distinction between "here is your instruction" and "here is the situation, act accordingly"
is the one this project is built on.

## Alternatives considered

- **Return the state and let the lead work out the move** — rejected as ADR-0001 with extra
  steps. It is the shape every prompt-chain orchestrator has, and the reason they cannot
  guarantee anything about their flow.

- **Attach the remaining stages to the order, marked as read-only** — rejected. A marker is
  a request, and the reader is a model. What is in the context gets used; labelling it
  "informational" does not change that, and the failure would be invisible — a lead that ran
  two stages and reported both would look efficient, not disobedient.

- **No panorama at all** — rejected as answering the wrong question. The lead being an agent
  is for the conversation, and a lead that cannot say how far along a task is makes for a bad
  one. The problem was never that the information exists; it was that it arrived attached to
  an instruction.

- **Have `luna next` advance the task as a side effect** — rejected. It reads well until a
  caller retries: an order that advances on being read loses a stage every time a process
  crashes between the read and the work, with nothing in the log saying so. Entering a stage
  is a transition and belongs to the reducer.

## Consequences

- **Positive:** the guarantee survives the lead becoming a model. Flow control stays in code
  because the interface has no place for the model to put a decision.

- **Positive:** the hand-driven path is real. `next`, `done` and `status` are enough to run a
  task with no agent at all, which makes phase 1 testable before any of the topology changes
  land.

- **Positive:** the order is idempotent, so retrying is safe by construction rather than by
  the caller being careful.

- **Negative:** obedience is enforced by the interface's shape, not by types. The order
  narrows what the lead can decide; it cannot stop a lead that ignores it and runs something
  else. RFC-0002 says so plainly, and that limit is real.

- **Negative:** two commands where a chattier design would have one. A caller that wants both
  makes two calls, and the extra round trip is the price of the separation being structural.

- **Impacts:** `fsm.Order` and `fsm.NextOrder`; `luna next`, `luna done` and `luna status`.
  `fsm.TaskState` gains `Base` and `fsm.Complete` gains `Commit` — the handoff becoming a
  field rather than a payload.

## References

- Related documents:
  [RFC-0002](../RFCs/rfc-0002-the-lead-is-an-agent-and-the-commit-is-the-handoff.md)
  (phase 1a), [ADR-0001](0001-flow-control-out-of-model.md),
  [ADR-0002](0002-hybrid-lead.md), [ADR-0024](0024-the-reducer-is-pure-verification-runs-outside.md),
  [INV-core-1](../invariants/core.md)
