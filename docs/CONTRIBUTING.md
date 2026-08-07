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

In design. The architecture is settled; the code has not started. The most useful
contributions right now are **design critique** — especially if you have already operated a fleet
of agents and seen a failure mode the design does not cover.

## Git-flow

- **Default branch:** `master`. There is no `develop` — the project is too small to
  justify two integration lines.
- **Every change comes out of a branch off `master`** and comes back through a PR. Name it by
  type and subject: `feat/stage-contract`, `fix/handoff-snapshot`,
  `docs/adr-numbering`.
- **PRs target `master`.**
- **Release tags are created on `master`**, in SemVer (`v0.1.0`). While there is no
  release, there is no tag.

## Flow

1. Open an issue describing the problem before writing code.
2. Work on a branch off `master`.
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

## Documentation

Documentation is part of the delivery, it does not come afterwards. Before writing any
document, read the contract in [`docs/README.md`](README.md) to know which layer
it belongs to. `make lint-docs` validates the form and fails CI like a code lint.

## Conventions

The whole project is written in English — code, tests, documentation, commits. The `README.md` opens with the
problem, not the stack. The code conventions are in [`AGENTS.md`](../AGENTS.md).
