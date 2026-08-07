# ADR-0013: Perfis de gate nomeados, escolhidos por tarefa

**Status:** Aceito
**Data:** 2026-08-06

## Contexto

Nem toda tarefa merece o mesmo nível de supervisão humana. Falta decidir qual dimensão
governa quais gates param.

## Decisão

Quais gates param é decidido por **perfil nomeado, escolhido por tarefa** — por exemplo
`interativo` (todos os gates esperam humano), `turbo` (só a escrita espera) e `noturno`
(nada espera).

## Alternativas consideradas

- **Perfil por tipo de tarefa** — descartada porque o tipo não prediz o risco: um bug
  crítico pode merecer mais gate que uma feature trivial.
- **Perfil por repositório** — descartada porque não distingue tarefa arriscada de
  trivial dentro do mesmo código.

## Consequências

- **Positivas:** o nível de supervisão acompanha o risco real da tarefa, e não uma
  categoria que só o aproxima.
- **Impactos:** o perfil passa a ser um parâmetro da tarefa, escolhido na entrada.

## Referências

- Documentos relacionados: [arquitetura](../architecture/overview.md)
