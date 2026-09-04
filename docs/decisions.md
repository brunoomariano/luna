# Decisions

What was decided, what was rejected, and what was tried and thrown away. Living document —
if a decision changes, this file changes with it, and the old wording is in git.

The discipline worth keeping is **writing down the rejected alternative**, so a settled
question is not reopened without a new argument. If you are about to propose something
listed as rejected, bring something the record does not already answer.

Everything here is scoped to Luna as it stands: a tool an agent calls at a phase boundary.
The much larger machine that preceded it — a state machine that ran agents, opened
worktrees, built sandboxes and owned a database — is in git history, and the parts of it
worth remembering are in [lessons.md](lessons.md).

---

## The shape

**The agent calls Luna; Luna does not call the agent.** The premise is unchanged — flow
control does not belong to the model — but who holds the flow moved out. A person composes
the session before any agent starts, something conducts the phases, and Luna is invoked at
a boundary to prove what was delivered.

*Rejected: Luna as the parent process.* It is what shipped for a year and it worked;
one full cycle ran seven phases in 124 turns for $7.42. It lost anyway, because two things
that both want to be the parent do not compose — every attempt to add Luna to a working
setup turned into a proposal to replace it. Six of those seven phases also paid a cold
start to re-read code the previous phase had just written.

**What Luna keeps is the part nothing else had.** A delivery verified by running the tool;
a contract that says what "delivered" means; a loop whose floor is mechanical. The
surrounding stack had already reached the same conclusion in prose, without the machine to
enforce it — no executable proof, no compensating control:

> *"sem prova executável, não há compensação"* <!-- lint-language: quoted verbatim -->

**What Luna drops is everything it was reimplementing.** Worktrees, sandbox composition,
agent invocation, durable memory, conducting. Each belongs to a tool that already does it
and is somebody's main product. *Rejected: a thin adapter for each.* An adapter is a second
place for a contract to drift, and the measured version of that mistake is here already —
a role table whose pointer drifted from what it pointed at, with nothing failing.

**Which phases run is not Luna's business.** It sees one contract at a time and never knows
what comes next. *Rejected: keeping the flow in the binary.* Editing a phase would mean
recompiling, and a flow in two places — the binary and whatever conducts — is two places to
disagree.

---

## The contract

**It arrives on stdin, and Luna keeps none of it.** *Rejected: a contract file in the
checkout.* Nothing is written into a repository at all — no state file, no anchor, no ignore
entry to maintain. A file in a worktree also dies with the worktree, and the stack that did
this had already paid for it: lessons were lost on `git worktree remove` before anything
synthesised them.

**Each artifact declares how it is proven, beside the obligation that produces it.** An
artifact with no declared verifier is refused rather than defaulted: a contract that does
not say how something is proven is the ceremony this project exists against.

**Evidence carries scope, and scope never upgrades.** `full`, `targeted`, `existence`,
`human`. An unstated scope on a command defaults *down* to `targeted` — under-claiming only
costs a phase that has to prove more, while over-claiming laundering a targeted run into a
full one is unrecoverable. An unknown scope satisfies nothing and is satisfied by nothing,
in both directions, so a typo cannot outrank the floor.

**`lint` reports every fault at once.** A contract is written by hand at a gate; one error
per run turns a five-minute correction into five rounds of it.

**A `[verify.x]` block for an artifact the contract does not owe is an error.** It is how a
rename leaves a check behind that looks like it is running and is not.

**The TOML parser is hand-written.** *Rejected: a dependency.* The subset a contract needs
is a few dozen lines, and a contract format that pulls in a parser pulls in a supply chain.
`go.mod` is three lines.

---

## Verification

**Output is validated by running the real tool.** Not format, not a schema, not a field
that came back filled in.

**The check runs over the delivered commit, in a throwaway checkout cut from the
repository.** Not the working tree, and not the phase's own worktree.

The worktree was the first attempt and broke twice. It holds uncommitted files, a local
`.env`, a stale build artifact — so a suite that passes there says nothing about what was
handed over. And it is removed when its phase ends: a stalled phase had its tree cleaned up
correctly, and every retry then failed on `chdir: no such file` rather than on the work,
while the delivery itself was green. A commit outlives the tree that produced it.

**A phase that delivered nothing does not pass on somebody else's code.** Measured twice,
and both times a phase was paid for and proved nothing:

- TALLY-3: an empty commit reached the checkout, which read it as `HEAD` and resolved it
  against the main repository. A build phase was billed $0.56 and recorded `make test → 0`
  while its branch sat on the base commit with a clean worktree. The green was true, and it
  was true about code the phase did not write.
- TALLY-5: the guard asked whether a commit *existed*, and a phase that adds none hands back
  its base — which exists and resolves. So the check ran over the code the phase was given,
  which was already green when it arrived. $0.64 over 17 turns, ending clean at its base
  with none of the feature written.

**A check that could not run is not a failing check.** No shell, no such directory, the
deadline hit — those come back as errors. Recording one as a failed verdict would tell
whoever reads the record that the tests ran and lost. A cancellation and a timeout are
reported separately, because "somebody stopped the run" and "the check ran out of time" are
different facts.

**The hooks of the project being checked are turned off in the throwaway checkout.** Not
tidiness: measured on the swarm bench, a tracker's `init` set `core.hooksPath` and installed
a `post-checkout` calling a mise shim; inside a sandbox with a tmpfs `$HOME` mise could not
resolve it, and `git worktree add` exited 1 having created the worktree anyway. Reading that
exit code blocked a phase that had delivered.

**A simulation is not a result.** The earlier version claimed each contract's declared scope
with the truth in a field the renderer never printed, so a run walked to `done` on checks
nothing ran.

`--dry-run` means two different things, one per verb, and both are deliberate. On `check`
it runs nothing and **records nothing** — a simulated verdict in the ledger is the exact
failure above. On `record` it writes the line and **marks it `simulated`**, because a
rehearsal of a flow has to leave a trail to be worth rehearsing, and every view says so.
The rule that unifies them: *a simulation never produces evidence, and never hides that it
was one.*

---

## The ledger

**One file, JSONL, append-only, outside every checkout. The state is the last line.**

*Rejected: replay.* Folding every event from the beginning is what forced flow fingerprints,
sequence ownership, a single writing process and a golden corpus — a large apparatus buying
a guarantee this design does not need, because Luna decides no transitions and therefore
reconstructs no state. What it costs is stated in INV-2 rather than hidden.

*Rejected: SQLite, and the daemon that owned it.* The daemon existed because the store was
central, and the store was central because Luna owned the state. Nineteen orphaned daemons
were once found alive on one machine, each holding a socket and a database nobody could
reach. With lines appended under `O_APPEND`, a concurrent fleet writes without a lock and
the whole chain goes.

**A line is self-contained and bounded.** Reading one never requires reading the ones before
it, and a line that would exceed the size a write lands atomically at is refused rather than
truncated. Measured: eight processes writing 100 lines each, and host and sandbox writing
simultaneously, produced no corrupt line.

**A line that cannot be parsed is reported, not skipped.** Skipping is how a record quietly
stops being the record: the reader would answer confidently from whatever remained readable.

**The ledger knows where the worktree is, not the other way round.** That is what lets a
record outlive the tree it describes.

**Durability is proven before every write, by asking the kernel.** `statfs`, compared
against `TMPFS_MAGIC`.

*Rejected: degrading to a warning.* That is the exact failure that produced the daemon — a
write that succeeded, reported success, and evaporated, with the write and the read agreeing
with each other and with nobody else. *Rejected: inferring containment from the process
environment.* The environment is composed by somebody else and says nothing about where a
write lands. *Rejected: checking once at startup.* A caller that checked and wrote an hour
later is trusting a mount that could have changed, and the check is a single syscall.

> Found while testing: `/tmp` is tmpfs on this machine and on any systemd default, so the
> obvious temporary directory is exactly what the guard refuses. That is the guard being
> right, and the tests say so — the mistake is easy to make in the other direction and
> would have them "fixed" by weakening what they guard.

**`report` is the listing, and it filters by repository, not by the run's latest one.** A
run is listed under every repository its lines name.

*Rejected: grouping by the latest line's project.* The obvious reading, and the ledger
already disproves it: two runs there carry two repositories each, because a batch seeded
them from one checkout and stamped that checkout's remote on their first line while their
later lines named where the work really happened. Filtering on the latest hides such a run
from the repository it was seeded in, and on the first hides it from the one it delivered
to — and both do so silently, which is the failure mode that matters. *Rejected: a
separate `list` verb.* `report` already reads the whole ledger and orders by what needs a
person; a second verb over the same data with a different sort is two things to keep in
step.

**A version number, and the build says which commit it came from.**

*Rejected: leaving `dev` in place.* It was a placeholder nothing set for the tool's whole
life, which made two binaries indistinguishable at exactly the moment that matters — a
skill documenting three flows against a binary that carried one, found by a real session
rather than by a check. `make install` overrides the constant with `git describe --dirty`,
because half the confusing sessions with this tool have been a binary built from
uncommitted work behaving unlike the commit it claims. *Rejected: a `VERSION` file.* A
second place to bump, and the tag is already the thing releases are cut from. The constant
in the source is what a plain `go install` from a clone answers with, and it is
deliberately a pre-release: the six verbs are settled, and the flags on them have moved
twice since.

---

## Autonomy and stopping

**Three named modes: `manual`, `semi`, `auto`.** A gate declares which clears it.

*Rejected: the 0–10 knob.* Eleven values, three meanings — eight of them indistinguishable
while still reading as a choice. The same defect this project already recorded for a set of
profiles that had silently collapsed into each other: a control whose values cannot be told
apart is worse than no control.

**The mode can move mid-run; a gate already open keeps whoever opened it.** Changing the
mode while a question is on somebody's screen would rewrite who answered it.

**Luna records a mode; it does not resolve a gate against one.** `Autonomy.Clears` and
`Autonomy.Blocks` were written to answer "does this mode clear this gate" here, were
tested, and were called by nothing — `make deadcode` found them. They are gone rather than
wired: which mode clears which gate is a flow decision, and putting flow decisions back
inside Luna is precisely what this redesign undid. What survives is the closed set and the
refusal, because a typo recorded as an autonomy would sit in an append-only record forever.

**A phase may stop for missing information at any mode, including `auto`.** The difference
between running without asking and running without thinking is the whole value of an
unattended fleet, and a fleet that cannot stop produces expensive noise.

**A block carries three things, and `looked` is not optional.** The question, where the
answer was looked for and what each source failed to say, and what would unblock it. A block
saying only what it wants is indistinguishable from a phase that did not read what it
already had.

**The exit codes are the contract: 0 proven, 2 not proven, 1 Luna could not run.** One code
for the last two would make a broken machine read as a failed delivery, and whoever conducts
would send work back over it.

---

## Rejected and removed

Knowing what failed is worth more than knowing what shipped. These went with the
redesign; each was real, and each is here because the want behind it may come back.

| What | Why it went |
|---|---|
| **The state machine that ran agents** | see *The shape*. It worked and it did not compose |
| **The central SQLite store and its daemon** | consequences of owning state; both vanish when the state is a file |
| **Replay and flow fingerprints** | bought reproducibility of a decision Luna no longer makes |
| **Worktree, sandbox and agent invocation** | each is another tool's main product |
| **beads, and any external registry** | data loss under Luna's exact concurrency pattern (`bd close` reporting success and not persisting, seven of eight lost); 95 ms per read against 0.08 ms to replay a whole task; three of the four things it was adopted for were written by nothing |
| **Profiles as gate-waiting policy** | two of three had silently collapsed into the same thing |
| **`PreToolUse` as *the* gating mechanism** | right principle, wrong mechanism: each harness denies differently. Worth revisiting now that Luna targets a session it does not start |
| **Two budgets (idle + tool)** | the selector had no caller and could not have one: telling "thinking" from "compiling" needs a signal the runner does not produce |
| **The watchdog as an interface over state** | a replayed state carries no clock, so nothing but a test fake could implement it. It came back as a *query*, because a watchdog that needs a process running cannot catch the stall where everything has stopped |
| **Reacting to the agent's terminal** | it is a view, not a signal; a person tidying their terminal would block a working task |
| **Smaller prompts as a cost lever** | measured and refuted: an 18-clause contract cost *less* than 5040 bytes of prose, and the spread across runs ($0.38–$0.53) is model variance. The cost is the agent's own turns, not what is sent |
| **Driving a harness's human interface** | pty sizing, trust dialogs, ready-marker parsing — all of it disappeared with headless mode. See [lessons.md](lessons.md) |

---

## Where the ideas came from

Sources studied before building, and what each settled. A source matters for the decision it
produced; a rejected borrowing is a rejected alternative like any other.

**SwarmForge** — <https://github.com/unclebob/swarm-forge>. *Taken:* delivery validated by
running `git` rather than by checking that the text looks like a SHA; "produced no
functional change" as a stopping condition rather than a plain iteration count. *Rejected:*
git as the transport channel; notification by injecting keystrokes into a terminal. The
reading that closed the study: it has strong enforcement on **transport** and none on
**flow**. What was missing there and became a requirement here: nothing detects a stuck
agent, so a swarm that stops talking stops in silence.

**12-Factor Agents** — <https://github.com/humanlayer/12-factor-agents>. The central
observation this project is a bet on: the products that work are *"mostly deterministic
code, with LLM steps sprinkled in at just the right points"*. Factor 8 (own your control
flow) is the premise; factor 7 (contact humans with tool calls) is why a block is mechanism
rather than convention.

**ai-jail and ai-memory** — <https://github.com/akitaonrails/ai-jail> ·
<https://github.com/akitaonrails/ai-memory>. Containment and durable memory, orthogonal to
verification and already validated in use. Luna composes with them by **not touching them**:
a person chooses them before the agent starts, and Luna inherits whatever that produced. The
one thing Luna needs from that arrangement is a durable directory, and INV-4 is how it finds
out whether it got one.

**"Harness, loop and graph engineering are mostly ceremony"** —
<https://akitaonrails.com/2026/08/18/hot-take-harness-loop-engineering-graph-engineering-sao-bullshit/>.
The sharpest argument against this project, and it is right about most of it: no multi-agent
arrangement beat *"um modelo forte, sozinho, num loop simples"*, and a chain of ten agents at
90% each is 35% end to end. It provoked the reset that cut the documentation suite by 90%,
and then this redesign, which concedes the orchestration and keeps only the part the article
does not answer: **a command returning zero over the delivered commit is not an aesthetic
preference.**

---

## Open

- **What a conductor should do with a `[BLOCKING]` review finding.** Luna reports; sending
  work back is the conductor's move, and the vocabulary for it is not settled here.
- **`PreToolUse` hooks.** Rejected once as a gating mechanism because harnesses differ. Luna
  now targets a session it does not start, and the harnesses in use both have hooks — this
  is unevaluated rather than settled.
- **Whether a merged build loop judges its own rounds well.** Unmeasured. The argument for
  merging is that a biased fast verdict inside the loop, corrected by an unbiased one after
  it, beats an unbiased slow verdict at every step.
