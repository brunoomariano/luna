# Alvos canônicos. `make` sozinho lista o que existe.
# Não há `up`/`down`/`logs`/`clean_db`: a Luna é um CLI sem serviços nem banco de
# desenvolvimento. Target no-op é cerimônia, e cerimônia treina a ignorar o processo.
.DEFAULT_GOAL := help
.PHONY: help bootstrap doctor ci ci-check test lint lint-docs fmt build clean

help: ## lista os alvos
	@grep -E '^[a-z_-]+:.*?## ' $(MAKEFILE_LIST) \
	  | awk 'BEGIN{FS=":.*?## "}{printf "  \033[36m%-12s\033[0m %s\n", $$1, $$2}'

bootstrap: ## prepara o ambiente de desenvolvimento
	@command -v mise >/dev/null && mise install || echo "(mise ausente — instale de https://mise.jdx.dev)"
	@command -v go >/dev/null || { echo "go não encontrado — instale com mise"; exit 1; }
	@go mod download 2>/dev/null || echo "(sem go.mod ainda — projeto em desenho)"

doctor: ## confere o ambiente sem instalar nada
	@printf 'mise     '; command -v mise     >/dev/null && mise --version || echo '— ausente'
	@printf 'go       '; command -v go       >/dev/null && go version || echo '— ausente'
	@printf 'bd       '; command -v bd       >/dev/null && bd version || echo '— ausente (Beads)'
	@printf 'sqlite3  '; command -v sqlite3  >/dev/null && sqlite3 --version || echo '— ausente'

# Cobertura mínima. Sobe conforme o motor cresce; não baixe para fazer passar.
COVER_MIN ?= 80

fmt: ## formata
	@go fmt ./...

lint: ## análise estática
	@go vet ./...

test: ## testes
	@go test ./...

cover: ## testes com cobertura, falhando abaixo de COVER_MIN
	@go test -coverprofile=coverage.out ./... >/dev/null
	@go tool cover -func=coverage.out | tail -1
	@go tool cover -func=coverage.out | awk -v min=$(COVER_MIN) '/^total:/ { \
	  gsub(/%/,"",$$3); \
	  if ($$3+0 < min) { printf "cobertura %.1f%% abaixo do mínimo %d%%\n", $$3, min; exit 1 } }'

lint-docs: ## valida a forma da suíte de docs (read-only)
	@sh scripts/lint-docs.sh

ci-check: fmt lint lint-docs cover ## o mesmo que o CI remoto roda

ci: ci-check ## alias de ci-check

build: ## compila o binário
	@go build -o bin/luna ./src/cmd/luna

clean: ## remove artefatos de build
	@rm -f luna
	@rm -rf dist/
