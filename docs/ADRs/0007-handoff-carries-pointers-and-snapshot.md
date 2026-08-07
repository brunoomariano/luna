# ADR-0007: O handoff carrega ponteiros e snapshot, não resumo

**Status:** Aceito
**Data:** 2026-08-06

## Contexto

Com contexto novo a cada etapa (ver [ADR-0006](0006-fresh-context-per-stage.md)), o
handoff é a única ponte entre etapas. O que ele carrega determina se o receptor vê o
estado real ou a interpretação de outro agente sobre ele.

## Decisão

O handoff carrega **ponteiros** — identificador da tarefa, etapa de origem, artefatos
produzidos, decisões de gate anteriores — e um **snapshot endereçado por conteúdo**: o
hash do que existia no momento do handoff, para que o receptor veja exatamente o que o
emissor viu, mesmo se a worktree mudou depois.

O snapshot endereçado por conteúdo dá a imutabilidade que um commit daria, sem amarrar o
transporte ao versionamento.

## Alternativas consideradas

- **Resumo em prosa do que a etapa fez** — descartada porque reintroduz interpretação na
  cadeia, que é justamente a degradação que se quer eliminar.
- **Git como canal** — funciona (é o que o sistema de referência faz), mas descartada
  porque amarra o transporte ao versionamento.

## Consequências

- **Positivas:** o receptor lê o estado real; ninguém interpreta para ele.
- **Impactos:** exige um store de conteúdo endereçado por hash para os snapshots.

## Referências

- Documentos relacionados: [arquitetura](../architecture/overview.md),
  [referências](../references.md)
