# ADR-0043: `luna chat` is the layer, and the pane is a proxy to it

**Status:** Accepted
**Date:** 2026-08-11

## Context

[ADR-0038](0038-the-conversation-translates-it-never-decides.md) settled that a
conversational layer translates in both directions and never decides a transition, and
that it is a client of the CLI with exactly the authority a person at a terminal has. It
did not say where the layer runs, how it reads state, or what it may do without asking.

Three questions, and the third turned out to have a better answer than the options
offered for it.

## Decision

### The layer is `luna chat`; the herdr pane is a proxy

The conversation lives in Luna's own CLI. A pane in herdr may host it — talking to a pane
beside the task's panes is the comfortable place to work — but **that pane is a proxy to
`luna chat`, not the layer itself**.

The distinction is what keeps ADR-0030's promise honest. If the layer lived inside a herdr
agent, leaving herdr would mean rebuilding it. As a proxy, the pane is a convenience: it
disappears and the layer keeps working from any terminal.

### It reads structure, not prose

Reading commands grow `--json`:

```
luna gates --json
luna task show LUNA-1 --json
```

The layer reads that; a person keeps reading the text. This is the consequence ADR-0038
already recorded — without it, the layer parses output written for humans, and every
reworded message becomes a silent breakage. It also makes the CLI a contract rather than
a surface, which is worth something to scripts that have nothing to do with the
conversation.

### It writes freely, except for one thing

`run`, `unblock`, `gate reject` and `gate adjust` execute on the layer's reading of
intent. The log is append-only, a misread intent costs a wasted run, and requiring
confirmation for everything turns a conversation into a form.

**`gate approve` is the exception, and it confirms.**

A gate exists because someone decided a person should look at something. Approving it is
not a command that happens to write — it is the statement *"a human looked"*, and the log
records it as indistinguishable from the person having read the artifact. A layer that
approves on its own reading is a model saying a human approved.

The wave 5 study found this exact threat treated as a threat: agent-of-empires generates
a nonce for every approval, sends it to the UI, and requires it echoed back —
specifically so that *"malicious agents synthesizing approvals"* cannot. The agent never
sees the nonce. Luna's version of that defence is cheaper and has the same shape: the
layer proposes, the person confirms, and the confirmation is what the log records.

## Alternatives considered

- **The layer as an agent living in a herdr pane** — rejected, and this is the option the
  proxy replaces. It puts the layer inside the thing Luna is meant to be able to leave,
  and it adds a resident agent that itself needs supervising. The comfort it buys — one
  screen, one place to talk — the proxy buys without the coupling.
- **A skill or MCP server, so the person uses their own agent** — rejected for now, not on
  principle. It is attractive: Luna hosts no model and the person keeps the harness they
  already use. It is also a different contract to design and version, and `luna chat`
  does not preclude it — a later MCP surface would call the same commands.
- **Reading the store directly** — rejected. It is faster and more complete, and it gives
  the layer authority no person at a terminal has, which contradicts what makes ADR-0038's
  boundary checkable.
- **Parsing the human-readable output** — rejected. Nothing to build, and it makes every
  message string a contract nobody declared.
- **Confirming every write** — rejected, deliberately. The friction is real and the
  recovery is real: the log survives a wrong `run`, and a conversation that asks twice for
  everything stops being one.
- **Approving gates without confirmation** — considered and rejected after the objection
  was raised once. It is the only write where the log cannot undo the meaning: a run can
  be run again, a block can be re-blocked, and an approval says something about a person
  that was not true.

## Consequences

- **Positive:** the person gets one place to talk to, and it is a place that survives
  herdr. Structured output makes the CLI usable by scripts and by a future MCP surface
  without either parsing prose.
- **Negative / costs:** `--json` is a second output format to keep correct, and the two
  will drift unless a test reads both. The single confirmation on `gate approve` is an
  exception someone has to remember when the layer feels inconsistent.
- **Impacts:**
  - `--json` belongs on the reading commands only; a write command's output is what it
    already prints;
  - the layer needs no store access and should not be given any — its view is what the
    CLI reports;
  - the proxy is a thin thing: a pane running `luna chat`, not a herdr agent with its own
    model, or the coupling comes back through the side door;
  - `gate adjust` writes and does not confirm, which is deliberate — it changes an
    artifact under review rather than declaring it accepted, and the gate still has to be
    answered afterwards.

## References

- Related documents: [ADR-0038](0038-the-conversation-translates-it-never-decides.md),
  [ADR-0022](0022-gate-carries-artifact-for-review.md),
  [ADR-0030](0030-the-node-boundary-keeps-herdr-replaceable.md),
  [invariants](../invariants/core.md)
