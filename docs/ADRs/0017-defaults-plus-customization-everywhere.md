# ADR-0017: Padrão + customização, em tudo

**Status:** Aceito
**Data:** 2026-08-06

## Contexto

O fluxo padrão da Luna é o desenho de quem a construiu. Outra pessoa terá outro — e uma
ferramenta com fluxo fixo serve a um usuário só.

## Decisão

Tudo que define comportamento tem **versão padrão e versão do usuário**: etapas, papéis,
perfis de gate, loops e skills. O padrão vem instalado; o usuário pode desabilitar,
editar ou criar o seu.

## Alternativas consideradas

- **Fluxo fixo** — descartada porque o desenho padrão é o nosso, e outra pessoa terá
  outro. Sem extensão, a ferramenta serve a um usuário só.

## Consequências

- **Positivas:** a Luna serve fluxos diferentes do que a originou.
- **Impactos:** cada eixo de comportamento precisa de um ponto de extensão declarado — de
  `src/stock/` como padrão à configuração do usuário como sobreposição.

## Referências

- Documentos relacionados: [arquitetura](../architecture/overview.md),
  [etapas padrão](../architecture/stages.md)
