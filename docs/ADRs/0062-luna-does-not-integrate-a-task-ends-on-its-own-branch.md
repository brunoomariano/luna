# ADR-0062: Luna does not integrate; a task ends on its own branch

**Status:** Accepted
**Date:** 2026-08-14

## Context

[ADR-0053](0053-the-merge-is-code-and-the-watchdog-reads-a-clock-the-reducer-never-sees.md)
made the merge Luna's job, and it answered a real question. RFC-0002 had given every role
its own worktree finishing in a commit, so **something** had to bring the pieces together —
and swarm-forge showed what happens when that something is prose: `merge_and_process` was a
phrase in a prompt, defined nowhere, and their
[issue #29](https://github.com/unclebob/swarm-forge/issues/29) records an agent passing it to
Bash and stopping on `command not found`. A fork reached `git merge -X theirs` and discarded
its own work in silence.

"Then it becomes code, with a dry run and no automatic conflict resolution" was the right
answer to *that* question. What went unexamined is the assumption underneath it: that
integrating is Luna's job at all.

The flow's last stage merges the task's work into the repository's own branch:

```
git merge --no-ff -m "luna: <task> (commit)" <base>
```

Three facts about that step, all measured on the code as it stands:

- **Nothing consumes `commit_sha`.** It is produced by stage 14 and read nowhere. Its only
  effect is to trigger the merge.
- **No invariant mentions merging.** The twelve rules in `docs/invariants/` cover flow
  control, the log, the handoff and containment. Integration was never elevated to a rule
  that always holds — it is an ADR-level decision, revisable by this one.
- **The work is already complete on a branch before the merge runs.** On a real run,
  `luna/tally-tq8-reviewer` pointed at the full thirteen-stage chain; the merge copied that
  onto `main` and added a commit.

And one that is not about the code. Luna cannot see what a merge sets off — whether the
repository has a remote, a CI pipeline, a deploy hook, or another person about to pull. A
step whose consequences are invisible to the system taking it is a step the system should
not take.

## Decision

**Luna does not integrate. A task ends on its own branch, and moving that work anywhere
else is a manual act outside Luna.**

Concretely:

- the `commit` stage and its `confirm-write` gate leave the flow; `node.Merger` leaves the
  production path;
- when the last stage closes, `luna/<task>` is pointed at that stage's commit — the branch
  already exists (created by `setup`, and until now left stranded at the discovery commit),
  so this gives it a purpose rather than adding a name;
- `done` means **ready to integrate**, and `luna status` says where: the branch and the
  commit.

The write Luna keeps is `git branch -f` on a ref it owns. Every ref under `luna/` is Luna's;
nothing outside that namespace is touched.

## Alternatives considered

- **Keep the merge and exclude `confirm-write` from any autonomy** — the shape this
  discussion started from. Rejected because it defends the gate rather than the step: it
  leaves Luna performing an integration whose consequences it cannot see, and merely insists
  a person be asked first.

- **End on the last role's branch, with no `luna/<task>`** — the simplest thing that works,
  and rejected for a small reason with a real cost: the last role varies by kind
  (`reviewer` on a chore, `hardener` on a feature), so whoever integrates has to consult the
  status to learn the branch name. A stable name per task is worth one `git branch -f`.

- **Make the merge configurable, off by default** — rejected because it keeps the code, the
  dry run, the conflict detection and the ownership check on a path most projects would
  never take, and every one of those is a thing that can be wrong.

- **Push the branch** — rejected for the same reason as the merge, more so: a push leaves
  the machine.

## Consequences

- **Positive:** the boundary is easier to defend. "Luna only writes refs under `luna/`" is
  checkable; "Luna is the sole owner of git" required Luna to be right about a merge.

- **Positive:** `confirm-write` disappears, and with it the hardest case in
  [PRD gate-0001](../PRDs/gate/gate-0001-an-autonomy-knob-over-the-flow.md) — a gate with
  nothing mechanical left to check, whose real question was "do I want this in my repository
  now?". That question belongs to a person because it is outside Luna, not because a knob
  forbids it.

- **Positive:** the failure ADR-0053 was written against cannot happen, because the step
  does not exist. No phrase, no code, no conflict policy.

- **Negative:** the watchdog loses its only real object. `merge_blocked` was the first fact
  it could genuinely observe (ADR-0053), and it goes with the merge. What is left for the
  watchdog is the stuck-task listing, which is weaker.

- **Negative:** `done` changes meaning, and a person who does not read the new status could
  believe a task is delivered when it is staged. This is why the status change is part of
  the decision rather than a follow-up.

- **Negative:** a repository accumulates one `luna/<task>` branch per task. They are cheap
  and namespaced, and nothing here cleans them up.

- **Impacts:** `src/stock/stages/140-commit.toml` (removed), `internal/herdr/node.go`
  (`integrate`, `mergedArtifact`), `internal/node/merge.go` (off the production path),
  `internal/cli/order.go` (`StatusReport`), and the stage table in
  `docs/architecture/stages.md` — 14 stages become 13.

## References

- **Supersedes the merge half of
  [ADR-0053](0053-the-merge-is-code-and-the-watchdog-reads-a-clock-the-reducer-never-sees.md)
  and nothing else.** That ADR decided two things at once — the merge and the watchdog — and
  only the first is revised here; the watchdog reading a clock the reducer never sees still
  holds. Its status line stays `Accepted` because the enum has no half-measure and the
  alternative would overstate what changed: this reference is the record of the scoping.
- Related documents:
  [ADR-0055](0055-one-worktree-per-task-and-role-branched-from-the-last-delivery.md) (the
  branches this ends on), [INV-core-6](../invariants/core.md) (the commit as handoff),
  [PRD gate-0001](../PRDs/gate/gate-0001-an-autonomy-knob-over-the-flow.md) (the gate this
  removes from the argument)
- Prior art: [references](../references.md) — swarm-forge, whose undefined
  `merge_and_process` is why ADR-0053 made it code in the first place.
