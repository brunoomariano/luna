# Instruções para agentes

Você está trabalhando **no código da Luna**, não sendo orquestrado por ela.

## O que é este projeto

Uma máquina de estados que orquestra agentes de IA através de um fluxo de trabalho,
tirando a decisão de controle de fluxo do modelo e colocando em código. Leia
[`docs/architecture.md`](docs/architecture.md) antes de propor qualquer mudança
estrutural.

## Estado atual

**Em desenho.** As decisões estão fechadas e registradas; o código ainda não começou. Se
você for escrever código aqui, comece por
[`prototypes/fsm-flow.html`](prototypes/fsm-flow.html) — o módulo `LunaFSM` dentro dele é
puro e descreve o comportamento pretendido do núcleo.

## Antes de mudar arquitetura

[`docs/decisions.md`](docs/decisions.md) registra cada decisão **com a alternativa
recusada**. Se você for propor algo que já foi recusado, traga argumento novo — o
documento existe para não reabrir discussão sem motivo.

## Convenções

- **Idioma:** documentação e mensagens de commit em português; nomes de código, tipos e
  identificadores em inglês.
- **Commits:** Conventional Commits. O corpo explica o **porquê**, não o quê — o diff já
  mostra o quê.
- **Documentação:** o `README.md` abre pelo problema que o projeto resolve, nunca pela
  stack. Detalhe técnico vai para `docs/`.

## Estrutura

```
cmd/luna/        ponto de entrada do CLI
internal/fsm/    o motor: etapas, transições, contrato
internal/store/  estado append-only e store de conteúdo
internal/node/   execução de um nó (chamada do agente)
stock/           padrões: etapas, papéis, perfis, skills
prototypes/      protótipos descartáveis; não são código de produção
docs/            arquitetura, decisões, referências
```

## O que não fazer

- **Não mova o controle de fluxo para o modelo.** É a premissa do projeto inteiro.
- **Não escreva estado com `UPDATE`.** O store é append-only; o histórico é a auditoria.
- **Não promova protótipo a produção.** O que está em `prototypes/` nasceu sem teste e
  sem tratamento de erro. Reescreva ao integrar.
- **Não crie etapa sem contrato.** Toda etapa declara o que exige e o que produz — é o
  que impede handoff incompleto.
