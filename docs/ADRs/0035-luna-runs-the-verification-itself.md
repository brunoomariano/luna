# ADR-0035: Luna runs the verification itself

**Status:** Accepted
**Date:** 2026-08-10

## Context

[ADR-0028](0028-herdr-status-triggers-verification-it-never-closes-a-stage.md) settled that a
stage closes on a real check, and [ADR-0032](0032-the-contract-declares-how-each-artifact-is-verified.md)
settled that the contract declares what that check is. Neither said who executes it.

With Luna running under herdr (ADR-0027), the obvious answer was "herdr, like everything
else". The node layer's `Runner` interface was drafted that way, with a `Verify` method
alongside `OpenWorktree`, `StartAgent` and `Prompt`.

Running a command through herdr means sending keystrokes to a pane and reading the screen
back. There is no exit code on that path — it has to be printed and then scraped out of
terminal output. herdr's own documentation concedes the problem: when output must be
reliable, its recommended workaround is to have the agent write to a file and reply with
the path.

INV-core-4 says delivery is verified by running the real tool, and that checking format
validates the appearance of a delivery rather than the delivery. An exit code recovered by
parsing a screen is exactly that appearance.

## Decision

**Luna executes verification commands itself, with `os/exec`, in the worktree herdr
created.**

```
herdr:  worktree.create → /path/to/worktree
        hosts the agent in a pane

Luna:   exec.CommandContext(ctx, "sh", "-c", command)
        cmd.Dir = workspace.Path
        → a real exit code, real output, a real timeout
```

`Verify` therefore leaves the `Runner` interface. It was never herdr's operation; putting
it there conflated "where the agent lives" with "how the work is proven". The three
remaining `Runner` methods are all genuinely about hosting an agent.

The division this produces is sharper than the one before it:

| herdr | Luna |
|---|---|
| creates the worktree | runs the check inside it |
| hosts the agent | reads the exit code |
| reports that it stopped | decides what that proves |

## Alternatives considered

- **Running the check through a herdr pane** — rejected on evidence quality. The exit code
  becomes a parsing problem, the output competes with whatever else is on screen, and the
  timeout belongs to herdr rather than to the check. It buys one thing: the verification
  is visible in the TUI. That is worth something, and it is not worth evidence that could
  be wrong.
- **Asking the agent to run the check and report** — rejected. It is hermes-agent's
  `kanban_complete(artifacts)`, where a model's claim becomes a downstream premise. The
  whole point of ADR-0028 is that the verdict does not come from the thing being verified.
- **A verification pane, separate from the agent's** — rejected as the worst of both: the
  scraping problem remains, plus a second pane to manage per task.

## Consequences

- **Positive:** evidence comes from a real exit code, which is what INV-core-4 requires.
  Luna owns the timeout, so a hung `go test` is bounded by the same layer that bounds
  everything else — closing the gap the study found in herdr, which bounded its agent
  calls and forgot its own `git` subprocesses. And the verifier stops depending on herdr
  at all, so leaving herdr gets cheaper rather than harder.
- **Negative / costs:** the verification does not appear in the TUI. Someone watching
  herdr sees the agent work and then sees the stage close, with the check invisible
  between them. `luna task show` reports it — the command, the exit code, the scope — but
  that is a different surface, and a person watching the panes will not see it happen.
- **Impacts:**
  - `Runner` loses `Verify` and keeps three methods, all about hosting an agent;
  - the verifier needs the worktree path, which `worktree.create` already returns;
  - every spawned command gets a deadline, because an unbounded external process is the
    failure the study found in herdr's git calls;
  - `sh -c` means the configured command is a shell line, not an argv — deliberate, since
    contracts will want pipes and `&&`, and the command comes from the project's own
    config rather than from a model.

## References

- Related documents: [ADR-0024](0024-the-reducer-is-pure-verification-runs-outside.md),
  [ADR-0028](0028-herdr-status-triggers-verification-it-never-closes-a-stage.md),
  [ADR-0032](0032-the-contract-declares-how-each-artifact-is-verified.md),
  [ADR-0027](0027-luna-runs-under-herdr-as-a-socket-client.md),
  [invariants](../invariants/core.md)
