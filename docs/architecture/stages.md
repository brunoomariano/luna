# Etapas padrão

Estas são as etapas que a Luna traz instaladas. Não são obrigatórias: podem ser
desabilitadas, editadas ou substituídas, e novas podem ser criadas.

Cada etapa declara o papel que a executa, a skill que ela usa, o que **exige** para
começar e o que **produz** ao terminar. O contrato é o que impede uma etapa de começar
cega ou fechar pela metade.

O que uma etapa produz vem em duas naturezas, e a distinção é do contrato, não
cosmética:

- **`produces`** — o que o **fluxo** consome. Alguma etapa adiante o declara em
  `requires`, e a verificação estática garante que exista quem o produza.
- **`produces_for_human`** — o que **só uma pessoa** lê: relatórios de auditoria,
  pareceres, diagnósticos. Verificado na saída igual ao `produces` (a etapa não fecha
  sem entregar), mas **isento da verificação estática** — não é defeito ninguém
  consumi-lo.

| # | Etapa | Papel | Gate | Condição | Exige | Produz p/ o fluxo | Produz p/ humano |
|---|---|---|---|---|---|---|---|
| 1 | `discovery` | — | confirm-repos | | `task_id` | `repos` | |
| 2 | `setup` | — | | | `repos` | `worktree` | |
| 3 | `intake` | analyst | | | `task_id`, `worktree` | `briefing`, `kind` | |
| 4 | `diagnose` | — | | é bug | `briefing` | `root_cause` | `min_case` |
| 5 | `scenarios` | gherkin author | approve-plan | | `briefing`, `kind` | `scenarios`, `approach` | |
| 6 | `spec` | — | approve-spec ⇄ | feature ou bug | `approach` | `contract` | |
| 7 | `build` | implementer | | 🔁 | `scenarios`, `approach`, `worktree`, `contract`* | `code`, `tests_green` | |
| 8 | `refactor` | cleaner | | 🔁 | `code`, `tests_green` | `code` | |
| 9 | `verify` | — | | 🔁 | `code`, `scenarios` | `ci_green` | `dod_checked` |
| 10 | `qa` | QA tester | | não é chore | `ci_green`, `briefing` | | `qa_report` |
| 11 | `code-review` | reviewer | | não é docs | `code`, `ci_green` | | `review_report` |
| 12 | `harden` | hardener | | feature ou bug | `tests_green`, `code` | | `mutation_report` |
| 13 | `architecture` | architect | | mexe em estrutura † | `code` | | `arch_report` |
| 14 | `commit` | — | confirm-write | | `ci_green`, `code` | `commit_sha` | |

🔁 = participa do loop de convergência. ⇄ = gate que **carrega artefato** para revisão.
† = condição sobre um **fato descoberto durante a execução**, não sobre a natureza da
tarefa: só se sabe que a mudança tocou a estrutura depois de olhar o que o `build`
produziu. Por isso a condição de etapa consulta o contexto inteiro (natureza, artefatos
já produzidos e fatos descobertos), e não apenas o `kind`.
\* = `contract` só é exigido quando a etapa `spec` entrou no fluxo (feature ou bug); em
`chore` e `docs` ela é pulada e o `build` não o pede.

> **Ainda não implementado.** O `requires` condicional descrito no `*` acima depende de
> um mecanismo que ainda não foi escolhido — a alternativa está em avaliação por A/B.
> Até lá, `DefaultFlow()` **não** declara `contract` no `requires` do `build`, e o
> código traz a lacuna marcada. Doc e código divergem aqui de propósito: a tabela
> descreve o destino, o código descreve o presente.

## Notas de desenho

**As etapas 10 a 13 são condicionais por natureza.** Teste de mutação numa mudança de
uma linha é cerimônia — e cerimônia treina o humano a ignorar o processo. A condição de
cada uma está na tabela.

**Quem escreve não revisa.** O `implementer` não faz `code-review`; o `cleaner` não roda
`harden`. A separação está nos papéis, não na boa vontade do modelo.

**O loop tem saída por julgamento.** Quando `qa`, `code-review` ou `harden` acham algo, o
modelo decide pelo **alinhamento com a tarefa**: alinhado volta ao `build` (e invalida o
verde anterior); fora do escopo vira tarefa nova e o fluxo segue. Essa distinção não é
mecanizável — é exatamente onde a camada de julgamento existe.

**O loop tem três tetos, contados separadamente.** Não é um contador só: cada teto
detecta uma patologia diferente, e somá-los num número esconderia justamente a diferença.

| Teto | Conta | Detecta |
|---|---|---|
| `max_rounds` | voltas totais do loop | o loop que não termina |
| `no_progress_rounds` | voltas seguidas sem mudança funcional | o loop que gira sem produzir |
| `oscillation_rounds` | voltas seguidas alternando entre os mesmos estados | o loop que desfaz o que acabou de fazer |

Estourado qualquer um, o loop **abre um gate** em vez de continuar iterando — não
bloqueia. A distinção importa: o loop que não converge não é falha, é decisão a tomar.

O contador de volta de loop é **separado do contador de retry de falha**
(ver [ADR-0011](../ADRs/0011-failure-retry-rollback-or-block.md)): são coisas distintas,
com tetos distintos, e uni-los faria uma falha transitória consumir orçamento de
convergência.

Loops são declarados em arquivo, com regras próprias — os três tetos são configuráveis
por loop.

**Um gate pode carregar um artefato para revisão.** O caso claro é `spec`: ela produz o
`contract`, e o gate `approve-spec` entrega esse contrato ao humano, que pode **aprovar,
ajustar ou recusar**. Só a versão aprovada entra no contexto — e é ela que o `build`
consome como `requires`.

Isso faz do gate mais que uma pausa: ele é o ponto onde o humano **edita o artefato** que
a etapa seguinte vai usar. Sem esse mecanismo, o `contract` seria produzido e nunca
consumido, e a revisão humana aconteceria fora do sistema, sem deixar rastro.

- **aprovar** — o artefato entra no contexto como está;
- **ajustar** — o humano edita; a versão editada é a que entra, e o ajuste fica
  registrado no handoff;
- **recusar** — o artefato não entra; a etapa que o produziu volta a rodar com a recusa
  no contexto.

## O que não é etapa

Quatro coisas que existiam no fluxo anterior e não viraram estado:

- **`prime`** e **`close`** são efeitos — carregar memória e gravar o resumo. Viram ações
  de entrada e saída, não etapas.
- **`triage-type`** é um predicado: decide se `diagnose` entra. Vira condição de
  transição.
- **`reread-issue`** existia só porque um modo de preparo pulava o `discovery`. Com
  estado externo, a tarefa já chega carregada.

Um estado que não tem trabalho próprio não deveria ser um estado.

## Antes do fluxo

O fluxo começa numa tarefa já escrita. Transformar demanda crua em tarefa executável é
`luna refine`, um comando à parte — não uma etapa. Decidir **o quê** construir continua
sendo trabalho do humano.
