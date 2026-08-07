# ADR-0014: Etapas condicionais

**Status:** Aceito
**Data:** 2026-08-06

## Contexto

O fluxo padrão inclui etapas de revisão pesada — QA, code review, teste de mutação,
revisão de arquitetura. Rodar todas em qualquer mudança, independente do tamanho, é
possível, mas tem custo.

## Decisão

Etapas são **condicionais**: etapas de revisão pesada não rodam em tarefa trivial. Cada
etapa declara a condição sob a qual entra no fluxo.

## Alternativas consideradas

- **Rodar tudo sempre** — descartada porque teste de mutação numa mudança de uma linha é
  cerimônia, e cerimônia treina o humano a ignorar o processo.

## Consequências

- **Positivas:** o custo de revisão acompanha o tamanho da mudança; o processo não perde
  credibilidade por excesso de ritual.
- **Impactos:** cada etapa precisa declarar sua condição, além do contrato de
  `requires`/`produces`.

## Referências

- Documentos relacionados: [etapas padrão](../architecture/stages.md),
  [arquitetura](../architecture/overview.md)
