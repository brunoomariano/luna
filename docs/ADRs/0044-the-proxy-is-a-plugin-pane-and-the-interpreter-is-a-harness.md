# ADR-0044: The proxy is a plugin pane, and the interpreter is a harness

**Status:** Accepted
**Date:** 2026-08-11

## Context

[ADR-0043](0043-luna-chat-is-the-layer-and-the-pane-is-a-proxy.md) settled that `luna chat`
is the conversational layer and that a herdr pane may host it as a proxy. Two things it
left open, and one confusion worth clearing up first.

**The interpreter and the proxy are different things at different layers.** The
`Interpreter` is a Go interface inside `luna chat`: it reads what a person said and picks a
command. The proxy is a pane in herdr that displays that conversation. One is the brain,
the other is the window, and either works without the other — `luna chat` runs in any
terminal, and a proxy with no interpreter shows the message saying none is configured.

## Decision

### The proxy is a herdr plugin

A manifest declaring the pane entrypoint:

```toml
[[panes]]
id        = "chat"
title     = "Luna"
command   = ["luna", "chat"]
placement = "split"
```

Opened with `plugin.pane.open`, which launches the entrypoint as an argv-backed terminal
pane and takes `split`, `tab`, `zoomed`, `overlay` or `popup`.

**This does not contradict ADR-0027, which rejected plugins.** That rejection was about
hosting Luna's *daemon* as a plugin: plugin invocations are subprocesses spawned per call,
capped at 32 in flight with 64KB of captured output and no restart, so a resident process
would hold a slot forever, mute and unsupervised. A pane entrypoint is the opposite shape —
it is a terminal the person interacts with, which is exactly what the mechanism is for.

The alternative was worse for a specific, measured reason: **`pane.split` over the socket
does not accept argv.** Probing it on a live 0.8.0, the only field it takes is `cwd`. So
without a plugin the proxy would open a pane and then type `luna chat` into a shell that
takes seconds to appear — reintroducing the startup race ADR-0036 already had to handle
twice.

### One conversation, all tasks

The chat carries no notion of "the task you are looking at". You name the task; it acts on
that one.

A proxy that inherited the workspace it opened from would let `approve` mean "the task
whose pane I am beside" — convenient, and an implicit target for the one command ADR-0043
deliberately requires confirmation for. The confirmation exists so an approval is never
attributed to a person who did not look; an implicit target would let it be attributed to
the wrong *task* as well.

### The interpreter is an official harness, run non-interactively

Luna calls one of the four official harnesses (ADR-0042) with what the person said and
what Luna currently reports, and reads back the command to run. Verified as present:
`claude --print`, `pi --print`, `codex exec`, `opencode --print`. Verified as usable:
`claude -p` returns exactly the answer and nothing else.

This keeps the promise that **Luna hosts no model**. It brings no API key, no HTTP client
and no SDK into a project whose external dependency count is one, and it reuses the
harness table that already exists.

## Alternatives considered

- **`pane.split` plus `send_text`** — rejected on the measurement above. Nothing to
  install, and it types a command into a shell that may not be ready, which is a race this
  project has already paid for twice.
- **No proxy; open a pane and run `luna chat` by hand** — rejected as the answer, kept as
  the fallback. It works today and needs nothing, and it is what someone does when the
  plugin is not installed.
- **A direct API call to a model** — rejected. Faster per turn, and it puts a network
  dependency, a key and an SDK inside the engine, and breaks the property that Luna hosts
  no model.
- **A deterministic interpreter matching patterns** — rejected as the destination, and it
  is worth building anyway as a test double: it proves the whole loop with no tokens, which
  is what the existing chat tests already use. It is not the natural-language conversation
  that was asked for.
- **A chat scoped to the workspace it opened from** — rejected above.

## Consequences

- **Positive:** the proxy is argv-backed, so no shell race and no typed command. The
  interpreter reuses harnesses the project already supports, so a person who has `pi`
  configured can use it without Luna knowing anything new.
- **Negative / costs:** a process per turn is slower than an API call, and a harness that
  changes its non-interactive flag breaks the interpreter — the same version-drift risk
  ADR-0036 recorded, with the same mitigation: the facts are learned from the tool, so
  they need a test that fails loudly.
- **Impacts:**
  - the plugin manifest is a small file to ship and install, and the proxy is unavailable
    until someone installs it — `luna chat` in a terminal has to keep working, and does;
  - the interpreter must be told to answer with a command and nothing else, and Luna has to
    handle a harness that answers with prose anyway — a model that will not produce a
    command is a turn that says so, not a crash;
  - a turn spawns a process, so the interpreter needs the same deadline discipline every
    other external process gets (ADR-0035);
  - the harness used for interpretation is a configuration choice, and defaults to the
    house preference order.

## References

- Related documents: [ADR-0027](0027-luna-runs-under-herdr-as-a-socket-client.md),
  [ADR-0036](0036-herdr-facts-learned-from-a-running-server.md),
  [ADR-0042](0042-four-harnesses-four-ways-to-deny-a-tool.md),
  [ADR-0043](0043-luna-chat-is-the-layer-and-the-pane-is-a-proxy.md)
