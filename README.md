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
the previous transport, delivering a real feature. The terminal driving is now gone and
both headless adapters have been exercised against their real binaries. A Codex run used
the central daemon, a managed ai-memory workstream, artifact handover and the native
transcript exposed by `luna console`; it reached the first contained stage, where the
current nested development sandbox could not start another `bwrap`. A full seven-stage
cycle has therefore not completed on the new transport yet. What is decided but
not built is listed at the bottom of [`docs/architecture.md`](docs/architecture.md) rather
than implied by silence.

**What it costs is now measured, and the numbers are the open question.** A feature whose
entire content is a `--loud` flag for a two-line shell script cost **$6.78 and 5.4 million
tokens across six stages**. The work was good — five tests including a shellcheck pass,
idempotent repeated flags, exit 2 with a diagnostic on an unknown argument — but a single
session would have done it for a fraction of that. The per-stage column is in
`luna task show`, and it says the expense is in the stages *before* the code: scenarios
and spec are $4.14 of the $6.78, build is $0.64.

That reading has since been acted on: the full flow is seven stages instead of twelve.
Solo execution uses one broad agent policy; pack execution gives each distinct stage
briefing an independent agent. Whether the shorter flow is cheaper is the next thing to
measure, not something to claim here. See
[`docs/lessons.md`](docs/lessons.md).

Task state is global to the installed Luna, not stored in a checkout. One daemon owns
`$XDG_DATA_HOME/luna/luna.db`; every CLI process opens that file in SQLite read-only mode
and sends events and artifact writes to the daemon over its private socket. Rows carry a
project identity, so equal task ids in different repositories remain separate while
`luna task list`, `luna gates`, `luna stuck`, `luna fleet report` and `luna flow check`
can report the whole workload.

Claude and Codex are measured harness adapters. Shipped flows declare Claude; passing
`--agent codex` to `luna lead`, `luna fleet run` or `luna work` changes the execution
harness without changing the flow fingerprint. `lead_harness = codex` selects Codex for
the conductor and autonomous gate judgements. Both record native sessions and token usage,
and `luna console` locates either transcript. Claude reports USD cost; Codex currently does
not, so Luna displays `cost n/a` and refuses a Codex run carrying a dollar ceiling.
Inside a stage, Luna exposes only artifact handover commands; task transitions remain the
node's responsibility even if a harness discovers a host-installed Luna orchestration
skill.

## Installation

```sh
go install github.com/brunoomariano/luna/src/cmd/luna@latest
```

Or from a checkout, which is the same build by the same route:

```sh
make install          # into GOBIN, else GOPATH/bin
make uninstall
```

`make install` prints what it installed, where, and — separately — what `luna`
resolves to for your shell. Those two are not always the same binary, and a
stale copy earlier on your PATH is invisible until it refuses a flag the current
build has.

```sh
luna version
```

says which build this is and which flows it carries, with their fingerprints.
Check it against whatever runbook or skill you are following: a document written
for one surface and run against another fails at the first unknown flag, with
nothing saying which of the two is behind.

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
