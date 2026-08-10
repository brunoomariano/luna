# ADR-0026: The log records the gate decision, not the policy that produced it

**Status:** Accepted
**Date:** 2026-08-07

## Context

[ADR-0013](0013-named-gate-profiles-per-task.md) settled that a named profile decides which
gates wait, chosen per task. [ADR-0017](0017-defaults-plus-customization-everywhere.md) went
further and put gate profiles on the list of things a user may extend: *"the default comes
installed; the user can disable, edit or create their own."*

The first implementation delivered neither. `ParseProfile` is a closed list of three names,
so a profile someone defined for themselves would be rejected at the command line. And
`WaitsFor` decides by switching on the *name*, so a custom profile that somehow got past the
parser would fall through to the cautious default and ignore whatever its author configured.

Underneath that is a harder question the closed list was hiding. The log stores the profile's
name, and the replay re-derives each gate decision by asking the profile what it does. That
works only while the profile means the same thing it meant when the task ran. The moment
profiles are editable, it stops working: change `turbo` today and every past task replays as
though it had run under the new rules. A run that stopped at two gates would replay as one
that stopped at four, and the history would be quietly wrong.

## Decision

**Profiles are configuration. The log records the decision, not the policy.**

A project defines its own profiles in `.luna/config.toml`, naming the gate kinds that wait:

```toml
[profile.paranoid]
waits = ["confirm", "confirm-write", "review-artifact", "loop-ceiling"]
```

And every `Advance` that meets a gate records **whether that gate waited**, alongside the
profile name that decided it:

```
Advance{Profile: "paranoid", Gate: "confirm", Waited: true}
```

Replay reads the recorded decision instead of re-deriving it. The profile name stays in the
log for the audit trail — so a reader can see *which policy* was in force — but nothing at
replay time consults the policy itself.

This is what makes the two properties hold together:

- **Profiles are editable** (ADR-0017), because editing one cannot reach into the past.
- **A replay reproduces the run as it happened**, because the decision is a recorded fact
  rather than a recomputation.

The three shipped profiles remain, defined the same way as any other. They are defaults, not
special cases.

## Alternatives considered

- **Profiles in configuration, replay recalculates** — rejected. It is simpler and it is what
  the code does today, but it makes editing a profile rewrite how past tasks replay. An
  overnight run that skipped four gates would, after someone tightened `nightly`, replay as a
  supervised run that stopped at all of them. The log would be telling a story that did not
  happen, which is the failure INV-core-2 exists to prevent.
- **Storing the whole policy in every event** — rejected. It survives edits, but it puts a
  configuration blob in the history of every transition, and the log stops being readable as a
  sequence of things that happened. The decision is one bit; the policy that produced it is
  not history.
- **Keeping the three profiles fixed** — rejected because it leaves ADR-0017 describing a
  capability that does not exist. A document that promises more than the code delivers is
  worse than one that promises less.

## Consequences

- **Positive:** a profile can be added or edited without touching the engine, and without
  changing what any past task replays to. The audit gains a fact it did not have — not just
  which policy was in force, but what it decided at each gate.
- **Negative / costs:** `Advance` grows two fields, and every gate encounter writes them. It
  is a small event either way, but the log stops being purely "what was attempted" and starts
  carrying "and here is what the policy said about it".
- **Impacts:**
  - `ParseProfile` stops being a closed list; validation moves to "is it defined in the
    config", which the CLI can answer and the engine no longer needs to;
  - `WaitsFor` stops switching on the name and starts reading a set of gate kinds;
  - a task whose profile was deleted from the config still replays, because replay never asks
    the config anything;
  - the unknown-profile warning changes meaning: it now flags a profile *no longer defined*
    rather than one this build does not know, and it stays useful for exactly the same reason.

## References

- Related documents: [ADR-0013](0013-named-gate-profiles-per-task.md),
  [ADR-0017](0017-defaults-plus-customization-everywhere.md),
  [invariants](../invariants/core.md)
