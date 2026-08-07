# Glossário: núcleo

> Fonte única dos termos do núcleo da Luna. Um termo, uma definição. Ver os padrões
> em [docs/README.md](../README.md).

## Tarefa

**Definição.** A unidade de trabalho que a Luna executa de ponta a ponta. Entra por
`luna task new` ou por um adaptador de importação, atravessa a FSM e termina em
commit, bloqueio ou abandono explícito.

**Não confundir com.** *Etapa* — a tarefa é o todo, a etapa é um passo dentro dela.

**Onde aparece.** É a raiz de tudo: cada tarefa tem um lead, uma worktree e uma
instância da FSM.

---

## Etapa (*stage*)

**Definição.** Um estado da máquina, com trabalho próprio. Declara o papel que a
executa, a skill que usa, o que **exige** para começar (`requires`) e o que **produz**
ao terminar (`produces`).

**Não confundir com.** *Efeito* — carregar memória e gravar resumo acontecem na
entrada e na saída do fluxo, não são estados. Um estado sem trabalho próprio não
deveria ser um estado.

**Onde aparece.** As etapas padrão estão em
[`docs/architecture/stages.md`](../architecture/stages.md); os arquivos vivem em
`src/stock/stages/`.

---

## Contrato de etapa

**Definição.** O par `requires`/`produces` que cada etapa declara. Sustenta três
verificações: estática (antes de rodar, algum `requires` não é produzido por nenhuma
etapa anterior?), de entrada (não chama o agente sem insumo) e de saída (não fecha sem
entregar).

**Não confundir com.** *Validação de formato* — o contrato é verificado **rodando a
ferramenta**: o teste passa, o arquivo existe, o commit resolve. Um JSON bem-formado
pode descrever algo que não existe.

**Onde aparece.** É o que impede handoff incompleto — ver
[`docs/invariants/core.md`](../invariants/core.md).

---

## Papel (*role*)

**Definição.** O perfil sob o qual um agente executa uma ou mais etapas. Declara o que
possui (`owns`), o que **não** possui (`not_owns`) e as ferramentas a que tem acesso
(`tools_allow`/`tools_deny`).

**Não confundir com.** *Etapa* — um papel pode cobrir várias etapas; a etapa é o passo,
o papel é quem o executa.

**Onde aparece.** `src/stock/roles/`. O `tools_allow`/`tools_deny` é gating mecânico: a
FSM restringe as ferramentas antes de o agente começar.

---

## Especialização por negação

**Definição.** O mecanismo pelo qual um papel se define pelo que **não** faz. Quem
escreve não revisa: o `implementer` não roda `code-review`, o `cleaner` não roda
`harden`.

**Onde aparece.** O campo `not_owns` de cada papel. Parte da separação é econômica —
a operação mais cara do pipeline é rodada por um papel só.

---

## Nó (*node*)

**Definição.** Uma chamada de agente: recebe contexto, executa, devolve resultado. É
um redutor sem estado — não guarda nada entre invocações.

**Não confundir com.** *Etapa* — a etapa é o estado da máquina; o nó é a execução do
agente dentro dela.

**Onde aparece.** `src/internal/node/`.

---

## Lead

**Definição.** Quem conduz uma tarefa através da FSM. É **híbrido**: no caminho feliz
é código (decide a etapa, chama o agente, valida, grava — custo zero de token,
determinístico); quando algo sai do trilho, um modelo decide o que fazer.

**Não confundir com.** *Nó* — o lead conduz o fluxo; o nó faz o trabalho da etapa.

**Onde aparece.** Uma goroutine por tarefa. Ver
[`docs/architecture/overview.md`](../architecture/overview.md).

---

## Handoff

**Definição.** A transição registrada entre duas etapas. Acontece a **cada** transição,
mesmo quando o papel não muda. Carrega ponteiros (identificador da tarefa, etapa de
origem, artefatos, decisões de gate) e um snapshot endereçado por conteúdo.

**Não confundir com.** *"Passar para outro agente"* — o handoff é a transição em si, e
o log de handoffs é a auditoria completa da tarefa.

**Onde aparece.** É a **única** ponte entre etapas, já que cada etapa começa com
contexto limpo. O payload é gerado pelo sistema, não escrito pelo agente.

---

## Snapshot endereçado por conteúdo

**Definição.** O hash do que existia no momento do handoff, para que o receptor veja
exatamente o que o emissor viu — mesmo que a worktree tenha mudado depois. Dá a
imutabilidade que um commit daria, sem amarrar o transporte ao versionamento.

**Onde aparece.** Parte do payload de todo handoff; armazenado no store de conteúdo.

---

## Gate

**Definição.** Um ponto onde a tarefa para e espera decisão humana. **Não** segura um
processo vivo: após uma espera curta no terminal, a tarefa suspende e libera o slot.
`luna gate approve` retoma do ponto exato.

**Não confundir com.** *Bloqueio por falha* — o gate é uma pausa planejada; o bloqueio
é a saída de um nó que falhou.

**Onde aparece.** Quais gates param é decidido por *perfil*.

---

## Perfil (*profile*)

**Definição.** O conjunto de regras que decide quais gates esperam humano. Escolhido
**por tarefa**, não por tipo de tarefa nem por repositório — o tipo não prediz o risco.

**Onde aparece.** Os três padrão: `interativo` (todos os gates esperam), `turbo` (só a
escrita espera), `noturno` (nada espera). Vivem em `src/stock/profiles/`.

---

## FSM

**Definição.** A máquina de estados que governa as etapas **dentro** de uma tarefa. É
ela — e não o modelo — que decide qual é a próxima etapa.

**Não confundir com.** *Beads* — a FSM governa as etapas dentro de uma tarefa; o Beads
governa a ordem **entre** tarefas (dependências, o que está livre, reivindicação
atômica).

**Onde aparece.** `src/internal/fsm/`. O estado vive em SQLite append-only.

---

## Loop de convergência

**Definição.** O ciclo `build → refactor → verify` que repete até o trabalho convergir.
Tem tetos: voltas máximas, voltas sem progresso e voltas em oscilação abrem um gate em
vez de continuar iterando.

**Não confundir com.** *Retry de falha* — o retry é a resposta a um nó que quebrou (até
2 tentativas); o loop de convergência é o ciclo normal de refinamento.

**Onde aparece.** As etapas marcadas 🔁 em
[`docs/architecture/stages.md`](../architecture/stages.md).

---

## Skill

**Definição.** O corpo de instrução que um papel usa para executar uma etapa. Há as
**da Luna** (instaladas no harness, explicam a estrutura ao agente), as **do bundle do
usuário** (apontadas em configuração) e as **transversais do desenvolvedor** (que
servem dentro e fora da Luna).

**Onde aparece.** `src/stock/skills/`. Um comando de indexação cataloga o que existe.
