# ADR-0057: The log lives in the main repository, and Luna is its only writer

**Status:** Accepted
**Date:** 2026-08-13

## Context

[ADR-0055](0055-one-worktree-per-task-and-role-branched-from-the-last-delivery.md) made every
stage run in an ephemeral worktree that is deleted when the stage ends. That change was about
isolation, and it quietly broke something else: **where the log lives.**

`storePath` resolved `.luna/luna.db` from `os.Getwd()`. Inside a worktree that is the
worktree — so a `luna` invoked from a stage's checkout created a *second* log, in a directory
built to be thrown away.

Measured rather than reasoned about. Running the binary from a linked worktree produced:

```
/main/.luna/luna.db          the task
/wt-role/.luna/luna.db       empty, and about to be deleted with its checkout
```

The task created in the main checkout was invisible from the worktree, and any task recorded
from inside one would have gone when the stage ended.

Two related questions came with it. **Who may write the log** — swarm-forge enforces a single
owner of shared state with an exit code, and Luna already applies that to the merge
([ADR-0053](0053-the-merge-is-code-and-the-watchdog-reads-a-clock-the-reducer-never-sees.md))
while leaving the log open to anything that could open the file. And **how much of the state
must be in the log at all**, once beads holds a registry.

## Decision

**The log is `.luna/luna.db` in the main repository, resolved with
`git rev-parse --git-common-dir`.**

The mechanism was chosen by measurement, and the obvious call is the wrong one. From inside a
linked worktree:

```
--show-toplevel   → /repos/wt-app-LUNA-1-reviewer   the worktree
--git-common-dir  → /repos/app/.git                 the main repository
```

`--show-toplevel` answers "which checkout am I in", which is exactly the question that must
not decide where shared state lives. A directory that is no repository at all is not an
error — Luna runs in plain directories, and the honest answer there is the directory itself.
`LUNA_STORE` still wins, because a person who names a path means it.

**Luna is the only writer.** `store.OpenAs(path, LunaOwnsTheLog)` claims it; `store.Open`
returns a store that reads and refuses to append. The check sits in `appendTx`, the single
choke point every append passes through, so a fifth entry point added later inherits the rule
instead of having to remember it. Reading needs no owner — replaying a task and listing what
is blocked are safe, and gating them would make every read command claim an ownership it does
not need.

An agent that appends does not corrupt a file: it **fabricates history**, and the history is
the audit trail (INV-core-2). That is why this rule matters more than the merge's.

**Running from a worktree is allowed and reported.** The log resolves to the main repository
either way, so it works — but `luna` says so on stderr, because someone who believes they are
in an isolated checkout should know the state they are changing is shared. `InsideAWorktree`
answers that from git rather than from an environment variable: an agent inherits and can
unset the environment, and it cannot make git lie about where it is standing.

**What stays in the log, and what does not.** The registry now holds status, stage and base,
so the question was whether the log could shrink to match. It cannot, and the reason is
worth recording because it is not obvious:

| | in the log | why |
|---|---|---|
| `Context` | yes | artifacts and facts decide which conditional stages enter, and a review **removes** an artifact when it sends work back — git has no way to un-commit an assertion |
| `Evidence` | yes | `Scope` is what separates a green suite from a file that exists, and `RecordedAt` vs `Seq` is the staleness rule (ADR-0032) — a comparison of log positions, which needs a log |
| `Status`, `Stage`, `Base` | log **and** registry | the log derives them; the registry publishes them so another checkout can see them |
| `Retry`, `Loop` | derived | counters the reducer recomputes; nothing writes them independently |

Which is the honest version of what [ADR-0054](0054-the-registry-is-beads-and-the-flow-is-not.md)
promised: the **registry** moved to beads. The **history** did not, because beads holds state
rather than a sequence of transitions, and the two are not the same thing.

## Alternatives considered

- **`--show-toplevel`** — rejected on measurement. It is the call everyone reaches for and it
  returns the worktree, which is the one answer that must not be used here.

- **Walk up looking for `.git`** — rejected. It finds a linked worktree's `.git` *file* and
  would need to parse it to get anywhere; `--git-common-dir` is git answering the question git
  is authoritative about.

- **Keep the log per-worktree and merge them** — rejected outright. Two logs for one task
  cannot be ordered without a clock, and the log's sequence is what the staleness rule
  compares against.

- **Detect an agent by an environment variable herdr sets** — rejected, and worth recording
  why: there is no such variable documented, and I could not measure one because no herdr
  server was running. Shipping a guard on an unverified environment variable would be a guard
  that silently never fires. Git's own answer needs no server.

- **A read-only mode enforced by file permissions** — rejected as the wrong layer. It would
  also stop Luna itself, since one binary both reads and writes.

- **Drop `Retry` and `Loop` from the log** — considered and not done. They are already derived
  by the reducer and cost no I/O, so removing them is arrangement rather than saving. Said
  plainly because it was the part of the original plan with the least behind it.

## Consequences

- **Positive:** the defect is fixed, and a test drives it through `run` end to end: a task
  created in the main checkout is visible from a worktree, and no second log appears.

- **Positive:** the log has an owner, checked at the one place every write passes through.

- **Positive:** what belongs in the log is written down, with the reason. The next person
  asking "can this move to beads" gets an answer rather than the question again.

- **Negative:** resolving the path costs a `git rev-parse` on every invocation. It is local
  and immediate, and it is bounded by a timeout like every other process Luna starts.

- **Negative:** `store.Open` has no caller in Luna's own binary, which the dead-code report
  names. It is kept deliberately — the ownership rule is only demonstrable if a store without
  it exists — and the doc comment says so rather than leaving it looking like an oversight.

- **Negative:** finding the root means running git, and the store may not run processes
  (`.golangci.yaml` enforces that). So `Root`, `DefaultPath` and `InsideAWorktree` live in
  `internal/node`, which already owns git — a package boundary decided by a linter rule,
  which is a slightly odd provenance for a design decision but the right place regardless.

- **Impacts:** `node.Root`, `node.DefaultPath`, `node.InsideAWorktree`, `store.Open`,
  `store.OpenAs`, `store.Owner`, and `cmd/luna`'s `storePath`.

## References

- Related documents:
  [ADR-0055](0055-one-worktree-per-task-and-role-branched-from-the-last-delivery.md)
  (whose worktrees exposed this), [ADR-0053](0053-the-merge-is-code-and-the-watchdog-reads-a-clock-the-reducer-never-sees.md)
  (the same ownership shape, for git), [ADR-0054](0054-the-registry-is-beads-and-the-flow-is-not.md)
  (narrowed here to what actually moved), [ADR-0024](0024-the-reducer-is-pure-verification-runs-outside.md),
  [ADR-0032](0032-the-contract-declares-how-each-artifact-is-verified.md),
  [INV-core-2](../invariants/core.md)
- Prior art: [references](../references.md) — swarm-forge's `ensure-main-git-owner!`, and its
  `.squad/` state living in the main repository rather than in any worktree
