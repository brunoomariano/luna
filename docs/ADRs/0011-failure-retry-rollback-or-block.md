# ADR-0011: Falha — tentar até 2, depois bloquear com aviso

**Status:** Aceito
**Data:** 2026-08-06

## Contexto

Quando um nó falha, alguém decide o que fazer. Os dois extremos são conhecidos: insistir
indefinidamente, ou chamar o humano na primeira falha. Nenhum dos dois serve.

## Decisão

Quando um nó falha, o modelo avalia e escolhe entre duas saídas:

- **tentar de novo**, até 2 vezes, com o erro no contexto;
- **bloquear e avisar o humano**.

Esgotadas as tentativas, a tarefa vai para `blocked` e notifica. Não há morte
silenciosa: toda tarefa bloqueada avisa.

## Alternativas consideradas

- **Retry infinito com backoff** — descartada porque é o loop que não converge e queima
  tokens.
- **Escalar na primeira falha** — descartada porque transforma o humano no loop de
  correção de coisas que uma tentativa resolveria.
- **Voltar à etapa anterior como terceira saída** — descartada. A ideia era escalar a
  resposta (tenta → volta → bloqueia), mas o degrau do meio não se sustenta:
  - **duplica caminho com o retorno por revisão.** Voltar de `verify` para `build` é a
    mesma transição que um achado alinhado faz — só que por fora da invalidação de
    `ci_green` (ver [ADR-0020](0020-review-finding-invalidates-green.md)). Dois caminhos
    para o mesmo lugar, um deles esquecendo de invalidar o verde;
  - **o que ela resolveria já está coberto.** Falta de insumo é violação de `requires`,
    pega na entrada da etapa (ver [ADR-0004](0004-stage-requires-produces-contract.md));
    falha transitória é o que o retry trata; trabalho ruim a montante é o que as etapas
    de revisão existem para encontrar;
  - **não é mecanizável sem decidir mais coisa.** Quantas etapas voltar? Os artefatos já
    produzidos são invalidados? Sem essas respostas, seria decisão em aberto disfarçada
    de opção fechada.

## Consequências

- **Positivas:** o teto de tentativas impede o loop que não converge; o aviso obrigatório
  impede a tarefa morta em silêncio. Com duas saídas em vez de três, o retorno a uma
  etapa anterior tem **um único caminho** — o de revisão, que invalida o que precisa ser
  invalidado.
- **Negativas / custos:** uma falha que a etapa anterior causaria vai para `blocked` em
  vez de ser corrigida sozinha. É deliberado: o humano decide se o caso merece uma volta,
  em vez de a FSM adivinhar quantas etapas retroceder.
- **Impactos:** depende da camada de julgamento do lead híbrido (ver
  [ADR-0002](0002-hybrid-lead.md)) para escolher entre as duas saídas.

## Referências

- Documentos relacionados: [arquitetura](../architecture/overview.md),
  [etapas padrão](../architecture/stages.md)
