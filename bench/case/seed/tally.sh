#!/usr/bin/env bash
# Sums the numbers given on the command line.
set -euo pipefail

total=0
for n in "$@"; do
  total=$(( total + n ))
done
echo "$total"
