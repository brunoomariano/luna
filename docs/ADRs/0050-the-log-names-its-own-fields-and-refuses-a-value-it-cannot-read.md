# ADR-0050: The log names its own fields, and refuses a value it cannot read

**Status:** Accepted
**Date:** 2026-08-12

## Context

An event has two halves, and only one of them was protected.

```
seq=3 action=Complete payload={"Delivered":["repos"],"Evidence":{...}}
```

The **action name** is a hand-written string constant, and `codec.go` says why:
*"the log outlives the code"*. The **payload** was `json.Marshal` over a Go struct with no
tags, so the field names in every log written so far are Go identifiers, derived
automatically. The reasoning that protected the first half was never applied to the second.

Two failures follow, both measured against the real code before anything was changed.

**A renamed field decodes cleanly and means something else.** Rename `Verdict` to `Result`
and `json.Unmarshal` does not complain — it ignores what it does not recognise and leaves
the field at its zero value. `""` is not `VerdictPassed`, so `Passing()` returns false, so
the task **blocks claiming verification did not pass**. The cause is a rename; the symptom
sends whoever investigates to read the test suite.

**A value from a newer version is accepted.** `Scope: "mutation"` unmarshals into a
`~string` type without complaint and reaches the reducer as a value no switch matches. What
happens next is whichever `default` it falls into.

That second failure is the one neither journalling nor replay catches, and the two most
mature references both document it. Temporal compares commands and never payloads — their
own docs admit *"it does not check on the Activity's input arguments"*. Restate protects the
sequence and not the meaning, and their example is a number on a 0-10 scale reinterpreted as
0-100: the journal matches, no error fires, the decision is wrong. Temporal solves it for
itself by using protobuf and offers users nothing.

The debt is not hypothetical. `Advance.GateDecision` already carries a comment about needing
*"a fallback"* for events that *"predate the field"* — the first failure, met once and
handled by hand.

## Decision

**Three changes, narrowest first.**

### Every action and every type it carries names its own fields

Explicit `json` tags on the nine actions and on `Evidence` and `LoopLimits`, in snake_case.
The log's vocabulary stops being a shadow of Go's identifiers, so renaming a field is a
refactor rather than a change to what past events mean.

### The engine's own enums refuse a value they do not know

`Verdict`, `Scope`, `TaskKind` and `GateWaited` decode through `UnmarshalJSON`, which
accepts the known set and returns `ErrUnknownValue` for anything else. That makes the whole
action fail to decode, which makes the replay stop — the third refusal alongside
`ErrUnknownAction` and `ErrFlowChanged`, and the same posture as both.

**`Profile` is deliberately excluded**, and the exclusion is tested. `ShippedProfiles` names
the three Luna comes with and its own doc says the engine *"does not validate against it: a
name it has never heard of is a profile someone defined, not an error"*
([ADR-0017](0017-defaults-plus-customization-everywhere.md),
[ADR-0026](0026-the-log-records-the-gate-decision-not-the-policy.md)). The line that decides
membership: these four are closed because **the engine** decides what they mean; a profile's
meaning lives in configuration, so an unrecognised one is somebody else's vocabulary rather
than a log this build cannot read.

`Status` and `GateKind` are excluded for a duller reason — they are derived during replay
and never written, so there is no stored value to misread.

### A corpus of recorded payloads, replayed in CI

Every action with every field populated, encoded once and checked into `testdata/golden/`,
plus one whole task log with the state it replayed to. The test decodes each recorded
payload, re-encodes it, and insists on identical bytes.

Re-encoding is the part that matters: decoding successfully is not enough, because a field
that silently stops being written decodes fine and comes back missing. This is Temporal's
replay-corpus practice — theirs captured from a running server, Luna's written by the test,
because every action is a plain struct and the point is that the bytes stay put while the
code moves.

## Alternatives considered

- **A `SchemaVersion` on every payload** — rejected as ceremony. A version number is only
  useful if someone knows when to increment it, and the rule would be "when the meaning
  changed", which is exactly the judgement nobody makes reliably. The narrower check asks a
  question with a mechanical answer: is this value one this build knows?

- **Protobuf** — rejected on proportion. It is what Temporal uses for itself and it would
  work, but it contradicts [ADR-0025](0025-pure-go-sqlite-and-blobs-in-the-same-database.md)
  and adds a build step and a schema language to a CLI whose whole store is one SQLite file.

- **Tags without the enum check** — rejected as half. Tags stop a *rename* from changing
  meaning; they do nothing about a *value* whose meaning changed, which is the failure with
  no error attached.

- **Closing `Profile` along with the rest** — rejected, and nearly done by accident. It
  would have broken the extension point in passing, which is why there is now a test
  asserting a project can still define its own.

- **Capturing golden logs by hand** — rejected because there is nothing to capture from.
  Luna has no running service producing histories, and a corpus written by the test is
  reproducible in a way a hand-copied one is not.

## Consequences

- **Positive:** the log's field names are now a decision rather than a side effect of Go
  identifiers, which is what the action names have been since the beginning.

- **Positive:** the failure that has no error attached — a value the engine accepts and
  cannot interpret — now stops the replay and names the value.

- **Positive:** the corpus catches both classes mechanically. It earned its place
  immediately: recording it revealed a JSON tag applied to the wrong struct, so `LoopLimits`
  was writing `Oscillation` while its siblings wrote snake_case.

- **Negative:** the payload format changed, so every log written before this reads
  differently — the fields are there under their old Go names and the new decoder does not
  look for them. In practice this costs nothing today, because there is no production log;
  it would have been expensive in a month, which is why it lands now.

- **Negative:** the corpus has to be re-recorded whenever the format changes deliberately,
  and a re-record that nobody reads is a check that has been switched off. The failure
  message says to read the diff, which is the most a test can do about it.

- **Impacts:** adding a value to a closed enum means adding it to its `Known…` list, or it
  will not decode. That is the intended cost and the test that enumerates each list catches
  a forgotten one.

## References

- Related documents: [ADR-0046](0046-the-log-records-which-flow-it-was-written-under.md),
  [ADR-0048](0048-a-field-read-by-the-reducer-is-history.md),
  [ADR-0025](0025-pure-go-sqlite-and-blobs-in-the-same-database.md),
  [ADR-0026](0026-the-log-records-the-gate-decision-not-the-policy.md),
  [INV-core-2](../invariants/core.md)
