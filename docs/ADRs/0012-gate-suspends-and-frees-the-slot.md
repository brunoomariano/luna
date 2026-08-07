# ADR-0012: Gate suspende e libera o slot

**Status:** Aceito
**Data:** 2026-08-06

## Contexto

Um gate para a tarefa e espera decisão humana. Com N tarefas em paralelo (ver
[ADR-0003](0003-parallelism-between-tasks.md)), a forma dessa espera decide quantos
recursos ficam parados enquanto o humano decide.

## Decisão

O gate faz uma espera curta no terminal; sem resposta, **suspende** a tarefa e libera o
slot. Outra tarefa usa o recurso enquanto você decide, e a aprovação retoma do ponto
exato.

## Alternativas consideradas

- **Agente vivo esperando** — descartada porque com N tarefas em paralelo seriam N
  processos parados, com contexto envelhecendo.

## Consequências

- **Positivas:** o recurso não fica preso à latência humana; nenhum contexto envelhece
  esperando.
- **Impactos:** exige que o estado suspenso seja retomável do ponto exato — o que o store
  append-only (ver [ADR-0010](0010-append-only-sqlite-and-content-addressed-store.md))
  sustenta.

## Referências

- Documentos relacionados: [arquitetura](../architecture/overview.md),
  [referências](../references.md)
