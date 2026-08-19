# RFC-0008: a scratch artifact is handed over through a socket

**Status:** DONE
**Last reviewed:** 2026-08-19
**Source issue:** —
**PRD:** —

## Motivation

The handoff is the commit ([ADR-0055](../ADRs/0055-one-worktree-per-task-and-role-branched-from-the-last-delivery.md),
[INV-core-6](../invariants/core.md)), and for what the repository is *for* — code, tests,
project documentation — that is right and is not in question here.

It is wrong for the rest. `contract`, `scenarios`, `approach` and the five audit reports are
scaffolding: they exist so the next stage, or a person, can decide something. Committing them
puts working notes into the delivered history of somebody else's repository, scattered across
whatever directory each agent picked.

Two invariants already point at the gap from opposite sides, and neither is closed:

- **[INV-core-11](../invariants/core.md)** wants the handoff to carry *the location of each
  audit artifact*. Today the location exists only inside `provePath`, is spent on one
  `git ls-tree`, and survives as prose in `Evidence.Detail`.
- **[INV-core-12](../invariants/core.md)** wants a human-facing artifact to be locatable by
  command. Today `produces_for_human` never enters `Context.Artifacts` — deliberately, so an
  audit report cannot satisfy some stage's `requires` ([ADR-0021](../ADRs/0021-produces-for-human-is-a-separate-contract-field.md))
  — and `luna task show` builds its "produced" list from exactly that map. So `qa_report` is
  verified, has evidence in the log, and appears nowhere.

[ADR-0070](../ADRs/0070-an-artifact-can-declare-where-it-lives.md) made the *delivery*
checkable by declaring a directory. This RFC is about what happens to the artifact **after**
it is proven, and about the ones that should never have been in the repository at all.

## Technical proposal

### Overview (guide-level)

**An artifact that is not part of the delivery is written to Luna's store through a socket,
and the commit stops carrying it.**

The agent's interface stays `luna` — it does not learn a protocol and does not leave its
worktree:

```
luna artifact put contract < contract.md
luna artifact get scenarios
```

What changes is who writes. `luna artifact put` does not open the database; it connects to a
Unix socket that Luna opened **inside the agent's own worktree**, and Luna — running outside
the sandbox — validates, hashes and appends.

```
   inside the jail                    the boundary                outside
 ┌───────────────────────┐                 │            ┌────────────────────┐
 │ agent, cwd = worktree │                 │            │ luna run           │
 │                       │                 │            │                    │
 │ luna artifact put ────┼── .luna/put.sock ───────────► │ validate, hash     │
 │                       │                 │            │ append to store    │
 │ luna artifact get ◄───┼─────────────────┼────────────┤ read from store    │
 └───────────────────────┘                 │            └────────────────────┘
        only cwd is reachable         AF_UNIX connect        .luna/luna.db
```

This is the shape Luna already uses for herdr ([ADR-0027](../ADRs/0027-luna-runs-under-herdr-as-a-socket-client.md)):
a socket is the one surface that crosses containment.

### Why not have the CLI write the database directly

Because it was measured, against a real `ai-jail` 1.17.0, and it fails in the worst available
way. From inside the jail, with cwd on the worktree:

```
command -v luna                → /home/…/.local/bin/luna            ok
git rev-parse --git-common-dir → …/m3/repo/.git                     ok
luna task new probe-a5         → created probe-a5 (…)   exit=0      ok
```

Three greens, and the task does not exist:

```
outside:            luna task show probe-a5   → no task "probe-a5"
the real store:     select * from events      → []
find luna.db*       → no second database anywhere
```

The cause is the mount. The jail gives the process a **tmpfs root** (`tmpfs on / type tmpfs`),
so `luna` created the log fresh in RAM, wrote to it, and read it back — coherently, with
itself. Creating and reading in the *same* jail session shows `events 1`; leaving the session
shows nothing, because there is nothing.

`LUNA_STORE` pointed at the real path changes nothing: the path does not exist in there
either. `ls` and `echo >` on the absolute path both answer `No such file or directory`, while
`luna` "succeeds" — because `luna` creates what it cannot find.

This is [ADR-0068](../ADRs/0068-a-worktree-lives-where-the-process-can-reach-it.md)'s failure
shape — two realities, one answer — with the exit code reporting 0, which is strictly worse:
there is no ambiguous return value left for a caller to distrust.

### Why the socket has to live inside the worktree

Also measured. Four positions, one works:

```
socket in ~/.config/…             → FileNotFoundError
socket in /tmp/lb/sock/           → FileNotFoundError
symlink inside cwd → outside      → FileNotFoundError   (the target stays invisible)
real socket inside the cwd        → connected
```

And the crossing proven end to end — a server running outside the jail, appending to a file
outside the jail, with the client contained:

```
inside:   luna-like client → "PERSISTED 51"
outside:  {"artifact":"contract","body":"o contrato inteiro"}
```

Landlock permits `connect()` on a socket whose inode is under the reachable directory. The
process on the other end is not contained and writes wherever it likes.

### Detail (reference-level)

- **`src/internal/store/`** — a content store returns, as `blobs(task_id, artifact, seq, hash,
  body)`. Append-only like the log ([INV-core-2](../invariants/core.md)): a revised artifact is
  a new row, and both versions stay readable. Deleting a task deletes its rows, which is the
  cleanup routine the current design has nowhere to put.
- **`src/internal/node/`** — the socket server. It belongs here for the reason the package
  exists: it touches the world, and the reducer must not.
- **`src/internal/cli/`** — `luna artifact put|get|show`, as a socket client. `show` is what
  closes INV-core-12.
- **`src/internal/herdr/node.go`** — opens the socket before the agent starts, closes it with
  the stage, and names it in the brief.
- **`src/internal/fsm/`** — `Evidence` gains the hash. The reducer still runs nothing: the
  hash arrives inside the action, exactly as a verdict does ([ADR-0024](../ADRs/0024-the-reducer-is-pure-verification-runs-outside.md)).

Contract shape, per artifact in the stage TOML:

```toml
[verify.contract]
kind  = "existence"
scope = "task"        # handed over through the store, never committed
```

`path` ([ADR-0070](../ADRs/0070-an-artifact-can-declare-where-it-lives.md)) and this are
mutually exclusive, for the reason a path and a command already are: an artifact proven in two
places has no answer to "which one decided?".

**Limit: 1 MiB per blob.** A dense `qa_report` is around 30 KB, so this is roughly thirty times
the real case and still refuses a build log pasted in by accident. The refusal names the size
and the ceiling, per this project's error rule.

## Alternatives considered

- **The CLI writes the database directly** — the obvious route, and the one measured above. It
  reports success and loses the write. Rejected on evidence, not on taste.

- **A scratchpad on disk, in `.luna/` of the worktree** — the first design. It survives the
  jail, but the artifact leaves the verifiable handoff: nothing but the agent's word says it
  was written, which is the self-report [ADR-0028](../ADRs/0028-herdr-status-triggers-verification-it-never-closes-a-stage.md)
  refuses and ADR-0070 had just closed. It also dies with the worktree ([ADR-0055](../ADRs/0055-one-worktree-per-task-and-role-branched-from-the-last-delivery.md)),
  making the copy mandatory and on the critical path. And measured: `/.luna/` is in *this*
  repository's `.gitignore` and in no target's, so the scratchpad shows as untracked in
  somebody else's repository — invisible on the machine this was tested on only because
  `~/.config/git/ignore` hides it there.

- **Keep committing everything** — today's behaviour, and it is what motivates this. Working
  notes in the delivered history, scattered by whatever directory each agent chose.

- **Give the agent the database over a bind mount** — `ai-jail` can expose a path. Rejected:
  it hands an agent write access to the log, which *is* the audit ([INV-core-2](../invariants/core.md)).
  An agent able to write the log can rewrite its own history. The socket keeps Luna the only
  writer, so containment stops depending on good behaviour.

## Drawbacks

- **It reopens a decision that is five days old.** [ADR-0058](../ADRs/0058-what-had-no-caller-is-either-wired-or-gone.md)
  deleted the content store because [RFC-0002](rfc-0002-the-lead-is-an-agent-and-the-commit-is-the-handoff.md)
  made the commit the handoff and "git stores content better than a table of blobs". That
  argument stands for the artifacts it was about — the ones that belong in the repository. The
  new argument is a case it did not consider: at the time everything went to git anyway, and
  the question of what should *not* be committed had not been asked.
- **A second handoff channel.** Two mechanisms where there was one, and the boundary between
  them has to stay legible or every new artifact becomes a debate.
- **A live socket per stage** — one more thing to open, close and leak.
- **The store grows.** The log was small because it held facts; blobs are content.
- **Correction to ADR-0058's record:** it states that the `blobs` table and the `events.blob`
  column would stay so old logs would still decode. They are not in the schema — only `events`,
  with no `blob` column. So this is a rebuild, not a rewiring.

## Impact and migration

- **Data/persistence:** a new `blobs` table. Additive; no existing log changes meaning, and no
  backfill — tasks that ran before this simply have no rows.
- **Compatibility:** `scope = "task"` is a new field. Absent, an artifact behaves exactly as
  today, so no shipped contract changes on upgrade. It **does** enter the flow fingerprint
  ([ADR-0046](../ADRs/0046-the-log-records-which-flow-it-was-written-under.md)) — unlike
  `path`, this changes *what the stage owes*, not merely what is checked.
- **Observability:** every put is an appended event, so the log gains the artifact's hash and
  size. `luna task show` starts listing human-facing artifacts, which is INV-core-12's gap.
- **Rollback surface:** stop declaring `scope = "task"`. The socket goes unused and the
  contracts behave as before; the table stays and costs nothing.

## Rollout plan (phased)

1. **Phase 1 — the store.** `blobs`, append-only, with the size limit and its refusal. Pure
   storage, no socket, no CLI. Leaves the system valid: nothing calls it yet.
2. **Phase 2 — the socket and the CLI.** Server in `node`, `luna artifact put|get`, and the
   test that matters: a client under `ai-jail` writes and the blob is in the store outside.
3. **Phase 3 — the contract.** `scope = "task"` parsed, fingerprinted, refused alongside
   `path`. Still nothing shipped declares it.
4. **Phase 4 — the flow.** Move `contract`, `scenarios`, `approach` and the five reports over,
   name the socket in the brief, and make `luna task show` and `luna gate show` read blobs.

Feature flag: no. Phases 1–3 are inert until a contract declares the field, which is the flag.

## Open questions

- [x] **Per task or per stage? — per stage.** The key collides in practice: `build` and
      `refactor` both produce `code` and `tests_green`, a loop can revisit a stage up to
      `MaxRounds` times, a retry re-enters it, and a `review-artifact` gate replaces the
      payload with the human's version. Keyed by task alone, those all become one line of
      history separated only by `seq`, and "what did `build` hand over" needs a join against
      the log. Keyed by stage, authorship is free — and it is what
      [INV-core-11](../invariants/core.md) asks for, since `produces_for_human` is declared by
      the stage, not the task. The simplicity of the task key is recovered rather than lost:
      `luna artifact get contract` with no stage named returns the most recent from any stage.
- [ ] **Concurrency under load.** The socket makes Luna the only writer, which is SQLite's best
      case, and WAL plus `busy_timeout(5000)` are already set with a single connection
      (`openOwned`). Still to be measured with N agents putting at once — the one question
      this RFC leaves open past DONE.
- [x] **Does an artifact in the store need to be reachable after the task is deleted? — no.**
      `luna task forget` removes a task's blobs; the log keeps every hash, so the history of
      what was produced outlives the content, and nothing else needs to.
- [x] **Does the lead read blobs directly, or always through the CLI? — always the CLI.** It
      runs uncontained and could open the store, which is exactly why the rule is worth
      stating: two paths to the same data is how they drift.

## References
- Issue: —
- PRD: —
- Decision record: [ADR-0071](../ADRs/0071-a-scratch-artifact-is-handed-over-through-a-socket.md)
- Related ADRs: [ADR-0058](../ADRs/0058-what-had-no-caller-is-either-wired-or-gone.md) (deleted
  the content store — the decision this revisits), [ADR-0070](../ADRs/0070-an-artifact-can-declare-where-it-lives.md),
  [ADR-0068](../ADRs/0068-a-worktree-lives-where-the-process-can-reach-it.md),
  [ADR-0027](../ADRs/0027-luna-runs-under-herdr-as-a-socket-client.md),
  [ADR-0021](../ADRs/0021-produces-for-human-is-a-separate-contract-field.md)
- Invariants: [INV-core-11](../invariants/core.md), [INV-core-12](../invariants/core.md),
  [INV-core-2](../invariants/core.md)
- PRs: —
