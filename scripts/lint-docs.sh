#!/usr/bin/env sh
# lint-docs — valida a FORMA da suíte de docs. O contrato está em docs/README.md;
# aqui ele vira checagem executável, para não depender de quem escreve lembrar.
#
# Sai 1 em qualquer violação [E]. `--strict` promove os avisos [W] a erro.
# Read-only: não corrige nada, só reporta.

set -eu

cd "$(dirname "$0")/.."

fail=0
strict=0
[ "${1:-}" = "--strict" ] && strict=1

err()  { echo "$1: ERRO: $2"; fail=1; }
warn() { echo "$1: aviso: $2"; [ "$strict" = 1 ] && fail=1 || true; }

# Exclui os diretórios que não são documentação escrita à mão.
docs_md() { find docs -name '*.md' | sort; }

# --- 5. o contrato existe -----------------------------------------------------
# Sem docs/README.md a suíte é um amontoado: é a peça que governa o resto.
[ -f docs/README.md ] || err "docs/README.md" "contrato da suíte ausente"

# --- 1/2/3. convenções transversais -------------------------------------------
for f in $(docs_md); do
  base=$(basename "$f")

  # 1. sem YAML front-matter — status vai em linha negrito, não em bloco ---
  if head -n1 "$f" | grep -qE '^---[[:space:]]*$'; then
    err "$f:1" "front-matter YAML proibido (use **Status:** em negrito)"
  fi

  # 2. kebab-case.md, com as exceções nomeadas do contrato
  case "$base" in
    README.md|_template.md|CHANGELOG.md|CONTRIBUTING.md) ;;
    *)
      echo "$base" | grep -qE '^[a-z0-9]+(-[a-z0-9]+)*\.md$' \
        || err "$f" "nome fora de kebab-case.md" ;;
  esac

  # 3. um único # Título por documento.
  # Conta só fora de bloco cercado: um `# comentário` dentro de ```toml não é
  # cabeçalho, e o template de status mostra `# PRD` como exemplo de sintaxe.
  h1=$(awk '/^```/{fence = !fence; next} !fence && /^# /{n++} END{print n+0}' "$f")
  [ "$h1" = "1" ] || warn "$f" "esperado exatamente um '# Título' (achou $h1)"

  # 4. Gherkin em bullets, nunca em bloco de código
  if grep -qE '^```[[:space:]]*gherkin' "$f"; then
    err "$f" "Gherkin em bloco de código; use bullets (- **Dado** ...)"
  fi
done

# --- 6/7/7b. datados: PRD e RFC -----------------------------------------------
for f in $(find docs/PRDs -name '*.md' 2>/dev/null | grep -v '_template' || true); do
  [ "$(basename "$f")" = "README.md" ] && continue
  grep -qE '^\*\*Status:\*\* (NÃO IMPLEMENTADO|IMPLEMENTADO|OBSOLETO)$' "$f" \
    || err "$f" "**Status:** ausente ou fora do enum de PRD"
  grep -qE '^\*\*Última revisão:\*\* [0-9]{4}-[0-9]{2}-[0-9]{2}$' "$f" \
    || err "$f" "**Última revisão:** ausente ou data inválida"
  echo "$(basename "$f")" | grep -qE '^[a-z0-9]+(-[a-z0-9]+)*-[0-9]{4}-' \
    || err "$f" "nome sem identificador <domínio>-NNNN- (ex.: fsm-0001-titulo.md)"
done

for f in $(find docs/RFCs -name '*.md' 2>/dev/null | grep -v '_template' || true); do
  [ "$(basename "$f")" = "README.md" ] && continue
  grep -qE '^\*\*Status:\*\* (RASCUNHO|EM ANDAMENTO|CONCLUÍDO|OBSOLETO)$' "$f" \
    || err "$f" "**Status:** ausente ou fora do enum de RFC"
  grep -qE '^\*\*Última revisão:\*\* [0-9]{4}-[0-9]{2}-[0-9]{2}$' "$f" \
    || err "$f" "**Última revisão:** ausente ou data inválida"
  echo "$(basename "$f")" | grep -qE '^rfc-[0-9]{4}-' \
    || err "$f" "nome sem identificador rfc-NNNN- (ex.: rfc-0001-titulo.md)"
done

# --- 8/10. ADR: numeração contígua e status no enum ---------------------------
if [ -d docs/ADRs ]; then
  expected=1
  for f in $(find docs/ADRs -maxdepth 1 -name '[0-9][0-9][0-9][0-9]-*.md' | sort); do
    n=$(basename "$f" | cut -c1-4)
    [ "$n" = "0000" ] && continue   # o template não conta como decisão
    if [ "$n" != "$(printf '%04d' "$expected")" ]; then
      err "$f" "numeração com buraco: esperado $(printf '%04d' "$expected")"
    fi
    expected=$((expected + 1))

    grep -qE '^\*\*Status:\*\* (Proposto|Aceito|Rejeitado|Substituído por \[ADR-[0-9]{4}\])' "$f" \
      || err "$f" "**Status:** ausente ou fora do enum de ADR"
    grep -qE '^\*\*Data:\*\* [0-9]{4}-[0-9]{2}-[0-9]{2}$' "$f" \
      || err "$f" "**Data:** ausente ou data inválida"
  done
fi

# --- 9. imutabilidade do ADR aceito -------------------------------------------
# Um ADR 'Aceito' não muda de corpo. A única edição permitida depois do commit
# que o aceitou é a linha de **Status:** (para 'Substituído por ...').
# É a checagem que só o git dá — revisão humana quase sempre perde.
if [ -d docs/ADRs ] && git rev-parse --git-dir >/dev/null 2>&1; then
  for f in $(find docs/ADRs -maxdepth 1 -name '[0-9][0-9][0-9][0-9]-*.md' | sort); do
    [ "$(basename "$f" | cut -c1-4)" = "0000" ] && continue
    grep -qE '^\*\*Status:\*\* Aceito$' "$f" || continue

    # commit que introduziu o status 'Aceito' neste arquivo
    accepted=$(git log --format=%H -S'**Status:** Aceito' --  "$f" 2>/dev/null | tail -n1 || true)
    [ -n "$accepted" ] || continue

    # linhas de conteúdo alteradas desde então, ignorando a própria linha de Status
    changed=$(git diff "$accepted" -- "$f" 2>/dev/null \
      | grep -E '^[+-]' \
      | grep -vE '^(\+\+\+|---)' \
      | grep -vE '^[+-]\*\*Status:\*\*' \
      | wc -l | tr -d ' ')

    [ "${changed:-0}" = "0" ] \
      || err "$f" "ADR aceito teve o corpo alterado ($changed linhas) — ADR é imutável; crie um novo e marque este como Substituído"
  done
fi

# --- 11. links relativos não quebrados ----------------------------------------
# Roda num subshell com pipe, então acumula falha em arquivo temporário.
broken=$(mktemp)
for f in $(docs_md); do
  dir=$(dirname "$f")
  grep -oE '\]\([^)#]+\.md(#[^)]*)?\)' "$f" 2>/dev/null \
    | sed -E 's/^\]\(//; s/\)$//; s/#.*$//' \
    | while read -r link; do
        case "$link" in
          http*|/*|"") continue ;;
          # Placeholders de template e exemplos de sintaxe não são links reais:
          # `NNNN-title.md`, `<domínio>-NNNN-title.md`, `0007-new-title.md`.
          *NNNN*|*'<'*|*-title.md) continue ;;
        esac
        [ -e "$dir/$link" ] || echo "$f: ERRO: link quebrado: $link" >> "$broken"
      done
done
if [ -s "$broken" ]; then
  cat "$broken"
  fail=1
fi
rm -f "$broken"

# --- âncoras da raiz ----------------------------------------------------------
# CLAUDE.md é ponteiro, não cópia (ver docs/README.md).
if [ -f CLAUDE.md ]; then
  grep -qE '^@AGENTS\.md$' CLAUDE.md \
    || err "CLAUDE.md" "deve conter a diretiva @AGENTS.md (é ponteiro, não cópia)"
fi

[ "$fail" = "0" ] && echo "lint-docs: ok"
exit $fail
