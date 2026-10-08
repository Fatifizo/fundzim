# Stage 3 — Core Platform Foundation: Implementation

> **What this is:** a description of the code that exists on branch `stage-3/core-platform`, area by area,
> plus the deviations from the Stage 2 → 3 handover and the Stage 3 brief. It describes only what is in the
> repository. Test results are recorded separately by the stage lead; this document makes no claim that any
> test passes. Nothing here is a claim of regulatory approval or compliance.

Inputs: [prerequisite-assessment.md](prerequisite-assessment.md) ·
[STAGE-2-TO-STAGE-3.md](../stage-handover/STAGE-2-TO-STAGE-3.md) ·
[design-baseline.md §12](../stage-2/design-baseline.md) · Next: [STAGE-3-TO-STAGE-4.md](../stage-handover/STAGE-3-TO-STAGE-4.md) ·
Security: [security-review.md](security-review.md) · Guides: [configuration.md](../development/configuration.md),
[migrations.md](../development/migrations.md), [seed-data.md](../development/seed-data.md)

---

## 1. Summary

| Area | Built in Stage 3 |
|---|---|
| Toolchain | One Go module `github.com/Fatifizo/fundzim` at the repo root, `go 1.27.1`. Node 24.21.0 for `apps/web` |
| Binaries | `apps/api/cmd/api` (HTTP API) and `apps/api/cmd/fundzimctl` (operator CLI). **No worker binary** |
| Platform packages | `internal/app` (composition root) and `internal/platform/{config,logging,errs,httpx,health,db,cache,storage,metrics,money,ids,version}` |
| HTTP | 5 enveloped/probe routes on the public listener; metrics and detailed readiness on an internal listener |
| Database | PostgreSQL 17, four goose migrations: roles/schemas/shared functions, platform tables, two audit tables, derived runtime grants |
| Local environment | Root `compose.yaml`: Postgres, Valkey, Garage (S3), storage init, migrate, API, web, optional Mailpit |
| Frontend | Next.js 16.4 development preview: homepage and content pages, "coming soon" pages for later features, same-origin API proxy, typed API client, money formatting, tests (Vitest, Playwright) |
| CI | GitHub Actions `ci.yml`: Go, web, e2e, contracts, integration, images, secrets |

Explicitly **not** built (see §12): worker process, River job queue and its tables, outbox dispatcher,
idempotency middleware, authentication of any kind, sqlc, OpenTelemetry tracing export, architecture
import-graph test, golangci-lint.

## 2. Backend packages

| Package | Responsibility |
|---|---|
| `apps/api/cmd/api` | Loads config (exit 1 with a names-only problem list on error), builds the logger, wires dependencies, listens on the public and internal addresses, runs until `SIGINT`/`SIGTERM`, then shuts down gracefully |
| `apps/api/cmd/fundzimctl` | `config check`, `migrate up|status|version|down`, `version`, `healthcheck [url]` (container `HEALTHCHECK`; the distroless image has no shell or curl) |
| `internal/app` | Composition root. `NewDeps` (DB pool, optional Redis, optional storage, metrics, version), `Checker` (readiness checks), `NewRouter` (routes), `PublicHandler` (middleware chain), `InternalHandler`, `NewServers` (hardened `http.Server`s), `Serve` (graceful shutdown). Only the cmd mains and tests import it |
| `internal/platform/config` | Typed configuration from the environment, validation, production refusals, `Secret` type ([configuration.md](../development/configuration.md)) |
| `internal/platform/logging` | `log/slog` JSON/text logger with `service`, `env`, `version` on every record; key-based redaction; request-scoped logger in the context |
| `internal/platform/errs` | Error model: `Kind` → HTTP status, stable `UPPER_SNAKE` codes, client-safe message, `Retryable`, field `Details`, wrapped `Cause` (logged only) |
| `internal/platform/httpx` | Envelope writers, router with mandatory per-route policy, enveloped 404/405, middleware (request ID, access log, recovery, security headers, CORS, body limit, timeout, rate limit), client-IP and trusted-proxy logic |
| `internal/platform/health` | Concurrent dependency checks, each bounded by its own timeout (even if the check ignores its context); critical vs non-critical |
| `internal/platform/db` | pgx v5 pool (`NewPool` lazy, `Open` eager), `Ping`, `SchemaVersion`, `CheckSchemaCurrent` |
| `internal/platform/cache` | Optional Redis/Valkey client (go-redis v9) with bounded timeouts; `Get`, `Set` (TTL mandatory), `Ping` |
| `internal/platform/storage` | S3 client per storage class (aws-sdk-go-v2), one credential per class; `Check`, `Put`, `Get`, `Delete`, `NewRef` |
| `internal/platform/metrics` | Prometheus registry and Stage 3 metric families |
| `internal/platform/money` | Exact money type (int64 minor units + currency) |
| `internal/platform/ids` | UUIDv7 generation and validation (`github.com/google/uuid`) |
| `internal/platform/version` | Build metadata via `-ldflags`, falling back to the Go VCS stamp |
| `migrations` | Embedded SQL migrations and `ExpectedVersion()` |

Not created (handover §3 listed them): `platform/clock`, `platform/market`, `platform/crypto`, `platform/actor`,
`platform/outbox`, `platform/jobs`, `platform/idempotency`, `platform/featureflags`, `platform/httpclient`,
`platform/testkit`, and the `audit` Go module. There is no Go code that writes audit events yet; the tables
and their database-level guards exist.

Direct Go dependencies (`go.mod`): `aws-sdk-go-v2` (+ `credentials`, `service/s3`), `google/uuid`,
`jackc/pgx/v5`, `pressly/goose/v3`, `prometheus/client_golang`, `redis/go-redis/v9`.

## 3. Configuration

Implemented in `internal/platform/config`; every variable, default and refusal is listed in
[configuration.md](../development/configuration.md). Key points: `APP_ENV` required; values validated once at
boot and reported by name only; secrets in a self-redacting `Secret` type; staging/production refuse
non-`verify-full` database TLS, plain Redis, missing or plain-HTTP storage, text logs, disabled rate limiting
and `http` CORS origins; production also refuses debug logging. Three distinct storage buckets and three
distinct storage keys are enforced in every environment when storage is configured.

## 4. Logging, errors and the envelope

**Logging.** One JSON line per record in containers (`LOG_FORMAT=json`), text locally. Every record carries
`service`, `env`, `version`. `AccessLog` adds `request_id` and `correlation_id` (same value in Stage 3) to a
request-scoped logger and writes one line per request: `operation` (route template, never a raw path), method,
status, `duration_ms`, `response_bytes`. Probe requests (`/healthz`, `/readyz`) log at debug; 5xx at error.
Bodies are never logged. A `ReplaceAttr` hook replaces the value of any attribute whose **key** matches a
sensitive pattern (password, secret, token, authorization, cookie, session, otp, api key, private key,
credential, card, id_number, passport, account_number, dsn, database_url, redis_url, pan/cvv/pin as whole
words) with `[REDACTED]`. Redaction is key-based only (see [security-review.md](security-review.md) F-08).

**Errors.** `errs.Kind` fixes the status: `Invalid` 400, `Unauthenticated` 401, `Forbidden` 403, `NotFound` 404,
`MethodNotAllowed` 405, `Conflict` 409, `Unprocessable` 422, `TooLarge` 413, `RateLimited` 429,
`Unavailable` 503, `Timeout` 503, anything else 500. `errs.As` turns any non-`*errs.Error` into
`INTERNAL_ERROR` with the original error kept only as the logged cause. Platform codes defined:
`MALFORMED_REQUEST`, `VALIDATION_FAILED`, `ROUTE_NOT_FOUND`, `METHOD_NOT_ALLOWED`, `PAYLOAD_TOO_LARGE`,
`UNSUPPORTED_MEDIA_TYPE`, `RATE_LIMITED`, `INTERNAL_ERROR`, `SERVICE_UNAVAILABLE` (Stage 3 routes emit the 404,
405, 413, 429, 500 and 503 ones).

**Envelope.** Success: `{"data": …, "meta": {"request_id": "…"}}`. Error:
`{"error": {"code", "message", "retryable", "details"?}, "meta": {"request_id"}}`. `Retry-After: 5` is added to
429/503 errors when no other value is set. All JSON responses carry `Cache-Control: no-store`. Strict JSON
request decoding is **not** implemented yet (no Stage 3 route accepts a body).

## 5. HTTP: routes, middleware and servers

### 5.1 Routes

| Route | Listener | Response |
|---|---|---|
| `GET /healthz` | public | `200 {"status":"ok"}` (raw, no envelope, no dependency checks) |
| `GET /readyz` | public | `200 {"status":"ok"}` or `503 {"status":"unavailable"}` + `Retry-After: 5`. No dependency names |
| `GET /api/v1/health` | public | Envelope `{"data":{"status":"ok"}}` |
| `GET /api/v1/ready` | public | Envelope `{"data":{"status":"ready"}}`, or `503 SERVICE_UNAVAILABLE` envelope (`retryable: true`) |
| `GET /api/v1/version` | public | Envelope `{"data":{"name","version","build","commit"}}` |
| any other path | public | `404 ROUTE_NOT_FOUND` envelope |
| known path, wrong method | public | `405 METHOD_NOT_ALLOWED` envelope with an `Allow` header |
| `GET /metrics` | internal | Prometheus exposition |
| `GET /internal/readiness` | internal | `{"ready": bool, "checks": [{"name","critical","ok","error"?,"duration_ms"}]}`; `503` when not ready |

Every public route is registered through `httpx.Router.Handle(pattern, policy, h)`; a route without a policy
or without a method panics at startup. Stage 3 has only `PolicyPublic`. A unit test checks that every
registered route exists in `api/openapi/fundzim-v1.yaml`.

### 5.2 Middleware order (outermost first)

1. **Request ID** — accepts an inbound `X-Request-ID` only from a peer inside `TRUSTED_PROXY_CIDRS` and only if it
   matches `^[A-Za-z0-9._:-]{8,64}$`; otherwise generates a UUIDv7. Echoed in the response header.
2. **Access log** — one line per request + request metrics (route template label; unmatched routes are
   labelled `unmatched`).
3. **Panic recovery** — `500 INTERNAL_ERROR` envelope, stack trace to the log, `fundzim_panics_recovered_total`.
   `http.ErrAbortHandler` is re-panicked.
4. **Security headers** — `X-Content-Type-Options: nosniff`, `X-Frame-Options: DENY`,
   `Referrer-Policy: no-referrer`, `Content-Security-Policy: default-src 'none'; frame-ancestors 'none'`,
   `Cross-Origin-Resource-Policy: same-origin`, `Permissions-Policy: camera=(), microphone=(), geolocation=()`,
   `Cache-Control: no-store`; `Strict-Transport-Security: max-age=63072000; includeSubDomains` only outside
   development/test.
5. **CORS** — a no-op unless `CORS_ALLOWED_ORIGINS` is set; then exact-origin match, no credentials,
   preflight answered with `204`.
6. **Body limit** — `413 PAYLOAD_TOO_LARGE` for a declared `Content-Length` above `HTTP_MAX_BODY_BYTES`;
   `http.MaxBytesReader` caps streamed bodies.
7. **Timeout** — request context deadline `REQUEST_TIMEOUT` (handlers must honour the context; the server
   `WriteTimeout` = `REQUEST_TIMEOUT` + 5 s is the hard stop).
8. **Rate limit** (when `RATE_LIMIT_ENABLED`) — in-memory token bucket per client IP
   (`RATE_LIMIT_RPS`/`RATE_LIMIT_BURST`), `429 RATE_LIMITED` with `Retry-After`. `/healthz` and `/readyz` are
   exempt. `X-Forwarded-For` is honoured only from trusted peers (right-most untrusted hop). A
   **Redis-backed limiter is not implemented**; Redis is not used for rate limiting.

Session authentication, CSRF and idempotency are not in the chain yet (Stage 4 slots, see the
[Stage 4 handover](../stage-handover/STAGE-3-TO-STAGE-4.md)). There is no OpenTelemetry span middleware.

### 5.3 Servers

Public: `ReadHeaderTimeout 5s`, `ReadTimeout 15s`, `WriteTimeout REQUEST_TIMEOUT+5s`, `IdleTimeout 120s`,
`MaxHeaderBytes 64 KiB`. Internal: `ReadHeaderTimeout 5s`, `ReadTimeout 10s`, `WriteTimeout 30s`,
`IdleTimeout 60s`. On `SIGINT`/`SIGTERM` both servers stop accepting, in-flight requests finish within
`SHUTDOWN_TIMEOUT`, then the DB pool and Redis client close.

## 6. Health and readiness

Readiness checks (run concurrently; `/readyz` and `/api/v1/ready` reuse one result per second, `/internal/readiness` always runs them fresh):

| Check | Critical | What it does | Timeout |
|---|---|---|---|
| `database` | yes | `pool.Ping` as `fundzim_app` | 2 s |
| `migrations` | yes | `max(version_id)` from `public.goose_db_version` ≥ embedded `ExpectedVersion()` | 2 s |
| `storage_PUBLIC_CAMPAIGN_MEDIA`, `storage_PRIVATE_IDENTITY_DOCUMENTS`, `storage_PRIVATE_COMPLIANCE_DOCUMENTS` | yes (only present when storage is configured) | `ListObjectsV2` (max 1 key) under the class prefix with that class's own credential | 3 s |
| `redis` | only if `REDIS_REQUIRED=true` (present only when `REDIS_URL` is set) | `PING` | 1 s |

Public readiness responses never name hosts, versions or failing checks; failures are logged server-side
(`readiness check failed`, with the check name and error) and exposed in detail only on the internal listener.
Each check updates `fundzim_dependency_up{dependency=…}`. The database pool is **lazy**: the API starts while
PostgreSQL is down and reports `503` until it answers. Liveness (`/healthz`, `/api/v1/health`) never touches
dependencies.

## 7. Database

- **PostgreSQL 17** (`postgres:17.11-alpine3.24` locally; decision P8 in the prerequisite assessment).
- **Roles** (local init script `deploy/docker/postgres/init/10-roles.sh`, first start of an empty volume):
  `fundzim_migrator` (owns the database and the `public` schema; no superuser, no `CREATEDB`, no `CREATEROLE`),
  `fundzim_app`, `fundzim_worker`, `fundzim_kyc`, `fundzim_compliance`, `fundzim_readonly` (all `LOGIN`,
  no superuser). Role defaults: `fundzim_app` statement 15 s / lock 3 s / idle-in-transaction 30 s;
  `fundzim_worker` 60 s / 3 s / 30 s; `fundzim_kyc` and `fundzim_compliance` statement 15 s;
  `fundzim_readonly` statement 60 s and `default_transaction_read_only = on`. `CONNECT` on the database is
  revoked from `PUBLIC` and granted to the runtime roles; database timezone UTC.
- **Pool.** The API opens **one** pool, `fundzim_app` (`DATABASE_URL`), max 20 connections by default, idle
  5 min, lifetime 30 min, health check 30 s, `application_name=fundzim-api`, session `timezone=UTC`. The kyc,
  compliance, worker and readonly pools from the design are not opened.
- **No transaction helper** (`WithTx`) and no query layer (sqlc) yet: Stage 3 has no domain queries.
- **Migrations:** see §9 and [migrations.md](../development/migrations.md).

## 8. Redis and object storage

**Redis (Valkey 9.0.6 locally).** Optional, never authoritative. Password-protected, no persistence
(`--save "" --appendonly no`), 128 MB `allkeys-lru`. The API pings it at startup (warning or startup error per
`REDIS_REQUIRED`) and in readiness. The `cache` package exists (`Get`/`Set` with mandatory TTL) but no
feature uses it yet.

**Object storage (Garage v2.4.1 locally).** Three logical classes, three buckets, three credentials; each
credential is granted read/write on exactly one bucket by `deploy/docker/garage/init.sh` (I-19 as amended by
I-26):

| Class | Bucket (local name) | Key prefix | Credential |
|---|---|---|---|
| `PUBLIC_CAMPAIGN_MEDIA` | `fundzim-public-media` | `media/` | `STORAGE_PUBLIC_*` |
| `PRIVATE_IDENTITY_DOCUMENTS` | `fundzim-private-kyc` | `kyc/` | `STORAGE_KYC_*` (kyc module only, from Stage 5) |
| `PRIVATE_COMPLIANCE_DOCUMENTS` | `fundzim-private-evidence` | `compliance/` | `STORAGE_EVIDENCE_*` (storage module only, from Stage 5) |

The client builds a separate S3 client per class from that class's credential. Object keys are
`<prefix><UUIDv7>`; a `Ref` whose key is outside its class prefix (or contains `..`) is refused. No public
URLs and no presigned URLs are produced in Stage 3. No bucket is configured for anonymous access. Garage has no
server-side encryption configured locally (local data is synthetic). The admin API (port 3903) is not
published to the host.

## 9. Metrics and money

**Metrics** (internal listener only; bounded labels): `fundzim_http_requests_total{method,route,status}`,
`fundzim_http_request_duration_seconds{method,route}`, `fundzim_panics_recovered_total`,
`fundzim_dependency_up{dependency}`, `fundzim_build_info{version,commit}`, plus Go runtime and process
collectors. There is **no tracing exporter**; correlation is by `X-Request-ID` only.

**Money** (`internal/platform/money`): `Money{amountMinor int64, currency}`; currencies only via a `Registry`
(`Default()` = USD and ZWG, 2 minor units, mirroring the seed). `Add`, `Sub`, `Neg`, `Cmp` fail with
`ErrCurrencyMismatch` across currencies and `ErrOverflow` on overflow (no panics, no conversion).
`MulBasisPoints` (128-bit intermediate; `HalfUp`, `Floor`, `Ceil`), `Allocate` (largest remainder, parts sum
exactly), `Parse` (decimal string, never rounds), `DecimalString`, `LogValue`, JSON as
`{"amount_minor":"<digits>","currency":"<code>"}` with strict `Unmarshal` (rejects JSON numbers, decimals,
exponents, `+`, leading zeros, unknown fields). Tests include randomized property loops (`math/rand/v2`, fixed
seeds) and two fuzz targets; the `rapid` library proposed in the handover was not added.

## 10. Migrations content

| File | Creates |
|---|---|
| `20261008120000_foundation.sql` | `NOLOGIN` role placeholders if missing; `REVOKE ALL ON SCHEMA public FROM PUBLIC`; schemas `app`, `queue`, `ledger`, `audit`, `kyc`, `risk`, `compliance`, `recon`; `app.forbid_mutation()`, `app.set_updated_at()`, `app.keep_created_at()`; table `app.status_transitions` (itself append-only) and `app.guard_transition()` |
| `20261008120100_platform.sql` | `app.forbid_column_change()`, `app.allow_only_column_changes()`, `app.set_once_columns()`, `app.bump_version()`; tables `app.currencies` (never deleted; code immutable; minor units frozen once verified) with USD and ZWG; `app.markets` with ZW; `app.idempotency_keys` (identity immutable, `COMPLETED` rows frozen); `app.outbox_events` (content immutable, only relay bookkeeping may change, undispatched rows undeletable, no truncate); `app.inbox_events` (unique consumer+event, no update); `app.feature_flags` (version bump, no delete, deferred check that each version has a change row and that approval-required updates have an approver) and `app.feature_flag_changes` (append-only, maker ≠ checker CHECK; `requested_by`/`approved_by` are `uuid` without FK until Stage 4); seed policy flag `campaign.individual_for_others.enabled = false` |
| `20261008120200_audit.sql` | `audit.audit_events` and `audit.security_audit_events`: append-only (UPDATE/DELETE/TRUNCATE triggers), SHA-256 hash chain assigned by `audit.chain_append()` under a per-table advisory lock, `audit.verify_chain(regclass)`; no FK to users by design. Evidence tables wait for Stage 5 (I-18) |
| `20261008120300_runtime_grants.sql` | `app.apply_runtime_grants()` (derived least-privilege grants) and its first call; `fundzim_worker` ∈ `fundzim_app` verified (membership granted by infrastructure); `EXECUTE audit.verify_chain` for `fundzim_worker` only; `SELECT` on `goose_db_version` for readiness; default privileges |

The `queue` schema exists but is empty: no River tables (see §12).

## 11. Local environment, containers and CI

**`compose.yaml`** (project `fundzim`): all published ports bind to `127.0.0.1`; images pinned to exact tags;
json-file log rotation; memory limits.

| Service | Image | Host port | Role |
|---|---|---|---|
| `fundzim-postgres` | `postgres:17.11-alpine3.24` | 5432 | Database; init script creates roles and the `fundzim` database |
| `fundzim-redis` | `valkey/valkey:9.0.6-alpine3.24` | 6379 | Cache (password, no persistence) |
| `fundzim-storage` | `dxflrs/garage:v2.4.1` | 3900 (S3 API only) | Object storage, single node, `replication_factor = 1` |
| `fundzim-storage-init` | `curlimages/curl:8.22.0` | — | One-shot `deploy/docker/garage/init.sh`: layout, three keys, three buckets, one grant per key (idempotent) |
| `fundzim-migrate` | `fundzim/api:local` | — | One-shot `fundzimctl migrate up` as `fundzim_migrator` |
| `fundzim-api` | `fundzim/api:local` | 8080, 9090 (internal) | API; starts after migrate and storage-init succeed and Redis is healthy |
| `fundzim-web` | `fundzim/web:local` | 3000 | Next.js standalone; starts after the API is healthy; `API_BASE_URL=http://fundzim-api:8080` |
| `fundzim-mail` (profile `tools`) | `axllent/mailpit:v1.31.4` | 8025 (UI), 1025 (SMTP) | Optional mail catcher; nothing sends mail in Stage 3 |

Volumes: `postgres-data`, `storage-meta`, `storage-data`.

**Images.** `deploy/docker/api.Dockerfile` (context: repo root): `golang:1.27.1-alpine3.24` build with
`CGO_ENABLED=0`, `-trimpath`, version ldflags; runtime `gcr.io/distroless/static-debian12:nonroot` pinned by
digest, user `65532:65532`, binaries `api` and `fundzimctl`, `HEALTHCHECK` via `fundzimctl healthcheck`.
`deploy/docker/web.Dockerfile` (context: `apps/web`): `node:24.21.0-bookworm-slim` (tag, not digest),
`npm ci` → `next build` → standalone runtime as user `node`, app files root-owned and read-only except
`.next/cache`, `HEALTHCHECK` on `/healthz` via `node`.

**Scripts.** `scripts/dev-env-init.sh` (generates `.env` with random local secrets, mode 600, never
overwrites); `scripts/check-secrets.sh` (pattern and forbidden-file scan).

**Makefile** targets: `help env up dev down logs ps build test test-go test-web test-integration test-e2e lint
fmt migrate-up migrate-down migrate-status openapi-lint db-validate security clean reset web-install`
(each has a non-make equivalent in [DEVELOPMENT.md §3](../DEVELOPMENT.md)).

**CI** (`.github/workflows/ci.yml`; push to `main`/`stage-*` and PRs; `permissions: contents: read`; actions
pinned by commit SHA; no secrets):

| Job | Steps |
|---|---|
| `go` | `gofmt -l` check, `go vet ./...`, `go vet -tags integration ./tests/...`, `go test -race ./...`, 20 s fuzz smoke for both money fuzz targets, `govulncheck@v1.8.0` |
| `web` | `npm ci`, lint, typecheck, Vitest, build, `npm audit --omit=dev --audit-level=high` (blocking), full `npm audit` (warning only) |
| `e2e` | Playwright (Chromium) against the standalone build |
| `contracts` | Redocly lint of the OpenAPI contract; SQL design-draft validation (PGlite) |
| `integration` | Generates throwaway `.env`, starts Postgres/Valkey/Garage, storage init, migrate, runs the API on the runner, `go test -tags integration` with `FUNDZIM_IT_DESTRUCTIVE=1`, `docker compose down -v` |
| `images` | Builds both images; asserts the API image user is `65532:65532` and the web image has a user set |
| `secrets` | `check-secrets.sh`; gitleaks v8.30.1 (checksum-verified download) over full history |

Not in CI (handover §10 proposed them): golangci-lint, architecture test, Trivy image scan, SAST, nightly
workflow, CODEOWNERS, `migration-test` as a separate job, test-integrity check.

## 12. Frontend (`apps/web`)

Next.js 16.4 (App Router, `cacheComponents`), React 19.3, TypeScript strict, Tailwind v4. Details:
[apps/web/README.md](../../apps/web/README.md), [FRONTEND.md](../FRONTEND.md).

- **Pages.** Homepage (hero, how-it-works, payment-method overview with a "payments are not live yet" notice,
  empty featured-campaigns state, planned features), `/how-it-works`, `/about`, `/contact`, and draft
  placeholders `/privacy` and `/terms` (marked "DRAFT — placeholder only", `LEGAL_REVIEW_REQUIRED`).
  "Coming soon" pages: `/explore` (Stage 7), `/start` (Stage 6), `/login` and `/register` (Stage 4),
  `/dashboard` (Stages 4–6). `/campaigns/[slug]` always answers 404 with an explanatory page and never echoes
  the slug. A development-preview banner is shown on every page; `robots.txt` disallows everything and pages
  are `noindex`. Error, global-error, loading and not-found states exist.
- **API access.** `src/app/api/v1/[...path]/route.ts` is a runtime **route-handler proxy**: it reads
  `API_BASE_URL` per request and forwards `/api/v1/*` to the API (fixed origin, path + query only; strips
  hop-by-hop and client `Forwarded`/`X-Forwarded-*`/`X-Real-IP`; validated or generated `X-Request-ID`; 1 MiB
  body cap; 30 s timeout; no retries; redirects passed through; upstream failure → `503`/`504`
  `SERVICE_UNAVAILABLE` envelope). There is **no** `rewrites()` in `next.config.ts`.
- **API client** (`src/lib/api/`): typed envelope and `Money` types (`amount_minor: string`), `ApiError` and
  client-side error classes, `createApiClient` with per-attempt timeout and request IDs; only GET/HEAD are ever
  retried (max 2, backoff + jitter, honours `Retry-After` up to 5 s); `server.ts` (`server-only`) builds the
  server-side client; `config.ts` validates `API_BASE_URL`.
- **Money formatting** (`src/lib/money.ts`): string/BigInt only, wire-format validation, `US$` / `ZiG`
  display, screen-reader text. An ESLint rule bans `Number()`/`parseFloat` on `amount_minor`.
- **Health.** `/healthz` returns `{"status":"ok"}` without calling the API. The site footer shows a request-time
  API status (via `/api/v1/health` and `/api/v1/version`, inside a Suspense boundary) that degrades gracefully
  when the API is down.
- **Security headers** in `next.config.ts` (CSP, nosniff, `X-Frame-Options: DENY`, referrer policy,
  permissions policy, COOP); `X-Powered-By` removed. CSP uses `script-src 'self' 'unsafe-inline'` (§13).
- **Tests.** Vitest + Testing Library + axe (unit/component, the proxy route handler, the API client, money);
  Playwright E2E (desktop and mobile Chromium) against the standalone build with an unreachable API.

## 13. Deviations from the Stage 2 → 3 handover and the brief

From [prerequisite-assessment.md §2](prerequisite-assessment.md) (the brief governs unless it would break an
accepted ADR or a security control):

| Topic | Decision implemented |
|---|---|
| Go module location | Stage 2 kept: one module at the repo root; entrypoints in `apps/api/cmd/*` |
| Health endpoints | Both: `/healthz`, `/readyz` (probes) **and** `/api/v1/health`, `/api/v1/ready`, `/api/v1/version` (in the OpenAPI contract, `x-fundzim-status: IMPLEMENTED`) |
| Config names | `HTTP_HOST`/`HTTP_PORT` adopted; storage keeps three credential sets under `STORAGE_*` instead of the brief's single `S3_*` set ([configuration.md §10](../development/configuration.md)) |
| Storage classes | The brief's three classes mapped onto separately credentialed storage |
| Compose file | Root `compose.yaml` (not `deploy/local/compose.yaml`); images and init scripts under `deploy/docker/` |
| Redis | Runs in compose by default; the API still works without it (`REDIS_REQUIRED=false`) |
| Frontend scope | The brief's homepage/design system/routes/API client built; future features show "coming soon" |

Additional deviations:

| # | Deviation | Reason / consequence |
|---|---|---|
| D1 | **Garage instead of MinIO** for local S3 | MinIO community images are no longer published on Docker Hub. The code uses only the S3 API (ADR-008), so the local server is swappable |
| D2 | **Three buckets** (I-26 amends I-19) instead of two buckets with prefix-scoped policies | Garage has no prefix-scoped policies; bucket-level grants are stricter and supported by every S3-compatible store. Prefixes remain as namespacing |
| D3 | **No `rewrites()`**; a runtime route-handler proxy instead | `next.config.ts` is evaluated at build time, so a rewrite would bake the build-time `API_BASE_URL` into the standalone image |
| D4 | **No worker binary, no River, no outbox dispatcher, no idempotency middleware** | Out of the brief's objectives. The `queue` schema, `app.outbox_events`, `app.inbox_events` and `app.idempotency_keys` tables exist; the processing code is carried to the start of Stage 4. (The prerequisite assessment's statement that "the worker binary starts and is health-checked" does not match the code: there is no worker binary.) |
| D5 | **CSP uses `'unsafe-inline'` for scripts** (FRONTEND.md §8 asks for nonces) | Per-request nonces force dynamic rendering and conflict with Partial Prerendering under `cacheComponents`. Must be revisited before Stage 7 renders owner content |
| D6 | No OpenTelemetry tracing export | Only request/correlation IDs and Prometheus metrics. `OTEL_*` variables are not read |
| D7 | No golangci-lint, architecture import-graph test, sqlc, testcontainers, `rapid` | CI lint is `gofmt` + `go vet` + `govulncheck`; integration tests use the compose services instead of testcontainers; property tests use seeded `math/rand/v2` loops |
| D8 | Single DB pool (`fundzim_app`) | The kyc/compliance/worker/readonly pools have no users yet; roles and passwords exist locally |
| D9 | Rate limiter is in-memory only | No Redis-backed limiter; per-replica limits (security-review F-01) |
| D10 | Mailpit behind the `tools` profile; no ClamAV container | Nothing sends mail or scans files in Stage 3 (uploads arrive in Stage 5) |
| D11 | `fundzimctl config check` prints `configuration OK` or the problem list, not the redacted configuration (local-environment-design §5.2 proposed printing it) | Simpler; values are never printed |

## 14. API endpoint status

The contract (`api/openapi/fundzim-v1.yaml`) has **269 operations**. Status as of Stage 3:

| Status | Operations |
|---|---|
| **IMPLEMENTED** | `GET /api/v1/health` (HLT-03), `GET /api/v1/ready` (HLT-04), `GET /api/v1/version` (HLT-05) — marked `x-fundzim-status: IMPLEMENTED` in the contract; plus the probes `GET /healthz` (HLT-01) and `GET /readyz` (HLT-02), implemented but not yet marked in the contract. Internal-only, not in the public contract: `GET /metrics`, `GET /internal/readiness` |
| **PLANNED** | All other 264 operations, by the stage that builds the area (indicative, from [ROADMAP.md](../ROADMAP.md) and [migration-plan.md](../database/migration-plan.md)) — see below |
| **BLOCKED** | No operation is blocked as an operation. The real-provider behaviour behind `POST /webhooks/{provider}` (WHK-01) and `POST /webhooks/screening/{vendor}` (WHK-03) is blocked on payment-provider and screening-vendor selection (all PENDING; see [provider-questions.md](../payments/provider-questions.md)). The routes themselves are planned with the sandbox provider/fakes |

PLANNED by area (operation counts by OpenAPI tag):

| Tag | Ops | Planned stage |
|---|---|---|
| Authentication | 15 | 4 |
| User profiles | 7 | 4 |
| Organisations | 10 | 5 |
| KYC · KYB | 16 · 12 | 5 |
| Beneficiaries | 13 | 6 |
| Campaign creation · updates · moderation (staff) | 15 · 5 · 18 | 6 |
| Campaign discovery | 7 | 7 |
| Donations · Payment intents · Payment status | 9 · 5 · 1 | 8 (real rails 9) |
| Refund requests (incl. disputes/recovery) | 24 | 8 (ledger effects 10) |
| Webhooks | 2 | 8 (provider), 5/13 (screening) |
| Payout destinations · requests · status | 9 · 11 · 4 | 11 |
| Risk (staff) · Compliance (staff) | 12 · 18 | 13 (screening subset 5) |
| Admin operations | 22 | 14 (individual operations earlier where their domain needs them, e.g. review policy in 6) |
| Audit access (staff) | 5 | 14 |
| Notifications | 3 | 15 |
| Reconciliation (staff) | 18 | 10 (settlement matching) and 17 |
| Reports | 3 | 17 |
