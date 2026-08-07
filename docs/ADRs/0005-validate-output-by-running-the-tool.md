# ADR-0005: A saída é validada rodando a ferramenta

**Status:** Aceito
**Data:** 2026-08-06

## Contexto

A verificação de saída de uma etapa (ver
[ADR-0004](0004-stage-requires-produces-contract.md)) precisa de um critério. Há formas
baratas de checar — código de saída do processo, formato do retorno — e há a forma cara:
executar a ferramenta que prova o fato.

## Decisão

A saída é validada **rodando a ferramenta**: o teste roda, o commit resolve, o arquivo
existe. Não se confere formato, confere-se realidade.

## Alternativas consideradas

- **Código de saída do processo** — descartada porque o CLI sair com zero não significa
  que o trabalho ficou certo.
- **Validar formato** — descartada porque um JSON bem-formado pode descrever algo que
  não existe.

## Consequências

- **Positivas:** é a verificação que mais importa — pega o buraco onde ele nasce, não
  duas etapas adiante quando o sintoma já está deslocado da causa.
- **Negativas / custos:** validar custa execução real de ferramenta a cada fechamento de
  etapa.

## Referências

- Documentos relacionados: [arquitetura](../architecture/overview.md),
  [referências](../references.md)
