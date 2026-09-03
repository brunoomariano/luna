![Luna](docs/assets/imgs/luna_banner.png)

# Luna

**An agent will tell you the tests pass. Luna runs them.**

Coding agents produce completion language whether or not the work is done — "tests
passing" while the suite is red, files "created" that exist only in the prompt. The usual
answer is more prose in the instructions. Luna's answer is to run the command and read the
exit code, over **what was actually committed**.

```sh
$ luna check --contract - <<'TOML'
phase    = "forge"
produces = ["ci_green"]

[verify.ci_green]
run   = "make ci"
scope = "full"
TOML

FAIL ci_green                 full       make ci
     FAIL  internal/parser  0.4s · exit status 1

forge is not proven: ci_green
$ echo $?
2
```

Exit 0 proven, 2 not proven, 1 Luna could not run.

## What it is not

Luna does not run your agent, open your worktree, build your sandbox, or decide what
happens next. Those belong to whatever you already use. It is called *by* the agent, at a
phase boundary, and it does three things nothing else in a typical setup does:

- **Verification that ran.** A command returning zero over the delivered commit — not over
  the working tree, where an uncommitted file or a stale build artifact makes a green
  meaningless.
- **A contract.** What a phase owes and how each debt is proven, checked on the way out.
  Evidence carries how much it proves, and `existence` — "the artifact is there, nothing
  more is claimed" — is an honest answer.
- **A floor under the loop.** The model judges whether a round made progress; it may not
  declare the loop finished while the command it converges on is red.

## Install

```sh
go install github.com/brunoomariano/luna/src/cmd/luna@latest
```

No dependencies. `go.mod` is three lines.

## Use

Five verbs.

```sh
luna check --contract - [--commit <sha>] [--base <sha>]  # prove, and record each verdict
luna contract lint <file|->                              # static, before anything runs
luna record --run <id> --event <kind> …                  # what Luna did not verify
luna state [--run <id>]                                  # where a run stands
luna report [--since 12h]                                # every run, blocked first
```

With no `--run`, the branch answers: a checkout on `luna/<run>/<phase>` knows which run it
is.

### The ledger

One file outside every checkout, one JSON line per event, append-only.

```
$XDG_DATA_HOME/luna/ledger.jsonl
```

Where a run stands is its most recent line — read by tailing, not by replaying. Lines are
self-contained and bounded, so a concurrent fleet appends with no lock.

**Luna refuses to write when that directory is not durable.** Inside a sandbox with a tmpfs
`$HOME`, a write succeeds, reports success and evaporates — so Luna asks the kernel first
and stops with instructions rather than losing the record in silence.

```
the ledger is not on durable storage: ~/.local/share/luna is in memory…
  For ai-jail, that is `rw_maps` in the config, or `--rw-map ~/.local/share/luna`
```

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
