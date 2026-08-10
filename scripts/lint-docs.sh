#!/usr/bin/env sh
# lint-docs — validates the SHAPE of the docs suite. The contract lives in
# docs/README.md; here it becomes an executable check, so it does not depend on
# whoever is writing remembering it.
#
# Exits 1 on any [E] violation. `--strict` promotes [W] warnings to errors.
# Read-only: it fixes nothing, it only reports.

set -eu

cd "$(dirname "$0")/.."

fail=0
strict=0
[ "${1:-}" = "--strict" ] && strict=1

err()  { echo "$1: ERROR: $2"; fail=1; }
warn() { echo "$1: warning: $2"; [ "$strict" = 1 ] && fail=1 || true; }

# The documents this suite is accountable for: the ones the repository tracks.
#
# Ignored paths are excluded by construction rather than by a list here. Working
# material — cloned third-party repositories, scratch notes — lives under docs/
# without being part of the suite, and linting someone else's markdown for our
# conventions reports violations we neither own nor can fix.
#
# The fallback keeps the check working outside a git checkout (a release tarball,
# a container build), where nothing is ignored and everything present is ours.
# `--cached --others --exclude-standard` is the combination that matters: tracked
# files plus new ones not yet committed, minus anything ignored. A document must
# be linted before it is committed, not after — checking only tracked files would
# let every new document through on the run that mattered.
docs_md() {
  if git rev-parse --git-dir >/dev/null 2>&1; then
    git ls-files --cached --others --exclude-standard -- 'docs/*.md' 'docs/**/*.md' \
      | sort -u
  else
    find docs -name '*.md' | sort
  fi
}

# --- 5. the contract exists ---------------------------------------------------
# Without docs/README.md the suite is a pile: it is the piece governing the rest.
[ -f docs/README.md ] || err "docs/README.md" "suite contract missing"

# --- 1/2/3. cross-cutting conventions -----------------------------------------
for f in $(docs_md); do
  base=$(basename "$f")

  # 1. no YAML front matter — status goes in a bold line, not in a --- block
  if head -n1 "$f" | grep -qE '^---[[:space:]]*$'; then
    err "$f:1" "YAML front matter is forbidden (use a bold **Status:** line)"
  fi

  # 2. kebab-case.md, with the named exceptions from the contract
  case "$base" in
    README.md|_template.md|CHANGELOG.md|CONTRIBUTING.md) ;;
    *)
      echo "$base" | grep -qE '^[a-z0-9]+(-[a-z0-9]+)*\.md$' \
        || err "$f" "file name is not kebab-case.md" ;;
  esac

  # 3. a single # Title per document.
  # Counted outside fenced blocks only: a `# comment` inside ```toml is not a
  # heading, and the status template shows `# PRD` as a syntax example.
  h1=$(awk '/^```/{fence = !fence; next} !fence && /^# /{n++} END{print n+0}' "$f")
  [ "$h1" = "1" ] || warn "$f" "expected exactly one '# Title' (found $h1)"

  # 4. Gherkin in bullets, never in a code block
  if grep -qE '^```[[:space:]]*gherkin' "$f"; then
    err "$f" "Gherkin in a code block; use bullets (- **Given** ...)"
  fi
done

# --- 6/7/7b. dated layers: PRD and RFC ----------------------------------------
for f in $(find docs/PRDs -name '*.md' 2>/dev/null | grep -v '_template' || true); do
  [ "$(basename "$f")" = "README.md" ] && continue
  grep -qE '^\*\*Status:\*\* (NOT IMPLEMENTED|IMPLEMENTED|OBSOLETE)$' "$f" \
    || err "$f" "**Status:** missing or outside the PRD enum"
  grep -qE '^\*\*Last reviewed:\*\* [0-9]{4}-[0-9]{2}-[0-9]{2}$' "$f" \
    || err "$f" "**Last reviewed:** missing or invalid date"
  echo "$(basename "$f")" | grep -qE '^[a-z0-9]+(-[a-z0-9]+)*-[0-9]{4}-' \
    || err "$f" "name lacks the <domain>-NNNN- identifier (e.g. fsm-0001-title.md)"
done

for f in $(find docs/RFCs -name '*.md' 2>/dev/null | grep -v '_template' || true); do
  [ "$(basename "$f")" = "README.md" ] && continue
  grep -qE '^\*\*Status:\*\* (DRAFT|IN PROGRESS|DONE|OBSOLETE)$' "$f" \
    || err "$f" "**Status:** missing or outside the RFC enum"
  grep -qE '^\*\*Last reviewed:\*\* [0-9]{4}-[0-9]{2}-[0-9]{2}$' "$f" \
    || err "$f" "**Last reviewed:** missing or invalid date"
  echo "$(basename "$f")" | grep -qE '^rfc-[0-9]{4}-' \
    || err "$f" "name lacks the rfc-NNNN- identifier (e.g. rfc-0001-title.md)"
done

# --- 8/10. ADR: contiguous numbering and status within the enum ---------------
if [ -d docs/ADRs ]; then
  expected=1
  for f in $(find docs/ADRs -maxdepth 1 -name '[0-9][0-9][0-9][0-9]-*.md' | sort); do
    n=$(basename "$f" | cut -c1-4)
    [ "$n" = "0000" ] && continue   # the template does not count as a decision
    if [ "$n" != "$(printf '%04d' "$expected")" ]; then
      err "$f" "numbering gap: expected $(printf '%04d' "$expected")"
    fi
    expected=$((expected + 1))

    grep -qE '^\*\*Status:\*\* (Proposed|Accepted|Rejected|Superseded by \[ADR-[0-9]{4}\])' "$f" \
      || err "$f" "**Status:** missing or outside the ADR enum"
    grep -qE '^\*\*Date:\*\* [0-9]{4}-[0-9]{2}-[0-9]{2}$' "$f" \
      || err "$f" "**Date:** missing or invalid date"
  done
fi

# --- 9. immutability of an accepted ADR ---------------------------------------
# An 'Accepted' ADR does not change body. The only edit allowed after the commit
# that accepted it is the **Status:** line (to 'Superseded by ...').
# This is the check only git can give, and the one human review almost always misses.
if [ -d docs/ADRs ] && git rev-parse --git-dir >/dev/null 2>&1; then
  for f in $(find docs/ADRs -maxdepth 1 -name '[0-9][0-9][0-9][0-9]-*.md' | sort); do
    [ "$(basename "$f" | cut -c1-4)" = "0000" ] && continue
    grep -qE '^\*\*Status:\*\* Accepted$' "$f" || continue

    # the commit that introduced the 'Accepted' status in this file
    accepted=$(git log --format=%H -S'**Status:** Accepted' --  "$f" 2>/dev/null | tail -n1 || true)
    [ -n "$accepted" ] || continue

    # content lines changed since then, ignoring the Status line itself
    changed=$(git diff "$accepted" -- "$f" 2>/dev/null \
      | grep -E '^[+-]' \
      | grep -vE '^(\+\+\+|---)' \
      | grep -vE '^[+-]\*\*Status:\*\*' \
      | wc -l | tr -d ' ')

    [ "${changed:-0}" = "0" ] \
      || err "$f" "an accepted ADR had its body changed ($changed lines) — an ADR is immutable; create a new one and mark this as Superseded"
  done
fi

# --- 11. relative links that do not break -------------------------------------
# Runs in a subshell through a pipe, so failures accumulate in a temp file.
broken=$(mktemp)
for f in $(docs_md); do
  dir=$(dirname "$f")
  grep -oE '\]\([^)#]+\.md(#[^)]*)?\)' "$f" 2>/dev/null \
    | sed -E 's/^\]\(//; s/\)$//; s/#.*$//' \
    | while read -r link; do
        case "$link" in
          http*|/*|"") continue ;;
          # Template placeholders and syntax examples are not real links:
          # `NNNN-title.md`, `<domain>-NNNN-title.md`, `0007-new-title.md`.
          *NNNN*|*'<'*|*-title.md) continue ;;
        esac
        [ -e "$dir/$link" ] || echo "$f: ERROR: broken link: $link" >> "$broken"
      done
done
if [ -s "$broken" ]; then
  cat "$broken"
  fail=1
fi
rm -f "$broken"

# --- root anchors -------------------------------------------------------------
# CLAUDE.md is a pointer, not a copy (see docs/README.md).
if [ -f CLAUDE.md ]; then
  grep -qE '^@AGENTS\.md$' CLAUDE.md \
    || err "CLAUDE.md" "must contain the @AGENTS.md directive (it is a pointer, not a copy)"
fi

[ "$fail" = "0" ] && echo "lint-docs: ok"
exit $fail
