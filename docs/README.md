# Documentação da Luna

Este documento é a **porta de entrada** e o **padrão** da documentação do projeto.
Define quais camadas de documentação existem, o que cada uma responde, onde mora e
que forma segue. Toda nova documentação — feita por pessoas ou por agentes — deve
respeitar este contrato.

> **Por que isto existe:** sem um acordo de forma, cada documento reinventa
> estrutura e tom, specs antigas descrevem estados já mudados e conceitos de
> domínio não têm fonte única. Este padrão é a fundação: as camadas seguintes
> (glossário, arquitetura, invariantes, registros de decisão) nascem sob a mesma
> forma.

## Como usar este guia

1. Vai **criar** um documento? Use o [critério de classificação](#critério-de-classificação)
   para descobrir em qual camada o conteúdo entra.
2. Encontrou a camada? Vá até a linha dela no [mapa de camadas](#mapa-de-camadas),
   abra a localização indicada e copie o `_template.md` daquela camada.
3. Escreva seguindo as [convenções transversais](#convenções-transversais).

---

## Mapa de camadas

Cada camada responde a **uma pergunta** e tem uma **fronteira** — o que
deliberadamente *não* mora nela. Quando um conteúdo parece caber em duas camadas,
a coluna "Não pertence" e o [critério de classificação](#critério-de-classificação)
desambiguam.

| Camada | Pergunta que responde | Localização | Envelhece? |
|---|---|---|---|
| **README do projeto** | Como eu rodo e entendo o sistema? | `../README.md` (raiz) | sim (vivo) |
| **Diretrizes de engenharia** | Como escrevo código aqui? | `../AGENTS.md` (raiz) | sim (vivo) |
| **Contribuição** | Como faço commit, PR e tag? | `CONTRIBUTING.md` | sim (vivo) |
| **Glossário** | O que esse termo significa neste domínio? | `glossary/` | sim (vivo) |
| **Arquitetura** | Como o sistema é estruturado, operado e integrado? | `architecture/` | sim (vivo) |
| **Invariantes** | Que regras sempre valem, independente da implementação? | `invariants/` | sim (vivo) |
| **PRD** | O que esta feature faz e como deveria se comportar? | `PRDs/<domínio>/` | sim (datado) |
| **RFC** | Como vou executar esta mudança (rota técnica, alternativas)? | `RFCs/` | sim (datado) |
| **Registro de decisão (ADR)** | Por que decidimos assim, naquele momento? | `ADRs/` | não (imutável) |
| **Changelog** | O que mudou entre versões? | `CHANGELOG/` | acumula |
| **Referências** | De onde veio a ideia, e o que foi recusado dela? | `references.md` | sim (vivo) |

A coluna **Envelhece?** governa a [regra de obsolescência](#regra-de-obsolescência-e-atualização):

- **vivo** — reflete o estado atual; é atualizado no lugar quando a realidade muda.
- **datado** — descreve um estado no tempo; ganha cabeçalho de status e pode ficar obsoleto.
- **imutável** — registra uma decisão de um momento; nunca é editado, só substituído.
- **acumula** — é um histórico append-only.

> **Camadas que este projeto não usa.** Não há `architecture/api/` nem
> `architecture/contracts/` porque a Luna é um CLI sem superfície HTTP nem
> mensageria. Não há `BRs/` — invariantes, PRD e glossário cobrem esse propósito.
> Se alguma dessas realidades mudar, a camada entra aqui antes de o primeiro
> arquivo ser escrito.

> **Camada extra deste projeto.** `references.md` registra a **prior art**: de onde
> cada ideia do desenho veio e o que foi recusado dela. Não é arquitetura (não
> descreve o sistema montado) nem ADR (não é decisão nossa, é leitura de terceiros).
> Num projeto que nasceu do estudo de outros orquestradores, essa procedência é
> conhecimento de primeira classe.

### Detalhe de cada camada

**README do projeto** — `../README.md`
Abre pelo **problema que a Luna resolve**, nunca pela stack. Visão geral e o caminho
rápido para rodar. Menciona que o git-flow vive em [`CONTRIBUTING.md`](CONTRIBUTING.md)
e aponta para lá.
*Não pertence:* regra de código (vai em `AGENTS.md`), detalhe técnico (vai em
`architecture/`), o fluxo de branch em si (vai em `CONTRIBUTING.md`).

**Diretrizes de engenharia** — `../AGENTS.md`
Como o código é escrito: estrutura de diretórios, convenções, testes, Makefile.
Vale para humanos e agentes; prevalece sobre instruções globais dentro do repo.
*Não pertence:* o que o sistema faz (PRD), por que uma escolha foi feita (ADR), o
fluxo de branch/merge/tag (`CONTRIBUTING.md`).

**Contribuição** — [`CONTRIBUTING.md`](CONTRIBUTING.md)
Commits (Conventional Commits, PT-BR), descrição de PR, tags de release e o
**git-flow**. É aqui — e só aqui — que o fluxo de branch/merge/tag é normativo.
*Não pertence:* convenção de código de produção (vai em `AGENTS.md`).

**Glossário** — [`glossary/`](glossary/)
Fonte única dos termos do domínio: etapa, papel, handoff, lead, gate, perfil,
contrato, nó, tarefa. Um termo, uma definição, sem ambiguidade.
*Não pertence:* como o termo é implementado (arquitetura), regra que o governa
(invariantes).

**Arquitetura** — [`architecture/`](architecture/)
Estrutura do sistema e o **porquê** dela. Descreve o *como está montado* hoje.

- [`architecture/overview.md`](architecture/overview.md) — o desenho geral: o
  problema, a forma da solução, lead híbrido, contrato de etapa, handoff, papéis,
  falha, gates, estado, extensão.
- [`architecture/stages.md`](architecture/stages.md) — as etapas padrão, com papel,
  gate, condição, `requires` e `produces`.

*Não pertence:* a decisão pontual e datada que levou a uma escolha (ADR), o passo a
passo de uma feature (PRD).

**Invariantes** — [`invariants/`](invariants/)
Regras que **sempre** valem, independentemente de implementação. São o contrato
conceitual que qualquer código deve preservar — o que a Luna deixa de ser se for
violado.
*Não pertence:* como a regra é codificada (arquitetura), por que ela foi adotada
(ADR), o que uma feature faz (PRD).

**PRD** — [`PRDs/`](PRDs/)`<domínio>/`
Especificação de produto de uma feature: problema, objetivo, comportamento
esperado, requisitos, impactos. Datado — carrega `**Status:**`. Identificado por
`<domínio>-NNNN` (slug em inglês, ex.: `fsm-0001`).
*Não pertence:* o plano de execução técnico (RFC), a decisão estrutural (ADR).

**RFC** — [`RFCs/`](RFCs/)
Planejamento de uma mudança — a rota técnica de *como* executá-la: motivação,
proposta, alternativas, rollout faseado, questões em aberto. Datado; identificado
por `rfc-NNNN`. **Linka o PRD** em vez de repetir requisitos.
*Não pertence:* os requisitos de produto (PRD), a decisão permanente (ADR).

**Registro de decisão (ADR)** — [`ADRs/`](ADRs/)
Por que uma decisão foi tomada, no contexto daquele momento, **com a alternativa
recusada**. Numerado, imutável. Ver [`ADRs/README.md`](ADRs/README.md).
*Não pertence:* o estado atual resultante da decisão (arquitetura), a definição de
um termo (glossário).

**Changelog** — [`CHANGELOG/`](CHANGELOG/)
Histórico de mudanças entre versões, em linguagem de comportamento. Acumula; não se
reescreve o passado.
*Não pertence:* a motivação de uma decisão (ADR), a spec de uma feature (PRD).

**Referências** — [`references.md`](references.md)
Prior art: os projetos e relatos que informaram o desenho, o que foi **trazido** de
cada um e o que foi **recusado**.
*Não pertence:* a nossa decisão em si (ADR — o ADR pode linkar a referência que o
motivou), a descrição do sistema (arquitetura).

---

## Critério de classificação

Quando não estiver claro onde um conteúdo entra, responda **na ordem** — a primeira
que casar é a camada:

1. **É a definição de um termo do domínio?** → **Glossário**.
2. **É uma regra que sempre vale, independente de como o código a implementa?** →
   **Invariantes**.
3. **É leitura de um projeto de terceiros — o que trouxemos ou recusamos dele?** →
   **Referências**.
4. **Estou registrando *por que* escolhemos um caminho, naquele momento, com
   alternativas descartadas?** → **ADR**.
5. **Descreve como o sistema está estruturado hoje (limites, camadas, etapas,
   fluxos)?** → **Arquitetura**.
6. **Especifica o que uma feature faz e como deve se comportar?** → **PRD**.
7. **É a rota técnica de como executar uma mudança?** → **RFC**.
8. **É como contribuir (commit, PR, tag)?** → **Contribuição**.
9. **É como rodar/entender o projeto, ou como escrever código aqui?** → **README** /
   **Diretrizes de engenharia**.

### Desambiguação entre camadas próximas

- **Invariante vs. Arquitetura:** a invariante é a regra conceitual ("o store nunca
  faz `UPDATE`"); a arquitetura é como o código a sustenta ("o estado vive em SQLite
  append-only, com transição atômica"). A regra vai em invariantes; a estrutura que
  a garante, em arquitetura.
- **Arquitetura vs. ADR:** a arquitetura descreve o **estado atual** ("o lead é
  híbrido"); o ADR registra a **decisão datada** que levou a ele ("em 2026-08-06
  escolhemos lead híbrido porque…, recusando lead puramente código e puramente
  modelo"). O ADR não é editado quando a arquitetura muda — cria-se um novo ADR que
  substitui o anterior.
- **ADR vs. Referências:** o ADR é a **nossa** decisão; a referência é a **leitura de
  outro projeto**. Um ADR pode linkar a referência que o motivou, mas o "trazido /
  recusado do SwarmForge" mora em `references.md`, não em ADR.
- **ADR vs. Invariantes:** o ADR explica o *porquê* datado; o invariante descreve a
  regra vigente que o código deve preservar. Quando uma decisão cria uma regra
  permanente, os dois se linkam em vez de duplicar.
- **PRD vs. RFC:** o PRD é o *quê* e o comportamento esperado; o RFC é a rota técnica
  de execução. Quando há os dois, o RFC linka o PRD e não repete requisitos.

---

## Convenções transversais

Valem para todas as camadas, salvo onde a camada especificar o contrário.

- **Idioma:** português do Brasil na prosa. Nomes de símbolo, path, comando,
  identificador de issue **e o slug do nome do arquivo** ficam em inglês — como o
  código (ver [`../AGENTS.md`](../AGENTS.md)).
- **Formato:** Markdown. Um `# Título` por documento.
- **Sem YAML front-matter.** Metadado de status vai em linhas em negrito no topo
  (`**Status:** …`, `**Última revisão:** …`), nunca em bloco `---`.
- **Tom:** objetivo e em linguagem de **comportamento**, não de implementação.
  Descreva o efeito observável, não a função que o produz. Frases curtas.
- **Nomes de arquivo:** `kebab-case.md` (ex.: `stage-contract.md`).
- **Diagramas:** [Mermaid](https://mermaid.js.org/) embutido quando um fluxo ou
  estrutura ficar mais claro visualmente. Prefira diagrama a parágrafo longo
  descrevendo passos.
- **Gherkin em bullets**, nunca em bloco de código: `- **Dado** …`, `- **Quando** …`,
  `- **Então** …`.
- **Templates:** cada camada com forma fixa tem um `_template.md` na sua pasta.
  Comece sempre dele — não reinvente a estrutura.
- **Fonte única:** um fato mora em uma camada só. Se precisa repetir, **referencie**
  (link relativo) em vez de copiar.

A **forma** destas convenções é validada por `make lint-docs` (parte do
`make ci-check`) — não é confiada à memória de quem escreve. Ver
[`../scripts/lint-docs.sh`](../scripts/lint-docs.sh).

---

## Regra de obsolescência e atualização

A forma como um documento lida com o tempo depende da sua coluna **Envelhece?** no
[mapa de camadas](#mapa-de-camadas).

### Documentos vivos (README, AGENTS, CONTRIBUTING, glossário, arquitetura, invariantes, referências)

Refletem o estado atual. Quando a realidade muda, **atualize no lugar** — não há
versão "antiga" a preservar. Um documento vivo desatualizado é um bug de
documentação.

### Documentos datados (PRD, RFC)

Descrevem um estado num momento e podem ficar obsoletos. Carregam, no topo, um
cabeçalho de status (o enum difere por camada):

```markdown
# PRD
**Status:** NÃO IMPLEMENTADO | IMPLEMENTADO | OBSOLETO
**Última revisão:** AAAA-MM-DD

# RFC
**Status:** RASCUNHO | EM ANDAMENTO | CONCLUÍDO | OBSOLETO
**Última revisão:** AAAA-MM-DD
```

- Ao entregar, o PRD vai de `NÃO IMPLEMENTADO` para `IMPLEMENTADO`; o RFC de `EM
  ANDAMENTO` para `CONCLUÍDO`.
- Se o comportamento deixou de valer, marque `OBSOLETO` e aponte o sucessor:
  `> ⚠️ Obsoleto desde AAAA-MM-DD. Ver: <link>`.
- Documento `OBSOLETO` **move para `archive/`** (`PRDs/<domínio>/archive/`,
  `RFCs/archive/`) — `git mv`, nunca apagar. O nome e o número não mudam; a
  numeração conta o `archive/` no mesmo escopo. Um PRD `IMPLEMENTADO` descreve
  comportamento vigente e **fica**.

### Documentos imutáveis (ADR)

Um ADR **nunca é editado** após aceito. Quando uma decisão é revista, crie um
**novo** ADR e marque o anterior como substituído:

```markdown
**Status:** Substituído por [ADR-0007](0007-new-title.md)
```

ADR não tem `archive/`: a cadeia de substituição é o valor. Detalhes em
[`ADRs/README.md`](ADRs/README.md).

---

## Índice de templates

Comece sempre pelo template da camada:

- Glossário — [`glossary/_template.md`](glossary/_template.md)
- Arquitetura — [`architecture/_template.md`](architecture/_template.md)
- Invariantes — [`invariants/_template.md`](invariants/_template.md)
- PRD — [`PRDs/_template.md`](PRDs/_template.md)
- RFC — [`RFCs/_template.md`](RFCs/_template.md)
- Registro de decisão — [`ADRs/0000-template.md`](ADRs/0000-template.md)
  (convenção em [`ADRs/README.md`](ADRs/README.md))
