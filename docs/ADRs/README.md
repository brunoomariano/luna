# Registros de decisão (ADR)

Esta pasta guarda os **Architecture Decision Records** do projeto: o registro de
*por que* tomamos cada decisão técnica ou de produto relevante, no contexto do
momento em que foi tomada. Ver os padrões gerais em [docs/README.md](../README.md).

## O que é um ADR

Um ADR captura uma decisão única e significativa — uma escolha estrutural, de
contrato, de processo ou de produto cujo *porquê* vale preservar. Não é
documentação do estado atual (isso é [arquitetura](../architecture/)); é o
registro datado da decisão que levou a ele.

Registre um ADR quando a decisão:
- afeta a estrutura, um contrato ou um limite do sistema;
- tem alternativas reais que foram descartadas;
- seria custosa ou confusa de reverter sem entender o motivo original.

Não registre ADR para escolhas triviais ou reversíveis sem custo.

## Convenção

- **Imutável.** Um ADR aceito **nunca é editado**. Ele é o registro de um momento.
  Mudou a decisão? Crie um ADR novo (ver abaixo).
- **Numerado.** Arquivos seguem `NNNN-title-in-kebab.md`, com `NNNN` sequencial
  e zero-padded a partir de `0001`. O `0000-template.md` é o modelo e não conta
  como decisão.
- **Status.** Todo ADR carrega um status no topo:
  - `Proposto` — em discussão, ainda não decidido.
  - `Aceito` — decisão vigente.
  - `Substituído por [ADR-NNNN](NNNN-title.md)` — revisado por um ADR posterior.
  - `Rejeitado` — proposta avaliada e não adotada (mantida pelo registro).

## Como adicionar um ADR

1. Copie `0000-template.md` para `NNNN-title.md`, usando o próximo número livre.
2. Preencha contexto, decisão e consequências. Comece em `Proposto`.
3. Ao bater o martelo, mude o status para `Aceito`.

## Como substituir uma decisão

1. Crie um **novo** ADR descrevendo a decisão revista e o motivo da mudança.
2. No ADR antigo, troque o status para
   `Substituído por [ADR-NNNN](NNNN-title.md)` — **essa é a única edição
   permitida** num ADR aceito.
3. Atualize a [arquitetura](../architecture/) viva para refletir o novo estado.

## Índice

<!-- Liste os ADRs aqui conforme forem criados, do mais recente ao mais antigo. -->
<!-- - [ADR-0001](0001-title.md) — <título> — `Aceito` -->

- [ADR-0017](0017-defaults-plus-customization-everywhere.md) — Padrão + customização, em tudo — `Aceito`
- [ADR-0016](0016-cli-first.md) — CLI primeiro — `Aceito`
- [ADR-0015](0015-core-knows-no-issue-tracker.md) — O núcleo não conhece rastreador de issues — `Aceito`
- [ADR-0014](0014-conditional-stages.md) — Etapas condicionais — `Aceito`
- [ADR-0013](0013-named-gate-profiles-per-task.md) — Perfis de gate nomeados, escolhidos por tarefa — `Aceito`
- [ADR-0012](0012-gate-suspends-and-frees-the-slot.md) — Gate suspende e libera o slot — `Aceito`
- [ADR-0011](0011-failure-retry-rollback-or-block.md) — Falha: tentar até 2, depois bloquear com aviso — `Aceito`
- [ADR-0010](0010-append-only-sqlite-and-content-addressed-store.md) — SQLite append-only + store endereçado por conteúdo — `Aceito`
- [ADR-0009](0009-go.md) — Go como linguagem de implementação — `Aceito`
- [ADR-0008](0008-system-generated-payload.md) — O payload é gerado pelo sistema — `Aceito`
- [ADR-0007](0007-handoff-carries-pointers-and-snapshot.md) — O handoff carrega ponteiros e snapshot, não resumo — `Aceito`
- [ADR-0006](0006-fresh-context-per-stage.md) — Contexto novo a cada etapa — `Aceito`
- [ADR-0005](0005-validate-output-by-running-the-tool.md) — A saída é validada rodando a ferramenta — `Aceito`
- [ADR-0004](0004-stage-requires-produces-contract.md) — Cada etapa declara `requires` e `produces` — `Aceito`
- [ADR-0003](0003-parallelism-between-tasks.md) — Paralelismo entre tarefas, não dentro — `Aceito`
- [ADR-0002](0002-hybrid-lead.md) — O lead é híbrido, não puramente determinístico — `Aceito`
- [ADR-0001](0001-flow-control-out-of-model.md) — O controle de fluxo sai do modelo — `Aceito`
