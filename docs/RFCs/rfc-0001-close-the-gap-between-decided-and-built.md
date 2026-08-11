# RFC-0001: Close the gap between what was decided and what was built

**Status:** IN PROGRESS
**Last reviewed:** 2026-08-11
**Source issue:** —
**PRD:** —

## Motivation

An audit of the engine against its own documentation found that several decisions recorded
as settled have **no production path**. This is not ordinary technical debt: `docs/ADRs/`
today describes a more complete system than `src/` implements, and
[`AGENTS.md`](../../AGENTS.md) instructs every reader — human or agent — to treat the ADRs
as the settled state.

Verified, each one against the code:

| Decided in | State in the code |
|---|---|
| [ADR-0032](../ADRs/0032-the-contract-declares-how-each-artifact-is-verified.md) — the contract declares how each artifact is verified | no stage in `DefaultFlow()` declares a verifier |
| [ADR-0011](../ADRs/0011-failure-retry-rollback-or-block.md) / [ADR-0002](../ADRs/0002-hybrid-lead.md) — retry budget, hybrid lead | `Watchdog` and `Judge` are never assigned in `conduct()` |
| [ADR-0041](../ADRs/0041-the-reviewer-cannot-write-and-its-report-is-the-handoff.md) — a review finding returns to build | `ReviewFinding` has no emitter |
| [ADR-0042](../ADRs/0042-four-harnesses-four-ways-to-deny-a-tool.md) — four harnesses, four denials | `opencode` is marked `Precise: true` and denies nothing |
| [ADR-0023](../ADRs/0023-three-separate-loop-ceilings.md) — three loop ceilings | `Loop.NoProgress` is never incremented |
| [INV-core-12](../invariants/core.md) — the gate's artifact is retrievable by command | `PendingGate.Payload` is never populated |

The consequence that makes this urgent follows from the first row. `VerifierFor` returns
`Existence{}` when a stage declares nothing, and `Existence` yields `VerdictPassed` without
executing anything. So in the shipped flow:

> `ci_green` closes without running the pipeline. `commit_sha` closes without resolving a
> commit. `tests_green` closes without running a test.

That is [INV-core-4](../invariants/core.md) inverted in the default flow — the invariant
names *"accepting a `produces` because the field came filled in"* as the violation, and
that is the current behaviour.

**Why now, and why it is a documentation problem before it is a code problem.**
`docs/PRDs/` and `docs/RFCs/` are empty — template only. There are 46 ADRs deciding *how*,
with no document declaring *what is expected* or *what the route is*. The ADRs have been
doing the work of three layers, and an ADR by its nature records a **decision**, never
**progress**. The suite already solved this: a PRD carries `NOT IMPLEMENTED | IMPLEMENTED`
and an RFC carries `DRAFT | IN PROGRESS | DONE`
([docs/README.md](../README.md#dated-documents-prd-rfc)). The mechanism existed and went
unused. This RFC is the first use of it.

## Technical proposal

### Overview (guide-level)

Two moves, in order. The first stops the bleeding and costs almost nothing; the second
closes the holes.

**A — make the gap visible.** Nothing about the ADRs changes: an accepted ADR stays a
settled decision, and stays immutable. What changes is that **work in flight is tracked
where the contract says it should be** — in a dated document with a progress status. This
RFC is that document, and its checklist is the authoritative answer to "is this built?".

Alongside it, the divergences where documentation describes something the code refuses are
corrected, because those actively mislead:

- `architecture/overview.md` shows a `src/stock/roles/reviewer.toml` with `tools_allow`,
  `owns` and `not_owns` — three fields that do not exist in `fsm.Role` and that the config
  parser **rejects**. Pasting the documented example produces a parse error.
- `architecture/stages.md` lists `diagnose`, `spec` and `verify` as having no role;
  `flow.go` gives all three a role. The code is right (`NeedsRole()` requires it), the
  table is wrong, and `TestDefaultFlowMatchesDocumentedStages` did not catch it because it
  compares IDs and order only.

**B — close the holes**, in the order below. The order is not arbitrary: the first item is
the only one where Luna currently *asserts something it did not verify*, which is worse
than a missing feature.

### Detail (reference-level)

**B1. Declare verifiers in the shipped flow.** Every mechanically provable artifact gets a
`Command`; artifacts that are prose keep `Existence` as a **declared** choice rather than a
default nobody noticed. Extend `AuditContract` to report an artifact with no declared
verifier, so the floor is visible statically.

**B2. Connect scope to the exit check.** `Scope.Satisfies` exists and has **no production
caller** — the exit check only asks `Passing()`. Until it is wired, evidence proving
`existence` closes a stage whose contract wanted a command, which is the laundering
[ADR-0032](../ADRs/0032-the-contract-declares-how-each-artifact-is-verified.md) exists to
prevent.

**B3. Stop reporting false success on `opencode`.** `Deny` returns `(nil, nil)` while the
harness is marked `Precise: true`, so a gated role starts fully capable and nothing in the
log says the denial did not take. Under
[ADR-0042](../ADRs/0042-four-harnesses-four-ways-to-deny-a-tool.md) that is the failure
mode the closed table exists to prevent. Measured against the installed binary, `opencode`
also **ignores an unknown configuration key silently** — an invalid *value* exits 1 with a
clear error, while a mistyped *key* exits 0 and the entire denial disappears.

**B4. Fix the interpreter invocation.** `opencode --print` prints a banner and exits **0
with no answer**; the non-interactive mode is `opencode run`. The failure is silent
success, which is the worst shape available.

**B5. Populate `PendingGate.Payload`.** Without it, `luna gate show` prints a blank line,
and `GateApprove` never records human evidence because it is guarded by
`if gate.Payload != ""`, which is always false. INV-core-12's second acceptance criterion
passes today only because the test injects the payload by hand.

**B6. Wire `Watchdog` and `Judge`.** Without a `Judge`, `handleFailure` always reaches
`DecideBlock`, so the retry budget of ADR-0011 is never spent and the hybrid lead of
ADR-0002 is, in production, purely deterministic.

**B7. Emit `ReviewFinding`.** The reducer handles it, the codec serialises it, nothing
produces it — so a `[BLOCKING]` finding has no effect, nothing returns to `build`, and the
loop ceilings never fire.

- Impacted modules: `src/internal/fsm/`, `src/internal/cli/`, `src/internal/herdr/`,
  `src/internal/interpret/`, `src/stock/`
- Affected contracts: the stage contract (verifier declaration), the harness gating table
- Constraints: `Stage.Verifiers` is `map[Artifact]Verifier` holding Go functions, so it is
  **not serialisable from TOML** — see Open questions.

## Alternatives considered

- **Add an implementation-status field to the ADRs** — rejected because an ADR records a
  decision, not progress, and the suite already has a layer whose status enum tracks
  delivery. Adding a second mechanism would put the same fact in two layers, which
  [docs/README.md](../README.md) forbids.

- **Rewrite the ADRs to describe only what exists** — rejected because an accepted ADR is
  immutable; a revised decision becomes a new ADR. Narrowing seven ADRs would mean seven
  new ADRs to record that the code had not caught up, which is progress, not decision.

- **Close the holes without writing anything down** — rejected because the cause is the
  missing layer, not the missing code. Without a tracked route the same divergence returns
  with the next wave.

## Drawbacks

- The checklist below becomes a second place to keep current, and a stale checklist is
  worse than none. Mitigated by this being a **dated** document that goes `DONE` and stops
  being consulted, rather than a living one.
- B1 and B2 will make stages fail that used to close. That is the point, but it means the
  first run after this RFC is likely to block on real work — the flow was closing on
  nothing.

## Impact and migration

- **Data/persistence:** none. No log format changes here; payload versioning is a separate
  question (extended study, round 3).
- **Compatibility:** B2 changes when a stage closes. A task in flight whose evidence was
  accepted under the old rule may block on the next transition. Acceptable while there is
  no production history.
- **Observability:** B3 and B5 make two currently silent failures visible.
- **Rollback surface:** each item is independent and separately revertible.

## Rollout plan (phased)

1. **Phase 1 — visibility.** This RFC; correct `overview.md` and `stages.md`; extend
   `TestDefaultFlowMatchesDocumentedStages` to compare roles as well.
2. **Phase 2 — stop asserting what was not verified.** B1, B2, and the audit that reports
   an undeclared verifier.
3. **Phase 3 — stop reporting false success.** B3, B4, B5.
4. **Phase 4 — connect what exists.** B6, B7.

- Feature flag? no
- Rollback strategy: revert per item; each phase leaves the system valid.

## Checklist

- [x] Phase 1 — RFC written; `overview.md` and `stages.md` corrected; role comparison added
      to the flow/doc test
- [x] Phase 2 — B1 (verifiers declared for `tests_green`, `ci_green`, `commit_sha`, with
      `code` and `dod_checked` declared as `Existence` rather than defaulted); B2
      (`underProven` wired into the exit check, and `Scope.Satisfies` given its first
      production caller)
- [x] Phase 3 — B3 (`opencode` refuses instead of returning no arguments); B4 (`opencode
      run`, plus an empty answer from any harness now being an error at the interpreting
      boundary); B5 (payload carried when the artifact exists)
- [ ] Phase 4 — B6, B7

Phase 4 is deliberately left open. Wiring `Judge` and emitting `ReviewFinding` are the two
items that change what the engine *decides* rather than what it *checks*, and both deserve
their own scrutiny — a `Judge` that turns failures into retries changes the flow's shape,
and `ReviewFinding` needs the report format ADR-0041 describes to exist first.

## Found while executing

**The review-artifact gate opens before the artifact exists.** `gateFor` attaches the
`approve-spec` gate on **entry** to `spec`, and `contract` is what `spec`
**produces** — so the gate asks a human to review something that has not been written yet.
That is why `PendingGate.Payload` was never populated: at the moment the gate opens there
is nothing to put in it.

[ADR-0022](../ADRs/0022-gate-carries-artifact-for-review.md) is unambiguous about the
intent — the gate exists *"for the human to review before construction"*, and rejecting it
means *"the stage that produced it runs again"*. Both sentences describe a gate on the
**exit** of `spec`, not its entry.

This is larger than the item it was found under: it changes when a gate opens, which is a
transition, so it needs its own ADR rather than being fixed in passing. Until then, B5
delivers the half that is safe — the payload is carried when the artifact exists — and the
timing stays as it is.

## Open questions

- [ ] **How does a project declare a verifier?** `Stage.Verifiers` holds Go functions and
      the config parser knows only `[profile.*]` and `[role.*]`, so ADR-0032 has a mechanism
      in the engine and no surface through which anyone can use it. `src/stock/` is the
      intended home for shipped defaults and is currently empty — loading stages from TOML
      is larger than this RFC and likely deserves its own.
- [ ] **Does `AuditContract` run in production?** It is implemented and tested and no CLI
      path invokes it, so a flow broken on paper is only caught by tests.
- [ ] **What replaces the second INV-core-7 acceptance criterion?** It asked for detecting
      `tools_allow` contradicting `not_owns`; neither field exists, so the criterion was
      inexpressible. The rewrite under
      [ADR-0045](../ADRs/0045-the-agent-is-fallible-except-at-the-evidence-boundary.md)
      replaces it with a load-time check that a role's harness can express its declared
      denial.

## References

- Issue: —
- PRD: —
- Related ADRs: [ADR-0045](../ADRs/0045-the-agent-is-fallible-except-at-the-evidence-boundary.md)
  (threat model), ADR-0032, ADR-0042, ADR-0011, ADR-0002, ADR-0023, ADR-0041
- PRs: —
