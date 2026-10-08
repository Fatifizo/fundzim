# FundZim developer commands.
#
# Stage 0: only apps/web (Next.js) has code. Go targets activate automatically once
# go.mod exists (Stage 3); until then they print what is pending instead of faking it.
# See docs/DEVELOPMENT.md.

SHELL := /usr/bin/env bash
.SHELLFLAGS := -eu -o pipefail -c
.DEFAULT_GOAL := help

WEB_DIR := apps/web
NPM     := npm --prefix $(WEB_DIR)
HAS_GO_MODULE := $(wildcard go.mod)

.PHONY: help dev test lint build migrate security clean web-install

help: ## List available targets
	@awk 'BEGIN {FS = ":.*##"} /^[a-zA-Z_-]+:.*##/ {printf "  \033[36m%-12s\033[0m %s\n", $$1, $$2}' $(MAKEFILE_LIST)

web-install: ## Install web dependencies from the lockfile (npm ci)
	$(NPM) ci

dev: ## Run the web app locally (API + local services arrive in Stage 3)
	@echo "Starting apps/web on http://localhost:3000"
	@echo "Note: the Go API and docker compose services (Postgres, Redis, MinIO) do not exist yet (Stage 3)."
	$(NPM) run dev

test: ## Run all tests that currently exist
ifneq ($(HAS_GO_MODULE),)
	go test -race ./...
else
	@echo "Go: no go.mod yet - backend tests begin in Stage 3."
endif
	@echo "Web: no test runner configured yet (Vitest/Playwright arrive with the first UI features)."
	@echo "Nothing was tested. This target does not pretend otherwise."

lint: ## Lint all code
ifneq ($(HAS_GO_MODULE),)
	go vet ./...
	golangci-lint run
else
	@echo "Go: no go.mod yet - skipping."
endif
	$(NPM) run lint

build: ## Build all deployables
ifneq ($(HAS_GO_MODULE),)
	go build -o bin/ ./apps/api/...
else
	@echo "Go: no go.mod yet - skipping."
endif
	$(NPM) run build

migrate: ## Apply database migrations (Stage 2/3)
	@echo "No migrations or migration tool yet. Tool is chosen in Stage 2/3 - see migrations/README.md."
	@exit 1

security: ## Secret scan + dependency audit
	./scripts/check-secrets.sh
	@if command -v gitleaks >/dev/null 2>&1; then gitleaks detect --source . --no-banner --redact; \
	else echo "gitleaks not installed - install it for full history scanning (docs/DEVELOPMENT.md)."; fi
	$(NPM) audit --audit-level=high || { echo "npm audit reported issues - review docs/DEVELOPMENT.md (Known issues)."; exit 1; }
ifneq ($(HAS_GO_MODULE),)
	govulncheck ./...
endif

clean: ## Remove build artefacts (never touches .env or data volumes)
	rm -rf bin dist $(WEB_DIR)/.next $(WEB_DIR)/out $(WEB_DIR)/*.tsbuildinfo
