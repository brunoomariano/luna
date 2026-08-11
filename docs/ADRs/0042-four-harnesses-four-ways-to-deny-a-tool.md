# ADR-0042: Four harnesses, four ways to deny a tool

**Status:** Accepted
**Date:** 2026-08-11

## Context

[ADR-0041](0041-the-reviewer-cannot-write-and-its-report-is-the-handoff.md) decided that a
gated role runs without the tools it must not use, and that a harness which cannot enforce
that refuses to run the stage. It also recorded a measurement that turned out to be wrong.

That measurement came from grepping each CLI's `--help` for `disallowed-tools`, found the
flag only on `claude`, and concluded the other agents could not gate. Reading the help
text properly says otherwise: every one of them can, and no two do it the same way.

Luna's official harnesses, in order of preference: **claude, pi, codex, opencode.** Others
may be added later; these are the ones supported now.

## Decision

**Luna maps a role's `tools_deny` onto whatever each harness actually offers. The
vocabulary is per harness, and the mapping is a closed table.**

| Harness | Mechanism | What Luna passes | Vocabulary |
|---|---|---|---|
| `claude` | tool denylist | `--disallowed-tools Edit Write` | `Edit`, `Write` — capitalised |
| `pi` | tool denylist | `--exclude-tools edit,write` | `edit`, `write` — lower case |
| `codex` | sandbox policy | `-s read-only` | none — the mode denies writing wholesale |
| `opencode` | agent permission | `permission: edit: deny` in the agent file | `edit` |

Three shapes, not one:

- **`claude` and `pi` deny by name**, and the names differ in case. A role that says
  `tools_deny = ["Edit", "Write"]` means the *capability*, and Luna translates it into
  whatever the harness calls it. The role file does not hold four spellings.
- **`codex` denies by sandbox.** `-s read-only` blocks model-generated shell commands from
  writing; it takes no tool names at all. For a reviewer this is sufficient and arguably
  stronger — the objective is that nothing is written, not that a particular tool is
  absent. It is also coarser: `read-only` cannot express "no `Edit`, but `Bash` is fine".
- **`opencode` denies in the agent definition**, `permission: edit: deny` in the
  frontmatter of a markdown file, not on the command line. Luna generates that file for
  the role rather than passing a flag.

**The table is closed, and an unlisted harness is refused.** Guessing that an agent
supports denial and being wrong fails open — a reviewer that can edit, with nothing saying
so. That direction is the one INV-core-7 cares about most, so an agent Luna has no entry
for stops the stage with a message naming it.

**Capability, not spelling, is what a role declares.** `tools_deny = ["Edit", "Write"]`
names what the role must not do. Whether that becomes `--exclude-tools edit,write` or
`-s read-only` is Luna's problem, and keeping it there is what lets a project switch
`reviewer` from claude to pi without rewriting the role.

## Alternatives considered

- **The earlier measurement — only `claude` can gate** — wrong, and worth recording as
  wrong. It came from grepping for one flag name across four CLIs that use four different
  words for the same idea. The lesson generalises: a capability check that looks for one
  vendor's spelling finds one vendor.
- **Letting the role hold the harness's own vocabulary** — rejected. It would put
  `Edit`/`edit`/`read-only` in the role file, so changing a role's agent would mean
  rewriting its denials, and a role would silently stop denying anything the moment its
  agent changed.
- **Treating codex's sandbox as not-really-gating** — rejected. It is mechanical
  enforcement that happens before the model acts, which is what ADR-0018 asks for. That it
  is coarser than a denylist is a property to record, not a reason to refuse.
- **Falling back to a prompt instruction for an unlisted harness** — rejected for the
  reason ADR-0041 already gave: a restriction that lives in the prompt is the violation
  INV-core-7 names.

## Consequences

- **Positive:** all four official harnesses can run a gated role, so ADR-0041's cost — a
  reviewer restricted to one agent — mostly evaporates. A project can put `reviewer` on
  pi and `implementer` on claude and have both genuinely gated.
- **Negative / costs:** the mapping is version-specific and unversioned. A harness that
  renames a flag turns gating off silently unless Luna checks, and the symptom is a
  reviewer that can write with nothing in the log saying the denial stopped working. The
  same class of problem ADR-0036 recorded for herdr, and the same mitigation applies:
  these facts are learned from the tool, so they need a test that fails loudly.
- **Impacts:**
  - `codex`'s coarseness means a role denying `Edit` but needing `Bash` cannot be
    expressed there; the table should record what each harness can and cannot express,
    not just whether it can deny;
  - `opencode` needs a generated agent file per role, which is a different lifecycle from
    a flag — it is written before the agent starts and belongs to the worktree;
  - the preference order (claude, pi, codex, opencode) is what a role falls back through
    when it names no agent;
  - adding a fifth harness means adding a row, and the refusal for unlisted ones is what
    makes that a deliberate act rather than an accident.

## References

- Related documents: [ADR-0018](0018-tool-gating-by-pretooluse-hook.md),
  [ADR-0031](0031-agents-start-through-herdrs-allowlist.md),
  [ADR-0036](0036-herdr-facts-learned-from-a-running-server.md),
  [ADR-0041](0041-the-reviewer-cannot-write-and-its-report-is-the-handoff.md),
  [invariants](../invariants/core.md)
