# Autonomous build log — waves 7 and 6

**Status:** READY FOR REVIEW
**Started:** 2026-08-07
**Waves covered:** 7 (CLI) and 6 (lead)

This file records the decisions taken without asking, during a build the user
authorised to run unattended: *"if you need a decision, take the most recommended
option and note the trade-off"*.

It exists to be reviewed. Every entry states what was chosen, what was given up, and
what would have to change if the choice turns out wrong. Nothing here is settled — a
decision that survives review becomes an ADR; one that does not gets reverted while it
is still cheap.

Decisions the user took explicitly before the run started are listed at the end for
context, and are **not** up for review.

---

## Trade-offs taken autonomously

### 1. `GateConfirmWrite` split out from `GateConfirm`

**Chosen:** the commit gate has a kind of its own instead of being a plain confirm.

**Why:** the turbo profile is defined as "only the write waits" (ADR-0013). With one
confirm kind, `WaitsFor` had no way to tell the commit gate from the discovery gate, so
turbo would either stop at everything or at nothing.

**Given up:** one more value in the enum, and a distinction that only one profile uses.

**If wrong:** collapse the two and give `PendingGate` a boolean like `Irreversible`.
The reason the kind won is that it names *why* the gate is special — everything before
the commit is reversible — where a boolean would only say *that* it is.

### 2. An unknown profile is treated as `interactive`

**Chosen:** `WaitsFor` returns true for a profile it does not recognise.

**Why:** the failure modes are not symmetric. Guessing the permissive answer would let a
typo in configuration turn a supervised run into an unattended one; guessing the
cautious answer only stops a task that would have carried on.

**Given up:** a typo in a profile name fails quietly rather than loudly — the run gets
more gates than asked for, and nothing says why.

**If wrong:** make profile parsing reject an unknown name at the edge, and leave
`WaitsFor` exhaustive. That is probably the better shape once there is a CLI to parse
it; the default stays as the last line of defence.

### 3. A loop ceiling respects the profile

**Chosen:** `GateLoopCeiling` goes through `WaitsFor` like any other gate, so a nightly
run does not stop when a loop stops converging.

**Why:** consistency with what nightly means. A profile that stops at nothing but this
would surprise whoever chose it, and the surprise would arrive at 3am.

**Given up:** an unattended run can now spin past its ceiling with nobody watching.

**If wrong, and this is the entry most likely to be:** ADR-0023 says a spent ceiling is
"a decision to make with the history in view", which reads like something that should
always wait. The counter-argument is the watchdog (ADR-0019), which is what is supposed
to catch a nightly run going nowhere — but the watchdog does not exist yet, so right now
nothing does. Worth revisiting together with wave 6.

### 4. `$EDITOR` is split on whitespace

**Chosen:** `strings.Fields($EDITOR)`, first word is the program, the rest are
arguments.

**Why:** `EDITOR="code --wait"` and `EDITOR="emacsclient -nw"` are ordinary. Passing
the whole string as the program name looks for an executable literally called
`code --wait` and fails with a confusing error. A test caught this — `sed -i` as a
stand-in editor would not run.

**Given up:** an editor whose path contains a space breaks. That is rare enough, and
loud when it happens, that it beats breaking the common case quietly.

### 5. `errcheck` no longer flags `fmt.Fprint*`

**Chosen:** the print family is excluded from the unchecked-error rule.

**Why:** it is the one case where checking buys nothing — if stdout is broken,
reporting that fact to stdout will not work either. Twelve call sites were being
flagged, and wrapping each would have added noise without adding a single handled
failure.

**Given up:** a genuinely broken writer now fails silently. In a CLI that writes to a
terminal, that is not a case worth code.

### 6. The CLI validates the profile name, and the engine still guesses

**Chosen:** both. `parseProfile` rejects an unknown name at the edge; `WaitsFor` still
treats what it does not recognise as interactive.

**Why:** they answer different questions. The CLI knows the name was typed just now
and can say "did you mean nightly"; the engine may be replaying a log written by a
version that knew a profile this one does not, and there the cautious guess is the only
safe move.

**Given up:** the validity of a profile name is expressed twice, and the two lists can
drift.

**If wrong:** a single `ParseProfile` in the engine, with the CLI calling it. The
reason it is not that today is that the engine's fallback has to keep working for a
name that never passed through any parser.

### 7. Wave 7 shipped without exercising the review-artifact gate through the real flow

**Chosen:** the test seeds a log directly to the `spec` gate instead of driving eleven
transitions to reach it.

**Why:** the test is about the gate, and eleven setup steps would make it about the
flow.

**Given up:** nothing verifies that the real flow actually reaches a review-artifact
gate the way the seed assumes. The seed asserts the state it produced is the expected
one, which catches drift — but it is checking its own premise.

**If wrong:** one end-to-end test that walks the whole flow with a nightly profile. It
belongs with the lead (wave 6), where something exists to do the walking.

### 8. The lead reads "stage finished" from the evidence, not from a note it kept

**Chosen:** a stage counts as closed when every artifact it owed — flow products and audit
reports alike — has an entry in `state.Evidence`.

**Why:** a closed stage and a stage about to start are both `running`, so the lead needs
some way to tell them apart or it runs the same node forever. It does: the first version
kept a map of finished stages in memory, and that is exactly the kind of state INV-core-2
exists to forbid — a restarted lead would have lost it.

The context alone will not do the job. `qa` and `code-review` produce only an audit report,
which deliberately never enters the context (ADR-0021), so those stages would look
permanently unfinished. The evidence records both kinds, which makes it the only complete
trace.

**Given up:** a stage that owes nothing at all cannot be distinguished this way. There are
none in the shipped flow, and such a stage would have no work to verify — but a custom flow
could declare one, and its node would run twice.

**If wrong:** record stage entry as its own event, so "started" and "finished" are both
facts in the log rather than one being inferred. That is the more honest model and costs an
event per stage.

### 9. `Complete` carries the flow, and the codec supplies it at replay

**Chosen:** `Complete` gained a `Flow` field marked `json:"-"`, injected by the decoder the
same way `Advance` already worked.

**Why:** a test caught the bug. `complete` looked the stage up in `DefaultFlow()`, so a task
running a custom flow (ADR-0017) had its delivery checked against a contract it was not
running — the stage was simply not found, and the exit check passed on an empty contract.

The field is excluded from the payload deliberately: storing the flow would freeze a task to
the one it started under, which is the thing ADR-0017 exists to prevent.

**Given up:** two actions now carry a field that is stripped on the way to the log and
re-supplied on the way back. It works, but a third such field would be a sign the shape is
wrong.

### 10. The lead spends the whole retry budget to block

**Chosen:** when the judge says block, the lead records `Fail` repeatedly until the
reducer's budget is spent and the block happens through the normal path.

**Why:** one definition of what blocked means. The alternative — a separate "block now"
action — would give the reducer two ways to reach the same state, and the two would drift.

**Given up:** the log shows three failures where there was one decision. An audit reading it
sees a retry that never happened.

**If wrong, and this is the second most likely:** a `Block{Reason}` action the reducer
accepts directly. It would make the log honest at the cost of a second path into
`StatusBlocked`. Worth deciding deliberately rather than leaving as is.

---

## Resolved after review

### #2 and #6 — one profile list, and the surface says when it meets an unknown name

**Raised by the user:** "when does this replay happen, and why are we reading from the log?"

The answers changed the fix. The log is `.luna/luna.db`, a SQLite file in the project — it
is not published anywhere. Everything reads from it because **the state is not stored, it is
derived**: there is no row saying a task is at a gate, only the actions and the reducer.

An unknown profile therefore reaches the engine by exactly one realistic route: a log written
by a newer version, replayed by an older one. Failing the replay was rejected because
`luna gates` replays every task to find the suspended ones — one unreadable task would hide
all the others.

**Done:** `fsm.ParseProfile` is now the single list of valid names, and the CLI calls it. The
fallback in `WaitsFor` stays, because it is not a second list — it is what happens to a name
that never passed through any parser. `luna gates` and `luna task show` now flag a profile
this build does not know, so a nightly run stopping at every gate says why.

The user's objection to storing the whole policy in the log was right and that option was
dropped: the log keeps the name.

**Superseded by [ADR-0026](ADRs/0026-the-log-records-the-gate-decision-not-the-policy.md).**
The fix above answered "what happens to a name this build does not know" and left the harder
question standing: `ParseProfile` being the single list meant a project could not define a
profile at all, which contradicts what ADR-0017 promised. Making profiles configurable then
broke the assumption underneath the fix — replay re-derived each gate decision from the
profile, so editing one would rewrite how past tasks replay.

The log now records the decision (`Advance{GateDecision: "waited"}`) and keeps the name for
the audit. `ParseProfile` and `KnownProfile` are gone; validation moved to the CLI, which asks
the config. The warning survives with a changed meaning: it flags a profile *no longer
defined* rather than one this build never knew — the same signal, for the situation that can
actually happen now.

### #8 — the status says the stage finished

**Raised by the user:** "why not improve the record instead of reading a side effect? If it
finished, change the status."

Correct, and it exposes the real mistake. There were two situations — a node with work to do
and a node that had finished — collapsed into one `running` status, and inferring the
difference from what landed in the context was working around the symptom.

**Done:** `StatusStageDone`. `Complete` sets it, `Advance` accepts it, and `Advance` now
*refuses* a stage still running — a guard that was impossible before, because the state could
not tell the two apart. The inferred `closed()` helper is gone.

Two things this fixes beyond the loop: a custom flow with a stage that owes nothing no longer
runs its node twice, and evidence being optional some day would no longer break the
tie-break silently.

### #10 — a block is one event

**Raised by the user:** "I prefer an honest log, without the phantom retry."

**Done:** `Block{Reason}` is an action the reducer accepts directly. The lead records one
event where it used to record three `Fail`s to spend the retry budget.

The reason is required rather than defaulted: a task that halts without saying why is the
silent failure INV-core-8 forbids. And the retry counter is left untouched, because a block is
not an attempt.

The cost I named — a second path into `StatusBlocked` — is real but smaller than the log
lying. The wave-5 study points at what makes it disappear: multica's closed failure taxonomy,
where a reason that is never retried has no other way in.

### #3 — kept

**User's call:** "having a different ceiling per profile is good, keep it."


### #4 — the editor is now the project's choice first

**Raised by the user:** "$EDITOR is the user's default; the Luna system may want another."

Correct, and now implemented. Resolution order, most specific first:

1. `editor` in `.luna/config.toml` — the project's own choice
2. `$LUNA_EDITOR` — overrides the project for one invocation, without editing a versioned file
3. `$EDITOR`
4. `$VISUAL`

The reasoning: `$EDITOR` serves someone's git, not this project. A repository whose contracts
are long markdown is not obliged to open them in the editor that writes commit messages.

An unknown key in the config is an **error**, not a warning — a typo in `editor` would
otherwise leave the setting silently unapplied, and the person would conclude the feature does
not work rather than that they misspelled it.

The config parser is hand-rolled for one key rather than pulling in a TOML library, which
would be the project's second external dependency for a dozen lines of work. The comment says
so, and says the trade flips if the file ever grows sections or arrays.

**Where the editor is used:** exactly one place — `luna gate adjust`, which today only fires
on the `approve-spec` gate of the `spec` stage. It is the only gate that carries an artifact
for review (ADR-0022).

---

## Decisions the user took before the run

These framed everything above and are not under review:

| # | Decision | Rationale given |
|---|---|---|
| 1 | A `TaskCreated` event opens every log | `Replay` needed the kind from outside, but the kind is produced by `intake` and already lives in the log |
| 2 | The lead's judgement is an interface (`Judge`) | Keeps wave 6 testable without the network; the real implementation arrives with wave 5 |
| 3 | `gate adjust` opens `$EDITOR` | The idiom of `git commit` and `crontab -e` — nothing new to learn |
| 4 | The gate profile travels in the `Advance` action | It reaches the log, so a replay reproduces the run as it happened rather than as it would happen today |
| 5 | Wave 7 ships without `luna run` | Reading and answering gates works on the store alone; running belongs with the lead |
| 6 | Wave order inverted to 7 → 6 | The CLI delivers something demonstrable without the lead existing |

---

## What to look at first, if time is short

Three entries are the ones most likely to be wrong, in order:

1. **#3 — a loop ceiling respects the profile.** A nightly run can now spin past its ceiling
   with nobody watching, and the watchdog that was supposed to catch that does not exist yet.
2. **#10 — the lead spends the whole retry budget to block.** The log shows three failures
   where there was one decision.
3. **#8 — stage completion is inferred from the evidence.** It works, but "started" and
   "finished" being one inferred fact rather than two recorded ones is a design smell.

The rest are small or self-evidently right.

## What the wave-5 study says about these

The study of five orchestrators (`../luna-study/SYNTHESIS.md`) landed while these waves were
being built, and it bears on two entries:

- **#3** — multica runs a three-layer watchdog with a *semantic inactivity* timer
  (`codex.go:1089`), and hermes has a pure loop-detector whose `idempotent_no_progress` signal
  catches an agent that succeeds without progressing. Together they are the watchdog ADR-0019
  describes. Once that exists, letting a nightly run past its loop ceiling is defensible; until
  then it is a gap.
- **#10** — multica's failure taxonomy (`taskfailure/failure.go:56-186`) is a closed set of 22
  reasons with allowlisted retry. If Luna adopts something like it, `Block{Reason}` stops being
  a second path into `StatusBlocked` and becomes the natural one.
