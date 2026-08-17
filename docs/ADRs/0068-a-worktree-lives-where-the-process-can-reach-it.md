# ADR-0068: a worktree lives where the process can reach it

**Status:** Accepted
**Date:** 2026-08-17

## Context

[ADR-0055](0055-one-worktree-per-task-and-role-branched-from-the-last-delivery.md) put each
stage's checkout in `../wt-<repo>-<task>-<role>`, a **sibling** of the repository. The
reasoning holds: a checkout nested inside the repository is caught by every recursive walk
the repository does to itself, and one inside a tool's directory is caught by that tool's
cleanup.

A full twelve-stage run under `ai-jail` then finished `done`, with green evidence on every
artifact, and `luna/<task>` still pointing at the seed commit. The fix, the tests, the
contract and the scenarios each sat on a different sibling branch, each holding a third of
the answer. The reviewer agent diagnosed it unprompted:

> Every stage branched off c887861 independently and none merged, so the fix, the tests, the
> contract and the scenarios each live on a different sibling branch.

**Inside `ai-jail` only the working directory is reachable.** A sibling of the repository is
not merely unwritable — it does not exist. Measured:

```
git -C ../wt-ciclo-probeX rev-parse HEAD      → 58bef18…      (outside)
ai-jail git -C ../wt-ciclo-probeX rev-parse   → fatal: cannot change to …
ai-jail git -C .luna-wt/probe  rev-parse      → 58bef18…      (inside, under cwd)
```

So every stage's `Handover` failed, and `node.Handover` returned `"", ""` — which is also
what it returns for a repository with no commit yet. Two different facts, one answer.

The consequence compounds one layer down. An empty commit makes `CheckoutAt` fall back to
`HEAD`, so the stage was verified against the repository's own head rather than against what
it delivered — **and passed**. A silent failure that also launders the evidence, which is
what [INV-core-8](../invariants/core.md) exists to forbid.

## Decision

**Two changes, and the first is the one that generalises.**

**1. `Handover` distinguishes "nothing was committed" from "the tree could not be read."**
It asks `rev-parse --git-dir` first — separately, because `rev-parse HEAD` cannot tell an
unreadable tree from a branch with no commits — and returns an error for the second case.
The node stops the stage on it rather than recording an empty commit.

**2. The checkout's location follows what the process can reach.** Uncontained it stays a
sibling, exactly as ADR-0055 says. Contained it is `<repo>/.luna/wt/<task>-<role>`:

- it is the one place reachable in both worlds;
- `.luna` is already Luna's directory, beside the log;
- a **linked** worktree is tracked through `.git/worktrees`, so it does not appear as
  untracked files in the repository it was cut from — which is the specific harm ADR-0055's
  "never a child" rule was protecting against.

Containment is read once, when the runner is built (`node.Contained()`, already the source
of this answer elsewhere). It is a property of the process and cannot change mid-run.

## Consequences

- **The base moves again.** Verified on a fresh run under the jail:
  `b3a7d67 → 7233862 → 7115165`, each stage branching from the last delivery, which is what
  ADR-0055 promised and RFC-0002 depends on.
- **A tree that cannot be read blocks, by name.** The same run before the location fix
  stopped at the first stage with `reading what …/wt-ciclo-fix-1 delivered: git rev-parse
  --git-dir: chdir …: no such file or directory` — twelve silent stages became one loud one.
- **The two fixes are independent on purpose.** The location fix stops this cause; the
  `Handover` fix stops *any* unreadable tree from being read as an empty delivery. A future
  sandbox with a different boundary is caught by the second even if the first does not
  anticipate it.
- **ADR-0055's rule is narrowed, not overturned.** "Never a child" was about tool cleanup and
  recursive walks, and both are still true of an ordinary directory. A linked worktree under
  `.luna` is neither.
- **`luna stuck` and the watchdog see it.** The failure now arrives as a block with a reason,
  which is the ending INV-core-8 requires.

## Alternatives considered

- **Fix only `Handover` and let the stage block inside a sandbox.** Honest, and it is half of
  what shipped. Rejected as the whole answer because it makes `luna run` unusable under the
  sandbox that is the *recommended* way to run it — an agent outside the jail stops to ask
  permission for Bash, which is the failure this cycle hit first.

- **Ask the sandbox to expose the sibling.** `ai-jail` can be told to allow a path. Rejected
  because it makes Luna's correctness depend on a third tool's configuration being right on
  every machine, and the failure mode when it is not is the silent one this ADR is about.

- **Keep the worktree inside `.git/`.** Reachable and already git's. Rejected: `.git` is
  git's own namespace, and putting a checkout there invites exactly the recursive-walk
  problem ADR-0055 named, with a worse blast radius.

- **Detect reachability rather than containment** — try the sibling, fall back on failure.
  Rejected because it decides layout from a failed syscall, so a transient error silently
  changes where a task's worktrees live mid-run. Containment is the actual property, and
  Luna already reads it.

## References

- [ADR-0055](0055-one-worktree-per-task-and-role-branched-from-the-last-delivery.md) — one
  worktree per task and role, branched from the last delivery; the sibling rule it sets
- [ADR-0035](0035-luna-runs-the-verification-itself.md) — Luna runs the verification, and
  it runs against the delivered commit, which is what an empty commit silently redirected
- [ADR-0028](0028-herdr-status-triggers-verification-it-never-closes-a-stage.md) — a status
  never closes a stage; only the verifier does, and one pointed at the wrong tree reports the
  wrong thing
- [INV-core-6](../invariants/core.md) — the handoff is the artifact, not a description of it
- [INV-core-8](../invariants/core.md) — no failure is silent, which is what this restores
