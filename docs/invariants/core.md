# Invariantes: núcleo

> Regras que **sempre** valem na Luna, independentemente de implementação. São o
> contrato conceitual que qualquer código deve preservar — violar um destes não é bug
> de código, é a Luna deixando de ser a Luna. Documento vivo. Ver os padrões em
> [docs/README.md](../README.md).

## INV-core-1: a próxima etapa nunca é decidida pelo modelo

**Regra.** Qual etapa vem agora é decisão da máquina de estados. O modelo trabalha
**dentro** de uma etapa; nunca escolhe qual é a seguinte.

**Por que vale.** É a premissa do projeto inteiro. Um fluxo descrito em prosa é
sugestão, não garantia — e falha nos quatro modos conhecidos: reinterpretação da
instrução, loop abandonado, erosão de papel e cadeia rompida em silêncio.

**Como é preservada.** A FSM detém a transição; o nó recebe contexto e devolve
resultado. Ver [arquitetura](../architecture/overview.md) e
[ADR-0001](../ADRs/0001-flow-control-out-of-model.md).

**O que a violaria.** Um agente escolhendo a próxima etapa; uma etapa cuja saída
inclui "qual etapa rodar agora"; prosa de instrução que substitua a transição
codificada.

> **Fronteira deliberada.** O lead é híbrido: quando algo sai do trilho, um modelo
> decide **o que fazer com a falha** — tentar de novo, voltar, abrir gate, bloquear.
> Isso não viola o invariante: escolher a resposta a uma falha não é escolher o
> caminho feliz. Ver [ADR-0002](../ADRs/0002-hybrid-lead.md).

---

## INV-core-2: o estado nunca é sobrescrito

**Regra.** O store é append-only. Não existe `UPDATE`: toda mudança de estado é um
registro novo.

**Por que vale.** O histórico **é** a auditoria. Sobrescrever apaga a evidência de
como a tarefa chegou onde chegou — e num sistema cujo propósito é ser confiável sem
supervisão contínua, a evidência é o produto.

**Como é preservada.** SQLite append-only, transições atômicas. Matar o processo e
religar reconstrói o estado exato, porque ele nunca esteve só em memória. Ver
[ADR-0010](../ADRs/0010-append-only-sqlite-and-content-addressed-store.md).

**O que a violaria.** Qualquer `UPDATE` ou `DELETE` na tabela de estado; estado
mantido apenas em memória entre transições; compactação que descarte histórico.

---

## INV-core-3: nenhuma etapa começa sem o que exige, nem fecha sem o que produz

**Regra.** Toda etapa declara `requires` e `produces`. A FSM não chama o agente de uma
etapa cujo `requires` não está no contexto, e não fecha a etapa que não entregou o
`produces` declarado.

**Por que vale.** É o que impede handoff incompleto — e o que permite distinguir "o
modelo errou" de "o modelo não recebeu o que precisava". Sem contrato, a falha aparece
duas etapas adiante, quando o sintoma já se deslocou da causa.

**Como é preservada.** Três verificações: estática (antes de rodar, percorrendo as
etapas em ordem), de entrada e de saída. Ver
[ADR-0004](../ADRs/0004-stage-requires-produces-contract.md).

**O que a violaria.** Uma etapa sem contrato declarado; um `produces` marcado como
entregue sem verificação; uma transição que ignore `requires` ausente.

---

## INV-core-4: a entrega é verificada rodando a ferramenta, não conferindo formato

**Regra.** O que a etapa produziu é validado executando a ferramenta real: o teste
passa, o arquivo existe, o commit resolve para exatamente um objeto e esse objeto é um
commit.

**Por que vale.** Um JSON bem-formado pode descrever algo que não existe, e um CLI
sair com código zero não significa que o trabalho ficou certo. Conferir formato valida
a aparência da entrega, não a entrega.

**Como é preservada.** A verificação de saída de cada etapa invoca a ferramenta. Ver
[ADR-0005](../ADRs/0005-validate-output-by-running-the-tool.md).

**O que a violaria.** Aceitar um `produces` porque o campo veio preenchido; validar
por schema em vez de execução; confiar no código de saída do processo.

---

## INV-core-5: cada etapa começa com contexto limpo

**Regra.** Nenhuma etapa herda a sessão da anterior, mesmo quando o papel é o mesmo.

**Por que vale.** Ataca a erosão de papel na origem: não há sessão longa para
degradar. Sob compactação prolongada, agentes perdem a identidade do papel — o
implementador passa a revisar e o revisor fica sem trabalho.

**Como é preservada.** Um processo novo por etapa; todo payload de handoff é prefixado
com a instrução de reler o papel e as regras. Ver
[ADR-0006](../ADRs/0006-fresh-context-per-stage.md).

**O que a violaria.** Manter o processo do agente vivo entre etapas; reaproveitar
contexto "porque o papel não mudou".

**Consequência aceita.** O handoff vira a única ponte entre etapas — por isso
INV-core-3 deixa de ser opcional.

---

## INV-core-6: o handoff carrega ponteiros e snapshot, nunca resumo em prosa

**Regra.** O que atravessa a fronteira entre etapas são ponteiros e um snapshot
endereçado por conteúdo. O receptor lê o estado real; ninguém interpreta para ele.

**Por que vale.** Resumo em prosa reintroduz interpretação na cadeia — exatamente a
degradação que o desenho existe para eliminar. Cada salto reescreveria, e a mensagem
degradaria ao longo da cadeia.

**Como é preservada.** O payload é **gerado pelo sistema**: o agente preenche campos
estruturados, o corpo entregue é sintetizado. O agente não consegue injetar prosa na
mensagem. Ver [ADR-0007](../ADRs/0007-handoff-carries-pointers-and-snapshot.md) e
[ADR-0008](../ADRs/0008-system-generated-payload.md).

**O que a violaria.** Um campo de texto livre redigido pelo agente que atravesse o
handoff; o receptor confiando na descrição do emissor em vez de ler o estado.

---

## INV-core-7: quem escreve não revisa

**Regra.** O papel que produz um artefato não é o papel que o avalia.

**Por que vale.** É a separação que dá valor à revisão. Um agente avaliando o próprio
trabalho não é revisão, é confirmação.

**Como é preservada.** Cada papel declara `not_owns`, e o gating de ferramentas
(`tools_allow`/`tools_deny`) é aplicado pela FSM **antes** de o agente começar — o
`reviewer` não tem `Edit` nem `Write`. O gating **bloqueia a chamada**, não instrui o
agente a evitá-la (ver [ADR-0018](../ADRs/0018-tool-gating-by-pretooluse-hook.md)).

**O que a violaria.** O `implementer` rodando `code-review`; o `cleaner` rodando
`harden`; um papel com `tools_allow` que contradiga o seu `not_owns`; **um papel cuja
restrição exista apenas como texto no prompt**.

**Critério de aceite** — o código não é dado por pronto sem:

- teste que tente usar uma ferramenta negada e verifique que a chamada é **recusada**,
  não apenas desaconselhada;
- teste que detecte `tools_allow` contradizendo `not_owns` na carga do papel, falhando
  no carregamento e não em execução;
- em harness sem bloqueio pré-execução, degradação **explícita e reportada** — nunca
  silenciosa.

---

## INV-core-8: nenhuma falha é silenciosa

**Regra.** Toda tarefa termina em commit, gate ou bloqueio **avisado**. Nenhuma morre
sem que alguém saiba.

**Por que vale.** Um enxame que para de conversar para em silêncio. Numa execução
desassistida, a falha que ninguém vê é mais cara que a falha que interrompe.

**Como é preservada.** Retry limitado a 2 tentativas (sem retry infinito, que é o loop
que não converge e queima tokens) e, esgotadas, bloqueio com aviso. Watchdog de
inatividade para o agente que trava sem falhar (ver
[ADR-0019](../ADRs/0019-inactivity-watchdog.md)). Ver
[ADR-0011](../ADRs/0011-failure-retry-rollback-or-block.md).

**O que a violaria.** Retry com backoff sem teto; uma tarefa suspensa que não notifica;
uma exceção engolida entre transições; um nó que para de progredir sem que nada perceba.

**Critério de aceite** — o código não é dado por pronto sem:

- teste que force esgotamento de retry e verifique que o estado final é `blocked` **e**
  que a notificação foi emitida;
- teste que simule um nó sem progresso além do limite e verifique que o watchdog o
  transforma em decisão, não em espera indefinida;
- nenhum caminho de `running` que termine sem `blocked`, `awaiting_gate` ou `done`.

---

## INV-core-9: o núcleo não conhece rastreador de issues

**Regra.** A FSM não sabe o que é Plane, Jira ou GitHub. A tarefa entra por
`luna task new` ou por um adaptador de importação, que é um comando separado do núcleo.

**Por que vale.** Amarrar o núcleo a uma ferramenta específica limitaria o sistema ao
usuário dela. A tarefa pode vir de qualquer lugar — ou de lugar nenhum.

**Como é preservada.** Adaptadores de importação são comandos à parte. Ver
[ADR-0015](../ADRs/0015-core-knows-no-issue-tracker.md).

**O que a violaria.** Um import de cliente de rastreador dentro de
`src/internal/fsm/`; um campo do estado que só faça sentido para uma ferramenta
específica.

---

## INV-core-10: o gate não segura processo vivo

**Regra.** Um gate suspende a tarefa e **libera o slot**. Após uma espera curta no
terminal, o recurso volta para o pool.

**Por que vale.** Com N tarefas em paralelo, gates que segurassem processos seriam N
processos parados com contexto envelhecendo. O paralelismo entre tarefas é o ganho do
desenho — um gate que o anula custa caro.

**Como é preservada.** A suspensão persiste no store; `luna gate approve` retoma do
ponto exato. Ver [ADR-0012](../ADRs/0012-gate-suspends-and-frees-the-slot.md).

**O que a violaria.** Um agente bloqueado em leitura de stdin esperando o humano; um
slot ocupado por tarefa suspensa.

---

## INV-core-11: toda etapa entrega o que declarou, inclusive o que só o humano lê

**Regra.** Uma etapa não fecha sem entregar **tanto** o `produces` quanto o
`produces_for_human` que declarou. O segundo não é opcional por não ter consumidor no
fluxo.

**Por que vale.** O relatório de QA, o parecer de arquitetura e o diagnóstico existem
para alguém decidir com eles. Se a entrega deles depender de o fluxo senti-los faltando,
nunca serão cobrados — nenhuma etapa adiante os pede.

**Como é preservada.** A verificação de saída considera os dois campos; a verificação
estática, só o `produces` (ver
[ADR-0021](../ADRs/0021-produces-for-human-is-a-separate-contract-field.md)). O handoff
registra caminho e hash de ambos, para que sejam localizáveis depois.

**O que a violaria.** Uma etapa fechando sem o relatório declarado; um artefato de
auditoria gravado fora do handoff, sem rastro de onde está; tratar
`produces_for_human` como sugestão.

**Critério de aceite** — o código não é dado por pronto sem:

- teste que declare `produces_for_human` e verifique que a etapa **não fecha** sem ele;
- teste que verifique que a verificação estática **não** reclama de
  `produces_for_human` sem consumidor;
- o handoff carregando a localização de cada artefato de auditoria produzido.

---

## INV-core-12: o que precisa de decisão humana é localizável sem alguém lembrar

**Regra.** Uma tarefa em `awaiting_gate` e um artefato produzido para leitura humana são
**descobríveis por comando**, sem depender de alguém ter visto passar no terminal.

**Por que vale.** A morte silenciosa tem uma segunda forma: não a tarefa que falha sem
avisar, mas a que **espera para sempre** porque ninguém soube que estava esperando. Num
sistema com N tarefas paralelas e gates que liberam o slot, a suspensão é invisível por
construção — o processo não está lá para lembrar você.

**Como é preservada.** `luna gates` lista o que aguarda decisão, com tarefa, etapa, tipo
de gate, tempo de espera e artefato anexado; `luna gate show` abre o artefato;
`luna task show` lista o que a tarefa produziu para leitura humana. Ver
[arquitetura](../architecture/overview.md).

**O que a violaria.** Uma tarefa suspensa que só aparece se você souber o identificador;
um artefato de auditoria gravado em caminho não registrado; um gate cujo motivo só exista
no log que já rolou para fora da tela.

**Critério de aceite** — o código não é dado por pronto sem:

- teste que suspenda uma tarefa em gate e verifique que ela aparece na listagem;
- teste que verifique que o artefato anexado ao gate é recuperável pelo comando, não só
  pelo filesystem.
