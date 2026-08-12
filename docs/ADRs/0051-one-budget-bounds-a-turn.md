# ADR-0051: One budget bounds a turn

**Status:** Accepted
**Date:** 2026-08-12

## Context

[ADR-0034](0034-the-watchdog-delegates-detection-and-owns-the-verdict.md) settled that herdr
detects and Luna decides, and that the limits come from the profile. That part holds and is
not what this revises.

It also introduced **two** budgets, on an argument that reads well:

> a real build legitimately runs silent for many minutes

So `Budgets` carries `Idle` (30 minutes, for an agent that should be reacting) and `Tool`
(2 hours, for one that is waiting on something long). `Budgets.For(toolInFlight bool)`
chooses between them.

Wiring the watchdog turned up the problem: **`For` has no caller, and cannot have one.**

The distinction needs Luna to know a tool is in flight, and nothing tells it. herdr reports
five statuses, and the relevant one is `working` — which means *the agent is doing
something*, not *the agent is waiting on a command*. An agent thinking and an agent
compiling are both `working`.

Worse, the deadline Luna does send is not measuring what its name says. `agent.prompt`
carries a single `timeout_ms` covering the whole wait, and the wait ends when the agent
reaches `idle`, `done`, `blocked` or `unknown`. While a build runs the agent is `working`, so
the wait does not return — the 30-minute `Idle` budget has been bounding **the entire turn**,
including any tool the agent ran inside it.

That makes the shipped default wrong in the direction that costs a task: a turn that includes
`make ci` gets thirty minutes, and the two-hour budget written for exactly that case is
parsed, validated, and discarded. `tool_budget = "6h"` in a profile does nothing, silently —
the failure mode `ParseBudget` refuses malformed durations to avoid, arriving through the
field that was not checked.

## Decision

**One budget, and it bounds a turn.**

`Budgets` collapses to a single `Turn` duration: how long a prompt may take from sending to
the agent settling, tool time included. It is what the code has been measuring since herdr
became the runner, now named for what it does.

The default rises from 30 minutes to **2 hours** — the value ADR-0034 chose for the case that
actually applies, since every turn may contain a build.

`Idle` and `Tool` are gone, along with `For`. A profile naming either is refused at load
rather than accepted and ignored: an author who wrote a budget believes they set one.

**What ADR-0034 got right and this keeps:** detection is delegated, the verdict is Luna's, a
stall is a `Block` with a named reason and never a `Fail`, and it does not spend the retry
budget. The two-budget taxonomy is the only part revised.

## Alternatives considered

- **Keep both and ask herdr for tool state** — rejected because the state does not exist.
  Distinguishing "thinking" from "compiling" would mean parsing pane output, which is
  scraping a screen for a fact — the thing ADR-0028 refuses for verdicts and which would be
  no more reliable here.

- **Keep both and infer tool time from elapsed silence** — rejected as a guess dressed as a
  measurement. It would also reintroduce, by the back door, the wall-clock cap that multica
  removed after it killed legitimate long runs.

- **Keep `Tool` as the only budget and drop `Idle`** — same outcome, worse name. What is
  bounded is a turn, and calling it a tool budget would keep implying a distinction the code
  cannot make.

- **Refuse `tool_budget` at parse and leave the rest alone** — rejected as treating the
  symptom. The unused field is a consequence of a taxonomy that does not fit; renaming the
  refusal would leave the 30-minute default bounding builds.

## Consequences

- **Positive:** the budget means what it says. A profile that sets it gets what it set, and
  the value in the log is the value that applied.

- **Positive:** the default stops killing legitimate work. Thirty minutes for a turn
  containing `make ci` was a wrong answer that no configuration could fix, because the field
  that would fix it was ignored.

- **Negative:** a profile written against ADR-0034 stops loading, naming a field that no
  longer exists. That is the intended failure — the alternative is the silent discard being
  fixed — and there are no profiles in the wild to break.

- **Negative:** Luna loses the ability to express "react quickly, but take your time
  building", which is a real thing to want. It never had it in practice, and getting it back
  needs a signal herdr does not currently produce.

- **Impacts:** `fsm.Budgets`, the profile parser, and the runner's deadline. `luna task show`
  and any profile documentation that named the two budgets.

## References

- Related documents: [ADR-0034](0034-the-watchdog-delegates-detection-and-owns-the-verdict.md)
  (revised here in one part), [ADR-0019](0019-inactivity-watchdog.md),
  [ADR-0013](0013-named-gate-profiles-per-task.md),
  [PRD node-0002](../PRDs/node/node-0002-no-progress-detection-and-the-shape-of-timeouts.md)
