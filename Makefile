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

# Enquanto não há go.mod, os alvos de código são no-op explícito em vez de
# falhar — assim o CI nasce verde e passa a valer de verdade quando o código vier.
HAS_GO := $(shell test -f go.mod && echo 1)

fmt: ## formata
ifdef HAS_GO
	@go fmt ./...
else
	@echo "(sem go.mod ainda — projeto em desenho)"
endif

lint: ## análise estática
ifdef HAS_GO
	@go vet ./...
else
	@echo "(sem go.mod ainda — projeto em desenho)"
endif

test: ## testes
ifdef HAS_GO
	@go test ./...
else
	@echo "(sem go.mod ainda — projeto em desenho)"
endif

lint-docs: ## valida a forma da suíte de docs (read-only)
	@sh scripts/lint-docs.sh

ci-check: fmt lint lint-docs test ## o mesmo que o CI remoto roda

ci: ci-check ## alias de ci-check

build: ## compila o binário
ifdef HAS_GO
	@go build -o luna ./src/cmd/luna
else
	@echo "(sem go.mod ainda — projeto em desenho)"
endif

clean: ## remove artefatos de build
	@rm -f luna
	@rm -rf dist/
