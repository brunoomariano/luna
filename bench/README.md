# Benchmark

One case, several variants, the same words to each. It exists because every claim
about a flow change is otherwise an opinion — and the sharpest point in the
article that provoked the August 2026 reset is that a tool with no measurement is
an aesthetic preference.

## What it measures

Two columns, kept apart on purpose:

| Column | Question |
|---|---|
| **product** | did the delivered code pass the acceptance criteria? |
| **flow** | did the run close cleanly, or stop somewhere? |

They are separate because a run can be 8/8 on the first and blocked on the second,
and for an unattended fleet that is a failure. Collapsing them into one score
hides exactly the difference worth knowing.

## The cases

Two, each a directory holding a statement, a scorer and the flows it is a
candidate for. `bench/run.sh` runs one; `BENCH_CASE=case-bug bench/run.sh` runs
the other.

Both start from the same `tally.sh`. That is the honest arrangement rather than a
saving: one baseline genuinely admits both tasks, and a benchmark whose cases
start from different code cannot say whether a difference came from the flow or
from the starting point.

| Case | Task | Candidate flows | Seed scores |
|---|---|---|---|
| `case` | add an `--avg` flag — a feature | `solo`, `luna:chore`, `luna:full` | 4/8 |
| `case-bug` | a leading zero is read as octal — a defect with a reproduction | `solo`, `luna:fix`, `luna:full` | 5/9 |

**Comparisons are within a case.** A row from one does not belong in a table with
a row from the other, and the candidate list is declared by the case rather than
by the runner — because a flow measured against a task class it was not built for
produces a real number that means nothing. Measured: run through the feature case,
`fix` spent $2.88 across 48 turns in `diagnose` looking for the root cause of
something that was not broken, 78% of its whole bill, and still closed clean at
8/8.

## The feature case

`tally.sh` sums the numbers given to it. The task is to add `--avg`, keeping the
summing behaviour and handling an empty list. `case/score.sh` grades the result
against eight objective assertions — an input and an exact expected line each,
never a judgement, because a benchmark whose product score is a judgement measures
the judge.

One of the eight is a trap, and it is there because a real cycle failed it:
`tally.sh 1 --avg 2` must print `1`. A delivery that reads the flag once before
the loop passes every other case and gets this wrong. The seed scores 4/8.

## The bug case

The same `tally.sh` has a real defect: bash arithmetic reads a leading zero as
octal, so `./tally.sh 1 08 3` prints `1`, writes a diagnostic to stderr, and
**exits 0**. The wrongness is invisible to a caller reading the exit code, which
is what makes the reproduction the specification and the task a `fix`.

`010` is the nastier variant — it prints `8`, silently, with nothing on stderr,
because octal 010 is 8. The scorer checks stdout, stderr and the exit status
together for that reason: the baseline already prints *a* number.

Four of the nine assertions are the defect, four are what must not regress, and
one is shellcheck. The seed scores 5/9.

## Running it

```sh
make build
bench/run.sh                    # every variant
bench/run.sh solo luna:fix      # two of them
```

It spends real money and needs a real agent, so it is **never** part of `make ci`.

Every variant is given a ceiling — `BENCH_BUDGET_USD`, $12 by default — and the
first run without one is why. The `full` flow found a real contract violation at
`review`, sent the work back exactly as designed, and went round again: build
$10.93, refactor $8.37, verify $7.23, review $5.80, **$35.07 and climbing**, on a
case whose bare-agent baseline is $0.57. Nothing was broken. A send-back loop is
the mechanism working, and a mechanism that works without a ceiling is how an
unattended night bills like that.

## What to compare

Not Luna against a bare agent. That answer is known, it will not move, and it is
the wrong question: a strong model in a simple loop delivers this case well, and
orchestration buys containment, evidence and a log rather than a better diff. The
solo row stays as the baseline so the multiple remains visible and honest.

The comparisons worth running are:

- **flow against flow, among the flows this case is a candidate for** — what does
  `full` buy over `chore` on the same words?
- **this build against the last** — did a change to the flow move cost, time or
  the product score, and in which direction?

A change that improves nothing on all three is a change with no argument for it.

**Only candidate flows.** `fix` is not in the default set, and leaving it out is
the correction to a measurement rather than a gap. This case is a feature; running
it through the flow built for a bug with a reproduction measured the mismatch and
not the flow — `diagnose` spent $2.88 across 48 turns looking for the root cause of
something that was not broken, 78% of that variant's entire bill, and the flow
still closed clean at 8/8. Reporting that number in the same table invites the
reading that `fix` is expensive, when what is expensive is asking it the wrong
question. A bug case belongs here and does not exist yet; `fix` returns with it.

## Measured so far

Against the same words, on one machine, with Claude Opus 5:

**`case` — the feature**

| variant | product | flow | seconds | usd | tokens |
|---|---|---|---|---|---|
| `luna:chore` | 8/8 | clean | 110 | $0.6681 | 424k |

For reference, a bare agent on this case measured 100/100 in 100s at $0.5657 — so
the lean flow bought a full evidence trail, a sandbox and a log for about a fifth
more.

**`case-bug` — the defect**

| variant | product | flow | seconds | usd | tokens |
|---|---|---|---|---|---|
| `luna:fix` | 9/9 | clean | 491 | $2.2621 | 1.25M |

`diagnose` was $0.8847 over 19 turns and `build` $1.3773 over 13. That first number
is the one worth keeping: the same stage, given the feature case it was not built
for, cost $2.8826 over 48 turns. **The stage is not expensive; asking it for the
root cause of something that is not broken is** — 3.3×, measured both ways.

Rows are added as they are run; a row nobody has run is not in the table.
