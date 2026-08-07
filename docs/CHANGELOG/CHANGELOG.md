# Changelog

Format: [Keep a Changelog](https://keepachangelog.com/en/1.1.0/).

## [Unreleased]

### Added
- Architecture design: deterministic FSM with a hybrid lead, stage contract
  (`requires`/`produces`), handoff with a content-addressed snapshot, and roles with
  tool gating.
- Initial documentation: architecture, decisions, stages and references.
- Layered documentation suite, governed by the `docs/README.md` contract: core
  glossary, invariants, ADRs, and the PRD and RFC layers formalized.
- `make lint-docs` validates the shape of the documentation and fails CI like a code
  linter.
- `mise.toml` pins the Go version that `make bootstrap` installs.
- The engine started: the contract's static check (`AuditContract`) detects a flow
  broken on paper — a stage requiring what no earlier stage produces — before any agent
  is called. The 14-stage default flow is verified in CI.
- `make cover` fails below the minimum coverage, and is part of `ci-check`.
- A quality pipeline that stands in for line-by-line review: `.golangci.yaml` names
  seventeen linters with a reason each, coverage carries a hard 95% floor, cyclomatic
  complexity is capped at 10, and `depguard` encodes the rule that the engine has no
  HTTP surface — a rule that until now lived only in prose. Race detection and
  vulnerability scanning run in remote CI; complexity, dead code, CRAP index and
  mutation testing report without gating.
- The append-only store: a task's history persists to SQLite and replays back into
  state through the reducer. Killing the process and reopening the file rebuilds the
  exact state, because it was never only in memory (INV-core-2). Snapshots live as
  content-addressed rows in the same database, so an event and the blob it points at
  land in one transaction. `AwaitingGate` makes a suspended task discoverable by query
  rather than by having watched it happen (INV-core-12).
- The reducer: a task now has state (`ready`, `running`, `awaiting_gate`, `blocked`,
  `done`) and moves between them through eight actions. This is where the contract's
  exit check lands — a stage that promised two artifacts and delivered one does not
  close — along with retry-then-block on failure, the three gate answers, the green
  invalidation on an aligned review finding, and the three loop ceilings.
- Stage selection: `NextStage` answers which stage comes next, skipping the ones whose
  condition does not hold, and `MissingFor` is the contract's entry check — the sibling
  of the static one. An unknown stage is an error rather than a silent restart.
- Three decisions that lived only in the project's memory or in the prototype now have a
  record: tool gating by a blocking hook (ADR-0018), inactivity watchdog (ADR-0019) and
  green invalidation when returning to `build` (ADR-0020).
- The stage contract now distinguishes what the flow consumes from what only a person
  reads (ADR-0021), and a gate can now carry the artifact a human reviews, adjusts or
  rejects (ADR-0022).
- The convergence loop gained three separately counted ceilings (ADR-0023).
- Invariants now carry **acceptance criteria**: the list of tests without which the rule
  is described but not implemented.

### Changed
- The decisions stopped living in a single file and became 23 numbered, immutable ADRs
  in `docs/ADRs/`, each one with its rejected alternative.
- The failure policy no longer has "go back to the previous stage" as an exit: the only
  return to an earlier stage is the one from a review finding, which invalidates the
  green on the way back. Two doors to the same place would mean one of them forgetting
  to invalidate.
- The references now record the mechanical vs. prose separation observed in SwarmForge,
  and the thesis that follows from it: enforcement on the flow, not only on transport.
- The application code now lives under `src/`; `internal/` remains the import barrier
  enforced by the compiler.
- `CONTRIBUTING.md` and `CHANGELOG.md` moved to `docs/`, and the git flow became
  normative only in `docs/CONTRIBUTING.md`.
- The whole project switched to English — code, tests, documentation, comments and
  messages. A half-Portuguese project is unreadable to half of whoever arrives, and
  mixed languages degrade search: `rg "etapa"` and `rg "stage"` found different halves
  of the same concept.
