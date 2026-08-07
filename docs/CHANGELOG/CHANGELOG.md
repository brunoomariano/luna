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

### Modificado
- As decisões deixaram de viver num arquivo só e viraram 17 ADRs numerados e
  imutáveis em `docs/ADRs/`, cada um com a alternativa recusada.
- O código da aplicação passou a viver sob `src/`; `internal/` segue sendo a barreira
  de import garantida pelo compilador.
- `CONTRIBUTING.md` e `CHANGELOG.md` passaram para `docs/`, e o git-flow passou a ser
  normativo só em `docs/CONTRIBUTING.md`.
