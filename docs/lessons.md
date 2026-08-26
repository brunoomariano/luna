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

**A fake of a boundary that has no boundary tests nothing.** Three bugs in a row stopped
every stage from working and none was reachable by the suite: the sandbox was not asked
for the network, then not asked for git worktree metadata, then given no identity to
commit as. The tests used a fake sandbox that consumes its flags and execs the rest — it
has no filesystem boundary, so a worktree is always visible to it and a missing flag
never costs anything. The suite was at 96% and every one of these was found by a run.

The general form: **when you fake an external boundary, the fake keeps the interface and
drops the constraint** — and the constraint is the whole reason the boundary exists. What
a fake can still hold is the *invocation*: asserting what the sandbox was asked to do
catches a missing flag even when nothing can simulate its effect. That check costs a
line, and it is the one that would have caught all three.

**The reply is evidence when nothing else is.** Five stages produced no commit, cost
$3.33, and reported "delivered nothing" — while each agent was saying, in a reply Luna
read and threw away, that git was unreachable. The work was real: it was found afterwards
in a directory the agents created because they could not use git. A failure can be loud
at the boundary and silent in the log, and discarding the only channel that carries the
reason is how that happens.

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

**A guard that only checks one direction is a guard with a blind spot.** A test held a
list of artifacts exempted from mechanical proof, each with the reason no command could
prove it. It walked the flow's produced artifacts and looked each one up — so an
exemption whose artifact no longer existed was never visited, and three of them survived
a flow change invisibly. Its sibling test was written for exactly that failure ("the
exemption outlived the artifact") and could not see it either, because it checked its own
hand-written list rather than the map. The fix was to walk the map as well as the flow,
and the proof was inverting it: a dead name put back now fails, and did not before.

**Coverage measures the code, not the environment it runs in.** Four faults shipped past a
suite at ~96%, and every one of them was the transport rather than the logic: the sandbox
started with no network, so no stage agent could reach a model at all; a stage killed
mid-flight stranded a git registration that blocked the next stage of the same role; a
stage that committed nothing was verified against the repository's own head; and the lead
could not run `luna next`, the first command its own brief tells it to run. Each needed one
real task to surface and none of them could have been found by a test of Luna's own code,
because none of them *is* Luna's own code — they are what Luna asks of the things around
it. The suite was never wrong; it was answering a different question.

**The lead found a hole by reading the design, not by hitting it.** Mid-run it observed
that "a stage can self-report a commit SHA that Luna never verifies exists — the delivery
check caught the weak proof, but a fabricated SHA would sail past a stage whose check *was*
strong enough". Probed, and it was right: forty hex characters became the task's base, the
commit every later stage branches from. This is the SwarmForge lesson the project wrote
down at the start and then did not apply everywhere — validate the commit by running git,
not by checking that the text looks like a SHA.

**A brief can promise a mechanism that does not exist, and nothing will say so.** The
lead's brief said "the agent you start does the work — you do not do it yourself" from the
beginning, and Luna had no command that starts an agent: `next` reads, `done` reports, and
`run` drives the whole flow, which is the one thing the lead must not do. So the lead did
the work itself, obediently and well, and every artifact was recorded as "reported by hand
through `luna done`" — no cost, no handover, and a review gate whose artifact had never
been attached to it. The sentence was true as a rule and empty as an instruction, and only
a real run could tell the difference.

**A helper the test has and the product does not is a hole shaped exactly like the
product.** The fake lead in the suite opened each stage itself, through a test helper, with
the comment "what a real lead does" above it. A real lead has no such command: `luna next`
reads and changes nothing by design, and nothing else opens a stage. So `luna lead` could
never close its first stage — `luna done` answered "no running stage to finish", every
time, for every task — while eight tests proved the loop worked. The helper was not
standing in for the product; it was standing in for the part that was missing.

**A fake that cannot be wrong the way the real thing is wrong proves nothing.** Every test
of the agent transport passed `Sandbox: "/bin/sh"`. It ran, it recorded its arguments, the
assertions held — and because `sh` refuses a leading long option, the whole suite
structurally encoded "the sandbox is invoked with no flags of its own". That was the
assumption under a shipped agent that could never reach a model: `ai-jail` was started
without `--network`, so `claude -p` inside it opened its TLS bundle and blocked forever on
a connection the jail forbids. No output, no error, no exit — and the default budget is two
hours. A real run found it in twelve minutes; the suite could not have found it at all.
The fix to the tests was a fake sandbox that takes its own flags and execs the rest, the
way the real one does.

**The same bug twice, because the second copy had no test that could see it.** `Run` was
fixed months ago to give an agent its own process group and kill the group, after a stage
with a 200ms budget ran its child for the full 30 seconds. The interpreter package had the
same defect the whole time and nobody knew, because nothing there ever tested a deadline
against a process that ignores it. Writing that test while moving the code took the
package's suite from 60 seconds to 0.4 — the tests had been *waiting out* the bug.

**A collapsed name turns a survivable failure into a blocking one.** A branch is named for
the task and the role. While every stage had its own role, a stage killed mid-flight
stranded a registration git still held — and nothing ever asked for that name again, so
nobody noticed. With one `maker` across plan, build and refactor, the very next stage asks
for exactly that name and the task blocks on `already used by worktree at …`, pointing at
a directory that no longer exists. Merging roles did not create the fault; it removed the
slack that had been hiding it.

**State that has to survive a process belongs in the log, and "a process" is shorter than
it looks.** The session id that makes `context = "live"` work lived in a map on the runner.
That is correct for as long as one conducting process stays alive — and the shipped flow puts a
gate in the middle of the maker's run, so answering it ends the process and the next stage
starts cold. The cost column is what showed it: `fresh` recorded where `live` was declared.
A counter that only confirmed expectations would have hidden it.

**Verify against the binary and invert the test.** Running the claim against the built
artifact, and writing the test so it fails when the behaviour is absent, caught an error
almost every time it was applied. Reasoning about whether code is correct is not the same
activity as finding out.

**Without a counter, there is no argument.** The whole system ran for months with no idea
what it cost in tokens against doing the same work in one session. Any claim about the
value of orchestration that is not measured is an aesthetic preference. The harness reports
usage for free; there was never a reason not to record it.

**And the first numbers were worse than expected.** The first measured run, on a feature
whose entire content is a `--loud` flag for a two-line shell script:

| stage | tokens | cost | turns |
|---|---|---|---|
| intake | 137,601 | $0.31 | 4 |
| scenarios | 1,912,707 | $2.02 | 47 |
| spec | 1,554,155 | $2.12 | 41 |
| build | 467,095 | $0.64 | 12 |
| refactor | 705,529 | $0.85 | 17 |
| verify | 592,690 | $0.84 | 14 |
| **total** | **5,369,777** | **$6.78** | **135** |

**$6.78 and 5.4 million tokens for a `--loud` flag.** A single session would have done it
for a fraction of that, and any claim that the flow is worth its cost has to be made
against numbers like these rather than against the argument for the design.

Two things the same run says in the other direction, which is why the number is a question
rather than a verdict. The work was **good**: five tests including a shellcheck pass,
idempotent repeated flags, a stderr diagnostic and exit 2 on an unknown argument — none of
which the task asked for by name and all of which the contract had made obligations. And
the expensive stages are the ones *before* code: scenarios and spec together are $4.14 of
the $6.78, while build is $0.64. Whatever the flow is buying, it is buying it in the
stages that write prose about the work rather than the ones that do it — which is exactly
where the case for cutting stages should start looking.

The counter was also wrong in the direction that flatters: spend was recorded only where a
stage *closed*, and the exit checks return before that — so a blocked stage came out free
and the flow that fails most read as the cheapest. Caught by running a task, not by
reading the code.

**And then the measurement was acted on, which is the only reason to take one.** The flow
went from twelve stages to eight and from twelve roles to three. `scenarios` and `spec`
merged, because they were 61% of the bill and the artifact the second one existed to
produce — `contract` — was required by no stage in the flow. The four review stages merged,
because they were structurally the same stage with four different conditions, reading the
same code and the same green, paying to ingest one diff four times.

The roles collapsed for a related reason: twelve of them differed only in the sentence
describing the work, and Luna already generates that sentence per stage with the contract
in it. What a role is actually for is what a stage cannot express — which tools it denies,
and whether it may continue the previous session. That test leaves two boundaries, not
twelve.

Whether this is cheaper is not yet known. Twelve cold starts became two, and `--resume`
measured about eleven times cheaper than starting cold — but that is arithmetic about the
transport, not a second measurement of the flow. The number to compare against is $6.78.

**A guard aimed at the wrong command is indistinguishable from a broken feature.**
`luna artifact put` runs *inside* a stage's sandbox, where the log is deliberately out of
reach — the agent hands its work to Luna through a socket and Luna is the only writer. But
the CLI resolved and validated the store before dispatching any command, so the guard
protecting against a ghost store refused every artifact command from inside the jail. A
stage produced its contract, could not deliver it, and blocked.

Two things about how it was found. It survived a full test suite because every test ran
outside a sandbox, where the store resolves fine. And it was diagnosed by the *agent* —
which committed a message naming the exact cause ("luna exits 1 on every subcommand
because the recorded log dir is outside this sandbox mount"), explicitly declined to work
around the refusal, and left the artifact behind for a human. The adversarial framing has
a limit: an agent given a clear contract and a hard boundary reported the boundary rather
than defeating it.

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
