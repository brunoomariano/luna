# RFC-NNNN: <title of the change>

> Planning of a change — the technical route of how to execute it. Dated document:
> see the obsolescence rule in
> [docs/README.md](../README.md#obsolescence-and-update-rule).
>
> **Identifier.** The file is named `rfc-NNNN-title.md`, with `NNNN` sequential
> zero-padded starting from `0001` (the `rfc-0000-template.md` is the model and does not count). The
> prefix is a fixed `rfc` — not per domain, because an RFC usually crosses domains. The
> **slug is in English**, like the rest of the project. E.g.:
> `docs/RFCs/rfc-0001-dedupe-mqtt-on-consumer.md`.
>
> **Boundary (single source).** The RFC describes the **how** of the change. The **what** and the
> **expected behavior** (functional/non-functional requirements, edge cases,
> acceptance criteria) live in the **PRD** — the RFC **links** the PRD instead of repeating. The
> **why** of a structural choice that outlives the feature becomes an **ADR**. If you are
> listing a product requirement, it moved up to the PRD; if you are recording "why we decided
> this way" permanently, it moved down to the ADR.

**Status:** DRAFT | IN PROGRESS | DONE | OBSOLETE
**Last reviewed:** YYYY-MM-DD
**Source issue:** <tracker ID, e.g. ALERT-45> | —
**PRD:** [<domain>-NNNN](../PRDs/<domain>/<domain>-NNNN-title.md) | —

## Motivation
Why this change needs to happen now. The pain, the trigger, the cost of not
doing it. Situate the reader without assuming the context of the issue — the RFC must stand
on its own. (The product *what* is in the PRD; here it is the *why now* of the execution.)

## Technical proposal
### Overview (guide-level)
The change explained as if taught to another person on the team: what comes to
exist, what changes in the flow, in behavior language. Mermaid when the flow
becomes clearer as a diagram.

```
flowchart TD
  A[Current state] --> B[Proposed change]
  B --> C[Target state]
```

### Detail (reference-level)
The concrete technical route, without descending to the level of a specific `if`/query (that belongs to
`lsh-code-cycle:build`): services/modules touched (real paths), integration points,
affected contracts (API/MQTT/schema), the sequence of the parts that fit together.

- Impacted services/modules (real paths):
- Affected contracts/boundaries:
- Relevant technical constraints:

## Alternatives considered
The execution approaches that were weighed and why they were **not** chosen.
A route decision with no recorded alternatives loses half its value. (If one of
these choices is **structural and permanent**, promote it to an ADR and link it here.)

- **<Approach A>** — discarded because …
- **<Approach B>** — discarded because …

## Drawbacks
What this proposal costs, even being the chosen one — debt taken on, complexity
added, what gets worse before it gets better. Being honest here is what separates
an RFC from a pitch.

## Impact and migration
- Data/persistence (schema migration? backfill?):
- Compatibility (does it break a contract? versioning?):
- Observability (what starts being logged/measured):
- Rollback surface:

## Rollout plan (phased)
The execution steps, in deliverable phases. Each phase must leave the system in a
valid state.

1. **Phase 1 —** …
2. **Phase 2 —** …
3. **Phase 3 —** …

- Feature flag? (yes/no)
- Rollback strategy:

## Open questions
What is not yet resolved and needs a decision before or during execution.
A question without an owner here is an unmitigated risk.

- [ ]
- [ ]

## References
- Issue: <ID>
- PRD: <domain>-NNNN
- Related ADRs: <ADR-NNNN, if the route settled a structural decision>
- PRs: <if any>
