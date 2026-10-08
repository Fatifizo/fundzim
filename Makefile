# FundZim developer commands. Every target runs real commands; none fakes success.
# `make` is optional: each target's command is listed in docs/DEVELOPMENT.md for copy-paste use.

SHELL := /usr/bin/env bash
.SHELLFLAGS := -eu -o pipefail -c
.DEFAULT_GOAL := help

WEB_DIR  := apps/web
NPM      := npm --prefix $(WEB_DIR)
COMPOSE  := docker compose
GO       ?= go
REDOCLY  := npx --yes @redocly/cli@2.54.3
# Go packages. Explicit patterns: `./...` would descend into apps/web/node_modules (which contains Go files).
GO_PKGS  := ./apps/api/... ./internal/... ./migrations/...
# -race needs cgo (a C compiler). CI always runs with -race; locally it is used when cgo is available.
RACE     := $(shell [ "$$($(GO) env CGO_ENABLED 2>/dev/null)" = "1" ] && echo -race)
# Load .env (if present) into the environment of commands that run on the host.
WITH_ENV := set -a; [ -f .env ] && . ./.env; set +a;

VERSION    ?= $(shell git describe --tags --always --dirty 2>/dev/null || echo 0.0.0-dev)
COMMIT     ?= $(shell git rev-parse --short=12 HEAD 2>/dev/null || echo unknown)
BUILD_TIME ?= $(shell date -u +%Y-%m-%dT%H:%M:%SZ)
LDFLAGS    := -s -w -X github.com/Fatifizo/fundzim/internal/platform/version.Version=$(VERSION) \
              -X github.com/Fatifizo/fundzim/internal/platform/version.Commit=$(COMMIT) \
              -X github.com/Fatifizo/fundzim/internal/platform/version.BuildTime=$(BUILD_TIME)

.PHONY: help env up dev down logs ps build test test-go test-web test-integration test-e2e lint fmt \
        migrate-up migrate-down migrate-status openapi-lint db-validate security clean reset web-install

help: ## List available targets
	@awk 'BEGIN {FS = ":.*##"} /^[a-zA-Z_-]+:.*##/ {printf "  \033[36m%-17s\033[0m %s\n", $$1, $$2}' $(MAKEFILE_LIST)

env: ## Create .env with generated local secrets (never overwrites)
	./scripts/dev-env-init.sh

up: ## Build and start the full local stack in the background (docker compose)
	GIT_COMMIT=$(COMMIT) BUILD_TIME=$(BUILD_TIME) $(COMPOSE) up -d --build
	$(COMPOSE) ps

dev: ## Start infrastructure in docker, then run the API and the web app on the host (hot reload for web)
	$(COMPOSE) up -d --wait fundzim-postgres fundzim-redis fundzim-storage
	$(COMPOSE) up fundzim-storage-init fundzim-migrate
	@echo "API on http://127.0.0.1:8080 · web on http://localhost:3000 (Ctrl-C stops both)"
	$(WITH_ENV) trap 'kill 0' EXIT; $(GO) run ./apps/api/cmd/api & $(NPM) run dev

down: ## Stop the local stack (keeps data volumes)
	$(COMPOSE) down

logs: ## Follow logs of all local services
	$(COMPOSE) logs -f --tail=100

ps: ## Show local service status
	$(COMPOSE) ps

build: ## Build Go binaries into bin/ and the production web build
	CGO_ENABLED=0 $(GO) build -trimpath -ldflags "$(LDFLAGS)" -o bin/ ./apps/api/cmd/...
	$(NPM) run build

test: test-go test-web ## Unit tests (Go with -race, web with Vitest)

test-go: ## Go unit tests (with -race when cgo is available)
	$(GO) test $(RACE) -count=1 $(GO_PKGS)

test-web: ## Web unit tests
	$(NPM) test

test-integration: ## Integration tests against the running local stack (needs `make up`)
	$(WITH_ENV) FUNDZIM_IT_API_URL=$${FUNDZIM_IT_API_URL:-http://127.0.0.1:8080} \
	  FUNDZIM_IT_WEB_URL=$${FUNDZIM_IT_WEB_URL:-http://127.0.0.1:3000} \
	  $(GO) test -tags integration -count=1 -v ./tests/integration/...

test-e2e: ## Browser end-to-end tests (Playwright)
	$(NPM) run test:e2e

lint: ## gofmt check, go vet, web lint and type check
	@out=$$(gofmt -l apps internal migrations tests); if [ -n "$$out" ]; then echo "gofmt needed:"; echo "$$out"; exit 1; fi
	$(GO) vet $(GO_PKGS)
	$(GO) vet -tags integration ./tests/...
	$(NPM) run lint
	$(NPM) run typecheck

fmt: ## Format Go code
	gofmt -w apps internal migrations tests

migrate-up: ## Apply pending migrations (DATABASE_MIGRATION_URL from .env)
	$(WITH_ENV) $(GO) run ./apps/api/cmd/fundzimctl migrate up

migrate-down: ## DEVELOPMENT ONLY: roll back the most recent migration (refused outside development/test)
	$(WITH_ENV) $(GO) run ./apps/api/cmd/fundzimctl migrate down

migrate-status: ## Show migration status
	$(WITH_ENV) $(GO) run ./apps/api/cmd/fundzimctl migrate status

openapi-lint: ## Validate api/openapi/fundzim-v1.yaml
	$(REDOCLY) lint api/openapi/fundzim-v1.yaml --config api/openapi/redocly.yaml

db-validate: ## Validate the Stage 2 SQL design drafts in in-memory PGlite
	cd design/sql/validate && npm ci --no-audit --no-fund && node run.mjs && node catalogue.mjs

security: ## Secret scan, dependency audits (gitleaks/govulncheck if installed)
	./scripts/check-secrets.sh
	@if command -v gitleaks >/dev/null 2>&1; then gitleaks detect --source . --no-banner --redact; \
	else echo "gitleaks not installed locally (CI runs it)."; fi
	$(GO) run golang.org/x/vuln/cmd/govulncheck@v1.8.0 $(GO_PKGS)
	$(NPM) audit --audit-level=high

clean: ## Remove build artefacts (never touches .env or data volumes)
	rm -rf bin dist $(WEB_DIR)/.next $(WEB_DIR)/out $(WEB_DIR)/*.tsbuildinfo

reset: ## DESTRUCTIVE: stop the stack and DELETE local data volumes (postgres, storage)
	@read -r -p "This deletes ALL local FundZim data (database, objects). Type 'reset' to continue: " ans; \
	  [ "$$ans" = "reset" ] || { echo "aborted"; exit 1; }
	$(COMPOSE) down -v --remove-orphans

web-install: ## Install web dependencies from the lockfile (npm ci)
	$(NPM) ci
