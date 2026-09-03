# Contributing

This is the normative home of the **branch, merge, commit and tag** flow. The `README.md` and
[`AGENTS.md`](../AGENTS.md) point here instead of repeating — single source.

## Before opening an issue or PR

Read [`docs/decisions.md`](decisions.md). Every decision is recorded with the alternative that was
rejected and the reason. Proposals that reopen a decision without a new argument will be
closed with a pointer to the corresponding decision.

If the change touches a permanent rule of the domain, also check
[`docs/invariants.md`](invariants.md).

## Project state

Luna is a verifier an agent calls at a phase boundary: a contract on stdin, checks run over
the delivered commit, one line per verdict in a durable ledger. It stopped being an
orchestrator in September 2026 — [`decisions.md`](decisions.md) says why. It remains
pre-release: design critique and evidence from real cycles are especially useful,
particularly from people who have operated agents unattended and seen a failure mode the
contract does not cover.

Two things to know before writing a test:

- **Give it its own ledger.** A test that leaves `XDG_DATA_HOME` alone writes into the
  developer's real record. The CLI takes the path through `cli.Env`, so a test names one.
- **`t.TempDir()` is on tmpfs here**, and the durability guard refuses it — correctly. A
  test that needs durable storage uses the helper that finds some, or skips.

## Git-flow

- **Default branch:** `main`. There is no `develop` — the project is too small to
  justify two integration lines.
- **Every change comes out of a branch off `main`** and comes back through a PR. Name it by
  type and subject: `feat/stage-contract`, `fix/handoff-snapshot`,
  `docs/adr-numbering`.
- **PRs target `main`.**
- **Release tags are created on `main`**, in SemVer (`v0.1.0`). While there is no
  release, there is no tag.

## Flow

1. Open an issue describing the problem before writing code.
2. Work on a branch off `main`.
3. Conventional Commits; the body explains the why.
4. `make ci` green before opening the PR.

## Commits

[Conventional Commits](https://www.conventionalcommits.org/en/), in English.

- The **title** says what changes, in the imperative: `feat(fsm): validate produces on stage exit`.
- The **body explains the why**, not the what — the diff already shows the what. If the change
  exists because of a recorded decision, cite that decision.
- Types in use: `feat`, `fix`, `docs`, `refactor`, `test`, `chore`.

## Pull Request

- Describe the **problem** the PR solves before the solution.
- Point to the related decision or invariant, if any.
- Explicitly highlight any compatibility break.
- `make ci` green. Remote CI runs exactly the same `make ci-check`.

## The quality pipeline

Luna orchestrates agents that write code. That premise sets the bar for the code
Luna itself is made of: it has to hold up without a human reading every line, which
means the metrics do the reviewing.

Both `ci` and `ci-check` announce each step with a blue `▸` line, mark the result,
and close with a summary. They **stop at the first failure** — a linter answering
about a tree the formatter already rejected reports noise — but the summary still
prints, naming what failed and what never got a turn:

```
▸ fmt-check ──────────────────────────────────────────────────────────
✓ fmt-check (0s)

▸ lint ───────────────────────────────────────────────────────────────
src/internal/verify/verify.go:103:1: cyclomatic complexity 12 (gocyclo)
✗ lint (1s)

ci-check — 5 steps, 1s ───────────────────────────────────────────────
  ✓ fmt-check      0s
  ✗ lint           1s
  · lint-docs      not run
  · lint-language  not run
  · cover          not run
failed at lint — 3 step(s) not run
```

Colour is dropped when stdout is not a terminal, so CI logs stay readable.

**`ci-check` — the gate. Fails the build.**

| Step | Catches |
|---|---|
| `fmt-check` | formatting drift (gofumpt, stricter than gofmt) |
| `lint` | the linter set in [`.golangci.yaml`](../.golangci.yaml) — correctness, security, complexity, dependency direction |
| `lint-docs` | the shape of the docs — links and headings, never their truth |
| `lint-language` | Portuguese left in a project written in English |
| `cover` | coverage below **95%** |

**Runs in remote CI, outside the local gate.**

| Step | Why it is not in `ci-check` |
|---|---|
| `race` | needs CGO and roughly doubles the test time |
| `vuln` | reaches the network; reachability-filtered, so a finding is real |

**Reported, never gated.** These answer *how healthy is the code*, which is a
judgement call rather than a threshold — a number nobody agreed on should not block
a merge.

| Target | Reports |
|---|---|
| `cyclo` | the most cyclomatically complex functions |
| `deadcode` | unreachable functions — including the ones reachable only from tests |
| `crap` | CRAP index — complexity weighted by coverage |
| `mutation` | whether the suite would catch an injected bug |

`cyclo` and `deadcode` also run in remote CI, where they cannot fail the build but
stay visible. That is not belt-and-braces: three unreachable methods lived for two
weeks because "reported, never gated" had nothing reporting either.

`deadcode` is deliberately **not** in `ci-check`. A function written before its
caller is ordinary mid-TDD, and a gate that goes red in the middle of the cycle is
a gate people learn to ignore.

**Mutation testing measures test strength**, which is the one thing coverage cannot:
100% coverage with weak assertions passes `cover` and fails `mutation`. `gremlins`
changes one operator — `>=` to `>`, a negation, an arithmetic base — reruns the
suite, and reports every mutant that still passed. A survivor is a line no
assertion actually pins.

```sh
make mutation                                    # the tree: ~50s
make mutation MUTATE=./src/internal/contract/    # one package: ~2s
```

Two real holes came out of the first run, and both are now regression tests:
`Scope.Satisfies` never checked that a scope satisfies *itself* (`>=` mutated to
`>` survived), and the ledger's line-size guard was never tested at the boundary
(`>` mutated to `>=` survived) — the guard standing between this format and its one
unrecoverable failure.

It stays out of every pipeline. Each mutant is a full test run, so the cost scales
with the suite, and a survivor is a prompt to think rather than a build to fail.
The `--timeout-coefficient` in the target is not decoration: at the default, every
mutant here timed out and the score read `0.00%` — a number that looks like a
verdict and is a stopwatch.

### Two thresholds worth understanding

**Coverage at 95%, not 100%.** The floor exists to catch untested behaviour, not to
be gamed. Chasing the last few percent produces tests that assert nothing, which is
worse than the gap they close.

**Complexity at 10.** Functions here are meant to be 4–20 lines
([`AGENTS.md`](../AGENTS.md)); anything past 10 branches is a function that should
have been two. The ceiling is enforced by `lint`, and `cyclo` reports the ranking.

### What is not here yet, and when it arrives

Four checks have no target in this project today. They enter when the thing they
guard exists — not before, because a gate that always passes empty teaches people
to ignore the pipeline:

- **gitleaks** — when the first secret or `.env` appears;
- **depguard rules beyond the current set** — when another architecture boundary needs
  compiler-like enforcement;
- **Trivy** — when there is a Dockerfile;
- **Spectral/vacuum** — when there is an API specification to validate.

## Documentation

Documentation is part of the delivery, it does not come afterwards. There are four files
and each answers one question:

| File | Question |
|---|---|
| [`architecture.md`](architecture.md) | how does it work today? |
| [`invariants.md`](invariants.md) | what always holds? |
| [`decisions.md`](decisions.md) | what was chosen, and what was rejected? |
| [`lessons.md`](lessons.md) | what did building it teach? |

Do not add a fifth without a reason that survives the question *"which of the four should
have held this instead?"* — the previous suite had ten layers and 95,000 words for 14,000
lines of code, and the shape is what let it drift out of sync with the code while every
structural check stayed green.

A statement in `architecture.md` that the code contradicts is a **bug**, fixed like one.
`make lint-docs` checks links and shape; it cannot check truth, so that part is on the
person changing the code.

## Conventions

The whole project is written in English — code, tests, documentation, commits. The `README.md` opens with the
problem, not the stack. The code conventions are in [`AGENTS.md`](../AGENTS.md).
