# Contributing

This is the normative home of the **branch, merge, commit and tag** flow. The `README.md` and
[`AGENTS.md`](../AGENTS.md) point here instead of repeating — single source.

## Before opening an issue or PR

Read [`docs/ADRs/`](ADRs/). Every decision is recorded with the alternative that was
rejected and the reason. Proposals that reopen a decision without a new argument will be
closed with a pointer to the corresponding ADR.

If the change touches a permanent rule of the domain, also check
[`docs/invariants/`](invariants/).

## Project state

Engine under construction. The architecture is settled and recorded in
[`docs/ADRs/`](ADRs/); the core has the contract's static and entry checks and stage
selection. Design critique is still the most useful contribution — especially if you
have operated a fleet of agents and seen a failure mode the design does not cover.

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
  exists because of a recorded decision, cite the ADR.
- Types in use: `feat`, `fix`, `docs`, `refactor`, `test`, `chore`.

## Pull Request

- Describe the **problem** the PR solves before the solution.
- Point to the related ADR, invariant or PRD, if any.
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
ci-check — 4 steps, 1s
──────────────────────────────────
  ✓ fmt-check      0s
  ✗ lint           1s
  · lint-docs      not run
  · cover          not run
failed at lint — 2 step(s) not run
```

Colour is dropped when stdout is not a terminal, so CI logs stay readable.

**`ci-check` — the gate. Fails the build.**

| Step | Catches |
|---|---|
| `fmt-check` | formatting drift (gofumpt, stricter than gofmt) |
| `lint` | the linter set in [`.golangci.yaml`](../.golangci.yaml) — correctness, security, complexity, dependency direction |
| `lint-docs` | the shape of this suite (see [`README.md`](README.md)) |
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
| `deadcode` | unreachable functions |
| `crap` | CRAP index — complexity weighted by coverage |
| `mutation` | whether the suite would catch an injected bug |

Mutation testing is the one that measures test **strength** rather than test
quantity: 100% coverage with weak assertions passes `cover` and fails `mutation`.
It is slow, so it stays manual — run it before a release, not on every commit.

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
- **depguard rules beyond the current two** — when the first external dependency
  arrives (`go.mod` has none today);
- **Trivy** — when there is a Dockerfile;
- **Spectral/vacuum** — when there is an API spec. ADR-0016 puts a visual interface
  in phase two, so this is furthest out.

## Documentation

Documentation is part of the delivery, it does not come afterwards. Before writing any
document, read the contract in [`docs/README.md`](README.md) to know which layer
it belongs to. `make lint-docs` validates the form and fails CI like a code lint.

## Conventions

The whole project is written in English — code, tests, documentation, commits. The `README.md` opens with the
problem, not the stack. The code conventions are in [`AGENTS.md`](../AGENTS.md).
