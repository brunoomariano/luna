# Arquitetura

## O problema

Um fluxo de trabalho escrito em prosa é uma **sugestão** para o modelo, não uma
garantia. Ele segue quase sempre — e é o "quase" que custa caro:

- **Reinterpreta a instrução.** Um agente instruído a *rodar* um comando decidiu que
  rodar significava *imprimi-lo*.
- **Abandona o loop.** Repete a mesma coisa 100 vezes e na 101ª faz outra coisa, ou
  simplesmente para.
- **Perde o papel.** Numa sessão longa com compactação, o revisor começa a
  implementar e o implementador a revisar.
- **Rompe a cadeia em silêncio.** Decide que não vale passar adiante, e ninguém nota.

Todos observados em execução real (ver [`references.md`](../references.md)).

## A forma da solução

```
  origem da tarefa (qualquer)          Beads
  ┌──────────────────────┐        ┌──────────────┐
  │ luna task new        │───────▶│ grafo de     │
  │ luna import <fonte>  │        │ dependências │
  └──────────────────────┘        └──────┬───────┘
                                         │ o que está livre
                     ┌───────────────────┴────────────────┐
                     ▼                   ▼                ▼
                 lead(A)             lead(B)          lead(C)      ← uma goroutine
                     │                                              por tarefa
        ┌────────────┴─────────────────────────────┐
        │  FSM da tarefa A                         │
        │                                          │
        │  etapa → papel → skill                   │
        │    ├ verifica o que a etapa EXIGE        │
        │    ├ chama o agente (contexto novo)      │
        │    ├ valida o que a etapa PRODUZIU       │
        │    └ grava o handoff e transiciona       │
        └──────────────────────────────────────────┘
```

**O paralelismo é entre tarefas, não dentro delas.** Cada tarefa tem seu lead e sua
worktree; dentro dela as etapas são sequenciais.

## O lead é híbrido

No caminho feliz o lead é **código**: decide a etapa, chama o agente, valida, grava.
Custo zero de token, comportamento determinístico.

Quando algo sai do trilho — o nó falhou, a saída não validou, uma revisão achou
problema — **um modelo decide o que fazer**: tentar de novo, voltar uma etapa, abrir um
gate ou bloquear.

Essa divisão importa por um motivo concreto. Numa execução real do sistema que inspirou
este desenho, a máquina de estados tinha um bug e mandou o lead revisar um documento já
revisado. **O lead recusou e escalou ao humano** — ele fora instruído a obedecer a FSM,
e ainda assim reconheceu que a instrução não fazia sentido. A camada determinística dá o
esqueleto; a camada de julgamento pega o erro do esqueleto. As duas se protegem.

## O contrato de etapa

Cada etapa declara o que **exige** e o que **produz**:

```toml
[stage.build]
role     = "implementer"
skill    = "lsh-code-cycle:build"
requires = ["scenarios", "approach", "worktree"]
produces = ["code", "tests_green"]
```

Isso sustenta três verificações:

**1. Estática, antes de rodar.** Percorrendo as etapas em ordem, algum `requires` não é
produzido por nenhuma etapa anterior? Se sim, o fluxo está quebrado no papel — e isso é
detectável sem executar nada.

**2. Na entrada.** A FSM não chama o agente de uma etapa cujo `requires` não está no
contexto. Sem isso, o agente começaria cego e a falha pareceria burrice do modelo.

**3. Na saída.** A etapa não fecha sem entregar o `produces` declarado, e a entrega é
verificada **rodando a ferramenta** — o arquivo existe, o teste passa, o commit resolve.
Não se confere formato, confere-se realidade.

A terceira é a que mais importa: ela pega o buraco **onde ele nasce**, não duas etapas
adiante quando o sintoma já está deslocado da causa.

## Contexto novo a cada etapa

Cada etapa começa com o contexto limpo, mesmo quando o papel é o mesmo. Ataca a erosão
de papel direto — não há sessão longa para degradar.

**A consequência é estrutural:** com contexto novo, o handoff é a **única** ponte entre
etapas. Se algo necessário não estiver nele, o agente começa cego. Por isso o contrato
acima não é burocracia — é o que sustenta a decisão de zerar o contexto.

Todo payload de handoff é prefixado com uma instrução para reler o papel e as regras.
Contexto limpo e regra reinjetada são duas defesas pelo mesmo flanco.

## O handoff

Registrado a **cada** transição de etapa, mesmo quando o papel não muda. O handoff não é
"passar para outro agente" — é a transição registrada. O log de handoffs é a auditoria
completa da tarefa.

O que ele carrega:

- **ponteiros** — identificador da tarefa, etapa de origem, artefatos produzidos,
  decisões de gate anteriores;
- **snapshot endereçado por conteúdo** — o hash do que existia no momento do handoff,
  para que o receptor veja exatamente o que o emissor viu, mesmo se a worktree mudou
  depois.

O que ele **não** carrega: resumo em prosa do que a etapa anterior fez. O receptor lê o
estado real. Ninguém interpreta para ele.

O payload é **gerado pelo sistema**, não escrito pelo agente. O agente preenche campos
estruturados; o corpo entregue é sintetizado. Isso elimina a degradação por reescrita
sucessiva ao longo da cadeia.

## Papéis

Um papel pode cobrir várias etapas. Cada um declara o que possui, o que **não** possui,
e as ferramentas a que tem acesso:

```toml
# src/stock/roles/reviewer.toml
stages      = ["code-review"]
tools_allow = ["Read", "Grep", "Bash"]
tools_deny  = ["Edit", "Write"]
owns        = "..."
not_owns    = "..."
```

O `not_owns` é o mecanismo de especialização, e a separação por negação é deliberada:
quem escreve não revisa. Parte da separação também é econômica — testes de mutação são
caros, então só um papel os roda.

O `tools_allow`/`tools_deny` é o gating mecânico: a FSM restringe as ferramentas antes
de o agente começar. Um arquivo, dois consumidores — a FSM lê os metadados, o agente lê
a prosa.

## Falha

```
nó falha
   ↓
modelo avalia
   ├── tentar de novo (até 2), com o erro no contexto
   ├── voltar à etapa anterior
   └── bloquear + avisar o humano
```

Sem retry infinito: é exatamente o loop que não converge e queima tokens. E sem morte
silenciosa: toda tarefa bloqueada avisa.

## Gates

Um gate para a tarefa e espera decisão humana. Ele **não** segura um processo vivo: após
uma espera curta no terminal, a tarefa suspende e libera o slot. Outra tarefa usa o
recurso enquanto você decide; `luna gate approve` retoma do ponto exato.

Quais gates param é decidido por **perfil**, escolhido por tarefa:

| Perfil | Comportamento |
|---|---|
| `interativo` | todos os gates esperam humano |
| `turbo` | só a escrita (commit) espera |
| `noturno` | nada espera |

## Estado

Duas camadas, com responsabilidades distintas:

- **Beads** governa a ordem **entre** tarefas — dependências, o que está livre para
  começar, reivindicação atômica.
- **A FSM** governa as etapas **dentro** de uma tarefa.

O estado da FSM vive em SQLite **append-only**: sem `UPDATE`, o histórico é a auditoria.
Transições são atômicas — matar o processo e religar reconstrói o estado exato, porque
ele nunca esteve só em memória.

**A FSM não conhece rastreador de issues.** Plane, Jira, GitHub ou nada: a tarefa entra
por `luna task new` ou por um adaptador de importação, que é um comando separado do
núcleo.

## Extensão

Tudo que define comportamento tem versão padrão e versão do usuário:

| O quê | Padrão | Extensão |
|---|---|---|
| Etapas | as de `src/stock/stages/` | desabilitar, editar, criar |
| Papéis | os de `src/stock/roles/` | próprios |
| Perfis de gate | três | próprios |
| Loops | um | declarar outros, com regras |
| Skills | as da Luna, instaladas junto | apontar um diretório próprio |

As skills merecem nota: há as **da Luna** (instaladas no harness, explicam a estrutura
ao agente), as **do bundle do usuário** (apontadas em configuração) e as
**transversais do desenvolvedor** (que servem dentro e fora da Luna). Um comando de
indexação cataloga o que existe.
