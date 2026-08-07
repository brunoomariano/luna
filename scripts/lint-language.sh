#!/usr/bin/env sh
# lint-language — catches Portuguese left in the project.
#
# The rule is in AGENTS.md: the whole project is written in English. This script
# makes that rule checkable instead of trusting whoever writes to remember it.
#
# It is NOT part of ci-check by default. Natural language detection is heuristic:
# a proper noun, a quoted example or a URL can trip it, and a linter that cries
# wolf is a linter people learn to ignore. Run it after a translation pass, or
# wire it into ci-check once the false-positive rate is known to be zero.
#
# Exits 1 on any hit. Read-only.

set -eu

cd "$(dirname "$0")/.."

fail=0
hit() { echo "$1"; fail=1; }

# Files the language rule does not govern: local tooling markers, the git history
# itself (commit messages already written stay as they are), and this script —
# which necessarily spells out the Portuguese it hunts for, and would otherwise
# report itself on every run.
# `--cached --others` rather than plain `ls-files`: a file that is new and not yet
# staged is exactly the one most likely to carry fresh Portuguese, and checking
# only tracked files would wave it through. `--exclude-standard` keeps .gitignore
# honoured so build output and local tooling stay out.
tracked() {
  git ls-files --cached --others --exclude-standard \
    | grep -vE '^(\.ai-jail|\.ai-memory\.toml|scripts/lint-language\.sh)$'
}

# --- layer 1: accented characters ---------------------------------------------
# The cheapest signal, and the one that catches most of it.
found=$(tracked | xargs grep -nE '[áàâãéêíóôõúçÁÀÂÃÉÊÍÓÔÕÚÇ]' 2>/dev/null || true)
[ -z "$found" ] || hit "$found"

# --- layer 2: unaccented Portuguese words -------------------------------------
# Accents alone are not enough: "para", "como", "mais", "cada", "quando" carry
# none and would pass clean through layer 1.
#
# `nos`, `nas`, `dos`, `das` are deliberately absent — they collide with English
# words and with acronyms, and the noise is not worth the catch.
PT_WORDS='nao|entao|porem|voce|atraves|qualquer|onde|quando|porque|antes|depois|deve|fazer|isso|aquilo|outro|tudo|nada|sempre|nunca|assim|pois|embora|desde|apos|durante|conforme|pelo|pela|essa|esse|aquele|aquela|muito|ainda|apenas|mesmo|algum|nenhum|talvez'
found=$(tracked | xargs grep -nwiE "($PT_WORDS)" 2>/dev/null \
  | grep -viE 'https?://|conventionalcommits|keepachangelog' || true)
[ -z "$found" ] || hit "$found"

# --- layer 3: Portuguese suffixes ---------------------------------------------
# Morphology catches words the fixed list above misses.
found=$(tracked | xargs grep -noiE '\b[a-z]{4,}(ção|ções|mente|ável|ível|agem|ência)\b' 2>/dev/null || true)
[ -z "$found" ] || hit "$found"

# --- layer 4: domain terms that should have been translated -------------------
# `handoff`, `gate` and `worktree` are deliberately NOT here: they are terms the
# project keeps untranslated on purpose.
#
# `etapa` and `stage` appear together in AGENTS.md and the CHANGELOG as the
# example explaining why mixed languages hurt search — those two are expected.
PT_DOMAIN='etapa|etapas|papel|papeis|fluxo|tarefa|tarefas|artefato|artefatos|lacuna|achado|teto|perfil|perfis|contrato|invariante|decisao|decisoes'
found=$(tracked | xargs grep -nwiE "($PT_DOMAIN)" 2>/dev/null \
  | grep -viE '"etapa"' || true)
[ -z "$found" ] || hit "$found"

[ "$fail" = "0" ] && echo "lint-language: ok"
exit $fail
