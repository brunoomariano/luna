# ADR-0069: Luna starts every agent inside the sandbox

**Status:** Accepted
**Date:** 2026-08-17

## Context

[ADR-0042](0042-four-harnesses-four-ways-to-deny-a-tool.md) passes each harness the flag
that lets it run unattended. For claude there are two, and Luna chose between them by asking
whether **its own process** was contained:

```go
args := unattendedFor(harness, node.Contained())   // reads /proc/self/uid_map
```

Contained meant `--permission-mode bypassPermissions`; uncontained meant `acceptEdits`, which
auto-approves edits but not Bash — so an uncontained agent stops at its first `make test`.
The intent was [INV-core-7](../invariants/core.md): Luna delegates containment rather than
building it, so it only grants the wider permission when something else is holding the
boundary.

**The two facts are unrelated, and measuring one to decide the other was wrong.**

Luna talks to herdr over a Unix socket. herdr is a server that was already running before
`luna run` started and outlives it, and `agent.start` makes the agent a **child of herdr** —
it inherits nothing from Luna's process. So `ai-jail luna run` contains *Luna*, changes what
`/proc/self/uid_map` says, and hands the agent `bypassPermissions` — while the agent itself
runs uncontained, with every check off, on the strength of a sandbox that is not around it.

Measured on a real run: the agent's process tree sat under the herdr server, outside the jail
Luna was in, with `bypass permissions on`.

## Decision

**Luna starts every agent inside the sandbox itself, and refuses to start one without it.**

The agent is launched by typing its command into the worktree's pane, wrapped:

```
HERDR_AGENT=claude ai-jail claude --permission-mode bypassPermissions
```

Three parts, each load-bearing:

- **`ai-jail`** is the containment, and it wraps the *agent* — which is the whole point.
- **`HERDR_AGENT`** is herdr's own answer to a wrapper hiding the real process: it names
  which screen manifest to detect with. Without it herdr sees `ai-jail` and reports no agent
  at all. The pattern is documented by herdr, with `fence` and `nono` as its examples.
- **the flag** is now unconditional, because the condition it depended on is guaranteed.

`node.Contained()` is deleted. The question "is Luna contained?" no longer decides anything,
which is the point: **Luna contains the agent, so it knows.**

### Why not `agent.start`

Its `kind` is a closed set compiled into the herdr binary. Measured against a live 0.8.0,
both through the CLI and raw over the socket:

```
kind: "luna-claude"  →  {"code":"unsupported_agent_kind"}
```

Local manifests in `~/.config/herdr/agent-detection/` only patch detection for agents herdr
already knows; herdr's own docs say a new agent "still requires a Herdr binary update". There
is nowhere in `agent.start` to put a wrapper, so the command has to be typed at the pane
instead — `pane.send_text` with a trailing newline, which is what submits it.

### No sandbox, no agent

Where `ai-jail` is not on `PATH`, Luna **refuses the stage** with `ErrNoSandbox` rather than
falling back to the uncontained form.

The fallback is what shipped before, and it is worse than a refusal: the agent runs holding
permissions granted on the assumption of containment, and nothing on screen says the
assumption is false. A refusal is loud, names what is missing, and is recoverable in one
step.

## Consequences

- **The permission and the containment are the same decision.** Nothing can grant
  `bypassPermissions` to an agent that is not contained, because the same code does both.
- **`ai-jail luna run` is no longer needed**, and doing it changes nothing about the agent.
  It was the way to make this work and never did what it looked like.
- **The worktree layout is one again.** [ADR-0068](0068-a-worktree-lives-where-the-process-can-reach-it.md)
  added a second form under `.luna/wt` for when Luna itself ran inside the sandbox and could
  not see a sibling. Luna reads the tree from outside now and the agent works in it as its
  own cwd, so the sibling is reachable from both and `checkoutPath` has one branch again.
  ADR-0068's other half — `Handover` distinguishing "nothing committed" from "unreadable" —
  stands, and is what would catch this class of thing again.
- **An agent has no herdr-side name.** `agent.start` named it; `pane.send_text` cannot. The
  prompt targets the pane instead — measured: `agent.prompt` resolves a pane id and answers,
  and only an unknown *name* is refused. Reuse on a resumed stage keys on the pane too:
  `worktree.open` returns the pane a stopped run left behind, with `already_open: true`.
- **A newly started agent needs a moment before its first prompt.** herdr reports `idle` as
  soon as it recognises the screen, while the agent is still booting, and `agent.prompt`'s
  wait "first requires an observed state change within 5000ms; otherwise it returns
  `agent_prompt_stalled`". A cold claude takes longer, so every first prompt stalled — the
  prompt was never submitted, and it read as an agent that would not answer. There is a
  settle between detection and the first prompt; `interactive_ready`, the field that would
  have said so, is absent for an agent started this way.
- **Which sandbox is not configurable.** `ai-jail` is named in code. Which sandbox holds the
  boundary is not a per-project preference — it is what INV-core-7 rests on, and a project
  that could swap it for `cat` would be a project with no boundary.

## Alternatives considered

- **Start the herdr server under `ai-jail`.** Every agent would be born contained and
  `Contained()` would coincide with reality. Rejected: it contains the terminal multiplexer
  the person is using, not just Luna's agents, and it makes Luna's guarantee depend on how
  somebody launched an unrelated program — invisible from inside Luna and impossible to
  verify.

- **Ask herdr for a wrapper setting.** Cleanest if it existed. It does not: there is no
  agent-command configuration, and adding a kind needs a herdr release. `HERDR_AGENT` is the
  supported answer, and it is the one taken.

- **Keep the fallback and warn.** A line on stderr before the agent runs uncontained.
  Rejected on the same grounds as every other silent-degradation path in this project: the
  warning scrolls past, the run continues, and the permission is already granted. INV-core-7
  is not a preference to be warned about.

- **Make the sandbox configurable.** A project could name its own. Rejected as above — and it
  would move the security boundary into the same file where `editor` lives.

## References

- [ADR-0042](0042-four-harnesses-four-ways-to-deny-a-tool.md) — four harnesses, four ways to
  deny a tool; the flags this chooses between
- [ADR-0027](0027-luna-runs-under-herdr-as-a-socket-client.md) — Luna is a socket client,
  which is why the agent is herdr's child and not Luna's
- [ADR-0036](0036-herdr-facts-learned-from-a-running-server.md) — measure the protocol, do
  not read it; every fact here was measured against a live 0.8.0
- [ADR-0068](0068-a-worktree-lives-where-the-process-can-reach-it.md) — the worktree layout
  this simplifies back to one form
- [INV-core-7](../invariants/core.md) — Luna delegates containment; this is what makes the
  delegation real rather than assumed
