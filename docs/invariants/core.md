# Invariants: core

> Rules that **always** hold in Luna, regardless of implementation. They are the
> conceptual contract that any code must preserve — violating one of these is not a code
> bug, it is Luna ceasing to be Luna. Living document. See the standards in
> [docs/README.md](../README.md).

## INV-core-1: the next stage is never decided by the model

**Rule.** Which stage comes now is a decision of the state machine. The model works
**inside** a stage; it never chooses which one is next.

**Why it holds.** It is the premise of the entire project. A flow described in prose is a
suggestion, not a guarantee — and it fails in the four known modes: reinterpretation of the
instruction, abandoned loop, role erosion and a chain broken in silence.

**How it is preserved.** The FSM owns the transition; the node receives context and returns
a result. See [architecture](../architecture/overview.md) and
[ADR-0001](../ADRs/0001-flow-control-out-of-model.md).

**What would violate it.** An agent choosing the next stage; a stage whose output
includes "which stage to run now"; instruction prose that replaces the coded
transition.

> **Deliberate boundary.** The lead is hybrid: when something goes off the rails, a model
> decides **what to do with the failure** — retry, go back, open a gate, block.
> That does not violate the invariant: choosing the response to a failure is not choosing the
> happy path. See [ADR-0002](../ADRs/0002-hybrid-lead.md).

---

## INV-core-2: state is never overwritten

**Rule.** The store is append-only. There is no `UPDATE`: every state change is a
new record.

**Why it holds.** The history **is** the audit. Overwriting erases the evidence of
how the task got where it got — and in a system whose purpose is to be reliable without
continuous supervision, the evidence is the product.

**How it is preserved.** Append-only SQLite, atomic transitions. Killing the process and
restarting rebuilds the exact state, because it was never only in memory. See
[ADR-0010](../ADRs/0010-append-only-sqlite-and-content-addressed-store.md).

**What would violate it.** Any `UPDATE` or `DELETE` on the state table; state
kept only in memory between transitions; compaction that discards history.

---

## INV-core-3: no stage starts without what it requires, nor closes without what it produces

**Rule.** Every stage declares `requires` and `produces`. The FSM does not call the agent of a
stage whose `requires` is not in the context, and does not close a stage that did not deliver the
declared `produces`.

**Why it holds.** It is what prevents an incomplete handoff — and what allows distinguishing "the
model got it wrong" from "the model did not receive what it needed". Without a contract, the failure appears
two stages later, when the symptom has already moved away from the cause.

**How it is preserved.** Three checks: static (before running, walking the
stages in order), input and output. See
[ADR-0004](../ADRs/0004-stage-requires-produces-contract.md).

**What would violate it.** A stage with no declared contract; a `produces` marked as
delivered without verification; a transition that ignores a missing `requires`.

---

## INV-core-4: delivery is verified by running the tool, over what was delivered

**Rule.** What the stage produced is validated by executing the real tool: the test
passes, the commit resolves to exactly one object and that object is a commit. The
verification runs over **what the stage delivered**, not over whatever was left in the
working tree.

**Why it holds.** A well-formed JSON can describe something that does not exist, and a CLI
exiting with code zero does not mean the work turned out right. Checking format validates
the appearance of the delivery, not the delivery.

The second half of the rule is why this is the invariant that carries the adversarial
posture of [ADR-0045](../ADRs/0045-the-agent-is-fallible-except-at-the-evidence-boundary.md).
A verdict about the wrong tree produces the one failure the flow cannot absorb: not a wrong
artifact, which review rejects, but a **false record** — an assertion with the authority of
a verified fact, written into a log that exists to be the audit. The evidence is the
product, and a product that can be forged has no value.

The dominant way that goes wrong is not sabotage. Measured across coding agents, it is
incoherence: an uncommitted file, a local `.env`, a test edited and never committed, a stale
build artifact — a tree that passes and a delivery that does not. Verifying the delivery
rather than the tree closes that class outright, and it does so without confining anything.

**How it is preserved.** The output check of each stage invokes the tool, and the tool runs
against a checkout of what was committed rather than against the tree the agent worked in.
See
[ADR-0005](../ADRs/0005-validate-output-by-running-the-tool.md),
[ADR-0035](../ADRs/0035-luna-runs-the-verification-itself.md) and
[ADR-0045](../ADRs/0045-the-agent-is-fallible-except-at-the-evidence-boundary.md).

**What would violate it.** Accepting a `produces` because the field came filled in;
validating by schema instead of execution; trusting the process exit code; **reporting a
verdict about a tree that is not the delivery it names**.

**What this does not claim.** It is not containment. An agent that controls what it commits
controls what is verified, and nothing here prevents that — confining the process belongs to
a sandbox, which Luna delegates rather than builds
([INV-core-7](#inv-core-7-whoever-writes-does-not-review)). The guarantee is narrower and
worth having on its own: the verdict describes the delivery.

**Deliberate floor.** Not every artifact is mechanically provable — a written report is
delivered, not passed. Evidence carries the **scope** of what was proven
(`full`/`targeted`/`existence`/`human`), and `existence` is the honest floor for prose:
it says the file is there and claims nothing more. That is not an exception to this
invariant but the shape of it — the rule is that evidence never claims more than the check
proved, and scope has no upgrade path
([ADR-0032](../ADRs/0032-the-contract-declares-how-each-artifact-is-verified.md)).

**Acceptance criteria** — the code is not considered done without:

- every artifact in the shipped flow that is mechanically provable declaring a verifier
  that runs a command; an artifact closing on `existence` alone must be a **declared**
  choice, not the default that nobody noticed;
- a test that the recorded evidence's scope satisfies the scope the contract asked for —
  an `existence` proof does not close a stage whose contract wanted a command;
- a test that a change left in the working tree and not delivered does **not** reach the
  verification.

---

## INV-core-5: every stage starts with a clean context

**Rule.** No stage inherits the session of the previous one, even when the role is the same.

**Why it holds.** It attacks role erosion at the source: there is no long session to
degrade. Under prolonged compaction, agents lose the identity of the role — the
implementer starts reviewing and the reviewer is left with no work.

**How it is preserved.** A new process per stage; every handoff payload is prefixed
with the instruction to reread the role and the rules. See
[ADR-0006](../ADRs/0006-fresh-context-per-stage.md).

**What would violate it.** Keeping the agent process alive between stages; reusing
context "because the role did not change".

**Accepted consequence.** The handoff becomes the only bridge between stages — that is why
INV-core-3 stops being optional.

---

## INV-core-6: the handoff carries pointers and a snapshot, never a prose summary

**Rule.** What crosses the boundary between stages are pointers and a content-addressed
snapshot. The receiver reads the real state; nobody interprets it for them.

**Why it holds.** A prose summary reintroduces interpretation into the chain — exactly the
degradation the design exists to eliminate. Every hop would rewrite it, and the message
would degrade along the chain.

**How it is preserved.** The payload is **generated by the system**: the agent fills in
structured fields, the delivered body is synthesized. The agent cannot inject prose into the
message. See [ADR-0007](../ADRs/0007-handoff-carries-pointers-and-snapshot.md) and
[ADR-0008](../ADRs/0008-system-generated-payload.md).

**What would violate it.** A free-text field written by the agent that crosses the
handoff; the receiver trusting the sender's description instead of reading the state.

---

## INV-core-7: whoever writes does not review

**Rule.** The role that produces an artifact is not the role that evaluates it.

**Why it holds.** It is the separation that gives the review its value. An agent evaluating its own
work is not a review, it is a confirmation.

**How it is preserved.** The separation is **structural**: the flow assigns a different
role to the stage that produces and the stage that evaluates, and the engine refuses a
review action from a stage that is not a review stage. On top of that, tool gating is
applied by the FSM **before** the agent starts — the `reviewer` is started with `Edit` and
`Write` denied (see [ADR-0018](../ADRs/0018-tool-gating-by-pretooluse-hook.md) and
[ADR-0042](../ADRs/0042-four-harnesses-four-ways-to-deny-a-tool.md)).

**Where the floor is, stated plainly.** Tool gating removes the **named** tools. It does
not remove the shell, so a role denied `Edit` and `Write` can still write through `Bash` on
any harness whose denial is per-tool rather than per-capability. Measured against the
installed binaries: `claude` and `pi` leave the shell open, `codex -s read-only` does not,
and `opencode` cannot be gated from the command line at all.

**This is accepted, not deferred.** The reviewer's brief tells it not to edit, and on three
of the four harnesses that instruction is the barrier. Under
[ADR-0045](../ADRs/0045-the-agent-is-fallible-except-at-the-evidence-boundary.md) that is
the correct place to spend nothing: a reviewer that writes corrupts a **review**, which the
next stage reads and a person can reject. It does not corrupt a **record**, which nothing
downstream could catch.

**Containment strong enough to be a guarantee is delegated, not built.** Luna spawns agents
through a harness under a multiplexer; a sandbox that confines the process belongs to that
layer, and reimplementing it here would be a worse copy of something the environment already
provides. Where a sandbox is present the guarantee is the sandbox's; where it is absent, the
floor above is what holds, and this invariant claims nothing more.

What the engine does guarantee is the **separation**, which is structural and does not
depend on any harness honouring a flag.

**What would violate it.** The `implementer` running `code-review`; the `cleaner` running
`harden`; a role whose separation exists **only** as text in the prompt, with no structural
counterpart in the flow; a harness whose denial silently does nothing while Luna reports
that it applied.

Note what is **not** on that list: a reviewer that writes through a shell. That is the
declared floor, not a violation — and the distinction matters, because an invariant that
forbids what the code permits teaches people to stop believing invariants.

**Acceptance criteria** — the code is not considered done without:

- a test that a review action originating from a non-review stage is **refused** by the
  engine, so the separation does not depend on the agent honouring its brief;
- a test that a role naming a harness Luna cannot gate fails **before the agent starts**,
  naming the harness and the capability — never starting an ungated agent and reporting
  success.

---

## INV-core-8: no failure is silent

**Rule.** Every task ends in a commit, a gate or a **notified** block. None dies
without someone knowing.

**Why it holds.** A swarm that stops talking stops in silence. In an unattended
execution, the failure nobody sees is more expensive than the failure that interrupts.

**How it is preserved.** Retry limited to 2 attempts (no infinite retry, which is the loop
that does not converge and burns tokens) and, once exhausted, a block with a notification. An inactivity
watchdog for the agent that hangs without failing (see
[ADR-0019](../ADRs/0019-inactivity-watchdog.md)). See
[ADR-0011](../ADRs/0011-failure-retry-rollback-or-block.md).

**What would violate it.** Retry with backoff and no ceiling; a suspended task that does not notify;
an exception swallowed between transitions; a node that stops progressing without anything noticing.

**Acceptance criteria** — the code is not considered done without:

- a test that forces retry exhaustion and verifies that the final state is `blocked` **and**
  that the notification was emitted;
- a test that a node reporting a stall becomes a **decision** rather than an indefinite
  wait — and that it does not spend the retry budget, since a stall says nothing about the
  stage;
- no `running` path that ends without `blocked`, `awaiting_gate` or `done`.

**On the missing third.** An agent that is busy and achieving nothing is not covered: what
exists bounds a turn, and a task circling without converging never exceeds it. `Loop.NoProgress`
is the counter for that and nothing feeds it, because the loops it counts do not run yet
([PRD node-0002](../PRDs/node/node-0002-no-progress-detection-and-the-shape-of-timeouts.md)).
That is a gap in coverage rather than in this invariant — the failure would still end in a
block once a budget runs out, just far later than it should.

---

## INV-core-9: the core knows no issue tracker

**Rule.** The FSM does not know what Plane, Jira or GitHub are. The task enters through
`luna task new` or through an import adapter, which is a command separate from the core.

**Why it holds.** Tying the core to a specific tool would limit the system to that
tool's users. The task can come from anywhere — or from nowhere.

**How it is preserved.** Import adapters are separate commands. See
[ADR-0015](../ADRs/0015-core-knows-no-issue-tracker.md).

**What would violate it.** A tracker client import inside
`src/internal/fsm/`; a state field that only makes sense for one specific
tool.

---

## INV-core-10: the gate does not hold a live process

**Rule.** A gate suspends the task and **frees the slot**. After a short wait in the
terminal, the resource returns to the pool.

**Why it holds.** With N tasks in parallel, gates that held processes would be N
stopped processes with aging context. Parallelism between tasks is the gain of the
design — a gate that cancels it is expensive.

**How it is preserved.** The suspension persists in the store; `luna gate approve` resumes from
the exact point. See [ADR-0012](../ADRs/0012-gate-suspends-and-frees-the-slot.md).

**What would violate it.** An agent blocked reading stdin waiting for the human; a
slot occupied by a suspended task.

---

## INV-core-11: every stage delivers what it declared, including what only the human reads

**Rule.** A stage does not close without delivering **both** the `produces` and the
`produces_for_human` it declared. The second is not optional just because it has no consumer in
the flow.

**Why it holds.** The QA report, the architecture assessment and the diagnosis exist
for someone to decide with them. If their delivery depends on the flow feeling them missing,
they will never be demanded — no stage downstream asks for them.

**How it is preserved.** The output check considers both fields; the static check,
only the `produces` (see
[ADR-0021](../ADRs/0021-produces-for-human-is-a-separate-contract-field.md)). The handoff
records the path and hash of both, so they can be located later.

**What would violate it.** A stage closing without the declared report; an audit
artifact written outside the handoff, with no trace of where it is; treating
`produces_for_human` as a suggestion.

**Acceptance criteria** — the code is not considered done without:

- a test that declares `produces_for_human` and verifies that the stage **does not close** without it;
- a test that verifies that the static check does **not** complain about
  `produces_for_human` having no consumer;
- the handoff carrying the location of each audit artifact produced.

---

## INV-core-12: what needs a human decision is locatable without anyone remembering

**Rule.** A task in `awaiting_gate` and an artifact produced for human reading are
**discoverable by command**, without depending on someone having seen it scroll by in the terminal.

**Why it holds.** Silent death has a second form: not the task that fails without
warning, but the one that **waits forever** because nobody knew it was waiting. In a
system with N parallel tasks and gates that free the slot, suspension is invisible by
construction — the process is not there to remind you.

**How it is preserved.** `luna gates` lists what awaits a decision, with task, stage, gate
type, waiting time and attached artifact; `luna gate show` opens the artifact;
`luna task show` lists what the task produced for human reading. See
[architecture](../architecture/overview.md).

**What would violate it.** A suspended task that only shows up if you know the identifier;
an audit artifact written to an unrecorded path; a gate whose reason exists only
in the log that has already scrolled off the screen.

**Acceptance criteria** — the code is not considered done without:

- a test that suspends a task at a gate and verifies that it appears in the listing;
- a test that verifies that the artifact attached to the gate is retrievable by command, not only
  through the filesystem.
