# ADR-0023: O loop tem três tetos contados separadamente

**Status:** Aceito
**Data:** 2026-08-07

## Contexto

O loop de convergência (`build` → `refactor` → `verify`) precisa de teto, senão é o loop
que não converge e queima tokens — o mesmo motivo que descartou o retry infinito em
[ADR-0011](0011-failure-retry-rollback-or-block.md).

O protótipo usa **um contador só** (`loop`), incrementado tanto por falha de nó quanto
por volta de revisão, e zerado a cada etapa que fecha. É simplificação de protótipo: o
desenho original previa três limites distintos, que a simplificação fundiu.

Um contador único esconde a diferença entre patologias que pedem respostas diferentes:
um loop que gira quatro vezes **progredindo** é saudável e deve continuar; um que gira
duas vezes **sem produzir mudança funcional** está travado; um que alterna entre os
mesmos dois estados está desfazendo o próprio trabalho.

## Decisão

Três tetos, contados e configurados separadamente por loop:

| Teto | Conta | Detecta |
|---|---|---|
| `max_rounds` | voltas totais | o loop que não termina |
| `no_progress_rounds` | voltas seguidas sem mudança funcional | o loop que gira sem produzir |
| `oscillation_rounds` | voltas seguidas alternando entre os mesmos estados | o loop que desfaz o que acabou de fazer |

Estourado **qualquer um**, o loop **abre um gate** — não bloqueia. A distinção importa: o
loop que não converge não é falha do nó, é decisão a tomar, e o humano tem o histórico
para tomá-la.

O contador de volta de loop é **separado do contador de retry de falha** (ADR-0011). São
naturezas distintas: retry responde a um nó que quebrou; volta de loop responde a
trabalho que ainda não convergiu. Uni-los faria uma falha transitória consumir orçamento
de convergência.

`no_progress_rounds` vem do amortecedor observado no SwarmForge (ver
[referências](../references.md)): *"não produziu mudança funcional"* como condição de
parada, em vez de só contagem de voltas.

## Alternativas consideradas

- **Um contador só, como no protótipo** — descartada porque força um teto único a
  arbitrar três situações diferentes. Baixo demais, mata loop saudável que estava
  progredindo; alto demais, deixa o loop travado girar até o limite.
- **Só `max_rounds`** — descartada porque a contagem de voltas não distingue progresso de
  estagnação. Quatro voltas produtivas e quatro voltas idênticas somam o mesmo número.
- **Bloquear em vez de abrir gate** — descartada porque não convergir não é anomalia do
  nó, e `blocked` é o estado de anomalia. Um loop que atingiu o teto tem informação útil
  para o humano decidir se continua, aborta ou muda o rumo.

## Consequências

- **Positivas:** cada patologia é detectada pelo sinal que a caracteriza. O gate em vez do
  bloqueio mantém a decisão com quem pode tomá-la.
- **Negativas / custos:** exige definir o que conta como "mudança funcional" — a mesma
  pergunta que o SwarmForge responde de forma grosseira (mudança só de manifesto não
  conta). É calibração que só o uso resolve.
- **Impactos:** o estado do loop deixa de ser um inteiro e passa a carregar as três
  contagens mais o suficiente para detectar oscilação (os últimos estados visitados).

## Referências

- Documentos relacionados: [etapas padrão](../architecture/stages.md),
  [referências](../references.md)
