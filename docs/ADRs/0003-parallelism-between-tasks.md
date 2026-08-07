# ADR-0003: Paralelismo entre tarefas, não dentro

**Status:** Aceito
**Data:** 2026-08-06

## Contexto

Um orquestrador de agentes pode paralelizar em dois eixos: rodar várias tarefas ao mesmo
tempo, ou rodar vários agentes concorrentes dentro da mesma tarefa. O sistema de
referência escolheu o segundo, com worktree por agente e troca de mensagens entre eles.

## Decisão

O paralelismo é **entre tarefas**. Cada tarefa tem um lead e uma worktree; as etapas
dentro dela são sequenciais.

## Alternativas consideradas

- **Agentes concorrentes por papel dentro da mesma tarefa** — descartada porque exige
  fila por papel e transporte de mensagens entre agentes vivos. É complexidade que só se
  paga quando o gargalo é a tarefa individual — não é o nosso caso.

## Consequências

- **Positivas:** dispensa fila por papel e transporte entre agentes vivos; a FSM é o
  único canal dentro da tarefa.
- **Impactos:** a worktree passa a ser por tarefa, não por agente.

## Referências

- Documentos relacionados: [arquitetura](../architecture/overview.md),
  [referências](../references.md)
