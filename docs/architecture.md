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

## Flows

Luna ships three, and a task picks one when it is created:

| Flow | Stages | For |
|---|---|---|
| `full` | setup → intake → plan → build → refactor → pipeline → verify → review | new behaviour, uncertain design; the only one with a gate |
| `fix` | setup → diagnose → build → verify | a bug with a reproduction: the reproduction is the specification |
| `chore` | setup → build → verify | mechanical work — a bump, a rename, a formatting pass |

`luna task new FIX-1 --kind bug --flow fix`. The choice is fixed at creation and
recorded in the opening event, by **name** so a later replay can load it and by
**fingerprint** so it can tell it is being read against a different one. Both,
because either alone lies in a different direction: a name whose flow was edited
since resolves to something the task never ran, and a fingerprint with no name has
nothing to compare against.

A flow directory is self-contained rather than a selection over a shared pool of
stages, and that is not duplication for its own sake — **a lighter flow rewires
what the surviving stages require.** `build` asks for `contract` under `full` and
for `root_cause` under `fix`; same stage id, different contract, so two files.

Two consequences worth stating, because both were surprises:

- **The command runs before the model, everywhere.** `ci_green` is proven by
  running `make ci`, and nothing about it is a judgement — `judgement` in
  `role.go` does not list it — so the stage that owes it names no role and starts
  no agent. Under `full` that is the `pipeline` stage, and `verify` *requires*
  what it produces: the entry check refuses the critic until the pipeline has
  passed, so no model is ever paid to read code the compiler has not accepted.
  The lean flows have the same stage without the critic after it.
- **A lean flow is a different fingerprint, and that is the point.** Dropping
  `plan` changes stage ids, order, `requires` and `produces` — all history. So a
  task that ran without planning is not replayable as though it had planned, which
  is the honest answer rather than an obstacle.

`luna flow check` audits every flow, not one: a build that reports "the contract
holds" about a third of what it runs is saying something true and useless.

## Driving it by hand

The same rails, with a person where the agent goes:

```sh
luna next  <id>   # what to do, how it will be checked, and the brief itself
luna start <id>   # open it — `next` is a read and changes nothing
luna done  <id> --delivered <a,b> --commit <sha>
```

`next` prints how each owed artifact is proven, beside what is owed, so nobody has
to go and read the stage file to find out what they are held to. It carries the
same brief the agent would get — contract duty, gate criteria, what is handed over
rather than committed — rather than the role's own sentence about itself.

`start` exists because nothing else opened a stage without also starting an agent,
so the pair the help text called the hand-driven path failed on its first use. It
is a separate verb rather than folding into `done`, because `done` refusing a task
with no running stage is what makes a mistyped id a clean error instead of two
events in a log nobody meant to touch.

**What a hand-driven stage cannot do is launder a verdict.** `done` records
`existence` for everything it is told, always and deliberately, so a stage whose
contract declares a command does not close on it — `luna work` is what closes
those, carrying what its verifiers actually observed.

## The stage contract

A stage is a TOML file in `src/stock/flows/<flow>/`. This is the whole shape:

```toml
id                 = "verify"
role               = "critic"
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

  **The shipped flow uses it, and that is what collapsing the roles bought.** While the
  flow had twelve roles for twelve stages, every stage started cold by construction and
  the setting had nowhere to apply. With three roles, `build` and `refactor` continue the
  session `plan` opened, and `review` continues `verify`'s — two cold starts in a run
  instead of twelve.

  One of those cold starts is not an economy to be recovered. `plan` is `fresh` because
  on a bug task `diagnose` runs immediately before it, and continuing "the previous
  session" would mean continuing the investigator's. The static check refused it, which is
  what the static check is for.

  The session id is recorded in the log, with the stage's spend. It was a map on the
  runner until a real run showed what that costs: answering a gate ends the process, the
  shipped flow gates in the middle of the maker's run, and `build` started cold every
  time — one resumed pair out of three. A session the harness no longer has is retried
  fresh rather than failing the stage, and the retry reports `fresh`, so the cost column
  never claims a resumption that did not happen.

## Roles

A role is a TOML file in `src/stock/roles/` — three of them ship:

```toml
agent      = "claude"
brief      = "You judge what was delivered. You report findings; you do not edit."
tools_deny = ["Edit", "Write"]
```

| Role | Stages | Denies |
|---|---|---|
| `maker` | intake, plan, build, refactor | — |
| `critic` | verify, review | `Edit`, `Write` |
| `investigator` | diagnose | — |
| — | setup, pipeline | mechanical: no agent runs |

The stage names its role, not the reverse — the flow is the single place that decides who
runs what. Separation is by negation: whoever writes does not review.

**Why three and not twelve.** There were twelve, one per stage, differing only in the
sentence describing the work — and the stage's brief already says that, generated by Luna
with the contract in it. What is left to a role is what the stage cannot express: which
tools it denies, and whether it may inherit the previous session. Only two boundaries
survive that test — writing versus judging, and diagnosing before there is anything to
build on. The other nine were nine cold starts buying nine adjectives.

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

Four shapes: **confirmation** (yes/no), **artifact for review** (`luna gate show` prints
the blob; the human can approve, adjust or reject, and the adjusted version is what enters
the context), **flow decision** (a loop ceiling blew — continue, abort, change course), and
**guard**.

A guard is the one gate that opens on what the delivery *contains* rather than on where the
task stands. A stage declares the paths it will not land unattended:

```toml
[guard]
paths  = ["migrations/", "secret", ".env", "deploy"]
reason = "the delivery touches migrations, credentials or deployment"
```

It carries no judgement criteria on purpose, so **no autonomy setting gets past it**.
Those changes are not likelier to be wrong than any other — they are the ones a person
cannot undo by reading the next morning's report, and whether dropping a table was intended
is not a thing Luna can weigh.

The patterns are matched by the node against the *diff*, not the tree — a repository that
has always held `migrations/` would otherwise stop every task forever — and the match
arrives inside the action like every other verdict. So the patterns are policy and stay out
of the fingerprint: editing the list changes what stops tomorrow and cannot rewrite what
stopped last week. A diff that cannot be read counts as every pattern matched, which is the
one place in the node where the cautious answer is the noisy one.

Who answers is the autonomy knob, `0`–`10` per task. `0` sends every gate to a person;
higher lets the lead judge gates at or below that criticality; declared checks are answered
by their exit code before any model is involved. Profiles ship for the common settings:
`interactive`, `turbo`, `nightly`.

Changing the knob changes what happens from here on and leaves the past alone — every
advance records what its gate actually decided, so a replay reads a fact instead of
recomputing one.

## The fleet

`luna run` drives one task. `luna fleet run` drives every eligible one, several at a time:

```sh
luna fleet run --flow fix --budget-usd 20 --concurrency 4
luna fleet report --since 12h
```

**Eligible** means not finished, not called off, not waiting on a person and not blocked. A
blocked task is deliberately excluded — it stopped for a reason somebody has to deal with,
and a fleet that retried it every night would turn a notified block into a nightly bill.
`luna unblock` is how it becomes eligible again.

The fleet's ceiling stops it **starting** rather than stops it running, and the slot is
taken before the ceiling is weighed. That ordering is the correctness of the loop: checking
first would decide while the previous task was still going, against a total that did not yet
include it, so a fleet of one would always start one task too many.

Parallelism between tasks is not a fleet decision — it is the property everything else
rests on. One worktree and one lead per task is what makes two tasks unable to see each
other's work, and the store has been proven safe for concurrent appends across tasks since
before there was a fleet to need it.

`luna fleet report` is the morning's product rather than a side effect of it: every task
grouped by what has to happen to it next, with both verdicts and the bill. A task that no
longer replays is *reported* rather than skipped, because it is exactly the one that would
otherwise sit unnoticed forever.

## State

Append-only SQLite. No `UPDATE`, no `DELETE`. The log is the state; anything else is a
projection that can be rebuilt.

The opening event carries the **name of the flow** the task was born under and a
**fingerprint of its identity** — stage ids in order, with what each requires and produces.
The name is what a replay resolves; the fingerprint is what it verifies. Replaying against a
different flow is refused, because a renamed stage used to replay as the new name and an
inserted stage made a task redo finished work, both silently.

Every reader goes through `ReplayOwnFlow`, which resolves the task's flow before replaying
it. `Replay(id, flow)` is the narrower door beneath it, and it is right for exactly one
caller — `luna flow check`, which is asking what a *different* flow would do to this log.

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
  internal/agent/    calling an agent: one subprocess, prompt in, usage out
  internal/node/     running a stage: worktree, sandbox, socket, verification
  internal/cli/      commands
  internal/lead/     conducting a task, and the model that judges a gate
  stock/             defaults: flows, roles, profiles (embedded TOML)
docs/                this suite
scripts/             lint helpers
```

`internal/` is an import barrier the Go compiler enforces: the engine is not importable
from outside the module.

## What is decided but not built

Stated here rather than implied by silence, and verified against the code rather than
remembered:

- **Conditional `requires`** — a stage can be conditional (`when = "is-bug"`), but its
  `requires` cannot. Nothing needs it today: the case that wanted it was `build` requiring
  `contract` only when `spec` ran, and merging `spec` into the unconditional `plan` closed
  it. A flow that adds a conditional producer will want the mechanism back.
- **Import adapters** — no tracker adapter ships.
- **Token accounting** — being added now that the transport reports usage.
- **Skills** — `src/stock/skills/` is empty. A role declares an agent and a brief; the
  skill set is parsed and read by nothing.
- **A second harness.** All three roles name `claude`. The transport supports four and
  `CanGate` knows which of them can deny a tool, but no shipped role exercises another,
  so "harness-agnostic" is built and unmeasured.

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
