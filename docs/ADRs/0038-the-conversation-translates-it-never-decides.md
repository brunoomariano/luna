# ADR-0038: The conversation translates; it never decides

**Status:** Accepted
**Date:** 2026-08-11

## Context

The intended shape of working with Luna is a single point of contact: a person talks to
one thing, that thing talks to the agents, and the agents talk back to it. The person
should not have to know which stage is running or which pane an agent lives in.

Luna today has no such surface. A person drives it with `luna run`, `luna gates`, `luna
gate approve`, `luna unblock` — precise, and requiring them to know the vocabulary and
to poll for state.

The obvious way to build the single point of contact is the one the project exists to
reject. A live agent conducting the task — deciding which stage comes next, when to
retry, when to stop — is flow control in a model. It is swarm-forge's design, the one
the wave 5 study found failing: the pipeline's central verb was a phrase a model was
expected to interpret. INV-core-1 forbids it and ADR-0002 is built against it.

But "the person talks to one thing" and "a model decides the flow" are separable, and
conflating them is what makes the request look like it contradicts the premise.

## Decision

**A conversational layer translates in both directions. It never decides a transition.**

```
person: "how is LUNA-1 doing?"
   ↓  the model turns intent into a command
luna task show LUNA-1
   ↓  the model turns state into an answer
"waiting at the contract gate — want to see it?"
```

What the model may do: read the person's intent and pick a command; read Luna's state
and phrase it. Both are interpretation of *language*, which is what models are for.

What the model may not do, and what the code must make impossible rather than
discourage:

- choose which stage runs next — that is `NextStage` on the flow (INV-core-1);
- decide a gate on the person's behalf — a gate is answered by a person, and an
  approval the model synthesised is the forgery agent-of-empires' nonce design exists
  to prevent;
- declare a stage complete — that is the verifier's verdict (ADR-0028);
- write to the log directly. Every transition still goes through `Reduce`, which refuses
  an illegal one whoever proposed it.

The layer is a **client of the CLI**, not a component inside the engine. It has exactly
the authority a person at a terminal has, and no more — which is what makes the boundary
checkable rather than a matter of prompt discipline.

## Alternatives considered

- **A live agent conducting the task** — rejected on the project's founding premise. It
  is the design the study examined five times and found failing in the same way each
  time: flow in a prompt, completion self-reported. Luna exists because that does not
  work.
- **Commands only, no conversation** — rejected, though it is what exists and it is
  honest. It requires the person to know the vocabulary and to poll; the single point of
  contact is a real want, and refusing it because a bad implementation exists would be
  the wrong lesson.
- **Letting the layer answer gates when it is confident** — rejected outright. Confidence
  is not authority. A gate exists because someone decided a person should look, and a
  model deciding it has looked enough is the failure mode with the worst consequences of
  any in this document.

## Consequences

- **Positive:** the person gets one place to talk to, and the engine keeps deciding.
  Because the layer is a CLI client, everything it can do is already something a person
  could do — so the blast radius of a bad interpretation is bounded by the commands
  themselves.
- **Negative / costs:** a misread intent runs the wrong command. `luna run` on the wrong
  task is recoverable; the layer should confirm before anything that writes, and never
  chain commands the person did not ask for.
- **Impacts:**
  - the CLI becomes a contract, not just a surface: it needs machine-readable output for
    the layer to phrase, without which the layer would parse text meant for humans;
  - `luna gates` and `luna task show` are what the layer reads; nothing new is needed in
    the engine;
  - the layer needs no access to the store, and should not have it — its view of the
    task is whatever the CLI reports.

## References

- Related documents: [ADR-0002](0002-hybrid-lead.md),
  [ADR-0022](0022-gate-carries-artifact-for-review.md),
  [ADR-0028](0028-herdr-status-triggers-verification-it-never-closes-a-stage.md),
  [invariants](../invariants/core.md)
