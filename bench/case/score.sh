#!/usr/bin/env bash
# Scores a delivered tally.sh against the acceptance criteria, and nothing else.
#
# Objective on purpose: every case is an input and an exact expected line. A
# benchmark whose product score is a judgement measures the judge, and the whole
# reason this file exists is that Luna's own claim about a delivery must not be
# the thing Luna is graded on.
#
# Usage: score.sh <dir-holding-tally.sh>
set -uo pipefail

dir=${1:?usage: score.sh <dir>}
script="$dir/tally.sh"

if [ ! -x "$script" ]; then
  echo "0 0 tally.sh missing or not executable"
  exit 0
fi

pass=0
total=0

check() {
  local want=$1; shift
  local got
  total=$(( total + 1 ))
  got=$("$script" "$@" 2>/dev/null || echo "<error>")
  if [ "$got" = "$want" ]; then
    pass=$(( pass + 1 ))
  else
    echo "  fail: tally.sh $* → $got, want $want" >&2
  fi
}

# The behaviour that already existed and must not regress.
check 6 1 2 3
check 5 5
check 0

# The behaviour the task asks for.
check 2 --avg 1 2 3
check 1 --avg 1 2
check 0 --avg

# The trap: the flag is positional-independent, and a delivery that stores it in
# a variable read only before the loop gets this wrong while passing every case
# above. A real cycle failed exactly here and called the variable load-bearing.
check 1 1 --avg 2

# Lint, when it is available. Counted rather than skipped, because "shellcheck
# passes" is one of the stated criteria.
total=$(( total + 1 ))
if command -v shellcheck >/dev/null 2>&1; then
  if shellcheck "$script" >/dev/null 2>&1; then
    pass=$(( pass + 1 ))
  else
    echo "  fail: shellcheck" >&2
  fi
else
  echo "  skip: shellcheck not installed, counted as a failure" >&2
fi

echo "$pass $total"
