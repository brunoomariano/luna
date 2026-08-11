# ADR-0037: Worktrees are siblings, named by the house convention

**Status:** Accepted
**Date:** 2026-08-11

## Context

[ADR-0027](0027-luna-runs-under-herdr-as-a-socket-client.md) settled that herdr creates
the worktree for a task, one per task. It did not say where that worktree lives, and the
first live runs answered by default: herdr puts checkouts under a directory of its own,
`~/Projects/herdr-worktrees/<repo>/<branch-slug>`.

That is a reasonable default for herdr and the wrong answer here, for a reason that has
nothing to do with herdr. Worktrees in this house follow one convention regardless of
what created them — `../wt-<repo>-<id>`, a sibling of the repository — so that `ls
../wt-*` finds every one and a person cleaning up does not have to know which tool made
which. Luna creating its worktrees somewhere else would mean two conventions in the same
repository, and the one nobody expects is the one that gets left behind.

The convention also has a reason of its own, independent of tidiness. A checkout nested
*inside* the repository is caught by every recursive walk the repository does to itself —
test discovery, linters, `find`, the build — and one inside a tool's directory
(`.claude/`, `.git/`) is caught by that tool's own cleanup. A sibling is outside all of
it.

`worktree.create` accepts a `--path`, and a live herdr honours it exactly.

## Decision

**Luna passes the checkout path. It is `../wt-<repo>-<task-id>`, a sibling of the
repository.**

```
/home/someone/repos/api          the repository
/home/someone/repos/wt-api-LUNA-1  the worktree for LUNA-1
```

The path is derived from the repository Luna was pointed at, so `--repo .` from inside a
checkout resolves to an absolute sibling rather than something relative that herdr would
interpret from its own working directory.

**Reopening is the exception: herdr's answer wins over the requested path.** A worktree
made before this convention existed lives where it lives, and the verification has to run
in the checkout that is actually there (ADR-0035) rather than where one would be created
today.

Two refusals rather than guesses, both because the alternative is a checkout nobody meant
to create:

- a repository path that names nothing (the filesystem root) is refused instead of
  producing `/wt--LUNA-1`;
- a path that cannot be resolved is reported before anything is asked of herdr, so a
  mistake in the command is not blamed on the integration.

## Alternatives considered

- **Accepting herdr's default** — rejected. It works, and it puts Luna's worktrees
  somewhere no other tool in this house looks. The cost is not aesthetic: the checkouts
  outlive the tasks, `worktree.remove` never deletes the branch (ADR-0027), and a
  directory nobody thinks to check is where they accumulate. Eleven were left behind by
  one afternoon of testing before this was fixed.
- **Making the path configurable, defaulting to the convention** — rejected for now as
  unearned. There is one convention and no second caller asking for a different one.
  Adding the setting later costs a field; carrying it now costs a decision nobody has
  needed to make.
- **Nesting the worktree inside the repository** (`./worktrees/<task>`) — rejected. It is
  the arrangement that seems tidiest and is caught by everything the repository does to
  itself, from test discovery to `make ci`.

## Consequences

- **Positive:** every worktree in the repository follows one convention, whoever made it.
  Cleanup is `ls ../wt-*` and does not require knowing that Luna was involved.
- **Negative / costs:** Luna now depends on `worktree.create` honouring `--path`, which is
  verified against a running herdr rather than promised by anything. If a future herdr
  ignores it, the worktrees quietly move back to its default and nothing fails — the
  symptom is checkouts appearing where nobody looks.
- **Impacts:**
  - the convention is `wt-<repo>-<task-id>`, so a task id that is not path-safe would
    produce an awkward directory name; task ids are short identifiers today, and this is
    the place to guard it if that changes;
  - branch cleanup remains Luna's and remains unbuilt: `worktree.remove` never deletes
    the branch, so a finished task leaves `luna/<id>` behind;
  - a worktree removed outside Luna leaves the task pointing at a checkout that is gone,
    which the node layer discovers on the next run rather than at the moment it happens.

## References

- Related documents: [ADR-0027](0027-luna-runs-under-herdr-as-a-socket-client.md),
  [ADR-0035](0035-luna-runs-the-verification-itself.md),
  [ADR-0036](0036-herdr-facts-learned-from-a-running-server.md)
