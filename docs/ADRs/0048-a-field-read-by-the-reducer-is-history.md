# ADR-0048: A field the reducer reads is history; a field only the node reads is policy

**Status:** Accepted
**Date:** 2026-08-12

## Context

[ADR-0046](0046-the-log-records-which-flow-it-was-written-under.md) put the flow's identity
in the log and had to decide what "identity" covers. It settled on stage ids, order, and
the artifacts each stage requires and produces, leaving out `Role`, `Verifiers` and the body
of a `When` condition. The reasoning was sound and the boundary was drawn by hand, one field
at a time.

Camunda's scar tissue says that is where this goes wrong. Their migration preserves a
message subscription under the **old** message name; if the name changed, the instance waits
forever — `HTTP 204`, no incident, one customer with ~10,000 stuck instances. Their own
conclusion is the useful part: a system that validates 1,388 lines of preconditions and
still accepts a migration that hangs the instance got the **boundary** wrong, not the
volume. The rule they paid for:

> If a field is read from the definition at the moment of use, it migrates with you whether
> you want it to or not. Only what is copied into the state stays frozen.

Applied to Luna, that rule has a mechanical answer, because the reducer is pure. A field
read inside `Reduce` participates in rebuilding the past. A field read anywhere else cannot.
There is no judgement call — `grep` decides.

Running it turned up two things the hand-drawn boundary had wrong.

**`When` is history and was left out.** `AppliesTo` decides whether a stage enters the flow
at all, so changing `diagnose` from "is a bug" to "is a feature" changes which stages a task
should have walked through. It was excluded because `func(TaskContext) bool` has no identity
a digest can record — a real obstacle, treated as a reason.

**`Verifiers` became history and nobody noticed.** ADR-0046 excluded it on the grounds that
evidence records what actually ran. That was true when written. It stopped being true in the
same wave that wired `Scope.Satisfies` into the exit check: `underProven` now compares the
recorded scope against **what the contract declares today**, so lowering `ci_green` from a
command to `Existence` makes a past `Complete` that blocked start closing.

## Decision

**The classification follows the read, not the judgement.**

- **History** — read inside the reducer: `ID`, `Requires`, `Produces`, `ProducesForHuman`,
  `When`, and the *scope* each verifier declares.
- **Policy** — read only outside it: `Role`, and the *command* a verifier runs.

Three consequences follow.

**`When` gains a name.** It becomes a `Condition{Name, Applies}` rather than a bare
function, and the fingerprint records the name. This does not let a digest see inside the
function — nothing could — but it gives a stable handle a person maintains deliberately.
Renaming the condition is how you say *this rule is not the rule it was*, and it is the same
discipline the log's action names already rely on: hand-written string constants, because
the log outlives the code.

**Verifiers contribute their scope and not their command.** `make test` becoming
`go test ./...` changes how an artifact is proven, not how much was proven, and the evidence
records what actually ran ([ADR-0024](0024-the-reducer-is-pure-verification-runs-outside.md)).
Including the command would refuse a replay because somebody renamed a Makefile target.

**The classification is frozen by a test.** Reflection over `Stage`'s fields fails when one
is neither history nor policy, and a second test changes each history field in turn and
insists the digest moves — so the list is a claim the code has to honour rather than a
comment that drifts. The test is by reflection precisely because Camunda's lesson is that
the boundary, not the effort, is what fails.

## Alternatives considered

- **Include the whole `When` function** — impossible rather than rejected: Go offers no
  stable identity for a function value. Hashing the pointer changes between builds and
  would refuse every replay.

- **Leave `When` out and document it** — rejected because it is the field with the largest
  blast radius. A condition decides whether a stage exists for a task, so changing one
  rewrites the entire path a replay walks, not one stage's contract.

- **Include the verifier's command as well as its scope** — rejected as the noise trap. It
  would fire on edits that cannot change what a past event meant, and a check people learn
  to work around protects nothing. Recorded as a real cost: a command that changes *what it
  proves* while keeping its declared scope is a lie the fingerprint will not catch, and the
  defence against that is the scope declaration being honest in the first place.

- **Revert the scope comparison so `Verifiers` stops being history** — rejected outright.
  The comparison is what stops evidence proving existence from closing a stage whose
  contract declared a command, which is the laundering the scopes exist to prevent. Removing
  a guarantee to simplify a fingerprint is the wrong trade.

- **Keep the classification as a comment** — rejected because the next field added would
  skip it silently, in either direction, and both directions fail quietly.

## Consequences

- **Positive:** the boundary is now derivable rather than remembered. "Does the reducer read
  it?" answers the question for any field added later.

- **Positive:** two real gaps closed — a changed condition and a lowered verification
  requirement now move the fingerprint, and both silently rewrote history before.

- **Negative:** the condition's name is a discipline, not a mechanism. Changing a
  condition's logic while keeping its name is undetectable, and the honest statement is that
  the fingerprint records what a person declared rather than what the code does.

- **Negative:** the shipped flow's fingerprint changes with this ADR, since conditions and
  scopes now contribute. Any task open across the change stops replaying and has to be
  abandoned — the cost ADR-0046 accepted, arriving for the first time.

- **Impacts:** `Stage.When` changes type, so a flow declaring conditions has to name them.
  `src/stock/` will need a way to express a condition when it exists, and a named set is far
  easier to put in a config file than a function ever was — an unplanned benefit worth
  noting.

## References

- Related documents: [ADR-0046](0046-the-log-records-which-flow-it-was-written-under.md),
  [ADR-0032](0032-the-contract-declares-how-each-artifact-is-verified.md),
  [ADR-0024](0024-the-reducer-is-pure-verification-runs-outside.md),
  [ADR-0017](0017-defaults-plus-customization-everywhere.md)
