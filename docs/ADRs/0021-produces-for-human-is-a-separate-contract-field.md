# ADR-0021: O que só o humano lê é campo próprio do contrato

**Status:** Aceito
**Data:** 2026-08-07

## Contexto

Ao mapear o grafo de dados do contrato, sete artefatos apareceram como **folhas**:
`dod_checked`, `min_case`, `qa_report`, `review_report`, `mutation_report`, `arch_report`
e — antes de [ADR-0022](0022-gate-carries-artifact-for-review.md) — o `contract`. São
produzidos e nunca declarados em `requires` por etapa nenhuma.

Isso cria uma assimetria silenciosa. A verificação estática (ver
[ADR-0004](0004-stage-requires-produces-contract.md)) percorre as etapas acumulando o que
cada uma produz e acusando `requires` sem produtor. Ela **não tem como acusar o
contrário**: um `produces` que ninguém consome passa despercebido, e nada no contrato
distingue "artefato que o fluxo precisa" de "relatório que uma pessoa vai ler".

A consequência prática: o relatório de QA é tão obrigatório quanto o código, mas o
contrato não sabe disso. Se a etapa fechasse sem produzi-lo, nenhuma verificação
reclamaria — porque nenhuma etapa adiante sentiria falta.

## Decisão

A etapa declara **três** campos, não dois:

```toml
[stage.qa]
requires           = ["ci_green", "briefing"]
produces           = []                 # o fluxo não consome nada de qa
produces_for_human = ["qa_report"]      # mas o report é obrigatório
```

- **`produces`** — consumido por alguma etapa adiante. Entra na verificação estática.
- **`produces_for_human`** — lido por uma pessoa. **Verificado na saída** exatamente
  como o `produces` (a etapa não fecha sem entregar), mas **isento da verificação
  estática**: não é defeito ninguém consumi-lo.

A geração é **configurável por etapa**: um artefato de auditoria pode ser desligado
quando não se justifica, e o contrato registra essa escolha em vez de deixá-la implícita.

Um artefato pode migrar de `produces_for_human` para `produces` quando alguma etapa
passar a consumi-lo — foi o que aconteceu com o `contract`.

## Alternativas consideradas

- **Manter um campo só e relaxar a verificação estática para avisar em vez de reprovar**
  — descartada porque perde a capacidade de configurar a geração por etapa, e porque
  transforma uma distinção semântica real ("quem consome isto?") num aviso que se aprende
  a ignorar.
- **Marcar o item dentro de `produces` (`consumed_by_flow: false`)** — descartada por
  verbosidade: a marcação se repetiria em cada artefato, em vez de morar no campo que já
  a expressa.
- **Não produzir o que ninguém consome** — descartada de saída. O relatório de QA e o
  parecer de arquitetura são parte do valor da etapa; o fluxo não os consumir não os
  torna descartáveis, torna-os **destinados a outro leitor**.

## Consequências

- **Positivas:** a obrigatoriedade do relatório passa a ser verificável — a etapa não
  fecha sem ele. A verificação estática continua estrita para o que o fluxo consome, sem
  falso positivo no que ele não consome.
- **Negativas / custos:** um campo a mais no contrato de toda etapa, e uma regra a mais
  para quem escreve etapa nova entender.
- **Impactos:** a verificação de saída passa a considerar os dois campos; a estática, só
  o primeiro. O handoff registra ambos, porque o artefato de auditoria também precisa ser
  localizável depois (ver [arquitetura](../architecture/overview.md), "como o humano vê o
  que está esperando").

## Referências

- Documentos relacionados: [etapas padrão](../architecture/stages.md),
  [arquitetura](../architecture/overview.md), [invariantes](../invariants/core.md)
