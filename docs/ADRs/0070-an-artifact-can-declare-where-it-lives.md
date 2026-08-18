# ADR-0070: an artifact can declare where it lives

**Status:** Accepted
**Date:** 2026-08-18

## Context

[ADR-0032](0032-the-contract-declares-how-each-artifact-is-verified.md) made every artifact
declare how it is proven, and `existence` is the floor: the artifact was delivered, and that
is the entire claim. What `existence` actually checks is **nothing** — `Prove` returns a
passing verdict without touching the filesystem, and `Describe()` returns the word
"delivered".

Measured on a full run of the shipped flow, ten days after that ADR:

```
worktree  briefing  kind  approach  scenarios  contract
existence existence existence existence existence existence
```

**Six of six artifacts closed on a check that ran nothing.** An agent that ends its commit
message with `Delivered: contract` and commits no file at all closes the stage green.

The immediate cause of the original failure was already fixed —
[RFC-0004](../RFCs/rfc-0004-an-artifact-declares-where-it-lives.md) records `verify` writing
144 lines of `dod_checked` content into a file called `verification`, with the stage closing
and the artifact never appearing — by making the agent declare what it delivered. But that
declaration is the agent reporting on itself, which is the self-reported completion
[ADR-0028](0028-herdr-status-triggers-verification-it-never-closes-a-stage.md) rejects for
status, applied one level down.

## Decision

**An artifact may declare the directory it lives in, and then git answers instead of the
agent.**

```toml
[verify.dod_checked]
kind = "existence"
path = "reports/"
```

Declared, the delivery is checked against the **commit**: `git ls-tree -r --name-only <sha>
-- <path>` either finds a file there or does not. Undeclared, nothing is checked and the
agent's word is the whole record — which is every artifact that shipped before this.

Four decisions shape it, and each closes one of the RFC's open questions.

### The path is not in the flow fingerprint

Where an artifact lives is not whether the stage closed. Putting it in the fingerprint would
strand every open task to move a directory, which is disproportionate for a field that
changes what is checked rather than what was owed.

It falls out of what the fingerprint already writes: `writeProofs` records
`VerifierFor(...).Proves()`, and a path does not raise the scope — a file in the right
directory is still just a file. `TestAPathDoesNotChangeTheFingerprint` pins it, because the
property is one an innocent-looking edit to `writeProofs` would break.

### A directory, not a filename

The agent names the file; the contract names where it goes. This is what swarm-forge's
`features/` does, and it keeps a contract from having to predict a name it cannot know. A
fixed filename would give the agent less to get wrong and give the contract more to be wrong
about.

### A path and a command are mutually exclusive

`ci_green` runs `make ci`, and a path beside it would be a second, weaker check on the same
artifact — with no good answer to "which one decided?". A command already proves what a path
would. The parser refuses the combination rather than picking one.

That makes "has a command" and "is not a document" the same axis in this flow, which is the
answer to the RFC's third question: they are not two axes that happen to line up, they are
one distinction seen from two sides.

### The path sits beside the `Delivered:` line, not instead of it

For an artifact with a path, git decides and the declaration adds nothing. For every artifact
without one — the majority — the declaration is still the only signal there is. They are
different things: the path is evidence, the line is a claim, and ADR-0028 already separates
the two.

### The agent is told

A declared path is named in the brief: *"write it under `reports/`, which is where it is
looked for"*. The check is worthless if the agent has to guess the directory — an agent that
writes the right content in the wrong place would fail a check nobody showed it.

## Consequences

- **`dod_checked` is checkable**, which is the artifact this started with. So are the four
  reports (`qa_report`, `review_report`, `mutation_report`, `arch_report`), which declared no
  verifier at all and now declare `reports/`.
- **The shipped fingerprint is unchanged** — `a7da0f3c7ef41a06` before and after, verified
  against the binary. No open task was stranded by this.
- **Nothing that passed starts failing.** An artifact with no path behaves exactly as before,
  and a stage with no commit yet passes rather than being blamed for not having delivered
  what it is about to deliver.
- **A missing artifact fails with what was looked for.** The evidence carries `<sha>
  committed nothing under reports/`, and a passing one carries the file it found — so an
  audit reads what satisfied the check rather than that something did.
- **An unreadable tree is an error, not an absence.** Reporting a broken repository as a
  missing artifact would blame the agent for it.
- **`existence` still proves existence.** The scope does not rise, and a report that says
  what a person wants to hear passes exactly as one that does not — what is checked is that
  it was written, not that it is right.

## Alternatives considered

- **Make the path a filename.** Stricter and easier to check. Rejected: the contract would
  have to predict names for work it has not seen, and a stage that produces two reports would
  need two declarations for what is one rule.

- **Put the path in the fingerprint.** Coherent — it is part of the exit check, and the exit
  check is what the fingerprint protects. Rejected on proportion: moving a directory would
  refuse the replay of every open task, and the thing being protected is the *requirement*,
  which a path does not change.

- **Replace the `Delivered:` line with the path.** Tempting, since git is the better witness.
  Rejected because it only witnesses the artifacts that have a path, and most do not — the
  line would have to stay for them anyway, and two mechanisms with one name is worse than
  two named things.

- **Check the worktree instead of the commit.** Simpler: the tree is right there. Rejected
  for the reason [INV-core-4](../invariants/core.md) exists — the tree holds uncommitted
  files and is removed when the stage ends, so a check against it says nothing about what was
  handed over.

## References

- [ADR-0032](0032-the-contract-declares-how-each-artifact-is-verified.md) — the contract
  declares how each artifact is verified; this makes its floor checkable
- [ADR-0028](0028-herdr-status-triggers-verification-it-never-closes-a-stage.md) — a
  self-report never closes a stage, which is the principle applied here one level down
- [ADR-0046](0046-the-log-records-which-flow-it-was-written-under.md) — what the fingerprint protects, and why a
  path is not it
- [RFC-0004](../RFCs/rfc-0004-an-artifact-declares-where-it-lives.md) — the route, and the
  run that motivated it
- [INV-core-4](../invariants/core.md) — the verdict describes the delivery, not the tree the
  agent worked in
