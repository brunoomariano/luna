#!/usr/bin/env sh
# run-steps — runs a list of make targets, one banner per step, one summary at the end.
#
# Stops at the first failure, like a plain make chain would: a later step running
# on a tree the earlier one already rejected reports noise, not information.
#
# What this adds over chaining prerequisites is the summary. A bare make chain
# dies mid-output and leaves you to work out which step broke and which never got
# a turn; here the tail of the run always says so explicitly.
#
# Usage: run-steps.sh <label> <target>...
#   label   — name of the pipeline, shown in the summary header
#   target  — make targets to run in order
#
# Exits 1 when a step fails, so `make` still fails the build.

set -u

label=$1
shift

# Colour only when stdout is a terminal. Piped into a file or a CI log, the escape
# codes would be noise — and CI logs are read far more often than they are watched.
if [ -t 1 ] && [ "${NO_COLOR:-}" = "" ]; then
  blue=$(printf '\033[34m'); bold=$(printf '\033[1m')
  green=$(printf '\033[32m'); red=$(printf '\033[31m')
  dim=$(printf '\033[2m'); off=$(printf '\033[0m')
else
  blue=''; bold=''; green=''; red=''; dim=''; off=''
fi

results=$(mktemp)
failed=0
first_failure=''
started=$(date +%s)

for target in "$@"; do
  # Once something has failed, the rest is recorded as skipped rather than run:
  # a linter answering about a tree the formatter already rejected reports noise.
  if [ "$failed" -ne 0 ]; then
    echo "$target skip 0" >> "$results"
    continue
  fi

  printf '%s%s▸ %s%s\n' "$blue" "$bold" "$target" "$off"

  step_start=$(date +%s)
  # --no-print-directory: the "Entering directory" pair around every sub-make
  # would double the line count of a clean run and bury the actual output.
  if make --no-print-directory "$target"; then
    status=ok
  else
    status=fail
    failed=$((failed + 1))
    first_failure=$target
  fi
  elapsed=$(( $(date +%s) - step_start ))

  echo "$target $status $elapsed" >> "$results"

  if [ "$status" = ok ]; then
    printf '%s✓ %s%s %s(%ss)%s\n\n' "$green" "$target" "$off" "$dim" "$elapsed" "$off"
  else
    printf '%s✗ %s%s %s(%ss)%s\n\n' "$red" "$target" "$off" "$dim" "$elapsed" "$off"
  fi
done

total=$(( $(date +%s) - started ))

# ── summary ──────────────────────────────────────────────────────────────────
printf '%s%s%s — %s steps, %ss%s\n' "$bold" "$label" "$off" "$#" "$total" "$off"
printf '%s%s%s\n' "$dim" "──────────────────────────────────" "$off"

skipped=0
while read -r target status elapsed; do
  case "$status" in
    ok)
      printf '  %s✓%s %-14s %s%ss%s\n' "$green" "$off" "$target" "$dim" "$elapsed" "$off" ;;
    fail)
      printf '  %s✗%s %-14s %s%ss%s\n' "$red" "$off" "$target" "$dim" "$elapsed" "$off" ;;
    skip)
      skipped=$((skipped + 1))
      printf '  %s· %-14s not run%s\n' "$dim" "$target" "$off" ;;
  esac
done < "$results"

rm -f "$results"

if [ "$failed" -eq 0 ]; then
  printf '%s%sall clear%s\n' "$green" "$bold" "$off"
  exit 0
fi

# Naming what never got a turn matters: without it, a step that was skipped looks
# identical to one that passed silently, and the next run is a guess.
if [ "$skipped" -gt 0 ]; then
  printf '%s%sfailed at %s%s%s — %s step(s) not run%s\n' \
    "$red" "$bold" "$off$red" "$first_failure" "$bold" "$skipped" "$off"
else
  printf '%s%sfailed at %s%s\n' "$red" "$bold" "$first_failure" "$off"
fi

exit 1
