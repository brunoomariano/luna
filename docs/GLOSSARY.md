# Glossary

Every part of Luna, defined once. Look a word up here; read
[architecture.md](architecture.md) to see how the parts fit together.

This is a reference, not a fifth layer of the
[four-document suite](architecture.md) — the same reason `CONTRIBUTING.md` and
`DESIGN.md` sit beside it. It answers *"what does this word mean?"*, which none of the
four asks. Where a definition here and the code disagree, **the code is right and this
file is a bug**.

---

## The pieces

### run

One task from start to finish. Named by an id you choose — `WID-4`, `PROJ-330` — and
carried by the branch `luna/<run>`, so a checkout knows which run it is without being
told.

The unit the ledger groups by, and the only field Luna refuses to invent: a line nothing
can be grouped by is a line nobody can read back.

> One branch per run, not per phase. An older shape was `luna/<run>/<phase>`; it is still
> parsed, but the phase in a branch name goes stale the moment the run moves on, so
> nothing depends on it.

### phase

One named stretch of a run — `forge`, `review`, `close`.

**Luna knows no list of phases and validates no name.** The field is free text. Which
phases exist, what they are called and what order they run in belongs to whoever
conducts; a fixed list inside Luna would be flow control, which is the thing the redesign
removed.

### contract

What a phase owes, as TOML arriving on **stdin**. It names the artifacts the phase
produces and, for each, how it is proven.

Luna keeps none of it. It is written by whoever conducts, from criteria a person
approved, and read once per call.

```toml
phase    = "forge"
produces = ["code", "ci_green"]

[verify.ci_green]
run   = "make ci"        # this project's own command
scope = "full"           # what a zero exit establishes

[verify.code]
kind = "existence"       # nothing proves this, and that is said rather than assumed
```

### artifact

One thing a phase owes, named in the contract.

Verdicts are **per artifact**, never per phase: "forge failed" cannot say which debt went
unmet, and the artifact is what somebody acts on.

### verifier

How one artifact is proven. Either a command (`run` + `scope`) or an existence claim
(`kind = "existence"` + `path`). It is a *declaration* — the contract package executes
nothing.

### evidence

What one check observed: the artifact, the verdict, the scope, the command, the exit
code, and a short detail when it helps.

Recorded whether it passed or failed. A report listing only failures cannot be told from
one where nothing ran.

The detail is bounded, because the ledger line is. An `existence` check with a `path` names
what it found, up to ten paths, and then says how many more there were — the count is
always exact. A folder of a few hundred files would otherwise push the line past the 4 KB
ceiling in INV-2 and be refused partway through a run.

### scope

How much a passing check establishes. Four values, and they form a ladder:

```text
full  >  targeted  >  human  >  existence
```

| Scope | A passing check establishes |
|---|---|
| `full` | the project's own command ran, whole |
| `targeted` | only what the change touched was run |
| `human` | a person looked and said so |
| `existence` | the file is there, and nothing more is claimed |

**Scope never upgrades.** Evidence may prove *more* than the contract asked and be
accepted; it may never prove less and be read as more. An unknown scope is refused from
both sides rather than guessed — a typo must not outrank the floor.

### ledger

The record. One JSONL file, append-only, outside every repository, at
`$XDG_DATA_HOME/luna/ledger.jsonl` (`~/.local/share/luna/ledger.jsonl` by default).

One JSON object per line, each self-contained: reading one never requires reading the
ones before it. That is what lets a concurrent fleet append with no lock.

**Luna writes nothing into your repository** — no state file, no anchor, no ignore entry.
And it refuses to write at all when the ledger's directory is not durable storage: a
record that reports success and evaporates is the worst failure available, because both
sides agree.

### status

Where a run stands. Six values, closed:

| Status | Meaning | In the listing |
|---|---|---|
| `running` | a phase is being worked on | in flight |
| `awaiting_resume` | prepared by a batch, nobody has picked it up | in flight |
| `awaiting_gate` | a person has been asked something | needs somebody |
| `blocked` | stopped for missing information | needs somebody |
| `done` | finished | finished |
| `abandoned` | given up on | finished |

`awaiting_resume` is the one that is easy to miss. It is the whole handoff a batch leaves
behind — there is no file beside it — and it counts as unfinished.

### event

What kind of fact a line is. Seven values, closed: `phase`, `check`, `gate`, `block`,
`unblock`, `autonomy`, `discovery`.

Six of them are written by `luna record`. **`check` is written by `luna check`**, from a
command that actually ran, and that is the distinction the tool exists for.

### autonomy

How much runs without a person: `manual`, `semi` or `auto`. It starts at `manual` and
moves only when asked.

Luna **records** the mode. Which mode clears which gate is a flow decision and belongs to
whoever conducts. Every mode still blocks on missing information, `auto` included.

### conductor

Whoever drives the phases — an agent, a skill, a person, a script.

Luna is called *by* one and never is one. Every "Luna does not decide" in these documents
has the conductor as its other half.

### briefing

The first message `luna session` hands an agent: this repository's open runs, what each is
blocked on, and how to conduct the work.

Composed from the ledger at launch and kept nowhere. It is a message, not a file — Luna
writes nothing into a checkout, and that includes this.

### skill

A directory of documents that teaches an agent how to use a tool, read from the harness's
own directory.

Luna carries one, about Luna itself — a `SKILL.md` and the references beside it — and
`install-skills` writes it out. It knows nothing about your flow: which phases exist and
what they are called stays yours.

### launcher

What composes a session's layers around the agent: sandbox, durable memory, permissions.
`ai-run` by default.

Luna calls one and builds none of it. Each of those layers is another tool's main product,
and taking them over is what the redesign undid.

**A launcher is not a dependency of Luna.** Luna has none — `go.mod` is three lines, and
that includes this. When the *default* launcher is not installed, the agent starts directly
and Luna says what was lost; `--bare` says you meant that and warns about nothing.

A launcher you **name** is never dropped: `--launcher firejail` is a request for
containment, so a missing firejail is refused rather than quietly started without. An absent
default is a missing convenience; an absent named launcher is a broken instruction.

---

## The verbs

| Verb | Question it answers |
|---|---|
| `luna check` | is this phase's delivery proven? |
| `luna contract lint` | is this contract usable at all? |
| `luna record` | *(writes)* something happened that Luna did not verify |
| `luna state` | where is this run **now**? |
| `luna trail` | what did this run **do**? |
| `luna runs` | which runs exist, and which need me? |
| `luna session` | *(starts an agent)* here is what this repository has open |
| `luna install-skills` | *(writes)* teach an agent how to call Luna |
| `luna version` | which build am I talking to? |

### check

Runs every check the contract declares, **over the delivered commit** — in a throwaway
checkout, never over your working tree — and records one line per verdict.

The only verb that decides anything, and what it decides is whether a command returned
zero.

| Exit | Means |
|---|---|
| `0` | proven |
| `2` | not proven — a check observed a failure |
| `1` | Luna could not run at all |

Exit `1` is kept apart from `2` deliberately: a conductor has to tell "the delivery is
not proven" from "Luna is broken", and one code for both makes a broken machine read as a
failed delivery.

### contract lint

Reads a contract and reports **every** way it is unusable, without running anything.
Cheap, and it is where a mistake costs nothing.

It reports all faults at once rather than the first, because fixing one field at a time
costs a round trip per mistake.

### record

Writes a line for something Luna did not verify: a phase starting, a gate answered, a
block, an autonomy change, a discovery.

This is the verb that takes your word for it — which is exactly why it is a different
verb from `check`.

### state vs trail

The distinction worth knowing, and the reason these are two verbs:

- **`state`** — *where is this run now?* Its most recent line. A **tail**, not a fold:
  nothing is reconstructed, because Luna decides no transitions and so has no state to
  rebuild, only a position to report.
- **`trail`** — *what did this run do?* Every line for the run, oldest first: the phases
  it walked, each check with its verdict and scope, the discoveries, the gates, the
  blocks, the notes. Never summarised — a report collapsing four rounds into "3 failed"
  hides which round failed and why.

A conductor deciding what to do next wants one line. A person reading a finished task
wants the whole story.

### runs

The listing: every run, most recently touched first, with what needs a person at the top.

| Flag | Narrows to |
|---|---|
| `--here` | runs that touched the repository you are standing in |
| `--project <r>` | runs that touched a repository you are not standing in |
| `--open` | runs that have not finished |
| `--since <d>` | runs touched within a window |

A run is listed under **every** repository its lines name, not only the latest — a run
that moved between two belongs to both listings.

### session

Starts an agent with a [briefing](#briefing) as its first message.

It reads the ledger, writes nothing, and **replaces itself** with the launcher — nothing of
Luna is left as a parent process. That is the whole reason this verb is allowed to exist:
being the parent process, and driving an agent's human interface, are two recorded failures
of this project, and `execve` is neither.

An open run is **not** an instruction to resume it. The briefing tells the agent to ask
first, for the same reason autonomy starts at `manual`.

### install-skills

Writes the skills this build carries into an agent's skill directory —
`~/.claude/skills/` or `~/.codex/skills/`, or anywhere with `--dir`.

**Idempotent.** It overwrites what it wrote before and deletes nothing else; an install
that refuses because a file is already there is an install nobody runs twice.

The skill is **embedded in the binary**, which is the point: one that documented `luna
report` against a build answering `luna runs` would cost somebody a session. A test holds
every verb the skill names to one `luna help` documents, so the two cannot drift — and an
alias that still answers but is no longer documented fails it.

`--print` writes the skill to stdout for somebody who would rather place it themselves —
each file under a `===== luna/<path> =====` header, since the skill is a tree.

---

## Three words that mean two things

Not pedantry. Both senses of each are real, both are in use, and one has already been
written down ambiguously in a live run.

### gate

1. **In Luna:** an event — a decision a person answered.
2. **In ordinary use:** *the project's own verification command* — `make ci`,
   `pnpm check`, `cargo test`.

A real discovery line in the ledger reads `gate: make ci-check`, which is sense 2 written
into a vocabulary where the field means sense 1. When it matters, say **"the project's
gate"** or **"a gate a person answers"**.

### check

1. **In Luna:** one artifact's verifier being run, the ledger line it leaves, and the
   name of the verb.
2. **In ordinary use:** the project's command, which is frequently spelled `check`.

One sentence in `architecture.md` carries both at once — *"why a later check ran
`pnpm check`"* — and both readings are correct, which is the problem.

### floor

1. **The loop's floor:** the mechanical condition a repeating phase may not leave.
2. **The coverage floor:** the 95% minimum in the Makefile.

Unrelated, both real.

None of the three is renamed. The second senses are what people actually say, and a term
nobody uses is worse than one that needs a sentence.

---

## What Luna is not

Words that describe things Luna deliberately does **not** do, listed because their absence
is a design decision rather than a gap.

| Not this | Why |
|---|---|
| an orchestrator | it opens no worktree, builds no sandbox, and decides no transition. It did all of that for a year and went unused: two things that both want to be the parent process do not compose. `luna session` *starts* an agent and then **stops existing** — it hands over a briefing and `execve`s, so there is no parent to compose against. Starting one process is not orchestrating it. |
| a state machine | it decides no transitions. The last line *is* the state. |
| a sandbox | you compose one before the agent starts. Luna only checks that its ledger landed somewhere durable. |
| a memory | a `discovery` is recorded and **never consulted**. The command arrives in the contract every time; a gate Luna remembered would be project configuration Luna owns. |
| a requirement checker | a phase's `requires` is parsed and enforced by nothing. Deciding from the ledger whether a phase may *start* is flow control — see INV-3. |
