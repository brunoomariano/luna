# PRD <domínio>-NNNN: <título>

> Especificação de produto de uma feature. Documento datado: ver a regra de
> obsolescência em [docs/README.md](../README.md#regra-de-obsolescência-e-atualização).
>
> **Identificador.** O arquivo se chama `<domínio>-NNNN-title.md` — o `<domínio>` é
> o mesmo do path (`docs/PRDs/<domínio>/`, o termo do glossário) e `NNNN` é sequencial
> zero-padded dentro daquele domínio (`0001`, `0002`…). O **nome do arquivo (slug) é em
> inglês** — como todo path/ID da casa —, ainda que a prosa do PRD seja PT-BR. Ex.:
> `docs/PRDs/alerts/alerts-0001-suppression-on-recovery.md`. É esse ID que o RFC, o ADR
> e a issue referenciam para fechar a rastreabilidade.

**Status:** NÃO IMPLEMENTADO | IMPLEMENTADO | OBSOLETO
**Última revisão:** AAAA-MM-DD
**Issue de origem:** <ID do rastreador, ex. ALERT-45> | —
**RFC:** [<rfc-NNNN>](../../RFCs/<rfc-NNNN>-title.md) | —

## Visão geral
Resumo simples da feature, em linguagem stakeholder-facing.

## Problema
Qual dor do usuário ou do sistema está sendo resolvida.

## Objetivo
O que essa feature resolve ou melhora.

## Fluxo funcional (Mermaid)

```
flowchart TD
  A[Evento / Trigger] --> B[Processamento]
  B --> C{Decisão}
  C -->|Caminho A| D[Resultado A]
  C -->|Caminho B| E[Resultado B]
```

## Comportamento esperado
### Fluxo principal
1.
2.
3.

### Casos de borda
-

### Tratamento de erros
- O que acontece em falhas
- O que deve ser logado/observado

## Requisitos
### Funcionais
- RF1:
- RF2:

### Não funcionais
- RNF1:
- RNF2:

## Impactos no sistema
- Serviços afetados:
- Dados/persistência:
- Observabilidade:

## Plano de rollout
- Flag de feature? (sim/não)
- Estratégia de rollback:
