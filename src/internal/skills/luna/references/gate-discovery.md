# Discovering the project's gate

How to find **which command proves this repository is green** — without assuming
every project has `make` and a `ci` target. It is what fills a contract clause's
`run`.

The rule that holds the rest up: **no command is written in by habit.** The gate
comes from what the project declares, and the finding is recorded with its
source.

## Order of discovery

**First, what the project says about itself.** A repository that documents how it
is validated is more reliable than an inference from build files, and it takes
seconds:

0. **The documentation** — `AGENTS.md`, `CLAUDE.md`, `README`, `CONTRIBUTING`,
   `docs/`. Look for the sentence that says what to run before delivering. A
   `README` saying *"run `pnpm check` before pushing"* has already answered.

If the documentation does not say (or names something that does not exist),
detect the runner in the order below and stop at the first that applies:

1. **Makefile** — `make ci-check`, `make ci`, `make check`, `make lint`, `make
   test`, in that order. Confirm the real targets with
   `grep -E '^[a-zA-Z_-]+:' Makefile`.
2. **`justfile`** — `just ci-check`, `just ci`, `just check`, `just test`
   (confirm with `just --list`).
3. **Node** — read `scripts` in `package.json` and prefer `ci`, `check`, `lint`,
   `test`, `typecheck`. Use the manager the lockfile names (`pnpm`, `npm run`,
   `yarn`).
4. **Python** — `tox`, `nox`, or `uv run`/`poetry run` calling
   `pytest`/`ruff`/`mypy` according to `pyproject.toml`/`tox.ini`.
5. **Go** — `go vet ./... && go test ./...`.
6. **Rust** — `cargo clippy && cargo test`.
7. **Pre-commit** — `.pre-commit-config.yaml` → `pre-commit run --all-files`.

If nothing names one, **ask**. A guessed gate that passes proves nothing.

## Two targets, and which one to reach for

Where a project separates them:

- **A verify-only target** (often `ci-check`) writes nothing and mirrors what
  remote CI runs. Prefer it for a baseline measurement: it does not mutate files
  before you measure the state.
- **A fix-then-verify target** (often `ci`) formats or sorts and then checks.
  Useful while developing, misleading as a baseline.

The split is not cosmetic: a CI step that reformats code hides exactly what it
should be failing on.

## The gate alone is not the contract's command

The contract runs in a **clean, disposable checkout** — no installed
dependencies, no configured shim. Almost every discovered gate assumes a
workspace that was already installed, so discovery has to find **two** commands:

```toml
run = "<discovered bootstrap> && <discovered gate>"
```

The right command for a person is often the wrong command for a contract.

## Recording what was discovered

The finding outlives the moment it was made: whoever reads the task later needs
to know where it came from.

```sh
luna record --event discovery --phase setup \
  --found "gate: pnpm check; bootstrap: pnpm install --frozen-lockfile" \
  --where "package.json scripts.check (README confirms)"
```

`--where` is required. A finding nobody can check is a claim.

Luna records this and **never consults it** — the command still arrives in the
contract on every call.
