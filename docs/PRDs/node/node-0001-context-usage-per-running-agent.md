# PRD node-0001: Context usage per running agent

**Status:** NOT IMPLEMENTED
**Last reviewed:** 2026-08-13
**Source issue:** —
**RFC:** —

> Recorded so it is not lost, not to be built yet. Nothing here is decided: the open
> questions at the end are the point, and answering them is the work.

## Overview

Show how much context each running agent has consumed, so a person watching a fleet can
tell which sessions are close to their limit — and so Luna can eventually act on it before
the limit is reached.

## Problem

An agent runs inside a harness session with a finite context window. Today Luna knows
nothing about how full that window is. Two consequences follow.

**A person cannot see it.** With several tasks in flight there is no way to answer "which
of these is about to run out?" short of opening each pane and reading it.

**Luna cannot act on it.** A session that exhausts its context does not fail cleanly: it
degrades. The prior study named this failure mode — under prolonged compaction, agents lose
the identity of the role, and [INV-core-5](../../invariants/core.md) exists because of it.
Luna already attacks the cause by giving each stage a fresh context, but a single stage can
still be long enough to run out, and nothing notices.

The engine cannot ask this question itself: the reducer is pure, and token counts come from
outside ([ADR-0024](../../ADRs/0024-the-reducer-is-pure-verification-runs-outside.md)).
Whatever this becomes, it enters as observed fact through the node layer.

## Goal

Make context consumption visible per running agent, and make it possible to define a
threshold at which Luna does something about it rather than waiting for the degradation.

## Expected behavior

### Main flow

1. While an agent runs, its context usage is observable — some measure of how much of the
   window is spent.
2. `luna task show` and any fleet-level view report it for the agents currently running.
3. If a threshold is configured and crossed, Luna takes the declared action instead of
   letting the session degrade silently.

### Edge cases

- A harness that does not report usage: the answer is "unknown", said plainly, never
  guessed. The same posture as an evidence scope — a measurement that did not happen is not
  a measurement of zero.
- A session already over the threshold when Luna first looks.
- Usage that shrinks, because the harness compacted on its own.

### Error handling

- Losing the ability to measure must not stop the task. This is an observation, not a
  gate — unless a policy is explicitly configured to make it one.
- A threshold that fires must leave a trace in the log. A handoff that happened because a
  context filled up is exactly the kind of fact the audit exists to hold
  ([INV-core-2](../../invariants/core.md)).

## Requirements

### Functional

- RF1: report context usage per running agent, or say it is unknown.
- RF2: surface it where a person is already looking, rather than in a new place.
- RF3: allow a threshold to be configured, with a declared action when crossed.

### Non-functional

- RNF1: measuring must not depend on the engine — it is observed fact arriving through the
  node layer.
- RNF2: an unavailable measurement degrades to "unknown", never to a default number.

## System impacts

- **Affected services:** `internal/node` and the herdr boundary — whatever reports usage
  reports it there. Possibly `internal/herdr` if the multiplexer can answer it.
- **Data/persistence:** if a threshold action is taken, it is an event in the log. Whether
  usage *itself* is logged is an open question — a number sampled continuously is telemetry,
  not history, and the log is not a metrics store.
- **Observability:** this is the observability item; the question is which surface.

## Rollout plan

- Feature flag? probably not — reporting is additive, and the threshold is opt-in by being
  unset.
- Rollback strategy: the reporting is read-only; the threshold policy is configuration.

## Measured, 2026-08-13

Every number below came from running the tool, never from its documentation — the posture
[ADR-0036](../../ADRs/0036-herdr-facts-learned-from-a-running-server.md) exists for.

### Does herdr already know? — **No.**

- `agent.list` returns `agent_status`, `interactive_ready`, `state_change_seq`, `revision`,
  `cwd`. No token, no context, no window.
- `herdr api schema --json` (251 KB) has `tokens` and `context`, and neither is this:
  `tokens` is a **string map for templating** (`additionalProperties: {type: string}`),
  `context` is a view enum (`current_workspace_id`, `current_tab_id`).
- `~/.local/state/herdr/agent-detection/status.toml` tracks agent **versions**.

So this is a measurement Luna takes, not an observation it subscribes to.

### Can the harnesses answer it?

| harness | tokens | window | how it was read | authenticated |
|---|---|---|---|---|
| **claude** | **yes** | **yes** — `contextWindow: 1000000` | `--print --output-format=json` | yes |
| **codex** | yes | **no** | `exec --json --skip-git-repo-check` | yes (ChatGPT) |
| **opencode** | yes | **no** | `export <ses_id>` → `info.tokens` | yes |
| **pi** | **fields present, all zeros** | no | `--print --mode json` | yes (openai) |

**claude** is the only complete answer: `cache_read_input_tokens` +
`cache_creation_input_tokens` = 36,298 against `contextWindow: 1000000` → 3.6%. Both halves,
so a percentage is computable.

**codex** and **opencode** report consumption and no window. A token count without a window
is not comparable across harnesses, which is the asymmetry the unit question predicted.

**pi** is the trap worth recording: `message.usage = {input:0, output:0, totalTokens:0}` on
a run that **did answer**, with `pi auth check --provider openai` reporting `ready`. The
field exists, is well-shaped and is not populated. A reader that trusted the shape would
report 0% forever.

### The blocking constraint nobody had noticed

All four numbers above come from **non-interactive** invocations. Luna runs its agents
**interactively**, through herdr's `agent.start` + `agent.prompt` — there is no stdout to
parse, only a terminal pane.

claude writes per-message usage to `~/.claude/projects/<slug>/<session>.jsonl`, which would
carry it. But a complete 14-stage run left **no `.jsonl` at all**: the two directories that
exist for Luna's worktrees contain only `tool-results/`. Measured twice —
inside ai-jail `$HOME` is tmpfs so the file evaporates, and an interactive agent started
through herdr outside the jail did not write one either.

**So on the path Luna actually uses, none of the four harnesses can be read for usage
today.** That is the finding that reshapes this PRD: the question is no longer "which
harness reports it" but "how does Luna get at what claude already knows".

### The hook fires, and points at a file that is not there

A `Stop` hook was the most promising route and was measured end to end, because it fires
exactly when a Luna stage settles. Two results, and the second is the one that matters.

**Non-interactive, it works completely.** The hook payload carries no usage itself, but it
carries `transcript_path`, and reading that file gives the number:

```
hook payload → transcript_path → 38,577 tokens → 3.86% of a 1M window
```

**Interactive under herdr, the hook fires and the file does not exist.** Started through
`agent.start` with `--settings`, the same hook delivered a payload naming
`.../wt-luna-testbed-hookprobe/<session>.jsonl` — and that path was absent while the
worktree and the agent were both still alive. Then a `--print` run in the *same directory*
created the directory and wrote a `.jsonl` immediately.

Not disk (382 GB free), not `cleanupPeriodDays`, not the sandbox — this run was outside the
jail. The panes carry a truncated `⚠ Transcript saving i…` warning that is almost certainly
the explanation, and it could not be read at herdr's 24-column pane width.

A `PostToolUse` hook was then measured on the same path, to rule out the transcript merely
being flushed late: it fires mid-session, carries the same `transcript_path`, and that file
does not exist **while the session is still running** either.

**This is the concrete blocker, and it is narrow.** The hook is the right shape, fires on
the right path, and names the right file. What is missing is a transcript for interactive
sessions — the one thing Luna cannot supply itself.

Routes left, none tried:
- read the truncated `⚠ Transcript saving i…` warning at a usable pane width, which
  probably names the cause outright;
- a setting or flag that re-enables transcripts for a non-TTY interactive session;
- ask the hook for usage directly — it is claude's payload, and adding a token field there
  would make the whole question disappear. Upstream work, but the smallest fix.

## Open questions

Two are now answered above and struck through. The rest are why this is still a PRD.

- [x] ~~**Can the harnesses even answer it?**~~ **Answered:** claude fully, codex and
      opencode partially (tokens, no window), pi not at all despite having the fields. See
      the table above.
- [x] ~~**Or does herdr already know?**~~ **Answered: no.** Nothing in `agent.list` or the
      API schema carries usage.
- [ ] **How does Luna reach what claude knows?** This replaces the two above as the
      blocking question, and it is new. The usage exists and is unreachable on the
      interactive path. Options worth weighing, none obviously right:
      a **hook** (claude supports them, and Luna already writes none);
      a **`/context`-style prompt** whose answer Luna parses, which costs a turn and
      pollutes the session;
      **reading the pane**, which is scraping a UI and the project already rejected that
      class for status ([ADR-0028](../../ADRs/0028-herdr-status-triggers-verification-it-never-closes-a-stage.md));
      or **asking herdr to expose it**, which is upstream work.
- [ ] **Is a partial answer worth building?** claude works and is the default agent; codex
      and opencode give a number with no denominator. A feature that answers for one
      harness and says "unknown" for three may still be worth having — or may be the kind
      of half-measure that reads as broken.
- [ ] **What is the unit?** Tokens, percentage of window, messages? A percentage is
      comparable across harnesses and a token count is not — but a percentage needs a window
      size the harness may not report either.
- [ ] **What does crossing the threshold *do*?** The obvious answer is a handoff, and it is
      not obviously right. Luna's handoff carries pointers and a snapshot, never prose
      ([INV-core-6](../../invariants/core.md)), and a mid-stage handoff has no contract to
      satisfy — the stage has not produced what it owes. Options worth weighing: fail the
      stage and let retry give it a fresh context; open a gate; block and notify; or a
      mid-stage handoff that would need its own contract shape.
- [ ] **Does it belong in the log?** A sampled number is telemetry. The *decision* taken
      because of it is history. Those may be different records with different lifetimes.
- [ ] **Is this per agent or per stage?** [INV-core-5](../../invariants/core.md) gives every
      stage a fresh context, so a full window means one stage ran long — which may be worth
      knowing as a property of the stage rather than of the agent.

## References

- Issue: —
- Related ADRs: [ADR-0024](../../ADRs/0024-the-reducer-is-pure-verification-runs-outside.md),
  [ADR-0034](../../ADRs/0034-the-watchdog-delegates-detection-and-owns-the-verdict.md),
  [ADR-0042](../../ADRs/0042-four-harnesses-four-ways-to-deny-a-tool.md)
- Related invariants: [INV-core-5](../../invariants/core.md),
  [INV-core-6](../../invariants/core.md), [INV-core-8](../../invariants/core.md)
