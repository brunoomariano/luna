# ADR-0002: O lead é híbrido, não puramente determinístico

**Status:** Aceito
**Data:** 2026-08-06

## Contexto

Com o controle de fluxo em código (ver [ADR-0001](0001-flow-control-out-of-model.md)),
resta decidir quem conduz a tarefa quando algo sai do trilho: o nó falhou, a saída não
validou, uma revisão achou problema.

Há um caso real que pesa na escolha. Numa execução do sistema que inspirou este desenho,
a máquina de estados tinha um bug e mandou o lead revisar um documento já revisado. **O
lead recusou e escalou ao humano** — ele fora instruído a obedecer a FSM, e ainda assim
reconheceu que a instrução não fazia sentido.

## Decisão

O lead é híbrido: **código no caminho feliz**, **modelo quando algo sai do trilho**.

No caminho feliz o lead decide a etapa, chama o agente, valida e grava — custo zero de
token, comportamento determinístico. Fora dele, um modelo decide o que fazer: tentar de
novo, voltar uma etapa, abrir um gate ou bloquear.

## Alternativas consideradas

- **Lead puramente código** — descartada porque perde o discernimento que pega o erro da
  própria FSM. Existe caso real de um lead que recusou uma instrução sem sentido e
  escalou.
- **Lead puramente modelo** — descartada pelo custo por tarefa, e porque volta a ser
  não-determinístico justamente onde precisamos de garantia.

## Consequências

- **Positivas:** a camada determinística dá o esqueleto; a camada de julgamento pega o
  erro do esqueleto. As duas se protegem.
- **Negativas / custos:** existem dois caminhos de decisão a manter, em vez de um.

## Referências

- Documentos relacionados: [arquitetura](../architecture/overview.md),
  [referências](../references.md)
