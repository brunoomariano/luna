# Architecture

How Luna works today. If this file and the code disagree, the file is the bug.

## The problem

A workflow written in prose is a suggestion to the model, not a guarantee. It is followed
almost every time — and the "almost" is what costs. All four of these were observed in
real runs:

- an agent told to **run** a command decided that running meant **printing** it;
- an agent repeated the same thing 100 times and on the 101st did something else;
- under long sessions, roles eroded — the reviewer started implementing;
- a chain broke silently, and nobody noticed until two phases later.

The usual answer is to write more prose. Luna's answer is to **run the tool and read the
exit code**, over what was actually delivered.

## The shape

Luna is called by whoever conducts the work. It starts no agent, builds no sandbox, and
decides nothing about what happens next.

```text
a person composes the session      ai-run: sandbox? durable memory? both? neither?
   │
   ▼
an agent conducts the phases       a skill, a person, a script — Luna does not care
   │
   ▼
at a phase boundary:
   luna check --contract -         the contract arrives on stdin
      │
      ├─ run each declared check over the DELIVERED COMMIT
      ├─ record one line per verdict
      └─ exit 0 proven · 2 not proven · 1 Luna could not run
```

Three verbs and two nouns. The contract says what a phase owes; the ledger says what
happened; `check` is the only thing that decides anything, and what it decides is whether a
command returned zero.

## The words

Nine of them, and they are used precisely throughout. The two that cost a reader the most
are first: `conductor` is the subject of most rules here and was never defined, and the
scopes lived only in Go while an invariant depended on them.

| Word | What it means |
|---|---|
| **conductor** | whoever drives the phases — an agent, a skill, a person, a script. Every "Luna does not decide" in this document has the conductor as its other half. Luna is called *by* one and never is one. |
| **run** | one task from start to finish, named by an id (`WID-4`) and carried by the branch `luna/<run>`. The unit the ledger groups by. |
| **phase** | one named stretch of a run — `forge`, `review`, `close`. The conductor names them; Luna only records which one a line belongs to. |
| **contract** | what a phase owes, as TOML on stdin: the artifacts it produces, and how each is proven. Luna keeps none of it between calls. |
| **artifact** | one thing a phase owes, named in the contract. A verdict is per artifact, not per phase — "forge failed" cannot say which debt went unmet. |
| **evidence** | what one check observed: the verdict, the scope, the command, the exit code. Recorded whether it passed or not. |
| **scope** | how much a passing check establishes. See below — it is the one word that carries a rule. |
| **gate** | a decision handed to a person, with an answer recorded. |
| **block** | a run stopping because information is missing, carrying three parts: the question, where the answer was looked for and what each source failed to say, and what would unblock it. Distinct from a gate — a gate has an artifact to judge, a block has a question nobody answered. |

**The scopes are a ladder**, and it is the reason `scope` is not just a label:

```text
full  >  targeted  >  human  >  existence
```

| Scope | A passing check establishes |
|---|---|
| `full` | the project's own gate ran, whole |
| `targeted` | only what the change touched was run |
| `human` | a person looked and said so |
| `existence` | the file is there, and nothing more is claimed |

A check may prove *more* than the contract asked and be accepted; it may never prove
less and be read as more. That is INV-3, and it is why an unknown scope is refused from
both sides rather than defaulted — a typo must not outrank the floor.

> **`floor` means two unrelated things in this repository**, and both are load-bearing.
> *The loop's floor* is the mechanical condition a repeating phase may not leave (below).
> *The coverage floor* is the 95% in the Makefile. Neither is going to be renamed; they
> simply do not refer to each other.

## Why the agent calls Luna, and not the other way round

Luna used to be the parent process: it opened worktrees, built the sandbox, started the
agent, read its reply and picked the next phase. That shape works and was measured — one
full cycle, seven phases, $7.42 over 124 turns — and it lost anyway, because two things
that both want to be the parent process do not compose. Adding Luna to a working setup
always turned into replacing it.

Inverting the call fixed that and removed most of the code with it. What remains is the
part nothing else in the surrounding stack had: a delivery proven by running a command, a
contract that says what "delivered" means, and a loop whose floor is mechanical.

## The contract

A contract is TOML, written by whoever conducts, and it arrives on stdin. Luna keeps none
of it: a contract on disk inside a checkout is a file somebody has to keep in step with a
flow that does not live here.

```toml
phase    = "forge"
requires = ["scenarios", "approach"]
produces = ["code", "ci_green"]
produces_for_human = ["delivery_summary"]

[verify.ci_green]
run   = "make ci"      # the command that proves it
scope = "full"         # what a zero exit establishes

[verify.code]
kind = "existence"     # nothing proves it; that is said rather than defaulted

[loop]
converges_on = ["ci_green"]   # what has to be green before the loop may end
max_rounds   = 4
```

Two checks come out of it:

1. **`luna contract lint`** — static, before anything runs. Every fault at once, because a
   contract is written by hand at a gate and one error per run turns a five-minute
   correction into five rounds of it.
2. **`luna check`** — on the way out. The phase is not proven until its checks pass.

`scope` is the honesty knob: `full` (the whole gate ran), `targeted` (only what was
touched), `existence` (the artifact is there and nothing more is claimed), `human`. There
is no upgrade path — an unknown scope satisfies nothing and is satisfied by nothing, in
both directions, so a typo cannot outrank the floor.

`produces_for_human` is checked on the way out like anything else, and no later phase will
ask for it.

## What "delivered" means

The check runs over **the delivered commit**, in a throwaway checkout cut from the
repository — never over the working tree.

The failure this closes is not sabotage. It is incoherence: an uncommitted file, a local
`.env`, a stale build artifact, a test edited and never committed. A tree that passes and a
delivery that does not.

Two measured cases shaped the rule, and both were phases that were **paid for** and proved
nothing:

- A phase that committed nothing used to pass, because an empty commit resolved to the
  repository's own `HEAD` and the command ran over whatever was already there.
- A phase that hands back its base also used to pass, because the base exists and resolves
  — so the check ran over the code the phase was *given*, which was already green.

Both now fail. Pass `--base` and a delivery equal to it is no delivery.

## The loop's floor

A repeating phase declares what it converges on. The verdict on a round belongs to the
model — no exit code tells "this round fixed something" from "this round traded one failure
for another", and that distinction is what separates a loop that converges from one that
burns its ceiling. What the model may not do is *leave*: `converges_on` names what has to be
passing, and the rounds are counted in the ledger rather than by the model.

## The ledger

One file, outside every checkout, one JSON object per line.

```
$XDG_DATA_HOME/luna/ledger.jsonl
```

```json
{"at":"2026-09-02T14:31:02Z","run":"MAX-2","project":"github.com/me/app","phase":"forge",
 "event":"check","artifact":"ci_green","verdict":"failed","scope":"full","exit":1,"round":2}
```

**Where a run stands is its most recent line** — read by tailing, not by folding. Luna
decides no transitions, so it has no state to reconstruct, only a position to report. What
it *did* is every line for that run, in order, which is `luna trail`: the phases it walked,
each check with its verdict and scope, the gates, the blocks, the notes.

**A phase can also record what it found out about the project it is working in** — which
command is this repository's own gate, how it bootstraps — as a `discovery`, with the file
it read to conclude it. That is recorded and never consulted: the command still arrives in
the contract on every call. A gate Luna remembered would be project configuration Luna
owns, and owning that is what the per-project store was removed for. What the record buys
is a reader who can see *why* a later check ran `pnpm check` rather than guessing.

Each line is self-contained: reading one never requires reading the ones before it. That is
what lets a concurrent fleet append with **no lock** — a write under 4 KB to a file opened
`O_APPEND` lands whole. A line that would exceed that is refused rather than truncated,
because a torn line is the one failure this format cannot recover from. Measured: eight
processes writing 100 lines each, and host and sandbox writing simultaneously, produced no
corrupt line.

Nothing is written into a checkout — no state file, no anchor, no ignore entry. The ledger
knows where the worktree is, rather than the worktree knowing where the ledger is, so a
record outlives the tree it describes.

## Durability, and why it is checked every time

Luna does not build the environment it runs in, so it cannot know from the inside whether
`$HOME` is a tmpfs a sandbox handed it. Under such a sandbox a ledger write succeeds,
reports success, and evaporates — and the write and the read agree with each other and with
nobody else.

So before its first write, Luna asks the kernel: `statfs` on the ledger's directory,
compared against `TMPFS_MAGIC`. In memory, it refuses, and the refusal says what to do.

```
the ledger is not on durable storage: ~/.local/share/luna is in memory…
  For ai-jail, that is `rw_maps` in the config, or `--rw-map ~/.local/share/luna`
```

Measured inside a real `ai-jail`: refused without the map, and with the map a line written
from inside the sandbox landed in the host's ledger beside one written outside it.

## Autonomy

Three named modes: `manual` (the default), `semi`, `auto`. A gate declares which mode
clears it, and the mode can move mid-run — a gate already open keeps whoever opened it,
because changing the mode while a question is on somebody's screen would rewrite who
answered it.

**Every mode still blocks on missing information, including `auto`.** The difference
between running without asking and running without thinking is the whole value of an
unattended fleet. A block carries three things: the question, where the answer was looked
for and what each source failed to say, and what would unblock it. The middle one is not
optional — it is what separates a real block from an unread file.

## The commands

```sh
luna check --contract - [--commit <sha>] [--base <sha>] [--round N]   # prove and record
luna contract lint <file|->                                            # static, cheap
luna record --run <id> --event <kind> …                                # what Luna did not verify
luna state [--run <id>]                                                # the most recent line
luna trail [<id>]                                                      # the whole story of one run
luna report [--here] [--open] [--project <r>] [--since 12h]             # every run, blocked first
luna version                                                           # what this build is
```

With no `--run`, the branch answers: a checkout on `luna/<run>` knows which run it
is. The branch is the authority because it travels with the work, where a directory can be
moved or made by hand.

`report` is the listing — which runs exist, and where each one stands. `--here` narrows it
to the repository you are standing in, and `--project` to one you are not. A run is listed
under **every** repository its lines name, not only the latest: the ledger has runs whose
first line names the checkout a batch seeded them from and whose later lines name the
repository they really worked in, and either half alone hides the run from somebody with a
right to see it.

## Layout

```
src/
  cmd/luna/          the binary: six verbs and an exit code
  internal/contract/ what a phase owes and how each debt is proven
  internal/verify/   running the checks over the delivered commit
  internal/ledger/   the record, its durability guard, and autonomy
  internal/cli/      the command surface
docs/                four files
```

No dependencies. `go.mod` is three lines, and the TOML subset the contract needs is a few
dozen lines of scanner — a contract format that pulls in a parser pulls in a supply chain.
