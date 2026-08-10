# Architecture

## The problem

A workflow written in prose is a **suggestion** to the model, not a
guarantee. It follows almost every time — and it is the "almost" that costs dearly:

- **Reinterprets the instruction.** An agent told to *run* a command decided that
  running meant *printing it*.
- **Abandons the loop.** Repeats the same thing 100 times and on the 101st does
  something else, or simply stops.
- **Loses the role.** In a long session with compaction, the reviewer starts to
  implement and the implementer to review.
- **Breaks the chain silently.** Decides it is not worth passing along, and nobody notices.

All observed in real execution (see [`references.md`](../references.md)).

## The shape of the solution

```
  task origin (any)                    Beads
  ┌──────────────────────┐        ┌──────────────┐
  │ luna task new        │───────▶│ dependency   │
  │ luna import <source> │        │ graph        │
  └──────────────────────┘        └──────┬───────┘
                                         │ what is free
                     ┌───────────────────┴────────────────┐
                     ▼                   ▼                ▼
                 lead(A)             lead(B)          lead(C)      ← one goroutine
                     │                                              per task
        ┌────────────┴─────────────────────────────┐
        │  task A FSM                              │
        │                                          │
        │  stage → role → skill                    │
        │    ├ checks what the stage REQUIRES      │
        │    ├ calls the agent (fresh context)     │
        │    ├ validates what the stage PRODUCED   │
        │    └ writes the handoff and transitions  │
        └──────────────────────────────────────────┘
```

**Parallelism is between tasks, not inside them.** Each task has its own lead and its own
worktree; inside it the stages are sequential.

## The lead is hybrid

On the happy path the lead is **code**: it decides the stage, calls the agent, validates, writes.
Zero token cost, deterministic behavior.

When something goes off the rails — the node failed, the output did not validate, a review found a
problem — **a model decides what to do**: retry, go back a stage, open a
gate or block.

This split matters for a concrete reason. In a real execution of the system that inspired
this design, the state machine had a bug and told the lead to review a document that had already been
reviewed. **The lead refused and escalated to the human** — it had been instructed to obey the FSM,
and still recognized that the instruction made no sense. The deterministic layer gives the
skeleton; the judgment layer catches the skeleton's error. The two protect each other.

## The stage contract

Each stage declares what it **requires** and what it **produces**:

```toml
[stage.build]
role     = "implementer"
skill    = "lsh-code-cycle:build"
requires = ["scenarios", "approach", "worktree"]
produces = ["code", "tests_green"]
```

This sustains three checks:

**1. Static, before running.** Walking the stages in order, is some `requires` not
produced by any earlier stage? If so, the flow is broken on paper — and this is
detectable without executing anything.

**2. On entry.** The FSM does not call the agent of a stage whose `requires` is not in
the context. Without this, the agent would start blind and the failure would look like the model being dumb.

**3. On exit.** The stage does not close without delivering the declared `produces`, and the delivery is
verified **by running the tool** — the file exists, the test passes, the commit resolves.
Format is not checked, reality is.

The third is the one that matters most: it catches the gap **where it is born**, not two stages
later when the symptom is already displaced from the cause.

## Fresh context at each stage

Each stage starts with a clean context, even when the role is the same. It attacks role
erosion head-on — there is no long session to degrade.

**The consequence is structural:** with fresh context, the handoff is the **only** bridge between
stages. If something needed is not in it, the agent starts blind. That is why the contract
above is not bureaucracy — it is what sustains the decision to zero out the context.

Every handoff payload is prefixed with an instruction to reread the role and the rules.
Clean context and reinjected rule are two defenses on the same flank.

## The handoff

Recorded at **every** stage transition, even when the role does not change. The handoff is not
"passing to another agent" — it is the recorded transition. The handoff log is the complete
audit of the task.

What it carries:

- **pointers** — task identifier, origin stage, produced artifacts,
  previous gate decisions;
- **content-addressed snapshot** — the hash of what existed at the moment of the handoff,
  so that the receiver sees exactly what the sender saw, even if the worktree changed
  afterwards.

What it does **not** carry: a prose summary of what the previous stage did. The receiver reads the
real state. Nobody interprets it for them.

The payload is **generated by the system**, not written by the agent. The agent fills structured
fields; the delivered body is synthesized. This eliminates degradation by successive
rewriting along the chain.

## Roles

A role can cover several stages. Each one declares what it owns, what it does **not** own,
and the tools it has access to:

```toml
# src/stock/roles/reviewer.toml
stages      = ["code-review"]
tools_allow = ["Read", "Grep", "Bash"]
tools_deny  = ["Edit", "Write"]
owns        = "..."
not_owns    = "..."
```

The `not_owns` is the specialization mechanism, and separation by negation is deliberate:
whoever writes does not review. Part of the separation is also economic — mutation tests are
expensive, so only one role runs them.

The `tools_allow`/`tools_deny` is the mechanical gating: the FSM restricts the tools before
the agent starts. One file, two consumers — the FSM reads the metadata, the agent reads
the prose.

## Failure

```
node fails
   ↓
model evaluates
   ├── retry (up to 2), with the error in the context
   └── block + notify the human
```

There is no "go back to the previous stage" as a failure exit: the only return to an earlier
stage is the one from a review finding, which invalidates the green when it goes back. Two doors to the
same place would mean one of them forgetting to invalidate.

No infinite retry: that is exactly the loop that does not converge and burns tokens. And no silent
death: every blocked task notifies.

## Gates

A gate stops the task and waits for a human decision. It does **not** hold a live process: after
a short wait in the terminal, the task suspends and releases the slot. Another task uses the
resource while you decide; `luna gate approve` resumes from the exact point.

Which gates stop is decided by **profile**, chosen per task:

| Profile | Behavior |
|---|---|
| `interactive` | every gate waits for a human |
| `turbo` | only the write (commit) waits |
| `nightly` | nothing waits |

These three come installed; a project defines its own in `.luna/config.toml`, naming the gate
kinds that wait. Editing a profile changes what tasks do from here on and leaves the past
alone: every advance records what its gate actually decided, so a replay reads a fact instead
of recomputing one
([ADR-0026](../ADRs/0026-the-log-records-the-gate-decision-not-the-policy.md)).

### The gate carries what needs to be decided

A gate is not just a pause: it delivers to the human **what motivated the stop**. Three
forms, from the simplest to the richest:

- **confirmation** — only asks yes/no (`confirm-repos`: are these the repositories?);
- **artifact for review** — carries what the stage produced, and the human can
  **approve, adjust or reject**. `approve-spec` delivers the `contract`; the approved
  version — possibly edited — is the one that enters the context and that `build` consumes;
- **flow decision** — a loop ceiling blew, and the choice is to continue, abort or
  change course.

The third case is what prevents the infinite loop from becoming an automatic block: when
convergence fails, the human decides, with the history in view.

### How the human sees what is waiting

A suspended task cannot depend on someone remembering to look — that would be silent
death by another name. Three surfaces:

- **`luna gates`** lists everything awaiting a decision: task, stage, gate type, how
  long it has been waiting, and the attached artifact when there is one;
- **`luna gate show <task>`** opens the artifact for reading and editing;
- **`luna gate approve|adjust|reject <task>`** answers and resumes from the exact point.

The audit artifacts (`produces_for_human`) follow the same principle: they are
recorded in the handoff with path and hash, and `luna task show <task>` lists what the
task produced for human reading. A report nobody knows exists is
wasted work.

### Notification is a pluggable piece, not part of the core

The queryable state is the **base**: `luna gates` answers "what is waiting?" without
depending on anything external. On top of it, notification — webhook, Telegram, email — is a
**coupled output**, not a responsibility of the engine.

The split matters for two reasons. The core cannot depend on a channel that may
be down: if the webhook fails, the task stays suspended and stays listed in
`luna gates`. And the right channel is only known by running — which one serves depends on how you
operate, and that is not decided on paper.

That is why the decision about the **channel** is left open on purpose, while the one about the **contract**
is not: any notifier consumes the same state that `luna gates` exposes. The engine emits
the event; who listens is configuration.

> **Open:** which channels will exist and how they are configured. To be decided after running,
> with real usage. What is **not** open: notification does not become a dependency of the
> core, and its absence never makes a suspended task invisible — that is
> INV-core-12.

## State

Two layers, with distinct responsibilities:

- **Beads** governs the order **between** tasks — dependencies, what is free to
  start, atomic claiming.
- **The FSM** governs the stages **inside** a task.

The FSM state lives in **append-only** SQLite: no `UPDATE`, the history is the audit.
Transitions are atomic — killing the process and restarting rebuilds the exact state, because
it was never only in memory.

**The FSM knows no issue tracker.** Plane, Jira, GitHub or nothing: the task enters
through `luna task new` or through an import adapter, which is a command separate from the
core.

## Extension

Everything that defines behavior has a default version and a user version:

| What | Default | Extension |
|---|---|---|
| Stages | the ones in `src/stock/stages/` | disable, edit, create |
| Roles | the ones in `src/stock/roles/` | your own |
| Gate profiles | three | your own, in `.luna/config.toml` — **built** |
| Loops | one | declare others, with rules |
| Skills | Luna's, installed alongside | point to your own directory |

Skills deserve a note: there are **Luna's** (installed in the harness, they explain the structure
to the agent), the **user bundle's** (pointed to in configuration) and the
**developer's cross-cutting ones** (which serve inside and outside Luna). An indexing
command catalogs what exists.
