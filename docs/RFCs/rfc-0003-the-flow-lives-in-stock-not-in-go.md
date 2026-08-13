# RFC-0003: The flow lives in stock, not in Go

**Status:** DRAFT
**Last reviewed:** 2026-08-13
**Source issue:** —
**PRD:** —

## Motivation

[ADR-0017](../ADRs/0017-defaults-plus-customization-everywhere.md) says the flow is replaceable:
stages can be disabled, edited or replaced, and new ones created. In practice, replacing it
means **recompiling Luna**. The fourteen stages, their contracts, their conditions and their
verifiers live in `flow.go` as Go literals, and the config parser knows only `[profile.*]`
and `[role.*]`.

The sharpest consequence is the question that outlived RFC-0001:

> **How does a project declare a verifier?** `Stage.Verifiers` holds Go functions.
> [ADR-0032](../ADRs/0032-the-contract-declares-how-each-artifact-is-verified.md) says the
> contract declares how each artifact is verified — the mechanism exists in the engine and
> there is **no file through which anyone can reach it**.

So a project that wants `make check` instead of `make ci`, or one extra review stage, has
the same options as one that wants to rewrite the engine. That is not a configurable flow;
it is a flow with a comment saying it could be.

`src/stock/` has existed as four empty directories with a `.gitkeep` since the layout was
drawn. `AGENTS.md` describes it as *"defaults: stages, roles, profiles, skills"*, and
`architecture/overview.md` says plainly that the defaults live in Go and that the directory
is the intended home.

**Why now.** Everything else about a stage became data along the way.
[ADR-0048](../ADRs/0048-a-field-read-by-the-reducer-is-history.md) gave conditions names,
[ADR-0049](../ADRs/0049-a-stage-declares-its-gate-and-what-a-review-costs.md) turned gates
and review behaviour into fields. `Verifier` is the last field of `Stage` still holding
something a file cannot express — which means the gap is now one type wide rather than a
rewrite.

## Technical proposal

### Overview (guide-level)

The flow stops being a Go function and becomes files that ship with Luna.

```
  today                          after

  flow.go  DefaultFlow()         src/stock/stages/*.toml   ← embedded in the binary
    14 stages as Go literals        14 stages as data
    Verifiers as Go values          verifiers declared explicitly
                                        │
                                        │ luna init
                                        ▼
                                  .luna/stock/stages/*.toml   ← the project's copy
                                        │
                                        │ edited by a person
                                        ▼
                                  the flow this project runs
```

Three moves, and the order matters because each one is separately revertible.

**A. The stock is the source, and it ships inside the binary.** `go:embed` puts
`src/stock/**` into the executable, so a Luna with no files beside it still has a flow.
`DefaultFlow()` stops being literals and becomes "parse the embedded stock" — which means
the parser is exercised by every existing test on the first day, rather than by whatever
tests get written for it.

**B. `luna init` writes the stock into the project.** `.luna/stock/`, beside the config and
the log. From then on the project's copy is what runs, and editing it is the customisation
ADR-0017 promised. A project with no `.luna/stock/` runs the embedded one, so nothing breaks
for a task that exists today.

**C. The flow's identity is what makes editing safe.** Editing a file changes the
fingerprint, and [ADR-0046](../ADRs/0046-the-log-records-which-flow-it-was-written-under.md)
already refuses to replay a log written under a different flow. That protection exists and
was designed for a flow that changed at compile time; a flow that changes when someone saves
a file needs it to be *visible* rather than merely correct.

### Detail (reference-level)

**The stage file.** One TOML per stage in `src/stock/stages/`, named by its id so the
directory reads as the flow.

```toml
# src/stock/stages/070-build.toml
id       = "build"
role     = "implementer"
requires = ["scenarios", "approach", "contract"]
produces = ["code", "tests_green"]

[verify.tests_green]
run   = "make test"
scope = "targeted"

# Every artifact declares how it is proven, including the ones nothing can prove
# mechanically. That is the point of the explicit form: `existence` becomes a
# choice someone made rather than a default nobody noticed (ADR-0032).
[verify.code]
kind = "existence"
```

Order comes from the filename prefix, not from a field. `AuditContract` checks precedence,
so the order is part of the contract — and a numeric prefix makes inserting a stage an
edit to one filename rather than a renumbering.

**Verifiers are explicit** (decision 1). `[verify.<artifact>]` with either `run` + `scope`
for a command, or `kind = "existence"` for the floor. An artifact with no `[verify]` block
is a **load error**, not a silent `Existence` — which is the whole reason ADR-0032 exists,
finally reachable from outside the engine.

**Conditions stay named, and the set stays closed.** `when = "is-bug"` resolves against the
conditions in `condition.go`. A name the build does not know is refused at load. This is
deliberately *not* an expression language: a condition is history — it decides which stages
a task should have walked through (ADR-0048) — and an arbitrary predicate in a config file
is a flow whose past cannot be reconstructed.

**Roles and profiles move too** (decision 2). `ShippedRoles()` and the profile defaults stop
being Go maps and become `src/stock/roles/*.toml` and `src/stock/profiles/*.toml`. The
existing `[role.*]` and `[profile.*]` config sections keep working and keep overriding —
what changes is where the *defaults* come from, so there is one home for a default instead
of two.

**`luna init`** (decision 2, the second half). Writes `.luna/stock/` from the embedded copy.
It refuses to overwrite by default; `--force` says otherwise. The project's stock is
complete rather than partial — all fourteen stages, all roles, all profiles — because a
partial copy raises "does this file replace the default or merge with it?", and the answer
is always going to be one that someone gets wrong.

**The UX for a changed flow** (decision 3). Three things, and none of them is a new
mechanism:

- `luna flow check` already reports what is open and what no longer replays. It gains the
  fingerprint of the stock on disk against the one tasks were written under, so the answer
  to "did I just break something" is one command.
- Editing the stock while a task is open is exactly the case ADR-0046 refuses. The check
  is what a person runs first; the refusal is what catches them when they did not.
- A stage file that does not parse fails at load with the file and the line, before any
  task moves. A flow that half-loads is worse than one that refuses.

- Impacted modules: `src/internal/fsm/` (flow.go, a new loader), `src/internal/cli/`
  (config.go, flow.go, a new `init`), `src/stock/**`
- Affected contracts: the stage contract becomes a file format, which is a public surface
- Constraints: `Verifier` is a closed interface with an unexported method, so the loader
  maps a declared shape onto the two implementations rather than accepting arbitrary types

## Alternatives considered

- **Leave the flow in Go and add a TOML override for verifiers only** — rejected. It is the
  smallest change that answers RFC-0001's question, and it leaves two homes for the same
  fact: the stage in Go, its verifier in a file. The next question ("can I add a stage?")
  arrives immediately and is not answered.

- **Read the stock from disk only, with no embedding** — rejected. A binary whose flow is a
  directory beside it breaks the moment someone moves the binary, and the failure is at run
  time in someone else's project. Embedding means Luna always has a flow and the files are
  a *copy*.

- **An expression language for conditions** — rejected. A condition decides which stages a
  task walked through, so it is history (ADR-0048); an arbitrary predicate in a file makes
  the past unreconstructable and the fingerprint meaningless. Named conditions from a closed
  set keep both.

- **YAML or JSON instead of TOML** — rejected for consistency: `.luna/config.toml` is
  already TOML and the parser is already a dependency. There is no argument here beyond not
  having two formats.

- **Merge the project's stock with the embedded one file by file** — rejected. It sounds
  convenient and produces the question "does my file replace or extend?" at every load. A
  complete copy has one answer.

## Drawbacks

- **The stage contract becomes a public format.** Today it is Go, and changing it is a
  compile error in one repository. After this, a field rename breaks every project that
  copied the stock — which is the cost of the surface being real.

- **Errors move from compile time to load time.** A typo in a stage id is caught by the
  compiler today. Mitigated by the audits already existing and now running in
  `luna flow check` (ADR-0058), and by refusing to load rather than half-loading.

- **`DefaultFlow()` stops being a constant.** Every test that builds a flow in Go still
  works — the type is unchanged — but the shipped flow now comes from a parser, so a broken
  parser breaks everything at once. That is deliberate: it means the parser is covered by
  the whole existing suite from the first commit rather than by its own tests alone.

- **A person can now write a flow that does not hold together.** `AuditContract` reports it
  and `luna flow check` runs it, so the failure is visible — but it is a failure a project
  can now reach, and could not before.

## Impact and migration

Nothing to migrate for an existing task: the embedded stock is the same fourteen stages, so
the fingerprint of a freshly-parsed default flow **must equal** the one tasks were opened
under. That is the acceptance criterion for move A, and the test is a one-liner comparing
`Fingerprint(DefaultFlow())` against the value recorded in this RFC.

A project adopts the surface by running `luna init` and editing what it wants. A project
that never runs it never notices the change.

## Rollout plan (phased)

1. **Phase 1 — the loader, proven against the current flow.** Parse `src/stock/stages/*.toml`
   into `[]Stage`, embed it, and make `DefaultFlow()` read it. The flow it produces must be
   byte-identical in fingerprint to today's. Nothing else changes.
2. **Phase 2 — roles and profiles follow.** `ShippedRoles()` and the profile defaults come
   from the stock. The existing override sections keep working.
3. **Phase 3 — `luna init` and the flow-change UX.** The project's copy, and
   `luna flow check` reporting the stock's fingerprint against the tasks'.

- Feature flag? no. Phase 1 is invisible by construction — the fingerprint proves it.
- Rollback: phases 1 and 2 revert to Go literals; phase 3 is additive.

## Open questions

- [ ] **Does `skills` get a format now or later?** The directory exists and
      `architecture/overview.md` says skills parse and are read by nothing. Giving them a
      file format before anything reads them would be inventing a surface with no consumer.
- [ ] **Should `luna init` be able to write a single stage?** A project that wants one extra
      review stage copies fourteen files to get it. The complete copy is the right default;
      whether there is a narrower gesture is a question for after someone has used it.

## References

- Related documents: [ADR-0017](../ADRs/0017-defaults-plus-customization-everywhere.md) (the promise
  this makes real), [ADR-0032](../ADRs/0032-the-contract-declares-how-each-artifact-is-verified.md)
  (the mechanism with no surface), [ADR-0046](../ADRs/0046-the-log-records-which-flow-it-was-written-under.md)
  (why editing is safe), [ADR-0048](../ADRs/0048-a-field-read-by-the-reducer-is-history.md),
  [ADR-0049](../ADRs/0049-a-stage-declares-its-gate-and-what-a-review-costs.md),
  [ADR-0058](../ADRs/0058-what-had-no-caller-is-either-wired-or-gone.md) (the audits that
  now run), [RFC-0001](rfc-0001-close-the-gap-between-decided-and-built.md) (whose open
  question this answers)
