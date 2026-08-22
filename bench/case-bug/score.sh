#!/usr/bin/env bash
# Scores a delivered tally.sh against the bug case, and nothing else.
#
# The bug is bash arithmetic reading a leading zero as octal, so `08` is not a
# digit sequence it will accept. What makes it worth a `fix` flow rather than a
# `chore` is that the script *keeps going*: it writes a diagnostic, drops the
# argument, prints a wrong total and exits 0. A caller cannot tell the answer is
# wrong from the exit code, which is why the reproduction is the specification.
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

# check compares stdout, and separately insists stderr stayed empty. Both halves
# matter here: the baseline already prints *a* number for `1 08 3` — it is the
# diagnostic beside it, and the wrongness of the number, that is the defect.
check() {
  local want=$1; shift
  local got err status
  total=$(( total + 1 ))
  err=$(mktemp)
  got=$("$script" "$@" 2>"$err")
  status=$?

  if [ "$got" = "$want" ] && [ ! -s "$err" ] && [ "$status" -eq 0 ]; then
    pass=$(( pass + 1 ))
  else
    echo "  fail: tally.sh $* → '$got' (exit $status, stderr $(wc -c <"$err") bytes), want '$want' clean" >&2
  fi
  rm -f "$err"
}

# The defect, and its neighbours.
check 12 1 08 3
check 10 010
check 9 09
check 8 08

# What must not regress.
check 6 1 2 3
check -5 -3 -2
check 5 5
check 0

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
