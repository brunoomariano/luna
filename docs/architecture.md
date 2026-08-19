# Architecture

How Luna works today. If this file and the code disagree, the file is the bug.

## The problem

A workflow written in prose is a suggestion to the model, not a guarantee. It is followed
almost every time — and the "almost" is what costs. All four of these were observed in
real runs, not imagined:

- an agent told to **run** a command decided that running meant **printing** it;
- an agent repeated the same thing 100 times and on the 101st did something else;
- under long sessions, roles eroded — the reviewer started implementing;
- a chain broke silently, and nobody noticed until two stages later.

Luna's answer is to take the flow-control decision out of the model and put it in code,
and then to **verify what came back by running a tool** rather than by reading a claim.

## The shape

```text
luna task new "add --avg to the tally"
   │
   ▼
one task, one worktree, one lead — parallelism is BETWEEN tasks, never inside one
   │
   ▼
for each stage in the flow:
   1. requires satisfied?          ── no → the agent is never called
   2. worktree ready, branched from the last delivery
   3. agent runs as a subprocess INSIDE the sandbox, briefed by Luna:
      prompt on stdin, answer and usage back on stdout
   4. agent works; code goes in a commit, scratch artifacts go to
      the store through a socket (`luna artifact put`)
   5. exit check RUNS the real tool — `make ci` returns 0, the blob
      has its hash, the commit resolves
   6. handoff appended to the log, with what the stage cost; transition
   7. gate declared? the task suspends and frees the slot
   │
   ▼
failure → bounded retry → notified block.  Never an infinite loop, never a silent death.
```

Everything above is replayable from the log, and the log refuses to replay against a flow
it was not written under.

## The engine is a reducer

`Reduce(state, action) → state`. Pure: no clock, no filesystem, no process. Verification
runs outside it and the verdict arrives **inside the action**.

That is what keeps a transition reproducible from the log and testable without
infrastructure. Running things belongs to `internal/node`; deciding belongs to
`internal/fsm`.

## The stage contract

A stage is a TOML file in `src/stock/stages/`. This is the whole shape:

```toml
id                 = "verify"
role               = "verifier"
requires           = ["code", "scenarios"]
produces           = ["ci_green"]
produces_for_human = ["dod_checked"]

[verify.ci_green]
run   = "make ci"      # the command that proves it
scope = "full"

[verify.dod_checked]
kind     = "existence" # a checklist a person reads; claiming more would be a lie
handover = "store"     # handed to Luna, not committed
```

Three checks come out of it, and the third is the one that matters most:

1. **Static**, before anything runs — walking the stages in order, is some `requires`
   never produced by an earlier stage? A broken flow is detectable on paper.
2. **On entry** — the agent is not called blind.
3. **On exit** — the stage does not close without its `produces`, and delivery is proven
   by running the tool. This catches the gap where it is born.

`scope` is the honesty knob: `full` (the whole gate ran), `targeted` (only what was
touched), `existence` (the file is there and nothing more is claimed), `human`. There is
no upgrade path — a stage cannot launder `existence` into `full`.

`handover = "store"` means the artifact is handed to Luna instead of committed. It changes
what delivering *means*, so it is part of the flow fingerprint; `path` is not.

## How an agent is run

One subprocess per stage, through the harness's non-interactive mode
(`claude -p --output-format json`). The prompt goes in on stdin, and what comes back is
the answer, the session id, and what the call cost — reported by the harness rather than
counted by Luna.

Luna used to drive the harness's terminal instead, and the reason it stopped is in
[lessons.md](lessons.md): everything that path required — pty sizing, a folder-trust
dialog, an input-ready marker, hand-tuned settles — was engineering against the wrong
interface, and none of it was about the model.

Three things follow from the transport:

- **Gating is real.** A denied tool is removed from the request, so it is absent from the
  agent's tool list rather than discouraged in its brief.
- **Cost is recorded.** Tokens, cache and price land in the log per stage, so what
  orchestration costs is a measurement instead of an argument.
- **A stage can continue.** `context = "live"` resumes the previous session for the same
  role, which is roughly an order of magnitude cheaper than starting cold. It is refused
  across a change of role: a reviewer inheriting the implementer's session would read its
  own reasoning instead of the delivery, and `luna flow check` says so before anything
  runs.

  **No shipped stage uses it yet, and that is a finding rather than an omission.** The
  twelve stages hand off between twelve different roles, so every one of them starts cold
  by construction — the setting has nowhere to apply until a flow puts two stages of the
  same role back to back. Whether the flow *should* do that (a `build`→`refactor` pair
  under one implementer, say) is exactly what the cost column now makes answerable.

## Roles

A role is a TOML file in `src/stock/roles/` — 12 of them ship:

```toml
agent      = "claude"
brief      = "You review. You report findings; you do not edit."
tools_deny = ["Edit", "Write"]
```

The stage names its role, not the reverse — the flow is the single place that decides who
runs what. Separation is by negation: whoever writes does not review.

**Where the floor is.** Tool gating removes the *named* tools; it does not remove the
shell. On harnesses whose denial is per-tool, a reviewer denied `Edit` can still write
through `Bash`. This is stated rather than hidden: the separation is structural and real,
the containment behind it belongs to the sandbox (INV-4). A reviewer that writes corrupts
a *review*, which the next stage reads and a person can reject — not a *record*, which
nothing downstream could catch.

## The sandbox and the socket

Every agent starts inside `ai-jail`. Under Landlock, the only writable position an agent
can reach is its own worktree — `$HOME`, `/tmp` and symlinks out all fail, measured.

So the socket lives there: `.luna/artifact.sock` inside the stage's worktree. The agent
calls `luna artifact put <name>`, the CLI dials the socket, and **Luna** writes the blob.
The producing stage is stamped by the server, never read from the request.

```text
agent (in jail)  ──`luna artifact put contract`──▶  socket in the worktree
                                                          │
                                            Luna is the only writer
                                                          ▼
                                    store: (task, stage, artifact, seq) → hash + body
```

Blobs are keyed **per stage**, because `build` and `refactor` both produce `code`, loops
revisit stages, and a gate replaces what it reviewed. Ceiling is 1 MiB; the refusal names
the size and the limit. `luna task forget <id>` empties a finished task's blobs — the log
and the hashes stay, only the content goes.

This is what keeps the delivered tree clean: in the last full cycle it held exactly the
three files the feature needed, and seven scratch artifacts lived in the store.

## Gates

A gate stops the task and waits for a human. It does **not** hold a live process: the task
suspends, the slot is freed, another task uses the resource, and `luna gate approve`
resumes from the exact point.

A gate waits only because its stage declared something to answer it with — judgement
criteria, or checks the task declared. A gate with neither was never going to put a
question in front of anybody, so it does not wait.

Three shapes: **confirmation** (yes/no), **artifact for review** (`luna gate show` prints
the blob; the human can approve, adjust or reject, and the adjusted version is what enters
the context), and **flow decision** (a loop ceiling blew — continue, abort, change course).

Who answers is the autonomy knob, `0`–`10` per task. `0` sends every gate to a person;
higher lets the lead judge gates at or below that criticality; declared checks are answered
by their exit code before any model is involved. Profiles ship for the common settings:
`interactive`, `turbo`, `nightly`.

Changing the knob changes what happens from here on and leaves the past alone — every
advance records what its gate actually decided, so a replay reads a fact instead of
recomputing one.

## State

Append-only SQLite. No `UPDATE`, no `DELETE`. The log is the state; anything else is a
projection that can be rebuilt.

The opening event carries a **fingerprint of the flow** the task was born under — stage
ids in order, with what each requires and produces. Replaying against a different flow is
refused, because a renamed stage used to replay as the new name and an inserted stage made
a task redo finished work, both silently.

The flow itself stays editable; what is recorded is its *identity*, not its content.
`luna flow check` reports whether anything is open before you change it.

The core knows no issue tracker. A task enters through `luna task new` or an import
adapter, which is a command outside the core.

## Layout

```text
src/
  cmd/luna/          CLI entry point
  internal/fsm/      the engine: stages, transitions, contract, fingerprint
  internal/store/    append-only log, replay, blob store
  internal/node/     running a stage: sandbox, socket, verification
  internal/cli/      commands
  internal/lead/     the model that judges a gate when the knob allows
  stock/             defaults: stages, roles, profiles, skills (embedded TOML)
docs/                this suite
scripts/             lint helpers
```

`internal/` is an import barrier the Go compiler enforces: the engine is not importable
from outside the module.

## What is decided but not built

Stated here rather than implied by silence, and verified against the code rather than
remembered:

- **Conditional `requires`** — a stage can be conditional (`when = "is-bug"`), but its
  `requires` cannot. `build` should require `contract` only when `spec` ran; the static
  check does not understand that, so `contract` stays out and the gap is recorded in
  `070-build.toml`.
- **Import adapters** — no tracker adapter ships.
- **Token accounting** — being added now that the transport reports usage.
- **Skills** — `src/stock/skills/` is empty. A role declares an agent and a brief; the
  skill set is parsed and read by nothing.

Three things read as gaps and are not:

- **The watchdog is a query, not a loop.** `luna stuck` plus `Store.Stalled` finds tasks
  stopped longer than a patience window; each agent call is bounded by a budget that
  kills the process group, and a stall arrives
  as an error. There is deliberately no in-process ticker — a watchdog that needs a
  process running cannot catch the stall where everything has stopped.
- **The profile files are empty on purpose.** A profile carries only its name; every
  setting that used to live inside one was retired, and the parser actively refuses them.
- **Notification exists** — it shells out to an external notifier on every block path.
  What is left open is which *additional* channels (webhook, Telegram) to support, and
  that stays open until real use answers it.
