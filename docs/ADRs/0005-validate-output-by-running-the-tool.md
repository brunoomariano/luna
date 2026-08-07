# ADR-0005: Output is validated by running the tool

**Status:** Accepted
**Date:** 2026-08-06

## Context

Checking a stage's output (see
[ADR-0004](0004-stage-requires-produces-contract.md)) needs a criterion. There are cheap
ways to check — the process exit code, the return format — and there is the expensive way:
executing the tool that proves the fact.

## Decision

Output is validated by **running the tool**: the test runs, the commit resolves, the file
exists. We do not check format, we check reality.

## Alternatives considered

- **Process exit code** — rejected because the CLI exiting with zero does not mean the
  work turned out right.
- **Validating format** — rejected because a well-formed JSON can describe something that
  does not exist.

## Consequences

- **Positive:** it is the check that matters most — it catches the hole where it is born,
  not two stages later when the symptom has already drifted from the cause.
- **Negative / costs:** validating costs a real tool execution at every stage close.

## References

- Related documents: [architecture](../architecture/overview.md),
  [references](../references.md)
