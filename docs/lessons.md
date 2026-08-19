# Lessons

What building this taught. Kept because the lessons survive the code that produced them —
these were paid for, mostly in debugging sessions.

The old suite recorded all of this across 73 immutable ADRs, 9 RFCs and 3 PRDs. The record
was worth having and the *format* was not: finding the live decision on a subject meant
reading a chain of four documents, and the summary at the top of the pile went stale
anyway. See [decisions.md](decisions.md) for what is live now; this file is what the
process taught.

## About agents

**An agent will do almost everything you ask, and the "almost" is the whole problem.**
Told to *run* a command, one printed it. Told to iterate, one did the same thing 100 times
and something else on the 101st. In long sessions, roles erode — the reviewer starts
implementing. None of this is fixable with better prompting; it is fixable by not asking
the model to be the thing that guarantees the process.

**But the model is not the enemy either.** In the system that inspired this one, the state
machine had a bug and told the lead to review an already-reviewed document. The lead
*refused and escalated to the human* — instructed to obey the FSM, it still recognised
that the instruction made no sense. The deterministic layer gives the skeleton; the
judgement layer catches the skeleton's errors. Designing as if the model were purely
adversarial throws that away.

**Verification beats every other defence.** Of everything built here, the exit check —
run the real tool, over what was actually delivered — is the only piece that never failed.
It is also the piece that made other defences unnecessary: fresh context per stage was an
invariant for months, until it became clear that what protects the flow is the wall at the
exit, not the agent's amnesia.

**The dominant failure is incoherence, not sabotage.** A tree that passes and a delivery
that does not: an uncommitted file, a local `.env`, a test edited and never committed.
Verifying the delivery rather than the working tree closes that whole class, and costs
nothing.

## About scaffolding and specs

**An artifact that must not rot is an artifact that must not persist.** Contracts,
scenarios, approaches and audit reports are real work products and terrible documentation.
The fix was to hand them to a content store keyed per task and stage, versioned by hash,
emptied with `luna task forget` when the task ends. They never become living docs, so they
never become lies.

**A spec detailed enough to generate correct code is a program written in prose.** The
practical form of that: a specification is only worth keeping if something *executes*
against it. If it cannot be checked by a command, it is a note, and it should be
disposable and small.

**Documentation shape can pass every check while the content is false.** The docs linter
validated structure — file naming, required sections, link integrity — and reported green
while `architecture/overview.md` claimed a directory was empty that held 12 files and
diagrammed a component removed four decisions earlier. Green form over false content is
worse than no document, because it earns trust it has not got.

## About the plumbing

**Driving a human interface is a bug you keep paying for.** Getting agents to run
unattended through a terminal multiplexer cost: a pty with no size, a folder-trust dialog,
an input-ready marker that was ambiguous with the shell prompt, a 108-byte limit on socket
paths, a store silently writing into a tmpfs that evaporated. Not one of those was about
the model. All of them disappeared by switching to the harness's headless mode, which had
existed the whole time.

The general form: **instruction that exists to work around a tool's behaviour is debt, not
knowledge.** When the workaround pile grows, check whether you picked the wrong interface.

**Measure the signal before building the guard.** The ghost-store guard went through three
wrong signals — filesystem type, "a repo with no files", ".git without objects" — each of
which either passed when it should have failed or broke a legitimate case. Two were caught
only because a test that had nothing to do with the guard went red. The one that worked
was the boring one: write an anchor file and read it back.

**Never write over a file you did not create.** An early version of the trust helper wrote
a config unconditionally; the sandbox binds the real `~/.claude.json` read-write, so it
destroyed the user's actual credentials. The rule that came out of it is absolute: merge
into an existing config, never create or replace one, and refuse rather than guess.

## About measuring

**A ceiling no test reaches is worse than no ceiling.** Limits that were never exercised
gave the appearance of a bound with none of the guarantee. Write the test that hits the
limit at the same time as the limit.

**A missing piece hides infinite loops in the pieces that look finished.** A component
reported complete and tested was masking a non-terminating loop elsewhere, because the
path that would have exposed it was never reached.

**Verify against the binary and invert the test.** Running the claim against the built
artifact, and writing the test so it fails when the behaviour is absent, caught an error
almost every time it was applied. Reasoning about whether code is correct is not the same
activity as finding out.

**Without a counter, there is no argument.** The whole system ran for months with no idea
what it cost in tokens against doing the same work in one session. Any claim about the
value of orchestration that is not measured is an aesthetic preference. The harness reports
usage for free; there was never a reason not to record it.

## About this project's own process

**Recording a decision and recording progress are different things.** Decision records
kept drifting into status updates, which is what made a chain of four necessary to find
one current answer. A decision record says what was chosen and what was rejected; where
the code stands belongs to the code.

**Rejected alternatives are half the value, and the reason the record exists.** The
discipline worth keeping from the ADR format is not immutability — git already preserves
history — but writing down what was considered and thrown away, so it does not get
relitigated without a new argument.

**The record grew faster than the thing it recorded.** 95,000 words of documentation for
14,000 lines of code, for a tool with no users. That ratio is the clearest signal that the
process had become the product.
