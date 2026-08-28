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
stages or to be read by a human — are handed to Luna's store through a socket under the
runtime directory, which Luna asks the sandbox to expose read-only. Luna is the sole
writer, and the producing stage is recorded by the server, never taken from the agent's
request.

The socket was inside the worktree, because that was the only position measured to work
under Landlock. What changed is that Luna asks: it builds the sandbox's command line, so
it passes `--map` for the socket's directory, and against ai-jail 1.20.1 that connects.
Read-only is enough — Landlock permits `connect()` on an inode it can merely see — so the
agent now reaches the socket and cannot write into the directory holding it, which it
could when the socket lived in a worktree it owned. The same mapping through a project's
own `.ai-jail` is refused by design, so it has to come from Luna's flags: a repository
must not be able to name what gets mounted into the sandbox it runs in.

**Why.** Containment is delegated, not built: reimplementing a sandbox would be a worse
copy of what the layer below already does. And an artifact that is neither committed nor
handed over is an artifact nobody can find — which is the same as one that does not exist.

**What violates it.** An agent started outside the sandbox; an artifact written to an
unrecorded path; the store trusting a stage name that came from the agent.

**The one exception, and it is named rather than implied.** `setup` runs its agent
uncontained. It is the stage that reads `.ai-jail` to report what the containment will be,
so containing it means running it under the configuration it exists to inspect — and when
that configuration is wrong or absent, it runs under the wrong jail to say the jail is
wrong. The exception is bounded by what the stage is allowed to do rather than by trust:
it **reports and never repairs**, so it writes nothing, and an agent that writes nothing
has nothing to contain. It is the only stage that runs before containment is established,
and any second exception is a change to this invariant rather than an application of it.

The risk this accepts is stated: an uncontained agent reading a repository can read
anything the user can. What it cannot do is change anything, and what it produces is a
report a person answers at a gate before the trail continues.

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
sandbox-invocation tests in `internal/agent` that assert what the jail is asked to allow;
and a test that no stage but `setup` is exempt from containment — an exception that is not
pinned is one the next stage inherits by accident.

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

**Known gap, narrowed rather than closed.** An agent that is busy and achieving nothing
is still not detected as such. What bounds it now is money: a task carries a spending
ceiling, checked where the next stage would open, so a task circling without converging
stops when it has cost what it was allowed to. That is a bound and not a detector — the
task still spends its whole ceiling before anything notices — and the thing that would
actually close this is a signal the runner does not produce: telling an agent that is
thinking from one that is stuck.

**A rule enforced at one end has to be stated at the other.** The gate holds the contract
to declared criteria; the stage that writes the contract was never shown them. Four
contracts went through, two rejected for the same criterion — "with no suggestions" — and
neither was a lapse in writing: both documents were properly imperative throughout, and
both put the offending sentence in a section the second one titled "Note for the maker".
An agent being helpful in a document that has no room for help. The stage producing a
judged artifact now sees the criteria it will be judged on, and the brief says a contract
admits no recommendation.

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
`[SHOULD-FIX]`, `[NIT]` and `[UNCERTAIN]`, the judging stage declares `sends_back_to`, and
nothing ever told the agent those tags existed. On TALLY-7 the critic — a separate role,
back then — found four real defects, every one verified against a named input, wrote them
under a heading called "Findings" in prose, and the parser read nothing. The brief teaches
the tags now,
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

**The retry budget was spent on the wrong failure.** A harness that would not start got
two retries; an agent that delivered everything but one handover got none, and blocked on
the first attempt with the budget untouched. The second is the more recoverable of the
two — Luna knows exactly which artifact is missing, the socket is still open, and a stage
declaring `context = "live"` resumes the session it already paid for. It is asked again
now, and told by name what did not arrive and that its earlier work stands; only the
attempt past the budget blocks. This is what killed a benchmark run whose code was
otherwise correct: the judging stage produced `ci_green` from a real `make ci` and forgot
the checklist it also owed.

**Covered by.** Retry-exhaustion and budget tests in `internal/agent`; the gate listing
test in `internal/cli`; the empty-delivery reporting tests in `internal/node`; the spending
ceiling tests in `internal/fsm`; and, for the fleet, the tests that a blocked task is not
retried nightly and that a task which no longer replays is reported rather than skipped.

---

## What is deliberately not an invariant

**Fresh context per stage.** It was INV-core-5 until August 2026. The reasoning was role
erosion in long sessions — real, and observed. But the mechanism was wrong: what protects
the flow is INV-1, not the agent's amnesia. An agent with live context that drifts is
caught by the same wall that catches a fresh agent that is simply bad.

Context is now a per-stage setting (`fresh` or `live`), and the choice is measured rather
than assumed. One piece of it stays mandatory: the stage that judges delivered work never
inherits the session that produced it. With separate roles that was half of what made a
review independent; it is now one of three again, and `review` declares `fresh` rather
than inheriting like every other stage after the first.

`AuditContextChain` earned its keep the first time the flow tried to use `live` in
earnest: `plan` was declared live, and on a bug task `diagnose` ran immediately before it —
so "continue the previous session" would have meant continuing the investigation's. It
refused that statically, before a task ran. It compares briefs now rather than role names,
which is stricter: a builder and a judge once held one role and are told different things,
so the old test would have let them share a session neither should inherit from the other.

**A prose summary never crosses the handoff.** Still true in the code — the payload is
synthesized by Luna, and the agent fills structured fields. It is a design rule rather
than an invariant: breaking it degrades quality, it does not make Luna stop being Luna.

**Whoever writes does not review.** It was demoted from the invariant list, then lost
entirely when one agent came to do every stage, and it is back — placed differently, and
for a reason worth stating rather than quietly restoring.

It is deliberately *not* held inside the loop. `forge` builds, cleans, checks and judges
its own rounds, and that is a self-assessment by construction. It buys speed: a failure
found by the check goes back into building in the same session, without a cold start. What
it cannot buy is an honest verdict on the delivery as a whole.

So the independent read is a separate stage after the delivery. `review` has all three
halves this time: a session it did not write (`fresh`), tools it does not hold (`Edit` and
`Write` denied, on a harness Luna can actually gate), and work it did not do. A judging
stage missing any of them reads its own reasoning back and agrees with it.

This is still not proof. Tool gating removes named tools, not the shell — the floor is
honest and soft, and containment is the sandbox's job. What is *not* soft is the half that
never depended on who was asking: a command that runs over the delivered commit does not
care who wrote it.

**What is unmeasured, and stated rather than assumed.** On the one full cycle that reached
it, an independent judging stage found a genuine violation of the contract's own clause and
sent the work back — the first time that mechanism ever fired. Whether the merged loop
judges its own rounds well is not known, and it is the thing to measure first: the whole
argument for merging is that a biased fast verdict inside the loop, corrected by an
unbiased one after it, beats an unbiased slow verdict at every step.
