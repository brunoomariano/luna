# ADR-0008: O payload é gerado pelo sistema

**Status:** Aceito
**Data:** 2026-08-06

## Contexto

Se cada agente redige a mensagem que entrega ao próximo, cada salto da cadeia é uma
reescrita — e a mensagem degrada ao longo dela. Foi observado no sistema de referência e
é a origem da prática trazida de lá.

## Decisão

O payload de handoff é **gerado pelo sistema**, não escrito pelo agente. O agente
preenche campos estruturados; o corpo entregue é sintetizado.

## Alternativas consideradas

- **Deixar o agente redigir a mensagem** — descartada porque cada salto reescreve, e a
  mensagem degrada ao longo da cadeia.

## Consequências

- **Positivas:** elimina a degradação por reescrita sucessiva; o agente não consegue
  injetar prosa na mensagem.
- **Impactos:** cada etapa precisa declarar os campos estruturados que o agente preenche.

## Referências

- Documentos relacionados: [arquitetura](../architecture/overview.md),
  [referências](../references.md)
