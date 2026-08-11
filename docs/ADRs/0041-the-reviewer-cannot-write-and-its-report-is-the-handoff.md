# ADR-0041: The reviewer cannot write, and its report is the handoff

**Status:** Accepted
**Date:** 2026-08-11

## Context

[ADR-0040](0040-a-role-resolves-to-an-agent-and-mechanical-stages-have-none.md) gave each
stage its own role and recorded a debt in the same breath: nothing stopped the `reviewer`
from editing the code it was reviewing. INV-core-7 does not accept that. Its acceptance
criteria are explicit — a test that a denied tool call is **refused**, a load-time check
that `tools_allow` cannot contradict `not_owns`, and it names the failure directly: *"a
role whose restriction exists only as text in the prompt."*

[ADR-0018](0018-tool-gating-by-pretooluse-hook.md) already decided the mechanism: the
restriction is enforced before the call runs, not asked for in the prompt. What was
missing was whether the harness could carry it. Verified against the installed tools:

- herdr's `agent.start` accepts `-- <AGENT_ARG>...` and passes them through;
- `claude --disallowed-tools Edit Write` exists.

So the chain works end to end. It also does not work for most agents. Of the five
installed on this machine, **only `claude` has the flag** — `codex`, `gemini`, `opencode`
and `copilot` do not.

The second half of the problem is what happens after. A reviewer that cannot write has to
hand its findings to someone who can, and the reducer already has that path:
`ReviewFinding{Aligned: true}` sends the task back to `build`, invalidates `ci_green`
(ADR-0020) and counts the loop against its ceilings (ADR-0023). Nothing produced that
action.

## Decision

**The reviewer runs without Edit and Write. Its report is an artifact, and Luna — not the
agent — turns that report into the action that sends work back.**

### The gating

A role declares what it cannot use:

```toml
[role.reviewer]
agent      = "claude"
tools_deny = ["Edit", "Write"]
```

Luna passes the denial to the harness when it starts the agent. The tool is not offered
and then discouraged; it is absent.

**Where the harness cannot enforce it, the stage refuses to run.** A `reviewer` configured
onto an agent without tool denial produces a block naming the harness and the flag it
lacks — not a degraded review that looks like a real one.

This is stricter than INV-core-7 requires. The invariant permits *"explicit and reported"*
degradation in a harness without pre-execution blocking; this decision does not permit it
at all. The cost is real and is stated below.

### The report, and who acts on it

The reviewer produces `review_report` like any other artifact of its contract, in the
shape this house already uses for review:

- each finding carries **exactly one** tag — `[BLOCKING]`, `[SHOULD-FIX]`, `[NIT]`,
  `[UNCERTAIN]`;
- each has a stable id — `B1`, `S2`, `N1` — which is the handle for the conversation after;
- `[UNCERTAIN]` is a statement about confidence, not a severity between two others, and
  must say what would confirm it.

**Luna reads the report and decides the transition.** A report carrying a `[BLOCKING]`
finding produces `ReviewFinding{Aligned: true}`, and the task returns to `build` with the
report in its context. Anything else lets the flow carry on.

The agent never emits the action. It reports; the code decides — which is INV-core-1
applied to the one place where letting the model decide would look most reasonable.

## Alternatives considered

- **The reviewer calls `luna review-finding --aligned` itself** — rejected. It is the
  direct route and it hands a transition to a model, which INV-core-1 forbids. Nothing
  would stop a false `--aligned`, and nothing would detect one.
- **A gate: the person decides whether work goes back** — rejected. It is the most
  controlled option and it makes `nightly` meaningless: four review stages become four
  places every unattended run stops. A gate is declared by the flow for a reason, not
  added wherever judgement happens.
- **Reported degradation where the harness cannot gate** — this is what INV-core-7 allows,
  and it was rejected in favour of refusing. A review that ran without gating is a review
  whose independence rests on the prompt, and the log saying so afterwards does not give
  the finding back its weight.
- **Prose findings, read by the next agent** — rejected. It is INV-core-6's degradation
  exactly: the handoff carries structure the system generated, not a summary that gets
  rewritten at every hop.

## Consequences

- **Positive:** INV-core-7 becomes mechanical. The reviewer cannot edit, the check is a
  test rather than a hope, and the debt ADR-0040 recorded is closed. The report's shape
  matches what this house already reads, so a person moving between Luna's output and a
  hand-run review is reading the same thing.
- **Negative / costs, and this one is sharp:** a gated role is restricted to harnesses
  that support denial — today, of the agents installed here, that is **`claude` alone**.
  Configuring `reviewer` onto `codex` stops the task rather than degrading it. That is the
  decision working as intended, and it will look like a bug the first time it happens, so
  the error has to name the harness, the flag, and the alternative.
- **Impacts:**
  - `fsm.Role` grows `ToolsDeny`, and the node passes it through herdr's `--` argv;
  - a capability table maps agent kind → whether it can deny tools, and it is a closed
    list for the same reason the gate kinds are: a wrong guess here fails open;
  - reading `review_report` into a `ReviewFinding` needs the report to be parseable, so
    the tagged format is a contract rather than a convention;
  - the four review stages (`qa`, `code-review`, `harden`, `architecture`) all inherit
    this, and all four are the ones ADR-0023's loop ceilings already govern;
  - `tools_deny` contradicting a role's own description should fail when the config
    loads, which is INV-core-7's second acceptance criterion.

## References

- Related documents: [ADR-0018](0018-tool-gating-by-pretooluse-hook.md),
  [ADR-0020](0020-review-finding-invalidates-green.md),
  [ADR-0023](0023-three-separate-loop-ceilings.md),
  [ADR-0040](0040-a-role-resolves-to-an-agent-and-mechanical-stages-have-none.md),
  [invariants](../invariants/core.md)
