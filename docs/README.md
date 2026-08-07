# Luna documentation

This document is the **entry point** and the **standard** for the project's
documentation. It defines which documentation layers exist, what each one answers,
where it lives and what form it follows. All new documentation — written by people or
by agents — must respect this contract.

> **Why this exists:** without an agreement on form, every document reinvents
> structure and tone, old specs describe states that already changed and domain
> concepts have no single source. This standard is the foundation: the following
> layers (glossary, architecture, invariants, decision records) are born under the
> same form.

## How to use this guide

1. Are you going to **create** a document? Use the [classification criteria](#classification-criteria)
   to find out which layer the content belongs to.
2. Found the layer? Go to its row in the [layer map](#layer-map),
   open the indicated location and copy that layer's `_template.md`.
3. Write following the [cross-cutting conventions](#cross-cutting-conventions).

---

## Layer map

Each layer answers **one question** and has a **boundary** — what deliberately does
*not* live in it. When content seems to fit two layers,
the "Does not belong" column and the [classification criteria](#classification-criteria)
disambiguate.

| Layer | Question it answers | Location | Does it age? |
|---|---|---|---|
| **Project README** | How do I run and understand the system? | `../README.md` (root) | yes (living) |
| **Engineering guidelines** | How do I write code here? | `../AGENTS.md` (root) | yes (living) |
| **Contributing** | How do I commit, PR and tag? | `CONTRIBUTING.md` | yes (living) |
| **Glossary** | What does this term mean in this domain? | `glossary/` | yes (living) |
| **Architecture** | How is the system structured, operated and integrated? | `architecture/` | yes (living) |
| **Invariants** | Which rules always hold, regardless of implementation? | `invariants/` | yes (living) |
| **PRD** | What does this feature do and how should it behave? | `PRDs/<domain>/` | yes (dated) |
| **RFC** | How am I going to execute this change (technical route, alternatives)? | `RFCs/` | yes (dated) |
| **Decision record (ADR)** | Why did we decide this way, at that moment? | `ADRs/` | no (immutable) |
| **Changelog** | What changed between versions? | `CHANGELOG/` | accumulates |
| **References** | Where did the idea come from, and what was rejected from it? | `references.md` | yes (living) |

The **Does it age?** column governs the [obsolescence and update rule](#obsolescence-and-update-rule):

- **living** — reflects the current state; updated in place when reality changes.
- **dated** — describes a state in time; gets a status header and may become obsolete.
- **immutable** — records a decision from a moment; never edited, only superseded.
- **accumulates** — an append-only history.

> **Layers this project does not use.** There is no `architecture/api/` nor
> `architecture/contracts/` because Luna is a CLI with no HTTP surface and no
> messaging. There is no `BRs/` — invariants, PRD and glossary cover that purpose.
> If any of these realities changes, the layer enters here before the first file
> is written.

> **Extra layer of this project.** `references.md` records the **prior art**: where
> each design idea came from and what was rejected from it. It is not architecture (it
> does not describe the assembled system) nor an ADR (it is not our decision, it is a
> reading of third parties). In a project born from studying other orchestrators, that
> provenance is first-class knowledge.

### Detail of each layer

**Project README** — `../README.md`
Opens with the **problem Luna solves**, never with the stack. Overview and the quick
path to run it. Mentions that the git-flow lives in [`CONTRIBUTING.md`](CONTRIBUTING.md)
and points there.
*Does not belong:* code rules (go in `AGENTS.md`), technical detail (goes in
`architecture/`), the branch flow itself (goes in `CONTRIBUTING.md`).

**Engineering guidelines** — `../AGENTS.md`
How code is written: directory structure, conventions, tests, Makefile.
Applies to humans and agents; takes precedence over global instructions inside the repo.
*Does not belong:* what the system does (PRD), why a choice was made (ADR), the
branch/merge/tag flow (`CONTRIBUTING.md`).

**Contributing** — [`CONTRIBUTING.md`](CONTRIBUTING.md)
Commits (Conventional Commits), PR description, release tags and the
**git-flow**. It is here — and only here — that the branch/merge/tag flow is normative.
*Does not belong:* production code conventions (go in `AGENTS.md`).

**Glossary** — [`glossary/`](glossary/)
Single source of the domain terms: stage, role, handoff, lead, gate, profile,
contract, node, task. One term, one definition, no ambiguity.
*Does not belong:* how the term is implemented (architecture), the rule that governs it
(invariants).

**Architecture** — [`architecture/`](architecture/)
The system structure and the **why** behind it. Describes *how it is assembled* today.

- [`architecture/overview.md`](architecture/overview.md) — the general design: the
  problem, the shape of the solution, hybrid lead, stage contract, handoff, roles,
  failure, gates, state, extension.
- [`architecture/stages.md`](architecture/stages.md) — the standard stages, with role,
  gate, condition, `requires` and `produces`.

*Does not belong:* the specific, dated decision that led to a choice (ADR), the
step-by-step of a feature (PRD).

**Invariants** — [`invariants/`](invariants/)
Rules that **always** hold, regardless of implementation. They are the conceptual
contract that any code must preserve — what Luna stops being if it is
violated.
*Does not belong:* how the rule is coded (architecture), why it was adopted
(ADR), what a feature does (PRD).

**PRD** — [`PRDs/`](PRDs/)`<domain>/`
Product specification of a feature: problem, goal, expected behavior,
requirements, impacts. Dated — carries `**Status:**`. Identified by
`<domain>-NNNN` (English slug, e.g.: `fsm-0001`).
*Does not belong:* the technical execution plan (RFC), the structural decision (ADR).

**RFC** — [`RFCs/`](RFCs/)
Planning of a change — the technical route of *how* to execute it: motivation,
proposal, alternatives, phased rollout, open questions. Dated; identified
by `rfc-NNNN`. **Links the PRD** instead of repeating requirements.
*Does not belong:* the product requirements (PRD), the permanent decision (ADR).

**Decision record (ADR)** — [`ADRs/`](ADRs/)
Why a decision was made, in the context of that moment, **with the rejected
alternative**. Numbered, immutable. See [`ADRs/README.md`](ADRs/README.md).
*Does not belong:* the current state resulting from the decision (architecture), the
definition of a term (glossary).

**Changelog** — [`CHANGELOG/`](CHANGELOG/)
History of changes between versions, in behavior language. Accumulates; the past is
not rewritten.
*Does not belong:* the motivation of a decision (ADR), the spec of a feature (PRD).

**References** — [`references.md`](references.md)
Prior art: the projects and reports that informed the design, what was **brought** from
each one and what was **rejected**.
*Does not belong:* our decision itself (ADR — the ADR may link the reference that
motivated it), the description of the system (architecture).

---

## Classification criteria

When it is not clear where content belongs, answer **in order** — the first
that matches is the layer:

1. **Is it the definition of a domain term?** → **Glossary**.
2. **Is it a rule that always holds, regardless of how the code implements it?** →
   **Invariants**.
3. **Is it a reading of a third-party project — what we brought or rejected from it?** →
   **References**.
4. **Am I recording *why* we chose a path, at that moment, with
   discarded alternatives?** → **ADR**.
5. **Does it describe how the system is structured today (boundaries, layers, stages,
   flows)?** → **Architecture**.
6. **Does it specify what a feature does and how it should behave?** → **PRD**.
7. **Is it the technical route of how to execute a change?** → **RFC**.
8. **Is it how to contribute (commit, PR, tag)?** → **Contributing**.
9. **Is it how to run/understand the project, or how to write code here?** → **README** /
   **Engineering guidelines**.

### Disambiguating between neighboring layers

- **Invariant vs. Architecture:** the invariant is the conceptual rule ("the store never
  does `UPDATE`"); the architecture is how the code sustains it ("state lives in
  append-only SQLite, with atomic transition"). The rule goes in invariants; the
  structure that guarantees it, in architecture.
- **Architecture vs. ADR:** the architecture describes the **current state** ("the lead is
  hybrid"); the ADR records the **dated decision** that led to it ("on 2026-08-06
  we chose a hybrid lead because…, rejecting a purely code lead and a purely
  model one"). The ADR is not edited when the architecture changes — a new ADR is created
  that supersedes the previous one.
- **ADR vs. References:** the ADR is **our** decision; the reference is the **reading of
  another project**. An ADR may link the reference that motivated it, but the "brought /
  rejected from SwarmForge" lives in `references.md`, not in an ADR.
- **ADR vs. Invariants:** the ADR explains the dated *why*; the invariant describes the
  standing rule the code must preserve. When a decision creates a permanent
  rule, the two link to each other instead of duplicating.
- **PRD vs. RFC:** the PRD is the *what* and the expected behavior; the RFC is the technical
  route of execution. When both exist, the RFC links the PRD and does not repeat requirements.

---

## Cross-cutting conventions

They apply to all layers, except where the layer specifies otherwise.

- **Language:** English throughout — prose, symbol names, paths, commands, issue
  identifiers **and the file name slug** stay in English — like the
  code (see [`../AGENTS.md`](../AGENTS.md)).
- **Format:** Markdown. One `# Title` per document.
- **No YAML front-matter.** Status metadata goes in bold lines at the top
  (`**Status:** …`, `**Last reviewed:** …`), never in a `---` block.
- **Tone:** objective and in **behavior** language, not implementation.
  Describe the observable effect, not the function that produces it. Short sentences.
- **File names:** `kebab-case.md` (e.g.: `stage-contract.md`).
- **Diagrams:** embedded [Mermaid](https://mermaid.js.org/) when a flow or
  structure becomes clearer visually. Prefer a diagram to a long paragraph
  describing steps.
- **Gherkin in bullets**, never in a code block: `- **Given** …`, `- **When** …`,
  `- **Then** …`.
- **Templates:** each layer with a fixed form has a `_template.md` in its folder.
  Always start from it — do not reinvent the structure.
- **Single source:** a fact lives in one layer only. If you need to repeat it, **reference**
  it (relative link) instead of copying.

The **form** of these conventions is validated by `make lint-docs` (part of
`make ci-check`) — it is not trusted to the memory of whoever writes. See
[`../scripts/lint-docs.sh`](../scripts/lint-docs.sh).

---

## Obsolescence and update rule

How a document deals with time depends on its **Does it age?** column in the
[layer map](#layer-map).

### Living documents (README, AGENTS, CONTRIBUTING, glossary, architecture, invariants, references)

They reflect the current state. When reality changes, **update in place** — there is no
"old" version to preserve. An outdated living document is a documentation
bug.

### Dated documents (PRD, RFC)

They describe a state at a moment and may become obsolete. They carry, at the top, a
status header (the enum differs per layer):

```markdown
# PRD
**Status:** NOT IMPLEMENTED | IMPLEMENTED | OBSOLETE
**Last reviewed:** YYYY-MM-DD

# RFC
**Status:** DRAFT | IN PROGRESS | DONE | OBSOLETE
**Last reviewed:** YYYY-MM-DD
```

- On delivery, the PRD goes from `NOT IMPLEMENTED` to `IMPLEMENTED`; the RFC from `IN
  PROGRESS` to `DONE`.
- If the behavior no longer holds, mark it `OBSOLETE` and point to the successor:
  `> ⚠️ Obsolete since YYYY-MM-DD. See: <link>`.
- An `OBSOLETE` document **moves to `archive/`** (`PRDs/<domain>/archive/`,
  `RFCs/archive/`) — `git mv`, never delete. The name and the number do not change; the
  numbering counts `archive/` in the same scope. An `IMPLEMENTED` PRD describes
  standing behavior and **stays**.

### Immutable documents (ADR)

An ADR is **never edited** after acceptance. When a decision is revised, create a
**new** ADR and mark the previous one as superseded:

```markdown
**Status:** Superseded by [ADR-0007](0007-new-title.md)
```

An ADR has no `archive/`: the supersession chain is the value. Details in
[`ADRs/README.md`](ADRs/README.md).

---

## Template index

Always start from the layer's template:

- Glossary — [`glossary/_template.md`](glossary/_template.md)
- Architecture — [`architecture/_template.md`](architecture/_template.md)
- Invariants — [`invariants/_template.md`](invariants/_template.md)
- PRD — [`PRDs/_template.md`](PRDs/_template.md)
- RFC — [`RFCs/_template.md`](RFCs/_template.md)
- Decision record — [`ADRs/0000-template.md`](ADRs/0000-template.md)
  (convention in [`ADRs/README.md`](ADRs/README.md))
