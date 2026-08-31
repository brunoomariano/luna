---
name: luna
description: Drive a coding task through Luna — open it with a statement worth building a contract from, pick the flow and the mode, and answer what it stops for. Use when the work is a real change to a repository and you want it verified by running the tool rather than by believing a claim.
---

# Driving a task through Luna

> **Check the binary first.** `luna version` says which build you have and which
> flows it carries. This skill is written against a build with three flows —
> `chore`, `fix`, `full` — and the `--flow`, `--workstream` and `fleet run <id>`
> surface. Against an older one it fails at the first unknown flag with nothing
> saying which of the two is behind, which is how somebody lost a run to
> `unknown flag --flow`. `make install` from a checkout, or
> `go install github.com/brunoomariano/luna/src/cmd/luna@latest`.

Luna is a state machine that conducts agents through a flow. It decides which
stage runs, and it closes a stage on what a command returned — not on what an
agent said. Your job here is the two ends: **state the work well enough to build a
contract from**, and **answer what Luna stops for**. Everything between those is
not yours to steer.

You are outside Luna in this skill. The agents Luna starts are not you.

## When to reach for it, and when not

| the work | do this |
|---|---|
| a real change to a repository, verifiable by a command | Luna |
| a question, a reading, a one-line fix you can verify by eye | just do it |
| something with no way to prove it worked | say so, then just do it |

Luna costs a flow's worth of agents. Reaching for it on work that takes one edit
buys ceremony and a bill.

## The two decisions that cost the most

They are independent, and the defaults are not the cheap ones.

**The flow** decides how many stages run. `--flow` defaults to `full`. The three
are fixed — see *The three flows* below for what each carries and how to pick.

Pick by how much ceremony the work deserves, not by how important it feels.
Measured: `diagnose` costs $0.88 over 19 turns on the task class it exists for
and $2.88 over 48 on the wrong one — running an investigation where there is
nothing to investigate is 3.3× for nothing.

**The kind** says what you believe the work is, and it defaults to `feature`. It
does not decide which stages run: the one conditional stage that ships is
`full`'s `diagnose`, and it keys on what `intake` concluded after reading the
code — not on what you typed before anyone had read anything.

```sh
luna flow check          # what each flow carries, and where it stops
```

`--flow` cannot change after the task opens: the flow's identity goes into the
opening event, and a task that switched mid-run would be a log no replay can
read. Getting this wrong means opening a new task, not fixing this one.

## Before you open anything

```sh
luna flow check
```

It says what each flow carries, how many agents its pack keeps, and — once any
project has run one — what each has cost in the central log, median by stage. `--flow`
cannot change after a task opens, so this is the one moment the decision is
cheap.

If the project's tests need a step between `git clone` and "the tests run", you
do not have to say so: `setup` reads the project and finds it, and Luna records
what it found once a person has confirmed the gate. Luna opens a clean worktree
**per stage**, so without that step every stage rediscovers it and the ones that
cannot fail on a check that was never about the work — a real task paid $10.36 of
$19.13 to learn it.

Everything else is `luna config`, and it is per project:

```sh
luna config                                  # what this project and this machine set
luna config set lead_harness codex           # conductor and gate judge
luna config set workstream the-project
luna config unset turn_budget
```

`editor` and `lead_harness` are the machine's — set them once and every project
here has them. `workstream`, `turn_budget` and `profiles` are the project's, and
a project setting wins over the machine's. `bootstrap` is shown and cannot be
set: it is what `setup` discovered, and typing it as well would be two sources
for one fact.

The measured harness names are `claude` and `codex`. `lead_harness` selects the
model Luna asks to conduct a pack and judge autonomous gates. Stage execution is
selected separately by the flow or by `--agent` on `lead`, `fleet run` and
`work`; an override changes the process used, not the task's flow fingerprint.

The settings live in the central database, so nothing about them is in the
checkout. A `.luna/config.toml` left over from an older build is not read at all
— not even once — so a project that had one sets what it wants again with `luna
config` and can then delete the file.

## Where state lives

There is one database for the installed Luna:
`$XDG_DATA_HOME/luna/luna.db`, or `~/.local/share/luna/luna.db`. The daemon starts
on demand and is the only process that opens it for writing. Every ordinary CLI
process opens it with SQLite read-only mode and forwards events and artifacts to
the daemon. Do not put `luna.db` in a target repository.

Task ids are project-local. Luna derives the project from the normalised `origin`
remote, falling back to the main checkout path when there is no remote. Commands
that act on one id use the current checkout. Workload commands cross projects and
always print the project key:

```sh
luna task list            # every active task, globally
luna gates                # every task waiting on a person, globally
luna stuck --for 2h       # every task stopped too long, globally
luna fleet report         # all history grouped by what it needs next
luna flow check           # global open-task survey and spend history
```

On first use, the daemon imports the former per-project data-home stores and an
older checkout-local `.luna/luna.db`, then renames each source `.migrated` only
after an exact copy. `LUNA_STORE` selects a different central database for an
isolated test; it is not a way to restore one database per repository.

Do not point `LUNA_STORE` at a populated database from the former per-project
layout. Its rows have no project key, so Luna refuses to open it as central;
leave it in one of the legacy locations above so the daemon can import it with
the project identity.

## The three flows

They ship inside the binary. There is no project override and no file to edit:
one build, one set of flows, every repository the same. Your one decision is
which of the three a task opens on, and it cannot change afterwards.

| flow | stages | for |
|---|---|---|
| `chore` | setup · build · verify | a change with a known shape and nothing to decide |
| `fix` | setup · **diagnose** · build · verify | something is broken and the cause is not yet known |
| `full` | setup · intake · **diagnose** · plan · forge · shipping · review | anything worth planning, and everything worth reviewing |

`full` decides for itself whether the investigation runs. `intake` reads the task
and the code and concludes whether something is *broken* or *missing*; only the
first opens `diagnose`. You do not have to know which when you open the task.

`fix` is the shortcut for when you already do: it has no intake, and its
`diagnose` always runs. Choosing the flow *is* the declaration that something is
broken, so there is nothing left for a condition to ask.

### Choosing from what the task says

Read the task's own words, in this order, and stop at the first that matches:

1. **Do you already know what broke, and roughly where?** — a stack trace, a
   reproduction, a regression you can point at. `fix --kind bug` skips the
   reading and goes straight to the investigation.
2. **Is the shape of the change already settled?** — a version bump, a rename, a
   flag with one obvious implementation, a lint rule. Nothing to plan and
   nothing to judge: `chore --kind chore`.
3. **Otherwise, `full`** — and it does not matter whether you call it a bug or
   a feature: `intake` reads the code and decides whether the investigation
   runs. Here the kind is a label on the task, not a fork in the trail.

Two traps worth naming, both measured:

- **Importance is not ceremony.** An urgent one-line fix is still a `chore`.
  What earns `full` is a change with decisions in it, not a change that matters.
- **A vague statement makes the flow moot.** `full` costs the most and buys the
  least when `--acceptance` cannot be run. Fix the statement first; the flow
  cannot rescue it.

### Where it stops, and why

`full` opens three gates:

| stop | shows you | needs autonomy |
|---|---|---|
| `setup` | what the containment will actually be and which workstream it writes to — measured by an agent that runs *outside* the jail, so it can read what the jail would do | 6 |
| `plan` | the contract every later stage is held to | 9 |
| `forge` | the commit plan and the delivery summary, **before** anything is committed | 9 |

`chore` and `fix` open none. Nothing in them stops for a person — that is the
trade for picking a flow with nothing to decide, and it means they run to the end
unattended. `luna flow check` says so per flow, in those words.

### The loop

`forge` is one stage that builds, cleans, checks, and then judges the round:
converged leaves, progressed and regressed go again, stuck asks you. Four rounds,
two without progress, two oscillating — then it stops.

The verdict is the agent's, and the exit is not: `forge` declares what it
converges on, and Luna refuses "converged" until that command has passed. It
judges its own rounds, which is fast and biased; `review` afterwards is the
correction, run cold by an agent that cannot edit.

### Where the briefing lives

A stage file holds everything about that stage: its contract
(`requires`/`produces`), how each artifact is proven, its gate if it has one,
and the **brief** its agent is given. One file answers what a stage is for.

There is no role. A stage names its own `agent` and `brief`, a stage that names
no agent is mechanical, and a solo run collapses every stage onto one brief. So
"who does this" is not a question you configure — it is what the stage file
already says.

```sh
luna flow check          # what each flow carries, and where it stops
```

## Opening a task

The statement is not description. It is what the `plan` stage turns into a
contract, and what the delivery is then held against — `forge`'s own check on
`full`, `verify` on `chore` and `fix`. A vague
acceptance line produces a contract nobody can check, and the whole flow
downstream inherits it.

```sh
luna task new AVG-1 --kind feature --flow full \
  --about      "tally.sh sums its arguments and there is no way to get their mean" \
  --design     "add an --avg flag that prints the mean instead of the sum" \
  --acceptance "./tally.sh --avg 1 2 3 prints 2; ./tally.sh 1 2 3 still prints 6; make ci is green"
```

Three fields and each answers a different question:

- **`--about`** — what is wrong or missing *today*. Present tense, about the
  code as it stands. Not the fix.
- **`--design`** — how it should be approached, only where the approach is
  actually constrained. Leave it thin when it is genuinely open; a design line
  that invents a constraint becomes an obligation somebody has to satisfy.
- **`--acceptance`** — **settled by running something**. Every clause has to be
  a command and an expected result. "works correctly" is not an acceptance
  criterion; `./tally.sh --avg 1 2 3 prints 2` is.

The acceptance line is the highest-leverage thing you write. Every criterion in
it has to appear as an obligation in the contract — that is one of the gate's
own judgement criteria — so a criterion you leave out is one nothing will check.

Correcting it later is an event, not an overwrite:

```sh
luna task statement AVG-1 --acceptance "…"
```

## The two modes

```sh
luna lead      AVG-1     # solo — one agent carries the task end to end
luna fleet run AVG-1     # pack — the lead conducts, each stage runs on its own brief
luna fleet run AVG-1 --agent codex
```

**Solo** is one broad agent policy across the task, with one stage worktree at a
time; each stage's context setting decides whether the session is fresh or
resumed. It has no conductor, and its `review` re-reads its own work — the one
half of independence a single agent can have.

**Pack** is the lead plus one agent per brief the flow declares, each with its own
worktree and session. It costs a conductor billed every turn and a cold start at
every stage. What it buys is a review that is actually independent: `review` is
denied `Edit` and `Write`, which one agent doing everything cannot be.

Default to solo. Reach for the pack when the change is large enough that you
want the judging done by something that cannot edit.

The mode is not recorded, so a task begun solo can be continued as a pack.

## The knob

```sh
luna autonomy AVG-1 7 "unattended overnight"
```

One number, 0–10: the autonomy a gate needs before the lead may answer it alone.
Each gate declares an `autonomy_floor`, and the lead judges it when `knob >=
floor`. `0` judges nothing and is the default; a gate that declares no floor
resolves to 10, so only the most autonomous setting absorbs it. It moves mid-run, and moving it
writes an event with the reason — a gate already open still goes to a person.

There used to be a guard that no setting reached, opening on what the delivery
*touched*. It was removed: the idea is right and the substring matching was too
blunt to earn its place. Until something replaces it, **a delivery touching
migrations, credentials or a deploy pipeline stops for nobody** — set the knob
with that in mind.

## The budget

No ceiling by default. Set one for anything unattended.

```sh
luna budget AVG-1 15 "overnight, one feature"
```

A task that stops on its budget is recoverable: raise it and unblock. The
ceiling is checked where the *next* stage would open, so unblocking runs that
stage rather than re-running and re-billing one that already delivered.

USD ceilings require a harness that reports USD spend. Claude does; Codex's
JSONL stream reports tokens but no price. Luna refuses a Codex stage on a task
with a dollar ceiling before starting the agent, rather than treating unknown
cost as zero. `turn_budget` still bounds Codex by elapsed time.

## Exercising a flow for free

A dry run needs its own task, opened as a simulation:

```sh
luna task new TRY-1 --kind feature --flow full --simulated --about "exercising the flow"
luna lead TRY-1 --dry-run
```

No agent, no worktree, no model. It walks the flow, the log and the gates end to
end, which is what tells a broken flow apart from a broken integration.

The two halves are refused against each other, both directions, and neither is
hypothetical: dry-running a real task would mark stages passed without running
them, and continuing a simulated one for real would leave one history where some
stages ran and some did not, with nothing saying which.

## When it stops

Three endings, and all three are discoverable by command:

```sh
luna status AVG-1        # the flow, the pack, the ledger, the ceiling, the
                         # cost per stage with the model that answered, and
                         # where each stage's worktree is
luna task list           # every active task across every project
luna gates               # every task waiting on a person, globally
luna stuck --for 2h      # what has been stopped too long, globally
```

`luna status` is the first place to look and usually the last: it answers what
used to take four commands.

### A gate

```sh
luna gate show AVG-1
```

It prints the artifact, the criteria the artifact is judged on, and — if the
knob reached the gate — what the lead already concluded and why. That reading is
not the answer: the gate is still yours.

This is also the only command that puts a handed-over document in front of a
person, and it shows the one the gate is about. `luna artifact get` belongs to
the agent and works only from inside a stage, where Luna is listening on the
socket — so `scenarios` and `approach` are readable by the stages that require
them and by nobody at the terminal.

```sh
luna gate approve AVG-1
luna gate reject  AVG-1 "obligation 7 wants six cases and the contract permits five"
luna gate adjust  AVG-1 --replace "…"     # change it, then accept the change
```

A rejection sends the stage that produced the artifact round again with the
reason in hand. Say what is wrong specifically enough to act on — "not good
enough" costs a full stage and buys nothing.

You can also answer a gate mechanically, once, for the whole task:

```sh
luna gate checks AVG-1 --on review-artifact --run "make contract-lint"
```

The commands run against what the stage delivered, and the first failure is the
answer.

### A block

```sh
luna status  AVG-1       # which of the six kinds, and why, under the walk
luna unblock AVG-1       # once the cause is dealt with
```

Any command that refuses because the task is blocked names the way out, so you
never have to guess a command that writes to the log:

```
task "AVG-1" has no running stage to work (it is blocked)
  — deal with what stopped it, then `luna unblock AVG-1`
```

| kind | what it means |
|---|---|
| `contract` | the stage delivered less than it owed |
| `failed-check` | a declared command came back non-zero — the block carries the command, its exit code and its output |
| `over-budget` | the ceiling was reached — raise it and unblock |
| `tooling` | the machinery broke — sandbox missing, git absent, bootstrap failed |
| `failed-node` | the stage failed and the retry budget is spent |
| `no-progress` | the loop circled without converging |

`unblock` resets the retry budget, because the block *was* the escalation. Deal
with the cause first: unblocking a `tooling` block without fixing the thing is a
second failure with a second bill — which is a trap somebody read this warning,
agreed with it out loud, and fell into anyway.

The two that get confused are `contract` and `tooling`, and they have opposite
treatments: one is fixed by making the work right, the other by making the
machine right. A `failed-check` now carries the command, its exit code and its
output, so you should not have to run the suite by hand in a parallel worktree to
find out which you have.

## The durable memory

Every agent a task starts writes to one workstream, so what one stage learned is
there for the next — and for the next task over the same ground. It is the
project's by default — `luna config set workstream <name>`.

```sh
luna task new AVG-1 --kind feature --workstream migration       # select another
luna task new AVG-2 --kind bug --new-workstream spike-tls       # open a new one
```

`--new-workstream` is the only way Luna opens one. That is deliberate: inferring
"create it" from a name that does not resolve would make a typo write to a
second ledger instead of stopping the stage.

## What not to do

- **Do not report a stage you did not carry out.** `luna done` is for a stage
  done by hand, and it runs what the contract declared. Reporting a delivery
  you did not make puts a fact in an append-only log that nothing produced.
- **Do not work around a stage that will not close.** A stage refusing to close
  is the contract catching something. Read what it says is still owed.
- **Do not raise the knob to get past a gate you have not read.** The setting is
  about who *may* answer, not about the answer.
- **Do not edit the flow to make a task pass.** Changing a flow under an open
  task stops it replaying, and the flows come from the binary anyway.
- **Do not treat a `[SHOULD-FIX]` from `review` as this task's work.** It is a
  real defect this change did not introduce. Open a task for it.
- **Do not claim more than the check proved.** Evidence carries a scope —
  `existence` for a file that is there, `targeted` or `full` for a command that
  ran — and scope never upgrades.

## The rest of the surface

```sh
luna version              # which build, and the flows it carries
luna next   <id>          # the order: stage, briefing, agent, worktree, base, what is owed
luna work   <id>          # run the agent for that stage and close it on its checks
luna done   <id> --delivered <a,b> [--commit <sha>]
luna artifact put <name>  # hand a document to Luna — runs inside a stage only
luna task show  <id>      # one task: its stage, contract, spend and artifacts
luna task list            # every active task across projects
luna console    <id>      # native Claude/Codex transcript, follower and resume command
luna task abandon <id> <reason>
luna task forget  <id>    # drop a finished task's documents; the log stays
luna fleet report         # every project and task, grouped by next action
luna trust                # record Luna's worktree parent in Claude's trust file
```

`luna next` is a read and changes nothing. `luna help` is the whole list.

Inside a stage process, Luna deliberately accepts only `artifact put|get`, help and
version. Do not call `luna done`, `luna work`, gate or task commands from a stage: the node
owns transitions and will refuse them. `luna console` can locate a transcript after the
harness has returned its native session id; the first call of a new stage is not
discoverable through this command while it is still running.

## Installing this in a target project

This skill ships with Luna, in `skills/`. It is not `src/stock/skills/`, which
belongs to a different idea — the capability bundles a stage loads, which
travel in the order as `skills=`. This one is for whoever drives Luna from
outside, so it is not the binary's to embed.

Copy it where the work happens:

```sh
mkdir -p <target>/.claude/skills
cp -r <luna>/skills/luna <target>/.claude/skills/

mkdir -p <target>/.agents/skills
cp -r <luna>/skills/luna <target>/.agents/skills/
```

The first location is Claude's project skill directory; the second is Codex's.
Codex needs no equivalent of `luna trust`: Luna invokes its non-interactive
adapter with the repository check skipped inside the outer sandbox.

The target project needs no file of any kind. Its build step is discovered by
`setup`, and the rest is `luna config`, which writes into the central database.

Nothing Luna holds enters the target checkout — not task state, not blobs, not
costs, not settings, and not a directory of its own. What it writes there is one
line inside `.git`, saying where the central database is, because that is the one
path a contained stage can also read.
