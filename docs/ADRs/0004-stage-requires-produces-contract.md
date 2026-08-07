# ADR-0004: Cada etapa declara `requires` e `produces`

**Status:** Aceito
**Data:** 2026-08-06

## Contexto

Sem um contrato explícito por etapa, o que uma etapa recebe é apenas o que sobrou do
contexto acumulado. Quando algo dá errado, não há como distinguir um erro do modelo de
uma entrega incompleta da etapa anterior.

## Decisão

Cada etapa declara o que **exige** (`requires`) e o que **produz** (`produces`).

Isso sustenta três verificações:

1. **estática, antes de rodar** — percorrendo as etapas em ordem, algum `requires` que
   nenhuma etapa anterior produz indica um fluxo quebrado no papel, detectável sem
   executar nada;
2. **de entrada** — a FSM não chama o agente de uma etapa cujo `requires` não está no
   contexto;
3. **de saída** — a etapa não fecha sem entregar o `produces` declarado.

## Alternativas consideradas

- **Contexto livre acumulado** — descartada porque sem contrato não há como distinguir
  "o modelo errou" de "o modelo não recebeu o que precisava".

## Consequências

- **Positivas:** um fluxo quebrado é detectável antes de qualquer execução; a falha é
  pega onde nasce, não duas etapas adiante quando o sintoma já se deslocou da causa.
- **Impactos:** nenhuma etapa pode existir sem contrato declarado.

## Referências

- Documentos relacionados: [arquitetura](../architecture/overview.md),
  [etapas padrão](../architecture/stages.md)
