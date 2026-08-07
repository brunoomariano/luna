# ADR-0010: SQLite append-only + store endereçado por conteúdo

**Status:** Aceito
**Data:** 2026-08-06

## Contexto

O estado da FSM precisa sobreviver a reinício e servir de auditoria da tarefa. O sistema
de referência usa arquivos como estado — a localização do arquivo é o estado, e toda
transição é um rename. É elegante e inspecionável, mas o nosso estado tem relações e
consultas.

## Decisão

O estado da FSM vive em **SQLite append-only**, sem `UPDATE`: o histórico é a auditoria.
Transições são atômicas — matar o processo e religar reconstrói o estado exato, porque
ele nunca esteve só em memória. Os snapshots de handoff vivem num **store endereçado por
conteúdo**.

## Alternativas consideradas

- **Arquivos como estado** (o que o sistema de referência faz) — elegante e
  inspecionável, mas descartada porque fica caro quando o estado tem relações e
  consultas.

## Consequências

- **Positivas:** reinício seguro por transição atômica; o log de transições é auditoria
  completa, sem esforço extra.
- **Impactos:** nenhum caminho de escrita pode usar `UPDATE` — é regra permanente do
  store.

## Referências

- Documentos relacionados: [arquitetura](../architecture/overview.md),
  [referências](../references.md)
