# ADR-0045: The agent is fallible, except at the evidence boundary

**Status:** Accepted
**Date:** 2026-08-11

## Context

Luna's documentation describes the agent two incompatible ways, and the contradiction
decides how much containment the engine has to build.

Most of the design treats the agent as **fallible**: it reinterprets an instruction,
abandons a loop, lets a role erode. [INV-core-1](../invariants/core.md) names those four
failure modes, and the answer to all of them is structure — a contract per stage, a fresh
context, a review by someone else. None of it assumes the agent is trying to get around
anything.

Two invariants speak a different language. [INV-core-7](../invariants/core.md) says the
gating **"blocks the call, it does not instruct the agent to avoid it"** — that is
containment, not guidance. [INV-core-4](../invariants/core.md) says **"a CLI exiting with
code zero does not mean the work turned out right"** — that is distrust of a result.

The two cannot both be the rule. A system that only guards against honest error does not
need to block a call; a system that guards against a determined optimiser cannot leave the
shell open next to a denied `Edit`.

Three findings force the question rather than leaving it academic.

**The gap is real, and measured.** A `reviewer` denied `Edit` and `Write` writes files
through `Bash` on three of the four supported harnesses. Only `codex -s read-only` closes
it, and Luna's own table treats codex as the least preferred. Every shipped role names
`claude`. INV-core-7 is not satisfied anywhere in production today.

**The worst case is not the reviewer editing.** Verification runs `sh -c` in the worktree
the agent controlled, with inherited environment. The agent can rewrite the `Makefile`
the check invokes, or put a fake `make` earlier on `PATH`. The resulting `exit 0` enters
the log as reproducible mechanical evidence ([ADR-0035](0035-luna-runs-the-verification-itself.md),
[ADR-0024](0024-the-reducer-is-pure-verification-runs-outside.md)) — the highest-value
claim Luna can make. That is not a check that fails; it is a check that lies with the
credential of truth.

**Optimisers do this without malice.** The published measurements of coding agents
(SpecBench, and the reward-hacking literature the extended study collected) find agents
inspecting their own working directory for expected outputs, and satisfying every visible
criterion while the system underneath stays incoherent. The dominant conclusion is that
**reward hacking arises from architectural failure, not from test manipulation** — the
agent is optimising for the signal available, which is what an optimiser does.

So "the agent is not adversarial" is not a statement about intent. It is a bet about
which behaviours the design has to survive.

## Decision

**The agent is treated as fallible everywhere, and as adversarial at the evidence
boundary.**

The **evidence boundary** is everything that produces an `Evidence` record written to the
log: the verification command, the environment it runs in, and the inputs it reads. Inside
that boundary, containment must make the unwanted outcome **impossible** — an instruction
is not a mechanism.

Everywhere else, containment **instructs**, and the flow catches what slips: the contract
refuses an incomplete handoff, the review reads what was produced, the gate asks a human.

The rule that separates them:

> Luna tolerates an agent that gets the work wrong. It does not tolerate an agent that
> gets the **record** wrong.

The reason is what the log is for. A mistaken artifact is a fact the next stage can find
and the review can reject — the flow is built to absorb it. A false `ci_green` is not a
mistake the flow corrects downstream; it is an assertion with the authority of a verified
fact, entering an append-only log that exists to be the audit
([INV-core-2](../invariants/core.md)). The evidence is the product, and a product that can
be forged has no value.

## Alternatives considered

- **Fallible everywhere** — rejected because it requires rewriting INV-core-4 down to
  "we check the exit code and trust it", which discards the invariant that separates Luna
  from the five systems the original study examined. Every one of them recorded the
  model's claim as fact; INV-core-4 is the refusal to do that, and it is worth defending.

- **Adversarial everywhere** — rejected on cost against benefit, not on principle. It
  would demand OS-level containment on every agent invocation, hermetic execution for
  every stage, and a threat posture for the whole surface. The failure it would prevent
  outside the evidence boundary — an agent editing what it should not — is a failure the
  flow already catches, because a role that produces is not the role that evaluates
  ([INV-core-7](../invariants/core.md)). Paying containment cost for a failure the design
  already absorbs is paying twice.

- **Deciding per harness** — rejected because it makes the guarantee depend on
  configuration. A guarantee that holds under `codex` and not under `claude` is not a
  guarantee; it is a coincidence of the profile in use, and nothing in the log would say
  which one applied.

## Consequences

- **Positive:** the strongest containment goes where the highest-value claim is made, and
  is not spread thin over surface that does not need it. The boundary is nameable, so a
  future decision can be checked against it: does this touch evidence, or not?

- **Positive:** it resolves the contradiction rather than deferring it. INV-core-4 becomes
  the invariant with teeth; INV-core-7 stops promising containment it never had.

- **Negative:** INV-core-7 must be rewritten. Its current wording — *"blocks the call, it
  does not instruct the agent to avoid it"* — falls on the fallible side of this boundary,
  because a reviewer that edits corrupts a review and not a record. The reviewer's tool
  denial stays, and stays valuable; what changes is that it is guidance backed by a
  mechanism where the mechanism exists, not a containment guarantee.

- **Negative:** a promise that Luna held on paper for 44 ADRs is being narrowed. Anyone
  who read INV-core-7 as containment read something the code never delivered, and this ADR
  makes that explicit rather than quietly true.

- **Impacts:**
  - `src/internal/node/verify.go` must run outside the agent's reach — the direction is
    settled here, the mechanism belongs to its own ADR.
  - [INV-core-7](../invariants/core.md) is rewritten to state what the gating does and
    where its floor is.
  - [INV-core-4](../invariants/core.md) gains the boundary explicitly: verification is
    trusted only when the agent could not influence it.
  - Every future containment decision states which side of the boundary it is on.

## References

- Related documents: [invariants/core](../invariants/core.md),
  [ADR-0024](0024-the-reducer-is-pure-verification-runs-outside.md),
  [ADR-0035](0035-luna-runs-the-verification-itself.md),
  [ADR-0042](0042-four-harnesses-four-ways-to-deny-a-tool.md)
