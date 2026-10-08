# FundZim

**Fund anyone in Zimbabwe, from anywhere.**

FundZim is a Zimbabwe-first, donation-based crowdfunding platform. Individuals and verified organisations in
Zimbabwe raise funds for medical, education, funeral, emergency, community and other approved causes; donors
in Zimbabwe and abroad contribute using payment methods that suit them (target rails include EcoCash,
OneMoney, InnBucks, O'Mari, ZimSwitch, local banks, Visa and Mastercard) through licensed payment service
providers.

FundZim is **not** an investment, lending, equity or rewards platform, and it is not a wallet or a payment
provider.

> **Status: Stage 4 — Authentication & Identity (complete; awaiting acceptance).** On top of the Stage 3
> platform foundation, the repository now has user registration and email verification, email + password
> login with optional TOTP MFA, server-side sessions with CSRF protection, password reset, phone verification
> (development SMS provider), staff accounts with mandatory TOTP created by invitation, maker-checker role
> grants, organisations with membership isolation, a background worker with a transactional outbox, and
> distributed rate limiting. There are **no campaigns, donations, payments, payouts or KYC features** yet,
> and no real money is handled. Production start-up is refused until KMS key management exists (Stage 18).
> Nothing here is a claim of regulatory approval or compliance — open legal questions are tracked in
> [`docs/COMPLIANCE.md`](docs/COMPLIANCE.md). What exists in detail:
> [`docs/stage-4/implementation.md`](docs/stage-4/implementation.md).

## Stack

| Layer | Choice |
|---|---|
| Web | Next.js 16.4 · React 19 · TypeScript · Tailwind CSS v4 (presentation only) — [ADR-003](docs/adr/ADR-003-nextjs-frontend.md) |
| API | Go 1.27 modular monolith, REST `/api/v1/` — [ADR-001](docs/adr/ADR-001-modular-monolith.md), [ADR-002](docs/adr/ADR-002-go-backend.md) |
| Data | PostgreSQL 17 (authoritative), goose migrations — [ADR-004](docs/adr/ADR-004-postgresql.md), [ADR-028](docs/adr/ADR-028-migration-strategy.md) |
| Cache | Redis-protocol cache (Valkey locally; optional, never authoritative) |
| Files | S3-compatible storage (Garage locally); three buckets with separate credentials — [ADR-008](docs/adr/ADR-008-s3-object-storage.md), [ADR-009](docs/adr/ADR-009-kyc-storage-separation.md) |
| Runtime | Containers; Docker Compose locally — [ADR-012](docs/adr/ADR-012-container-first.md) |

## Repository layout

```
apps/web/              Next.js frontend (development preview, same-origin /api/v1 proxy)
apps/api/cmd/          Go entrypoints: api (HTTP server), fundzimctl (migrations, config check, healthcheck)
internal/app/          Composition root (wiring, routes, middleware chain, servers)
internal/platform/     Shared kernel: config, logging, errs, httpx, health, db, cache, storage, metrics, money, ids, version
migrations/            Executable goose SQL migrations (embedded into the binaries)
api/openapi/           OpenAPI 3.1 contract for /api/v1
design/sql/            Stage 2 NON-EXECUTABLE schema drafts + in-memory validation (not migrations)
deploy/docker/         Dockerfiles and local init scripts (Postgres roles, Garage buckets/keys)
compose.yaml           Local development stack
scripts/               dev-env-init.sh (local .env with generated secrets), check-secrets.sh
tests/integration/     Integration tests against the local stack (build tag: integration)
docs/                  Specifications, roadmap, ADRs, stage reports and handovers
```

## Prerequisites

| Tool | Version | Notes |
|---|---|---|
| Go | 1.27.1 (`go.mod`) | Official tarball. On the current dev machine it is user-local in `~/.local/go`: `export PATH="$HOME/.local/go/bin:$PATH"` |
| Node.js | 24.21.0 (via nvm) | `nvm use default`; npm 11 ships with it |
| Docker Engine + Compose v2 | recent | Your user must be able to run `docker` without sudo: `sudo usermod -aG docker $USER`, then log out and in again (or run commands through `sg docker -c "…"` in the current session) |
| GNU make | 4.x, **optional** | `sudo apt install make`. Every target has a plain-command equivalent below |
| gitleaks | optional locally | CI runs it; `make security` uses it when installed |

## Quick start (local development)

```bash
./scripts/dev-env-init.sh        # once: creates .env with generated local secrets (never overwrites)
docker compose up -d --build     # postgres, valkey, garage (+ init), mailpit, migrations, api, worker, web
docker compose ps                # wait until fundzim-api and fundzim-web are healthy
curl -s http://127.0.0.1:8080/api/v1/ready      # {"data":{"status":"ready"},"meta":{...}}
```

Then open <http://127.0.0.1:3000>. With make: `make env` then `make up`.

| Service | URL / address (all bound to 127.0.0.1) | Notes |
|---|---|---|
| Web | <http://127.0.0.1:3000> | Next.js; `/api/v1/*` is proxied to the API |
| API | <http://127.0.0.1:8080> | `/healthz`, `/readyz`, `/api/v1/health`, `/api/v1/ready`, `/api/v1/version` |
| API internal | <http://127.0.0.1:9090> | `/metrics`, `/internal/readiness` (detailed) — never expose publicly |
| PostgreSQL | `127.0.0.1:5432`, database `fundzim` | Roles and passwords in `.env` |
| Redis (Valkey) | `127.0.0.1:6379` | Password in `.env` |
| Object storage (Garage S3 API) | <http://127.0.0.1:3900> | Three buckets, three keys in `.env` |
| Worker internal | <http://127.0.0.1:9091> | `/healthz`, `/readyz`, `/metrics` of the background worker |
| Mail UI (Mailpit) | <http://127.0.0.1:8025> | All local email lands here: verification and reset links, invitations, and dev "SMS" codes (to `<number>@sms.dev.invalid`) |

Host ports can be changed with the `*_HOST_PORT` variables in `.env` (then also edit the matching URLs in
`.env`). Full development guide: [`docs/DEVELOPMENT.md`](docs/DEVELOPMENT.md); configuration reference:
[`docs/development/configuration.md`](docs/development/configuration.md).

### First administrators

There is no default administrator. Create the first two SUPER_ADMINs once with the audited bootstrap ceremony,
then accept both invitation emails in Mailpit (each sets a password and enrols an authenticator app):

```bash
set -a; . ./.env; set +a
go run ./apps/api/cmd/fundzimctl bootstrap-admins \
  --admin-a-email ops-a@example.org --admin-a-name "Ops A" \
  --admin-b-email ops-b@example.org --admin-b-name "Ops B" \
  --justification "initial platform administrators"
```

Further staff are invited by an administrator; roles are granted by maker-checker requests
([`docs/stage-4/rbac.md`](docs/stage-4/rbac.md)).

### Hot-reload alternative

`make dev` starts only the infrastructure in Docker, runs storage init and migrations, then runs the API
(`go run`) and the web dev server (`next dev`) on the host. Without make:

```bash
docker compose up -d --wait fundzim-postgres fundzim-redis fundzim-storage
docker compose up fundzim-storage-init fundzim-migrate
set -a; . ./.env; set +a
go run ./apps/api/cmd/api &          # API on 127.0.0.1:8080
npm --prefix apps/web run dev        # web on http://localhost:3000
```

(Stop any containerised `fundzim-api`/`fundzim-web` first, or they will hold ports 8080/3000.)

## Migrations

Migrations run automatically in `docker compose up` (`fundzim-migrate`). By hand (loads `.env`):

| Task | make | without make |
|---|---|---|
| Apply | `make migrate-up` | `set -a; . ./.env; set +a; go run ./apps/api/cmd/fundzimctl migrate up` |
| Status | `make migrate-status` | `… go run ./apps/api/cmd/fundzimctl migrate status` |
| Roll back one (development/test only) | `make migrate-down` | `… go run ./apps/api/cmd/fundzimctl migrate down` |

Guide: [`docs/development/migrations.md`](docs/development/migrations.md).

## Testing

| Suite | make | without make |
|---|---|---|
| Go unit tests | `make test-go` | `go test -race -count=1 ./apps/api/... ./internal/... ./migrations/...` (name the packages: `./...` would descend into `apps/web/node_modules`) |
| Web unit tests (Vitest) | `make test-web` | `npm --prefix apps/web test` |
| Both | `make test` | both commands above |
| Web E2E (Playwright; first run: `npx --prefix apps/web playwright install chromium`) | `make test-e2e` | `npm --prefix apps/web run test:e2e` |
| Integration (needs the running stack) | `make test-integration` | `set -a; . ./.env; set +a; FUNDZIM_IT_API_URL=http://127.0.0.1:8080 FUNDZIM_IT_WEB_URL=http://127.0.0.1:3000 go test -tags integration -count=1 -v ./tests/integration/...` |

Integration tests need the running stack **including the worker** (identity tests read emails from Mailpit).
They write synthetic accounts, staff, organisations and undeletable audit rows into your local database, and
revoke existing SUPER_ADMIN assignments to re-run the bootstrap ceremony; `make reset` clears everything. Never set `FUNDZIM_IT_DESTRUCTIVE=1` against a database you care about.

## Linting, formatting and building

| Task | make | without make |
|---|---|---|
| Lint | `make lint` | `gofmt -l apps internal migrations tests` (must print nothing) · `go vet ./...` · `go vet -tags integration ./tests/...` · `npm --prefix apps/web run lint` · `npm --prefix apps/web run typecheck` |
| Format Go | `make fmt` | `gofmt -w apps internal migrations tests` |
| Build | `make build` | `CGO_ENABLED=0 go build -trimpath -o bin/ ./apps/api/cmd/...` · `npm --prefix apps/web run build` |
| Container images | `make up` builds them | `docker compose build` |
| OpenAPI lint | `make openapi-lint` | `npx --yes @redocly/cli@2.54.3 lint api/openapi/fundzim-v1.yaml --config api/openapi/redocly.yaml` |
| Security checks | `make security` | `./scripts/check-secrets.sh` · `gitleaks detect --source . --no-banner --redact` · `go run golang.org/x/vuln/cmd/govulncheck@v1.8.0 ./...` · `npm --prefix apps/web audit --audit-level=high` |

## Stopping and resetting

| Task | make | without make |
|---|---|---|
| Stop (keeps data) | `make down` | `docker compose down` |
| Logs / status | `make logs` / `make ps` | `docker compose logs -f --tail=100` / `docker compose ps` |
| **Reset all local data (destructive)** | `make reset` (asks you to type `reset`) | `docker compose down -v --remove-orphans` |

Reset deletes the Postgres and Garage volumes (database, roles, objects). `.env` is kept; the next
`docker compose up -d --build` recreates roles and re-runs migrations. If you delete and regenerate `.env`, you
**must** reset the volumes too (the database was initialised with the old passwords).

## Troubleshooting

| Symptom | Cause / fix |
|---|---|
| `permission denied while trying to connect to the Docker daemon socket` | Your user is not in the `docker` group: `sudo usermod -aG docker $USER`, then log out and in (or `sg docker -c "docker compose up -d --build"`) |
| `required variable … is missing a value: run scripts/dev-env-init.sh` | No `.env` yet: `./scripts/dev-env-init.sh` |
| `dev-env-init: .env already exists` | Intentional — it never overwrites. Keep it, or delete it **and** run `make reset` |
| `bind: address already in use` | Another process uses the port: `ss -ltnp \| grep -E ':(3000\|8080\|9090\|5432\|6379\|3900)\b'`; stop it or change the `*_HOST_PORT` in `.env` (and the matching URL) |
| `/readyz` or `/api/v1/ready` returns 503 | Ask the internal endpoint which check fails: `curl -s http://127.0.0.1:9090/internal/readiness` — `database` (Postgres down or wrong password → reset if `.env` was regenerated), `migrations` (run `make migrate-up` / `docker compose up fundzim-migrate`), `storage_*` (storage init failed: `docker compose logs fundzim-storage-init`), `redis` (only critical with `REDIS_REQUIRED=true`) |
| `fundzim-api: invalid configuration: …` | The API lists the variables that are missing or invalid (names only); see [configuration.md](docs/development/configuration.md). On the host, load `.env` first: `set -a; . ./.env; set +a` |
| `go: command not found` | `export PATH="$HOME/.local/go/bin:$PATH"` (user-local install) |
| Footer shows "Platform API: not reachable right now" | The web works without the API; check `docker compose ps` and the API logs (`docker compose logs fundzim-api`) |

## Documentation

| Document | What it covers |
|---|---|
| [CLAUDE.md](CLAUDE.md) | Binding engineering rules for humans and AI assistants |
| [PRODUCT](docs/PRODUCT.md) | Vision, scope, users, campaign lifecycle, KYC levels |
| [ROADMAP](docs/ROADMAP.md) | Stages 0–20 with dependencies and acceptance criteria |
| [ARCHITECTURE](docs/ARCHITECTURE.md) | System design, modules, boundaries, API conventions |
| [MONEY](docs/MONEY.md) | Money value model — integer minor units, currencies, rounding |
| [LEDGER](docs/LEDGER.md) | Double-entry ledger design and invariants |
| [PAYMENTS](docs/PAYMENTS.md) | Provider abstraction, payment states, webhooks, idempotency, payouts |
| [DATABASE](docs/DATABASE.md) | Database standards, roles, constraints, migrations |
| [SECURITY](docs/SECURITY.md) · [THREAT-MODEL](docs/THREAT-MODEL.md) · [AUDIT](docs/AUDIT.md) | Security baseline, threats, audit logging |
| [COMPLIANCE](docs/COMPLIANCE.md) | Regulatory boundary and the `LEGAL_REVIEW_REQUIRED` register |
| [DATA-CLASSIFICATION](docs/DATA-CLASSIFICATION.md) · [PRIVACY](docs/PRIVACY.md) | Data classes, handling, privacy architecture |
| [OBSERVABILITY](docs/OBSERVABILITY.md) · [FRONTEND](docs/FRONTEND.md) · [TESTING](docs/TESTING.md) · [DEVELOPMENT](docs/DEVELOPMENT.md) | Operating, building and testing standards |
| [ADRs](docs/adr/README.md) | Architecture Decision Records |
| [Stage 4 implementation](docs/stage-4/implementation.md) · [security review](docs/stage-4/security-review.md) · [testing](docs/stage-4/testing.md) | Authentication, sessions, RBAC, MFA, organisations, worker, distributed limits |
| [Stage 3 implementation](docs/stage-3/implementation.md) · [security review](docs/stage-3/security-review.md) | What Stage 3 built, deviations, endpoint status, security baseline findings |
| [Configuration](docs/development/configuration.md) · [Migrations](docs/development/migrations.md) · [Seed data](docs/development/seed-data.md) | Environment variables and refusals, migration guide, development data rules |
| [Stage handovers](docs/stage-handover/STAGE-4-TO-STAGE-5.md) | Stage 4 → 5 (KYC and verification); earlier handovers in the same folder |

## Licence

Not yet decided. All rights reserved by the project owner until a licence is chosen.
