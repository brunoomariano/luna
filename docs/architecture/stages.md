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
| 1 | `setup` | — | | | `task_id` | `worktree` | |
| 2 | `intake` | `analyst` | | | `task_id`, `worktree` | `briefing`, `kind` | |
| 3 | `diagnose` | `investigator` | | is a bug | `briefing` | `root_cause` | `min_case` |
| 4 | `scenarios` | `gherkin` | approve-plan | | `briefing`, `kind` | `scenarios`, `approach` | |
| 5 | `spec` | `specifier` | approve-spec ⇄ | feature or bug | `approach` | `contract` | |
| 6 | `build` | `implementer` | | 🔁 | `scenarios`, `approach`, `worktree`, `contract`* | `code`, `tests_green` | |
| 7 | `refactor` | `cleaner` | | 🔁 | `code`, `tests_green` | `code` | |
| 8 | `verify` | `verifier` | | 🔁 | `code`, `scenarios` | `ci_green` | `dod_checked` |
| 9 | `qa` | `qa` | | not a chore | `ci_green`, `briefing` | | `qa_report` |
| 10 | `code-review` | `reviewer` | | not docs | `code`, `ci_green` | | `review_report` |
| 11 | `harden` | `hardener` | | feature or bug | `tests_green`, `code` | | `mutation_report` |
| 12 | `architecture` | `architect` | | touches structure † | `code` | | `arch_report` |

The role names are the ones `DefaultFlow()` declares, and
`TestDefaultFlowMatchesDocumentedStages` compares them against this table. Only `setup` is
mechanical — a stage that produces a judgement and names no role is refused by
`AuditRoles` ([ADR-0040](../ADRs/0040-a-role-resolves-to-an-agent-and-mechanical-stages-have-none.md)).

Two stages are gone since this table was first written, both by
[ADR-0062](../ADRs/0062-luna-does-not-integrate-a-task-ends-on-its-own-branch.md): `commit`,
because Luna does not integrate — a task ends on `luna/<task>` and moving that work is a
manual act — and `discovery`, because a task is always about the current repository, so
there is nothing to discover and nobody to confirm it with.

**Every column above is a field of the stage**, including the gate and the condition. That
was not always so: gates were a `switch` over these four stage ids inside the reducer, which
meant a flow replaced under [ADR-0017](../ADRs/0017-defaults-plus-customization-everywhere.md)
got no gates at all, and a renamed review stage silently lost the right to send work back
([ADR-0049](../ADRs/0049-a-stage-declares-its-gate-and-what-a-review-costs.md)). The four
review stages also declare where a finding returns the work and what that invalidates —
`build`, and the green it attested to.

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

**The loop exits through judgment.** When `qa`, `code-review` or `harden` find something, the
model decides by **alignment with the task**: aligned goes back to `build` (and invalidates the
previous green); out of scope becomes a new task and the flow goes on. This is not something a machine can
decide — it is exactly where the judgment layer exists.

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
