# Etapas padrão

Estas são as etapas que a Luna traz instaladas. Não são obrigatórias: podem ser
desabilitadas, editadas ou substituídas, e novas podem ser criadas.

Cada etapa declara o papel que a executa, a skill que ela usa, o que **exige** para
começar e o que **produz** ao terminar. O contrato é o que impede uma etapa de começar
cega ou fechar pela metade.

| # | Etapa | Papel | Gate | Condição | Exige | Produz |
|---|---|---|---|---|---|---|
| 1 | `discovery` | — | confirm-repos | | `task_id` | `repos` |
| 2 | `setup` | — | | | `repos` | `worktree` |
| 3 | `intake` | analyst | | | `task_id`, `worktree` | `briefing`, `kind` |
| 4 | `diagnose` | — | | é bug | `briefing` | `root_cause`, `min_case` |
| 5 | `scenarios` | gherkin author | approve-plan | | `briefing`, `kind` | `scenarios`, `approach` |
| 6 | `spec` | — | approve-spec | feature ou bug | `approach` | `contract` |
| 7 | `build` | implementer | | 🔁 | `scenarios`, `approach`, `worktree` | `code`, `tests_green` |
| 8 | `refactor` | cleaner | | 🔁 | `code`, `tests_green` | `code` |
| 9 | `verify` | — | | 🔁 | `code`, `scenarios` | `ci_green`, `dod_checked` |
| 10 | `qa` | QA tester | | não é chore | `ci_green`, `briefing` | `qa_report` |
| 11 | `code-review` | reviewer | | não é docs | `code`, `ci_green` | `review_report` |
| 12 | `harden` | hardener | | feature ou bug | `tests_green`, `code` | `mutation_report` |
| 13 | `architecture` | architect | | mexe em estrutura | `code` | `arch_report` |
| 14 | `commit` | — | confirm-write | | `ci_green`, `code` | `commit_sha` |

🔁 = participa do loop de convergência.

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

**O loop também tem tetos.** Voltas máximas, voltas sem progresso e voltas em oscilação
abrem um gate em vez de continuar iterando. Loops são declarados em arquivo, com regras
próprias.

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
