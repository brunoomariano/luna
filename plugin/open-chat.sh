#!/usr/bin/env sh
# Opens `luna chat` in a herdr pane.
#
# It exists because a manifest's `command` is argv, and argv is resolved against
# herdr's PATH rather than the shell a person configured. A developer running
# from a checkout has `bin/luna` and no installed `luna`, and the failure is
# "No viable candidates found in PATH" — which reads as a broken plugin rather
# than a missing install.
#
# So: prefer whatever is installed, fall back to the checkout this plugin was
# linked from, and say something useful when there is neither.

set -eu

# HERDR_PLUGIN_ROOT is this directory; the repository is its parent.
repo="${HERDR_PLUGIN_ROOT:-$(dirname "$0")}/.."

if command -v luna >/dev/null 2>&1; then
	exec luna chat
fi

if [ -x "$repo/bin/luna" ]; then
	exec "$repo/bin/luna" chat
fi

echo "luna is not installed, and $repo/bin/luna does not exist."
echo "Build it with \`make build\`, or install luna on your PATH."

# Hold the pane open so the message is readable — a pane that closes instantly
# leaves someone with a flash and no explanation.
read -r _
