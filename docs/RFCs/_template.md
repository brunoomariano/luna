# RFC-NNNN: <título da mudança>

> Planejamento de uma mudança — a rota técnica de como executá-la. Documento datado:
> ver a regra de obsolescência em
> [docs/README.md](../README.md#regra-de-obsolescência-e-atualização).
>
> **Identificador.** O arquivo se chama `rfc-NNNN-title.md`, com `NNNN` sequencial
> zero-padded a partir de `0001` (o `rfc-0000-template.md` é o modelo e não conta). O
> prefixo é `rfc` fixo — não por domínio, porque um RFC costuma cruzar domínios. O
> **slug é em inglês** (como todo path/ID da casa), ainda que a prosa seja PT-BR. Ex.:
> `docs/RFCs/rfc-0001-dedupe-mqtt-on-consumer.md`.
>
> **Fronteira (fonte única).** O RFC descreve o **como** da mudança. O **quê** e o
> **comportamento esperado** (requisitos funcionais/não-funcionais, casos de borda,
> critérios de aceite) moram no **PRD** — o RFC **linka** o PRD em vez de repetir. O
> **porquê** de uma escolha estrutural que sobrevive à feature vira **ADR**. Se está
> listando requisito de produto, subiu pro PRD; se está gravando "por que decidimos
> assim" de forma permanente, desceu pro ADR.

**Status:** RASCUNHO | EM ANDAMENTO | CONCLUÍDO | OBSOLETO
**Última revisão:** AAAA-MM-DD
**Issue de origem:** <ID do rastreador, ex. ALERT-45> | —
**PRD:** [<domínio>-NNNN](../PRDs/<domínio>/<domínio>-NNNN-title.md) | —

## Motivação
Por que esta mudança precisa acontecer agora. A dor, o gatilho, o custo de não
fazer. Situe o leitor sem pressupor o contexto da issue — o RFC deve se sustentar
sozinho. (O *quê* de produto está no PRD; aqui é o *porquê agora* da execução.)

## Proposta técnica
### Visão geral (guide-level)
A mudança explicada como se fosse ensinada a outra pessoa do time: o que passa a
existir, o que muda no fluxo, em linguagem de comportamento. Mermaid quando o fluxo
fica mais claro em diagrama.

```
flowchart TD
  A[Estado atual] --> B[Mudança proposta]
  B --> C[Estado alvo]
```

### Detalhamento (reference-level)
A rota técnica concreta, sem descer ao nível de `if`/query específica (isso é do
`lsh-code-cycle:build`): serviços/módulos tocados (paths reais), pontos de integração,
contratos afetados (API/MQTT/schema), sequência das partes que se encaixam.

- Serviços/módulos impactados (paths reais):
- Contratos/fronteiras afetados:
- Restrições técnicas relevantes:

## Alternativas consideradas
As abordagens de execução que foram pesadas e por que **não** foram escolhidas.
Uma decisão de rota sem alternativas registradas perde metade do valor. (Se uma
dessas escolhas é **estrutural e permanente**, promova-a a um ADR e linke aqui.)

- **<Abordagem A>** — descartada porque …
- **<Abordagem B>** — descartada porque …

## Drawbacks
O que esta proposta custa, mesmo sendo a escolhida — dívida assumida, complexidade
adicionada, o que fica pior antes de ficar melhor. Ser honesto aqui é o que separa
um RFC de um pitch.

## Impacto e migração
- Dados/persistência (migração de schema? backfill?):
- Compatibilidade (quebra contrato? versionamento?):
- Observabilidade (o que passa a ser logado/medido):
- Superfície de rollback:

## Plano de rollout (faseado)
Os passos de execução, em fases entregáveis. Cada fase deve deixar o sistema num
estado válido.

1. **Fase 1 —** …
2. **Fase 2 —** …
3. **Fase 3 —** …

- Flag de feature? (sim/não)
- Estratégia de rollback:

## Questões em aberto
O que ainda não está resolvido e precisa de decisão antes ou durante a execução.
Uma pergunta sem dono aqui é um risco não mitigado.

- [ ]
- [ ]

## Referências
- Issue: <ID>
- PRD: <domínio>-NNNN
- ADRs relacionados: <ADR-NNNN, se a rota fixou decisão estrutural>
- PRs: <se houver>
