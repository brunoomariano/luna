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
