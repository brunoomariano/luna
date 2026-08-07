# ADR-0022: O gate carrega o artefato, e o humano pode ajustá-lo

**Status:** Aceito
**Data:** 2026-08-07

## Contexto

Até aqui o gate era só uma **pausa**: a tarefa suspendia, o humano dizia sim, a tarefa
retomava (ver [ADR-0012](0012-gate-suspends-and-frees-the-slot.md)). O que ele estava
aprovando ficava implícito — o humano teria de ir ver por fora, no worktree ou nos logs.

O caso da `spec` expõe o buraco. Ela produz o `contract` e tem o gate `approve-spec`, mas
**nenhuma etapa declarava `contract` em `requires`**. O artefato mais deliberado do fluxo
— aquele que existe justamente para o humano revisar antes da construção — era produzido
e esquecido.

E "aprovar" não é a única resposta que faz sentido ali. Uma spec que está quase certa não
merece nem um sim nem um não: merece **um ajuste**. Se o humano edita o contrato fora do
sistema, a FSM segue com a versão que ela conhece, e a edição se perde ou — pior — o
`build` usa uma coisa enquanto o registro diz outra.

## Decisão

Um gate pode **carregar o artefato** que a etapa produziu, e o humano tem três respostas:

- **aprovar** — o artefato entra no contexto como está;
- **ajustar** — o humano edita; a **versão editada** é a que entra no contexto, e o
  ajuste fica registrado no handoff;
- **recusar** — o artefato não entra; a etapa que o produziu volta a rodar, com a recusa
  no contexto.

O `contract` deixa de ser folha: passa a ser `requires` do `build`, na versão que saiu do
gate. Quando `spec` não entra no fluxo (`chore`, `docs`), o `build` não o exige.

Isso dá três formas de gate, por riqueza crescente: **confirmação** (sim/não),
**artefato para revisão** (este ADR) e **decisão de fluxo** (um teto de loop estourou).

## Alternativas consideradas

- **Gate continua só sinalizando; o humano abre o artefato por fora** — descartada porque
  não modela o "ajustar". O humano editaria algo que a FSM já considera produzido e
  fechado, criando divergência entre o artefato real e o que o handoff registra.
- **Uma etapa dedicada de revisão humana** — descartada porque duplicaria o mecanismo: o
  gate já é o ponto de parada para decisão humana, e uma etapa que não chama agente
  nenhum não tem trabalho próprio (ver [etapas](../architecture/stages.md), "o que não é
  etapa").
- **Deixar o `contract` como artefato de auditoria** (`produces_for_human`, ver
  [ADR-0021](0021-produces-for-human-is-a-separate-contract-field.md)) — descartada
  porque inverte a intenção: a spec existe **para** guiar a construção. Um contrato que o
  `build` não consome é documento decorativo.

## Consequências

- **Positivas:** a revisão humana passa a acontecer **dentro** do sistema, com rastro. O
  ajuste fica no handoff, então dá para saber depois que a spec construída não era a spec
  gerada — e o que mudou.
- **Negativas / custos:** o gate deixa de ser um booleano e ganha payload e estados de
  resposta. É mais mecanismo no ponto onde antes havia só uma pausa.
- **Impactos:**
  - `build` passa a exigir `contract` quando `spec` entrou no fluxo — a verificação
    estática precisa entender `requires` condicional, o que antes não era necessário;
  - a recusa cria um retorno à etapa anterior. Diferente do retorno de revisão (ver
    [ADR-0020](0020-review-finding-invalidates-green.md)), este acontece **antes** de o
    artefato entrar no contexto, então não há verde a invalidar;
  - o handoff passa a carregar a versão do artefato pós-gate, não a produzida pela etapa.

## Referências

- Documentos relacionados: [etapas padrão](../architecture/stages.md),
  [arquitetura](../architecture/overview.md)
