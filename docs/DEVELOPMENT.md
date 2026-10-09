# FundZim — Development Guide

> **Status:** Stage 3 — Core Platform Foundation (complete; awaiting acceptance). The Go API foundation,
> migrations, local Docker Compose stack, Next.js development preview and CI exist. There is no
> authentication, no domain feature and no money handling yet. This guide says exactly what works today and
> what is pending — it does not describe features that do not exist. What was built:
> [stage-3/implementation.md](stage-3/implementation.md).

Related: [CLAUDE.md](../CLAUDE.md) (binding engineering rules) · [ARCHITECTURE.md](ARCHITECTURE.md) ·
[TESTING.md](TESTING.md) · [SECURITY.md](SECURITY.md) · [ROADMAP.md](ROADMAP.md) · [adr/README.md](adr/README.md)

---

## 1. Prerequisites

| Tool | Version | Needed for | Status on the current dev machine (2026-10-08) |
|---|---|---|---|
| Git | any recent | everything | Installed; identity configured (`Fatifizo`) |
| Go | **1.27.2** toolchain (`go.mod`: `go 1.27.2`; 1.27.2 fixes GO-2026-6617, and CI's setup-go reads this directive) | API, `fundzimctl`, Go tests | Installed **user-local** at `~/.local/go` from the official tarball (checksum verified). Add `export PATH="$HOME/.local/go/bin:$PATH"` to your shell profile |
| Node.js via nvm | **24.21.0** | `apps/web`, OpenAPI lint, SQL draft validation | Installed (nvm, default alias `lts/*`) |
| npm | 11.x (ships with Node 24) | `apps/web` | Installed |
| Docker Engine + Compose v2 | recent | local stack, integration tests, image builds | Installed. **Docker-group access for the dev user is being set up by the owner:** `sudo usermod -aG docker $USER`, then log out and in again — or, in the current session, prefix commands with `sg docker -c "…"` |
| GNU make | 4.x | convenience only | **Not installed** (`sudo apt install make`). Every target has an equivalent in §3 |
| gitleaks | v8.x | `make security` (optional locally) | Not installed locally; CI runs v8.30.1 |
| govulncheck | v1.8.0 | `make security` | Not installed; run on demand with `go run golang.org/x/vuln/cmd/govulncheck@v1.8.0 ./...` |
| Playwright Chromium | matches `@playwright/test` 1.63.0 | web E2E | `npx --prefix apps/web playwright install chromium` on first use (no sudo needed for the headless shell) |
| golangci-lint | — | not used yet | Not part of Stage 3 (carried to Stage 4) |

Shell setup for a new terminal:

```bash
export NVM_DIR="$HOME/.nvm"; . "$NVM_DIR/nvm.sh"   # already in ~/.bashrc
nvm use default                                   # node v24.21.0
export PATH="$HOME/.local/go/bin:$PATH"          # go1.27.2 (an older local Go downloads the go1.27.2 toolchain automatically unless GOTOOLCHAIN=local)
```

---

## 2. Repository layout

```
/
├── CLAUDE.md             Binding engineering rules (read first)
├── README.md             Overview and quick start
├── Makefile              Developer commands (optional; equivalents in §3)
├── compose.yaml          Local stack: postgres, valkey, garage (+init), migrate, api, web, mailpit (profile tools)
├── go.mod, go.sum        One Go module at the repo root: github.com/Fatifizo/fundzim
├── .env.example          Template with placeholders/generation markers (tracked); .env is git-ignored
├── .gitleaks.toml, .gitleaksignore   Secret-scan configuration and reviewed false positives
├── .github/workflows/ci.yml          CI pipeline
├── apps/
│   ├── api/cmd/api/          HTTP API entrypoint
│   ├── api/cmd/worker/       Background worker (River jobs, outbox relay and delivery)
│   ├── api/cmd/fundzimctl/   Operator CLI: config check, migrate, bootstrap-admins, version, healthcheck
│   └── web/                  Next.js 16 (App Router, TS, Tailwind v4) — presentation only
├── internal/
│   ├── app/                  Composition root: dependencies, routes, middleware chain, servers
│   └── platform/             config, logging, errs, httpx, health, db, cache, storage, metrics, money, ids, version
├── migrations/           Executable goose migrations + embed.go
├── api/openapi/          OpenAPI 3.1 contract (source of truth for /api/v1)
├── design/sql/           Stage 2 NON-EXECUTABLE schema drafts + validation harness (never run against a DB)
├── deploy/docker/        api.Dockerfile, web.Dockerfile, postgres/init/10-roles.sh, garage/{garage.toml,init.sh}
├── scripts/              dev-env-init.sh, check-secrets.sh
├── tests/integration/    Integration tests against the local stack (build tag `integration`)
└── docs/                 Specifications; docs/adr/ ADRs; docs/development/ guides; docs/stage-*/ stage records
```

Unit tests live beside the code (`*_test.go`, `apps/web/src/**/*.test.ts(x)`, `apps/web/e2e/`); `tests/` holds
cross-cutting suites only. Domain modules (`internal/auth`, `internal/users`, …) are created in the stage
that implements them. See [ARCHITECTURE.md](ARCHITECTURE.md) for module boundaries.

---

## 3. Standard commands

Run `make help` for the live list. Host-side targets that need configuration load `.env` first
(`set -a; . ./.env; set +a`); do the same when running the equivalents by hand. No target fakes success.

| Target | What it does | Equivalent without make |
|---|---|---|
| `make help` | Lists targets | `grep -E '^[a-z-]+:.*##' Makefile` |
| `make env` | Creates `.env` with generated local secrets (never overwrites) | `./scripts/dev-env-init.sh` |
| `make up` | Builds images and starts the full stack in the background, then shows status | `docker compose up -d --build && docker compose ps` |
| `make dev` | Infrastructure in Docker, then storage init + migrations, then API (`go run`) and web (`next dev`) on the host; Ctrl-C stops both | `docker compose up -d --wait fundzim-postgres fundzim-redis fundzim-storage` · `docker compose up fundzim-storage-init fundzim-migrate` · `set -a; . ./.env; set +a; go run ./apps/api/cmd/api & npm --prefix apps/web run dev` |
| `make down` | Stops the stack, keeps data volumes | `docker compose down` |
| `make logs` | Follows logs | `docker compose logs -f --tail=100` |
| `make ps` | Service status | `docker compose ps` |
| `make build` | Go binaries into `bin/` (with version ldflags) and the production web build | `CGO_ENABLED=0 go build -trimpath -o bin/ ./apps/api/cmd/...` · `npm --prefix apps/web run build` |
| `make test` | `test-go` + `test-web` | both below |
| `make test-go` | Go unit tests with the race detector | `go test -race -count=1 ./...` |
| `make test-web` | Vitest unit/component tests | `npm --prefix apps/web test` |
| `make test-integration` | Integration tests against the **running** stack (API and web URLs default to the local ports) | `set -a; . ./.env; set +a; FUNDZIM_IT_API_URL=http://127.0.0.1:8080 FUNDZIM_IT_WEB_URL=http://127.0.0.1:3000 go test -tags integration -count=1 -v ./tests/integration/...` |
| `make test-e2e` | Playwright E2E against the standalone web build (API deliberately unreachable) | `npm --prefix apps/web run test:e2e` |
| `make lint` | gofmt check, `go vet` (also with the integration tag), ESLint, `tsc --noEmit` | `gofmt -l apps internal migrations tests` (must print nothing) · `go vet ./...` · `go vet -tags integration ./tests/...` · `npm --prefix apps/web run lint` · `npm --prefix apps/web run typecheck` |
| `make fmt` | Formats Go code | `gofmt -w apps internal migrations tests` |
| `make migrate-up` | Applies pending migrations as `fundzim_migrator` | `go run ./apps/api/cmd/fundzimctl migrate up` |
| `make migrate-down` | **Development/test only:** rolls back the most recent migration (refused otherwise) | `go run ./apps/api/cmd/fundzimctl migrate down` |
| `make migrate-status` | Lists migrations and applied times | `go run ./apps/api/cmd/fundzimctl migrate status` (also: `… migrate version`) |
| `make openapi-lint` | Redocly lint of the contract | `npx --yes @redocly/cli@2.54.3 lint api/openapi/fundzim-v1.yaml --config api/openapi/redocly.yaml` |
| `make db-validate` | Validates the Stage 2 SQL **design drafts** in in-memory PGlite | `cd design/sql/validate && npm ci --no-audit --no-fund && node run.mjs && node catalogue.mjs` |
| `make security` | Secret scan, gitleaks (if installed), govulncheck, `npm audit --audit-level=high` (fails on the known dev-dependency advisories, §9) | `./scripts/check-secrets.sh` · `gitleaks detect --source . --no-banner --redact` · `go run golang.org/x/vuln/cmd/govulncheck@v1.8.0 ./...` · `npm --prefix apps/web audit --audit-level=high` |
| `make clean` | Removes `bin`, `dist`, `.next`, `out`, tsbuildinfo; never touches `.env` or volumes | `rm -rf bin dist apps/web/.next apps/web/out apps/web/*.tsbuildinfo` |
| `make reset` | **Destructive:** asks you to type `reset`, then deletes all local data volumes | `docker compose down -v --remove-orphans` |
| `make web-install` | Installs web dependencies from the lockfile | `npm --prefix apps/web ci` |

Other useful commands: `go run ./apps/api/cmd/fundzimctl config check` (validates the environment, prints
variable names only), `curl -s http://127.0.0.1:9090/internal/readiness` (which readiness check fails),
Mailpit at <http://127.0.0.1:8025> (default service since Stage 4), the worker's readiness at
`curl -s http://127.0.0.1:9091/readyz`, the first administrators via `fundzimctl bootstrap-admins` (README),
and a second API replica for distributed-limit tests via `docker compose --profile scale-test up -d fundzim-api-2`.

---

## 4. Environment configuration

Full reference (every variable, default and refusal): [development/configuration.md](development/configuration.md).

- `.env.example` (tracked) holds placeholders and generation markers only. Create your `.env` with
  `./scripts/dev-env-init.sh` (`make env`): it fills every `<generate:…>` marker with a fresh random value,
  expands `${NAME}` references, writes mode 600 and **refuses to overwrite** an existing `.env`.
- The same `.env` serves two purposes:
  1. **Docker Compose interpolation.** `compose.yaml` takes secrets, bucket names, `LOG_LEVEL` and host ports
     from `.env` and sets the container environment itself (container hostnames, `APP_ENV=development`,
     JSON logs). Other API settings in `.env` (e.g. `RATE_LIMIT_*`) do not reach the containerised API.
  2. **Host processes.** `set -a; . ./.env; set +a` before `go run ./apps/api/cmd/api`, `fundzimctl` or the
     integration tests. The host defaults are loopback addresses, text logs and debug level.
- The API reads only the environment, validates everything at startup and exits listing the **names** of
  missing or invalid variables. Staging/production refuse unsafe settings (`sslmode` other than
  `verify-full`, plain `redis://`, missing or plain-HTTP storage, text logs, disabled rate limiting).
  AUTH, EMAIL, SMS, PAYMENTS, OBSERVABILITY and SECURITY variables are placeholders for later stages and are
  **not read** in Stage 3.
- Database role passwords are applied only when the Postgres volume is first created. If you regenerate
  `.env`, reset the volumes (`make reset`).
- `.env` and all `.env.*` (except `.env.example`) are git-ignored; `scripts/check-secrets.sh` flags any tracked
  env file. `NEXT_PUBLIC_*` variables are compiled into the browser bundle and are public — never name a
  secret `NEXT_PUBLIC_*` ([FRONTEND.md](FRONTEND.md) §8).
- **Staging/production secrets** come from a secret manager and are injected at runtime. They are never
  committed, never baked into images, never printed in logs or CI output, and are rotated on staff departure
  or suspected exposure. See [SECURITY.md](SECURITY.md).

---

## 5. Branching, commits and reviews

- `main` is protected (once hosted): no direct pushes, PR required, CI green required, ≥1 reviewer approval;
  ≥2 approvals (including the tech lead) for changes touching `internal/ledger`, `internal/payments`,
  `internal/payouts`, `internal/fees`, `internal/reconciliation`, `internal/kyc`, `internal/auth`,
  `migrations/`, or security controls.
- Branch names: `stage-N/<short-topic>`, `fix/<topic>`, `docs/<topic>`, `chore/<topic>`.
- **Conventional Commits**: `feat(campaigns): …`, `fix(ledger): …`, `docs(adr): …`, `chore(web): …`,
  `test(payments): …`, `refactor(…)`, `ci: …`, `security(…)`. Breaking changes use `!` and a `BREAKING CHANGE:`
  footer.
- One logical change per commit; migrations in their own commit with the code that needs them.
- Commits must not contain secrets, real PII, generated build output, or `.env` files. Run `make security`
  before pushing.
- Never rewrite published history on `main`. Never force-push shared branches.
- PR description: what/why, linked stage and ADRs, `LR-xxx` items touched, test evidence (commands run and
  results), screenshots for UI, migration notes, rollback notes.

---

## 6. Code style

**Go**
- `gofmt` formatting and `go vet` are mandatory (CI). `golangci-lint` is planned (carried to Stage 4; no
  config is committed yet).
- Errors wrapped with context (`fmt.Errorf("…: %w", err)`); domain errors are typed and mapped to stable API
  error codes at the HTTP boundary only.
- `context.Context` first parameter for I/O; no global mutable state; clock and ID generator injected.
- Logging via `log/slog` with the redaction rules in [OBSERVABILITY.md](OBSERVABILITY.md).
- No `float32`/`float64` in any money path (code review today; a lint rule/architecture test is carried to
  Stage 4).
- Module boundaries: import only another module's public package (code review today; the import-graph
  architecture test is carried to Stage 4).

**TypeScript / web**
- ESLint (Next.js config) must pass; `tsc --noEmit` strict mode. Prettier added with the first real UI work.
- No `any` in money, auth or API client code. Amounts are `string` (minor units) end-to-end.
- Follow [FRONTEND.md](FRONTEND.md). Read `apps/web/AGENTS.md`: this Next.js version differs from older
  documentation — consult `apps/web/node_modules/next/dist/docs/` before writing Next.js code.

**SQL**
- Lowercase snake_case identifiers; explicit constraints named per [DATABASE.md](DATABASE.md) (`pk_<table>`, `uq_<table>_<cols>`, `ck_<table>_<rule>`, `fk_<table>_<column>`);
  `timestamptz` only; money as `amount_minor BIGINT` + `currency CHAR(3)`. See [DATABASE.md](DATABASE.md).

**Formatting** — `.editorconfig`: UTF-8, LF, final newline, 2-space indent, tabs for Go and Makefiles.

---

## 7. Architecture Decision Records

ADRs live in `docs/adr/` (index: [adr/README.md](adr/README.md); template: `ADR-000-template.md`).

When to write one: any change to a decision in an existing ADR, a new core technology/dependency with
lasting impact, a change to module boundaries, money/ledger/payment/KYC/security architecture, data storage
location, or anything a future engineer would ask "why did we do this?" about.

How:
1. Copy `ADR-000-template.md` to `ADR-NNN-short-title.md` with the next free number.
2. Fill every section: Status, Context, Decision, Consequences, Alternatives, Security implications,
   Financial implications (where applicable).
3. Status starts `Proposed`; becomes `Accepted` after review. A replaced ADR is set to
   `Superseded by ADR-NNN` — never edit an accepted ADR's decision in place.
4. Add it to the index and update affected docs in the same PR.

---

## 8. Definition of Done

A change is done only when **all** apply:

1. Implements the agreed scope for its stage and nothing from later stages.
2. Consistent with CLAUDE.md, the relevant docs and ADRs — or ships a new/superseding ADR.
3. Tests added per [TESTING.md](TESTING.md); all suites pass locally and in CI; no test skipped, deleted or
   weakened.
4. Financial changes: idempotency, DB-level invariants, concurrency and failure tests present; invariant
   checker passes.
5. Security: authorization checks and IDOR tests on every new endpoint; inputs validated; no secrets/PII in
   logs; sensitive actions audited.
6. Lint, type checks, secrets scan and dependency scan pass (or exceptions documented with expiry).
7. Docs updated (including `.env.example` for new config, and the `LR` register for new regulatory
   dependencies).
8. Migrations reviewed, forward-only, and tested on empty and existing schemas.
9. Accessibility checks pass for UI changes (axe + keyboard walkthrough).
10. Reviewed and approved per §5.

---

## 9. Known environment issues (Stage 3)

- **Docker group access.** The dev user was not in the `docker` group at the start of Stage 3; the owner is
  setting it up (`sudo usermod -aG docker administrator`). Until you log in again, run Docker through
  `sg docker -c "docker compose …"`. Without Docker access, the compose stack, integration tests and image
  builds cannot run locally (unit tests, lint and the web build still can).
- **Go is user-local** (`~/.local/go`), not on the system `PATH`; add `~/.local/go/bin` to `PATH`.
- **`make` is not installed**; use the equivalents in §3.
- **`npm audit`** in `apps/web` reports high-severity advisories in development-only dependencies
  (the `eslint-config-next` chain). Production dependencies are audited as a blocking CI step; the full audit
  is warning-only. Consequence: `make security` exits non-zero at its last step. Tracked in the Stage 3
  known-issues record.
- **gitleaks** and **govulncheck** are not installed locally; CI runs both (`make security` runs govulncheck
  through `go run`, which downloads it on first use).
- **Integration tests leave synthetic audit rows** in the local database (append-only by design); reset the
  volumes to remove them ([development/seed-data.md §3](development/seed-data.md)).
- **GitHub Actions** has not been confirmed running for this repository from this machine.

---

## 10. Stage completion report template

Every stage ends with this report, then work **stops** until the user accepts the stage and asks for the
next one. Do not claim tests that were not run.

```markdown
# FUNDZIM STAGE N COMPLETION REPORT

## Stage
Stage N — <Stage name from docs/ROADMAP.md>

## Status
PASS / PARTIAL / FAIL

## Executive Summary
Concise description of what was completed.

## Repository
Repository path:
Git status:
Current branch:
Commit hash, if committed:

## Implemented
Detailed list.

## Documentation Created / Updated
Every significant document and its purpose.

## Architecture Decisions
ADRs created, accepted, or superseded.

## Repository Structure
Resulting high-level tree.

## Financial Safety Controls
Controls defined or implemented (and where enforced: app / DB / both).

## Security Controls
Controls defined or implemented.

## Compliance / Legal Questions
Every LEGAL_REVIEW_REQUIRED item touched or blocking (LR IDs).

## Assumptions
Important assumptions made.

## Tests / Validation Performed
Exact commands run and what they covered. Do not claim tests that were not run.

## Validation Results
Passed:
Failed:
Warnings:

## Secrets Scan
How the repository (and history) was checked for credentials, and the result.

## Known Issues

## Technical Debt
Debt intentionally accepted, with reason and planned resolution stage.

## Deviations From Stage Prompt
Each deviation and why. If none: None.

## Files Changed
Concise list.

## Recommended Stage N+1 Inputs
Information to research or decide before the next stage.

## Stage N Acceptance Recommendation
PASS / PASS WITH CONDITIONS / FAIL — with reasons.
```
