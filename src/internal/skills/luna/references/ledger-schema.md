# The ledger — shape and reading

**Nothing is written into the repository.** No `.luna/`, no state file, no ignore
entry to keep in step. A task's state lives in this ledger, outside every
checkout.

```
$XDG_DATA_HOME/luna/ledger.jsonl     (or ~/.local/share/luna/ledger.jsonl)
```

> **Why it left the worktree.** A state file inside the tree died with the tree:
> removing the worktree lost the record before anything could be synthesised —
> a real problem, measured, that cost several tasks their retrospective. The
> ledger outlives the work, and the window with no state — between discovery and
> setup, before a worktree exists — closes too: the ledger is global, so it
> exists first.

## The shape

One JSON line per event, append-only. Each line is **self-contained**: reading one
never requires reading the ones before it.

```json
{"at":"2026-09-03T14:31:02Z","run":"MAX-2","project":"github.com/me/app",
 "phase":"forge","event":"check","artifact":"ci_green","verdict":"failed",
 "scope":"full","exit":1,"round":2,"worktree":"/repos/wt-app-MAX-2"}
```

**Where a task stands = the last line for that `run`.** Read with `tail`, not by
rebuilding from the start.

### Closed enums

- `event`: `phase` · `check` · `gate` · `block` · `unblock` · `autonomy` · `discovery`
- `status`: `running` · `awaiting_gate` · `awaiting_resume` · `blocked` · `done` · `abandoned`
- `verdict`: `passed` · `failed`
- `scope`: `full` · `targeted` · `existence` · `human`
- `autonomy`: `manual` · `semi` · `auto`

A value outside the list is **refused**, not written: whoever reads an unknown one
cannot tell a new fact from a typo.

### The fields

| Field | Carries |
|---|---|
| `at` | when, in UTC. Set on append. |
| `run` | which task. The only field a reader has to group by. |
| `project` | the repository, normalised from the git remote. |
| `phase`, `event`, `status` | what kind of fact this is, and where the run stands after it. |
| `worktree` | where the work was checked out. The ledger knows where the worktree is, not the reverse. |
| `round` | which round of the phase's convergent loop — not a count of check attempts. |
| `artifact`, `verdict`, `scope`, `command`, `exit` | one check's result. |
| `gate`, `answer` | a gate being settled. |
| `question`, `looked`, `needs` | a block. `looked` is a list. |
| `found`, `where` | a discovery. `where` is its source. |
| `note` | free text for what the fields above do not cover. |
| `simulated` | this line came from a dry run. |

**`round` is not a retry counter.** The loop's ceilings are read off it, and a
retry numbered as a round makes a phase look like it iterated when it did not.

**`simulated` exists so a dry run cannot be mistaken for a result.** A simulation
that reads like a result is a lie with the truth beside it, and the reader only
ever sees one of the two.

## Reading it

```sh
luna state --run MAX-2      # the last line for that run
luna trail MAX-2            # every line, in order
luna runs --open            # every unfinished run, anywhere
```

Each takes `--json`, in any argument order.

A line that cannot be parsed is **reported rather than skipped**. Skipping is how
a record quietly stops being the record: the reader would answer confidently from
whatever remained readable.

## Resuming

A task resumes from what was recorded, not from what anyone remembers:

```sh
luna state --run MAX-2     # where it stopped
luna trail MAX-2           # how it got there
```

A run whose last line is `blocked` carries the question, where it was already
looked for, and what unblocks it. A run whose last line is `awaiting_gate` is
waiting on a person.

## The line has a ceiling

One record is bounded at just under 4 KB, because a write under `PIPE_BUF` to a
file opened with `O_APPEND` lands whole — which is what lets several processes
share this file with no lock.

A line over the ceiling is **refused rather than truncated**: a torn line is the
one failure this format cannot recover from. Whatever produces a long detail has
to shorten it at the source.

## What does NOT go in the ledger

- The contract. It arrives on stdin and Luna keeps none.
- A project's gate, for reuse. A `discovery` is recorded and never consulted.
- The full output of a command. The detail is bounded; a long suite writes its
  own log somewhere durable and the contract calls that script.
- Anything inside a checkout. The ledger is the only thing Luna writes, and it
  lives outside every repository.
