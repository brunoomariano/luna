# ADR-0001: O controle de fluxo sai do modelo

**Status:** Aceito
**Data:** 2026-08-06

## Contexto

Um fluxo de trabalho escrito em prosa é uma **sugestão** para o modelo, não uma
garantia. O modelo segue quase sempre — e é o "quase" que custa caro. Quatro modos de
falha foram observados em execução real:

- **reinterpretação da instrução** — um agente instruído a *rodar* um comando decidiu
  que rodar significava *imprimi-lo*;
- **loop abandonado** — repete a mesma coisa 100 vezes e na 101ª faz outra coisa, ou
  simplesmente para;
- **erosão de papel** — numa sessão longa com compactação, o revisor começa a
  implementar e o implementador a revisar;
- **cadeia rompida em silêncio** — o agente decide que não vale passar adiante, e
  ninguém nota.

## Decisão

A máquina de estados decide a próxima etapa; o modelo trabalha dentro dela. O controle
de fluxo é código, não prosa.

## Alternativas consideradas

- **Manter o fluxo em documentação e confiar na aderência do modelo** — descartada
  porque é o estado atual do problema, e falha nos quatro modos conhecidos
  (reinterpretação, loop abandonado, erosão de papel, cadeia rompida).

## Consequências

- **Positivas:** o fluxo passa a ser garantia, não sugestão. Os quatro modos de falha
  deixam de depender da boa vontade do modelo.
- **Impactos:** é a premissa do projeto inteiro — toda decisão posterior é medida contra
  ela.

## Referências

- Documentos relacionados: [arquitetura](../architecture/overview.md),
  [referências](../references.md)
