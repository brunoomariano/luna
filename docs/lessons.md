# Lessons

What building this taught. Kept because the lessons outlive the code that produced them —
these were paid for, mostly in debugging sessions and a few in dollars.

Most of what is described here belonged to a much larger machine: a state machine that ran
agents, opened worktrees, built sandboxes and owned a database. That machine is gone (see
[decisions.md](decisions.md)). The lessons are not, and several of them are *why* it is
gone.

## About agents

**An agent will do almost everything you ask, and the "almost" is the whole problem.**
Told to *run* a command, one printed it. Told to iterate, one did the same thing 100 times
and something else on the 101st. In long sessions, roles erode — the reviewer starts
implementing. None of this is fixable with better prompting; it is fixable by not asking
the model to be the thing that guarantees the process.

**But the model is not the enemy either.** In the system that inspired this one, the state
machine had a bug and told the lead to review an already-reviewed document. The lead
*refused and escalated to the human* — instructed to obey, it still recognised that the
instruction made no sense. The deterministic layer gives the skeleton; the judgement layer
catches the skeleton's errors. Designing as if the model were purely adversarial throws
that away.

**Verification beats every other defence.** Of everything built here, the exit check — run
the real tool, over what was actually delivered — is the only piece that never failed. It
is also the piece that made other defences unnecessary: fresh context per stage was an
invariant for months, until it became clear that what protects the flow is the wall at the
exit, not the agent's amnesia. It is the one part of the original machine that survived
into this one.

**The dominant failure is incoherence, not sabotage.** A tree that passes and a delivery
that does not: an uncommitted file, a local `.env`, a test edited and never committed.
Verifying the delivery rather than the working tree closes that whole class, and costs
nothing.

**How you ask decides what you get.** Asked *"read this and decide"*, a model approved a
delivery that violated an acceptance criterion **three times out of three** — twice naming
the contradiction in its own reasoning on the way to approving. Asked to walk the criteria
one at a time and cite the line that satisfies each, the same model rejected the same
artifact three times out of three. Same model, same artifact, same criteria.

## About scaffolding and specs

**An artifact that must not rot is an artifact that must not persist.** Contracts,
scenarios and audit reports are real work products and terrible documentation. They should
be disposable and scoped to the work that produced them, so they never become living docs
and never become lies.

**A spec detailed enough to generate correct code is a program written in prose.** The
practical form: a specification is only worth keeping if something *executes* against it.
If it cannot be checked by a command, it is a note.

**Documentation shape can pass every check while the content is false.** The docs linter
validated structure — file naming, required sections, link integrity — and reported green
while the architecture doc claimed a directory was empty that held 12 files and diagrammed
a component removed four decisions earlier. Green form over false content is worse than no
document, because it earns trust it has not got.

**Ten layers and 95,000 words for 14,000 lines of code went out of sync while every
structural check stayed green.** The suite is four files now, and this project's own
redesign cut the code by 87% without losing a guarantee — which says something about what
the other 87% was guaranteeing.

## About the plumbing

**Driving a human interface is a bug you keep paying for.** Getting agents to run
unattended through a terminal multiplexer cost: a pty with no size, a folder-trust dialog,
an input-ready marker ambiguous with the shell prompt, a 108-byte limit on socket paths, a
store silently writing into a tmpfs that evaporated. Not one was about the model. All of
them disappeared by switching to the harness's headless mode, which had existed the whole
time.

The general form: **instruction that exists to work around a tool's behaviour is debt, not
knowledge.** When the workaround pile grows, check whether you picked the wrong interface.

**Measure the signal before building the guard.** The ghost-store guard went through three
wrong signals — filesystem type read from a path, "a repo with no files", ".git without
objects" — each of which either passed when it should have failed or broke a legitimate
case. Two were caught only because a test that had nothing to do with the guard went red.
The one that worked was the boring one: ask the kernel. That is why the durability guard is
a single `statfs` and nothing cleverer.

**A fake of a boundary that has no boundary tests nothing.** Three bugs in a row stopped
every stage from working and none was reachable by the suite: the sandbox was not asked for
the network, then not asked for git worktree metadata, then given no identity to commit as.
The tests used a fake sandbox that consumes its flags and execs the rest — it has no
filesystem boundary, so a worktree is always visible to it and a missing flag never costs
anything. The suite was at 96% and every one of these was found by a run.

**A wired field is not a called field.** The same landing broke twice, and the second break
was caused by the fix for the first: the fix asserted the dependency was *set*, and the
second code path that should have used it never called it. A test that observes a
dependency being **used** is worth several that observe it being wired.

This one recurred during the redesign, in miniature: removing the durability check from the
ledger's `Append` broke nothing, because every test proved the guard function in isolation
and none proved that anything called it. The test that closes it writes where a write would
be lost and demands the refusal.

## About cost

Numbers from real runs, kept because they are the only defence against an aesthetic
argument.

- **A full seven-phase cycle: $7.42 over 124 turns.** Six of those phases paid a cold start
  to re-read code the previous phase had just written.
- **Two planning phases were $4.14 of a $6.78 task**, and one of the artifacts they
  produced was consumed by no later phase at all.
- **Running an investigation where there is nothing to investigate is 3.3× for nothing**:
  $0.88 on the class of task it exists for, $2.88 on the wrong one.
- **A phase that delivered nothing still cost $0.56, and closed green.** Another, $0.64 over
  17 turns, ended clean at its base with none of the feature written. Both are why the
  delivery check is what it is.
- **Prompt size is not the lever.** An 18-clause contract cost *less* than 5040 bytes of
  prose for the same phase; the spread across runs is model variance. The cost is the
  agent's own turns.

## About the first real use

Two tasks, driven by two different models, each asked afterwards what the tool was
like to use. Both reports were more useful than the sessions.

**The tool proved nothing, twice.** Ten `phase` events, one `gate`, and **zero
`check`** across both tasks. Both reported green, and both were telling the truth —
they had run the gates in their own tree. But the ledger held a claim where the
whole point of the tool is that it holds evidence. Nothing about the design made
that hard to do: `record --status done` was accepted in silence on a run that had
proved nothing. A tool whose one job is easy to skip gets skipped.

**A written field that is not shown is worse than one that is refused.** Four
phases were recorded with `--found` carrying the real finding of each — *"the whole
suite was red on the base: vitest 4 shadows jsdom"* — and the trail rendered them
as blank lines. The data was in the ledger, intact. The renderer read `Found` only
for a `discovery`, so everything else was dropped without a word. The writer
believed they had left a record. That is the failure this project keeps meeting in
new costumes: silent, agreeing with itself, and wrong.

**A model reported two bugs, checked, and both were its own.** An exit code eaten
by `| tail`, and a `node_modules` missing inside the throwaway checkout — which is
the tool working exactly as designed, over the delivered commit rather than the
working tree. Both reports said so, unprompted. That is worth more than a report
that finds three real bugs and no mistakes of its own.

**The syntax cost five calls before anyone read the help.** One model got `record`
wrong three times running, then discovered by accident that the branch supplies the
run id. There was a skill documenting it. A CLI learned by trial leaves its
mistakes in an append-only record — one of them is a phase note that says `teste`,
permanently.

## About this redesign

**A tool that wants to be the parent process cannot be added to a setup that already has
one.** Luna was measured, tested and correct, and it went unused for months next to a stack
that did less — because using it meant leaving the stack. Inverting the call was worth more
than every feature added in that time.

**Ask what a piece would be if the rest were gone.** Of 21,150 lines, the part that carried
the idea was about 770. The rest was consequence: a daemon because the store was central, a
store because the state was owned, a fingerprint because the log was replayed. None of those
was wrong given the one before it, and all of them went together when the first premise did.
