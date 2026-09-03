![Luna](docs/assets/imgs/luna_banner.png)

# Luna

**An agent will tell you the tests pass. Luna runs them.**

Coding agents produce completion language whether or not the work is done — "tests
passing" while the suite is red, files "created" that exist only in the prompt. The usual
answer is more prose in the instructions. Luna's answer is to run the command and read the
exit code, over **what was actually committed**.

## Who calls it

**You don't.** Luna is a tool your *agent* runs, at the boundary between two phases of
work. You keep the setup you already have.

```
   you                     your agent                        luna
    │                          │                               │
    │  "implement XPTO-45"     │                               │
    ├─────────────────────────▶│                               │
    │                          │  writes code, commits         │
    │                          │                               │
    │                          │  "am I done?"  ──────────────▶│
    │                          │                               │  runs `make ci`
    │                          │                               │  over the COMMIT
    │                          │◀───────────  exit 2, ci_green │
    │                          │                               │
    │                          │  not done. keeps working.     │
    │                          │                               │
    │                          │  "now?"       ──────────────▶ │
    │                          │◀───────────  exit 0, proven   │
    │  "done, and here's       │                               │
    │◀──  what proved it"      │                               │
```

The agent cannot skip this the way it can skip a sentence in a prompt: the exit code is
not a suggestion, and what it ran is in the record either way.

## What it is not

Luna does not run your agent, open your worktree, build your sandbox, or decide what
happens next. Those belong to whatever you already use. It does three things nothing else
in a typical setup does:

- **Verification that ran.** A command returning zero over the delivered commit — not over
  the working tree, where an uncommitted file or a stale build artifact makes a green
  meaningless.
- **A contract.** What a phase owes and how each debt is proven, checked on the way out.
  Evidence carries how much it proves, and `existence` — "the artifact is there, nothing
  more is claimed" — is an honest answer.
- **A floor under the loop.** The model judges whether a round made progress; it may not
  declare the loop finished while the command it converges on is red.

## Inside

Four pieces, and only one of them decides anything.

```
                  the contract                        what a phase owes,
                  (TOML, on stdin)                    and how each debt is proven
                        │
                        │  phase = "forge"
                        │  produces = ["code", "ci_green"]
                        │  [verify.ci_green] run = "make ci", scope = "full"
                        ▼
   ┌───────────────────────────────────────────────────────────────────┐
   │  contract    parse · lint                                         │
   │              refuses a contract that cannot be read, and says     │
   │              every fault at once. Nothing here executes.          │
   └───────────────────────────────┬───────────────────────────────────┘
                                   ▼
   ┌───────────────────────────────────────────────────────────────────┐
   │  verify      a throwaway checkout at the DELIVERED COMMIT,        │
   │              cut from the repository — never the working tree     │
   │                                                                   │
   │                git worktree add --detach <sha>                    │
   │                  └─ sh -c "make ci"  →  exit code                 │
   │                                                                   │
   │              one piece of evidence per artifact, each carrying    │
   │              the scope it proves. Scope never upgrades.           │
   └───────────────────────────────┬───────────────────────────────────┘
                                   ▼
   ┌───────────────────────────────────────────────────────────────────┐
   │  ledger      one JSON line per verdict, appended, never rewritten │
   │                                                                   │
   │              ~/.local/share/luna/ledger.jsonl                     │
   │              ├─ outside every checkout                            │
   │              ├─ statfs first: refuses tmpfs, loudly               │
   │              └─ O_APPEND: a fleet writes with no lock             │
   └───────────────────────────────┬───────────────────────────────────┘
                                   ▼
                            exit 0 · 2 · 1
```

**Where a run stands is its most recent line** — read by tailing, not by replaying. Luna
decides no transitions, so it has no state to reconstruct, only a position to report.

Nothing is written into your repository: no state file, no anchor, no ignore entry. The
ledger knows where the worktree is, rather than the worktree knowing where the ledger is,
so a record outlives the tree it describes.

## Install

```sh
go install github.com/brunoomariano/luna/src/cmd/luna@latest
```

No dependencies. `go.mod` is three lines.

## The five verbs

```
   ┌──────────────────┬──────────────────────────────────────────────────────┐
   │ luna contract    │  read a contract and report every way it is unusable │
   │      lint <file> │  before anything runs. Cheap, and the mistake costs  │
   │                  │  nothing here.                                       │
   ├──────────────────┼──────────────────────────────────────────────────────┤
   │ luna check       │  run every declared check over the delivered commit, │
   │   --contract ─   │  and record each verdict.                            │
   │                  │                                                      │
   │                  │    --commit  what to verify (default: HEAD)          │
   │                  │    --base    what the phase started from. Given, a   │
   │                  │              delivery equal to it is no delivery     │
   │                  │    --round   which round of a loop this is           │
   │                  │    --dry-run say what would run, run nothing         │
   │                  │                                                      │
   │                  │  → exit 0 proven · 2 not proven · 1 could not run    │
   ├──────────────────┼──────────────────────────────────────────────────────┤
   │ luna record      │  record what Luna did not verify: a phase starting,  │
   │   --event <kind> │  a gate answered, a block, an autonomy change.       │
   │                  │                                                      │
   │                  │    phase · gate · block · unblock · autonomy         │
   ├──────────────────┼──────────────────────────────────────────────────────┤
   │ luna state       │  where a run stands: its most recent line.           │
   │                  │  With no --run, the branch answers.                  │
   ├──────────────────┼──────────────────────────────────────────────────────┤
   │ luna report      │  every run, blocked first, most recent next.         │
   │   --since 12h    │  This is the morning's product.                      │
   └──────────────────┴──────────────────────────────────────────────────────┘
```

A checkout on `luna/<run>/<phase>` knows which run it is, so `--run` is optional
everywhere. The branch is the authority because it travels with the work, where a
directory can be moved or made by hand.

## Trying it

Write a contract once, as a file:

```toml
# forge.toml
phase    = "forge"
produces = ["code", "ci_green"]

[verify.ci_green]
run   = "make ci"        # the command that proves it
scope = "full"           # what a zero exit establishes

[verify.code]
kind = "existence"       # nothing proves this, and that is said rather than defaulted
```

```sh
$ luna contract lint forge.toml
forge: 2 owed, ci_green, code

$ luna check --contract forge.toml --run MAX-2
FAIL ci_green                 full       make ci
     FAIL  internal/parser  0.4s · exit status 1
ok   code                     existence  delivered

forge is not proven: ci_green
$ echo $?
2
```

In real use the contract does not live in a file — it comes from the acceptance criteria a
person approved, and the agent pipes it in with `--contract -`. Luna keeps none of it.

## Blocking

A phase that cannot settle a question from what it has may stop and hand it over — at every
autonomy setting, including `auto`. Running without asking is the point; running without
thinking is how an unattended fleet produces expensive noise.

```
$ luna state --run MAX-2
MAX-2  blocked
  phase     forge

  the question   is --largest meant to return an argument?
  looked in
    the contract, clause 4 — says "the largest", undefined with --max
    tests/ — the combination is not covered
  what unblocks  which of the two readings holds
```

The middle section is required. A block that says only what it wants is indistinguishable
from a phase that did not read what it already had.

## Inside a sandbox

Luna does not build the environment it runs in, so it cannot know from the inside whether
`$HOME` is a tmpfs somebody handed it. There, a ledger write succeeds, reports success and
evaporates. So Luna asks the kernel first, and stops with instructions:

```
the ledger is not on durable storage: ~/.local/share/luna is in memory…
  For ai-jail, that is `rw_maps` in the config, or `--rw-map ~/.local/share/luna`
```

## Documentation

Four files, each answering one question.

| File | Question |
|---|---|
| [architecture.md](docs/architecture.md) | how does it work today? |
| [invariants.md](docs/invariants.md) | what always holds? |
| [decisions.md](docs/decisions.md) | what was chosen, and what was rejected? |
| [lessons.md](docs/lessons.md) | what did building it teach? |

## Development

```sh
make ci        # fixes what it can, then verifies. Run before a PR.
make ci-check  # verify only — what remote CI runs
```
