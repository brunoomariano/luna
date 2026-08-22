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

## The case

`tally.sh` sums the numbers given to it. The task is to add `--avg`, keeping the
summing behaviour and handling an empty list. `case/score.sh` grades the result
against eight objective assertions — an input and an exact expected line each,
never a judgement, because a benchmark whose product score is a judgement measures
the judge.

One of the eight is a trap, and it is there because a real cycle failed it:
`tally.sh 1 --avg 2` must print `1`. A delivery that reads the flag once before
the loop passes every other case and gets this wrong. The seed scores 4/8.

## Running it

```sh
make build
bench/run.sh                    # every variant
bench/run.sh solo luna:fix      # two of them
```

It spends real money and needs a real agent, so it is **never** part of `make ci`.

## What to compare

Not Luna against a bare agent. That answer is known, it will not move, and it is
the wrong question: a strong model in a simple loop delivers this case well, and
orchestration buys containment, evidence and a log rather than a better diff. The
solo row stays as the baseline so the multiple remains visible and honest.

The comparisons worth running are:

- **flow against flow** — what does `full` buy over `fix` on the same case?
- **this build against the last** — did a change to the flow move cost, time or
  the product score, and in which direction?

A change that improves nothing on all three is a change with no argument for it.
