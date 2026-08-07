# Default stages

These are the stages that Luna ships installed. They are not mandatory: they can be
disabled, edited or replaced, and new ones can be created.

Each stage declares the role that executes it, the skill it uses, what it **requires** to
start and what it **produces** when it finishes. The contract is what prevents a stage from starting
blind or closing halfway.

What a stage produces comes in two natures, and the distinction belongs to the contract, it is not
cosmetic:

- **`produces`** — what the **flow** consumes. Some stage ahead declares it in
  `requires`, and the static check guarantees there is someone producing it.
- **`produces_for_human`** — what **only a person** reads: audit reports,
  opinions, diagnoses. Checked on exit just like `produces` (the stage does not close
  without delivering), but **exempt from the static check** — it is not a defect that nobody
  consumes it.

| # | Stage | Role | Gate | Condition | Requires | Produces for the flow | Produces for human |
|---|---|---|---|---|---|---|---|
| 1 | `discovery` | — | confirm-repos | | `task_id` | `repos` | |
| 2 | `setup` | — | | | `repos` | `worktree` | |
| 3 | `intake` | analyst | | | `task_id`, `worktree` | `briefing`, `kind` | |
| 4 | `diagnose` | — | | is a bug | `briefing` | `root_cause` | `min_case` |
| 5 | `scenarios` | gherkin author | approve-plan | | `briefing`, `kind` | `scenarios`, `approach` | |
| 6 | `spec` | — | approve-spec ⇄ | feature or bug | `approach` | `contract` | |
| 7 | `build` | implementer | | 🔁 | `scenarios`, `approach`, `worktree`, `contract`* | `code`, `tests_green` | |
| 8 | `refactor` | cleaner | | 🔁 | `code`, `tests_green` | `code` | |
| 9 | `verify` | — | | 🔁 | `code`, `scenarios` | `ci_green` | `dod_checked` |
| 10 | `qa` | QA tester | | not a chore | `ci_green`, `briefing` | | `qa_report` |
| 11 | `code-review` | reviewer | | not docs | `code`, `ci_green` | | `review_report` |
| 12 | `harden` | hardener | | feature or bug | `tests_green`, `code` | | `mutation_report` |
| 13 | `architecture` | architect | | touches structure † | `code` | | `arch_report` |
| 14 | `commit` | — | confirm-write | | `ci_green`, `code` | `commit_sha` | |

🔁 = takes part in the convergence loop. ⇄ = gate that **carries an artifact** for review.
† = condition over a **fact discovered during execution**, not over the nature of the
task: you only know the change touched the structure after looking at what `build`
produced. That is why the stage condition consults the whole context (nature, artifacts
already produced and discovered facts), and not just the `kind`.
\* = `contract` is only required when the `spec` stage entered the flow (feature or bug); in
`chore` and `docs` it is skipped and `build` does not ask for it.

> **Not implemented yet.** The conditional `requires` described in the `*` above depends on
> a mechanism that has not been chosen yet — the alternative is under A/B evaluation.
> Until then, `DefaultFlow()` does **not** declare `contract` in the `requires` of `build`, and the
> code carries the gap marked. Doc and code diverge here on purpose: the table
> describes the destination, the code describes the present.

## Design notes

**Stages 10 to 13 are conditional by nature.** A mutation test on a one-line change
is ceremony — and ceremony trains the human to ignore the process. The condition for
each one is in the table.

**Whoever writes does not review.** The `implementer` does not do `code-review`; the `cleaner` does not run
`harden`. The separation is in the roles, not in the model's good will.

**The loop has an exit by judgment.** When `qa`, `code-review` or `harden` find something, the
model decides by **alignment with the task**: aligned goes back to `build` (and invalidates the
previous green); out of scope becomes a new task and the flow goes on. This distinction is not
mechanizable — it is exactly where the judgment layer exists.

**The loop has three ceilings, counted separately.** It is not a single counter: each ceiling
detects a different pathology, and summing them into one number would hide precisely the difference.

| Ceiling | Counts | Detects |
|---|---|---|
| `max_rounds` | total rounds of the loop | the loop that does not end |
| `no_progress_rounds` | consecutive rounds without functional change | the loop that spins without producing |
| `oscillation_rounds` | consecutive rounds alternating between the same states | the loop that undoes what it just did |

Once any of them blows, the loop **opens a gate** instead of continuing to iterate — it does not
block. The distinction matters: the loop that does not converge is not a failure, it is a decision to make.

The loop round counter is **separate from the failure retry counter**
(see [ADR-0011](../ADRs/0011-failure-retry-rollback-or-block.md)): they are distinct things,
with distinct ceilings, and uniting them would make a transient failure consume the convergence
budget.

Loops are declared in a file, with their own rules — the three ceilings are configurable
per loop.

**A gate can carry an artifact for review.** The clear case is `spec`: it produces the
`contract`, and the `approve-spec` gate delivers that contract to the human, who can **approve,
adjust or reject**. Only the approved version enters the context — and it is the one that `build`
consumes as `requires`.

This makes the gate more than a pause: it is the point where the human **edits the artifact** that
the next stage will use. Without this mechanism, the `contract` would be produced and never
consumed, and the human review would happen outside the system, leaving no trace.

- **approve** — the artifact enters the context as it is;
- **adjust** — the human edits; the edited version is the one that enters, and the adjustment is
  recorded in the handoff;
- **reject** — the artifact does not enter; the stage that produced it runs again with the rejection
  in the context.

## What is not a stage

Four things that existed in the previous flow and did not become states:

- **`prime`** and **`close`** are effects — loading memory and writing the summary. They become
  entry and exit actions, not stages.
- **`triage-type`** is a predicate: it decides whether `diagnose` enters. It becomes a transition
  condition.
- **`reread-issue`** existed only because a preparation mode skipped `discovery`. With
  external state, the task already arrives loaded.

A state that has no work of its own should not be a state.

## Before the flow

The flow starts from an already written task. Turning a raw demand into an executable task is
`luna refine`, a separate command — not a stage. Deciding **what** to build remains
the human's work.
