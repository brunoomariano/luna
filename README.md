# Luna

**AI agents do not follow a deterministic process just because you asked for one in
prose.**

You write a workflow — investigate, plan, test, implement, review, deliver — and the
agent follows it *most of the time*. It skips a step when it decides it is not needed.
It reinterprets an instruction. It stops iterating for no reason. In a long session, it
forgets which role it had. Each of these failures is cheap on its own and expensive
together: you stop trusting the result and go back to reviewing everything by hand.

Luna solves this by moving **flow control** out of the model.

> *"When using agents, ensure that everything that can be deterministic, is done with
> a deterministic tool. Don't try to get the poor agents to follow a deterministic
> process."*
> — Robert C. Martin

A state machine decides which stage comes now, which role runs it, and validates the
result **by running the actual tool** — the test runs, the commit exists, the file is
there. The model does the work inside each stage, where judgement is what matters. It
never decides the next step.

## What this changes in practice

- **A stage does not close without delivering what it declared.** If `scenarios`
  promises to produce scenarios and an approach, and comes back with only the
  scenarios, the stage does not close and nothing is passed on. The hole shows up where
  it was born.
- **A stage does not start without receiving what it requires.** The contract is checked
  before any agent is called — including statically, before anything runs.
- **Every stage starts with a clean context.** No role erosion over a long session. What
  needs to cross over crosses through the handoff.
- **Failure is never silent.** A retry, or a block with a notice — the task does not die
  without you knowing.
- **You choose how much autonomy to grant**, per task: from the whole flow with human
  approval to an overnight run with no interruption.

## State

**Engine under construction.** The architecture is settled and recorded in
[`docs/`](docs/). The core started with the check that runs before any agent is called:
the contract's static audit, which detects a flow broken on paper.

## Installation

There is no release yet. When there is, it will be one command.

## Documentation

[`docs/README.md`](docs/README.md) is the documentation contract: it states which layers
exist, what each one answers and where it lives. Start there if you are going to write a
document. The entry points:

| Document | Subject |
|---|---|
| [`docs/architecture/overview.md`](docs/architecture/overview.md) | how the system works and why |
| [`docs/architecture/stages.md`](docs/architecture/stages.md) | the default stages and each one's contract |
| [`docs/ADRs/`](docs/ADRs/) | decisions taken, with the rejected alternative |
| [`docs/invariants/`](docs/invariants/) | rules that always hold |
| [`docs/glossary/`](docs/glossary/) | the domain terms |
| [`docs/references.md`](docs/references.md) | where the ideas came from |
| [`AGENTS.md`](AGENTS.md) | for agents working in this repository |
| [`docs/CONTRIBUTING.md`](docs/CONTRIBUTING.md) | contribution flow, commits and tags |

## License

To be defined.
