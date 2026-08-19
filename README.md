![Luna](docs/assets/imgs/luna_banner.png)

# Luna

**AI agents do not follow a deterministic process just because you asked for one in
prose.**

You write a workflow — investigate, plan, test, implement, review, deliver — and the agent
follows it *most of the time*. It skips a step it decided was unnecessary. It reinterprets
an instruction. It stops iterating for no reason. In a long session it forgets which role
it had. Each failure is cheap alone and expensive together: you stop trusting the result
and go back to reviewing everything by hand.

Luna moves **flow control** out of the model, and then refuses to believe the model about
whether the work is done.

> *"Ensure that everything that can be deterministic, is done with a deterministic tool.
> Don't try to get the poor agents to follow a deterministic process."*
> — Robert C. Martin

## What it actually gives you

Three things, in order of how much they matter:

- **A stage closes because a command proved it, not because an agent said so.** `make ci`
  returned zero, the artifact is in the store with its hash, the commit resolves. The
  check runs over what was *delivered*, not over whatever was left in the working tree.
- **An audit trail you can replay.** Append-only log, one record per transition, refusing
  to replay against a flow it was not written under. For unattended runs, the evidence is
  the product.
- **Agents that stay in their box.** Every agent runs inside a sandbox; scratch artifacts
  are handed to Luna through a socket instead of being committed, so the delivered tree
  holds the work and nothing else.

On top of those: a stage never starts without what it requires, failure ends in a bounded
retry and then a *notified* block, and you choose how much autonomy to grant per task —
from confirming every gate to an overnight run that stops for nothing.

## State

**Under construction, and honest about it.** A full 12-stage cycle ran zero-touch under
the previous transport, delivering a real feature. The transport was then replaced — the
terminal driving is gone, agents run headless — and the stages have run individually
against the new one but a full cycle has not been repeated on it yet. What is decided but
not built is listed at the bottom of [`docs/architecture.md`](docs/architecture.md) rather
than implied by silence.

**What it costs is now measured, and the first numbers are not flattering.** A feature
whose entire content is a `--loud` flag for a two-line shell script spent **$3.24 and 2.7
million tokens across three stages** before any code was written — 47 turns in the stage
that writes test scenarios alone. The per-stage cost column is in `luna task show`, and
the case for the flow has to be made against numbers like those rather than against the
argument for the design. See [`docs/lessons.md`](docs/lessons.md).

## Installation

No release yet. When there is one, it will be one command.

## Documentation

Four files. If one disagrees with the code, the file is the bug.

| Document | Subject |
|---|---|
| [`docs/architecture.md`](docs/architecture.md) | how the system works today |
| [`docs/invariants.md`](docs/invariants.md) | the five rules that always hold |
| [`docs/decisions.md`](docs/decisions.md) | what was decided, and what was tried and abandoned |
| [`docs/lessons.md`](docs/lessons.md) | what building this taught, including the mistakes |
| [`AGENTS.md`](AGENTS.md) | for agents working in this repository |
| [`docs/CONTRIBUTING.md`](docs/CONTRIBUTING.md) | commits, tags, the pipeline |

## License

To be defined.
