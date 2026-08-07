# Luna

**Agentes de IA não seguem um processo determinístico só porque você pediu em prosa.**

Você escreve um fluxo de trabalho — investigar, planejar, testar, implementar, revisar,
entregar — e o agente segue *na maior parte das vezes*. Ele pula um passo quando acha
que não precisa. Reinterpreta uma instrução. Para de iterar sem motivo. Numa sessão
longa, esquece qual era o papel dele. Cada uma dessas falhas é barata sozinha e cara
em conjunto: você deixa de confiar no resultado e volta a revisar tudo à mão.

A Luna resolve isso movendo o **controle de fluxo** para fora do modelo.

> *"When using agents, ensure that everything that can be deterministic, is done with
> a deterministic tool. Don't try to get the poor agents to follow a deterministic
> process."*
> — Robert C. Martin

Uma máquina de estados decide qual etapa vem agora, qual papel a executa, e valida o
resultado **rodando a ferramenta de verdade** — o teste roda, o commit existe, o
arquivo está lá. O modelo faz o trabalho dentro de cada etapa, onde julgamento é o que
importa. Ele nunca decide o próximo passo.

## O que isso muda na prática

- **A etapa não fecha sem entregar o que declarou.** Se `scenarios` promete produzir
  cenários e uma abordagem, e volta só com os cenários, a etapa não fecha e nada é
  passado adiante. O buraco aparece onde nasceu.
- **A etapa não começa sem receber o que exige.** O contrato é verificado antes de
  qualquer agente ser chamado — inclusive de forma estática, antes de rodar.
- **Cada etapa começa com contexto limpo.** Sem erosão de papel ao longo de uma sessão
  longa. O que precisa atravessar, atravessa pelo handoff.
- **A falha nunca é silenciosa.** Tentativa, volta de etapa ou bloqueio com aviso — a
  tarefa não morre sem você saber.
- **Você escolhe quanta autonomia dar**, por tarefa: do fluxo todo com aprovação humana
  até a execução noturna sem interrupção.

## Estado

**Em desenho.** A arquitetura está fechada e registrada em [`docs/`](docs/); o código
ainda não começou. Há um protótipo navegável do fluxo em
[`prototypes/fsm-flow.html`](prototypes/fsm-flow.html) — abra no navegador e clique
pelos cenários para ver a máquina de estados funcionando, incluindo o caso de handoff
incompleto.

## Instalação

Ainda não há release. Quando houver, será um comando.

## Documentação

[`docs/README.md`](docs/README.md) é o contrato da documentação: diz quais camadas
existem, o que cada uma responde e onde mora. Comece por ele se for escrever um
documento. Os pontos de entrada:

| Documento | Assunto |
|---|---|
| [`docs/architecture/overview.md`](docs/architecture/overview.md) | como o sistema funciona e por quê |
| [`docs/architecture/stages.md`](docs/architecture/stages.md) | as etapas padrão e o contrato de cada uma |
| [`docs/ADRs/`](docs/ADRs/) | decisões tomadas, com a alternativa recusada |
| [`docs/invariants/`](docs/invariants/) | regras que sempre valem |
| [`docs/glossary/`](docs/glossary/) | os termos do domínio |
| [`docs/references.md`](docs/references.md) | de onde vieram as ideias |
| [`AGENTS.md`](AGENTS.md) | para agentes que trabalham neste repositório |
| [`docs/CONTRIBUTING.md`](docs/CONTRIBUTING.md) | fluxo de contribuição, commits e tags |

## Licença

A definir.
