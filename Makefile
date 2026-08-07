# Canonical targets. `make` on its own lists what exists.
# There is no `up`/`down`/`logs`/`clean_db`: Luna is a CLI with no services and no
# development database. A no-op target is ceremony, and ceremony trains people to
# ignore the process.
.DEFAULT_GOAL := help
.PHONY: help bootstrap doctor ci ci-check test lint lint-docs fmt build clean

help: ## list the targets
	@grep -E '^[a-z_-]+:.*?## ' $(MAKEFILE_LIST) \
	  | awk 'BEGIN{FS=":.*?## "}{printf "  \033[36m%-12s\033[0m %s\n", $$1, $$2}'

bootstrap: ## prepare the development environment
	@command -v mise >/dev/null && mise install || echo "(mise missing — install from https://mise.jdx.dev)"
	@command -v go >/dev/null || { echo "go not found — install it with mise"; exit 1; }
	@go mod download 2>/dev/null || echo "(no go.mod yet)"

doctor: ## check the environment without installing anything
	@printf 'mise     '; command -v mise     >/dev/null && mise --version || echo '— missing'
	@printf 'go       '; command -v go       >/dev/null && go version || echo '— missing'
	@printf 'bd       '; command -v bd       >/dev/null && bd version || echo '— missing (Beads)'
	@printf 'sqlite3  '; command -v sqlite3  >/dev/null && sqlite3 --version || echo '— missing'

# Minimum coverage. Raise it as the engine grows; never lower it to make CI pass.
COVER_MIN ?= 80

fmt: ## format the code (writes)
	@go fmt ./...

fmt-check: ## check formatting without writing
	@out=$$(gofmt -l src/); \
	  if [ -n "$$out" ]; then echo "not formatted:"; echo "$$out"; exit 1; fi

lint: ## static analysis
	@go vet ./...

test: ## run the tests
	@go test ./...

cover: ## tests with coverage, failing below COVER_MIN
	@go test -coverprofile=coverage.out ./... >/dev/null
	@go tool cover -func=coverage.out | tail -1
	@go tool cover -func=coverage.out | awk -v min=$(COVER_MIN) '/^total:/ { \
	  gsub(/%/,"",$$3); \
	  if ($$3+0 < min) { printf "coverage %.1f%% below the %d%% minimum\n", $$3, min; exit 1 } }'

lint-docs: ## validate the shape of the docs suite (read-only)
	@sh scripts/lint-docs.sh

# ci fixes what it can and then verifies; ci-check ONLY verifies.
# The distinction is not cosmetic: ci-check is what remote CI runs, and a CI step
# that reformats the code hides exactly what it should be failing on.
ci-check: fmt-check lint lint-docs cover ## verify only — the same thing remote CI runs

ci: fmt ci-check ## fix what can be fixed, then verify. Run before opening a PR.

build: ## build the binary
	@go build -o bin/luna ./src/cmd/luna

clean: ## remove build artifacts
	@rm -f luna
	@rm -rf dist/
