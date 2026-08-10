# ADR-0031: Agents start through herdr's allowlist

**Status:** Accepted
**Date:** 2026-08-10

## Context

There are two ways for Luna to put an agent into a herdr pane.

`agent.start` is herdr's managed path. It takes a `kind` from a fixed allowlist of 21
recognised agents, requires an existing shell pane sitting at its prompt, and blocks until
the agent is confirmed present and interactive. Because herdr knows the kind, it applies
that vendor's detection manifest — the TOML rules that decide `working`, `idle` and
`blocked` for that specific agent's UI.

`pane.split` + `pane.send_text` is the generic path. It works for literally any command,
including agents herdr has never heard of, but herdr then has no calibrated rules for
reading that pane and falls back to generic detection.

The trade is coverage against fidelity: any agent with weak state detection, or 21 agents
with detection someone else maintains.

## Decision

**Wave 5 starts agents through `agent.start`, restricted to herdr's allowlist.**

The reason is that Luna's use of herdr's state is narrow and load-bearing. Under ADR-0028
the status never closes a stage — but it does decide *when to run the verification*, and
`blocked` becomes a Luna block (ADR-0029). Both want the most accurate reading of the pane
available, and the per-vendor manifests are that reading.

Restricting to the allowlist also keeps the failure surface small while the node layer is
new. If an agent is not supported, that is a clear error at task start, not a stage that
behaves oddly because herdr is guessing at an unfamiliar UI.

## Alternatives considered

- **`pane.split` + `pane.send_text` for everything** — rejected for now, and it is the
  strongest alternative. It supports any agent, it is one code path, and it is the same
  path Luna would use with its own CLI later. It was rejected because it discards herdr's
  per-vendor detection precisely where Luna depends on it: a generic pane makes `idle`
  weaker as a trigger and `blocked` less reliable as a signal, and both feed decisions.
  Revisit when Luna's own verification is strong enough that pane state matters less, or
  when the first genuinely needed agent falls outside the allowlist.
- **Both paths, chosen by kind** — rejected for wave 5. It is the eventual answer and the
  wrong starting point: two code paths and two failure modes from day one, in the layer with
  the least production exposure. Adding the generic path later is additive; the interface
  does not change.

## Consequences

- **Positive:** the pane state Luna reads is calibrated per vendor rather than guessed, and
  `agent.start` blocking until the agent is interactive removes a class of race from the
  node layer. One code path.
- **Negative / costs:** Luna runs only agents herdr recognises, and gains a dependency on
  someone else's allowlist. An agent Luna wants that herdr does not know is blocked until
  the generic path is added.
- **Impacts:**
  - `agent.start` never creates layout — the node layer must first produce a shell pane at
    its prompt, then start the agent into it;
  - an unsupported kind must fail loudly at task start with the supported list in the
    message, following the house rule that an error names what arrived and what was
    expected;
  - the stock roles and profiles have to name agents from the allowlist, or say plainly that
    they need the generic path;
  - when the generic path arrives it belongs behind the same `Node` implementation
    (ADR-0030), selected per kind — no new boundary.

## References

- Related documents: [ADR-0027](0027-luna-runs-under-herdr-as-a-socket-client.md),
  [ADR-0028](0028-herdr-status-triggers-verification-it-never-closes-a-stage.md),
  [ADR-0030](0030-the-node-boundary-keeps-herdr-replaceable.md)
