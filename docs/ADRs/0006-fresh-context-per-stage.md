# ADR-0006: Contexto novo a cada etapa

**Status:** Aceito
**Data:** 2026-08-06

## Contexto

A erosão de papel é um dos modos de falha conhecidos (ver
[ADR-0001](0001-flow-control-out-of-model.md)): numa sessão longa com compactação, o
revisor começa a implementar e o implementador a revisar. Manter o processo do agente
vivo entre etapas é mais barato e preserva contexto, mas é exatamente o vetor dessa
degradação.

## Decisão

Cada etapa começa com o contexto limpo, **mesmo quando o papel é o mesmo**. Não há
sessão longa para degradar.

Todo payload de handoff é prefixado com uma instrução para reler o papel e as regras —
contexto limpo e regra reinjetada são duas defesas pelo mesmo flanco.

## Alternativas consideradas

- **Manter o processo vivo entre etapas** — mais barato e preserva contexto, mas
  descartada por ser o vetor da erosão de papel: contexto longo degrada aderência.

## Consequências

- **Consequência aceita:** o handoff vira a **única** ponte entre etapas. Se algo
  necessário não estiver nele, o agente começa cego — e por isso o contrato de etapa
  (ver [ADR-0004](0004-stage-requires-produces-contract.md)) deixa de ser opcional.
- **Negativas / custos:** perde-se a economia de reaproveitar um processo já quente.

## Referências

- Documentos relacionados: [arquitetura](../architecture/overview.md),
  [referências](../references.md)
