#!/usr/bin/env sh
# lint-docs — checks the shape of the docs. It cannot check truth: a statement
# that the code contradicts passes here and is still a bug. That part is on the
# person changing the code (docs/CONTRIBUTING.md).
#
# The previous version of this script enforced ten document layers, ADR numbering
# and ADR immutability — 170 lines that reported green while architecture/overview.md
# described a directory as empty that held twelve files. Structure was never the
# thing worth guarding.
#
# Exits 1 on any violation. Read-only: it fixes nothing.

set -eu

cd "$(dirname "$0")/.."

fail=0
err() { echo "$1: ERROR: $2"; fail=1; }

# Tracked and new files, minus anything ignored — a document must be linted
# before it is committed, not after. The fallback keeps this working outside a
# git checkout (a release tarball), where everything present is ours.
docs_md() {
  if git rev-parse --git-dir >/dev/null 2>&1; then
    git ls-files --cached --others --exclude-standard -- 'docs/*.md' 'docs/**/*.md' \
      | sort -u
  else
    find docs -name '*.md' | sort
  fi
}

# --- the four files exist -----------------------------------------------------
# Adding a fifth is a decision, not an accident; removing one of these silently
# is how a suite starts drifting.
for required in architecture.md invariants.md decisions.md lessons.md; do
  [ -f "docs/$required" ] || err "docs/$required" "required document missing"
done

# --- and no fifth ------------------------------------------------------------
# The rule in AGENTS.md is "four files"; checking only that the four exist let a
# fifth ship unnoticed, which is how the suite grew the first time. CHANGELOG,
# CONTRIBUTING and DESIGN are conventional root-of-docs files, not a layer of the
# suite: none of them answers a question about how Luna works. DESIGN describes
# how the project's visual material is *drawn*, and it changes on a different
# clock from the code. Adding a name here is a decision, not a convenience.
for f in $(docs_md); do
  case "$f" in
    docs/*/*) continue ;;
  esac
  case "$(basename "$f")" in
    architecture.md|invariants.md|decisions.md|lessons.md) ;;
    CHANGELOG.md|CONTRIBUTING.md|DESIGN.md|README.md) ;;
    *) err "$f" "a fifth document: which of the four should have held this instead?" ;;
  esac
done

# --- cross-cutting conventions ------------------------------------------------
for f in $(docs_md); do
  base=$(basename "$f")

  # No YAML front matter: these are documents, not records with metadata.
  if head -n1 "$f" | grep -qE '^---[[:space:]]*$'; then
    err "$f:1" "YAML front matter is forbidden"
  fi

  # kebab-case.md, with the conventional capitalised exceptions.
  case "$base" in
    README.md|CHANGELOG.md|CONTRIBUTING.md|DESIGN.md) ;;
    *)
      echo "$base" | grep -qE '^[a-z0-9]+(-[a-z0-9]+)*\.md$' \
        || err "$f" "file name is not kebab-case.md" ;;
  esac

  # A single '# Title'. Counted outside fenced blocks: a `# comment` inside a
  # ```toml example is not a heading.
  h1=$(awk '/^```/{fence = !fence; next} !fence && /^# /{n++} END{print n+0}' "$f")
  [ "$h1" = "1" ] || err "$f" "expected exactly one '# Title' (found $h1)"
done

# --- relative links that do not break -----------------------------------------
# The loop body runs in a subshell through the pipe, so failures accumulate in a
# temp file rather than in a variable that would not survive it.
broken=$(mktemp)
for f in $(docs_md); do
  dir=$(dirname "$f")
  grep -oE '\]\([^)#]+\.md(#[^)]*)?\)' "$f" 2>/dev/null \
    | sed -E 's/^\]\(//; s/\)$//; s/#.*$//' \
    | while read -r link; do
        case "$link" in
          http*|/*|"") continue ;;
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
# CLAUDE.md is a pointer, not a copy: two files stating conventions drift apart.
if [ -f CLAUDE.md ]; then
  grep -qE '^@AGENTS\.md$' CLAUDE.md \
    || err "CLAUDE.md" "must contain the @AGENTS.md directive (it is a pointer, not a copy)"
fi

[ "$fail" = "0" ] && echo "lint-docs: ok"
exit $fail
