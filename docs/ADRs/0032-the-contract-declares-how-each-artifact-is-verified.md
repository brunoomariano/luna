# ADR-0032: The contract declares how each artifact is verified

**Status:** Accepted
**Date:** 2026-08-10

## Context

[ADR-0028](0028-herdr-status-triggers-verification-it-never-closes-a-stage.md) settled that a
stage closes on evidence of observed effect, never on an agent's status. It did not say where
the check that produces that evidence comes from.

The contract as it stands names artifacts and nothing else: `build` produces `code` and
`tests_green`, `qa` produces `ci_green`. Those are names for things that must exist. Nothing
anywhere says that `tests_green` means `go test ./...` passed.

That gap is exactly the failure the study found in multica: an `acceptance_criteria` column
that names done-ness, is faithfully stored, and is never read by any logic. A contract that
cannot be mechanically checked decays into decoration.

There is a second problem the first one hides. Several artifacts have no command that could
verify them — `briefing`, `scenarios`, `contract`, `root_cause` are prose written for the next
stage or for a person. Whatever answer is given for `tests_green` has to say something honest
about those too.

## Decision

**Each artifact declares how it is verified, alongside the contract that produces it.**

```go
Produces: []Artifact{"code", "tests_green"}

Verifiers: map[Artifact]Verifier{
    "tests_green": Command{Run: "go test ./...", Scope: ScopeFull},
    "ci_green":    Command{Run: "make ci",       Scope: ScopeFull},
}
```

The contract becomes the complete definition of done for that stage, which is precisely the
argument ADR-0028 makes against hermes-agent: an LLM harness cannot know what "verified" means
for an arbitrary project, but a project that declares it up front can.

**`Verifier` is an interface, not a shell string.** Two implementations to start:

- `Command` — runs a real tool and reads its exit code. Produces evidence with the scope it
  declares (`targeted` or `full`), and never upgrades one into the other.
- `Existence` — the artifact was delivered and is in the store. Produces evidence with
  `scope: existence`, which says exactly what happened and does not pretend a check ran.

An artifact with no verifier declared uses `Existence`. That is the honest default: the log
records that the thing was delivered, not that anything was validated. `luna task show`
displays the scope, so a reader can tell a green suite from a file that merely exists.

The interface exists so a third implementation can arrive without reopening this decision — a
`Review` verifier that asks a model to judge prose is the obvious candidate. It is
deliberately **not** built now: it costs tokens on every stage, and a model's verdict is the
self-reported completion ADR-0028 rejects. If it is ever added it will need its own ADR
arguing why a reviewing model is different from an implementing one, which is a real argument
(INV-core-7 already separates the two roles) but not one this decision makes.

## Alternatives considered

- **One verification command per stage** — rejected. A stage that produces two artifacts gets
  one verdict, so a failure cannot say *which* artifact is missing. The exit check already
  names the missing artifact by name; a coarser verifier would take that back.
- **Verifiers in `.luna/config.toml`, separate from the flow** — rejected, and this is the one
  worth naming. It is more flexible and it recreates multica's failure: a declaration of
  done-ness living far from the thing it describes, easy to leave stale, and impossible to
  read the stage and know what "done" means. Verifiers are configurable — a project can
  override them like it overrides stages and profiles (ADR-0017) — but the default ships
  *with* the contract.
- **A reviewing agent for prose artifacts, now** — rejected for wave 5 on cost and on
  principle. Every stage would pay tokens for a verdict that is still a model's opinion. The
  seam is left open; the wiring is not.
- **Refusing to let unverifiable artifacts close a stage** — rejected. It would turn every
  prose artifact into a gate, which makes the `nightly` profile meaningless and moves gate
  declaration out of the flow, where ADR-0013 put it.

## Consequences

- **Positive:** the contract is now the complete definition of done, checkable by code. The
  scope distinction (`full` / `targeted` / `existence`) is visible in the log, so an audit can
  tell what was actually proven from what was merely delivered.
- **Negative / costs:** every stage in the stock flow needs a verifier decision, and for most
  of them the honest answer is `Existence` — which reads as weak until you notice it is the
  truthful description of what the previous design did silently for everything.
- **Impacts:**
  - `Stage` grows a `Verifiers` map; the static check should warn when a `Produces` artifact
    has no verifier, so `Existence` is a choice rather than an oversight;
  - `Evidence` carries the scope that produced it (ADR-0028), and `existence` becomes a value
    it can hold;
  - the node layer runs the verifier and reports inward — the reducer still runs no command
    (ADR-0024);
  - a `Verifier` that needs the worktree path needs it supplied by the node layer, not read
    from the engine.

## References

- Related documents: [ADR-0024](0024-the-reducer-is-pure-verification-runs-outside.md),
  [ADR-0028](0028-herdr-status-triggers-verification-it-never-closes-a-stage.md),
  [ADR-0017](0017-defaults-plus-customization-everywhere.md),
  [architecture/stages](../architecture/stages.md)
