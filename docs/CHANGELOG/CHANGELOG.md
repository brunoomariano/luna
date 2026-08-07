# Changelog

Formato: [Keep a Changelog](https://keepachangelog.com/pt-BR/1.1.0/).

## [Não lançado]

### Adicionado
- Desenho da arquitetura: FSM determinística com lead híbrido, contrato de etapa
  (`requires`/`produces`), handoff com snapshot endereçado por conteúdo, e papéis com
  gating de ferramentas.
- Documentação inicial: arquitetura, decisões, etapas e referências.
- Suíte de documentação em camadas, governada pelo contrato `docs/README.md`:
  glossário do núcleo, invariantes, ADRs, e as camadas de PRD e RFC formalizadas.
- `make lint-docs` valida a forma da documentação e reprova o CI como lint de código.
- `mise.toml` fixa a versão do Go que `make bootstrap` instala.
- O motor começou: a verificação estática do contrato (`AuditContract`) detecta fluxo
  quebrado no papel — uma etapa que exige o que nenhuma anterior produz — antes de
  qualquer agente ser chamado. O fluxo padrão das 14 etapas é verificado no CI.
- `make cover` reprova cobertura abaixo do mínimo, e entra no `ci-check`.

- Três decisões que só viviam na memória do projeto ou no protótipo passaram a ter
  registro: gating de ferramenta por hook que bloqueia (ADR-0018), watchdog de
  inatividade (ADR-0019) e invalidação do verde ao voltar para `build` (ADR-0020).

- O contrato de etapa passou a distinguir o que o fluxo consome do que só uma pessoa
  lê (ADR-0021), e um gate passou a poder carregar o artefato que o humano revisa,
  ajusta ou recusa (ADR-0022).
- O loop de convergência ganhou três tetos contados separadamente (ADR-0023).
- Invariantes passaram a trazer **critério de aceite**: a lista de testes sem os quais
  a regra está descrita mas não implementada.

### Modificado
- As decisões deixaram de viver num arquivo só e viraram 23 ADRs numerados e
  imutáveis em `docs/ADRs/`, cada um com a alternativa recusada.
- A política de falha deixou de ter "voltar à etapa anterior" como saída: o único
  retorno a uma etapa anterior é o do achado de revisão, que invalida o verde ao
  voltar. Duas portas para o mesmo lugar significaria uma delas esquecendo de
  invalidar.
- As referências passaram a registrar a separação mecânico × prosa observada no
  SwarmForge, e a tese que dela decorre: enforcement no fluxo, não só no transporte.
- O código da aplicação passou a viver sob `src/`; `internal/` segue sendo a barreira
  de import garantida pelo compilador.
- `CONTRIBUTING.md` e `CHANGELOG.md` passaram para `docs/`, e o git-flow passou a ser
  normativo só em `docs/CONTRIBUTING.md`.
