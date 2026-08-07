# Contribuindo

Esta é a casa normativa do fluxo de **branch, merge, commit e tag**. O `README.md` e o
[`AGENTS.md`](../AGENTS.md) apontam para cá em vez de repetir — fonte única.

## Antes de abrir uma issue ou PR

Leia [`docs/ADRs/`](ADRs/). Cada decisão está registrada com a alternativa que foi
recusada e o motivo. Propostas que reabrem uma decisão sem argumento novo serão
fechadas com um ponteiro para o ADR correspondente.

Se a mudança toca uma regra permanente do domínio, confira também
[`docs/invariants/`](invariants/).

## Estado do projeto

Em desenho. A arquitetura está fechada; o código não começou. Contribuições mais úteis
agora são de **crítica ao desenho** — especialmente se você já operou uma frota de
agentes e viu um modo de falha que o desenho não cobre.

## Git-flow

- **Branch default:** `master`. Não há `develop` — o projeto é pequeno demais para
  justificar duas linhas de integração.
- **Toda mudança sai de uma branch a partir de `master`** e volta por PR. Nomeie pelo
  tipo e pelo assunto: `feat/stage-contract`, `fix/handoff-snapshot`,
  `docs/adr-numbering`.
- **PRs têm como alvo `master`.**
- **Tags de release são criadas na `master`**, em SemVer (`v0.1.0`). Enquanto não há
  release, não há tag.

## Fluxo

1. Abra uma issue descrevendo o problema antes de escrever código.
2. Trabalhe numa branch a partir de `master`.
3. Conventional Commits; o corpo explica o porquê.
4. `make ci` verde antes de abrir o PR.

## Commits

[Conventional Commits](https://www.conventionalcommits.org/pt-br/), em português.

- O **título** diz o que muda, no imperativo: `feat(fsm): valida produces na saída da etapa`.
- O **corpo explica o porquê**, não o quê — o diff já mostra o quê. Se a mudança
  existe por causa de uma decisão registrada, cite o ADR.
- Tipos em uso: `feat`, `fix`, `docs`, `refactor`, `test`, `chore`.

## Pull Request

- Descreva o **problema** que o PR resolve antes da solução.
- Aponte o ADR, invariante ou PRD relacionado, se houver.
- Destaque explicitamente qualquer quebra de compatibilidade.
- `make ci` verde. O CI remoto roda exatamente o mesmo `make ci-check`.

## Documentação

Documentação faz parte da entrega, não vem depois. Antes de escrever qualquer
documento, leia o contrato em [`docs/README.md`](README.md) para saber em qual camada
ele entra. `make lint-docs` valida a forma e reprova o CI como lint de código.

## Convenções

Documentação e commits em português; código em inglês. O `README.md` abre pelo
problema, não pela stack. As convenções de código estão em [`AGENTS.md`](../AGENTS.md).
