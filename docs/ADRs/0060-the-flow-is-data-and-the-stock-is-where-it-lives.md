# ADR-0060: The flow is data, and the stock is where it lives

**Status:** Accepted
**Date:** 2026-08-13

## Context

[ADR-0017](0017-defaults-plus-customization-everywhere.md) says the flow is replaceable.
Replacing it meant recompiling Luna: fourteen stages as Go literals in `flow.go`, twelve
roles as a Go map, three profiles derived from a Go function, and a config parser that knew
only `[profile.*]` and `[role.*]`.

The sharpest consequence is the question that outlived
[RFC-0001](../RFCs/rfc-0001-close-the-gap-between-decided-and-built.md): `Stage.Verifiers`
holds Go values, so [ADR-0032](0032-the-contract-declares-how-each-artifact-is-verified.md)
had a mechanism in the engine and **no file through which anyone could reach it**. A project
wanting `make check` instead of `make ci` had the same options as one wanting to rewrite the
engine.

`src/stock/` had existed as four empty directories since the layout was drawn, with
`AGENTS.md` describing it as the home of the defaults and `architecture/overview.md` saying
plainly that the defaults lived in Go instead.

What made it tractable now is that everything else about a stage had already become data:
[ADR-0048](0048-a-field-read-by-the-reducer-is-history.md) gave conditions names,
[ADR-0049](0049-a-stage-declares-its-gate-and-what-a-review-costs.md) turned gates and
review behaviour into fields. `Verifier` was the last field holding something a file could
not express.

## Decision

**The stock is the source. It ships inside the binary, and a project copies it.**

`src/stock/{stages,roles,profiles}/*.toml`, embedded with `go:embed`. `DefaultFlow()`,
`ShippedRoles()` and `ShippedProfiles()` parse them. A binary always has a flow; the files
beside a project are a *copy*, written by `luna init`.

**Verifiers are explicit.** `[verify.<artifact>]` with `run` + `scope` for a command, or
`kind = "existence"` for the floor. An artifact a stage produces and does not declare is a
**load error**. That is the whole point: `VerifierFor` returns `Existence` for anything
undeclared, so the floor was a default nobody noticed, and in a file it has to be written
down. `ProducesForHuman` is exempt — requiring a machine check on prose would be requiring
the wrong thing (INV-core-11).

**Conditions stay named against a closed set.** `when = "is-bug"` resolves against
`ShippedConditions()`; an unknown name is refused at load. Deliberately not an expression
language: a condition decides which stages a task walked through, which makes it history
(ADR-0048), and an arbitrary predicate in a file is a flow whose past cannot be
reconstructed once the predicate is edited away.

**Order is the filename.** `010-discovery.toml`, `020-setup.toml`. `AuditContract` checks
precedence rather than existence, so order is part of the contract — and a numeric prefix
makes inserting a stage an edit to one filename rather than a renumbering.

**A stock file has no sections.** Its name is the filename, so `[role.reviewer]` inside
`scout.toml` cannot disagree with itself. A section in a stock file is refused with a message
saying that sections belong in a project's `config.toml`, where the same keys mean the same
things.

**`fsm.ShippedPolicy` does not move.** It is called from inside the reducer as the fallback
for an event recorded before gate decisions existed
([ADR-0026](0026-the-log-records-the-gate-decision-not-the-policy.md)) — frozen history
rather than configuration, and reading a file for it would make a replay depend on what is
on disk today. A test ties the profile files to it, because a profile whose file disagreed
would make a replay and a fresh run answer differently about the same name.

**The equivalence is proven by the fingerprint.** The parsed stock and the retired Go
literals produce the same digest, `c0c9ff4d121b43fc`. That is the only guarantee that
matters to a task already open: ADR-0046 refuses a log written under a different flow, so an
identical fingerprint means the move cannot refuse a replay. The Go literals are kept as a
test fixture rather than deleted, and lowering one scope in one file makes the comparison
fail.

## Alternatives considered

- **A TOML override for verifiers only** — rejected. It is the smallest change that answers
  RFC-0001's question and it leaves the stage in Go with its verifier in a file, so "can I
  add a stage?" arrives immediately and is still unanswered.

- **Read the stock from disk with no embedding** — rejected. A binary whose flow is a
  directory beside it breaks when the binary moves, and it breaks at run time in someone
  else's project.

- **An expression language for conditions** — rejected, as above: it trades a
  reconstructable past for a convenience.

- **Merging a project's stock with the embedded one, file by file** — rejected. It sounds
  convenient and produces "does my file replace or extend?" at every load; a complete copy
  has one answer, which is why `luna init` writes all twenty-nine files.

- **Threading the flow through every caller instead of `fsm.UseFlow`** — considered
  seriously and rejected. Fifteen call sites would each pass the same value down from the
  same place. What keeps the package-level value from being a mutable global is that it is
  set once, in `main`, before any command runs — and a test asserts both directions.

## Consequences

- **Positive:** ADR-0017 is true. A project runs `luna init`, edits a file, and
  `luna flow check` tells it whether the result still holds together — verified end to end
  against a live herdr with a two-stage flow that looks nothing like the shipped one.

- **Positive:** ADR-0032 has a surface. The floor is a choice someone wrote down rather than
  an omission nobody noticed.

- **Positive:** `flow.go` went from 166 lines to 45, and every existing test now exercises
  the parser — which is what makes it proven by the whole suite rather than by its own tests.

- **Negative:** the stage contract is now a public format. A field rename breaks every
  project that copied the stock, which is what a real surface costs.

- **Negative:** errors moved from compile time to load time. A typo in a stage id was a
  compile error and is now a refusal at startup — mitigated by refusing rather than
  half-loading, and by naming the file and line.

- **Negative:** a project can now write a flow that does not hold together. The audits report
  it (ADR-0058) and `luna flow check` runs them, so it is visible — but it is a failure a
  project can now reach.

- **Negative:** editing a stage file while a task is open changes the fingerprint and stops
  that task replaying. That protection existed for a flow that changed at compile time; it
  now fires when someone saves a file. `luna flow check` names the tasks, which is the
  warning, and the refusal is what catches whoever did not run it.

- **Impacts:** `src/stock/**` (new, 29 files), `fsm.LoadFlow`, `fsm.ParseStage`,
  `fsm.UseFlow`, `fsm.ShippedConditions`, `cli.LoadRoles`, `cli.LoadProfiles`,
  `cli.StockDir`, `luna init`, and `luna flow check`. RFC-0001's verifier question closes
  with this.

## References

- Related documents: [RFC-0003](../RFCs/rfc-0003-the-flow-lives-in-stock-not-in-go.md),
  [ADR-0017](0017-defaults-plus-customization-everywhere.md) (made real here),
  [ADR-0032](0032-the-contract-declares-how-each-artifact-is-verified.md) (given a surface),
  [ADR-0046](0046-the-log-records-which-flow-it-was-written-under.md) (why editing is safe),
  [ADR-0026](0026-the-log-records-the-gate-decision-not-the-policy.md) (why ShippedPolicy
  stays), [ADR-0048](0048-a-field-read-by-the-reducer-is-history.md),
  [ADR-0058](0058-what-had-no-caller-is-either-wired-or-gone.md),
  [RFC-0001](../RFCs/rfc-0001-close-the-gap-between-decided-and-built.md)
