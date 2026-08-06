# Contribuindo

## Antes de abrir uma issue ou PR

Leia [`docs/decisions.md`](docs/decisions.md). Cada decisão está registrada com a
alternativa que foi recusada e o motivo. Propostas que reabrem uma decisão sem
argumento novo serão fechadas com um ponteiro para a entrada correspondente.

## Estado do projeto

Em desenho. A arquitetura está fechada; o código não começou. Contribuições mais úteis
agora são de **crítica ao desenho** — especialmente se você já operou uma frota de
agentes e viu um modo de falha que o desenho não cobre.

## Fluxo

1. Abra uma issue descrevendo o problema antes de escrever código.
2. Trabalhe numa branch a partir de `master`.
3. Conventional Commits; o corpo explica o porquê.
4. `make ci` verde antes de abrir o PR.

## Convenções

Documentação e commits em português; código em inglês. O `README.md` abre pelo problema,
não pela stack.
