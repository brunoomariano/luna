# Decisões

Cada entrada registra a escolha, o motivo e **a alternativa recusada** — para que
ninguém reabra a discussão sem argumento novo.

Data das decisões: 2026-08-06.

---

## Fundamentais

### O controle de fluxo sai do modelo

A máquina de estados decide a próxima etapa; o modelo trabalha dentro dela. Um fluxo
descrito em prosa é sugestão, não garantia.

**Recusado:** manter o fluxo em documentação e confiar na aderência do modelo. É o
estado atual do problema, e falha nos quatro modos conhecidos (reinterpretação, loop
abandonado, erosão de papel, cadeia rompida).

### O lead é híbrido, não puramente determinístico

Código no caminho feliz; modelo quando algo sai do trilho.

**Recusado — lead puramente código:** perde o discernimento que pega o erro da própria
FSM. Existe caso real de um lead que recusou uma instrução sem sentido e escalou.

**Recusado — lead puramente modelo:** custo por tarefa e volta a ser não-determinístico
justamente onde precisamos de garantia.

### Paralelismo entre tarefas, não dentro

Cada tarefa tem um lead e uma worktree; as etapas dentro dela são sequenciais.

**Recusado — agentes concorrentes por papel dentro da mesma tarefa:** exige fila por
papel e transporte de mensagens entre agentes vivos. Complexidade que só se paga quando
o gargalo é a tarefa individual — não é o nosso caso.

---

## Contrato e handoff

### Cada etapa declara `requires` e `produces`

Sustenta verificação estática (antes de rodar), de entrada (não chama sem insumo) e de
saída (não fecha sem entregar).

**Recusado:** contexto livre acumulado. Sem contrato não há como distinguir "o modelo
errou" de "o modelo não recebeu o que precisava".

### A saída é validada rodando a ferramenta

O teste roda, o commit resolve, o arquivo existe.

**Recusado — código de saída do processo:** o CLI sair com zero não significa que o
trabalho ficou certo.
**Recusado — validar formato:** um JSON bem-formado pode descrever algo que não existe.

### Contexto novo a cada etapa

Mesmo quando o papel é o mesmo.

**Recusado — manter o processo vivo entre etapas:** mais barato e preserva contexto, mas
é o vetor da erosão de papel. Contexto longo degrada aderência.

**Consequência aceita:** o handoff vira a única ponte entre etapas, e por isso o
contrato acima deixa de ser opcional.

### O handoff carrega ponteiros e snapshot, não resumo

Snapshot endereçado por conteúdo dá a imutabilidade que um commit daria.

**Recusado — resumo em prosa do que a etapa fez:** reintroduz interpretação na cadeia,
que é a degradação que se quer eliminar.
**Recusado — git como canal:** funciona (é o que o sistema de referência faz), mas
amarra o transporte ao versionamento.

### O payload é gerado pelo sistema

O agente preenche campos; o corpo é sintetizado.

**Recusado:** deixar o agente redigir a mensagem. Cada salto reescreve, e a mensagem
degrada ao longo da cadeia.

---

## Stack

### Go

Binário único, sem runtime na máquina alvo; concorrência nativa para watchdog e N leads.

**Recusado — Python:** iteração mais rápida, mas exige runtime e ambiente por máquina
num projeto que quer ser instalável em um comando.
**Recusado — Rust:** garantias mais fortes e alinhado às ferramentas vizinhas, mas caro
demais para uma fase em que o desenho ainda muda.

### SQLite append-only + store endereçado por conteúdo

Sem `UPDATE`: o histórico é a auditoria. Transição atômica dá segurança a reinício.

**Recusado — arquivos como estado** (o que o sistema de referência faz): elegante e
inspecionável, mas fica caro quando o estado tem relações e consultas.

---

## Fluxo e operação

### Falha: tentar até 2, voltar etapa, ou bloquear com aviso

**Recusado — retry infinito com backoff:** é o loop que não converge e queima tokens.
**Recusado — escalar na primeira falha:** transforma o humano no loop de correção de
coisas que uma tentativa resolveria.

### Gate suspende e libera o slot

Espera curta no terminal; sem resposta, suspende. Outra tarefa usa o recurso.

**Recusado — agente vivo esperando:** com N tarefas em paralelo seriam N processos
parados, com contexto envelhecendo.

### Perfis de gate nomeados, escolhidos por tarefa

**Recusado — perfil por tipo de tarefa:** o tipo não prediz o risco; um bug crítico pode
merecer mais gate que uma feature trivial.
**Recusado — perfil por repositório:** não distingue tarefa arriscada de trivial dentro
do mesmo código.

### Etapas condicionais

Etapas de revisão pesada não rodam em tarefa trivial.

**Recusado — rodar tudo sempre:** teste de mutação numa mudança de uma linha é cerimônia,
e cerimônia treina o humano a ignorar o processo.

### O núcleo não conhece rastreador de issues

Adaptadores de importação são comandos separados.

**Recusado — integração nativa com Plane:** amarraria o sistema a uma ferramenta
específica. A tarefa pode vir de qualquer lugar, ou de lugar nenhum.

### CLI primeiro

**Recusado — interface visual desde o início:** muito trabalho antes de a FSM provar
valor. Entra quando o fluxo estabilizar.

---

## Extensão

### Padrão + customização, em tudo

Etapas, papéis, perfis, loops e skills têm versão padrão e versão do usuário.

**Recusado — fluxo fixo:** o desenho padrão é o nosso, e outra pessoa terá outro. Sem
extensão, a ferramenta serve a um usuário só.
