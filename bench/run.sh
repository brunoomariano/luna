#!/usr/bin/env bash
# Runs the benchmark case through several variants and prints what each cost.
#
# It exists because every claim about a flow change is otherwise an opinion. The
# useful comparison is not Luna against a bare agent — that answer is known and
# will not move — it is flow against flow, and this build against the last one.
# The solo row stays as the baseline so the multiple remains visible.
#
# This spends real money and needs a real agent, so it is never part of `make ci`.
#
# Usage: bench/run.sh [variant ...]
#        bench/run.sh                     every variant
#        bench/run.sh solo luna:fix       just those two
set -uo pipefail

here=$(cd -- "$(dirname -- "$0")" && pwd)
root=$(cd -- "$here/.." && pwd)
luna="$root/bin/luna"
work=${BENCH_WORK:-$(mktemp -d)}
results="$work/results.tsv"
mkdir -p "$work"

# Which case is being run. Each one is a directory holding a statement, a scorer
# and the flows it is a candidate for.
#
# Two ship. `case` is a feature — add a flag — and `case-bug` is a defect with a
# reproduction, where bash reads a leading zero as octal and the script prints a
# wrong total, writes a diagnostic and still exits 0.
#
# They share one seed script, which is the honest arrangement rather than a saving:
# the same baseline genuinely admits both tasks, and a benchmark whose cases start
# from different code cannot say whether a difference came from the flow or the
# starting point.
# What one variant may spend before it stops. There has to be a number here, and
# the first run without one is the argument: the `full` flow found a real contract
# violation at `review`, sent the work back exactly as designed, and went round
# again — build $10.93, refactor $8.37, verify $7.23, review $5.80, $35.07 and
# climbing on a case whose bare-agent baseline is $0.57. Nothing was broken. The
# send-back is the mechanism working, and a mechanism that works without a ceiling
# is how an unattended night bills like that.
#
# Generous rather than tight: the point is to stop a runaway, not to cut a flow off
# mid-thought and then report it as expensive.
per_task_budget=${BENCH_BUDGET_USD:-12}

bench_case=${BENCH_CASE:-case}
case_dir="$here/$bench_case"

if [ ! -d "$case_dir" ]; then
  echo "no such case: $bench_case" >&2
  exit 1
fi

# The flows this case is a candidate for, declared by the case and not by the
# runner. Comparing flows only means something among flows that could each take
# the same task, and the cost of getting that wrong is measured: run through the
# feature case, `fix` spent $2.88 across 48 turns in `diagnose` looking for the
# root cause of something that was not broken — 78% of its bill — and still closed
# clean at 8/8. The number was real and it was not what `fix` costs.
#
# Comparisons are within a case. A row from one case does not belong in a table
# with a row from another.
all_variants=$(tr '\n' ' ' < "$case_dir/variants")
variants=${*:-$all_variants}

# kindFor is the task kind each flow is built around.
#
# `luna task new --kind` and `--flow` are different axes — kind drives the
# conditional stages inside a flow, the flow decides its shape — but a benchmark
# that pinned every variant to `feature` was quietly running each flow against a
# task class it was not written for.
kindFor() {
  case "$1" in
    chore) echo chore ;;
    fix)   echo bug ;;
    *)     echo feature ;;
  esac
}

if [ ! -x "$luna" ]; then
  echo "build it first: make build" >&2
  exit 1
fi

# The statement is one file so that every variant is given exactly the same words.
# A benchmark whose variants are briefed differently measures the briefing.
mapfile -t statement < "$case_dir/statement.txt"

# jailConfig is the sandbox configuration each seed carries.
#
# The variants run in scratch repositories, and a scratch repository has no
# `.ai-jail` — so the sandbox falls back to defaults that, on this machine, cannot
# reach the harness at all. The first real run of this script measured nothing for
# exactly that reason: `ai-jail-mise: exec: claude: not found`, exit 127, every
# variant.
#
# `no_mise` is what fixes it here, and the direction is machine-specific rather
# than universal. The harness is installed by mise, the jail masks mise's own
# installs directory, and the jail's mise integration therefore resolves nothing;
# turning the integration off lets the binary be found on PATH instead. A machine
# where the integration is what makes the harness reachable wants the opposite,
# which is why this is one overridable variable and not a line in Luna.
#
# It is committed into the seed rather than left beside it, and that took a second
# run to get right: the agent works in a worktree Luna creates as a *sibling* of
# the seed, ai-jail reads the config from the working directory and does not search
# upwards, so a file that is not in the git content never reaches the agent. Which
# is the honest shape anyway — a real project running Luna keeps its `.ai-jail`
# committed at its root, and a seed without one was the artificial case.
jail_config=${BENCH_AI_JAIL_CONFIG:-'no_mise = true'}

# seed lays down a fresh git repository holding the case, so no variant inherits
# another's work.
seed() {
  local dir=$1
  mkdir -p "$dir"
  cp "$here/case/seed/tally.sh" "$here/case/seed/Makefile" "$dir/"  # one baseline, both cases
  chmod +x "$dir/tally.sh"
  printf '%s\n' "$jail_config" > "$dir/.ai-jail"
  git -C "$dir" init -q
  git -C "$dir" config user.email bench@luna
  git -C "$dir" config user.name bench
  git -C "$dir" add -A
  # Forced past the ignore rules, and that is the point rather than a workaround:
  # `.ai-jail` is conventionally untracked — this machine's global gitignore
  # excludes it — and the seed needs it *in the git content*, because the agent
  # works in a worktree branched from this commit and reads its sandbox config
  # from there.
  git -C "$dir" add -f .ai-jail
  git -C "$dir" commit -qm "the case, before anything touched it"
}

# field reads one value out of a JSON reply, by dotted path.
#
# A parser rather than a grep, and the difference cost a run to find: the first
# version matched `"cost_usd":[0-9.]*`, Go's encoder writes `"cost_usd": 0.42`
# with a space, and the pattern therefore matched the key and no digits. Every
# variant came back costing nothing, which the runner then reported as "no agent
# was billed" — a false alarm on a run that had worked perfectly.
field() {
  printf '%s' "$1" | python3 -c '
import json, sys
try:
    value = json.load(sys.stdin)
except Exception:
    sys.exit(0)
for key in sys.argv[1].split("."):
    if not isinstance(value, dict) or key not in value:
        sys.exit(0)
    value = value[key]
print(value)
' "$2" 2>/dev/null
}

# score reports "<passed> <total>" for whatever is in the directory now.
score() {
  bash "$case_dir/score.sh" "$1" 2>/dev/null | tail -1
}

run_luna() {
  local dir=$1 flow=$2 id
  id="BENCH-${flow}"

  # Kept, never discarded. The first run of this script reported a table of
  # numbers for a night where no agent started at all, because every one of these
  # was going to /dev/null — and a benchmark that cannot say why it measured
  # nothing is worse than one that does not run.
  # Autonomy, or a flow with a gate can never finish here. A task is created at 0,
  # which sends every gate with criteria to a person — correct as a default and
  # wrong for a benchmark, where there is nobody to ask. The `full` flow gates its
  # contract at criticality 9, so an unset knob stopped it there with $2.74 spent
  # and seven stages never reached. Gateless flows are unaffected: the knob bounds
  # who answers a gate, and they open none.
  {
    ( cd "$dir" && "$luna" task new "$id" --kind "$(kindFor "$flow")" --flow "$flow" \
        --budget-usd "$per_task_budget" "${statement[@]}" )
    ( cd "$dir" && "$luna" autonomy "$id" 9 "unattended benchmark" )
    ( cd "$dir" && "$luna" run "$id" )
  } >>"$dir/run.log" 2>&1

  # The status and the bill both come out of the log, through the structured view
  # rather than the printed one — a benchmark parsing a column written for a
  # person breaks on every rewording.
  ( cd "$dir" && "$luna" task show "$id" --json 2>>"$dir/run.log" )
}

run_solo() {
  local dir=$1
  # One agent, one prompt, no flow. The baseline the whole comparison is against.
  ( cd "$dir" && claude -p --output-format json \
      "$(printf '%s\n' "${statement[@]}")" ) 2>>"$dir/run.log"
}

printf 'variant\tproduct\tflow\tseconds\tusd\ttokens\n' > "$results"
ran_nothing=''

for variant in $variants; do
  # The colon in `luna:fix` cannot become a directory name: it ends up in the
  # worktree Luna derives from it, and a path component with a colon in it is a
  # problem somebody debugs at the wrong layer.
  dir="$work/${variant/:/-}"
  seed "$dir"

  start=$(date +%s)
  case "$variant" in
    solo)
      out=$(run_solo "$dir")
      usd=$(field "$out" total_cost_usd)
      tokens=""
      status="n/a"
      ;;
    luna:*)
      out=$(run_luna "$dir" "${variant#luna:}")
      usd=$(field "$out" spend.cost_usd)
      tokens=$(field "$out" spend.tokens)
      status=$(field "$out" operation)
      ;;
    *)
      echo "unknown variant: $variant" >&2
      continue
      ;;
  esac
  seconds=$(( $(date +%s) - start ))

  # Scored from the worktree the variant left behind. Luna delivers on a branch,
  # so the branch is checked out first when there is one.
  git -C "$dir" checkout -q "luna/BENCH-${variant#luna:}/done" 2>/dev/null || true
  read -r passed total <<< "$(score "$dir")"

  # A variant that cost nothing ran no agent, whatever else it printed. Said here
  # rather than left for a reader to infer from a zero, because the zero is what
  # a cheap success and a total failure have in common.
  if [ "${usd:-0}" = "0" ] || [ -z "${usd:-}" ]; then
    ran_nothing="$ran_nothing $variant"
    status="${status}/no-agent"
  fi

  printf '%s\t%s/%s\t%s\t%s\t%s\t%s\n' \
    "$variant" "${passed:-0}" "${total:-0}" "$status" "$seconds" "${usd:-0}" "${tokens:-}" \
    >> "$results"
done

echo
column -t -s "$(printf '\t')" "$results"
echo
echo "case $bench_case, work kept in $work"

# The exit code is the whole point of this block. A benchmark that measures
# nothing and reports success is the shape of failure this project refuses
# everywhere else: a well-formed answer describing something that did not happen.
if [ -n "$ran_nothing" ]; then
  echo
  echo "MEASURED NOTHING:$ran_nothing"
  echo "no agent was billed, so the scores above are the seed's and not a result."
  for variant in $ran_nothing; do
    log="$work/${variant/:/-}/run.log"
    [ -s "$log" ] || continue
    echo
    echo "--- $variant ---"
    tail -5 "$log"
  done
  exit 1
fi
