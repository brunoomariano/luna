# ADR-0040: A role resolves to an agent, and mechanical stages have none

**Status:** Accepted
**Date:** 2026-08-11

## Context

[ADR-0039](0039-a-role-per-stage-and-what-carries-context-between-them.md) settled that
each stage runs the agent its role names. It did not say what a role resolves to, nor what
happens to a stage that names none.

`Stage.Role` already exists, and `DefaultFlow()` already names eight roles — `analyst`,
`gherkin`, `implementer`, `cleaner`, `qa`, `reviewer`, `hardener`, `architect`. Nothing
resolves them: `src/stock/roles/` holds a `.gitkeep`, and `herdr.Node` starts the same
agent for every stage from a single `Agent` field.

Six of the fourteen stages name no role, and reading what they produce shows why the gap
is not an oversight:

| Stage | Produces | Is it judgement? |
|---|---|---|
| `setup` | `worktree` | no — `git worktree add` |
| `verify` | `ci_green` | no — run the pipeline |
| `commit` | `commit_sha` | no — `git commit` |
| `discovery` | `repos` | yes — which repositories matter |
| `diagnose` | `root_cause` | yes — analysis |
| `spec` | `contract` | yes — writing |

Today Luna starts an agent for all of them, so it pays a model to run `git commit`.

## Decision

**A role resolves to an agent kind, a role brief, and a skill set. A stage with no role
runs mechanically, with no agent at all.**

```toml
[role.reviewer]
agent  = "codex"
brief  = "You review. You do not write."
skills = ["code-review"]

[role.implementer]
agent  = "claude"
skills = ["build"]
```

Roles ship as defaults in `src/stock/roles/` and a project overrides them in
`.luna/config.toml` — the same shape as profiles (ADR-0017, ADR-0026), for the same
reason: what a role means is the project's business, and the engine only needs to resolve
the name.

**A stage with no role is executed by Luna directly.** `setup` is a worktree, `verify` is
the pipeline, `commit` is git. Luna already runs commands with a real exit code
(ADR-0035), so a mechanical stage produces its artifact and its evidence without a model
in the loop — which is the project's premise applied to the stages where it is easiest to
forget.

The three stages that produce prose keep their agents; `discovery`, `diagnose` and `spec`
will name roles.

## The debt this decision knowingly leaves

INV-core-7 says the role that produces an artifact is not the role that evaluates it, and
its acceptance criteria are explicit: a test that a denied tool call is **refused**, a
check that `tools_allow` cannot contradict `not_owns`, and — where a harness cannot block
before execution — degradation that is **explicit and reported, never silent**. It names
the failure directly: *"a role whose restriction exists only as text in the prompt."*

**This decision does not deliver that.** Roles are distinct per stage, so `code-review`
runs a different agent from `build`, but nothing stops the `reviewer` from editing. The
separation exists in the flow and not in the agent's capabilities.

That is a deliberate scope choice, taken with the alternative on the table, and it is
recorded here rather than left to be discovered:

- **INV-core-7 remains described, not held.** By the rule in `AGENTS.md`, this part of the
  engine is not done, and calling it done would be the thing that rule exists to prevent.
- **The mitigation is a later wave**, with its own ADR: `tools_deny` on the role, passed
  to the agent where the harness supports it (`claude --disallowed-tools`), and a reported
  degradation where it does not.

## Alternatives considered

- **An agent for every stage, including the mechanical ones** — rejected. It is one code
  path instead of two, and it pays tokens to run `git commit` while letting a model get
  wrong something Luna would get right deterministically.
- **A role as nothing but a herdr kind** — rejected. It resolves the name and leaves
  `src/stock/skills/` empty forever, so a role could never carry what makes it that role.
- **Delivering tool gating in this wave** — considered and not taken. It is what INV-core-7
  actually requires, and it is a mechanism of its own: per-harness capability detection, a
  load-time contradiction check, and a degradation path. Bundling it here would make one
  wave carry two unrelated risks.

## Consequences

- **Positive:** mechanical stages stop costing tokens and stop being able to fail
  creatively. Roles become configuration, so a project can put a different model behind
  `reviewer` than behind `implementer` — which is the cheapest form of independence
  available before real gating exists.
- **Negative / costs:** the node layer grows a branch — agent stages and mechanical stages
  are executed differently, and a stage that should have had a role but does not will run
  mechanically and silently do nothing useful. The static check should warn when a stage
  produces prose and names no role.
- **Impacts:**
  - `herdr.Node`'s single `Agent` field becomes a lookup by the stage's role;
  - `src/stock/roles/` gains real content, and each role names one of herdr's 21 kinds
    (ADR-0031);
  - the agent name becomes per stage rather than per task, so `agentName` grows the stage —
    herdr's uniqueness rule makes a collision loud rather than silent (ADR-0036);
  - `discovery`, `diagnose` and `spec` need roles they do not have today;
  - a mechanical stage still produces evidence, and its scope is whatever the command
    proved — not `existence` merely because no agent was involved.

## References

- Related documents: [ADR-0039](0039-a-role-per-stage-and-what-carries-context-between-them.md),
  [ADR-0035](0035-luna-runs-the-verification-itself.md),
  [ADR-0018](0018-tool-gating-by-pretooluse-hook.md),
  [ADR-0031](0031-agents-start-through-herdrs-allowlist.md),
  [invariants](../invariants/core.md)
