---
name: luna
description: >
  Operate the luna binary — the verifier that proves what a phase delivered by
  running a command over the delivered commit, and records what happened in one
  append-only ledger outside every repository. Covers the nine verbs, the TOML
  contract, the scope ladder, the three exit codes, the autonomy modes (always
  starting at manual), blocking on missing information, how to DISCOVER a
  project's own gate instead of assuming `make`, and the durable-ledger
  requirement inside a sandbox.
  Use it whenever you call luna, write a contract, read where a task stands, or
  when "luna" appears in an error. This is not the orchestrator: it proves what a
  phase delivered, it does not decide which comes next.
metadata:
  short-description: How to call luna — contract, verification, trail, autonomy, blocking
---

# `luna` — the verifier

Luna **proves what was delivered** and **records what happened**. It runs no
agents, opens no worktrees, builds no sandboxes and decides no transitions: it is
called at a phase boundary, by whoever conducts the flow.

> **It inherits its environment, it does not create one.** Whoever conducts
> composes the session before the agent starts — jail, memory, both or neither.
> Luna runs in what it was given and has no opinion about it.

Which phases exist, what they are called, and what order they run in is **yours**.
Luna validates no phase name and knows no list of them.

## The nine verbs

```sh
luna check --contract <file|-> [--run <id>] [--commit <sha>] [--base <sha>] [--round N] [--json]
luna contract lint <file|->
luna record --run <id> --event <kind> [...]
luna state [--run <id>] [--json]
luna trail [<id>] [--json]
luna runs [--here] [--open] [--project <repo>] [--since <duration>] [--json]
luna session <agent> [--launcher <cmd>] [--skill <name>] [--print]
luna install-skills <claude|codex> [--dir <path>] [--dry-run] [--print]
```

`luna help` is authoritative. If this document and `luna help` disagree, the
binary is right.

**Without `--run`, the branch answers**: a checkout on `luna/<run>` knows which
run it is. It is the authority because it travels with the work — a directory can
be moved.

**One branch per task, not per phase.** The older shape was `luna/<run>/<phase>`,
from when each phase had its own worktree. A phase in the name ages at the first
transition. What answers where a task stands is `luna state`, never the branch
name. Branches in the old shape are still read; the suffix is ignored.

## The three exit codes

| Code | Means | What you do |
|---|---|---|
| **0** | proven | move on |
| **2** | **not** proven — a check observed a failure | read what failed and go back to the loop; **do not advance a phase** |
| **1** | Luna could not run at all | fix the environment; this is **not** a bad delivery |

Confusing 2 with 1 sends work back because of a broken machine. Always tell them
apart.

## The one rule the first real use broke

Two complete tasks ran, both reported green, and **neither called `luna check`
once** — 10 `phase` events, 1 `gate`, 0 `check`. Both told the truth: they ran
the gates in their own trees. But the ledger kept the claim where it should have
kept the proof.

**A phase that has a contract does not close with `record`. It closes with
`check`.**

```sh
# WRONG — this is you approving yourself
luna record --event phase --phase acceptance --status done --found "gates green"

# RIGHT — the command runs over the delivered commit, and the verdict is its own
printf '%s' "$CONTRACT" | luna check --contract - --round 2
```

`record` is for what Luna does **not** verify: a phase starting, a gate answered,
a block, a discovery. `check` is for what it does. Confusing the two turns the
tool into a diary.

Luna warns when a run closes having never proven anything:

```
  note: MAX-2 is done and nothing was ever proven — no `luna check` ran for it.
  The ledger has what you said, not what a command observed.
```

A warning and not a refusal on purpose — a phase with nothing provable by command
is normal, and refusing would be Luna deciding what "finished" means. But if that
warning appeared on a phase that **had** a gate to run, you skipped the only part
that is not opinion.

### The window between committing and proving

At the moment a phase finishes building, the commit does not exist yet, so the
order is easy to invert:

1. the phase runs its round and proves **in the tree** — provisional evidence,
   and the phase stays `running`, never `done`;
2. the writing gate is approved and the commit is made;
3. `luna check` runs the contract **over that commit**;
4. only a green check makes the phase `done`. A failure goes back to the loop,
   and the next round commits on top.

**Between the commit and the check, nothing is `done`.** A run that says `done`
in that window is asserting what nobody proved. This is the window an interrupted
session leaves a task closed by assertion — the warning above appeared in four
consecutive tasks that way, each with the phase closed minutes *before* its first
check, and one of them only came out right because the person remembered the
previous case. That is human memory doing the process's job.

## The contract

TOML, composed by whoever conducts, delivered on **stdin**. Luna keeps none: a
contract on disk inside a checkout is one more file somebody has to keep in step
with the flow that produced it.

```toml
phase    = "forge"
requires = ["scenarios", "approach"]
produces = ["code", "ci_green"]
produces_for_human = ["delivery_summary"]

[verify.ci_green]
run   = "make ci"      # the command that proves it
scope = "full"         # what a zero exit establishes

[verify.code]
kind = "existence"     # nothing proves this, and that is said rather than assumed

[verify.scenarios]
kind = "existence"
path = "tests/scenarios.md"   # with a path, git answers, not the agent

[loop]
converges_on = ["ci_green"]
max_rounds   = 4
```

**`produces_for_human`** is what a person reads (a review report, a minimal
case). Checked in the output like any other; no later phase will ask for it.

### Scope: what a passing check establishes

```
full  >  targeted  >  human  >  existence
```

| Scope | Means |
|---|---|
| `full` | the project's whole verification command ran |
| `targeted` | only what the change touched was run |
| `human` | a person looked and said so |
| `existence` | the file is there, and nothing more is claimed |

**Scope never upgrades.** Claiming `full` for a targeted run is the one lie the
ledger cannot detect later. `existence` is the honest answer for prose — a plan,
a report, a briefing. A command with no declared scope counts as `targeted`:
underclaiming costs you proving more, overclaiming is a lie in the record.

**A green gate is not the same as the acceptance criteria being met.** The gate
proves the code works; it does not prove the code delivers what was asked. In a
measured task the two were conflated — the gate passed, the phase closed, and the
person who noticed a criterion was not observable in the product was the reviewer,
who does not own that. If a phase owes a criterion-by-criterion verdict, that is
its own artifact with its own clause, usually `produces_for_human`.

### Writing the `run` line

**It is a single line in double quotes.** Luna undoes no escapes: whatever is
there reaches `sh -c` as it stands. A `\"` arrives as a literal quote, and a
multiline TOML string (`'''`) becomes one word — `sh: line 1: for n in ...: No
such file`. None of that is the delivery failing.

A clause that needs more than one command, internal quotes or a loop **becomes a
script outside the repository**, and the contract calls the script:

```toml
run = "sh /abs/path/verify.sh"   # do not embed the loop in the run
```

That is also how not to lose the output of a long suite: the script writes the
whole log somewhere durable, so an E2E failure stops reaching the ledger
truncated.

**`kind = "existence"` points at a file the delivery creates, not at a folder.**
With a folder path, the detail is the recursive listing; Luna summarises a long
one rather than aborting, but naming the file is what makes the check mean
something.

**Running the lint first is cheap**, and it reports **every** error at once:

```sh
luna contract lint - < contract.toml
```

It refuses: an artifact with no verifier, a loop converging on something the
phase does not produce, `run` together with `kind`, an unknown scope, a
`[verify.x]` block for an artifact the contract does not owe (which is how a
rename leaves a check that *looks* like it is running).

## Verifying

```sh
luna check --contract - --run MAX-2 --round 2 < contract.toml
```

It runs over **the delivered commit**, not over your working tree. An uncommitted
file, a local `.env` or a stale build make a green that says nothing about what
was delivered — and that incoherence, not sabotage, is the dominant way a green
check is wrong.

**The proving checkout is clean and disposable.** Luna materialises the commit in
a fresh tree, outside your worktree: there is no `.venv`, no `node_modules`, no
configured shim there. The gate the setup discovered almost always assumes
dependencies are installed, so each clause's `run` is the **pair**, not just the
gate:

```toml
run = "make bootstrap && make ci"   # <discovered bootstrap> && <discovered gate>
```

So discovery records **two** things — the bootstrap command and the gate.
`contract lint` does not cover this failure: it validates the contract's shape,
not the environment it will run in. Taking the gate alone costs a whole first
round, with a FAIL (`command not found`, `python: No such file`) that is not the
delivery's.

**In a worktree, `--commit` is optional.** Without it Luna resolves the HEAD of
the worktree you are standing in, which is where the delivery is. Passing a
resolved SHA is still the most explicit thing you can do, and it is what to reach
for when the delivery is somewhere other than where you are:

```sh
luna check --contract - --run MAX-2 --round 1 \
  --commit "$(git -C "$WT" rev-parse HEAD)" --base "$BASE" < contract.toml
```

**Pass `--base` whenever you know where the phase started.** With it, a delivery
equal to the base is reported as no delivery. Without it, a phase that committed
nothing passes on the code it was handed — measured twice, and both times it cost
money for nothing.

Each artifact checked becomes one ledger line, with verdict, scope and exit code.
Do not record that by hand.

## The command belongs to the project, never to Luna

Luna **does not know** what `make` is. It runs what the contract says, and the
contract carries what the project declares: `make ci`, `pnpm check`, `cargo
test`, `tox`, `just verify`.

Finding out which: the cascade in
[`references/gate-discovery.md`](references/gate-discovery.md) — **the project's
own documentation first** (`AGENTS.md`, `CLAUDE.md`, `README`, `CONTRIBUTING`),
the build files after.

Record the finding, with its source:

```sh
luna record --event discovery --phase setup \
  --found "gate: pnpm check" \
  --where "package.json scripts.check (README confirms)"
```

**`--where` is required** and Luna refuses without it: a finding nobody can check
is a claim, and whoever answers the next gate has to be able to go look at the
file.

**Never write a command in by habit.** `make ci` is *this* repository's gate, not
every repository's. A contract running `make ci` in a Node project fails because
the target does not exist — and it fails well, with exit 2 — but it spent a whole
round discovering what the documentation said on its first line.

If no gate is detectable at all, **block and ask**. A guessed gate that passes
proves nothing, and there is no cheaper moment to ask than before the first
round.

**It records and never consults.** The command enters the contract's `run` on
every `luna check`; Luna does not learn a project's gate to reuse later. A gate it
remembered would be project configuration living inside it — which is what the
per-project store was removed to avoid.

## Recording what Luna does not verify

```sh
luna record --run MAX-2 --event phase --phase forge --status running
luna record --event gate --gate approve-plan --answer approved
luna record --event autonomy --autonomy semi --note "the user asked"
```

`event`: `phase` · `check` · `gate` · `block` · `unblock` · `autonomy` · `discovery`
`status`: `running` · `awaiting_gate` · `awaiting_resume` · `blocked` · `done` · `abandoned`

**Before delegating the next phase, not after.** If the process dies, the last
line has to still be true.

**`--found` is valid on any event, not only `discovery`.** It is where the
phase's main finding goes — *"whole suite red on the base: vitest 4 shadows
jsdom"* — and `trail` shows it. Only `discovery` requires `--where` alongside; on
another event, `--where` without `--found` is refused (a source with no finding
says nothing).

**`--project` when the command runs outside the task's repository.** Otherwise
runs seeded from one checkout all carry *its* remote, and `runs` groups by
project — they appear under a repository none of them touched.

## Autonomy — starts at `manual`

| Mode | What passes on its own |
|---|---|
| **`manual`** (default) | nothing |
| **`semi`** | the decision gates that declare that floor |
| **`auto`** | everything that declares a floor, including the writing ones |

Which gates exist and what floor each declares belongs to whoever conducts the
flow; Luna only records the current mode.

**It changes only at the user's explicit request.** Do not infer it from haste,
from a simple task, or from "go ahead". It changes in flight, but **a gate
already open stays with whoever opened it**.

## Blocking on missing information

**In every mode, including `auto`.** Running without asking is the point; running
without thinking is how a night fleet produces expensive noise.

```sh
luna record --event block --status blocked --phase forge \
  --question "should --largest return one argument?" \
  --looked "the contract, clause 4 — says 'the largest', undefined with --max" \
  --looked "tests/ — the combination is not covered" \
  --needs "which of the two readings holds"
```

**`--looked` is required** and Luna refuses without it. It is what separates a
legitimate block from laziness: a block that says only what it wants is
indistinguishable from a phase that did not read what it already had.

When handing it to a person: the three sections, **no ceremony**. The question,
where you already looked and what each source failed to answer, what unblocks it.
No apology, no preamble, no offering three paths when the question is one.

Blocking is not failure. Guessing is.

## Reading what happened

```sh
luna state              # where this run is now (the branch answers)
luna trail MAX-2        # the task's log: everything that happened, in order
luna runs --here        # which runs are open in this repository
luna runs --open        # everything unfinished, anywhere
luna runs --since 12h   # the fleet; blocked first
```

`state` is the **last line** of that run — `tail`, not a replay. `trail` is
**all** of them:

```
$ luna trail WID-1
WID-1  github.com/me/widget
done, 9 events

17:19  discovery  setup     gate: node test.js   (package.json scripts.check)
17:19  phase      forge     running  round 1
17:19  check      forge     FAIL ci_green   full   r1   node test.js
                            AssertionError: 6 !== 4
17:19  check      forge     ok   ci_green   full   r2   node test.js
17:19  gate       commit    confirm-write approved
17:19  phase      close     done  widen delivered
```

That is what you send a person who asks *"what happened on this task?"*. Add
`--json` to any of them for a script.

## The ledger has to be durable

```
$XDG_DATA_HOME/luna/ledger.jsonl     (~/.local/share/luna/ledger.jsonl)
```

The shape of each line, the closed enums, how to read it and how to resume a task
from what was already recorded are in
[`references/ledger-schema.md`](references/ledger-schema.md).

**Luna writes nothing into your checkout** — no state file, no anchor, no ignore
entry.

Before the first write it asks the kernel whether that directory survives the
process. **On tmpfs, it refuses.**

```
the ledger is not on durable storage: ~/.local/share/luna is in memory…
```

This is **not an error to work around**. Inside a sandbox with `$HOME` on tmpfs,
the write would work, report success and vanish — the write and the read agreeing
with each other and with nobody else. The way out is to map the directory
read-write into the sandbox, which the message names, not to silence the refusal.

> **Careful with `/tmp`.** It is tmpfs on Arch and on any systemd default. A
> ledger there would be lost. The refusal is right.

## When NOT to use Luna

| The situation | What to do |
|---|---|
| the phase has nothing provable by command | do not invent a contract; `kind = "existence"` is the honest answer |
| a question, a reading, a one-line fix | do it and move on |
| you want to orchestrate phases | the flow calls Luna, not the other way round |
| you want to contain the agent | that is a sandbox, chosen before the agent starts |

A contract nobody can collect on by command is ceremony. Saying "this cannot be
proven, and here is why" is worth more than fabricating a check that always
passes.

**A flow does not stop because Luna is absent.** Without it, the phase is proven
by running the project's gate by hand and the result goes to a person. What is
lost is the record, not the work — which is the honest way round: a tool that
made itself required would be deciding what happens next.
