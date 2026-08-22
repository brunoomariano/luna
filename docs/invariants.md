# Invariants

Rules that always hold, regardless of implementation. Violating one is not a bug — it is
Luna ceasing to be Luna.

There are five. The list is short on purpose: an invariant that forbids what the code
permits teaches people to stop believing the list.

---

## INV-1 — Delivery is verified by running the tool, over what was delivered

A stage does not close because the agent said it was done. It closes because a command
returned zero, the blob is in the store with its hash, the commit resolves to exactly one
object and that object is a commit.

**Why.** Well-formed output can describe something that does not exist. This is the one
piece that worked without exception in real cycles, and it is what makes the rest
unnecessary to trust.

The verification runs over **what the stage delivered**, not over whatever was left in the
working tree. The failure mode it closes is not sabotage — it is incoherence: an
uncommitted file, a local `.env`, a stale build artifact. A tree that passes and a
delivery that does not.

**Floor, stated plainly.** Not every artifact is mechanically provable. Evidence carries
the **scope** of what was proven — `full`, `targeted`, `existence`, `human` — and
`existence` is the honest floor for prose: the file is there, nothing more is claimed.
Scope has no upgrade path.

**What violates it.** Accepting a delivery because a field came filled in; validating by
schema instead of execution; a verdict about a tree that is not the delivery it names.

**What it does not claim.** This is not containment. An agent controls what it commits,
and therefore what is verified. Confinement is INV-4's job.

**The case that got through.** A stage that delivered *nothing* used to pass. An empty
commit reached `CheckoutAt`, which read it as `HEAD` and resolved it against the main
repository — so the command ran over whatever was already committed there and exited zero.
Measured on TALLY-3: `build` recorded `make test → 0` while its branch sat on the base
commit with a clean worktree. The green was true, and it was true about code the stage did
not write. A stage that owes a committed artifact and adds no commit now fails; a pure
verification stage is different because the commit it received is the thing it is asked to
prove. A repository with no commit at all is still the first stage of the first task, and
still verifies its tree.

The other half of it took a second run to find. That guard asked whether a commit
*existed*, and a stage that adds none hands back the base — which exists, and resolves. So
the check ran over the code the stage was given and passed, because that code was already
green when the stage received it. Measured on TALLY-5: `build` was billed $0.64 over 17
turns, ended clean at its base with none of the feature written, and closed `tests_green`
as `targeted` and `passed`. A delivery equal to the base is not a delivery.

**Covered by.** Verifier scope tests in `internal/fsm`; the delivered-tree test,
`TestAStageThatCommittedNothingDoesNotPassOnSomebodyElsesCode` and
`TestAStageThatDeliveredNothingNewIsNotProvenByItsBase` in `internal/node`.

---

## INV-2 — State is append-only, and the log is the state

No `UPDATE`, no `DELETE`. Every change is a new record. Killing the process and restarting
rebuilds the exact state, because it was never only in memory.

**Why.** The history *is* the audit, and for unattended operation the evidence is the
product. A projection can be rebuilt; a discarded fact cannot.

The log records which flow it was written under — a fingerprint of the stage ids, in
order, with what each requires and produces. Replaying a log against a different flow is
refused rather than attempted, because a renamed stage used to replay as the new name and
a stage inserted mid-flow made a task re-run finished work, both in silence.

**What violates it.** Any `UPDATE` on the state table; state kept only in memory between
transitions; compaction that discards history; a replay that guesses.

**Covered by.** Replay tests in `internal/store`; the fingerprint refusal test.

---

## INV-3 — No stage starts without what it requires, nor closes without what it produces

Every stage declares `requires` and `produces`. Three checks: static (walking the stages in
order, before anything runs), on entry, and on exit.

**Why.** It distinguishes "the model got it wrong" from "the model did not receive what it
needed". Without it, the failure surfaces two stages later, when the symptom has already
moved away from the cause.

This includes `produces_for_human` — the report nobody downstream consumes. If its
delivery depended on the flow feeling it missing, it would never be demanded.

**What violates it.** A stage with no contract; a `produces` marked delivered without
verification; a transition that ignores a missing `requires`.

**Covered by.** The static check in `internal/fsm`; entry and exit tests per stage.

---

## INV-4 — Every agent runs inside the sandbox, and hands artifacts over rather than scattering them

Luna starts agents inside `ai-jail`. Scratch artifacts — the ones that exist to cross
stages or to be read by a human — are handed to Luna's store through a socket opened
inside the stage's worktree, the only position reachable under Landlock. Luna is the sole
writer, and the producing stage is recorded by the server, never taken from the agent's
request.

**Why.** Containment is delegated, not built: reimplementing a sandbox would be a worse
copy of what the layer below already does. And an artifact that is neither committed nor
handed over is an artifact nobody can find — which is the same as one that does not exist.

**What violates it.** An agent started outside the sandbox; an artifact written to an
unrecorded path; the store trusting a stage name that came from the agent.

**What containment must still let through.** Delegating containment means the sandbox
decides what an agent can reach, and two of its defaults make a stage unable to deliver
at all. The network is one: without it the harness blocks forever on a connection it
cannot make, and never reaches a model. Git metadata is the other: a stage's checkout is
a worktree whose `.git` points into the main repository, outside the jail, so without
`--worktree` every git command answers `fatal: not a git repository` and the agent has
no way to commit what it built. Both were found by a run rather than by a test — five
stages of TALLY-5 billed $3.33 and left HEAD on the base commit — because a fake sandbox
has no boundary to get this wrong. The identity a commit needs travels the same way, as
environment, because the jail has no `~/.gitconfig` to read one from.

**Covered by.** The socket boundary tests in `internal/node`; the ghost-store guard; the
sandbox-invocation tests in `internal/agent` that assert what the jail is asked to allow.

---

## INV-5 — No failure is silent, and no wait is invisible

Every task ends in a delivery, a gate, or a **notified** block. Retry is bounded — no
infinite loop that burns tokens without converging. A task waiting for a human is
discoverable by command (`luna gates`), never only by having watched the terminal.

**Why.** Silent death has two forms: the task that fails without warning, and the one that
waits forever because nobody knew it was waiting. With gates that free the slot,
suspension is invisible by construction — the process is not there to remind you.

**What violates it.** Retry with no ceiling; a suspended task that does not appear in the
listing; an exception swallowed between transitions.

**Known gap, stated rather than hidden.** An agent that is busy and achieving nothing is
not covered. What exists bounds a turn; a task circling without converging never exceeds
it. The failure still ends in a block once a budget runs out — just later than it should.

**A rule enforced at one end has to be stated at the other.** The gate holds the contract
to declared criteria; the stage that writes the contract was never shown them. Four
contracts went through, two rejected for the same criterion — "with no suggestions" — and
neither was a lapse in writing: both documents were properly imperative throughout, and
both put the offending sentence in a section the second one titled "Note for the maker".
An agent being helpful in a document that has no room for help. The stage producing a
judged artifact now sees the criteria it will be judged on, and the maker is told a
contract admits no recommendation.

**A wired field is not a called field.** The same landing broke twice, and the second
break was caused by the fix for the first. `luna lead` built its lead without `Land`, so a
finished task's work stayed on the stage branches; that was fixed and tested by asserting
the field was non-nil. But `land` is only called by `Lead.Run`, and `luna lead` runs
`conductTask` — a different loop, which still never called it. TALLY-8 finished six stages
and `luna status` printed `luna/TALLY-8/done` for a ref that did not exist, with a green
suite either side of the fix. A test that observes a dependency being *used* is worth
several that observe it being *set*.

**A refusal after the write is not a refusal.** `task abandon` skipped reading the state,
on the reasoning that the reducer would reject an illegal one on the next read and that
nobody would abandon a finished task by accident. The reducer rejecting it afterwards is
not a rejection — the event is already in an append-only log, and every command that reads
a task replays it, so `task show`, `status` and `forget` all fail and the task can neither
be read nor got rid of. It happened on the first occasion anyone tried, three minutes
after TALLY-6 finished. The read happens first now, and the one failure it passes over is
the one the command exists for: a task whose flow changed under it, which cannot be
replayed and most needs ending.

**A review that cannot be read sends nothing back.** The mechanism was complete at both
ends and the vocabulary crossed neither way: `ReadReport` looks for `[BLOCKING]`,
`[SHOULD-FIX]`, `[NIT]` and `[UNCERTAIN]`, the review stage declares `sends_back_to`, and
nothing ever told the agent those tags existed. On TALLY-7 the critic found four real
defects — every one verified against a named input — wrote them under a heading called
"Findings" in prose, and the parser read nothing. The critic's brief teaches the tags now,
and what may block is deliberately narrow: introduced by this change, or breaks a stated
acceptance criterion. A defect that was already there is reported to a person rather than
reopening the work, because blocking on inherited ones turns every task into an audit of
the repository.

**Two things are called the contract, and only one was checked.** The stage contract —
`requires` and `produces` — is the engine's. The contract *artifact*, the document the
plan stage writes, was checked by nobody: the gate judged whether it was coherent, and
nothing afterwards asked whether the delivery honoured it. On TALLY-7 that document
required a test pinning one of its own decisions, the test was never written, `build`
closed green — correctly, since its stage contract was satisfied — and `verify` reported
that all three decisions were pinned. `verify` now requires the contract and is told what
it is for.

**A simulation is not a result.** `--dry-run` exercises the machine with no agent, and
its evidence used to claim the scope each contract declared — with the truth in a `Detail`
field the CLI's own renderer never printed. So `luna task show` displayed
`ci_green passed (full) make ci → 0` for a command nothing ran, and `luna status` drew the
stage with the same `x` as any other. Worse, the flag could be pointed at a task with real
stages in it: on TALLY-6 it walked a task with four genuine commits to `done`, inventing
the two that check. A task is now marked as a simulation when it is created, both runners
refuse to mix the two, and every view says so on the first line. A lie with the truth
beside it is still the lie, once the reader only sees one of the two.

**What the invariant reaches, and what it did not.** A stage that produces nothing has an
account of why, and it is the agent's own reply. That reply was being discarded: it
reached the runner and nothing read it, so five stages of TALLY-5 each recorded
"delivered nothing" while the agent was saying, five times over, that git was unreachable
inside the sandbox. The failure was loud at the boundary and silent in the log, which is
the exact shape this rule exists to forbid. A stage that commits nothing on top of its
base now reports what the agent said; a stage that delivered does not, because there the
reply is the agent narrating a delivery that already speaks for itself.

**Covered by.** Retry-exhaustion and budget tests in `internal/agent`; the gate listing
test in `internal/cli`; the empty-delivery reporting tests in `internal/node`.

---

## What is deliberately not an invariant

**Fresh context per stage.** It was INV-core-5 until August 2026. The reasoning was role
erosion in long sessions — real, and observed. But the mechanism was wrong: what protects
the flow is INV-1, not the agent's amnesia. An agent with live context that drifts is
caught by the same wall that catches a fresh agent that is simply bad.

Context is now a per-stage setting (`fresh` or `live`), and the choice is measured rather
than assumed. One piece of it stays mandatory and lives in INV-3's separation instead: a
reviewer never inherits the session of whoever wrote the code, because independence of
review is not substitutable by verification.

That surviving piece is not decorative, and `AuditContextChain` earned its keep the first
time the flow tried to use `live` in earnest: `plan` was declared live, and on a bug task
`diagnose` runs immediately before it — so "continue the previous session" would have
meant continuing the investigator's. The check refused it statically, before a task ran.

**A prose summary never crosses the handoff.** Still true in the code — the payload is
synthesized by Luna, and the agent fills structured fields. It is a design rule rather
than an invariant: breaking it degrades quality, it does not make Luna stop being Luna.

**Whoever writes does not review.** Structural in the flow, and it holds. Demoted from the
invariant list because its enforcement floor is honest but soft: tool gating removes named
tools, not the shell. The separation is real; the containment behind it is INV-4's.
