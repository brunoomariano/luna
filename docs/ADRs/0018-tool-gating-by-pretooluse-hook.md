# ADR-0018: Tool gating is a blocking hook, not an instruction to the agent

**Status:** Accepted
**Date:** 2026-08-06

## Context

Each role declares `tools_allow` and `tools_deny` (see
[ADR-0017](0017-defaults-plus-customization-everywhere.md)). What remains is deciding
**how** that declaration becomes a real restriction.

The SwarmForge study (see [references](../references.md)) shows the separation that
matters: there, the **transport** has mechanical enforcement — the script refuses a
malformed message with exit 2 — while the **work policy** is only prose in the prompt, with
no enforcement at all. Phrases like *"work only in your worktree"* or *"do not send `note`
without authorization"* depend entirely on the model's adherence.

It is the same class of failure that motivates the entire project (see
[ADR-0001](0001-flow-control-out-of-model.md)): an instruction in prose is a suggestion. A
`reviewer` instructed not to edit files will obey almost always — and the "almost" is what
costs, because whoever writes not reviewing is a separation that only holds if it is
guaranteed (see [invariants](../invariants/core.md), INV-core-7).

## Decision

The role's `tools_deny` is enforced by a **`PreToolUse` hook that blocks the call** before
it executes. The FSM restricts the toolset upon entering the stage; the agent does not
receive the forbidden tool as something it ought to avoid using — it simply cannot use it.

The role file has a double consumer: the FSM reads the metadata (`tools_allow`,
`tools_deny`) for the mechanical gating, and the agent reads the prose (`owns`, `not_owns`)
for judgment. One source, two uses.

This is one of the design's **three enforcement mechanisms**, and the only one that is
mechanical:

1. `PreToolUse` hooks that block a tool outside the stage — **mechanical**;
2. re-injection of the rule at every turn (`Re-read your role and constitution.`) — prose;
3. inactivity watchdog (see [ADR-0019](0019-inactivity-watchdog.md)) — mechanical, but it
   detects instead of preventing.

## Alternatives considered

- **Instruct the role in prose and trust adherence** — rejected because it is exactly the
  failure the project exists to fix. Under prolonged compaction agents lose their role
  identity; a rule that only exists in the prompt disappears with it.
- **Filter the output afterwards** (let the agent edit and revert what it could not) —
  rejected because the side effect has already happened by the time the detection runs, and
  because it turns a clear restriction into a fragile later correction.

## Consequences

- **Positive:** separation by denial stops depending on the model's goodwill. A `reviewer`
  without `Edit` is not a `reviewer` oriented not to edit — it is one that does not edit.
- **Negative / costs:** it ties Luna to each harness's hook surface. Claude, Codex and
  OpenCode expose this in different ways, or do not expose it at all.
- **Impacts:** each supported harness needs a gating adapter. Where the harness offers no
  pre-execution blocking, the mechanism degrades to prose — and that degradation must be
  visible, not silent.

## Open question

**How `tools_deny` becomes real gating in each harness** is the most concrete item on the
design's list of pending work, and it is only settled with code running against each CLI.
This ADR fixes the *principle* (mechanical blocking, not instruction); the mechanism per
harness is implementation still to be decided.

## References

- Related documents: [architecture](../architecture/overview.md),
  [invariants](../invariants/core.md), [references](../references.md)
