# ADR-0016: CLI primeiro

**Status:** Aceito
**Data:** 2026-08-06

## Contexto

A FSM ainda precisa provar valor. Qualquer superfície de interação construída antes disso
é trabalho investido sobre um desenho que pode mudar.

## Decisão

A interface é **CLI primeiro**. Uma interface visual entra quando o fluxo estabilizar.

## Alternativas consideradas

- **Interface visual desde o início** — descartada por ser muito trabalho antes de a FSM
  provar valor.

## Consequências

- **Positivas:** o esforço fica concentrado no núcleo enquanto o desenho ainda muda.
- **Impactos:** a interação com gates e o acompanhamento de tarefas acontecem no terminal.

## Referências

- Documentos relacionados: [arquitetura](../architecture/overview.md)
