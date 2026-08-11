# The herdr proxy

**Status:** Living

A herdr pane that runs `luna chat`, so the conversation sits beside the tasks it is
about instead of in a separate terminal.

It is a window, not a system. Everything the conversation does goes through Luna's own
CLI (ADR-0043), so removing the proxy costs the convenience of a pane and nothing else —
`luna chat` keeps working in any terminal, and that is the fallback whenever anything
below goes wrong.

## Install

```sh
luna plugin install
```

That is the whole of it. It links the manifest shipped in `plugin/`, and prints the
command that opens the pane.

Then, from herdr:

```sh
herdr plugin pane open --plugin luna.chat --entrypoint chat --placement split
```

`--placement` also takes `tab`, `zoomed` or `overlay`.

## What the command checks, and why

Each of these fails with a message about herdr rather than about Luna, and a person
meeting one reads it as a broken plugin rather than a missing prerequisite.

| Checked | What you see | Why it matters |
|---|---|---|
| `herdr` is on PATH | *"the proxy is a herdr plugin — `luna chat` works in any terminal without it"* | the proxy is optional, and the message says so |
| the manifest exists | the paths that were tried | herdr would otherwise complain about a path nobody typed |
| `luna` is on PATH | a note, not a failure | the pane falls back to the checkout, so it still works |

The last one is the one that actually happened while building this. herdr resolves a
pane's argv against **its own PATH**, not the shell the install was typed in. Working
from a checkout there is no installed `luna`, and herdr answers:

```
Unable to spawn luna because:
No viable candidates found in PATH "..."
```

which reads as a broken plugin. So the manifest runs `plugin/open-chat.sh`, which prefers
an installed `luna`, falls back to `bin/luna` in the checkout the plugin was linked from,
and explains itself when there is neither.

## Status and removal

```sh
luna plugin status      # what herdr has registered
luna plugin uninstall   # unregister; the files in this repository stay
```

`uninstall` unlinks rather than uninstalling in herdr's sense: the manifest lives in this
repository, and herdr's own uninstall removes checkouts it manages. Deleting the files
would mean deleting part of the project.

**Unlinking needs a running herdr; linking does not.** herdr's documentation says both
work without a server, and only linking does — verified against 0.8.0. The command says
so rather than passing on `server_not_running`.

## Doing it by hand

The command hides nothing. The equivalent is:

```sh
herdr plugin link  <repo>/plugin
herdr plugin list                  # confirm it registered
herdr plugin unlink luna.chat      # undo
```

Installed and linked plugins are global to the user and available in every herdr session,
so this is done once rather than per project.

## What the manifest declares

```toml
[[panes]]
id        = "chat"
title     = "Luna"
command   = ["sh", "open-chat.sh"]
placement = "split"
```

A **pane entrypoint**, not an action. ADR-0027 rejected hosting Luna's daemon as a plugin
because plugin invocations are per-call subprocesses with no restart and capped output — a
resident process would hold a slot forever, mute. A pane is the opposite shape: a terminal
a person interacts with, which is what the mechanism is for (ADR-0044).

`command` is argv, and that is the reason a manifest exists at all. `pane.split` over the
socket takes only `cwd` — no argv — so without this the proxy would have to open a pane
and type `luna chat` into a shell that takes seconds to appear, reintroducing the startup
race ADR-0036 already documents twice.

## Related

- [ADR-0043](../ADRs/0043-luna-chat-is-the-layer-and-the-pane-is-a-proxy.md) — the layer is
  `luna chat`; the pane is a proxy to it
- [ADR-0044](../ADRs/0044-the-proxy-is-a-plugin-pane-and-the-interpreter-is-a-harness.md) —
  why a plugin, and who interprets
- [ADR-0027](../ADRs/0027-luna-runs-under-herdr-as-a-socket-client.md) — why Luna's daemon
  is *not* a plugin
