# The Luna proxy for herdr

A pane that runs `luna chat`, so the conversation lives beside the tasks it is
about instead of in a separate terminal.

It is a window, not a system. Everything the conversation does goes through
Luna's own CLI, so uninstalling this costs the convenience of a pane and nothing
else — `luna chat` keeps working anywhere.

## Install

```sh
herdr plugin link  <path-to-this-directory>
herdr plugin enable luna.chat
```

## Open

```sh
herdr plugin pane open --plugin luna.chat --entrypoint chat --placement split
```

`--placement` also takes `tab`, `zoomed` or `overlay`.

## Why a launcher instead of `command = ["luna", "chat"]`

argv is resolved against herdr's PATH, not the shell you configured. Working from
a checkout there is no installed `luna`, and herdr answers *"No viable candidates
found in PATH"* — which reads as a broken plugin rather than a missing install.

`open-chat.sh` prefers an installed `luna`, falls back to `bin/luna` in the
checkout this plugin was linked from, and says which is missing when there is
neither.
