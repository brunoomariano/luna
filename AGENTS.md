# Instructions for agents

You are working **on Luna's code**, not being orchestrated by it.

## What this project is

A tool an agent calls at a phase boundary to prove what was delivered — by running a
command over the delivered commit, rather than by believing a claim — and to record what
happened in one durable ledger.

It does not run agents, open worktrees, build sandboxes or decide what happens next. It did
all of that once; [`docs/decisions.md`](docs/decisions.md) says why it stopped.

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
- **Do not add a fifth file** without an answer to *"which of the four should have held
  this instead?"* `CHANGELOG.md`, `CONTRIBUTING.md` and `DESIGN.md` are conventional
  root-of-docs files rather than a layer of the suite — none answers a question about how
  Luna works.

Drawing anything — a diagram, a plate, a new image for the README — starts at
[`docs/DESIGN.md`](docs/DESIGN.md), which holds the palette, the line weights and the one
rule that is easy to get wrong: GitHub strips CSS from SVG, so every value goes on the
element.

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
  cmd/luna/          the binary: five verbs and an exit code
  internal/contract/ what a phase owes and how each debt is proven — no execution
  internal/verify/   running the checks over the delivered commit
  internal/ledger/   the record, its durability guard, and autonomy
  internal/cli/      the command surface
docs/                the four files above
scripts/             lint helpers
```

`internal/` is an import barrier the Go compiler enforces. Do not move anything out of it
without recording why in `decisions.md`.

**Luna has no dependencies.** `go.mod` is three lines, and it stays that way: adding one to
save a few dozen lines of parser buys a supply chain.

## Code conventions

- **The whole project is in English** — code, tests, comments, docs, errors, CLI output,
  and domain values that appear in config (`forge`, `blocked`, `awaiting_gate`). Mixed
  languages break search: `rg "etapa"` and `rg "phase"` find different halves of one
  concept.
- **Specific, searchable names.** Prefer the ones with few hits in `rg`. Avoid `data`,
  `handler`, `Manager` when a precise option exists.
- **Explicit typing.** No `interface{}`/`any` where a concrete type will do.
- **Errors carry the invalid value and the expected one.** A message saying neither costs
  a debugging session. A refusal a person can act on says what to do next.
- **Functions ideally 4–20 lines**; longer is fine when splitting would be artificial.
  **Files under 500 lines.** **Early returns** over nesting; at most 2 levels.

### Comments

Comment to say **why**, and only when the reason is not visible in the code. The bar:

- **Write one** for a constraint the code cannot show — a limit imposed from outside (a
  4 KB atomic write, a kernel magic number), an ordering that looks arbitrary and is not, a
  line that exists because of a measurement.
- **Do not write one** that restates the next line, names what a function obviously does,
  or explains that your change is correct. That last one is you talking to the reviewer,
  and it is noise the moment the change merges.
- **Reference a measurement or a SHA**, not a document number.
- **Keep existing comments** through refactors unless they became false.

## Tests

- Every new function has a test; every bug fix has a regression test.
- Test **observable behavior**, not implementation.
- **Use a real boundary where the boundary is the point.** Every measured defect in
  `verify` is about what git answers for a commit that is missing or equal to its base — a
  fake with no git has nothing to get wrong, and three bugs in a row shipped behind exactly
  that kind of fake. The tests build real repositories.
- **`t.TempDir()` is on tmpfs here.** `/tmp` is tmpfs on this machine and on any systemd
  default, and the durability guard refuses it — correctly. Tests that need durable storage
  use a helper that finds some, or skip.
- **Observe a dependency being used, not being wired.** Asserting a field is set has passed
  twice while the code path that should call it never did.
- An invariant in `docs/invariants.md` names the tests that hold it up. **Do not call a
  piece done while its invariant's coverage is missing.**

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
- **Do not make Luna the parent process again.** It ran agents, opened worktrees and built
  sandboxes for a year, and went unused because two things that both want to be the parent
  do not compose. The argument is in `decisions.md`; bring a new one.
- **Do not write into a checkout.** No state file, no anchor, no ignore entry. The contract
  arrives on stdin and the record lives outside every repository.
- **Do not rewrite a ledger line.** Append-only, and the last line is the state.
- **Do not let a write proceed without proving the ledger is durable.** A record that
  reports success and evaporates is the worst failure available, because both sides agree.
- **Do not claim more than the check proved.** Evidence carries scope, and scope never
  upgrades.
- **Do not add ceremony.** A document, a layer or a flag that nothing executes against is
  cost without a guarantee.
