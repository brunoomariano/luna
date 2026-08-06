# Referências

De onde vieram as ideias, e o que foi recusado de cada uma.

## SwarmForge — Robert C. Martin

<https://github.com/unclebob/swarm-forge>

Orquestração de squad de agentes em tmux, com worktrees por papel e um daemon de
handoff. Motor em Babashka. Estudado a fundo em 2026-08-06.

**Trazido:**

- **Payload sintetizado pelo sistema.** O agente preenche campos estruturados; o corpo
  entregue é gerado. O agente não consegue injetar prosa na mensagem — o que elimina a
  degradação por reescrita ao longo da cadeia.
- **Instrução de reler o papel em todo handoff.** Anti-erosão barato e eficaz.
- **Validação com a ferramenta real.** Ele valida o commit rodando `git` — não confere
  se o texto *parece* um SHA, confere se resolve para exatamente um objeto e se esse
  objeto é um commit.
- **Especialização por negação.** Cada papel declara o que **não** faz. Parte da
  separação é econômica: a operação mais cara do pipeline é rodada por um papel só.
- **Amortecedor de loop.** "Não produziu mudança funcional" como condição de parada,
  não só contagem de voltas.
- **Transição atômica como estado.** Nele o estado é a localização do arquivo e toda
  transição é um rename. A ideia foi mantida; o meio mudou.

**Recusado:**

- **Git como canal de transporte.** Funciona bem lá, mas amarra o transporte ao
  versionamento.
- **Worktree por agente.** Aqui é por tarefa — o paralelismo é entre tarefas.
- **Fila por papel com outbox/inbox.** A FSM é o canal; não há agentes concorrentes
  trocando mensagens dentro de uma tarefa.
- **Notificação por injeção de teclas no terminal**, com pausas ajustadas
  empiricamente. Frágil a mudanças nos CLIs.

**O que faltava lá e virou requisito aqui:** não há detecção de agente travado ou de
cadeia rompida. Um enxame que para de conversar para em silêncio. Daí o watchdog de
inatividade.

## Observações de campo do mesmo autor

Relatos públicos de execução real, agosto de 2026. São a origem dos modos de falha que
o desenho precisa cobrir:

- *"Ensure that everything that can be deterministic, is done with a deterministic tool.
  Don't try to get the poor agents to follow a deterministic process."*
- *"Agents are completely unreliable unless you pin them down with strict rules, and
  repeat those rules at every possible turn."*
- Um agente decidiu que **rodar** um comando significava **imprimi-lo**.
- *"Agents don't deal with loops well. They might do the same thing 100 times and the
  101st time do something completely different."*
- Sob compactação prolongada, os agentes **perderam a identidade de papel** — o
  implementador passou a revisar e o revisor ficou sem trabalho.
- A FSM dele tinha um bug e mandou o lead revisar um documento já revisado. **O lead
  recusou e escalou.** É o argumento a favor do lead híbrido.

## 12-Factor Agents — HumanLayer

<https://github.com/humanlayer/12-factor-agents>

Princípios para software com LLM que aguenta produção. Os que este desenho aplica:

| Fator | Onde aparece |
|---|---|
| 5 — unificar estado de execução e de negócio | um store, não quatro lugares |
| 6 — lançar/pausar/retomar por API simples | o gate suspende e libera o slot |
| 7 — falar com humanos por chamada de ferramenta | o gate é mecanismo, não convenção |
| 8 — controle de fluxo é seu | a FSM, e não o modelo, decide a próxima etapa |
| 10 — agentes pequenos e focados | um papel por responsabilidade |
| 12 — o agente é um redutor sem estado | o nó recebe contexto e devolve resultado |

A observação central: os produtos que funcionam são *"mostly deterministic code, with
LLM steps sprinkled in at just the right points"*.

## Beads

<https://github.com/gastownhall/beads>

Rastreador em grafo para agentes: dependências, cálculo do que está livre para começar,
reivindicação atômica. Usado para a ordem **entre** tarefas.

Não é usado para as etapas **dentro** de uma tarefa — modelar 14 etapas × N tarefas como
sub-issues inflaria o grafo sem ganho.

## Agent of Empires e herdr

<https://github.com/agent-of-empires/agent-of-empires> ·
<https://github.com/herdrdev/herdr>

Gerenciadores de sessão de agentes. Não compõem o desenho atual, mas resolvem o problema
de **executar e observar** processos de agente — que é uma decisão ainda em aberto.

Do herdr, a ideia que mais interessa: uma API que os próprios agentes dirigem, incluindo
esperar até que outro agente esteja genuinamente bloqueado.

## ai-jail e ai-memory

<https://github.com/akitaonrails/ai-jail> ·
<https://github.com/akitaonrails/ai-memory>

Contenção de sistema de arquivos e memória durável de projeto. Ortogonais à orquestração
e já validados em uso — a Luna compõe com eles em vez de reimplementá-los.

## Práticas de projeto

<https://akitaonrails.com/2026/05/30/boas-praticas-projetos-codigo-aberto-llm-o-minimo/>

A estrutura deste repositório segue o mínimo proposto ali: superfície de instalação em
um comando, CI automatizado, e documentação que abre pelo **problema** e não pela stack.
