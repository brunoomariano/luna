# Perguntas abertas — review que não bloqueia, e contrato que ninguém confere

Nota temporária, escrita em 2026-08-20 depois da medição da TALLY-7. Fica na
raiz e não em `docs/`, porque `AGENTS.md` proíbe um quinto arquivo de
documentação e isto não é documentação: é uma pergunta esperando resposta.
Quando as decisões forem tomadas, o que sobreviver vira `decisions.md` e este
arquivo é apagado.

## O contexto

A TALLY-7 foi a primeira execução em que o ciclo completo rodou de verdade: seis
stages, seis commits, `make ci` verde (exit 0, com shellcheck), critérios de
aceite conferidos à mão. Custou **$4,5727** em 2.914.104 tokens e 74 turnos.

O `critic` fez um trabalho genuinamente bom. Quatro defeitos reais, todos
verificados por mim rodando o código:

| defeito | comportamento |
|---|---|
| `tally.sh --avg -- 1 2 3` | imprime `0`, **exit 0** — nenhum número é contado |
| `tally.sh 010` | `8` — lido como octal |
| `tally.sh 'x=41+1'` | `42` — argumentos são avaliados, não parseados |
| a suposição do plano | `avg=100 ./tally.sh 1 2 --avg` → `102`, exit 0 |

O último é o melhor. O `plan` aceitou "flag só na primeira posição" argumentando
que um `--avg` perdido *falharia alto*. Mas o barulho vinha do `set -u` pegando
variável **não definida**, não de validação — com uma variável `avg` no ambiente,
o erro vira resposta confiante e errada. O `review` derrubou uma premissa do
plano lendo o código.

E **nada disso moveu o fluxo.** Os quatro ficaram como texto num relatório.

## O achado

O mecanismo de devolver trabalho existe e está completo:

```
stock/stages/100-review.toml   sends_back_to = "build"
internal/fsm/finding.go        ReadReport → Blocks
internal/lead/lead.go:386      readReview → fsm.ReviewFinding
```

O `ReadReport` procura achados marcados com `[BLOCKING]`, `[SHOULD-FIX]`,
`[NIT]` ou `[UNCERTAIN]`. O `Blocks` devolve o trabalho se houver um
`[BLOCKING]`.

**Nada nunca diz isso ao agente.** Nem `stock/roles/critic.toml`, nem os stages,
nem o `Brief` em `internal/node/stage.go` — conferido com `rg`, zero ocorrências.
Os dois relatórios da TALLY-7 têm **zero achados tagueados**: o `critic` escreveu
seções chamadas "Findings" em prosa, o parser não leu nada, `Blocks` devolveu
falso, o fluxo seguiu.

É a mesma forma dos outros bugs desta sessão — as duas pontas corretas e o
transporte entre elas ausente, invisível para uma suíte a 95%.

Corrigir o transporte é inequívoco e não depende de decisão. O que depende é o
que segue.

## Pergunta 1 — o que o `critic` pode bloquear?

O agente é literal: se o brief mandar tagear, ele tagueia. Então a régua importa.

Os quatro defeitos da TALLY-7 são **todos herdados do commit base** — nenhum foi
introduzido pela mudança, e o próprio `critic` disse isso sem que ninguém
pedisse.

- Se `[BLOCKING]` significar "defeito real que eu vi", o `build` reabre para
  consertar bash legado que o pedido nunca mencionou. A task pode não fechar
  nunca, e o custo sobe a cada volta.
- Se significar "defeito que **esta mudança** introduziu, ou que quebra o
  critério de aceite", a TALLY-7 teria fechado do mesmo jeito — corretamente — e
  os quatro continuariam registrados para quem quiser agir depois.

A segunda régua parece certa, mas é uma decisão sobre o que a ferramenta é: um
portão de qualidade do repositório, ou um portão da mudança em questão.

## Pergunta 2 — o contrato do artefato deve ser verificável?

Hoje há dois contratos com nomes parecidos e destinos opostos:

```
contrato do stage    (requires / produces)   →  verificado pelo motor   ✓
contrato do artefato (o documento do plan)   →  verificado por ninguém  ✗
```

O gate faz um modelo julgar se o documento é **coerente**. Ninguém depois
pergunta se o que foi construído o **cumpre**.

Isso apareceu concretamente: o contrato ajustado da TALLY-7 exigia, com todas as
letras, um teste fixando a decisão 3 (`./tally.sh 1 --avg 2` não calcula média).
O teste não foi escrito. O `build` fechou verde — corretamente, pelas regras que
existem, porque o contrato do *stage* pedia `code` + `tests_green` e ambos foram
entregues. E o `verify` chegou a afirmar que "as três decisões estão fixadas por
teste", o que é **falso**.

Três posturas possíveis:

- **(a) Aceitar.** O contrato é um documento para humanos, e o `review` já lê o
  código. Custo zero, e a lacuna fica registrada em vez de fechada.
- **(b) Dar o contrato ao `verify`.** Ele passa a receber o documento no brief,
  com um critério explícito: "o construído cumpre o contrato". Barato, e move a
  verificação para onde já existe um leitor.
- **(c) O contrato declara comandos e o motor os roda.** Muito mais forte, e
  muito mais caro de escrever — cada contrato vira parte executável.

## Pergunta 3 — quem escreve o teste que o contrato exige?

Caso concreto acima. É consequência da pergunta 1 — um `[BLOCKING]` do `critic`
resolveria — ou precisa de garantia mecânica própria?

Vale notar que eu quase cometi o mesmo erro ao redigir o ajuste: a primeira
versão fixava a decisão num cenário `S10` que não existia. Fui conferir, os
cenários iam só até S9, e eu estaria criando exatamente o defeito que estava
corrigindo. Uma obrigação que aponta para o vazio é fácil de escrever sem
perceber.

## O que já foi corrigido nesta sessão, para contexto

Nenhum destes está em aberto — estão aqui só para mostrar de onde vieram as
perguntas.

| commit | o que era |
|---|---|
| `fef5e51` | agente não enxergava git no sandbox: 5 stages, $3,33, zero commits |
| `f78c083` | critério de gate impossível de responder — faltava o enunciado |
| `d2ac94a` | raciocínio do lead era descartado, gate ficava sem explicação |
| `9e755ed` | `--dry-run` gravava `passed` sobre task real; `work` não abria stage |
| `cbfaa62` | task não conseguia pousar no nome que anunciava |
