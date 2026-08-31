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
one task, one active stage worktree, one lead — parallelism is BETWEEN tasks, never inside one
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
| `full` | setup → intake → *diagnose* → plan → **forge** → shipping → review | new behaviour or a defect; the trail with the gates and the loop |
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
  `capability.go` does not list it — so the stage that owes it names no agent and
  starts none. Under the lean flows that is `verify`, with nothing after it.
  Under `full` the command runs inside `forge`, which is the price of merging the
  loop into one stage: the ordering is the agent's to keep rather than the
  engine's to enforce. What the engine still holds is the exit — `forge` declares
  what it converges on, and a round may not be judged converged until that command
  has passed.
- **A lean flow is a different fingerprint, and that is the point.** Dropping
  `plan` changes stage ids, order, `requires` and `produces` — all history. So a
  task that ran without planning is not replayable as though it had planned, which
  is the honest answer rather than an obstacle.

`luna flow check` audits every flow, not one: a build that reports "the contract
holds" about a third of what it runs is saying something true and useless.

## The daemon

One process owns the central database. Every ordinary CLI process first makes sure that
daemon is running, then opens the same file with SQLite `mode=ro`. Events, artifact bodies
and the one allowed content deletion are forwarded to the daemon over its private socket.
The read-only connection is physical, not only a Go-level ownership flag.

```
$XDG_RUNTIME_DIR/luna/
  daemon-<db-hash>.sock    the writer — never exposed to a sandbox
  handover/
    <task>-<stage>.sock     one stage handover socket, mapped read-only
```

**Why a process and not a constant.** The rule was `LunaOwnsTheLog`, checked at the store's
single append — enforcement against code that respects it. The log then moved out of the
checkout, so it lives under `$HOME`, and ai-jail gives a contained process a tmpfs `$HOME`.
A `luna` run inside a jail would create a fresh log there, answer every read from it, and
keep none of it: the measured failure where a command reported success and the task never
existed. With a daemon, that `luna` cannot reach the writer at all, and failing to connect
is loud where writing to a tmpfs is silent.

**The two socket locations are separate on purpose.** A sandbox sees only the `handover/`
directory and its stage socket,
because an agent has to deliver artifacts. The node stamps the task and stage, and its
read-only store forwards the blob to the daemon. The daemon socket is never mapped: an
agent that could reach it could fabricate history directly.

**It starts itself.** A command that finds nobody listening spawns `luna daemon` and tries
once more. A second failure is reported rather than retried. Nothing about this is visible
to the person running a command, which is the point — the writer became a second process
and `luna` stayed one command.

The socket name includes a hash of the database path, so a daemon serving a test database
cannot answer a command aimed at the real one. A non-blocking lock beside the database
also refuses a second daemon even if it tries a different socket.

`store.OpenReadOnly` refuses to create or migrate a file. Setting `Via` on that store turns
an attempted mutation into a daemon request, which is why commands still call the store's
append and blob methods without gaining a write connection.

## Where the log lives

One log for **all projects**, outside every checkout:

```
$XDG_DATA_HOME/luna/            (or ~/.local/share/luna)
  luna.db
  luna.db.lock
```

Outside the checkout because Luna writing into a repository is a change nobody asked
for — and because a log inside a checkout is a log an agent working in that checkout can
reach. Every event and artifact row carries a project key. The key comes from the
normalised `origin` remote, or from the main checkout path when no remote exists, and it
joins the task id in the primary key. Two projects may therefore both own `TASK-1`, while
two clones of the same remote see the same history.

That project boundary is also the CLI boundary. Commands that mutate or inspect one id —
`luna status`, `luna work`, `luna gate approve`, `luna task abandon` — use the current
checkout's project. Workload commands deliberately cross it: `luna task list`, `luna
gates`, `luna stuck`, `luna fleet report` and the task survey and spend history in `luna
flow check` read every project and print the project key with each task.

**Older stores are imported by the daemon, not by the CLI.** On startup it scans the old
`$XDG_DATA_HOME/luna/projects/<project>/luna.db` layout. A command also points it at a
legacy `<repo>/.luna/luna.db` for that repository. Sequence numbers, timestamps, payloads
and blobs are copied exactly; a row already present must match exactly. Only after the
copy verifies does the source become `luna.db.migrated`. An unplaceable pre-version blob
or a conflicting history stops startup rather than losing or guessing data.

An older per-project database cannot be opened directly as the central database: its rows
have no project key, and assigning an empty or guessed one would make the tasks unreachable
from their checkout. That opening is refused with an import instruction. Empty old tables
may be upgraded in place because there is no identity to invent.

Nothing stays in the checkout. The settings went into the same database, keyed by project,
and the `.gitignore` that used to hide Luna's scratch went with the scratch. What Luna still
writes to a repository is one line inside `.git` naming where the database is.

The handover socket left too. It is at `$XDG_RUNTIME_DIR/luna/<task>-<stage>.sock`, which
the sandbox is asked to expose read-only, and one consequence is worth stating: how deep a
worktree sits stopped being able to break the handover. AF_UNIX caps an address at 108
bytes, and the first real run died on `bind: invalid argument` because a worktree 140 bytes
down carried the socket with it.

## Knowing where you are

A checkout describes itself, and Luna reads it rather than being told. `node.Identify`
answers from git and the path alone — nothing is read from a file Luna wrote, because a
marker file would be a second thing to keep in step with the first.

```
luna where          # in a stage's worktree
  repo      /repos/app
  remote    git@github.com:me/app.git
  worktree  /repos/wt-app-LUNA-1-forge
  branch    luna/LUNA-1/forge
  task      LUNA-1
  stage     forge
```

The branch is the authority: `luna/<task>/<stage>` travels with the work, where a
directory can be moved or made by hand. The directory name is the **cross-check** —
`wt-<repo>-<task>-<stage>` — and a disagreement is reported, never resolved. Which of
the two moved is something only the person standing there knows, and a convenience that
guesses between two sources is worse than one that asks.

Every command that takes an id now takes it optionally: an argument wins when there is
one, so naming a task still means that task from anywhere. What it removes is having to
name a task while standing in its own worktree.

## The lead's own commands

The lead conducts by running these, and they are readable and runnable by a person
for the same reason: there is one interface, not one for models and one for people.

```sh
luna next <id>    # what to do, how it will be checked, and the brief itself
luna work <id>    # start the agent for that stage, and close it on what its checks saw
luna done <id> --delivered <a,b> --commit <sha>   # a stage carried out by hand
```

`next` prints how each owed artifact is proven, beside what is owed, so nobody has
to go and read the stage file to find out what they are held to. It carries the
same brief the agent would get — contract duty, gate criteria, what is handed over
rather than committed.

There was a third verb, `start`, and a hand-driven mode built around it. Both are
gone: a mode where the work is not the lead's is a third way to reach the same
states, and the fixes only ever landed on one of them.

**What a hand-driven stage cannot do is launder a verdict.** `done` records
`existence` for everything it is told, always and deliberately, so a stage whose
contract declares a command does not close on it — `luna work` is what closes
those, carrying what its verifiers actually observed.

## The stage contract

A stage is a TOML file in `src/stock/flows/<flow>/`. This is the whole shape:

```toml
id                 = "forge"
agent              = "claude"
brief              = "You build, clean and check, then judge the round. …"
requires           = ["scenarios", "approach", "contract", "worktree"]
produces           = ["code", "ci_green"]
produces_for_human = ["delivery_summary"]

[loop]
converges_on = ["ci_green"]   # what must be green before it may leave
max_rounds   = 4

[verify.ci_green]
run   = "make ci"      # the command that proves it
scope = "full"

[verify.delivery_summary]
kind     = "existence" # prose a person reads; claiming more would be a lie
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

## The convergent loop

`forge` is one stage where there were five. It builds, cleans and checks, then judges what
the round produced and either leaves or goes again:

```
   ┌──────────────────────────────────────────────┐
   ▼                                              │
 build → hygiene → acceptance → JUDGE THE ROUND ──┤
                                     │            │
                    converged ───────┘            │
                    progressed ───────────────────┤
                    regressed  → undo, then ──────┤
                    stuck      → a person
```

**Why merge them.** A failure found by acceptance goes straight back into building, in one
session, with the reasoning still in context — instead of a send-back paying a cold start
to re-read code the same agent wrote an hour ago.

**What it costs, stated rather than hidden.** Four contract boundaries. The one that
mattered was `verify requires ci_green`, which kept a model from being paid to read code
the compiler had not accepted — measured at $1.62, every cent spent before the pipeline
had said anything. Inside one stage that ordering is the agent's to keep, and the brief
says so.

**Who decides.** The verdict is the model's: no exit code distinguishes *this round fixed
something* from *this round traded one failure for another*, and that distinction is what
separates a loop that converges from one that burns its ceiling going nowhere. What the
model may not do is leave. `[loop] converges_on` names the evidence that has to be passing,
and the reducer refuses `converged` without it — so the mechanical check is the floor and
the judgement moves inside it.

**Where it stops.** Three ceilings, in `LoopLimits`: four rounds, two without progress, two
oscillating. A regression counts against oscillation rather than progress, because the two
ask different questions. Reaching one opens a gate when somebody is waiting and blocks when
nobody is — a ceiling that resolved itself would be the infinite retry INV-5 names.

**Where independence went.** An agent judging its own rounds is fast and biased, and the
bias is not fixable from inside. So the independent read is a separate stage after the
delivery: `review` runs cold, on work it did not write, with `Edit` and `Write` denied.

## How an agent is run

One subprocess per stage, through the harness's non-interactive mode: Claude uses
`claude -p --output-format json`; Codex uses `codex exec --json`. The prompt goes in on
stdin, and Luna reduces each native reply to an answer, session id, turns and token usage.
Claude also reports the model and USD cost. Codex's JSONL stream reports neither, and Luna
records that absence rather than inventing a price.

Luna used to drive the harness's terminal instead, and the reason it stopped is in
[lessons.md](lessons.md): everything that path required — pty sizing, a folder-trust
dialog, an input-ready marker, hand-tuned settles — was engineering against the wrong
interface, and none of it was about the model.

Three things follow from the transport:

- **Gating is real.** Claude removes each denied tool from the request. Codex has a coarser
  boundary: the exact `Edit` plus `Write` denial runs the thread read-only with approvals
  disabled. Luna refuses a partial Codex denial rather than overstating what it withheld.
- **Usage is recorded.** Tokens and cache land in the log per stage for both harnesses;
  price and model land there only when the harness reports them. `cost n/a` is an absent
  measurement, not zero. A call rejected by a later node check carries the same spend in
  its `Fail` or `Block` event, so failed attempts do not disappear from the bill.
- **The node remains the conductor.** Every harness process receives `LUNA_STAGE=1`, and
  the CLI refuses task-control commands under that marker. A stage may use `luna artifact
  put|get`, but it cannot close, unblock or otherwise move its task. Codex also receives
  developer instructions naming the inner stage or conductor role so host-installed Luna
  orchestration skills do not start a second control loop.
- **A stage can continue.** `context = "live"` resumes the session of an identically
  briefed stage, which is roughly an order of magnitude cheaper than starting cold. It is
  refused across a change of brief: a judging stage inheriting the builder's session would
  read its own reasoning instead of the delivery, and `luna flow check` says so before
  anything runs.

  **The brief is what keys it, and that is stricter than the role it replaced.** While
  the flow had twelve roles for twelve stages, every stage started cold by construction
  and the setting had nowhere to apply. Collapsing them gave it somewhere — but a role
  covered stages told different things — one label over a builder and a judge — so
  under it they could have shared a session neither should inherit from the other. Comparing the
  brief closes that: two stages told the same thing are one worker, and two told
  differently are two.

  A solo run gives every stage one brief, but leaves the stage's `context` declaration in
  force; the shipped full trail starts each stage fresh. A pack gives each stage its own
  brief and therefore its own session as well.

  Those cold starts are not economies to be recovered. `AuditContextChain` refuses a live
  stage whose predecessor is briefed differently, and it is right to: a coder continuing
  the planner's session reads the plan's reasoning instead of the plan, and a cleaner
  continuing the coder's reads its reasoning instead of its output.

  The session id is recorded in the log, with the stage's spend. It was a map on the
  runner until a real run showed what that costs: answering a gate ends the process, the
  shipped flow gates in the middle of the lead's run, and `build` started cold every
  time — one resumed pair out of three. A session the harness no longer has is retried
  fresh rather than failing the stage, and the retry reports `fresh`, so the cost column
  never claims a resumption that did not happen.

  A resumable session is matched by brief, harness and denied capabilities. This matters
  when `--agent codex` overrides a flow that declares Claude: the override changes only
  the process being started, not the immutable flow fingerprint, and a later invocation
  must not hand one harness another's session or resume a writable thread as a reviewer.

The harness writes its transcript while it runs, but the native session id arrives in the
same result as the completed call and is appended to Luna's log then. `luna console` can
therefore locate every recorded stage and can follow a resumed stage, but it cannot locate
the first call of a brand-new stage before that call returns.

## Briefs

There is no role, and no role file. A stage's TOML holds everything about that stage — its
contract, how each artifact is proven, its gate, and the **brief** its agent is given:

```toml
id    = "build"
agent = "claude"
brief = "You are building the delivery. …"
```

| Flow | Stages with a brief | Mechanical |
|---|---|---|
| `chore` | build | setup, verify |
| `fix` | diagnose, build | setup, verify |
| `full` | setup, intake, diagnose, plan, forge, shipping, review | — |

A stage is **mechanical** when it names no agent — that is the whole test, and it reads the
field that decides whether anything starts rather than a label beside it. A worktree is
named after the stage. A solo run (`fsm.Solo`) collapses every stage onto one brief and
drops the denials, which is the independence solo already says it does not have.

**Why the brief moved.** A role table was a pointer a stage followed to find out what it
was told, and the two drifted: the table said one role shipped while the stock held five.
One file per stage answers "what happens here" without a second lookup, and a brief written
for one stage can say things a shared one cannot. The cost is real and was weighed: two
stages doing similar work now carry their own text, so keeping them consistent is a
person's job rather than a file's.

**Why the role went too.** Absorbing the brief left `role` a field that resolved to nothing,
and it was not merely idle: `luna flow check` counted distinct role names and reported "pack
of 5" for a flow that ran seven differently briefed agents, because a builder and a
judge shared a label. A name that makes the tool undercount is worse than no name. What it did is
now done by what it was standing in for — the brief keys a continued session, the stage id
names the worktree, and the agent decides whether a stage is mechanical.

**What solo costs.** Its `review` is not independent: the same agent re-reads its own work
in a fresh session. Pack mode gives that stage a separate session and withholds editing,
which is the stronger review. The mechanical checks are identical in both modes because a
command that runs over the delivered commit does not care who wrote it.

**Where the floor is.** Tool gating removes the *named* tools; it does not remove the
shell. On harnesses whose denial is per-tool, a reviewer denied `Edit` can still write
through `Bash`. This is stated rather than hidden: the separation is structural and real,
the containment behind it belongs to the sandbox (INV-4). A reviewer that writes corrupts
a *review*, which the next stage reads and a person can reject — not a *record*, which
nothing downstream could catch.

Codex's reviewer boundary is stronger and coarser: `Edit` plus `Write` selects its
read-only sandbox, which prevents worktree writes through the shell as well. Luna does not
translate either capability alone because that would claim a precision Codex does not
provide.

## The sandbox, the workstream and the socket

Every agent starts inside `ai-jail`, and inside it inside `ai-memory run`:

```text
ai-jail --network --worktree  ai-memory run --workstream <name>  <harness> <args…>
        └── contained first ──┘             └── then remembered ──┘
```

That order is load-bearing twice. Reversed, a call that must not escape could — the memory
wrapper would be outside the containment. And the workstream would not reach the agent at
all: the id travels to managed children as environment, and the jail clears the environment
on the way in.

**The workstream is the task's.** Every agent a task starts writes to one ledger, so what
the planner learned is there for the coder and for the next task over the same ground. The
default is the project's, from `luna config set workstream`; a task may name another with
`--workstream`, or ask Luna to open one with `--new-workstream`. What it used is written
into the opening event, so a replay reads where the work went rather than where the config
points today.

Selecting is tried first and creating is the fallback, and only for a task that asked.
`ai-memory` answers 404 for a name that does not exist and 409 for one that does, both
before the agent starts — so the ordinary case costs one launch, the retry costs no model
call, and a typo in a name stops the stage instead of opening a second ledger.

Under Landlock, the only writable position an agent can reach is its own worktree —
`$HOME`, `/tmp` and symlinks out all fail, measured.

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

There was a fourth. A **guard** opened on what the delivery *contained* rather than on where
the task stood: a stage declared paths — migrations, credentials, deploy pipelines — and a
delivery touching one stopped for a person at every autonomy setting. It was removed, and
the reason is in `decisions.md`: the idea is right and the mechanism was too blunt to earn
its place. Nothing replaces it yet.

## The pack

`luna lead` is one agent carrying the task end to end. `luna fleet run` is a pack: the lead
conducts, and every distinct stage briefing gets its own agent session and worktree. The
stage itself declares the harness, briefing and denied capabilities; there is no role
catalogue beside the flow.

```sh
luna lead      AVG-1              # solo: one broad agent policy
luna fleet run AVG-1              # pack: one member per distinct stage briefing
luna fleet report --since 12h     # every project, grouped by what each task needs
```

The size of the pack is the flow's rather than a flag's, and `luna flow check` reports it:

```
flow full/662a73f3cbedc7b1 (7 stages)
pack of 7: setup, intake, diagnose, plan, forge, shipping, review — `luna lead` runs the same flow with one
```

**What the pack buys.** A stage can deny capabilities to its own agent: `review` withholds
`Edit` and `Write`, so an audit is independent rather than a stage that merely says it is.
Each distinct briefing gets a separate session, so the builder's reasoning is not handed
to the agent judging the delivery.

**What it costs.** A conductor billed every turn, a worktree per stage and a cold start for
each distinct briefing. A solo run pays none of the conductor cost and claims none of the
pack's independence — its `review` re-reads its own work, which is the one half of
independence a single agent can have.

**The mode is not recorded.** Agent, briefing, session policy and tool denial are policy
rather than history, so they are outside the flow fingerprint. A task begun solo can be
continued as a pack and the other way round; the contract it must satisfy does not change.

Parallelism between tasks is not a fleet decision — it is the property everything else
rests on. One active stage worktree and one lead per task is what makes two tasks unable to see each
other's work, and the store has been proven safe for concurrent appends across tasks since
before there was a fleet to need it.

`luna fleet report` is the morning's product rather than a side effect of it: every project
and task in the central store grouped by what has to happen next, with both verdicts and the
bill. A task that no longer replays is *reported* rather than skipped, because it is exactly
the one that would otherwise sit unnoticed forever.

## State

Append-only SQLite, keyed by project and task. No event `UPDATE`, no event `DELETE`. The
log is the state; anything else is a projection that can be rebuilt. Artifact bodies are
also versioned appends, with deletion allowed only after a task ends; their recorded
hashes and event history remain.

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
  internal/store/    central project-scoped log, replay, blob store
  internal/agent/    calling an agent: one subprocess, prompt in, usage out
  internal/node/     running a stage: worktree, sandbox, socket, verification
  internal/cli/      commands
  internal/lead/     conducting a task, and the model that judges a gate
  stock/             defaults: flows and profiles (embedded TOML)
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
- **Per-model pricing** — Claude reports cost and Luna records it. Codex reports token
  usage but no USD value on this interface. Luna embeds no price table, so a Codex task
  with a USD ceiling is refused before its first agent call rather than running under an
  unenforceable budget.
- **A stage's skills** — `src/stock/skills/` is empty. A stage declares an agent and a
  brief; the skill set is parsed, travels in the order as `skills=`, and is read by
  nothing. Not to be confused with `skills/` at the root, which is the opposite
  direction: how to *use* Luna, for whoever drives it from outside.

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
