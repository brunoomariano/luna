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

all_variants="solo luna:chore luna:fix luna:full"
variants=${*:-$all_variants}

if [ ! -x "$luna" ]; then
  echo "build it first: make build" >&2
  exit 1
fi

# The statement is one file so that every variant is given exactly the same words.
# A benchmark whose variants are briefed differently measures the briefing.
mapfile -t statement < "$here/case/statement.txt"

# seed lays down a fresh git repository holding the case, so no variant inherits
# another's work.
seed() {
  local dir=$1
  mkdir -p "$dir"
  cp "$here/case/seed/tally.sh" "$here/case/seed/Makefile" "$dir/"
  chmod +x "$dir/tally.sh"
  git -C "$dir" init -q
  git -C "$dir" config user.email bench@luna
  git -C "$dir" config user.name bench
  git -C "$dir" add -A
  git -C "$dir" commit -qm "the case, before anything touched it"
}

# score reports "<passed> <total>" for whatever is in the directory now.
score() {
  bash "$here/case/score.sh" "$1" 2>/dev/null | tail -1
}

run_luna() {
  local dir=$1 flow=$2 id
  id="BENCH-${flow}"

  # Kept, never discarded. The first run of this script reported a table of
  # numbers for a night where no agent started at all, because every one of these
  # was going to /dev/null — and a benchmark that cannot say why it measured
  # nothing is worse than one that does not run.
  ( cd "$dir" && "$luna" task new "$id" --kind feature --flow "$flow" \
      "${statement[@]}" ) >>"$dir/run.log" 2>&1
  ( cd "$dir" && "$luna" run "$id" ) >>"$dir/run.log" 2>&1

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
      usd=$(printf '%s' "$out" | grep -o '"total_cost_usd":[0-9.]*' | cut -d: -f2)
      tokens=""
      status="n/a"
      ;;
    luna:*)
      out=$(run_luna "$dir" "${variant#luna:}")
      usd=$(printf '%s' "$out" | grep -o '"cost_usd":[0-9.]*' | head -1 | cut -d: -f2)
      tokens=$(printf '%s' "$out" | grep -o '"tokens":[0-9]*' | head -1 | cut -d: -f2)
      status=$(printf '%s' "$out" | grep -o '"status":"[a-z_]*"' | head -1 | cut -d'"' -f4)
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
echo "work kept in $work"

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
