# RFC-0002: The lead is an agent, and the commit is the handoff

**Status:** DRAFT
**Last reviewed:** 2026-08-12
**Source issue:** —
**PRD:** —

> **Nothing is built from this yet.** The merge question that was blocking §3 has been
> answered — see "How swarm-forge solved this" below — and the answer changed one part of the
> design.

## Motivation

Luna works, and the last seven rounds of hardening were mostly spent defending a design
decision rather than advancing the product. The store was local and append-only, so it needed
a flow fingerprint, a history-versus-policy classification, payload tags, closed enums, a
golden corpus and optimistic concurrency — six pieces of machinery, all correct, all in
service of one choice.

Meanwhile the things that make Luna useful — coordinating specialised agents, verifying what
they produced, asking a person when it matters — advanced very little.

The observation that prompted this: **too much of the work was reinventing something that
already exists.** A tracker exists. Git exists. A terminal multiplexer exists. A swarm
pattern exists and is published. Luna's contribution is the **contract and the
verification** — deciding what a stage owes and proving it delivered — and that is a smaller,
sharper thing than what has been built around it.

So this proposes a different assembly of the same idea, using parts that are already there:

| Part | Was | Becomes |
|---|---|---|
| task registry | SQLite, one per worktree | **beads**, central |
| audit trail | append-only event log | **git commits** |
| handoff | pointers plus a content-addressed snapshot | **the commit itself** |
| lead | a Go loop | **an agent**, driven by closed orders |
| worktree | one per task | **one per task and role, ephemeral** |
| flow control | the FSM, in code | the FSM, in code — unchanged |

The last row is the point. Everything else can be borrowed; that one cannot, and it is
[INV-core-1](../invariants/core.md).

## Technical proposal

### Overview

```
   person
     │
     ▼
 conversation agent  ──── luna task new ────▶  luna CLI
     ▲                                            │
     │                                     ┌──────┴──────┐
     │                                     │     FSM     │  stages, agent catalogue,
     │                                     │  (in code)  │  contract, verification
     │                                     └──────┬──────┘
     │                                            │  closed order
     │                                            ▼
     └──── blocked, via the knob ─────────  lead (an agent)
                                                  │  opens a worktree, starts an agent
                    ┌─────────────────────────────┼─────────────────────┐
                    ▼                             ▼                     ▼
              gherkin                      implementer              reviewer
           wt-…-gherkin                  wt-…-implementer         wt-…-reviewer
                    │ commit                      │ commit               │ commit
                    └─────────────────────────────┴──────────────────────┘
                                                  │
                                        Luna merges, deterministically
                                                  │
                                        Luna verifies over the commit
                                                  │
                                         verdict ─┴──▶ back to the lead

  beads  ◀── task, dependencies, status, what is free ──▶  luna
  herdr  ── hosts panes, creates worktrees, notifies ───   everything
```

### The cycle of one stage

```
lead              ──▶  luna next LUNA-1
                       │
                       ├─ the FSM reads: current stage, contract, what was delivered
                       └─▶ a CLOSED ORDER, not a suggestion:
                             stage=build  agent=implementer
                             worktree=wt-repo-LUNA-1-implementer
                             base=<sha of the previous stage>
                             brief=<…>  deny=Edit,Write

lead              ──▶  herdr: worktree.create, agent.start, agent.prompt
agent works       ──▶  commits in its own worktree
lead              ──▶  luna done LUNA-1 --commit <sha>
                       │
                       ├─ Luna merges the agent's work, deterministically
                       ├─ Luna verifies over the resulting commit
                       ├─ contract: did it deliver what it owed?
                       ├─ scope: did it prove as much as declared?
                       │
                       └─▶ passed: beads advances, next order
                           failed: a retry order, or a block → the knob
```

### The lead obeys, it does not interpret

This is the decision the rest hangs on, and it is the one most at risk of eroding
[INV-core-1](../invariants/core.md).

The lead becomes an agent so that a person can talk to it — that is the whole reason. But an
agent that *decides* what happens next is the model holding flow control, which is the thing
Luna exists to prevent.

The resolution is that **the FSM emits an order, not advice**. `luna next` returns a literal
command: which stage, which agent, which worktree, which base commit, which brief, which
tools denied. The lead executes it and reports back. It chooses nothing about the happy path.

Where the lead does exercise judgement is exactly where ADR-0002 already allows it: **what to
do about a failure**, bounded by the autonomy knob. That carve-out exists and is not being
widened here.

### One worktree per task and role, and a merge that is code

Each role works in `wt-<repo>-<task>-<role>`, branched from the current integrated state, and
finishes with a commit. The worktree is removed when the stage ends. The next agent's base is
the previous commit, so the handoff is the artifact rather than a description of it.

This is swarm-forge's shape, deliberately — and the part Luna was going to do differently
turns out to be the part swarm-forge has since rebuilt.

#### How swarm-forge solved this

The studied clone was pinned at 2026-07-10 and `main` is still there. The work moved to a
branch called `squad`, active through 2026-08-12, and the merge stopped being prose.

`merge_and_process` is still defined nowhere — it was reported as
[issue #29](https://github.com/unclebob/swarm-forge/issues/29), still open, with a Codex
agent passing it to Bash and stopping on `command not found`. A fork documented the damage in
the meantime: `git merge -X theirs`, an agent discarding its own work in silence, and *"no
consistent conflict-resolution policy"*.

What replaced it, verified in the branch's source:

**One owner of the shared git, enforced by code.**

```clojure
(defn ensure-main-git-owner!
  "Reject merge-ready/accept-merge unless caller is the daemon owner."
  [op]
  (when (and (= "daemon" (main-git-owner)) (not (main-git-allowed?)))
    (exit! 3 (str "MAIN_GIT_OWNER: only squadd may run " op) ...)))
```

Not a convention in a prompt — an exit code. The role prompt says the same thing in prose,
but the prose is redundant.

**A dry run, separate from the merge, in a throwaway worktree.**

```clojure
(defn dry-run-merge [root commit]
  (with-merge-check-worktree root
    (fn [worktree] (sh-at worktree "git" "merge" "--no-commit" "--no-ff" commit))))
```

A conflict becomes `merge_blocked` without the main repository being touched.

**No automatic resolution, deliberately.** Searching the whole branch for `-X theirs`,
`--ours` or `rerere` returns nothing. Having watched an agent silently discard its own work,
they refused the shortcut.

**Conflict resolution is a role, and its output re-enters the pipeline.** A dedicated,
transient `merger` agent proposes a commit; the same deterministic dry-run-then-merge path
validates and applies it. The model proposes, the code decides — never the reverse. Two
numeric limits guard it, both scar tissue from real bugs: `max_merger_depth 2` for endless
`*-merge-merge` chains, and a singleton merger against *"parallel-merger thrash"*.

#### What Luna takes from that

Four amendments, three adopted outright and one that changes this RFC's own design.

1. **One owner of the shared git.** Luna is the only thing that merges, and it is enforced
   rather than asked for. An agent works in its own worktree and commits there; nothing else.
2. **The dry run is the verification, and its result is the verdict.** This maps onto
   [ADR-0024](../ADRs/0024-the-reducer-is-pure-verification-runs-outside.md) exactly: the
   merge check runs outside the engine and `merge_ready` / `merge_blocked` arrives inside the
   action.
3. **Conflict resolution is a stage with a contract, bounded by a ceiling.** It cannot be
   deterministic — `git merge` is decidable, resolving a semantic conflict is not — so the
   indeterminacy is isolated in a stage whose output goes back through the deterministic
   path. The loop ceilings of [ADR-0023](../ADRs/0023-three-separate-loop-ceilings.md)
   already exist for this shape.
4. **The worktree is per task *and* role, and ephemeral.** This is the amendment. The
   original sketch said one worktree per role, which is what the fork had — and its own issue
   documents why that fails: role branches are *"permanent and never reset"*, so divergence
   *"compounds at every hop"* and feature N faces N-1 features of drift. swarm-forge creates
   the worktree from `HEAD` at each assignment and destroys it on retire. Luna does the same:
   `wt-<repo>-<task>-<role>`, branched from the current integrated state, removed when the
   stage ends.

### What beads holds, and what git holds

- **beads** — the task, its dependencies, what is free to start, the current stage, whether
  it is blocked. Queryable, which is what makes `luna gates` a query instead of a replay of
  every task.
- **git** — how it got there. Every stage ends in a commit, so `git log` is the history and
  the diff is the evidence.

Neither replays. That is the trade this makes, and §"Drawbacks" is honest about it.

## Alternatives considered

- **Keep the local store and add beads alongside it** — rejected because two registries
  disagree, and the disagreement surfaces at the worst moment. It would also keep every piece
  of machinery this RFC exists to retire.

- **Keep the lead as a Go loop and put the conversation somewhere else** — rejected because
  the conversation is the point of the lead being an agent. A person talks to the thing
  running the task; splitting them means the conversation layer has to reconstruct what the
  loop knows.

- **One worktree per task, as today** — rejected because it serialises the agents and makes
  the handoff a payload again. The per-role worktree is what makes "whoever writes does not
  review" structural rather than declared.

- **Let the model do the merge, as swarm-forge does** — rejected outright. It is the failure
  the study named, and reproducing it would give up Luna's only real advantage over a prompt
  chain.

## Drawbacks

- **Replay is gone.** A tracker holds state, not the sequence of actions that produced it.
  Questions like "what did the reducer see at step 4" stop being answerable, and the
  reproducibility that came free with a pure reducer over an event log becomes something git
  provides less precisely.

- **The merge is new surface, and it is the hard part.** Conflicts between roles are real,
  and a deterministic merge that hits one has to do something legible rather than something
  clever.

- **The lead's obedience is enforced by prompt, not by types.** A Go loop could not decide to
  skip a stage; an agent can. The closed order narrows the opening; it does not close it.

- **Six pieces of hardening are discarded.** They were correct and they were tested. Saying
  so plainly matters more than pretending the work carries over.

## Impact and migration

### What is retired

```
src/internal/store/                  the log, the codec, the golden corpus
src/internal/fsm/fingerprint.go      ADR-0046
src/internal/fsm/enum.go             ADR-0050
src/internal/fsm/classification_test.go  ADR-0048
src/internal/lead/lead.go            the loop
src/internal/cli/flow.go             flow check
```

Rounds 1, 2, 3 and 7 of the extended study are retired almost entirely. They were sound work
on a store that is being replaced.

### What survives, and why

```
src/internal/fsm/flow.go stage.go        the 14 stages and the contract
src/internal/fsm/verifier.go evidence.go scope, verdict, Satisfies
src/internal/fsm/role.go condition.go    the agent catalogue
src/internal/fsm/audit.go                the static check
src/internal/node/delivered.go           verifying over the commit
src/internal/node/verify.go              Shell.Prove
src/internal/herdr/                      gating, worktrees, prompt, notification
```

`delivered.go` is worth naming: it was built last round to verify a checkout of what was
committed rather than the working tree. In this model the commit **is** the handoff, so it
lands exactly where it is needed. That is luck rather than foresight.

### The invariants

| | |
|---|---|
| **INV-core-1** flow is never decided by the model | **unchanged** — the reason all of this exists |
| **INV-core-3** contract in, contract out | **unchanged** — becomes the core |
| **INV-core-4** verified by running the tool | **unchanged** — already verifies over the commit |
| **INV-core-7** whoever writes does not review | **stronger** — separate worktrees make it structural |
| **INV-core-2** state is never overwritten | **same rule, new medium** — git instead of SQLite |
| **INV-core-5** every stage starts clean | **unchanged, and stricter** — see below |
| **INV-core-6** the handoff carries no prose | **rewritten** — see below |
| **INV-core-12** what needs a human is findable | **unchanged, easier** — a beads query, not a replay |

**INV-core-5** holds for every case, including the same role running twice. An implementer
called again after a review starts fresh: swarm-forge lets the agent live on in its worktree,
and this deliberately does not. The commit carries the context, so the session does not
have to.

**INV-core-6** is rewritten rather than dropped. Its mechanism — a system-synthesised payload
of pointers and a snapshot — does not survive. Its concern does: what it guards against is
*chained interpretation*, A summarising for B who summarises for C. A commit is not a
summary, it is the artifact. The rule becomes:

> **The handoff is the commit; the message describes, the diff decides.**

And Luna verifies over the **diff**, never over the message alone. That keeps the guarantee
that nobody interprets on the receiver's behalf.

**INV-core-8** (no failure is silent) is unlisted because it is untouched in substance: a
block still notifies, and the notifier already delegates to herdr.

## Rollout plan (phased)

1. **Phase 0 — settle the merge.** The swarm-forge investigation lands, and the merge's shape
   is decided. Nothing is written before this.
2. **Phase 1 — the order.** `luna next` and `luna done` as the interface between the FSM and
   the lead, still over the current store. The contract and verification are untouched, so
   this is additive and reversible.
3. **Phase 2 — beads.** The registry moves. The store's retirement happens here, and it is
   the irreversible step.
4. **Phase 3 — per-role worktrees and the merge.** The topology changes and the merge is
   implemented.
5. **Phase 4 — the lead as an agent.** Its brief, the knob, the conversation.

- Feature flag? no. The phases are ordered so each leaves the system usable.
- Rollback: phases 1 and 2 are separable; after phase 3 the old topology is gone.

## Open questions

- [x] **How does swarm-forge merge today?** Answered above: deterministically, by a daemon,
      with the model gated out by an exit code.
- [x] **Does the conflict-resolving stage exist from the start?** Yes, with a dedicated
      `merger` role. Refusing and asking a person was the cheaper floor, but a conflict is
      ordinary rather than exceptional once several agents share a base, and a system that
      stops on every one of them is a system a person babysits.
- [x] **How does a blocked merge become visible?** The watchdog notices and tells the lead,
      which brings it to the person. swarm-forge's own `bugs.md` records the failure this
      avoids — a multi-hour stall where the dashboard never said why: *"UI never surfaced
      'merge conflict on acceptance/runner.clj'"*. Nothing waits for hours with nobody
      knowing.

      Worth noting what this changes: the watchdog interface was **removed** in the last
      round, because a replayed state carries no clock and nothing but a test fake could
      implement it. A blocked merge is the first thing it can genuinely observe — a fact with
      a timestamp, sitting in the registry — so the watchdog comes back with something real
      to watch rather than as an interface waiting for a purpose.
- [ ] **Does the lead see the whole flow, or only its next order?** Only the next order keeps
      it obedient; seeing the flow makes it a better conversational partner. These pull
      against each other.
- [ ] **What replaces replay for debugging?** "Why is this task here" was answerable by
      replaying the log. `git log` plus beads' history is less precise, and it is worth
      knowing how much less before phase 2.
- [ ] **Where does the autonomy knob live?** [PRD gate-0001](../PRDs/gate/gate-0001-an-autonomy-knob-over-the-flow.md)
      records the question and this RFC makes it load-bearing rather than optional.
- [ ] **Does the conversation agent and the lead being separate still make sense?** They were
      separate because the lead was code. Now both are agents.

## References

- Issue: —
- PRD: [gate-0001](../PRDs/gate/gate-0001-an-autonomy-knob-over-the-flow.md) (the knob),
  [node-0001](../PRDs/node/node-0001-context-usage-per-running-agent.md) (context per agent)
- Related ADRs: [ADR-0001](../ADRs/0001-flow-control-out-of-model.md),
  [ADR-0002](../ADRs/0002-hybrid-lead.md),
  [ADR-0003](../ADRs/0003-parallelism-between-tasks.md),
  [ADR-0006](../ADRs/0006-fresh-context-per-stage.md),
  [ADR-0045](../ADRs/0045-the-agent-is-fallible-except-at-the-evidence-boundary.md)
- Prior art: [references](../references.md) — beads, swarm-forge, herdr
