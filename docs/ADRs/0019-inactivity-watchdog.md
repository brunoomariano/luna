# ADR-0019: The inactivity watchdog watches the work, not the window

**Status:** Accepted
**Date:** 2026-08-06

## Context

The failure policy (see [ADR-0011](0011-failure-retry-rollback-or-block.md)) covers the
node that **fails**: retry up to 2, stage rollback, or blocking with a notice. It does not
cover the node that simply **stops** — returns no error, returns no success, returns
nothing.

The SwarmForge study (see [references](../references.md)) identifies this as the most
visible hole in that design: **zero liveness model**. Nothing there detects a stuck agent,
an agent that forgot to pass along, or a broken chain. The `swarm-window-watchdog` that
does exist watches **terminal windows**, not work — a live window with a stopped agent
inside passes as healthy.

The summary of the finding: *a fleet that stops talking, stops in silence.*

This collides with the rule that no failure is silent (see
[invariants](../invariants/core.md), INV-core-8). In a system whose purpose is to run
unattended — the `nightly` profile has no human gate at all (see
[ADR-0013](0013-named-gate-profiles-per-task.md)) — the failure nobody sees is costlier
than the failure that interrupts.

## Decision

Luna has an **inactivity watchdog** that watches the progress of the work: a task that
produces no transition, no tool output and no sign of life within a limit is treated as
stuck, and enters the same failure decision machine — the model chooses between resuming or
blocking with a notice (see [ADR-0011](0011-failure-retry-rollback-or-block.md)).

What is watched is the **work**, not the process nor the window. A live process that does
not progress is exactly the case the watchdog exists to catch.

## Alternatives considered

- **Watch the agent's process/window** (what SwarmForge does) — rejected because it is the
  wrong signal: the process stays alive while the work is stopped, which is precisely the
  failure mode to detect.
- **Trust the harness timeout** — rejected because each CLI has its own timeout semantics,
  and none of them knows the notion of "a stage that should have produced something". The
  harness timeout catches the hung process, not the task with no progress.
- **Have no watchdog and accept human intervention** — rejected because it nullifies the
  `nightly` profile. If every unattended run needs someone checking whether it got stuck, it
  is not unattended.

## Consequences

- **Positive:** it closes the "silently broken chain" failure mode, which is one of the four
  that motivate the project (see [ADR-0001](0001-flow-control-out-of-model.md)). It makes
  the `nightly` profile defensible.
- **Negative / costs:** it requires choosing an inactivity limit, and a badly calibrated
  limit produces false positives — killing a legitimately slow stage is worse than waiting.
  The limit probably needs to vary per stage.
- **Impacts:** node execution must emit an observable progress signal, which constrains the
  options for "who executes the node" — a decision still open.

## References

- Related documents: [architecture](../architecture/overview.md),
  [invariants](../invariants/core.md), [references](../references.md)
