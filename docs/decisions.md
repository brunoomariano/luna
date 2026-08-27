# Decisions

What was decided, what was rejected, and what was tried and thrown away. Living document —
if a decision changes, this file changes with it, and the old wording is in git.

This replaces 73 immutable ADRs. The discipline worth keeping from that format was never
the immutability — git already preserves history — but **writing down the rejected
alternative**, so a settled question is not reopened without a new argument. That
discipline stays. If you are about to propose something listed under "rejected", bring
something the record does not already answer.

---

## Flow control

**The state machine decides the next stage; the model works inside one.** The premise of
the project. Everything else is measured against it.

**The lead is hybrid: code on the happy path, a model only when something goes off the
rails.** Zero token cost when nothing is wrong. The argument for keeping the model in the
loop at all is a real case: an FSM bug told a lead to re-review a finished document, and
the lead refused and escalated to the human. The deterministic layer gives the skeleton;
the judgement layer catches the skeleton's errors.

**The lead is an agent, so the interface is designed for a reader that can disobey.**
`luna next` returns one closed order and no view of the remaining flow — a lead that could
see ahead would run two stages and report both, which reads as efficiency rather than
disobedience. The lead's reply is printed, never parsed: `luna done` moves the task, and
Luna compares the log position before and after.

> A turn cap was written for this and then removed as unreachable. The did-it-move check
> is strictly stronger, and a ceiling nothing can reach gets trusted without ever having
> held.

**Parallelism is between tasks, never inside one.** One worktree, one lead per task. The
fleet is what finally uses it: `luna fleet run` drives every eligible task at a bounded
concurrency, and needed no new isolation because the isolation was already the design.
A blocked task is not eligible — it stopped for a reason a person has to deal with, and
retrying it nightly would turn a notified block into a nightly bill.

**A stage that came up short is asked again, on the same budget as a failed node.** The
two failures were treated oppositely and backwards: infrastructure got two retries,
an incomplete delivery got none. The retry is briefed with what is missing by name and
told that what it already handed in is kept, so it finishes its contract rather than
starting over. *Rejected:* making the missing artifact non-blocking instead — the exit
check is INV-3, and a contract that can be waived is not a contract; what was wrong was
the number of chances, not the rule.

**A failed node retries at most twice, then blocks with a notice.** "Go back a stage" was
rejected as a third exit: it duplicates the review rollback while forgetting to invalidate
the green, and two doors to the same place means one of them forgets.

**Conditional stages resolve against a closed named set, not an expression language.** A
condition decides which stages a task walked through, which makes it history. An arbitrary
predicate in a file is a flow whose past cannot be reconstructed once the predicate is
edited away.

**The flow is data — embedded TOML in `src/stock/`.**
Order is the filename prefix. Equivalence with the retired Go literals was proven by
fingerprint, not by reading.

**A task picks a flow by name, and several ship.** `full`, `fix` and `chore` —
self-contained directories under `src/stock/flows/`, not a selection over a shared pool,
because a lighter flow rewires what its surviving stages require rather than merely
dropping some. The name goes into the opening event beside the fingerprint: the name is
what a replay resolves, the fingerprint is what it verifies, and a wrong name therefore
produces a refused replay rather than a wrong one. *Rejected:* a "lean profile" toggling
stages within one flow — `requires`, `produces` and stage order are read by the reducer,
so that is history, and one `Complete` closing under one setting and blocking under
another is a log that no longer replays deterministically (INV-2). A lighter flow is a
different fingerprint, and a task that ran without planning should not be auditable as
though it had.

**The flows come from the binary, and a project cannot override them.** `luna init` copied
the stock into `.luna/stock/` and from then on the copy was what ran. It went because the
thing it enabled is the thing that produces drift — two projects on one Luna running
different contracts, with nothing saying so — and the binary already gives what the
override was reaching for: one build, one set of flows, every repository the same, changed
atomically and under review because the flows are code. A project needing a different shape
gets a new named flow in the stock, where everyone can see it. *Rejected:* a central
registry the daemon serves — it solves the same drift and costs version control, which is
the wrong trade for a system whose product is an audit.

**The core knows no issue tracker.** Tasks enter through `luna task new` or an import
adapter outside the core. **CLI first. Go, for a static single binary. Defaults plus user
customization everywhere.**

**Two modes, and the mode is how many agents carry the task.** `luna lead` is solo: Luna
advances and starts one agent per stage, under the single role `fsm.Solo` collapses the flow
onto, so one worktree and one session survive from stage to stage and there is never a
conductor and a worker alive at once. `luna fleet run` is the pack: the lead conducts from
outside the sandbox — the `Ask`/`Run` split holding — and the flow's declared roles do the
work, one worktree and one session each.

The two are different engines and that is now correct rather than duplicated. They were the
same engine reached by four surfaces, which is two places for every fix to land: the block
notification, the gate account and the flow-fingerprint fix had each landed on one and not
the other. Now each engine owns a mode, and neither reaches a state the other does.

A pack is internal to one task. *Rejected:* the cross-task fleet that ran every eligible
task several at a time — it was the reading of "fleet" that fitted a design with no pack in
it, and keeping both would put the word on two different things. `luna fleet report` stays
cross-task, because the morning's question is about every task and answering it starts
nothing.

The mode can change between runs, and that falls out of a rule already written down: `Role`
is policy rather than history — the reducer never reads it, only the node does — so it is
out of the flow fingerprint and a task begun solo replays against a pack. If it did not,
choosing the mode would be a decision nobody could revisit.

The hand-driven mode went with `luna run`. `luna next`, `luna work` and `luna done` stayed,
because they are the interface a stage is carried out through and not a mode: the pack's
lead runs them, and so can a person reading what it did. What went is `luna start`, whose
only purpose was to open a stage for somebody who was not the lead. *Rejected:* keeping
`luna run` as a thin alias — a surface that reaches the same states by another path is the
thing being removed, not the words for it.

**A dry run is a flag, not a third mode.** It exercises a flow with no agent, no worktree and
no model, which is the one shape a lead cannot conduct: there is nobody to conduct with. So
it keeps the node-driven loop, which is now what that loop is for. *Rejected:* deleting it
with the mode it came from — it is how a flow is checked before it costs anything, and the
alternative to a free check is a paid one.

## State and the log

**The reducer is pure; verification runs outside and the verdict arrives inside the
action.** The load-bearing shape of the whole engine: it keeps a transition reproducible
from the log and testable without infrastructure. Cost recorded honestly — nothing in the
type system stops a caller fabricating a verdict.

**Append-only SQLite, no CGO. State is replayed, never stored.**

**The log lives in the main repository and Luna is its only writer.** Resolved with
`git rev-parse --git-common-dir`; `--show-toplevel` is the call everyone reaches for, and
it returns the worktree — the one answer that must not decide where shared state lives.

**The ghost-store guard is one marker, in the git directory.** There were two: a file
beside the log saying "Luna wrote this store", and one in `.git` saying "the log is over
there". Only the second is legible to a contained process, and only the second ever
detected anything — the first was a shortcut past the check, and the contained case creates
its database and its marker together on the same tmpfs, so the pair is always consistent
from inside anyway. Removing it costs one stat and one read per command and takes a file
out of every project that runs Luna.

**`TaskCreated` records a fingerprint of the flow's identity, and replay refuses a
mismatch.** `luna task abandon` is the escape hatch. Airflow rendered historical runs
against the current DAG for ten years and paid with four AIPs and a destructive migration.
The worst case was never the replay that dies — it was the one that passes with an empty
contract.

**An append declares the position it read from and lands only if the log still ends
there.** Reproduced with a probe: two processes writing from one state poisoned a log with
no repair path. The fix had three causes and the third was the one that mattered — a
deferred `BEGIN`, which SQLite refuses to upgrade without consulting the busy handler. WAL
and the connection pool alone still lost eight writes in twenty.

**A field the reducer reads is history; a field only the node reads is policy — and grep
decides, not judgement.** From Camunda: a system validating 1,388 lines of preconditions
still accepted a migration that hung ~10,000 instances, because the *boundary* was wrong,
not the volume. Running the rule mechanically found two fields the hand-drawn boundary had
misclassified.

**Every action names its own JSON fields, and the engine's enums refuse a value they do not
know.** A renamed field decodes cleanly and blocks the task claiming verification failed.
A golden corpus re-encodes and compares bytes, because decoding successfully is not enough
— a field that stops being written decodes fine and comes back missing.

**The log records the gate decision, not the policy that produced it.** Editable policy
plus recomputed replay would mean editing a profile rewrites how past tasks read.

**The log records when each event was written, and the reducer never reads it.** A state
carrying a clock makes replay depend on when it ran.

**A task reports two verdicts, never summed: what the work is, and what the flow did.**
A run can deliver code that passes every check and still stop on a missing artifact, and
for an unattended fleet that is a failure of the machinery with a good product inside it.
One column has to call that either a success or a failure and both readings are wrong.
`Product` is deliberately not a score — Luna cannot judge whether code is good, only
report what its own checks proved — and a simulation is its own value rather than
`verified` with a note beside it, because a reader sees the word and not the note.

**A block records which shape it is, beside the prose and not inside it.** Six kinds:
contract, failed-check, over-budget, tooling, failed-node, no-progress. Parsing the
message back out would silently reclassify every task that hit it the next time somebody
reworded one, and the grouping is the whole value — one needs a person, one needs the
environment fixed, one needs a bigger ceiling, one needs the code to change.

**A task carries a spending ceiling, and it is checked where the next stage would
open.** `CostUSD` was recorded in the log and compared against nothing, so an unattended
run had no limit at all. The check sits in `Advance` rather than where the money is
recorded, and the position is what makes it recoverable: the stage that went over has
already closed, so raising the ceiling and unblocking runs the stage that was *about* to
open instead of re-billing one that already delivered. Overshoot of one stage is inherent
and stated rather than hidden — the harness reports a call's cost after the call, so no
ceiling can refuse one before knowing what it costs. Zero is no ceiling rather than a
ceiling of nothing, which is the opposite asymmetry to `ParseKnob` and for the opposite
reason: an absent knob makes a run more supervised, an absent budget cannot make it
cheaper, only broken. *Rejected:* blocking at the `Complete` that spent the money — the
delivery is real and paid for, and throwing it away to enforce a limit costs more than the
limit saves.

**A task carries its own statement of work in its log.**

**The log directory ignores itself, and the intake hands its briefing over.** A real run
committed `.luna/` into the project, and the agent was not being careless: `briefing` and
`kind` were the only artifacts in the flow proven by a file on disk, and a file only
survives into the next stage's worktree if it is committed. The contract left no other
move. They are handed to the store now, like the plan's three, so nothing reaches the
commit — and `.luna/` carries its own `.gitignore`, because the log holds task statements,
handed-over documents and costs, and `git add -A` from an agent working in the repository
takes all of it. *Rejected:* appending to the project's `.gitignore` — that file belongs to
the project, and Luna editing it is a change somebody else has to review. A directory that
ignores itself needs nobody's permission. `config.toml` stays committable: it is the
project's settings, not Luna's state.

The `kind` artifact went with them, and that is a removal rather than a move. `luna task
new --kind` already records it, the reducer reads it to decide which conditional stages
apply, and nothing but the `plan` stage's own `requires` ever read the file. An agent
writing a word into a file that the log already holds is ceremony with a bill.

**A stage's worktree is bootstrapped before the stage starts.** Every serious repository has
a step between `git clone` and "the tests run", and Luna opens a clean worktree per stage —
so without one, every stage rediscovers it and the ones that cannot fail on a check that was
never about the work. Measured: `tests_green` runs `make test`, `make test` needs `make
build`, and two build stages failed on it for $10.36 of a $19.13 task, producing code that
had been correct since the first attempt. It is `bootstrap` in `.luna/config.toml`, because
one project builds with make and the next with pnpm and neither is the flow's business.
*Rejected:* running it once per task — a worktree is opened clean per stage and removed
after, so whatever the first one installed is not there for the second. That costs an
install per stage, and the alternative costs a stage.

**A shortfall says why, not just what.** A stage that never earned an artifact because a
command came back non-zero and one that simply did not produce the thing were reported as
the same block: *"declared [a b] and did not deliver [b]"*. The evidence was there the whole
time — the node proves every owed artifact and absorbs the failing verdicts — and none of it
was said, so the reader went and ran the suite by hand in a parallel worktree to find out.
It is the *honest* agent that produced the worse message, which is what makes it worth
fixing rather than tolerating: refusing to claim an artifact it could not prove is the
behaviour the whole design asks for, and it left the least to go on.

**A branch held elsewhere keeps its branch.** Luna opens a stage's worktree by branch, so a
branch checked out anywhere else stops the stage. *Rejected:* opening detached at the base
SHA — the branch is what keeps a stage's commits reachable between the worktree being
removed and the next stage branching from them, and a detached HEAD leaves them for `git
gc`. What changed is the message: it names the branch, points at `git worktree list`, and
carries `git worktree add --detach` for whoever wants to read the work without taking it.

## Verification

**Output is validated by running the real tool.** Not format, not exit codes alone.

**A status is a trigger, never a verdict.** All five orchestrators studied recorded the
model's claim as fact. The multiplexer Luna used to run under made the point for us: its
`Done` decayed to `Idle` when a human focused the tab, and an agent matching no detection
rule fell back to `Idle` — so a vendor UI change read as *finished*. A status is a trigger
for verification; only the verification is a verdict.

**Each artifact declares how it is verified, beside the contract that produces it.**
Evidence carries scope, and scope never upgrades. `existence` is the honest floor.

**Luna runs verification itself, in the worktree, against the delivered commit.** Running
it through the agent's own terminal would make the exit code a screen-parsing problem —
the appearance of a delivery rather than the delivery.

**Luna reads the review report; the agent never emits the transition.** Exactly one
`[BLOCKING]` sends work back; a pile of `[SHOULD-FIX]` does not, because summing severities
lets the engine overrule the reviewer by arithmetic. Which report to read comes from the
stage's `produces_for_human`, not a hardcoded name — a hardcoded one worked for one stage
and silently skipped three.

**An aligned finding invalidates the green, and what it invalidates is stage-declared.**
`reviewStages` used to be a hardcoded list of four names, so renaming `code-review` in a
custom flow removed the write/review separation *silently*, inside the code enforcing it.

**A spent loop ceiling stops the task: a gate where someone waits, a block where nobody
does.** This is a measured revision of the original ceiling design — wiring the emitter
produced a task that never terminated (eight rounds against a ceiling of four, oscillation
seven against a limit of two), because a spent ceiling opened a gate only if the profile
said someone was waiting.

**The no-progress signal is the delivered commit; nothing is hashed.** A hash over a diff
carrying a timestamp never matches itself, and a detector that never fires equals no
detector. A round that observed nothing leaves the streak alone — neither counted nor
cleared.

**The agent is fallible everywhere and adversarial at the evidence boundary.** Luna
tolerates an agent that gets the *work* wrong; it does not tolerate one that gets the
*record* wrong. Measured: a reviewer denied `Edit`/`Write` still writes through `Bash` on
three of four harnesses. The worst case is not the reviewer editing — it is an agent
rewriting the `Makefile` the check invokes, producing an `exit 0` that enters the log with
the credential of truth.

## Agents, roles and the sandbox

**The brief belongs to the stage, not to a role.** A stage's TOML holds its contract, its
verifiers, its gate *and* what its agent is told. A stage with no agent runs mechanically —
Luna was paying a model to run `git commit`.

> *Rejected: a role table a stage points into.* It is what shipped first, and the pointer
> drifted from what it pointed at — `architecture.md` described one role while the stock
> held five, and nothing failed. A stage file that answers "what happens here" without a
> second lookup cannot drift from itself.
>
> *Rejected: keeping the table for the stages that share a brief.* `verify` and `audit` are
> both judging stages, so absorbing duplicates ~300 words between two files. Weighed and
> accepted: the duplication is visible and a person can diff it, where the indirection hid
> which stage was told what. `role` survives as the branch grouping it always also was.
>
> *Rejected: a project overriding a role in `config.toml`.* Same reason `.luna/stock/` went
> — the thing it enabled is drift. One build, one set of flows, every repository the same.

**There is no role at all.** Absorbing the brief left `role` a field resolving to nothing,
kept as the branch grouping it had also always been. It did not survive the next reading:
`luna flow check` counted distinct role names and reported *"pack of 5"* for a flow that
runs seven differently briefed agents, because `verify` and `audit` share a label. A name
that makes the tool undercount is worse than no name.

Each of its jobs went to what it had been standing in for. A stage is mechanical when it
names no `agent` — the field that actually decides whether anything starts, where the label
could disagree with it: a stage naming an agent and no role read as mechanical and its agent
was never started. A worktree is named after the stage. A live stage continues the session
of an identically briefed one.

> *Rejected: keeping `role` as a display label.* It is what "pack of 5" was, and a label
> nothing enforces is the drift this whole change was about.
>
> *Rejected: keying the continued session on the stage id.* A stage runs once, so it would
> only ever find its own retry — `live` would be dead while looking alive.

**Four harnesses, four gating mechanisms; a stage declares a capability and Luna
translates.** The table is closed: an unlisted harness is refused, because guessing fails
open.

> The prior measurement here was wrong and is recorded as wrong. Grepping four CLIs for
> `disallowed-tools` found the flag only on claude and concluded the others could not gate.
> All four can — `--disallowed-tools`, `--exclude-tools`, `-s read-only`, and an agent-file
> permission. **A capability check that looks for one vendor's spelling finds one vendor.**

**Luna starts every agent inside `ai-jail` and refuses to start one without it.** The bug
this fixed: Luna chose the permission flag by asking whether *its own process* was
contained — but the agent is a child of the runner, so `ai-jail luna lead` contained Luna
and handed the agent `bypassPermissions` while the agent ran uncontained. Which sandbox is
used is deliberately not configurable; making it so would move the security boundary into
the file where `editor` lives.

**One worktree per task and role, branched from the last delivery.** Reusing a worktree
per role across tasks was rejected on evidence: role branches that never reset compound
drift at every hop, and a shared directory lets a reviewer read uncommitted files and
review something other than what was delivered. A cleanup failure warns; it does not fail
the stage.

**Luna does not integrate — a task ends on its own branch.** The merge was built and then
removed. What went unexamined the first time was whether integrating is Luna's job at all:
nothing consumed the merge commit, no invariant mentions merging, and Luna cannot see what
a merge sets off.

**A scratch artifact is handed over through a socket inside the worktree, keyed per
stage.** Measured: under Landlock a socket in `$HOME`, in `/tmp`, or behind a symlink
answers `ENOENT`; one under the cwd connects. The alternative failed in the worst way
available — inside the sandbox the store path resolved onto a tmpfs root, so `luna task
new` printed `created`, exited 0, and the task never existed. Nothing was denied; the write
and the read agreed with each other and with nobody else.

**What has no caller is either wired or gone.** Twenty-eight dead functions found; the
beads adapter and the merger had *zero* importers despite having decision records of their
own. The flow audit was called only from its own tests — Luna's flow was audited in Luna's
test suite while a project's flow was audited by nothing.

**The workstream is the task's, and every agent of that task writes to it.** It was a
stage's field, `memory = "on"`, defaulting to off — and no shipped stage ever turned it on,
so the feature was parsed, documented and doing nothing for its whole life. What made
per-stage look right was a fear of contamination: writing back from every stage of every
task is how a shared memory fills with the transient. A named workstream is a better answer
to that, because it separates by what the work *is* rather than by how much of it somebody
guessed was worth keeping.

The task is the unit and not the stage or the role, for the reason the contract already
gives: a task is one piece of work handed along. What the planner learned has to be there
for the coder, and a stage choosing its own ledger would answer "what happened on this
task" with a shrug. The default is the project's, named in `.luna/config.toml`; a task may
select another or ask Luna to open one, and what it used is written into `TaskCreated` —
so replaying a finished run reads where the work actually went rather than where today's
config points. *Rejected:* reading the workstream from config at replay time — a task's
history would move whenever somebody edited a file.

*Rejected:* an unnamed default. Empty means no memory at all, which is a different thing
from "nobody configured it": a run with no name lands in whatever workstream the machine
was last pointing at, and that is the contamination arriving by a different door. A machine
with no config writes to `luna`.

## Gates

**A gate suspends and frees the slot.** With N tasks in parallel, gates that held processes
would be N stopped processes with aging context.

**A gate carries the artifact, and the human can approve, adjust or reject.** The adjusted
version is what enters the context downstream.

**A review gate opens on the way *out* of the stage that produced the artifact.** Gates
used to attach on entry, so the gate opened before the artifact existed and asked a person
to review nothing. Measured twice: `luna gate show` printed a name and a blank line, and
the lead correctly refused to judge — a capability that measured 6/6 in isolation had never
once judged a real artifact.

**A guard opens on what the delivery touched, and no autonomy setting gets past it.** Every
other gate is a place in the flow; this one is a property of the work — a migration, a
credential, a deploy pipeline. It carries no judgement criteria deliberately, because
whether dropping a table was intended is not something a model can weigh, and a guard a
knob could wave through would be protection in name only. The patterns are matched against
the diff rather than the tree, by the node, and the match arrives inside the action: so
they are policy, stay out of the fingerprint, and editing them cannot rewrite what stopped
last week. *Rejected:* a glob language — a second syntax to learn and to get subtly wrong,
where a substring is what somebody writing "migrations/" actually means. *Rejected:*
failing open when the diff cannot be read — a guard that goes silent when it breaks is
worse than no guard, so an unreadable diff counts as every pattern matched.

**A gate waits because its stage declared something to answer it with; the knob decides who
answers.** A gate with nothing declared never waits. That is a reversal from the original
design and is stated plainly rather than left to be discovered.

**A gate's account reaches it from whichever of the two positions judged it.** The lead can
judge a gate at two moments, and they need different mechanisms because they differ in
whether the gate exists yet.

When the lead closes a stage itself — a stage no command could prove — the judgement happens while
*computing* the decision that opens the gate, so there is no gate to file anything against.
The verdict and an excerpt ride inside the `Advance` or `Complete` that opens it. When an
agent closes its own stage through `luna done` — which is what `luna lead` does — the gate is
already open before anybody judges, and the account arrives as `GateJudged`, an action that
changes nothing else.

Both were one action before, and it was the wrong one for the position that had the only
caller: the reducer refused `GateJudged` unless a gate was open, and the caller ran before
the gate existed, so every account was refused and none was kept. Measured on TALLY-6, where
the lead found a real contradiction in a contract, and again at knob 9, where it concluded
`cannot-decide` and left nothing behind but a warning on a stream an unattended run has
nobody to read. The refusal was never wrong — it just had no correct caller, and now it has
one. *Rejected:* keeping the whole transcript — what is kept is the verdict and the last 400
characters, because the reasoning is a note in front of whoever answers the gate rather than
an audit record, and a log that stores every judgement in full is one nobody keeps.
*Rejected:* letting the reducer accept a judgement against no gate — the next reader would
have to work out which gate it meant.

A gate that already carries a verdict is not judged again: the same gate reached from both
paths would otherwise cost two model calls for one answer.

This renamed `gate_decision` to `gate.decision` in three actions. The golden corpus caught
it, which is what the corpus is for; a log written before this commit replays an open gate as
none. It is re-recorded rather than bridged: no task older than this change exists.

**`produces_for_human` is a contract field of its own** — checked on the way out, exempt
from the static check, because no stage downstream will ever ask for it.

**The flow declares a pack of roles, and a solo run collapses them onto one.** The same test
has now been run three times and returned three numbers, which is the point of writing it
down: *a role is worth splitting from another only when it denies a different tool, cannot
inherit the previous session, or runs on a different harness.* Twelve roles became three
when the first two conditions were applied honestly. Three became one when a single agent
did every stage, because a lone agent cannot deny itself a tool and has only one session.
Five came back when the pack arrived, because with a role per specialism the second
condition holds by construction and the first works again — the `auditor` denies `Edit` and
`Write`, which is what makes an audit independent rather than a stage that says it is.

The pack is `planner`, `investigator`, `coder`, `cleaner`, `auditor`, and its size is the
flow's: `chore` names one working role, `fix` two, `full` five. Choosing the flow chooses the
depth, which is the shape SwarmForge gives its two-, four- and six-packs.

What a solo run gives up is named rather than hidden: one agent means one session and no
denials, so `audit` re-reads its own work with `context = "fresh"` and does not claim to be
an independent review. What a pack gives up is session continuity across a role change —
`build` and `refactor` are `fresh` in the pack because their predecessors hold a different
role, and `AuditContextChain` refuses a live stage that would inherit a stranger's session.
*Rejected:* letting a live stage fall back to a fresh session at runtime when the roles
differ — the static check is a proof, and trading it for a runtime argument about map keys
is how a guarantee turns into a habit.

**What that costs is named rather than hidden: whoever writes now reviews.** The judging
stage is called `audit`, not `review`, because a review is independent or it is not a
review, and calling it one afterwards would claim a property the design no longer has. What
survives is `context = "fresh"` — the same model re-reading its own work with no memory of
writing it, which is the one half of independence a single agent can have. What is
untouched is the half that never depended on who was asking: a command that runs over the
delivered commit does not care who wrote it. *Rejected:* deleting the stage — on the one
full cycle that reached it, it found a genuine violation of the contract's own clause and
sent the work back, the first time that mechanism ever fired. Whether it still finds that
when auditing itself is unmeasured, and the honest move is to keep the stage and measure.

**Eight stages, not twelve.** `scenarios`+`spec` merged into `plan`; `qa`+`code-review`+
`harden`+`architecture` merged into `review`. The argument is measured: the two planning
stages were $4.14 of a $6.78 task, and `contract` was consumed by no stage; the four review
stages had identical review blocks, verifiers and handovers, read the same two artifacts,
and paid to ingest one diff four times. *Rejected:* merging `build` with `refactor` —
`refactor` must re-earn `tests_green` after rewriting green code, and that boundary is what
forces the proof (it once closed without running anything). *Rejected:* merging `verify`
into `review` — `verify` produces `ci_green`, which the flow consumes, and is the only
stage that earns `scope = "full"`.

**`plan` starts fresh, even though the runtime would probably allow otherwise.** Sessions
are keyed by role, so a live `plan` would resume the session `intake` left under `maker`,
never the investigator's — the runtime is safe. But `AuditContextChain` reads the
*declared* flow, where `diagnose` sits between them, and refuses it. The check stays as it
is: it is a static proof, and trading one for a runtime argument about map keys is how a
guarantee turns into a habit. The cost is one cold start on a bug task. *Rejected:*
making the check role-aware so it follows the session key rather than the declared
neighbour — worth doing if a flow ever needs it, not worth doing to recover one call.

**The review lenses live in the role's brief, not the stage file.** The parser has no
section for them and refuses unknown keys, which is the right refusal: an invented
`[lenses]` table would have parsed as nothing. If lens-by-lens accounting is ever wanted,
that is a stage-file feature to design, not a brief to grow.

**Luna drives no conversational interface.** `luna chat` had a model read what a person
typed, pick one of seven Luna commands, and phrase the output back. The seven commands are
a menu a person can learn, and the layer between them and the person was a model that
could misread. It went, with `internal/interpret`, for the same reason the terminal
transport went one level down: driving a human interface is not what this is for.

What survives is the one thing that package really was — `Harness.Ask`, a subprocess with
a prompt on stdin — moved to `internal/agent`, which already owned the harness table.
*Rejected:* handing the conversation to the lead instead. The lead is per-task, its knob
is that task's autonomy, and its brief is shaped so it never has a choice on the happy
path; `luna gates` and starting another task are cross-task questions, and answering
them through a task's lead widens exactly what `lead/agent.go` narrows.

**`Ask` does not go through the sandbox, and `Run` refuses to start without one.** They
look alike and the difference is load-bearing: `Run` starts an agent that will write code,
`Ask` is the lead reading state to judge a gate, and it runs where Luna runs. Containing
it would need the store reachable from inside the jail, which is what INV-4 keeps out.
Stated as a test rather than a comment, because the two are one edit apart.

**A gate declares which of its criteria are settled by reading.** `judge_by_reading` names
entries of `judge` that the artifact itself answers. Without it the judging brief's rule —
with no checkout, a criterion whose evidence is a claim is UNSUPPORTED — was total, and the
shipped gate's criteria are all judgements about prose that has not become code. So the
lead declined every time and the autonomy knob did nothing at the only gate Luna ships.
Measured on TALLY-3 under `nightly`, which promises to stop at nothing and stopped there.

The rule it does not weaken is the one that matters: the artifact's verdict about itself is
still worth nothing, and the brief still asks for the line that settles each criterion. A
criterion is answered by what the text *says*, never by what it concludes about its own
quality. *Rejected:* giving the lead a checkout instead — the plan gate asks about prose
before there is any delivery to check out, so it would answer a gate Luna does not have.
*Rejected:* inferring which criteria are readable — that is exactly the judgement a model
must not make about its own task. A `judge_by_reading` entry naming no judged criterion is
refused by `luna flow check`, because a typo would silently narrow what the lead may
approve on.

**A gate's criteria are about the artifact it attaches.** The `plan` gate inherited criteria
about `scenarios` and `approach` when those stages merged into it, while a gate still
carries one artifact. A real judgement found it: asked about the scenarios, the lead
correctly answered UNSUPPORTED, having been handed a contract that only references them.
Asking about what is not shown teaches whoever answers to guess. The other two artifacts are
still handed over and still readable with `luna artifact get`; what is no longer claimed is
that this gate reviewed them.

---

## Rejected and removed

Kept because knowing what failed is worth more than knowing what shipped.

| What | Why it went |
|---|---|
| **beads, and any external registry** | data loss under Luna's exact concurrency pattern (`bd close` reporting success and not persisting, seven of eight lost); 95 ms per read against 0.08 ms to replay a whole task, ~1150×; three of the four things it was adopted for were written by nothing |
| **Profiles as gate-waiting policy** | two of the three profiles had silently collapsed into the same thing — a control whose values are indistinguishable is worse than no control, because it still reads as a choice |
| **`PreToolUse` hook as *the* gating mechanism** | right principle, wrong mechanism: each harness denies differently |
| **The merge step** | not just the gate — the step. Nothing consumed it and Luna cannot see its consequences |
| **Two budgets (idle + tool)** | the selector had no caller and could not have one: telling "thinking" from "compiling" needs a signal the runner does not produce. The idle budget had been silently bounding whole turns including builds |
| **The watchdog as an interface over state** | a replayed state carries no clock, so nothing but a test fake could implement it. It came back as a *query* (`luna stuck`), because a watchdog that needs a process running cannot catch the stall where everything has stopped |
| **Reacting to the agent's terminal** | it is a view, not a signal; a person tidying their terminal would otherwise block a working task. It went entirely when the transport did |
| **Fresh context as an invariant** | the mechanism was wrong, not the concern — see [invariants.md](invariants.md). Now a per-stage setting, to be measured |
| **The knob as a scale of execution modes** | it bundles four independent axes into one number — executor, authority, topology, containment — so "maker and critic with every gate still mine" and "solo and unattended for a mechanical upgrade" both become unsayable. The knob is coherent as *authority* alone; which executor runs is already the command you invoke, and a mode carried in task state would be history, replayable, and would change what a past event means |
| **Per-stage enforcement policy (`required`/`advisory`/`report-only`)** | `requires` and `produces` are read by the reducer, so one `Complete` would close under one policy and block under another, and the replay stops being deterministic (INV-2). A contract that can be waived is not a contract — that is what INV-3 is. The want behind it is answered by a leaner *flow*, which is a different fingerprint and honestly so |
| **`produces_for_human` by severity, including `auto-summarize`** | the same boundary: the exit check reads it, so it is history and it is in the fingerprint — kept apart from `Produces` there precisely because moving an artifact between the two is already a different flow. And Luna writing the human's report out of Luna's own log is a document nobody wrote, satisfying a contract clause so the flow closes green |
| **Smaller, cache-stable prompts as a cost lever** | measured and refuted: an 18-clause contract cost *less* at `plan` than 5040 bytes of prose, and the intake/plan spread across runs ($0.38–$0.53) is model variance. The brief is hundreds of bytes against stages of 240k–1.7M tokens — the cost is the agent's own turns, not what Luna sends. `context = "live"` is the lever that did move it: two cold starts a run instead of twelve |
| **`luna init` and the project stock** | the thing it enabled is the thing that produces drift: two projects on one Luna running different contracts, with nothing saying so. The binary already gives what it reached for — one build, one set of flows, under review because flows are code |
| **The anchor beside the log** | it was a shortcut past the guard's own check and detected nothing on its own; the marker in `.git` is what catches a contained process, because it is the one place legible from both sides |
| **Driving the harness's human interface** | pty sizing, trust dialogs, ready-marker parsing — all of it disappeared with headless mode. See [lessons.md](lessons.md) |

---

**The benchmark lives in the repository, and it compares flows rather than Luna against a
bare agent.** That second comparison is known and will not move: a strong model in a
simple loop delivers a coherent case well, and orchestration buys containment, evidence
and a log rather than a better diff. The solo row stays as a baseline so the multiple is
visible, and the comparisons worth running are flow against flow and this build against
the last. Product and flow are separate columns, because a run can be 8/8 on the code and
blocked on the machinery, and for an unattended fleet that is a failure. Scoring is
objective — an input and an exact expected line per case — since a benchmark whose product
score is a judgement measures the judge. It spends real money and never runs in CI.

---

## Where the ideas came from

Sources studied before building, and what each one settled. Kept here rather than in a
file of their own because a source only matters for the decision it produced — and a
rejected borrowing is a rejected alternative like any other.

### SwarmForge — Robert C. Martin

<https://github.com/unclebob/swarm-forge> — agent squads in tmux, worktree per role,
handoff daemon, Babashka engine.

**Taken:** the payload is synthesized by the system, so an agent fills structured fields
and cannot inject prose into the chain; the role is re-read at every handoff; delivery is
validated by running `git` rather than by checking that the text *looks* like a SHA;
roles specialize by negation, declaring what they do not do; "produced no functional
change" as a stopping condition rather than a plain iteration count.

**Rejected:** git as the transport channel (it ties transport to version control); a queue
per role with outbox/inbox (the FSM is the channel); notification by injecting keystrokes
into a terminal with hand-tuned pauses.

**Reversed:** worktree per agent was rejected here, on the reasoning that parallelism is
between tasks and a worktree therefore belongs to one. That was true of a design with no
pack in it. A pack is N roles inside one task, and each needs a checkout it can commit to
without merging with a peer mid-stage — so the worktree is per role, keyed by task and
role, and two tasks still cannot see each other's work because the task is half the key.
The reading that was wrong was not "per task" but the premise under it, and the premise
changed when the second mode arrived.

The reading that closes the study: it has strong enforcement on **transport** and none on
**flow**. Luna wants the inverse, and the stage contract is where it gets it. What was
missing there and became a requirement here: nothing detects a stuck agent, so a swarm
that stops talking stops in silence.

### 12-Factor Agents — HumanLayer

<https://github.com/humanlayer/12-factor-agents>

| Factor | Where it lands |
|---|---|
| 5 — unify execution and business state | one store, not four places |
| 6 — launch/pause/resume with a simple API | the gate suspends and releases the slot |
| 7 — contact humans with tool calls | the gate is mechanism, not convention |
| 8 — own your control flow | the FSM decides the next stage |
| 10 — small, focused agents | one role per responsibility |
| 12 — make your agent a stateless reducer | the node takes context and returns a result |

The central observation, and the one this project is a bet on: the products that work are
*"mostly deterministic code, with LLM steps sprinkled in at just the right points"*.

### Beads

<https://github.com/gastownhall/beads> — graph tracker for agents. Adopted, then removed;
the reasoning is in the table above. Still worth reading for the dependency graph, which
is the part Luna never used and the part a tracker should be judged on.

### Agent of Empires and herdr

<https://github.com/agent-of-empires/agent-of-empires> ·
<https://github.com/herdrdev/herdr>

Agent session managers. Luna ran under herdr until August 2026 and no longer does — see
the table above. `herdr notification show` is still shelled out to when installed, as one
external notifier among the possible ones. The idea worth keeping: an API the agents
themselves drive, including waiting until another agent is genuinely blocked. Not needed
while parallelism is between tasks rather than inside one.

### ai-jail and ai-memory

<https://github.com/akitaonrails/ai-jail> · <https://github.com/akitaonrails/ai-memory>

Filesystem containment and durable project memory. Orthogonal to orchestration and
already validated in use, so Luna composes with them instead of reimplementing them: the
sandbox is INV-4, and every agent runs inside `ai-memory run --workstream <name>`.

**Taken:** the workstream as the unit of memory, and its two refusals as the mechanism —
selecting a name that does not exist is a 404 and creating one that does is a 409, both
before the agent starts, so neither wastes a model call and neither falls back to whatever
workstream the machine was pointing at. Measured against ai-memory 1.32.1.

**Rejected:** letting an unknown name create its own workstream — a typo would open a
second ledger and the run would look fine.

### Project practices

<https://akitaonrails.com/2026/05/30/boas-praticas-projetos-codigo-aberto-llm-o-minimo/>
— a one-command installation surface, automated CI, and documentation that opens with the
**problem** rather than the stack.

<https://akitaonrails.com/2026/08/18/hot-take-harness-loop-engineering-graph-engineering-sao-bullshit/>
— the argument that harness and graph engineering are mostly ceremony. It is what
provoked the August 2026 reset: the documentation suite went from 95,000 words to 10,000,
the terminal transport was deleted, and the token counter exists because the article's
sharpest point is that a tool with no measurement is an aesthetic preference.

---

## Open

- **Conditional `requires`.** `build` should require `contract` only when `spec` ran. The
  static check does not understand conditional requires, so `contract` stays out of
  `requires`; the gap is recorded in `070-build.toml` itself.
- **Notification channels.** The queryable state is the base and does not depend on
  anything external. Which channel to push through is left open until real use answers it.
- **Token accounting.** Being added now that the transport reports usage.

- **Skills that teach an agent to use Luna.** `Role.Skills` parses and `Order` carries it;
  nothing reads it and `src/stock/skills/` is empty. What a skill would hold is what the
  brief re-teaches at every single call — the handover (`luna artifact put`), the severity
  tags, what a contract admits. Deferred on purpose until the flow has been run enough to
  know what an agent actually gets wrong, because a skill written from a guess becomes a
  second place for the brief to disagree with. Hooks for the same purpose are unevaluated.

