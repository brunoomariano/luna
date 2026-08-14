# References

Where the ideas came from, and what was rejected from each one.

## SwarmForge — Robert C. Martin

<https://github.com/unclebob/swarm-forge>

Agent squad orchestration in tmux, with worktrees per role and a handoff daemon.
Babashka engine. Studied in depth on 2026-08-06.

**Brought:**

- **Payload synthesized by the system.** The agent fills in structured fields; the
  delivered body is generated. The agent cannot inject prose into the message — which
  eliminates degradation by rewriting along the chain.
- **Instruction to re-read the role at every handoff.** Cheap and effective anti-erosion.
- **Validation with the real tool.** It validates the commit by running `git` — it does
  not check whether the text *looks like* a SHA, it checks whether it resolves to
  exactly one object and whether that object is a commit.
- **Specialization by negation.** Each role declares what it does **not** do. Part of
  the separation is economic: the most expensive operation in the pipeline is run by a
  single role.
- **Loop damper.** "Produced no functional change" as a stopping condition, not just a
  count of iterations.
- **Atomic transition as state.** There, state is the file's location and every
  transition is a rename. The idea was kept; the medium changed.

**Rejected:**

- **Git as a transport channel.** It works well there, but it ties transport to version
  control.
- **Worktree per agent.** Here it is per task — parallelism is between tasks.
- **Queue per role with outbox/inbox.** The FSM is the channel; there are no concurrent
  agents exchanging messages within a task.
- **Notification by injecting keystrokes into the terminal**, with empirically tuned
  pauses. Fragile to changes in the CLIs.

**Mechanical vs. prose — the separation the study reveals.** What it validates with a
script (rejecting with exit 2) and what it merely asks for in prose split like this:

| Mechanical — the script rejects | Prose — no enforcement |
|---|---|
| field whitelist; reserved header forbidden | "always pass it on to the next in the chain" |
| explicit type × field table | "preserve the task name when passing it on" |
| commit validated with real git: 10-hex regex → `--disambiguate` resolves to exactly 1 object → `cat-file -t` says `commit` | "do not send `note` without authorization" |
| body after the blank line = rejected | "work only in your worktree" |
| recipient exists in the role registry | "ignore the notice if you are busy" |

Mechanical covers **message structure and queue transition**. Prose covers **routing
policy and work discipline**. The result: a malformed message never enters the network —
but nothing guarantees that the agent sends the right message, to the right role, at the
right time.

**The reading that closes the study, and that guides this project:**

> It has strong enforcement on **transport** and zero on **flow**.
> We want the inverse — and we can have both.

The FSM governs the flow (what is prose there); the stage contract and tool gating give
the flow the same kind of mechanical rejection that there protects only transport.

**What was missing there and became a requirement here:** there is no detection of a
stuck agent or of a broken chain. A swarm that stops talking stops in silence. Hence the
inactivity watchdog — see [ADR-0019](ADRs/0019-inactivity-watchdog.md).

## Field observations from the same author

Public reports of real execution, August 2026. They are the origin of the failure modes
the design has to cover:

- *"Ensure that everything that can be deterministic, is done with a deterministic tool.
  Don't try to get the poor agents to follow a deterministic process."*
- *"Agents are completely unreliable unless you pin them down with strict rules, and
  repeat those rules at every possible turn."*
- An agent decided that **running** a command meant **printing** it.
- *"Agents don't deal with loops well. They might do the same thing 100 times and the
  101st time do something completely different."*
- Under prolonged compaction, the agents **lost their role identity** — the implementer
  started reviewing and the reviewer was left with no work.
- His FSM had a bug and sent the lead to review an already reviewed document. **The lead
  refused and escalated.** That is the argument in favor of the hybrid lead.

## 12-Factor Agents — HumanLayer

<https://github.com/humanlayer/12-factor-agents>

Principles for LLM software that holds up in production. The ones this design applies:

| Factor | Where it appears |
|---|---|
| 5 — unify execution state and business state | one store, not four places |
| 6 — launch/pause/resume with a simple API | the gate suspends and releases the slot |
| 7 — contact humans with tool calls | the gate is mechanism, not convention |
| 8 — own your control flow | the FSM, not the model, decides the next stage |
| 10 — small, focused agents | one role per responsibility |
| 12 — make your agent a stateless reducer | the node receives context and returns a result |

The central observation: the products that work are *"mostly deterministic code, with
LLM steps sprinkled in at just the right points"*.

## Beads

<https://github.com/gastownhall/beads>

Graph-based tracker for agents: dependencies, computing what is free to start, atomic
claiming. Used for the ordering **between** tasks.

It is not used for the stages **within** a task — modeling the stages × N tasks as
sub-issues would inflate the graph with no gain.

## Agent of Empires and herdr

<https://github.com/agent-of-empires/agent-of-empires> ·
<https://github.com/herdrdev/herdr>

Agent session managers. They are not part of the current design, but they solve the
problem of **running and observing** agent processes — which is still an open decision.

From herdr, the most interesting idea: an API that the agents themselves drive,
including waiting until another agent is genuinely blocked.

## ai-jail and ai-memory

<https://github.com/akitaonrails/ai-jail> ·
<https://github.com/akitaonrails/ai-memory>

Filesystem containment and durable project memory. Orthogonal to orchestration and
already validated in use — Luna composes with them instead of reimplementing them.

## Project practices

<https://akitaonrails.com/2026/05/30/boas-praticas-projetos-codigo-aberto-llm-o-minimo/>

This repository's structure follows the minimum proposed there: a one-command
installation surface, automated CI, and documentation that opens with the **problem**
and not with the stack.
