# PRD <domain>-NNNN: <title>

> Product specification of a feature. Dated document: see the obsolescence rule
> in [docs/README.md](../README.md#obsolescence-and-update-rule).
>
> **Identifier.** The file is named `<domain>-NNNN-title.md` — the `<domain>` is
> the same as the path (`docs/PRDs/<domain>/`, the glossary term) and `NNNN` is a sequential
> zero-padded number within that domain (`0001`, `0002`…). The **file name (slug) is in
> English**, like the rest of the project. E.g.:
> `docs/PRDs/alerts/alerts-0001-suppression-on-recovery.md`. It is this ID that the RFC, the ADR
> and the issue reference to close the traceability.

**Status:** NOT IMPLEMENTED | IMPLEMENTED | OBSOLETE
**Last reviewed:** YYYY-MM-DD
**Source issue:** <tracker ID, e.g. ALERT-45> | —
**RFC:** [<rfc-NNNN>](../../RFCs/<rfc-NNNN>-title.md) | —

## Overview
Simple summary of the feature, in stakeholder-facing language.

## Problem
Which user or system pain is being solved.

## Goal
What this feature solves or improves.

## Functional flow (Mermaid)

```
flowchart TD
  A[Event / Trigger] --> B[Processing]
  B --> C{Decision}
  C -->|Path A| D[Result A]
  C -->|Path B| E[Result B]
```

## Expected behavior
### Main flow
1.
2.
3.

### Edge cases
-

### Error handling
- What happens on failures
- What must be logged/observed

## Requirements
### Functional
- RF1:
- RF2:

### Non-functional
- RNF1:
- RNF2:

## System impacts
- Affected services:
- Data/persistence:
- Observability:

## Rollout plan
- Feature flag? (yes/no)
- Rollback strategy:
