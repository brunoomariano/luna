# ADR-0036: herdr's contract is read from a running server, not from its docs

**Status:** Accepted
**Date:** 2026-08-10

## Context

Wave 5's node layer was written against herdr's documented API, as described by the
wave 5 study. The first live run against a real herdr found that nearly every detail
was wrong — not subtly, but in ways that made every call fail.

Two separate causes. The study read documentation for a version newer than the one
installed, and the installed version then changed under us: 0.6.4 (protocol 11) had
45 methods and no `agent.prompt` at all; 0.8.0 (protocol 19) has 77 and matches what
the study described. An integration written against either one is wrong about the
other.

More importantly, several facts appear in no documentation at all and are only
discoverable by being refused.

## Decision

**The herdr package's contract is what a running herdr accepts, verified by running
one. Documentation is a hint.**

Every fact below was learned by a live 0.8.0 refusing a call, and each is now pinned
by a test so a future change fails loudly rather than at the first real run:

| Fact | How it announced itself |
|---|---|
| the request `id` is a **string** | `invalid type: integer 1, expected a string` |
| the server **hangs up after each reply** | broken pipe on the second call |
| response fields are `workspace_id` / `pane_id`, not `id` | silently empty structs |
| `agent.start` needs `name` **and** `kind` **and** `pane_id` | three successive `missing field` errors |
| agent names are **globally unique** | `agent_name_taken` on the second task |
| names must match `[a-z][a-z0-9_-]{0,31}` | `invalid_agent_name` on `luna-LUNA-1` |
| `wait` is an **object**, not a flag | `expected struct AgentPromptWaitOptions` |
| prompts target the agent **by name**, not by pane | `agent_not_ready` for a valid pane |
| a new worktree's pane is **not yet a shell** | `agent_pane_busy`, then success seconds later |
| an agent is registered **before** it is interactive | `agent_not_ready`, then success seconds later |

Three of these are not shape but **timing**, and they change the design:

1. **Two startup races exist**, and both are retried on their specific error code —
   `agent_pane_busy` when the pane has not reached its prompt, `agent_not_ready` when
   the agent is registered but not yet interactive. Retrying is bounded and matched by
   code, never blanket: retrying every failure would turn an unsupported agent kind
   into a long wait ending in the same refusal.

2. **One agent per task, not per stage.** herdr refusing a repeated name is what
   revealed it. The second stage finds the agent the first started and reuses it; a
   fresh agent per stage would lose whatever context the previous one built and strand
   the old one holding a pane.

3. **Connection per request.** herdr closes the socket after answering, so a client
   that holds one connection works exactly once. `Dial` proves reachability with a
   `ping` rather than by opening and dropping a socket, because a bare connect spends a
   request herdr has already accepted.

## Alternatives considered

- **Trusting the documented API and fixing bugs as they appear in production** —
  rejected, and this is what actually happened before this decision. The integration
  compiled, its tests passed against a fake that agreed with every wrong assumption,
  and the first live run failed on the first call. A fake built from documentation
  tests the documentation.
- **Pinning a herdr version and refusing others** — rejected for now. It would trade a
  real problem for a worse one: Luna would stop working every time herdr released. The
  version skew is real, though, and the mitigation belongs in a later wave — herdr
  reports `protocol` on `ping`, and checking it is cheap.
- **Generating the client from a schema** — rejected: herdr publishes none, and the
  facts that broke the integration are behavioural rather than structural. No schema
  encodes "the pane is not a shell yet".

## Consequences

- **Positive:** the integration works against a real herdr, and every fact it depends on
  is now a test. The fake server in `runner_test.go` imitates the real one's shape —
  string ids, one exchange per connection — so it can no longer agree with a bug.
- **Negative / costs:** the herdr package now encodes version-specific behaviour with no
  version check around it. On a herdr that changes an error code, the retries silently
  stop retrying, and the symptom is a task blocking on its first stage.
- **Impacts:**
  - `ADR-0031`'s allowlist is correct on 0.8.0 — 21 kinds, `--kind` required — and was
    simply absent on 0.6.4. The decision stands;
  - a protocol-version check on connect belongs in a later wave, with a clear message
    naming the version Luna expects;
  - the tests that pin these facts are worth more than most: each one exists because
    the real thing refused a call, and none of them would have been written from
    reading the documentation.

## References

- Related documents: [ADR-0027](0027-luna-runs-under-herdr-as-a-socket-client.md),
  [ADR-0030](0030-the-node-boundary-keeps-herdr-replaceable.md),
  [ADR-0031](0031-agents-start-through-herdrs-allowlist.md),
  [ADR-0034](0034-the-watchdog-delegates-detection-and-owns-the-verdict.md)
