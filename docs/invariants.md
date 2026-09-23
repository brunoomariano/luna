# Invariants

Rules that always hold, regardless of implementation. Violating one is not a bug — it is
Luna ceasing to be Luna.

There are five. The list is short on purpose: an invariant that forbids what the code
permits teaches people to stop believing the list.

---

## INV-1 — Delivery is verified by running the tool, over what was delivered

A phase does not close because the agent said it was done. It closes because a command
returned zero over the delivered commit — and that commit resolves to exactly one object,
which is a commit, and is not the base the phase was handed.

**Why.** Well-formed output can describe something that does not exist. This is the one
piece that worked without exception in real cycles, and it is what makes the rest
unnecessary to trust.

The verification runs over **what the phase delivered**, not over whatever was left in the
working tree. The failure mode it closes is not sabotage — it is incoherence: an
uncommitted file, a local `.env`, a stale build artifact. A tree that passes and a
delivery that does not.

**Floor, stated plainly.** Not every artifact is mechanically provable. Evidence carries
the **scope** of what was proven — `full`, `targeted`, `human`, `existence` — and
`existence` is the honest floor for prose: the file is there, nothing more is claimed.
Scope has no upgrade path.

**What violates it.** Accepting a delivery because a field came filled in; validating by
schema instead of execution; a verdict about a tree that is not the delivery it names.

**What it does not claim.** This is not containment. An agent controls what it commits,
and therefore what is verified. Luna builds no sandbox — a person composes the environment
before the agent starts, and Luna inherits it. What Luna guarantees about its own writing
is INV-4.

**The case that got through.** A phase that delivered *nothing* used to pass. An empty
commit reached `CheckoutAt`, which read it as `HEAD` and resolved it against the main
repository — so the command ran over whatever was already committed there and exited zero.
Measured on TALLY-3: `build` recorded `make test → 0` while its branch sat on the base
commit with a clean worktree. The green was true, and it was true about code the phase did
not write. A phase that owes a committed artifact and adds no commit now fails; a pure
verification phase is different because the commit it received is the thing it is asked to
prove. A repository with no commit at all is still the first phase of the first run, and
still verifies its tree.

**The half of it that stayed.** That account was written as though the whole defect were
closed, and it was not: resolving against the main repository remained what `check` did
whenever `--commit` was omitted. In a worktree — the case Luna is built for — that is the
base, so every check ran over code the phase had not written and the ledger recorded it as
proof. Six tasks across two days before it was caught. The HEAD now resolves in the
worktree the call was made from.

The other half of it took a second run to find. That guard asked whether a commit
*existed*, and a phase that adds none hands back the base — which exists, and resolves. So
the check ran over the code the phase was given and passed, because that code was already
green when the phase received it. Measured on TALLY-5: `build` was billed $0.64 over 17
turns, ended clean at its base with none of the feature written, and closed `tests_green`
as `targeted` and `passed`. A delivery equal to the base is not a delivery.

**Covered by.** The scope tests in `internal/contract`; and in `internal/verify`,
`TestTheCheckRunsOverTheDeliveryAndNotTheWorkingTree`,
`TestAPhaseThatCommittedNothingDoesNotPassOnSomebodyElsesCode` and
`TestAPhaseThatDeliveredNothingNewIsNotProvenByItsBase`.

---

## INV-2 — The record is append-only, and the last line is the state

No line is rewritten and none is removed. Every change is a new line in the ledger, and
where a run stands is its most recent line — read by tailing, not by replaying. A line is
self-contained: it carries the run, the project, the phase and what happened, so reading
one never requires reading those before it.

**Why.** The history is the audit, and for an unattended run the evidence is the product.
What was dropped deliberately is *replay*: rebuilding state by folding every event from
the beginning. Replay is what forced flow fingerprints, strict sequence ownership and a
single writing process, and it bought a guarantee this design does not need — Luna no
longer decides transitions, so it has no state to reconstruct, only a position to report.

**What that costs, stated rather than hidden.** A corrupted or partial line is not
detectable by reconciliation against the lines around it. The mitigation is the format
rather than a checker: one JSON object per line, appended with `O_APPEND` and under the
size a write is atomic at, so a concurrent fleet appends without a lock and a torn line
would have to come from outside Luna.

**What violates it.** Editing or deleting a line; a state that only the process holds
between two commands; a line that cannot be read without its predecessors.

**Covered by.** In `internal/ledger`: `TestAppendingNeverRewritesWhatIsAlreadyThere`,
`TestSeveralProcessesAppendWithoutCorruptingEachOther`,
`TestALineTooLongToAppendAtomicallyIsRefused`, `TestTheStateOfARunIsItsMostRecentLine` and
`TestOneRunsLinesDoNotAnswerForAnother`. In `internal/verify`:
`TestExistenceOfALargeFolderTruncatesItsListing`, which keeps a long existence listing
under the ceiling where the detail is built — the refusal above is correct, and arriving
mid-run it leaves the earlier checks of that run already recorded.

---

## INV-3 — No phase closes without what it produces

Every phase declares what it produces, and each debt names how it is proven. Two checks
run: **static**, over the contract before the phase runs (`luna contract lint`), and **on
exit**, over what the phase delivered (`luna check`).

A phase also declares `requires`. Nothing enforces it — see below, and that is deliberate.

**Why.** It distinguishes "the model got it wrong" from "the model did not receive what it
needed". Without it, the failure surfaces two phases later, when the symptom has already
moved away from the cause.

This includes `produces_for_human` — the report nobody downstream consumes. If its
delivery depended on the flow feeling it missing, it would never be demanded.

**Where the entry check went: nowhere. Only the exit half of this invariant is enforced.**

Luna no longer walks a flow, so it cannot refuse to start a phase — starting one is the
conductor's act. `requires` is parsed, carried, and **read by nothing**: `lint` accepts a
contract requiring artifacts no phase ever produced, and exits 0.

This paragraph used to claim `lint` refused exactly that. It did not, and the claim stood
for the whole redesign — in the file whose preamble says an invariant that forbids what
the code permits teaches people to stop believing the list. It was the list's own defect.

Building it is possible: only `check` events carry an artifact, so the ledger does know
what has been proven. It is not built, because deciding whether a phase may *start* from
what the ledger holds is flow control, and moving that back into Luna is what the redesign
removed. `requires` stays as documentation a person reads — the honest description of a
field nothing enforces.

**What violates it.** A phase with no contract; a `produces` marked delivered without
verification; a `check` that reports a verdict for an artifact it did not look for.

**Covered by.** The lint tests in `internal/contract` — including a loop converging on
something the phase never produces — and `TestEveryOwedArtifactIsReportedInAStableOrder` in
`internal/verify`.

---

## INV-4 — The ledger is durable, or nothing is recorded

Luna records to one ledger outside every checkout. Before the first write of a run, it
proves that ledger is on a filesystem that survives the process — and refuses to run when
it is not.

**Why.** Luna does not build the environment it runs in. A person composes it before the
agent starts — sandbox, memory, both, or neither — so Luna inherits whatever that
choice produced and cannot know it from the inside. Under a sandbox that gives the process
a tmpfs `$HOME`, a ledger under `$XDG_DATA_HOME` is writable, is written, reports success,
and evaporates. That failure is silent in both directions: the write and the read agree
with each other and with nobody else. The guard is what turns it loud.

**The signal is the filesystem, not a heuristic.** `statfs` on the ledger's directory,
compared against `TMPFS_MAGIC`. Three earlier signals were tried in this project for a
related guard and each either passed when it should have failed or broke a legitimate
case; the one that worked was the boring one that asks the kernel.

**What violates it.** Recording to a path nobody proved durable; degrading to a warning
and continuing; inferring containment from the process's own environment rather than from
the filesystem the write lands on.

**What it does not claim.** This is not containment. Luna starts no agent and builds no
sandbox, so it guarantees nothing about what an agent may reach — that belongs to the
layer the person chose. What Luna guarantees is that its own record either persists or
refuses.

**Covered by.** `TestALedgerInMemoryIsRefused` and `TestTheWritingVerbsRefuseBeforeTheirFirstAppend`
in `internal/ledger`; `TestAVerdictThatCannotBeRecordedIsReported` in `internal/cli`; and
`TestTheBinaryRefusesALedgerThatWouldNotSurvive`, which exercises the real binary.

Each of these skips when the machine cannot provide the filesystem it needs to contrast
against — a durable `$HOME`, or a writable tmpfs. On a machine whose `$HOME` is tmpfs the
whole of `internal/ledger`'s suite skips, INV-2's tests with it, and the run still reports
green. The invariant is enforced on every write regardless; what a green suite does not by
itself establish is that these four ran.

---

## INV-5 — No failure is silent, and no wait is invisible

Every phase ends in a delivery, a gate, or a **recorded** block. A run waiting for a person
is discoverable by command (`luna runs`), never only by having watched a terminal. A
phase that came up short records what it was missing by name.

**Why.** Silent death has two forms: the run that fails without warning, and the one that
waits forever because nobody knew it was waiting. Under an unattended fleet the second is
the expensive one — the process is not there to remind anybody.

**Blocking on missing information is a first-class ending, at every autonomy.** A phase
that cannot resolve a question from what it has may stop and hand it to a person, and
`auto` does not suppress that. The difference between running without asking and running
without thinking is what this rule protects, and a fleet that cannot stop produces
expensive noise. A block carries three things and no ceremony: the question, where the
answer was looked for and what each source failed to say, and what would unblock it. The
middle one is what separates a real block from an unread file.

**What violates it.** A loop with no ceiling; a blocked run that does not appear in the
listing; an exception swallowed between phases; an autonomy setting that turns a block into
a guess.

**A rule enforced at one end has to be stated at the other.** The gate held a contract to
declared criteria while the phase that wrote the contract was never shown them. Four
contracts went through and two were rejected for the same criterion — "with no
suggestions" — and neither was a lapse in writing: both documents were imperative
throughout, and both put the offending sentence in a section one of them titled "Note for
the maker". An agent being helpful in a document with no room for help. Whoever produces a
judged artifact sees the criteria it will be judged on.

**A wired field is not a called field.** The same landing broke twice, and the second break
was caused by the fix for the first: the fix asserted a dependency was *set*, and the
second loop that should have used it never called it. TALLY-8 finished six phases and
reported a ref that did not exist, with a green suite either side. A test that observes a
dependency being **used** is worth several that observe it being set.

**A review that cannot be read sends nothing back.** The mechanism was complete at both
ends and the vocabulary crossed neither way: the reader looked for `[BLOCKING]`,
`[SHOULD-FIX]`, `[NIT]` and `[UNCERTAIN]`, and nothing ever told the agent those tags
existed. On TALLY-7 the reviewer found four real defects, each verified against a named
input, wrote them under a heading called "Findings", and the parser read nothing. What may
block is deliberately narrow — introduced by this change, or breaks a stated acceptance
criterion — because blocking on inherited defects turns every run into an audit of the
repository.

**A simulation is not a result.** `--dry-run` exercises the machine with no agent, and its
evidence used to claim the scope each contract declared, with the truth in a field the
renderer never printed. A run is marked as a simulation from its first line, and every view
says so. A lie with the truth beside it is still the lie, once the reader only sees one of
the two.

**The account of a failure is the agent's own reply, and it was being discarded.** Five
phases of TALLY-5 each recorded "delivered nothing" while the agent was saying, five times
over, that git was unreachable inside the sandbox. Loud at the boundary and silent in the
record, which is the exact shape this rule forbids. A phase that delivers nothing reports
what the agent said; one that delivers does not, because there the reply narrates a
delivery that already speaks for itself.

**Covered by.** `TestABlockMustSayWhereTheAnswerWasLookedFor` and the report ordering tests
in `internal/ledger`; and, in `internal/cli`,
`TestABlockWithNoAccountOfWhatWasConsultedIsRefused` and
`TestAFailedCheckIsToldApartFromABrokenLuna`.

---

## What is deliberately not an invariant

**Fresh context per phase.** It was an invariant until August 2026. The reasoning was role
erosion in long sessions — real, and observed. But the mechanism was wrong: what protects
the flow is INV-1, not the agent's amnesia. An agent with live context that drifts is
caught by the same wall that catches a fresh agent that is simply bad.

Context is a per-phase setting, and the choice belongs to whoever conducts. One piece of it
stays load-bearing by convention rather than by enforcement: the phase that judges
delivered work never inherits the session that produced it.

**Containment.** Luna starts no agent and builds no sandbox. A person composes the
environment before the agent starts — sandbox, durable memory, both or neither — and Luna
inherits whatever that produced. This was an invariant (INV-4) while Luna was the process
that launched agents; it stopped being one when it stopped being that process, and
pretending otherwise would be a rule the code cannot hold. What replaced it is narrower and
actually enforceable: Luna's own record either persists or refuses.

**Whoever writes does not review.** Held by placement rather than by mechanism, and
deliberately *not* held inside the loop. The build loop judges its own rounds, which is a
self-assessment by construction; it buys speed, because a failure found by the check goes
back into building in the same session without a cold start. What it cannot buy is an
honest verdict on the delivery as a whole, so the independent read is a separate phase
after the delivery, in a session that did not write the code.

That independence is now the conductor's to arrange, not Luna's to enforce — Luna does not
start the reviewer and cannot deny it a tool. What is *not* soft is the half that never
depended on who was asking: **a command that runs over the delivered commit does not care
who wrote it.** That is the half Luna keeps.

**A prose summary never crosses the handoff.** A design rule rather than an invariant:
breaking it degrades quality, it does not make Luna stop being Luna.

**What is unmeasured, and stated rather than assumed.** On the one full cycle that reached
it, an independent judging phase found a genuine violation of the contract's own clause and
sent the work back — the first time that mechanism ever fired. Whether a merged loop judges
its own rounds well is not known, and it is the thing to measure first: the whole argument
for merging is that a biased fast verdict inside the loop, corrected by an unbiased one
after it, beats an unbiased slow verdict at every step.
