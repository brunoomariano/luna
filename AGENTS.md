# Instruções para agentes

Você está trabalhando **no código da Luna**, não sendo orquestrado por ela.

## Prioridade das instruções

- Este `AGENTS.md` prevalece sobre instruções globais dentro deste repositório.
- Documentação específica de uma área prevalece sobre este arquivo no mesmo assunto.
  A suíte de documentação é governada por [`docs/README.md`](docs/README.md).
- Em conflito real entre regras, pare e peça esclarecimento antes de alterar código.

## O que é este projeto

Uma máquina de estados que orquestra agentes de IA através de um fluxo de trabalho,
tirando a decisão de controle de fluxo do modelo e colocando em código. Leia
[`docs/architecture/overview.md`](docs/architecture/overview.md) antes de propor
qualquer mudança estrutural.

## Estado atual

**Motor em construção.** As decisões estão fechadas e registradas em
[`docs/ADRs/`](docs/ADRs/); o núcleo começou pela verificação estática do contrato
(`src/internal/fsm/`).

O contrato de etapa é a fonte: [`docs/architecture/stages.md`](docs/architecture/stages.md)
descreve as 14 etapas, e `DefaultFlow()` as implementa. Divergência entre os dois é bug —
`TestFluxoPadraoTemAsQuatorzeEtapas` existe para pegá-la.

## Fluxo padrão de trabalho

- Leia o contexto local antes de alterar arquivos.
- Preserve mudanças existentes no worktree. Não reverta alterações de terceiros sem
  pedido explícito.
- Faça alterações pequenas, coesas e limitadas ao escopo solicitado.
- Use os alvos do Makefile como interface principal de validação (`make help` lista).
- Ao finalizar, informe quais validações rodou e qualquer pendência relevante.

## Antes de mudar arquitetura

[`docs/ADRs/`](docs/ADRs/) registra cada decisão **com a alternativa recusada**. Se
você for propor algo que já foi recusado, traga argumento novo — o registro existe
para não reabrir discussão sem motivo. ADR é **imutável**: decisão revista vira ADR
novo, não edição do antigo.

As regras que **sempre** valem, independentemente de implementação, estão em
[`docs/invariants/`](docs/invariants/). Violar um invariante não é bug de código —
é a Luna deixando de ser a Luna.

## Estrutura

```
src/                 tudo que é aplicação
  cmd/luna/          ponto de entrada do CLI
  internal/fsm/      o motor: etapas, transições, contrato
  internal/store/    estado append-only e store de conteúdo
  internal/node/     execução de um nó (chamada do agente)
  stock/             padrões: etapas, papéis, perfis, skills
docs/                a suíte de documentação — o contrato está em docs/README.md
scripts/             utilitários de desenvolvimento (lint-docs)
bin/                 binários gerados; não versionado além do .gitkeep
config/              configuração de exemplo do usuário
```

`internal/` é barreira de import garantida pelo compilador Go: o motor não é
importável de fora do módulo. Não mova nada de `internal/` para fora sem uma
decisão registrada em ADR.

## Convenções de código

- **Idioma — o código nasce em inglês, sempre.** Nome de pacote, tipo, função,
  método, variável, constante, campo, **e nome de teste**. Sem exceção e sem
  "depois a gente traduz": renomear teste depois é diff que ninguém revisa de
  verdade.
  - **Em inglês:** tudo que é identificador, incluindo `TestStageSeparatesFields`
    e nomes de helper de teste.
  - **Em português:** documentação, comentários, docstrings, mensagens de commit,
    e o texto de mensagens de erro/log voltadas ao usuário do CLI.
  - **Slug de arquivo em `docs/`** também em inglês (`alerts-0001-suppression.md`).
  - A prosa PT-BR é transitória: a documentação e os comentários migram para
    inglês em algum momento. O código não migra porque já nasce certo.
- **Nomes específicos e pesquisáveis.** Prefira os que retornam poucas ocorrências
  em `rg`. Evite genéricos como `data`, `handler`, `Manager` quando houver opção mais
  precisa. Termos do domínio (`stage`, `role`, `handoff`, `gate`) são o nome natural
  do conceito e devem ser usados como tal.
- **Tipagem explícita.** Sem `interface{}`/`any` onde um tipo concreto serve.
- **Erros carregam o valor inválido e o esperado.** Uma mensagem que não diz o que
  chegou nem o que se queria custa uma sessão de depuração.
- **Funções idealmente entre 4 e 20 linhas;** maiores são aceitáveis quando manter a
  lógica unida for mais claro que dividir artificialmente.
- **Arquivos com menos de 500 linhas.** Divida por responsabilidade.
- **Retornos antecipados** em vez de `if` aninhado. Máximo de 2 níveis de indentação.
- **Comentários explicam o porquê**, não o quê — o código já mostra o quê. Mantenha
  comentários existentes; não os remova em refactors. Referencie ADR ou SHA quando
  uma linha existir por causa de uma decisão ou restrição externa.

## Testes

- Toda função nova tem teste; correção de bug tem teste de regressão.
- Teste o **comportamento observável**, não a implementação.
- Simule fronteira externa (processo de agente, filesystem, rede) com fake nomeado,
  não com stub inline.
- Rode pelos alvos do Makefile.

### Critério de aceite dos invariantes

Vários invariantes em [`docs/invariants/`](docs/invariants/) trazem uma seção
**Critério de aceite** — a lista de testes sem os quais aquele invariante não está
implementado, só descrito.

**Não dê por pronto um pedaço do motor cujo invariante correspondente tenha critério de
aceite não coberto.** Não é recomendação: é o que separa "o código faz" de "o código
garante". Um `reviewer` que apenas *não costuma* editar não cumpre INV-core-7; um
watchdog que existe mas nunca foi exercitado não cumpre INV-core-8.

Ao implementar, comece pelo teste que o critério descreve. Ao revisar, confira o
critério antes do diff.

## Interface Makefile

`make help` lista todos os alvos. Os que importam:

- `make bootstrap` — prepara o ambiente (mise + dependências). Idempotente.
- `make doctor` — confere o ambiente sem instalar nada.
- `make ci-check` — **só verifica**: `fmt-check`, `lint`, `lint-docs`, `cover`. É o que
  o CI remoto roda, e não escreve em nada.
- `make ci` — corrige o que dá (`fmt`) e então verifica. **Rode antes de abrir PR.**

A distinção entre os dois não é cosmética: um passo de CI que reformata o código
esconde exatamente o que deveria reprovar.
- `make lint-docs` — valida a **forma** da suíte de docs; falha o CI como lint de
  código. O contrato que ele executa está em [`docs/README.md`](docs/README.md).

Não há `up`/`down`/`logs`/`clean_db`: a Luna é um CLI sem serviços nem banco de
desenvolvimento. Target no-op seria cerimônia.

## Documentação

A suíte é governada por [`docs/README.md`](docs/README.md) — o contrato que diz
quais camadas existem, o que cada uma responde e que forma segue. **Consulte-o antes
de criar ou alterar qualquer documento**, para saber em qual camada ele entra.

| Onde | O quê |
|---|---|
| [`docs/architecture/`](docs/architecture/) | como o sistema está montado hoje |
| [`docs/glossary/`](docs/glossary/) | o que cada termo do domínio significa |
| [`docs/invariants/`](docs/invariants/) | regras que sempre valem |
| [`docs/ADRs/`](docs/ADRs/) | por que decidimos assim — imutável |
| [`docs/PRDs/`](docs/PRDs/) · [`docs/RFCs/`](docs/RFCs/) | comportamento esperado · rota técnica |
| [`docs/references.md`](docs/references.md) | de onde veio a ideia, e o que foi recusado |
| [`docs/CHANGELOG/`](docs/CHANGELOG/) | o que mudou entre versões |

O `README.md` da raiz abre pelo **problema que o projeto resolve**, nunca pela stack.
Detalhe técnico vai para `docs/`.

## Commits, PR e tags

O git-flow é normativo em [`docs/CONTRIBUTING.md`](docs/CONTRIBUTING.md) — e só lá.
Em resumo: Conventional Commits, corpo em português explicando o **porquê**.

## O que não fazer

- **Não mova o controle de fluxo para o modelo.** É a premissa do projeto inteiro.
- **Não escreva estado com `UPDATE`.** O store é append-only; o histórico é a auditoria.
- **Não crie etapa sem contrato.** Toda etapa declara o que exige e o que produz — é o
  que impede handoff incompleto.
- **Não edite um ADR aceito.** Decisão revista vira ADR novo; o antigo só muda de
  status. `make lint-docs` reprova a edição.
- **Não duplique conteúdo entre camadas de doc.** Um fato mora numa camada só; se
  precisa repetir, linke.
