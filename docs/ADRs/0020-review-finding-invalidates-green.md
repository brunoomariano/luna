# ADR-0020: Achado alinhado invalida o verde ao voltar para build

**Status:** Aceito
**Data:** 2026-08-06

## Contexto

Quando uma etapa de revisão — `qa`, `code-review`, `harden` ou `architecture` — encontra
um problema, o modelo decide pelo **alinhamento com a tarefa**: achado alinhado volta ao
`build`; achado fora do escopo vira tarefa nova e o fluxo segue (ver
[ADR-0014](0014-conditional-stages.md) e
[arquitetura/etapas](../architecture/stages.md)).

A volta ao `build` cria um problema que não é óbvio. As etapas de revisão só entram
**depois** de `verify` ter produzido `ci_green`. Se o fluxo volta ao `build`, o código
muda — mas `ci_green` continua no contexto, atestando um estado que não existe mais.

O contrato de etapa (ver [ADR-0004](0004-stage-requires-produces-contract.md)) verifica
que o `requires` **está presente** no contexto. Ele não tem como saber que um valor
presente ficou obsoleto. Sem invalidação explícita, a segunda passagem pelas etapas de
revisão encontraria `ci_green` satisfeito por uma execução anterior ao código atual — e o
contrato aprovaria a entrada, porque do ponto de vista dele o insumo está lá.

Isso derrotaria a garantia central: a validação é feita **rodando a ferramenta** (ver
[ADR-0005](0005-validate-output-by-running-the-tool.md)), justamente para não confiar em
atestado. Um `ci_green` obsoleto é atestado disfarçado de verificação.

## Decisão

Quando um achado alinhado devolve o fluxo ao `build`, a transição **remove `ci_green` do
contexto**. O verde precisa ser reconquistado por uma execução nova de `verify` sobre o
código novo.

A invalidação é parte da transição, não responsabilidade do agente: nenhuma etapa
"lembra" de invalidar o que a mudança dela tornou obsoleto.

## Alternativas consideradas

- **Manter `ci_green` e confiar que `verify` roda de novo no caminho** — descartada
  porque o contrato só exige presença, não frescor. `verify` participa do loop e seria
  reexecutado no caminho feliz, mas o fluxo não *garante* isso: bastaria uma condição de
  etapa mudar para o código novo alcançar `code-review` com o verde antigo.
- **Marcar `ci_green` com o hash do código que o produziu** e comparar na entrada —
  descartada por ora: resolve o mesmo problema com mais mecanismo, e exigiria estender o
  contrato de etapa de presença para validade. Fica registrada como a evolução natural se
  outros produtos passarem a precisar de invalidação por dependência.

## Consequências

- **Positivas:** impede que uma verificação obsoleta valide código novo. É o que mantém
  honesta a segunda passagem pelas etapas de revisão.
- **Negativas / custos:** toda volta ao `build` paga uma reexecução de `verify`, mesmo
  quando a mudança foi mínima. É o custo aceito para não confiar num verde velho.
- **Impactos:** estabelece que **a transição pode invalidar produtos anteriores**. Hoje o
  caso é só `ci_green`; se surgirem outros produtos com dependência de frescor, a
  alternativa do hash volta à mesa.

## Referências

- Protótipo: `prototypes/fsm-flow.html`, caso `REVIEW_FINDING` do redutor `LunaFSM` — o
  comportamento está implementado e é dirigível pelos cenários da página.
- Documentos relacionados: [arquitetura](../architecture/overview.md),
  [etapas](../architecture/stages.md), [invariantes](../invariants/core.md)
