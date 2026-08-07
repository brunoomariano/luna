# ADR-0018: O gating de ferramenta é hook que bloqueia, não instrução ao agente

**Status:** Aceito
**Data:** 2026-08-06

## Contexto

Cada papel declara `tools_allow` e `tools_deny` (ver
[ADR-0017](0017-defaults-plus-customization-everywhere.md)). Falta decidir **como** essa
declaração vira restrição de verdade.

O estudo do SwarmForge (ver [referências](../references.md)) mostra a separação que
importa: lá, o **transporte** tem enforcement mecânico — o script recusa mensagem
malformada com exit 2 — enquanto a **política de trabalho** é só prosa no prompt, sem
enforcement nenhum. Frases como *"trabalhe só no seu worktree"* ou *"não mande `note` sem
autorização"* dependem inteiramente da aderência do modelo.

É a mesma classe de falha que motiva o projeto inteiro (ver
[ADR-0001](0001-flow-control-out-of-model.md)): instrução em prosa é sugestão. Um
`reviewer` instruído a não editar arquivos vai quase sempre obedecer — e o "quase" é o
que custa, porque quem escreve não revisar é uma separação que só vale se for garantida
(ver [invariantes](../invariants/core.md), INV-core-7).

## Decisão

O `tools_deny` do papel é aplicado por **hook `PreToolUse` que bloqueia a chamada** antes
de ela executar. A FSM restringe o toolset ao entrar na etapa; o agente não recebe a
ferramenta proibida como algo que ele deveria evitar usar — ele simplesmente não
consegue usá-la.

O arquivo do papel tem um consumidor duplo: a FSM lê os metadados (`tools_allow`,
`tools_deny`) para o gating mecânico, e o agente lê a prosa (`owns`, `not_owns`) para o
julgamento. Uma fonte, dois usos.

Este é um dos **três mecanismos de força** do desenho, e o único que é mecânico:

1. hooks `PreToolUse` que bloqueiam ferramenta fora da etapa — **mecânico**;
2. reinjeção da regra a cada turno (`Re-read your role and constitution.`) — prosa;
3. watchdog de inatividade (ver [ADR-0019](0019-inactivity-watchdog.md)) — mecânico, mas
   detecta em vez de impedir.

## Alternativas consideradas

- **Instruir o papel em prosa e confiar na aderência** — descartada porque é exatamente
  a falha que o projeto existe para corrigir. Sob compactação prolongada os agentes
  perdem a identidade de papel; uma regra que só existe no prompt some junto.
- **Filtrar a saída depois** (deixar o agente editar e reverter o que não podia) —
  descartada porque o efeito colateral já aconteceu quando a detecção roda, e porque
  transforma uma restrição clara numa correção posterior frágil.

## Consequências

- **Positivas:** a separação por negação deixa de depender de boa vontade do modelo.
  Um `reviewer` sem `Edit` não é um `reviewer` orientado a não editar — é um que não
  edita.
- **Negativas / custos:** amarra a Luna à superfície de hooks de cada harness. Claude,
  Codex e OpenCode expõem isso de formas diferentes, ou não expõem.
- **Impactos:** cada harness suportado precisa de um adaptador de gating. Onde o harness
  não oferece bloqueio pré-execução, o mecanismo degrada para prosa — e essa degradação
  precisa ser visível, não silenciosa.

## Questão em aberto

**Como o `tools_deny` vira gating real em cada harness** é o item mais concreto da lista
de pendências do desenho, e só se resolve com código rodando contra cada CLI. Este ADR
fixa o *princípio* (bloqueio mecânico, não instrução); o mecanismo por harness é
implementação a decidir.

## Referências

- Documentos relacionados: [arquitetura](../architecture/overview.md),
  [invariantes](../invariants/core.md), [referências](../references.md)
