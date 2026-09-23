# Changelog

Format: [Keep a Changelog](https://keepachangelog.com/en/1.1.0/).

## [Unreleased]

### Changed
- **Luna is called by the agent, not the other way round.** It no longer runs agents, opens
  worktrees, builds sandboxes or decides what happens next. A person composes the session
  before any agent starts; something conducts the phases; Luna is invoked at a boundary to
  prove what was delivered. Production code went from 21,150 lines to 2,734, and `go.mod`
  from one dependency to none.
- **The contract arrives on stdin and nothing is written into a checkout.** No state file,
  no anchor, no ignore entry.
- **The record is one append-only JSONL ledger outside every checkout, and the state is a
  run's most recent line.** Replay, flow fingerprints, the central SQLite database and the
  daemon that owned it are gone with the decisions that required them.
- **Autonomy is `manual` / `semi` / `auto`** rather than a 0–10 knob, and every mode still
  blocks on missing information.
- **Exit codes are the contract with whoever conducts:** 0 proven, 2 not proven, 1 Luna
  could not run.
- **The listing is `luna runs`, not `luna report`.** "Report" named three unrelated things
  in one binary and was unfindable with `rg` among Go's own `reports whether` idiom. The
  old name still dispatches, undocumented, for one release.

### Added
- **`luna check` refuses to write to a ledger that would not survive the process** — a
  `statfs` against `TMPFS_MAGIC` before the first write, with a refusal that says how to map
  the directory into a sandbox. Verified inside a real `ai-jail`, with and without the map.
- **`luna session <agent>`** starts an agent already briefed with what this repository has
  open, and **`luna install-skills <claude|codex>`** writes the skill Luna carries into a
  harness's own directory. The skill ships inside the binary, so the version that installs
  is the version that was built — a skill documenting a verb the binary no longer answers
  costs somebody a session.
- **`luna record --event discovery`** records what a phase concluded about a project and
  the file it read to conclude it. Recorded and never consulted: the command still arrives
  in the contract on every call.
- **`luna trail`** reads a run's whole trail rather than its last line, and says when a run
  finished having never proven anything.
- **`--note` and `--round`** on `record`, which were usable and undocumented.

### Fixed
- **`luna check` resolves the delivery's HEAD in the worktree it was called from**, not in
  the main repository. They differ in exactly the case Luna is built for, and taking the
  repository's ran every check over the base and recorded it as proof — six tasks across
  two days.
- **A long existence listing is summarised rather than refused.** A `path` that is a folder
  listed every file under it; a few hundred files pushed the ledger line past its
  atomic-append ceiling and the run aborted partway through, with the earlier checks
  already recorded.
- **One branch per run**, not one per phase: a phase in the branch name ages at the first
  transition.
- **An empty listing names every filter that narrowed it**, rather than reading as no work.

### Removed
- `luna task`, `luna lead`, `luna fleet`, `luna gate`, `luna daemon`, `luna artifact`, the
  embedded flows under `src/stock/`, and the packages behind them.

## [Superseded by the redesign above]

### Changed
- **State is one central, project-scoped SQLite database owned by the daemon.** The CLI
  opens `$XDG_DATA_HOME/luna/luna.db` in physical read-only mode and forwards events,
  artifacts and artifact cleanup over the daemon socket. A per-database process lock
  prevents two daemons from owning the same file. Former per-project stores and legacy
  checkout-local stores are imported by the daemon and archived only after an exact copy.
- **Workload commands now read across projects.** `luna task list [--json]` lists every
  active task in the central store, and `luna gates`, `luna stuck`, `luna fleet report`
  and `luna flow check` use the same global view. Commands that act on one task remain
  scoped to the project of the current checkout, where equal ids are unambiguous.
- **A stage owns its agent policy.** The old role catalogue is gone: each stage declares
  its agent, briefing and denied capabilities, while solo mode collapses the briefings
  onto one broad agent policy. Pack size is derived from distinct stage briefings.
- **The documentation suite was reset.** Ten layers, 73 ADRs, 9 RFCs, 3 PRDs and ~95,000
  words became four living files: `architecture.md` (how it works today), `invariants.md`
  (the five rules that always hold), `decisions.md` (what was chosen and what was
  rejected) and `lessons.md` (what building it taught). The previous suite is preserved
  under the `pre-docs-reset` tag.

  The reason is drift the old shape could not catch: `architecture/overview.md` described
  a directory as empty that held twelve files and diagrammed a component removed four
  decisions earlier, while every structural check reported green. Documentation grew to
  ~7× the size of the code it described, for a tool with no users.

  Two rules changed with it. A decision record is now **living** — a revised decision is
  an edit, not a new document, because reading a chain of four to find the current answer
  was the cost that made the old format fail. What survives is the discipline of writing
  down the **rejected alternative**. And an in-code comment references a measurement or a
  SHA rather than a document number.
- **Fresh context per stage is no longer an invariant.** The concern (role erosion in long
  sessions) was real; the mechanism was wrong. What protects the flow is verification at
  the exit, not the agent's amnesia — an agent with live context that drifts is caught by
  the same wall as a fresh agent that is simply bad. Context becomes a per-stage setting,
  to be measured rather than assumed. The one mandatory part stays: a reviewer never
  inherits the session of whoever wrote the code.
- `lint-docs` shrank from 170 lines to the checks that still mean something — links,
  headings, and the four files existing. ADR numbering and ADR immutability checks went
  with the format they enforced.

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
