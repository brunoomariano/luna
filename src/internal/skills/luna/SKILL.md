---
name: luna
description: >
  Operate the luna binary: prove what a phase delivered by running a command over
  the delivered commit, and record what happened in one append-only ledger outside
  every repository. Covers the seven verbs (check, contract lint, record, state,
  trail, runs, session), the TOML contract, the scope ladder, the three exit
  codes, the three autonomy modes, blocking on missing information, and how to
  DISCOVER a project's own verification command instead of assuming one.
  Use it whenever you call luna, write a contract, read where a task stands, or
  when "luna" appears in an error. Luna does not decide what happens next: it
  proves and records. Deciding is yours.
metadata:
  short-description: How to call luna — contract, verification, trail, autonomy, blocking
---

# `luna` — proving what was delivered

Luna answers one question a model cannot answer about itself: **did the command
actually pass, over what was actually delivered?**

It runs no agents, opens no worktrees, builds no sandboxes and decides no
transitions. You conduct; Luna proves and records.

Everything here is the tool. Which phases exist, what they are called, and what
order they run in is **yours** — Luna validates no phase name and knows no list
of them.

## The seven verbs

```sh
luna check --contract -        prove a phase's delivery; exit 0 / 2 / 1
luna contract lint <file|->    is this contract usable at all? (runs nothing)
luna record --run <id> …       write down what Luna did not verify
luna state [--run <id>]        where is this run now?  (its most recent line)
luna trail [<id>]              what did this run do?   (every line, in order)
luna runs [--here] [--open]    which runs exist, and which need a person?
luna session <agent>           start an agent briefed with what is open here
```

`luna help` is authoritative. If this document and `luna help` disagree, the
binary is right.

## The one rule that matters

**Never report a phase as done because you believe it is.** Run `luna check` and
read the exit code:

| Exit | Means | What you do |
|---|---|---|
| `0` | proven | move on |
| `2` | not proven — a check observed a failure | fix it, or block |
| `1` | Luna could not run at all | Luna is broken, not the delivery |

`1` and `2` are deliberately different. A broken tool must never read as a failed
delivery.

## Discover the project's command — never assume one

**Do not write `make ci` into a contract because most projects have a Makefile.**
Find this project's actual verification command before you write any contract:

1. `AGENTS.md`, `CLAUDE.md`, `CONTRIBUTING.md`, `README.md` — the command is
   usually named in prose.
2. `Makefile`, `justfile`, `package.json` (`scripts`), `Cargo.toml`,
   `pyproject.toml`, `.github/workflows/*.yml`.
3. If nothing names one, **ask**. A guessed gate that passes proves nothing.

Then record what you found, with where you read it:

```sh
luna record --run WID-4 --event discovery \
  --found "gate: pnpm check" --where "package.json, scripts.check"
```

Both `--found` and `--where` are required. A finding nobody can check is a claim.
Luna records this and **never consults it** — the command still arrives in the
contract on every call.

## The contract

TOML on stdin. Luna keeps none of it: you compose it from the acceptance criteria
a person approved.

```toml
phase    = "forge"
produces = ["code", "tests_green"]

[verify.tests_green]
run   = "pnpm check"     # THIS project's command, discovered above
scope = "full"           # what a zero exit establishes

[verify.code]
kind = "existence"       # nothing proves this, and that is said rather than assumed
path = "src/"
```

```sh
printf '%s' "$CONTRACT" | luna check --contract - --run WID-4 --base "$BASE"
```

**`--base` matters.** Given it, a delivery equal to the base is reported as no
delivery — otherwise a phase that committed nothing passes on the code it was
handed.

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
a report, a briefing.

### Checks run in a clean checkout

Luna verifies the **delivered commit** in a throwaway worktree, never your working
tree. A command that assumes an installed workspace has to install it first:

```toml
run = "pnpm install --frozen-lockfile && pnpm check"
```

The right command for a person is often the wrong command for a contract.

## Recording what Luna did not verify

```sh
luna record --run WID-4 --event phase --phase forge --status running
luna record --run WID-4 --event gate  --gate confirm-write --answer approved
luna record --run WID-4 --event autonomy --autonomy semi --note "the user asked"
```

Events: `phase`, `gate`, `block`, `unblock`, `autonomy`, `discovery`.
Statuses: `running`, `awaiting_gate`, `awaiting_resume`, `blocked`, `done`,
`abandoned`.

`check` is a seventh event and is **not** written by hand: it comes from `luna
check`, from a command that ran. Prove it or record a phase — do not assert it.

**Closing a run that never proved anything is a warning, not a refusal.** If you
see that warning, you skipped the part that matters.

## Blocking — at every autonomy, including auto

Autonomy is `manual`, `semi` or `auto`. **It starts at `manual`** and moves only
when the person asks. Luna records the mode; which mode clears which gate is your
decision.

When information is missing, stop and hand it over — in every mode:

```sh
luna record --run WID-4 --event block --status blocked \
  --question "which of the two readings of criterion 3 holds?" \
  --looked "the issue — names the field, not the rule" \
  --looked "AGENTS.md — silent on this" \
  --needs "the person picks a reading"
```

All three parts are required, and `--looked` is repeatable. **The account of
where you looked is what separates a real block from an unread file.** Running
without asking is the point; running without thinking is expensive noise.

Blocking is not failure. Guessing is.

## Reading what happened

```sh
luna state              # where this run is now — the branch answers if on luna/<run>
luna trail WID-4        # the whole story: phases, checks, discoveries, gates, blocks
luna runs --here        # which runs are open in this repository
luna runs --open        # everything unfinished, anywhere
```

Add `--json` to any of them for a script.

## The ledger

One append-only JSONL file at `$XDG_DATA_HOME/luna/ledger.jsonl`, outside every
repository. **Luna writes nothing into your checkout** — no state file, no
anchor, no ignore entry.

It refuses to write when that directory is not durable storage. If you see that
refusal inside a sandbox, the ledger's directory has to be mapped read-write into
it — the message names the path. **Do not silence it:** a record that reports
success and evaporates is the worst failure available, because both sides agree.

## What Luna is not

| Not this | So |
|---|---|
| an orchestrator | it starts no agent and opens no worktree — `session` starts one and immediately stops existing |
| a state machine | it decides no transitions; the last line *is* the state |
| a sandbox | compose one before the agent starts, if you want one |
| a memory | a `discovery` is recorded and never consulted |
| a planner | which phases exist and what they are called is yours |

If you find yourself asking Luna what to do next, that is your decision, not a
missing feature.
