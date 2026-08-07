# ADR-0015: O núcleo não conhece rastreador de issues

**Status:** Aceito
**Data:** 2026-08-06

## Contexto

Uma tarefa pode vir de um rastreador de issues, de outro rastreador, ou de lugar nenhum.
Integrar nativamente com um deles tornaria o núcleo dependente daquela ferramenta.

## Decisão

A FSM **não conhece rastreador de issues**. A tarefa entra por um comando próprio de
criação ou por um **adaptador de importação**, que é um comando separado do núcleo.

## Alternativas consideradas

- **Integração nativa com Plane** — descartada porque amarraria o sistema a uma
  ferramenta específica. A tarefa pode vir de qualquer lugar, ou de lugar nenhum.

## Consequências

- **Positivas:** trocar ou acrescentar uma origem de tarefa não toca o núcleo.
- **Impactos:** cada origem suportada exige seu próprio adaptador de importação.

## Referências

- Documentos relacionados: [arquitetura](../architecture/overview.md)
