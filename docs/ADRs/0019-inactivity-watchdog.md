# ADR-0019: Watchdog de inatividade vigia o trabalho, não a janela

**Status:** Aceito
**Data:** 2026-08-06

## Contexto

A política de falha (ver [ADR-0011](0011-failure-retry-rollback-or-block.md)) cobre o nó
que **falha**: retry até 2, volta de etapa, ou bloqueio com aviso. Ela não cobre o nó que
simplesmente **para** — não retorna erro, não retorna sucesso, não retorna nada.

O estudo do SwarmForge (ver [referências](../references.md)) identifica isso como o buraco
mais visível daquele desenho: **zero modelo de liveness**. Nada lá detecta agente travado,
agente que esqueceu de repassar, ou cadeia rompida. O `swarm-window-watchdog` que existe
vigia **janelas de terminal**, não trabalho — uma janela viva com um agente parado dentro
passa como saudável.

O resumo do achado: *uma frota que para de conversar, para em silêncio.*

Isso colide com a regra de que nenhuma falha é silenciosa (ver
[invariantes](../invariants/core.md), INV-core-8). Num sistema cujo propósito é rodar
desassistido — o perfil `noturno` não tem nenhum gate humano (ver
[ADR-0013](0013-named-gate-profiles-per-task.md)) — a falha que ninguém vê é mais cara que
a falha que interrompe.

## Decisão

A Luna tem um **watchdog de inatividade** que vigia o progresso do trabalho: uma tarefa
que não produz transição, saída de ferramenta ou sinal de vida dentro de um limite é
tratada como travada, e entra na mesma máquina de decisão da falha — o modelo decide entre
retomar ou bloquear com aviso (ver [ADR-0011](0011-failure-retry-rollback-or-block.md)).

O que se vigia é o **trabalho**, não o processo nem a janela. Um processo vivo que não
progride é exatamente o caso que o watchdog existe para pegar.

## Alternativas consideradas

- **Vigiar o processo/janela do agente** (o que o SwarmForge faz) — descartada porque é o
  sinal errado: o processo continua vivo enquanto o trabalho está parado, que é
  precisamente o modo de falha a detectar.
- **Confiar no timeout do harness** — descartada porque cada CLI tem semântica própria de
  timeout, e nenhum deles conhece a noção de "etapa que devia ter produzido algo". O
  timeout do harness pega o processo pendurado, não a tarefa sem progresso.
- **Não ter watchdog e aceitar a intervenção humana** — descartada porque anula o perfil
  `noturno`. Se toda execução desassistida precisa de alguém conferindo se travou, ela não
  é desassistida.

## Consequências

- **Positivas:** fecha o modo de falha "cadeia rompida em silêncio", que é um dos quatro
  que motivam o projeto (ver [ADR-0001](0001-flow-control-out-of-model.md)). Torna o perfil
  `noturno` defensável.
- **Negativas / custos:** exige escolher um limite de inatividade, e limite mal calibrado
  gera falso positivo — matar uma etapa legitimamente lenta é pior que esperar. O limite
  provavelmente precisa variar por etapa.
- **Impactos:** a execução do nó precisa emitir sinal de progresso observável, o que
  restringe as opções de "quem executa o nó" — decisão ainda em aberto.

## Referências

- Documentos relacionados: [arquitetura](../architecture/overview.md),
  [invariantes](../invariants/core.md), [referências](../references.md)
