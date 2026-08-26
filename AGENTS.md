# Instructions for agents

You are working **on Luna's code**, not being orchestrated by it.

## What this project is

A state machine that drives AI agents through a workflow, taking the flow-control decision
away from the model and putting it in code — and then verifying the result by running a
tool rather than by believing a claim.

Read [`docs/architecture.md`](docs/architecture.md) before proposing any structural change.
The five rules that always hold are in [`docs/invariants.md`](docs/invariants.md).

## Documentation

Four files, and each answers one question:

| File | Question |
|---|---|
| [`docs/architecture.md`](docs/architecture.md) | how does it work today? |
| [`docs/invariants.md`](docs/invariants.md) | what always holds? |
| [`docs/decisions.md`](docs/decisions.md) | what was chosen, and what was rejected? |
| [`docs/lessons.md`](docs/lessons.md) | what did building it teach? |

Three rules about them:

- **A statement the code contradicts is a bug**, fixed like one. `make lint-docs` checks
  links and shape; it cannot check truth.
- **`decisions.md` is living.** Revising a decision means editing it, not appending a new
  record. What it must keep is the **rejected alternative** — that is why the file exists.
  If you are about to propose something listed as rejected, bring an argument the record
  does not already answer.
- **Do not add a fifth file** without an answer to *"which of the four should have held
  this instead?"* The previous suite had ten layers and 95,000 words for 14,000 lines of
  code, and went out of sync with the code while every structural check stayed green.

## Standard workflow

- Read the local context before changing files.
- Preserve existing changes in the worktree. Do not revert someone else's work without an
  explicit request.
- Make small, cohesive changes, limited to what was asked.
- Validate through the Makefile (`make help` lists the targets); `make ci` before a PR.
- When done, report which checks you ran and any loose ends.

## Layout

```
src/
  cmd/luna/          CLI entry point
  internal/fsm/      the engine: stages, transitions, contract, fingerprint
  internal/store/    append-only log, replay, blob store
  internal/agent/    calling an agent: one subprocess, prompt in, usage out
  internal/node/     running a stage: worktree, sandbox, socket, verification
  internal/cli/      commands
  internal/lead/     conducting a task, and the model that judges a gate
  stock/             defaults: flows, roles, profiles (embedded TOML)
docs/                four files, above
skills/              how to *use* Luna, for an agent driving it from outside
scripts/             lint helpers
```

`internal/` is an import barrier the Go compiler enforces. Do not move anything out of it
without recording why in `decisions.md`.

## Code conventions

- **The whole project is in English** — code, tests, comments, docs, errors, CLI output,
  commit messages, and domain values that appear in config or state (`feature`, `blocked`,
  `awaiting_gate`). No bilingualism, no "translate later". Mixed languages also break
  search: `rg "etapa"` and `rg "stage"` find different halves of one concept.
- **Specific, searchable names.** Prefer the ones with few hits in `rg`. Avoid `data`,
  `handler`, `Manager` when a precise option exists. Domain terms (`stage`, `role`,
  `handoff`, `gate`) are the natural name of the concept.
- **Explicit typing.** No `interface{}`/`any` where a concrete type will do.
- **Errors carry the invalid value and the expected one.** A message saying neither costs
  a debugging session.
- **Functions ideally 4–20 lines**; longer is fine when splitting would be artificial.
  **Files under 500 lines.** **Early returns** over nesting; at most 2 levels.

### Comments

Comment to say **why**, and only when the reason is not visible in the code. The bar:

- **Write one** for a constraint the code cannot show — a limit imposed from outside (a
  108-byte socket path, a harness that only accepts one spelling of a flag), an ordering
  that looks arbitrary and is not, a line that exists because of a measurement.
- **Do not write one** that restates the next line, names what a function obviously does,
  or explains that your change is correct. That last one is you talking to the reviewer,
  and it is noise the moment the change merges.
- **Reference a measurement or a SHA**, not a document number. A comment pointing at
  `ADR-0042` sends the reader to a file that no longer exists; a comment saying *"all four
  harnesses can gate — the earlier grep looked for one vendor's spelling"* carries the
  fact itself.
- **Keep existing comments** through refactors unless they became false.

## Tests

- Every new function has a test; every bug fix has a regression test.
- Test **observable behavior**, not implementation.
- Fake an external boundary (agent process, filesystem, network) with a named fake, not an
  inline stub.
- An invariant in `docs/invariants.md` names the tests that hold it up. **Do not call a
  piece done while its invariant's coverage is missing** — that is the line between "the
  code does" and "the code guarantees".

## Makefile

`make help` lists everything. What matters:

- `make ci` — fixes what it can (`fmt`), then verifies. **Run before a PR.**
- `make ci-check` — verify only, writes nothing. This is what remote CI runs.
- `make race`, `make vuln` — remote CI.
- `make cyclo`, `make crap`, `make mutation`, `make deadcode` — reported, never gated.

The split between `ci` and `ci-check` is not cosmetic: a CI step that reformats code hides
exactly what it should be failing on.

## What not to do

- **Do not move flow control into the model.** It is the premise of the project.
- **Do not write state with `UPDATE`.** The store is append-only; the history is the audit.
- **Do not create a stage without a contract.** `requires`/`produces` is what prevents an
  incomplete handoff.
- **Do not claim more than the check proved.** Evidence carries scope, and scope never
  upgrades.
- **Do not add ceremony.** A document, a layer or a stage that nothing executes against is
  cost without a guarantee — the reason this file is a third of its former length.
