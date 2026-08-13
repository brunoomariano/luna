# RFC-0004: An artifact declares where it lives

**Status:** DRAFT
**Last reviewed:** 2026-08-13
**Source issue:** —
**PRD:** —

## Motivation

The first full 14-stage run against a real repository closed every applicable stage and
merged a working feature. It also produced this, at the `verify` stage:

- the contract declares `produces_for_human = ["dod_checked"]`;
- the agent wrote 144 lines of exactly that content — the done-when list, clause by
  clause, each one measured against a running binary;
- it committed them to a file called `verification`;
- the stage **closed green**, and `luna task show` never listed `dod_checked` at all.

The immediate cause is fixed. The node reported `Delivered: owed` — everything the stage
*should* produce, regardless of what happened — so the exit check compared the stage's
promises against a copy of themselves. The agent now declares what it delivered in its
commit message, and `missingFromList` catches the mismatch.

**That fix has a known ceiling, and this RFC is about the ceiling.** The declaration is the
agent reporting; the code decides what it means, but nothing confirms the report. An agent
that writes `Delivered: dod_checked` and commits nothing at all still closes the stage. That
is the same class of self-reported completion [ADR-0028](../ADRs/0028-herdr-status-triggers-verification-it-never-closes-a-stage.md)
rejects for status, applied one level down.

What makes this worth an RFC rather than a patch is the measurement, not the anecdote:

| artifact kind | example | is there a file to check? |
|---|---|---|
| a document | `repos`, `contract`, `qa_report`, `mutation_report` | yes — 8 of 8 landed under the right name |
| a fact about the tree | `code`, `tests_green`, `ci_green`, `commit_sha` | no — `build` delivered `main.go`, `main_test.go`, `tally.go` |
| neither | `worktree`, `kind` | no — a directory, and the string `feature` |

Half the flow's artifacts are not files and never will be. A check that assumes they are
would block six of the twelve stages that run, which is why the cheap version of this was
rejected outright rather than shipped and refined.

## Technical proposal

### Overview (guide-level)

A stage already declares **what** it produces and **how each artifact is verified**
([ADR-0032](../ADRs/0032-the-contract-declares-how-each-artifact-is-verified.md)). It does
not declare **where** the artifact lives, or whether it lives anywhere at all.

The proposal is to make that third thing declarable, for the artifacts where it means
something:

```
[produces.dod_checked]
kind = "document"
path = "reports/"          # the artifact is a file, and it belongs here
```

An artifact with no such declaration behaves exactly as today: the agent's word is the only
record, and the contract check runs against it. An artifact that declares a path becomes
checkable against the commit — `git show <sha>:<path>` either finds it or does not, and that
answer comes from git rather than from the agent.

The prior art is swarm-forge's `squad` branch, which constrains artifacts by kind
(`features/`, `qa/`, `stories/`) and refuses anything outside the repository. Its packet
validator rejects an attach whose file is not where its kind says it should be — the same
shape, arrived at from the same problem.

### Detail (reference-level)

- **Impacted modules:**
  - `src/internal/fsm/stage.go` — the artifact declaration grows a shape;
  - `src/internal/fsm/load.go`, `parse.go` — the TOML that carries it;
  - `src/internal/fsm/fingerprint.go` — **the decision point**, see Drawbacks;
  - `src/internal/fsm/reduce.go` — the exit check consults it;
  - `src/internal/herdr/node.go` — the brief tells the agent the path;
  - `src/stock/stages/*.toml` — 14 stages to classify.

- **Affected contracts:** the stage contract itself, which is the flow's identity.

- **Relevant constraints:** the check must run against the **commit**, not the worktree.
  The worktree is removed when the stage ends, and verifying the tree the agent worked in
  is precisely the incoherence [INV-core-4](../invariants/core.md) exists to prevent.

## Alternatives considered

- **Check that a file named after the artifact exists** — rejected, and this is the one
  worth recording because it is the obvious first idea. It would break `code`,
  `tests_green`, `ci_green`, `worktree` and `commit_sha`: six of the twelve stages that run
  in a `feature` task deliver no file with the artifact's name, correctly. Measured, not
  reasoned about.

- **Have the agent declare a path as well as a name** — rejected as insufficient on its
  own. It moves the same unverified claim one field to the right; if the agent's word is
  what we are trying to stop relying on, asking it for more words does not help.

- **Do nothing, and rely on the declaration** — the current state, and defensible. The
  measured cost so far is one mis-named file in nine, and the eight artifacts that are
  documents landed under the right name unprompted. This RFC exists because the *class* of
  failure is silent, not because the rate is high.

## Drawbacks

- **It probably changes the flow fingerprint.** `fingerprint.go` hashes `Produces` and
  `ProducesForHuman` because the exit check decides whether a past `Complete` closed. A new
  per-artifact field is part of that check, so it belongs in the hash — and a changed hash
  stops every open task from replaying ([ADR-0046](../ADRs/0046-the-log-records-which-flow-it-was-written-under.md)).
  Keeping it out of the hash is possible and incoherent: a piece of the contract that is not
  part of the contract's identity.

- **It forces a taxonomy the domain does not have.** `worktree` is a directory. `kind` is
  the string `feature`. `tests_green` is an exit code. Classifying twelve artifacts means
  making arbitrary calls on the ambiguous ones, and every project that writes its own flow
  ([ADR-0017](../ADRs/0017-defaults-plus-customization-everywhere.md)) inherits those calls.

- **It makes the artifact name partly a path.** Today `dod_checked` is a name in a contract.
  Afterwards it is a name plus a location, and the two can disagree — a third thing to keep
  consistent across 14 stage files.

## Impact and migration

- **Data/persistence:** none directly; the log is unchanged. But see the fingerprint
  question — a changed flow identity is a migration in everything but name.
- **Compatibility:** a flow written before this must keep loading, with every artifact
  behaving as it does today. The field is additive or this is not shippable.
- **Observability:** the evidence would gain a real verdict for artifacts that are
  documents — `passed (existence)` becomes `passed (targeted): found at reports/dod_checked`.
- **Rollback surface:** removing the field returns to the declaration-only check, which is
  what ships today.

## Rollout plan (phased)

1. **Phase 1 — decide the fingerprint question.** Nothing else can be built until it is
   settled, because it determines whether this is additive or a migration. Likely its own
   ADR.
2. **Phase 2 — the declaration, unused.** The field parses, `luna flow check` reports it,
   and nothing consults it. A flow with paths declared and a flow without behave alike.
3. **Phase 3 — the check, on documents only.** The exit check confirms a declared path
   exists in the delivered commit. Artifacts with no declared path are untouched.
4. **Phase 4 — classify the shipped stages.** The 14 stock stages declare paths where they
   have them. This is where the taxonomy argument actually has to be had, and doing it last
   means the mechanism is proven before the argument starts.

- Feature flag? No — an undeclared path is the flag.
- Rollback strategy: stop declaring paths.

## Open questions

- [ ] **Does the path belong in the fingerprint?** The whole shape of phases 1–2 depends on
      this. Argument for: it is part of the exit check, and the exit check is what the
      fingerprint exists to protect. Argument against: it changes where an artifact lives,
      not whether the stage closed, and freezing every open task for that is
      disproportionate.
- [ ] **Is `path` a file or a directory?** `features/` in swarm-forge is a prefix, and the
      agent names the file. A fixed filename is stricter and gives the agent nothing to get
      wrong.
- [ ] **What about the artifacts that are facts?** `ci_green` already has a real verifier
      that runs a command. Is "has a command verifier" the same distinction as "is not a
      document", or are those two different axes that happen to line up in the shipped flow?
- [ ] **Does this subsume the declaration, or sit beside it?** If a path is checked against
      the commit, the agent's `Delivered:` line adds nothing for that artifact — but it is
      still the only signal for the ones with no path.

## References

- Related ADRs: [ADR-0032](../ADRs/0032-the-contract-declares-how-each-artifact-is-verified.md)
  (the contract declares how each artifact is verified),
  [ADR-0028](../ADRs/0028-herdr-status-triggers-verification-it-never-closes-a-stage.md) (a verdict, not a
  self-report), [ADR-0021](../ADRs/0021-produces-for-human-is-a-separate-contract-field.md)
  (why the two produce fields are separate),
  [ADR-0046](../ADRs/0046-the-log-records-which-flow-it-was-written-under.md) (the
  fingerprint this would disturb),
  [ADR-0055](../ADRs/0055-one-worktree-per-task-and-role-branched-from-the-last-delivery.md)
  (the commit the check would read)
- Prior art: [references](../references.md) — swarm-forge's `squad` branch constrains
  artifacts to declared prefixes and validates them against a sha.
