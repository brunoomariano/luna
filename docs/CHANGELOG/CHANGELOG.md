# Changelog

Formato: [Keep a Changelog](https://keepachangelog.com/pt-BR/1.1.0/).

## [Não lançado]

### Adicionado
- Desenho da arquitetura: FSM determinística com lead híbrido, contrato de etapa
  (`requires`/`produces`), handoff com snapshot endereçado por conteúdo, e papéis com
  gating de ferramentas.
- Protótipo navegável do fluxo em `prototypes/fsm-flow.html`.
- Documentação inicial: arquitetura, decisões, etapas e referências.
- Suíte de documentação em camadas, governada pelo contrato `docs/README.md`:
  glossário do núcleo, invariantes, ADRs, e as camadas de PRD e RFC formalizadas.
- `make lint-docs` valida a forma da documentação e reprova o CI como lint de código.
- `mise.toml` fixa a versão do Go que `make bootstrap` instala.

- Três decisões que só viviam na memória do projeto ou no protótipo passaram a ter
  registro: gating de ferramenta por hook que bloqueia (ADR-0018), watchdog de
  inatividade (ADR-0019) e invalidação do verde ao voltar para `build` (ADR-0020).

### Modificado
- As decisões deixaram de viver num arquivo só e viraram 20 ADRs numerados e
  imutáveis em `docs/ADRs/`, cada um com a alternativa recusada.
- As referências passaram a registrar a separação mecânico × prosa observada no
  SwarmForge, e a tese que dela decorre: enforcement no fluxo, não só no transporte.
- O código da aplicação passou a viver sob `src/`; `internal/` segue sendo a barreira
  de import garantida pelo compilador.
- `CONTRIBUTING.md` e `CHANGELOG.md` passaram para `docs/`, e o git-flow passou a ser
  normativo só em `docs/CONTRIBUTING.md`.
