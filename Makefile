# Canonical targets. `make` on its own lists what exists.
# There is no `up`/`down`/`logs`/`clean_db`: Luna is a CLI with no services and no
# development database. A no-op target is ceremony, and ceremony trains people to
# ignore the process.
.DEFAULT_GOAL := help
.PHONY: help bootstrap doctor ci ci-check test lint lint-docs lint-language \
        fmt fmt-check cover race vuln mod deadcode crap cyclo mutation build clean \
        install uninstall

help: ## list the targets
	@grep -E '^[a-z_-]+:.*?## ' $(MAKEFILE_LIST) \
	  | awk 'BEGIN{FS=":.*?## "}{printf "  \033[36m%-14s\033[0m %s\n", $$1, $$2}'

bootstrap: ## prepare the development environment
	@command -v mise >/dev/null && mise install || echo "(mise missing — install from https://mise.jdx.dev)"
	@command -v go >/dev/null || { echo "go not found — install it with mise"; exit 1; }
	@go mod download 2>/dev/null || echo "(no go.mod yet)"

doctor: ## check the environment without installing anything
	@printf 'mise           '; command -v mise          >/dev/null && mise --version || echo '— missing'
	@printf 'go             '; command -v go            >/dev/null && go version || echo '— missing'
	@printf 'golangci-lint  '; command -v golangci-lint >/dev/null && golangci-lint --version | cut -d' ' -f4 || echo '— missing'
	@printf 'gofumpt        '; command -v gofumpt       >/dev/null && gofumpt --version || echo '— missing'
	@printf 'govulncheck    '; command -v govulncheck   >/dev/null && echo present || echo '— missing'
	@printf 'gocyclo        '; command -v gocyclo       >/dev/null && echo present || echo '— missing'
	@printf 'sqlite3        '; command -v sqlite3       >/dev/null && sqlite3 --version || echo '— missing'

# Hard coverage floor. Luna orchestrates agents that write code, so the metrics
# stand in for the review nobody performs line by line. Raise it as the engine
# grows; never lower it to make CI pass.
COVER_MIN ?= 95

# Cyclomatic complexity ceiling. Matches .golangci.yaml — the same number in two
# places would drift, so gocyclo here is the report and golangci-lint is the gate.
CYCLO_MAX ?= 10

# ── formatting ───────────────────────────────────────────────────────────────
fmt: ## format the code (writes)
	@gofumpt -w src/
	@goimports -w src/

fmt-check: ## check formatting without writing
	@out=$$(gofumpt -l src/); \
	  if [ -n "$$out" ]; then echo "not formatted (gofumpt):"; echo "$$out"; exit 1; fi

# ── static analysis ──────────────────────────────────────────────────────────
lint: ## static analysis (golangci-lint, see .golangci.yaml)
	@golangci-lint run ./src/...

lint-docs: ## validate the shape of the docs suite (read-only)
	@sh scripts/lint-docs.sh

lint-language: ## catch Portuguese left in the project (read-only)
	@sh scripts/lint-language.sh

mod: ## check go.mod/go.sum consistency and module integrity
	@go mod tidy -diff
	@go mod verify

# ── tests ────────────────────────────────────────────────────────────────────
# Tests run through the pinned toolchain, not whatever is on the caller's PATH.
GO_TEST = mise exec -- go test

test: ## run the tests
	@$(GO_TEST) ./src/...

cover: ## tests with coverage, failing below COVER_MIN
	@$(GO_TEST) -coverprofile=coverage.out ./src/... >/dev/null
	@go tool cover -func=coverage.out | tail -1
	@go tool cover -func=coverage.out | awk -v min=$(COVER_MIN) '/^total:/ { \
	  gsub(/%/,"",$$3); \
	  if ($$3+0 < min) { printf "coverage %.1f%% below the %d%% minimum\n", $$3, min; exit 1 } }'

race: ## data race detector (needs CGO)
	@CGO_ENABLED=1 $(GO_TEST) -race ./src/...

# ── security ─────────────────────────────────────────────────────────────────
vuln: ## known vulnerabilities in dependencies, filtered by reachability
	@govulncheck ./src/...

# ── metrics: reported, not gated ─────────────────────────────────────────────
# These answer "how healthy is the code", which is a judgement call, not a
# threshold. They stay out of ci-check so a number nobody agreed on cannot block
# a merge — but they are one command away when the question comes up.
cyclo: ## report the most complex functions
	@gocyclo -top 15 -avg src/ || true

deadcode: ## report unreachable functions
	@go run golang.org/x/tools/cmd/deadcode@latest ./src/... || \
	  echo "(deadcode unavailable — needs network)"

crap: ## CRAP index: complexity weighted by coverage
	@go test -coverprofile=coverage.out ./src/... >/dev/null 2>&1
	@go run github.com/gilbertchen/crap4go@latest -c coverage.out ./src/... 2>/dev/null || \
	  echo "(crap4go unavailable — needs network)"

mutation: ## mutation testing — does the suite catch an injected bug?
	@go run github.com/gtramontina/ooze/cmd/ooze@latest ./src/... 2>/dev/null || \
	  echo "(mutation tool unavailable — needs network)"

# ── pipelines ────────────────────────────────────────────────────────────────
# ci fixes what it can and then verifies; ci-check ONLY verifies.
# The distinction is not cosmetic: ci-check is what remote CI runs, and a CI step
# that reformats the code hides exactly what it should be failing on.
#
# race and vuln are deliberately outside ci-check: race needs CGO and roughly
# doubles the test time, and vuln reaches the network. Both run in remote CI as
# separate steps, where the cost is paid once rather than on every local run.
#
# Both go through run-steps.sh rather than chaining prerequisites, because make
# stops at the first failure. A full pass that reports every problem turns four
# fix-and-rerun cycles into one.
CHECK_STEPS := fmt-check lint lint-docs cover
CI_STEPS    := fmt $(CHECK_STEPS) mod

ci-check: ## verify only — the same thing remote CI runs
	@sh scripts/run-steps.sh ci-check $(CHECK_STEPS)

ci: ## fix what can be fixed, then verify. Run before opening a PR.
	@sh scripts/run-steps.sh ci $(CI_STEPS)

build: ## build the binary
	@go build -o bin/luna ./src/cmd/luna

# Where `make install` puts the binary. GOBIN if it is set, else GOPATH/bin, else
# the Go default — the same three places `go install` would use, resolved the
# same way, so the two never disagree about where `luna` ended up.
INSTALL_DIR ?= $(shell go env GOBIN)
ifeq ($(INSTALL_DIR),)
INSTALL_DIR := $(shell go env GOPATH)/bin
endif

install: ## build and install luna into GOBIN (or GOPATH/bin)
	@mkdir -p "$(INSTALL_DIR)"
	@go build -o "$(INSTALL_DIR)/luna" ./src/cmd/luna
	@printf 'installed %s\n' "$$("$(INSTALL_DIR)/luna" version | head -1)"
	@printf '  at %s\n' "$(INSTALL_DIR)/luna"
	@# What `luna` resolves to for this shell, which is not always what was just
	@# installed: a stale copy earlier on PATH is invisible until it refuses a flag
	@# the current build has. Measured on a real run, where the skill documented
	@# three flows and the binary that answered carried one.
	@if command -v luna >/dev/null 2>&1; then \
	  printf '  `luna` resolves to %s, which is %s\n' \
	    "$$(command -v luna)" \
	    "$$(luna version 2>/dev/null | head -1 || true)$$(luna version >/dev/null 2>&1 || echo 'older than this build — it has no `version`')"; \
	else \
	  printf '  %s is not on your PATH — add it, or call luna by path\n' "$(INSTALL_DIR)"; \
	fi

uninstall: ## remove the installed binary
	@rm -f "$(INSTALL_DIR)/luna"
	@printf 'removed %s\n' "$(INSTALL_DIR)/luna"

clean: ## remove build artifacts
	@rm -f luna coverage.out
	@rm -rf dist/ bin/luna

