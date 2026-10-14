# FundZim

**Fund anyone in Zimbabwe, from anywhere.**

FundZim is a Zimbabwe-first, donation-based crowdfunding platform. Individuals and verified organisations in
Zimbabwe raise funds for medical, education, funeral, emergency, community and other approved causes; donors
in Zimbabwe and abroad contribute using payment methods that suit them (target rails include EcoCash,
OneMoney, InnBucks, O'Mari, ZimSwitch, local banks, Visa and Mastercard) through licensed payment service
providers.

FundZim is **not** an investment, lending, equity or rewards platform, and it is not a wallet or a payment
provider.

> **Status: Stage 11 — Withdrawals & payouts (complete; awaiting acceptance; TEST payouts only).** Campaign owners can
> request withdrawals of synthetic TEST_ONLY funds to a verified payout destination: a 22-check eligibility engine (recorded
> and hashed), an atomic ledger reservation (one in-flight payout per campaign and currency), FINANCE maker-checker
> approval with step-up and linked-identity separation of duties, a pre-submission re-check, provider-neutral
> orchestration through the sandbox SIMULATOR (UNKNOWN outcomes keep their reservation and are never resubmitted),
> ledger journals derived from each payout's state, reconciliation and an exception queue, plus staff and organiser UIs.
> No real payout can execute (no provider: ADR-040; constant, kill switch, configuration and database refuse it) and
> production available funds are BLOCKED (no settlement evidence). Docs:
> [docs/stage-11/implementation.md](docs/stage-11/implementation.md); ADR-043; handover
> [STAGE-11-TO-STAGE-12.md](docs/stage-handover/STAGE-11-TO-STAGE-12.md).
>
> **Status: Stage 10 — Immutable double-entry ledger (complete; awaiting acceptance; TEST ledger only).** The ledger
> exists: a database-enforced double-entry ledger (balanced journals checked at COMMIT, append-only history, rule
> conformance, per-currency journals, non-negative obligations), a dedicated `fundzim_ledger` posting role
> (`fundzim_app` has no ledger access), an idempotent posting engine, maker-checker reversals and corrections,
> account freeze/close, accounting periods with maker-checker close, an integrity checker, a reconciliation
> foundation (settlement reconciliation NOT_PERFORMED: no settlement evidence), and the staff finance UI under
> `/admin/finance/**`. The ledger database is designated **TEST**; production posting is disabled; test payments are
> never posted; every journal is a synthetic TEST_ONLY fixture. Docs:
> [docs/stage-10/implementation.md](docs/stage-10/implementation.md); ADR-042; handover
> [STAGE-10-TO-STAGE-11.md](docs/stage-handover/STAGE-10-TO-STAGE-11.md).
>
> **Status: Stage 9 — Payment provider integration readiness (complete; awaiting acceptance; no provider
> selected).** No Zimbabwean payment provider could be integrated or certified: none is selected (ADR-040,
> DECISION_DEFERRED), and there are no merchant agreements, sandbox credentials or written provider confirmations.
> Stage 9 therefore delivers the preparation: a Vault-KV-v2 secret manager integration (OpenBao in compose; AppRole,
> rotation audit, fail closed; ADR-041), per-process execution environments (`LOCAL_SIMULATOR`,
> `PROVIDER_SANDBOX`, `PRODUCTION` refused at startup), a layered production kill switch, environment-scoped
> credentials, normalised provider error categories, provider health derived from observations, an append-only
> provider evidence log with contradiction escalation, staff provider controls, the public
> `GET /api/v1/payment-methods/available`, an adapter conformance suite, and the legal review, provider confirmation
> register and evaluation. Docs: [docs/stage-9/implementation.md](docs/stage-9/implementation.md); handover
> [STAGE-9-TO-STAGE-10.md](docs/stage-handover/STAGE-9-TO-STAGE-10.md).
>
> **Status: Stage 8 — Payment Abstraction Layer (complete; awaiting acceptance; sandbox only).** The provider-neutral
> payment layer exists: donation intents, payment attempts with a 42-edge, database-guarded state machine
> (precedence, parking, unknown outcomes never treated as failures), a provider registry with versioned,
> evidence-backed capabilities and Model A routing guards, verified webhook ingestion with an inbox, outbox dispatch,
> dead letters and a rejection log, required idempotency keys, refund requests under maker-checker (never executed),
> reconciliation references and a staff payment operations UI. The only provider is a **sandbox simulator**
> (`PAYMENTS_SANDBOX_ENABLED`, refused in production); real payment execution is disabled by a code constant and a
> kill switch, and no provider is selected. There are still **no real donations, ledger postings, totals or
> payouts**; the public donation button stays disabled. Stage 8 docs: [docs/stage-8/implementation.md](docs/stage-8/implementation.md),
> ADR-039; handover [STAGE-8-TO-STAGE-9.md](docs/stage-handover/STAGE-8-TO-STAGE-9.md).
>
> **Status: Stage 7 — Public Fundraising Experience (complete; awaiting acceptance).** On top of the Stage 6
> campaign engine (verification, drafts with exact USD goals, review with conflict-of-interest checks and four eyes,
> controlled publication, updates, restriction enforcement), the repository now has the public experience: a home
> page with transparent featured campaigns (staff picks, then recently published), search and category pages,
> campaign pages with honest trust indicators, canonical URLs and short links, plain-link sharing (WhatsApp,
> Facebook, X, email, copy, Web Share), opt-in SEO (`PUBLIC_SITE_URL` + `SITE_INDEXING=allow`), a minimal PWA,
> public reporting with a staff triage queue (a report never changes a campaign by itself), first-party aggregate
> analytics with no cookies or third-party scripts, and a real-stack browser test suite. There are **no donations,
> payments, payouts or totals**: every campaign page says "Donations are not yet available." Raising funds for
> someone else as an individual stays disabled pending legal advice (LR-046 – LR-048). Production start-up is
> refused until KMS key management exists (Stage 18). Nothing here is a claim of regulatory approval or compliance —
> open legal questions are tracked in [`docs/COMPLIANCE.md`](docs/COMPLIANCE.md). What exists in detail:
> [`docs/stage-7/implementation.md`](docs/stage-7/implementation.md).

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
| Go | 1.27.2 (`go.mod`: `go 1.27.2`) | Official tarball. On the current dev machine it is user-local in `~/.local/go`: `export PATH="$HOME/.local/go/bin:$PATH"` |
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
Verification documents are scanned by the **worker** with ClamAV (`MALWARE_SCANNER=clamd`, `CLAMAV_ADDR=127.0.0.1:3310`
in `.env`): also run `docker compose up -d fundzim-clamav` (the first start downloads signatures for several
minutes) and `go run ./apps/api/cmd/worker`, otherwise uploads stay unscanned and cannot be submitted.

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
| Real-stack web E2E (needs the running stack; see `apps/web/e2e-real/README.md`) | — | `cd apps/web && flock /tmp/claude-1000/fundzim-stack.lock npm run test:e2e:real` (hold the lock: it shares the database with the integration tests) |
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
| [Stage 7 implementation](docs/stage-7/implementation.md) · [public experience](docs/stage-7/public-experience.md) · [discovery](docs/stage-7/campaign-discovery.md) · [sharing](docs/stage-7/social-sharing.md) · [SEO](docs/stage-7/seo.md) · [accessibility](docs/stage-7/accessibility.md) · [security](docs/stage-7/security-review.md) · [performance](docs/stage-7/performance.md) · [testing](docs/stage-7/testing.md) · [known issues](docs/stage-7/known-issues.md) | Public pages, discovery, sharing, SEO, reports, featured, analytics, API consistency |
| [Stage 6 implementation](docs/stage-6/implementation.md) · [lifecycle](docs/stage-6/campaign-lifecycle.md) · [eligibility](docs/stage-6/campaign-eligibility.md) · [moderation](docs/stage-6/campaign-moderation.md) · [media](docs/stage-6/campaign-media.md) · [security](docs/stage-6/campaign-security.md) · [testing](docs/stage-6/testing.md) · [known issues](docs/stage-6/known-issues.md) | Campaign engine, age attestation, restriction enforcement, staff links |
| [Stage 5 implementation](docs/stage-5/implementation.md) · [security review](docs/stage-5/security-review.md) · [testing](docs/stage-5/testing.md) · [known issues](docs/stage-5/known-issues.md) | KYC, KYB, beneficiaries, payout destinations, documents, review engine, compliance cases, risk foundation |
| [Stage 4 implementation](docs/stage-4/implementation.md) · [security review](docs/stage-4/security-review.md) · [testing](docs/stage-4/testing.md) | Authentication, sessions, RBAC, MFA, organisations, worker, distributed limits |
| [Stage 3 implementation](docs/stage-3/implementation.md) · [security review](docs/stage-3/security-review.md) | What Stage 3 built, deviations, endpoint status, security baseline findings |
| [Configuration](docs/development/configuration.md) · [Migrations](docs/development/migrations.md) · [Seed data](docs/development/seed-data.md) | Environment variables and refusals, migration guide, development data rules |
| [Stage 8 implementation](docs/stage-8/implementation.md) · [architecture](docs/stage-8/payment-architecture.md) · [provider interface](docs/stage-8/provider-interface.md) · [state machine](docs/stage-8/payment-state-machine.md) · [webhooks](docs/stage-8/webhook-security.md) · [security](docs/stage-8/security-review.md) · [testing](docs/stage-8/testing.md) · [known issues](docs/stage-8/known-issues.md) | Payment abstraction layer (sandbox only) |
| [Stage 9 implementation](docs/stage-9/implementation.md) · [legal review](docs/stage-9/legal-review.md) · [provider confirmations](docs/stage-9/provider-confirmation-register.md) · [evaluation](docs/stage-9/provider-evaluation.md) · [selection](docs/stage-9/provider-selection.md) · [secret management](docs/stage-9/secret-management.md) · [payment security](docs/stage-9/payment-security.md) · [certification](docs/stage-9/sandbox-certification.md) · [known issues](docs/stage-9/known-issues.md) | Provider integration readiness (no provider selected) |
| [Stage 10 implementation](docs/stage-10/implementation.md) · [ledger architecture](docs/stage-10/ledger-architecture.md) · [chart of accounts](docs/stage-10/chart-of-accounts.md) · [posting rules](docs/stage-10/posting-rules.md) · [reconciliation](docs/stage-10/reconciliation.md) · [security review](docs/stage-10/security-review.md) · [tests](docs/stage-10/financial-integrity-tests.md) · [performance](docs/stage-10/performance.md) · [known issues](docs/stage-10/known-issues.md) | Double-entry ledger (TEST ledger only) |
| [Stage 11 implementation](docs/stage-11/implementation.md) · [architecture](docs/stage-11/payout-architecture.md) · [lifecycle](docs/stage-11/withdrawal-lifecycle.md) · [eligibility](docs/stage-11/eligibility-engine.md) · [available funds](docs/stage-11/available-funds.md) · [simulator](docs/stage-11/payout-simulator.md) · [security review](docs/stage-11/security-review.md) · [testing](docs/stage-11/testing.md) · [known issues](docs/stage-11/known-issues.md) | Withdrawals & payouts (TEST payouts only) |
| [Stage handovers](docs/stage-handover/STAGE-11-TO-STAGE-12.md) | Stage 11 → 12 (fees & platform revenue); earlier handovers in the same folder |

## Licence

Not yet decided. All rights reserved by the project owner until a licence is chosen.
