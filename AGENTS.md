# Instructions for agents

You are working **on Luna's code**, not being orchestrated by it.

## Instruction precedence

- This `AGENTS.md` takes precedence over global instructions inside this repository.
- Area-specific documentation takes precedence over this file on the same subject.
  The documentation suite is governed by [`docs/README.md`](docs/README.md).
- On a genuine conflict between rules, stop and ask before changing code.

## What this project is

A state machine that drives AI agents through a workflow, taking the flow-control
decision away from the model and putting it in code. Read
[`docs/architecture/overview.md`](docs/architecture/overview.md) before proposing any
structural change.

## Current state

**Engine under construction.** The decisions are settled and recorded in
[`docs/ADRs/`](docs/ADRs/); the core started with the contract's static check
(`src/internal/fsm/`).

The stage contract is the source of truth:
[`docs/architecture/stages.md`](docs/architecture/stages.md) describes the 14 stages and
`DefaultFlow()` implements them. A divergence between the two is a bug —
`TestDefaultFlowMatchesDocumentedStages` exists to catch it.

## Standard workflow

- Read the local context before changing files.
- Preserve existing changes in the worktree. Do not revert someone else's work without
  an explicit request.
- Make small, cohesive changes, limited to what was asked.
- Use the Makefile targets as the primary validation interface (`make help` lists them).
- When done, report which checks you ran and any relevant loose ends.

## Before changing the architecture

[`docs/ADRs/`](docs/ADRs/) records every decision **along with the rejected
alternative**. If you are about to propose something already rejected, bring a new
argument — the record exists so discussions are not reopened without one. An ADR is
**immutable**: a revised decision becomes a new ADR, never an edit to the old one.

The rules that **always** hold, regardless of implementation, live in
[`docs/invariants/`](docs/invariants/). Violating an invariant is not a code bug — it is
Luna ceasing to be Luna.

## Layout

```
src/                 everything that is application code
  cmd/luna/          CLI entry point
  internal/fsm/      the engine: stages, transitions, contract
  internal/store/    append-only state and content store
  internal/node/     running a node (the agent call)
  stock/             defaults: stages, roles, profiles, skills
docs/                the documentation suite — its contract is docs/README.md
scripts/             development utilities (lint-docs)
bin/                 built binaries; not versioned beyond .gitkeep
config/              example user configuration
```

`internal/` is an import barrier enforced by the Go compiler: the engine is not
importable from outside the module. Do not move anything out of `internal/` without a
decision recorded in an ADR.

## Code conventions

- **Language — the whole project is in English.** No exceptions, no bilingualism, no
  "we'll translate later". This covers:
  - **code** — package, type, function, method, variable, constant, field;
  - **tests** — test name, helper, fake, failure message;
  - **comments and doc comments**;
  - **documentation** — the entire `docs/` suite, `README.md`, this file, and the file
    name slug (`alerts-0001-suppression.md`);
  - **messages** — errors, logs, CLI output, help text;
  - **commit messages** and PR descriptions;
  - **domain values** that appear in configuration or state (`feature`, `blocked`,
    `awaiting_gate`).

  The reason is reach: Luna is a distributable product, and a half-Portuguese project is
  unreadable to half of whoever arrives. Mixed languages also degrade search — `rg
  "etapa"` and `rg "stage"` find different halves of the same concept.

- **Specific, searchable names.** Prefer the ones that return few hits in `rg`. Avoid
  generic names like `data`, `handler`, `Manager` when a more precise option exists.
  Domain terms (`stage`, `role`, `handoff`, `gate`) are the natural name of the concept
  and should be used as such.
- **Explicit typing.** No `interface{}`/`any` where a concrete type will do.
- **Errors carry the invalid value and the expected one.** A message that says neither
  what arrived nor what was wanted costs a debugging session.
- **Functions ideally between 4 and 20 lines;** longer is fine when keeping the logic
  together reads better than splitting it artificially.
- **Files under 500 lines.** Split by responsibility.
- **Early returns** instead of nested `if`. At most 2 levels of indentation.
- **Comments explain why**, not what — the code already shows what. Keep existing
  comments; do not drop them in refactors. Reference an ADR or a SHA when a line exists
  because of a decision or an external constraint.

## Tests

- Every new function has a test; every bug fix has a regression test.
- Test **observable behaviour**, not the implementation.
- Fake an external boundary (agent process, filesystem, network) with a named fake, not
  an inline stub.
- Run them through the Makefile targets.

### Invariant acceptance criteria

Several invariants in [`docs/invariants/`](docs/invariants/) carry an **Acceptance
criteria** section — the list of tests without which that invariant is not implemented,
merely described.

**Do not call done a piece of the engine whose corresponding invariant has uncovered
acceptance criteria.** This is not a recommendation: it is what separates "the code
does" from "the code guarantees". A `reviewer` that merely *tends not to* edit does not
satisfy INV-core-7; a watchdog that exists but was never exercised does not satisfy
INV-core-8.

When implementing, start from the test the criterion describes. When reviewing, check
the criterion before the diff.

## Makefile interface

`make help` lists every target. The ones that matter:

- `make bootstrap` — prepares the environment (mise + dependencies). Idempotent.
- `make doctor` — checks the environment without installing anything.
- `make ci-check` — **verify only**: `fmt-check`, `lint`, `lint-docs`, `cover`. This is
  what remote CI runs, and it writes nothing.
- `make ci` — fixes what it can (`fmt`) and then verifies. **Run this before opening a
  PR.**
- `make lint-docs` — validates the **shape** of the docs suite; fails CI like a code
  linter. The contract it enforces is in [`docs/README.md`](docs/README.md).

The distinction between `ci` and `ci-check` is not cosmetic: a CI step that reformats
the code hides exactly what it should be failing on.

There is no `up`/`down`/`logs`/`clean_db`: Luna is a CLI with no services and no
development database. A no-op target would be ceremony.

## Documentation

The suite is governed by [`docs/README.md`](docs/README.md) — the contract stating which
layers exist, what each one answers, and what shape it follows. **Consult it before
creating or changing any document**, to know which layer it belongs to.

| Where | What |
|---|---|
| [`docs/architecture/`](docs/architecture/) | how the system is put together today |
| [`docs/glossary/`](docs/glossary/) | what each domain term means |
| [`docs/invariants/`](docs/invariants/) | rules that always hold |
| [`docs/ADRs/`](docs/ADRs/) | why we decided this way — immutable |
| [`docs/PRDs/`](docs/PRDs/) · [`docs/RFCs/`](docs/RFCs/) | expected behaviour · technical route |
| [`docs/references.md`](docs/references.md) | where the idea came from, and what was rejected |
| [`docs/CHANGELOG/`](docs/CHANGELOG/) | what changed between versions |

The root `README.md` opens with **the problem the project solves**, never with the
stack. Technical detail goes to `docs/`.

## Commits, PRs and tags

The git flow is normative in [`docs/CONTRIBUTING.md`](docs/CONTRIBUTING.md) — and only
there. In short: Conventional Commits, with a body explaining **why**.

## What not to do

- **Do not move flow control into the model.** It is the premise of the whole project.
- **Do not write state with `UPDATE`.** The store is append-only; the history is the
  audit trail.
- **Do not create a stage without a contract.** Every stage declares what it requires
  and what it produces — that is what prevents an incomplete handoff.
- **Do not edit an accepted ADR.** A revised decision becomes a new ADR; the old one
  only changes status. `make lint-docs` rejects the edit.
- **Do not duplicate content across doc layers.** A fact lives in one layer only; if you
  need to repeat it, link instead.
