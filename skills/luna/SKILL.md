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

**The flow** decides how many stages run. `--flow` defaults to `full`.

```
chore   setup · build · verify                                      3 stages
fix     setup · diagnose · build · verify                           4 stages
full    setup · intake · diagnose · plan · build · refactor ·       9 stages
        pipeline · verify · audit
```

Pick by how much ceremony the work deserves, not by how important it feels.
Measured: `diagnose` costs $0.88 over 19 turns on the task class it exists for
and $2.88 over 48 on the wrong one — running an investigation where there is
nothing to investigate is 3.3× for nothing.

**The kind** decides which conditional stages apply — `diagnose` runs for a bug
and not for a feature, `audit` runs for a feature and not for a chore. It
defaults to `feature`.

```sh
luna flow check          # what each flow carries, and the pack it declares
```

`--flow` cannot change after the task opens: the flow's identity goes into the
opening event, and a task that switched mid-run would be a log no replay can
read. Getting this wrong means opening a new task, not fixing this one.

## Opening a task

The statement is not description. It is what the `plan` stage turns into a
contract, and what `verify` and `audit` hold the delivery against. A vague
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
luna fleet run AVG-1     # pack — the lead conducts, the flow's roles do the work
```

**Solo** is one agent, one worktree, one session across every stage. Cheapest,
and its `audit` re-reads its own work with no memory of writing it — the one
half of independence a single agent can have.

**Pack** is the lead plus the roles the flow declares, each with its own
worktree and session. It costs a conductor billed every turn and a cold start at
every role change. What it buys is an audit that is actually independent: the
`auditor` role is denied `Edit` and `Write`, which one agent doing everything
cannot be.

Default to solo. Reach for the pack when the change is large enough that you
want the judging done by something that cannot edit.

The mode is not recorded, so a task begun solo can be continued as a pack.

## The knob

```sh
luna autonomy AVG-1 7 "unattended overnight"
```

One number, 0–10: the criticality up to which the lead may answer a gate on its
own. `0` judges nothing and is the default. It moves mid-run, and moving it
writes an event with the reason — a gate already open still goes to a person.

A guard gate is the exception and no setting reaches it. It opens on what the
delivery *touched* — migrations, credentials, deploy pipelines — and whether
dropping a table was intended is not a thing a model can weigh.

## The budget

No ceiling by default. Set one for anything unattended.

```sh
luna budget AVG-1 15 "overnight, one feature"
```

A task that stops on its budget is recoverable: raise it and unblock. The
ceiling is checked where the *next* stage would open, so unblocking runs that
stage rather than re-running and re-billing one that already delivered.

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
luna status AVG-1        # the whole flow, and where this task stands in it
luna gates               # every task waiting on a person
luna stuck --for 2h      # what has been stopped too long
```

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
luna task show AVG-1     # says which of the six kinds, and why
luna unblock AVG-1       # once the cause is dealt with
```

| kind | what it means |
|---|---|
| `contract` | the stage delivered less than it owed |
| `failed-check` | a declared command came back non-zero |
| `over-budget` | the ceiling was reached — raise it and unblock |
| `tooling` | the machinery broke — sandbox missing, git absent |
| `failed-node` | the stage failed and the retry budget is spent |
| `no-progress` | the loop circled without converging |

`unblock` resets the retry budget, because the block *was* the escalation. Deal
with the cause first: unblocking a `tooling` block without installing the thing
is a second failure with a second bill.

## The durable memory

Every agent a task starts writes to one workstream, so what one stage learned is
there for the next — and for the next task over the same ground. It is the
project's by default, from `.luna/config.toml`.

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
- **Do not treat a `[SHOULD-FIX]` from `audit` as this task's work.** It is a
  real defect this change did not introduce. Open a task for it.
- **Do not claim more than the check proved.** Evidence carries a scope —
  `existence` for a file that is there, `targeted` or `full` for a command that
  ran — and scope never upgrades.

## The rest of the surface

```sh
luna next   <id>          # the order: stage, role, worktree, base, what is owed
luna work   <id>          # run the agent for that stage and close it on its checks
luna done   <id> --delivered <a,b> [--commit <sha>]
luna artifact put <name>  # hand a document to Luna — runs inside a stage only
luna task abandon <id> <reason>
luna task forget  <id>    # drop a finished task's documents; the log stays
luna trust                # tell the harness it trusts where Luna makes worktrees
```

`luna next` is a read and changes nothing. `luna help` is the whole list.

## Installing this in a target project

This skill ships with Luna, in `skills/`. It is not `src/stock/skills/`, which
belongs to a different idea — the capability bundles a *role* loads, which
travel in the order as `skills=`. This one is for whoever drives Luna from
outside, so it is not the binary's to embed.

Copy it where the work happens:

```sh
mkdir -p <target>/.claude/skills
cp -r <luna>/skills/luna <target>/.claude/skills/
```

The target project also needs `.luna/config.toml` if it wants its own
workstream, turn budget or interpreter — Luna runs without one, on the shipped
defaults.
