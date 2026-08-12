# ADR-0055: One worktree per task and role, branched from the last delivery

**Status:** Accepted
**Date:** 2026-08-12

## Context

[ADR-0027](0027-luna-runs-under-herdr-as-a-socket-client.md) settled one worktree per task,
and it was right for the shape Luna had: one agent at a time, working through the stages in
sequence, handing along a payload of pointers and a snapshot.

[RFC-0002](../RFCs/rfc-0002-the-lead-is-an-agent-and-the-commit-is-the-handoff.md) changes
both halves of that. The handoff becomes the commit, and the roles become separable — which
turns one shared directory from a simplification into two problems.

**A shared directory undoes INV-core-7.** "Whoever writes does not review" is currently a
denied `Edit` and a line in a brief. But a reviewer working in the implementer's directory
reads uncommitted files, half-finished edits and stale build output — so it reviews something
other than what was delivered, without disobeying anything. The separation was declared, not
structural.

**A base is not the same as a directory.** If the handoff is the commit, the next stage has
to *start from* that commit. A worktree that persists across stages starts from whatever the
last one left lying around, which is a different thing and quietly a worse one.

There is a third option that looks obvious and is not: one worktree per **role**, reused
across tasks. swarm-forge's fork tried it, and its own issue explains the result — role
branches are *"permanent and never reset"*, so divergence *"compounds at every hop"* and
feature N faces N-1 features of drift. swarm-forge itself now creates the worktree from
`HEAD` at each assignment and destroys it on retire.

## Decision

**One worktree per task *and* role, branched from the base, removed when the stage ends.**

- The branch is `luna/<task>/<role>`, so two roles on one task cannot land on the same branch
  and a checkout is findable without consulting Luna. A mechanical stage names no role and
  works in `luna/<task>` — there is nobody to keep it apart from.
- It is branched from `state.Base`, the commit the last closed stage delivered. Empty means
  the repository's own head, which is the first stage of a task. `worktree.create` already
  takes a `base` parameter — checked against the herdr binary, like every other fact about
  this protocol (ADR-0036) — so this costs passing a field.
- It is removed when the stage ends, on the failure path as well as the success path. What
  survives is the commit.

**The three travel as one struct.** `WorktreeSpec{TaskID, Role, Base}` rather than three
parameters, because a call that omitted the base would branch from whatever the repository is
on, silently discard every stage before it, and look like it worked.

**A cleanup that fails does not fail the stage.** By then the work is committed, and turning
"the stage delivered" into "the stage failed" because a directory would not go away loses the
more important of the two. It is reported through `Node.Warn` rather than swallowed: a
checkout left behind on every run eventually fills a disk, and the first anyone would
otherwise hear of it is that.

## Alternatives considered

- **Keep one worktree per task** — rejected. It makes INV-core-7 a promise rather than a
  property, and it makes the base whatever the previous agent left behind rather than what it
  committed.

- **One worktree per role, reused across tasks** — rejected on evidence rather than
  principle. It is what the fork had, and its own issue documents the drift compounding at
  every hop. swarm-forge moved off it.

- **Keep the worktree alive across a retry of the same stage** — rejected, and it is the
  tempting one. An implementer called again after a review would keep its context, which
  sounds like an efficiency. It is exactly what INV-core-5 forbids: the commit carries the
  context, so the session does not have to, and a stage that starts from a directory somebody
  was already working in is not starting clean.

- **Let the agent clean up its own worktree** — rejected. It is the one instruction most
  likely to be skipped when something else went wrong, and the case that matters is the
  failure path.

- **Fail the stage when cleanup fails** — rejected. The work is committed by then; discarding
  a delivered stage over a busy directory trades a large loss for a small one.

## Consequences

- **Positive:** INV-core-7 becomes structural. A reviewer cannot see the implementer's
  uncommitted work, because it is not in its filesystem — and there is a test that fails when
  the two share a branch.

- **Positive:** the handoff is the artifact. Each stage starts from the previous commit,
  which is what INV-core-6 now means.

- **Positive:** INV-core-5 gets stricter for free. Every stage, including a repeat of the same
  role, starts from a fresh checkout of a known commit.

- **Positive:** the drift swarm-forge's fork documented cannot accumulate, because nothing
  persists to accumulate in.

- **Negative:** more worktrees, created and destroyed more often. A worktree shares the object
  database, so it is a checkout rather than a clone — but a large repository makes this cost
  real, and a stage now pays it every time.

- **Negative:** anything cached in the worktree is lost between stages — `node_modules`, a
  build cache, a `.venv`. That is the same trade INV-core-5 already makes for context, now
  paid in seconds instead of tokens, and it is the strongest argument anyone will raise
  against this.

- **Negative:** `Runner` grew a fifth operation, and the interface's smallness was a stated
  virtue. Creating without removing was never a complete interface, though; the gap was the
  bug.

- **Impacts:** `herdr.Runner`, `herdr.WorktreeSpec`, `herdr.Node` (which gained `Warn`), and
  the socket runner's `OpenWorktree`/`CloseWorktree`. ADR-0027's "one worktree per task" is
  revised in that one part; everything else it settled about herdr holds.

## References

- Related documents:
  [RFC-0002](../RFCs/rfc-0002-the-lead-is-an-agent-and-the-commit-is-the-handoff.md)
  (phase 3), [ADR-0027](0027-luna-runs-under-herdr-as-a-socket-client.md) (revised in one
  part), [ADR-0006](0006-fresh-context-per-stage.md),
  [ADR-0036](0036-herdr-facts-learned-from-a-running-server.md),
  [ADR-0052](0052-the-fsm-emits-an-order-and-the-panorama-is-a-separate-question.md) (the
  order that carries the base), [INV-core-5](../invariants/core.md),
  [INV-core-6](../invariants/core.md), [INV-core-7](../invariants/core.md)
- Prior art: [references](../references.md) — swarm-forge's ephemeral per-assignment worktree,
  and the fork whose permanent role branches argued against them
