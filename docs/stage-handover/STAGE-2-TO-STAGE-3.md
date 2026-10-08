# Stage 2 → Stage 3 Handover: Core Platform Foundation Blueprint

> **Status:** Stage 2 deliverable (design only). Stage 3 must not start until the project owner explicitly
> asks for it **and** the prerequisites in §15 are met. This is a blueprint for the **foundation only**. No
> authentication, payments, ledger, KYC or campaign features belong in Stage 3 (§14). Versions are
> "pin exact at Stage 3 start": the latest stable release at that date, recorded in `go.mod`/lockfiles and the
> Stage 3 report.

Inputs: [design-baseline.md](../stage-2/design-baseline.md) (the contract) ·
[prerequisite-assessment.md](../stage-2/prerequisite-assessment.md) · [ROADMAP.md Stage 3](../ROADMAP.md) ·
[go-module-design.md](../architecture/go-module-design.md) · [dependency-rules.md](../architecture/dependency-rules.md) ·
[system-overview.md](../architecture/system-overview.md) · [background-processing.md](../architecture/background-processing.md) ·
[observability.md](../architecture/observability.md) · [performance-capacity.md](../architecture/performance-capacity.md) ·
[local-environment-design.md](../development/local-environment-design.md) ·
[test-architecture.md](../testing/test-architecture.md) · [docs/database/migration-strategy.md](../database/migration-strategy.md) ·
[docs/database/migration-plan.md](../database/migration-plan.md) · [docs/api/](../api/) ·
[api/openapi/fundzim-v1.yaml](../../api/openapi/fundzim-v1.yaml) · [docs/security/](../security/) · ADR-021 – ADR-031

---

## 1. Stage 3 objective (from ROADMAP)

A runnable skeleton: Go api + worker modes, database with least-privilege roles, migrations, config, logging
with redaction, errors and envelope, health/readiness, outbox + job runner, local compose environment, CI with
security gates, and the architecture test. **Acceptance:** `make dev`, `make test`, `make lint` and
`make build` work; CI is green; the money type has property tests; the audit table rejects
`UPDATE`/`DELETE`/`TRUNCATE` from the app role.

## 2. Toolchain and pinned dependencies

| Item | Decision | Justification |
|---|---|---|
| Go | **Latest stable Go release at Stage 3 start.** Go ships every six months, so in October 2026 this is expected to be the 1.27 line (verify on go.dev, and use 1.26.x if 1.27 has had no point release yet). `go.mod`: `go 1.2X` + `toolchain go1.2X.N`. Install from the official go.dev tarball with its SHA-256 checksum verified. | Required features: `net/http.ServeMux` method/path patterns (≥ 1.22), `log/slog` (≥ 1.21), the `tool` directive for pinned dev tools (≥ 1.24), `testing/synctest` for time-dependent tests (GA in 1.25). The latest release gets security fixes the longest. |
| Module path | Placeholder `github.com/Fatifizo/fundzim` (baseline §1). The owner confirms it at Stage 3 start. | Decided in Stage 3 (ARCHITECTURE §14) |
| Node | 24 LTS (existing) | — |
| Driver/pool | `github.com/jackc/pgx/v5` (+ `pgxpool`) | ADR-031 |
| Query codegen | `sqlc` (tool directive) | ADR-031 |
| Migrations | `github.com/pressly/goose/v3`, embedded, run by `fundzimctl` | ADR-028 |
| Queue | `github.com/riverqueue/river` + `riverdriver/riverpgxv5`, schema `queue` | ADR-025. Verify the features in [background-processing.md §2.1](../architecture/background-processing.md) (BPC-1). |
| UUIDv7 | `github.com/google/uuid` (UUIDv7 support) behind `platform/ids` | Baseline §7 |
| Metrics | `github.com/prometheus/client_golang` on the internal listener | [observability.md §6](../architecture/observability.md) |
| Tracing | OpenTelemetry Go SDK + OTLP HTTP exporter (disabled when the endpoint is empty) + `otelhttp`, with a pgx tracer hook | [observability.md §4](../architecture/observability.md) |
| Object storage | `github.com/aws/aws-sdk-go-v2/service/s3` (vendor-neutral S3 API) | ADR-008. Works with MinIO and any S3-compatible store. |
| Tests | `testcontainers-go` (+ postgres module), `pgregory.net/rapid` | [test-architecture.md §5](../testing/test-architecture.md) |
| OpenAPI validation | Redocly CLI (lint, via npx, pinned) + an OpenAPI 3.1 response validator (spike: `pb33f/libopenapi-validator`) | TAC-2 |
| Lint/security tools (tool directive) | `golangci-lint` (v2 line), `govulncheck`; gitleaks binary in CI | DEVELOPMENT §1 |

Every third-party dependency is reviewed before it is added (licence, maintenance, transitive dependencies),
and the review is recorded in the Stage 3 report.

## 3. Directory structure created in Stage 3

Only what Stage 3 uses is created (CLAUDE.md: no placeholder files). Domain modules other than `platform`
and `audit` are **not** created in Stage 3. The table scope is fixed by baseline §12 **I-18**: there are no users/auth tables (Stage 4) and no storage or evidence tables (Stage 5).

```
go.mod  go.sum
.golangci.yml                         # linters incl. gosec, forbidigo (no fmt.Print/log.Print), errorlint
.github/
  workflows/ci.yml                    # push/PR pipeline (§10)
  workflows/nightly.yml               # extended runs (§10)
  CODEOWNERS                          # 2 reviewers on migrations/, internal/platform/{db,crypto,log}, security config
apps/api/
  Dockerfile                          # multi-stage → distroless static, non-root, one image with three binaries
  cmd/api/main.go                     # app.Run(ctx, app.ModeAPI)
  cmd/worker/main.go                  # app.Run(ctx, app.ModeWorker)
  cmd/fundzimctl/main.go              # migrate, seed, health, config check, jobs list/retry
apps/web/                             # existing; additions in §4
internal/
  app/                                # composition root: app.go wiring.go router.go jobs.go events.go config.go
  platform/
    clock/ ids/ errs/ money/ market/ config/ log/ crypto/ db/ actor/
    outbox/ jobs/ idempotency/ featureflags/ httpx/ httpclient/ testkit/
  audit/                              # root package: Recorder interface (Record(ctx, tx, Event))
    service/ store/{queries/,*.go}
migrations/
  embed.go                            # //go:embed *.sql (package migrations; ExpectedVersion)
  <timestamp>_foundation.sql          # §7
  <timestamp>_queue_default_privileges.sql
  <timestamp>_river_queue.sql
  <timestamp>_platform.sql
  <timestamp>_audit.sql                # audit_events + security_audit_events only (I-18)
  <timestamp>_grants_foundation.sql
  tests/*.sql                         # ported DB cases (test-architecture §3.3)
  schema.snapshot.sql                 # normalised pg_dump --schema-only (reviewed diff)
deploy/local/
  compose.yaml
  postgres/init/10-roles.sh
  minio/init.sh                       # 2 buckets, 3 credentials (I-19)
scripts/
  check-secrets.sh                    # existing
  dev-env-init.sh                     # .env from template with generated local secrets (never overwrites)
tests/
  architecture/  (modules.yaml, imports_test.go, queries_test.go, routes_test.go)
  api/           (contract + deny-by-default tests)
  integration/   (cross-cutting: outbox → job → consumer, migrations)
api/openapi/fundzim-v1.yaml           # exists from Stage 2; Stage 3 implements only what it marks for Stage 3
```

## 4. Next.js (`apps/web`, already exists): Stage 3 additions

Read `apps/web/AGENTS.md` and the Next 16 docs in `apps/web/node_modules/next/dist/docs/` first. This version
renamed `middleware` to `proxy` and uses `cacheComponents`.

| Addition | Detail |
|---|---|
| Dev same-origin proxy | `next.config.ts` `rewrites()`: `/api/v1/:path*` → `${API_BASE_URL}/api/v1/:path*` (dev and self-hosted). In deployed environments the reverse proxy does this (ARCHITECTURE §3). |
| API client base | `src/lib/api/client.ts`: server-side `fetch` wrapper that forwards `X-Request-ID` and `traceparent`, forwards the session cookie only when asked (Stage 4), sets timeouts, and parses the envelope. It never runs in the browser with secrets. |
| Envelope and money types | `src/lib/api/types.ts`: `ApiSuccess<T> { data: T; meta: { request_id: string; next_cursor?: string } }`, `ApiError { error: { code: string; message: string; details?: {field: string; code: string}[] }; meta }`, `Money { amount_minor: string; currency: string }`. **No `number` for amounts** (lint rule: no `Number()`/`parseFloat` on `amount_minor`). |
| Error code union | Generated from the OpenAPI error enum (or hand-maintained and tested against it) |
| Health | `src/app/healthz/route.ts` returns `200` without calling the API (liveness for compose/LB). No user-facing status page. |
| Test harness | Vitest + React Testing Library + axe, with at least one real test (envelope parsing, money formatting from string minor units). An empty harness is never reported as "tests pass". |
| Typed env | `NEXT_PUBLIC_*` only in client code. `API_BASE_URL` server-only. |

## 5. PostgreSQL connection management

| Concern | Design |
|---|---|
| Pools | `platform/db` opens one `pgxpool` per role. The **api** opens `app` (`DATABASE_URL`), `kyc` (`DATABASE_KYC_URL`) and `compliance` (`DATABASE_COMPLIANCE_URL`, new). The **worker** opens `worker` (`DATABASE_WORKER_URL`, new; role `fundzim_worker`, baseline §12 I-15) **instead of** `app`, plus `kyc`, `compliance` and `readonly` (`DATABASE_READONLY_URL`, new). In Stage 3 only `app`/`worker` are exercised by features. The others are opened, health-checked in `/readyz`, and reserved by **pool identity type**, so the kyc pool can only reach `kyc` stores (go-module-design §3.2). |
| Sizes | api app 20 / kyc 5 / compliance 5; worker `worker` 25 / kyc 3 / compliance 3 / readonly 5 ([performance-capacity.md §7](../architecture/performance-capacity.md)) |
| Timeouts | Role-level `statement_timeout` 15 s, `lock_timeout` 3 s, `idle_in_transaction_session_timeout` 30 s (set by the local init script and, in deployed environments, by infrastructure). `SET LOCAL` for per-tx overrides. |
| Transactions | `db.WithTx(ctx, pool, opts, fn)`: `READ COMMITTED` by default, retry on 40001/40P01 (max 3, jitter), and it marks `ctx` as in-tx so `httpclient` refuses outbound calls (BP-3). |
| TLS | `sslmode=disable` allowed only when `APP_ENV ∈ {development, test}`. Otherwise `verify-full` is required (boot refusal). |
| Roles | `fundzim_migrator` (DDL only, `fundzimctl migrate`); `fundzim_app` (api runtime); **`fundzim_worker`** (worker runtime: the same table privileges as `fundzim_app` plus `EXECUTE` on worker-only routines such as `ledger.fold_deferred_balances` and `audit.verify_chain`, which the api pool cannot run, I-15); `fundzim_kyc`; `fundzim_compliance`; `fundzim_readonly`. Stage 3 creates the role and its grants. Its worker-only routines arrive with their stages (ledger 10, audit chain verification 18). Nothing connects as the migrator or a superuser at runtime. |

## 6. Redis and S3/MinIO configuration

**Redis (optional).** `REDIS_URL`, `REDIS_REQUIRED=false`. On boot, if Redis is unreachable and not required,
log `warn "redis unavailable"`, set `fundzim_redis_available 0`, and use the in-process rate-limit fallback.
If `REDIS_REQUIRED=true` and it is unreachable, refuse to boot. Stage 3 uses Redis only for the rate-limit
store interface (with the fallback implementation tested). Nothing authoritative ever goes to Redis
([background-processing.md §11](../architecture/background-processing.md)).

**Object storage (baseline §12 I-19).** Two buckets and **three credentials**, each constructed separately so
that no code path holds more than its own:

| Credential | Env (proposal) | Scope | Used only by |
|---|---|---|---|
| public-media | `STORAGE_PUBLIC_*` | whole `public-media` bucket | `storage` (public media pipeline) |
| private-kyc | `STORAGE_KYC_*` | `private-kyc` bucket, prefix `kyc/` only | `kyc` |
| private-evidence | `STORAGE_EVIDENCE_*` (new) | `private-kyc` bucket, prefixes `compliance/` and `reports/` only | `storage` (evidence and statement files) |

Neither private credential can read the other's prefix. Stage 3 delivers only the client wiring plus an
integration test that each credential is **denied** outside its scope (other bucket and other prefix) and
that the private bucket has no anonymous access. Uploads, scanning and evidence records come in Stage 5/6.

## 7. Migrations (goose)

Per ADR-028: timestamped file names `YYYYMMDDHHMMSS_<desc>.sql`, embedded, run by `fundzimctl migrate up` as
`fundzim_migrator`, forward-only in production, `lock_timeout`/`statement_timeout` set in each file. The
logical numbering below follows the draft load order (baseline §10) and
[migration-plan.md](../database/migration-plan.md), which is authoritative if it differs.

| Order | File (logical) | Content | Source draft |
|---|---|---|---|
| 1 | `…_foundation.sql` (logical 0001) | Roles (`IF NOT EXISTS`, NOLOGIN placeholders; includes `fundzim_worker`, I-15), `REVOKE ALL ON SCHEMA public FROM PUBLIC`, the eight schemas, `app.forbid_mutation`, `app.set_updated_at`, `app.keep_created_at`, `app.status_transitions` + `app.guard_transition` | `design/sql/0001_foundation.sql` |
| 2 | `…_queue_default_privileges.sql` | `ALTER DEFAULT PRIVILEGES FOR ROLE fundzim_migrator IN SCHEMA queue …` for `fundzim_app` and `fundzim_worker`, **before** the River tables exist | `0018_grants.sql` (queue section) |
| 3 | `…_river_queue.sql` | River's schema migration SQL for the **pinned** River version, generated with River's migration tooling targeting schema `queue`, committed verbatim with a header naming the version (ADR-025: run by goose) | River |
| 4 | `…_platform.sql` (logical 0002) | `forbid_column_change`, `allow_only_column_changes`, `bump_version`; `currencies` (+ seed USD, ZWG with `minor_units_verified=false`), `markets` (+ ZW), `idempotency_keys`, `outbox_events`, `inbox_events`, `feature_flags`, `feature_flag_changes`. Actor columns (`requested_by`, `approved_by`) are `uuid` **without FK** until Stage 4 adds `app.users` (FK added `NOT VALID`, then validated, per baseline §12 I-16). | `0002_platform.sql` |
| 5 | `…_audit.sql` (logical 0003, partial) | `audit.audit_events` and `audit.security_audit_events` only (+ append-only triggers, hash-chain columns, actor columns without FK to `app.users` until Stage 4). **`evidence_records` and `evidence_holds` are created in Stage 5** with storage (I-18). | `0003_audit.sql` |
| 6 | `…_grants_foundation.sql` | Grants for the tables above, derived from the `0018_grants.sql` rules: `fundzim_app` and `fundzim_worker` get `INSERT/SELECT` on both audit tables; `INSERT` on outbox for kyc/compliance; `DELETE` only on ephemeral platform rows; `queue` access for app and worker; `SELECT` on the goose version table for `/readyz`. The evidence `legal_hold` grant waits for Stage 5. | `0018_grants.sql` |

From Stage 4 on, every migration that creates tables carries its own grants (or re-runs an idempotent
grants function), so grants are never "fixed later". `migrations/tests/*.sql` ports the cases for these
tables (test-architecture §3.3). The authoritative per-stage split is [migration-plan.md](../database/migration-plan.md).

## 8. API routing, middleware and errors

`internal/app/router.go` builds one `http.ServeMux`. Each module's `http/` package registers routes **with a
policy** through `platform/httpx` (`Public`, `Authenticated`, `Permission`, `Webhook`, `Internal`). A route
without a policy panics at startup (test-architecture §3.4).

Middleware order (ARCHITECTURE §5, with OTel added outermost so every span includes the full chain):

| # | Middleware | Stage 3 behaviour |
|---|---|---|
| 0 | OTel HTTP server span | Route-template span names |
| 1 | Request ID | Accept `X-Request-ID` only from `TRUSTED_PROXY_CIDRS` with format check, otherwise UUIDv7. Echo the header. Put it in ctx. |
| 2 | Panic recovery | 500 `INTERNAL_ERROR` envelope, `fundzim_panics_recovered_total`, stack trace to logs (redacted) |
| 3 | Access log | One line per request (route template, status, duration). No bodies. |
| 4 | Security headers | HSTS (non-local), `X-Content-Type-Options`, `Referrer-Policy`, `Content-Security-Policy: default-src 'none'; frame-ancestors 'none'` for API responses, `Cache-Control: no-store` default |
| 5 | Body size limit | `HTTP_MAX_BODY_BYTES`. `413 PAYLOAD_TOO_LARGE`. |
| 6 | Session authentication | Stage 4. Stage 3 installs a no-op authenticator port that yields `anonymous`. |
| 7 | CSRF | Stage 4 |
| 8 | Per-route rate limit | Interface + local fallback + optional Redis. Stage 3 applies a coarse per-IP default. |
| 9 | Idempotency | `platform/idempotency` store + middleware for routes flagged `x-idempotency`. Stage 3 tests it on a **test-only** route registered only in tests. |

**Envelope and errors** (`platform/httpx`, `platform/errs`): typed coded errors map to HTTP status at the
boundary only. Unknown errors become `500 INTERNAL_ERROR` with `request_id` and no internals. Strict JSON
decoding rejects unknown fields and numbers where strings are required. Unknown `/api/v1/*` paths return the
enveloped `404 NOT_FOUND`. Platform error codes are listed in [go-module-design.md §3.4](../architecture/go-module-design.md)
and [docs/api/](../api/).

**`http.Server` hardening:** `ReadHeaderTimeout 5s`, `ReadTimeout 15s`, `WriteTimeout 30s`, `IdleTimeout 120s`,
`MaxHeaderBytes 64 KiB`; graceful shutdown with `SHUTDOWN_GRACE_PERIOD`.

**Health:** `/healthz` and `/readyz` outside `/api/v1`, with the semantics in
[observability.md §7](../architecture/observability.md) (DB, goose version = embedded expected version, queue
reachable, kyc/compliance pools; Redis not included).

## 9. Logging, config and error handling

| Area | Stage 3 deliverable |
|---|---|
| Logging | `platform/log`: slog JSON/text, `RedactingHandler` with the field allow-list and deny list ([observability.md §2](../architecture/observability.md)), context loggers, golden tests, redaction table test |
| Config | `platform/config`: one typed struct per group (APP, DATABASE, REDIS, STORAGE, AUTH, EMAIL, SMS, PAYMENTS, OBSERVABILITY, SECURITY + proposed WORKER/LOCAL), parsed once at boot. Missing or invalid required values → exit non-zero listing **names only**. Unsafe production combinations are refused (SECURITY §14 + [local-environment-design.md §5.2](../development/local-environment-design.md)). `secret`-tagged fields render `[REDACTED]` in `String()`/`LogValue()`. `fundzimctl config check`. |
| `.env.example` | Add the proposed variables (local-environment-design §5.2) with placeholders only |
| Errors | `platform/errs` kinds exactly as [go-module-design.md §3.3](../architecture/go-module-design.md): `Invalid`, `Unauthenticated`, `Forbidden`, `NotFound`, `Conflict`, `Unprocessable`, `RateLimited`, `Unavailable`, `Internal`, plus stable codes. Wrapping with `%w`. Error text passes through redaction before logging. |
| Metrics/tracing | Internal listener (`METRICS_LISTEN_ADDR`, worker `WORKER_METRICS_LISTEN_ADDR`) with the Stage 3 metric families (observability §6.2, "Stage 3" rows). OTel exporter wiring. |

## 10. CI workflows (GitHub Actions)

`.github/workflows/ci.yml` (push and PR) and `nightly.yml`, with jobs exactly as
[test-architecture.md §7](../testing/test-architecture.md). Stage 3 activates: `lint-go`, `lint-web`,
`openapi-lint`, `test-unit`, `test-web`, `test-integration`, `migration-test`, `arch-test`,
`secrets-gitleaks`, `govulncheck`, `npm-audit` (with the documented exception for the
`eslint-config-next` chain, with an expiry), `sast`, `build`, `image-build` + `trivy`, `test-integrity`.
Actions are pinned by commit SHA, `permissions: contents: read`, and there are no secrets in CI for Stage 3
(none are needed). Branch protection on `main` requires these checks (owner action, §15).

## 11. Initial automated tests (Stage 3 gate)

Exactly the Stage 3 row of [test-architecture.md §6](../testing/test-architecture.md):

- `platform/money`: unit, property (`rapid`) and fuzz (parser rejects floats in disguise, JSON string round
  trip to `MaxInt64`, overflow errors, currency mismatch errors, allocation sums exactly).
- `platform/ids`, `clock`, `httpx` (envelope, strict decoding, error mapping), `config` (refusal table),
  `log` (redaction).
- Migrations: empty → head, re-run no-op, local roundtrip, ownership, grants golden, append-only triggers.
- DB cases: `audit.audit_events` and `audit.security_audit_events` reject `UPDATE`/`DELETE`/`TRUNCATE` **as `fundzim_app` and as `fundzim_worker`**; `feature_flag_changes` append-only and maker-checker CHECK; `currencies` guards;
  `idempotency_keys` immutability; outbox content immutability; undispatched outbox not deletable; app role
  cannot read `kyc`.
- Outbox: write → dispatch → consumer once, with duplicate delivery and a crash between dispatch steps.
- River: job enqueued in a domain tx is worked once; rolled-back tx enqueues nothing; job in schema `queue` as
  `fundzim_app`; job registry timeouts ≤ 10 min.
- Idempotency: replay, key reuse `409`, concurrent same-key race (real PG).
- Architecture: import graph, query ownership, route policies, no-float analyzer, BP-3 guard.
- API: deny-by-default, spec ↔ router parity, `/healthz`/`/readyz` semantics incl. a schema-behind case.
- Roles: `fundzim_worker` has app-equivalent table privileges; the api role cannot execute worker-only routines (asserted on a test routine until the real ones exist).
- Storage: three-credential separation (bucket and prefix, I-19).
- Web: Vitest harness with real tests (§4).

## 12. Implementation order

1. **Owner prerequisites confirmed** (§15): Stage 2 acceptance recorded, Docker access fixed, Go installed,
   module path confirmed.
2. `go.mod` with the pinned Go version and `tool` directives. `.golangci.yml`. An empty `internal/app`
   composition root and the three `cmd` mains printing version only.
3. `platform/clock`, `ids`, `errs`, `money` with full unit/property/fuzz tests (no DB needed, first green CI).
4. `platform/config` + `log` (redaction) + `fundzimctl config check`, with tests.
5. `deploy/local/compose.yaml` + init scripts + `scripts/dev-env-init.sh`. Bring up postgres/minio/mailpit.
6. `migrations/`: foundation → queue default privileges → River → platform → audit (two tables) → grants (incl. `fundzim_worker`). `fundzimctl
   migrate`. Migration tests + ported DB cases (testcontainers harness in `platform/testkit`).
7. `platform/db` (pools per role, `WithTx`, in-tx ctx marker) + `httpclient` (tx-open guard) + sqlc setup for
   `platform` and `audit` stores. Query-ownership test.
8. `audit` module minimal: `Recorder.Record(ctx, tx, Event)` for `audit_events` and `security_audit_events`, with
   hash-chain fields per `0003_audit.sql`. Append-only tests. No evidence records (Stage 5).
9. `platform/httpx` + router + middleware chain 0–5, 8, 9 + envelope/errors + `/healthz`/`/readyz` +
   internal listener + metrics + OTel wiring. API contract and deny-by-default tests.
10. `platform/idempotency` store and middleware (test-only route), with race tests.
11. `platform/jobs` (River client, registry, retry policy, error handler, rescue settings) +
    `platform/outbox` (writer, dispatcher periodic job, `Consume`). `platform.idempotency_expire` as the
    first real periodic job. Pipeline tests.
12. Worker mode: queues, graceful shutdown, worker health and readiness.
13. Storage client wiring (three credentials) + bucket/prefix separation test. Redis optional client + rate-limit fallback.
14. Next.js additions (§4).
15. Dockerfiles (non-root, distroless), `compose --profile app` parity, Trivy.
16. CI workflows (§10), CODEOWNERS. Make targets and `DEVELOPMENT.md` updated with the real commands.
17. Docs updated in the same change: `.env.example`, DEVELOPMENT.md (commands, prerequisites), and any deviation
    recorded as an ADR or in the Stage 3 report.
18. Run the acceptance commands (§13) locally **and** in CI. Write the Stage 3 completion report. Stop.

## 13. Acceptance tests and exact commands

All must be run in the Stage 3 session, with real output recorded. Nothing is claimed that was not run.

| # | Command | Expected |
|---|---|---|
| A1 | `go version` | Pinned version |
| A2 | `./scripts/dev-env-init.sh && docker compose -f deploy/local/compose.yaml --env-file .env --profile redis up -d && docker compose -f deploy/local/compose.yaml ps` | postgres, minio, mailpit, redis healthy; minio-init exited 0 |
| A3 | `go run ./apps/api/cmd/fundzimctl migrate up && go run ./apps/api/cmd/fundzimctl migrate status` | All applied. Version = embedded expected. |
| A4 | `go run ./apps/api/cmd/api` then `curl -si http://127.0.0.1:8080/healthz` and `/readyz` | `200` both. `X-Request-ID` present. |
| A5 | Stop postgres, then `curl -si http://127.0.0.1:8080/readyz` | `503 {"status":"not_ready"}`, no details |
| A6 | `curl -si http://127.0.0.1:8080/api/v1/does-not-exist` | `404` envelope `NOT_FOUND` with `meta.request_id` |
| A7 | `go run ./apps/api/cmd/worker` + `curl -s http://127.0.0.1:9091/readyz` | `200`. `platform.idempotency_expire` and `platform.outbox_dispatch` visible in metrics. |
| A8 | `make lint` (or `gofmt -l . && go vet ./... && go tool golangci-lint run && npm --prefix apps/web run lint`) | Clean |
| A9 | `make test` (or `go test -race ./... && go test -race -tags integration ./... && npm --prefix apps/web test`) | All pass, including the Stage 3 gate (§11) |
| A10 | `go test ./tests/architecture/...` | Pass. A deliberately forbidden import in a scratch branch fails it (demonstrated once, not committed). |
| A11 | `psql "$DATABASE_URL" -c "UPDATE audit.audit_events SET id = id"`; `psql "$DATABASE_URL" -c "DELETE FROM audit.audit_events"`; `psql "$DATABASE_URL" -c "TRUNCATE audit.audit_events"` | All three fail as `fundzim_app` (also an automated test) |
| A12 | `make build` (or `go build -trimpath -o bin/ ./apps/api/cmd/... && npm --prefix apps/web run build`) | Binaries + web build |
| A13 | `make security` (gitleaks, check-secrets, npm audit with documented exception, govulncheck) | Clean or documented exceptions |
| A14 | `npm --prefix apps/web run dev` then `curl -si http://127.0.0.1:3000/api/v1/does-not-exist` | Proxied to the api: enveloped `404` |
| A15 | CI run on the Stage 3 PR | All required jobs green |

## 14. Explicitly out of scope for Stage 3

Users and all `users`/`auth` tables, sessions, OTP, MFA, CSRF, and the FKs from actor columns to `app.users` (Stage 4). KYC, the `kyc` schema tables, `storage` tables (`stored_objects`, `upload_sessions`), `audit.evidence_records`/`evidence_holds`, and uploads/scanning (Stage 5). Campaigns
(6). Public pages beyond the scaffold (7). Payments, the `psp` module, the sandbox provider, the webhook inbox
and the webhook endpoint (8). Provider adapters (9). Ledger tables, posting and invariant jobs (10). Payouts
(11). Fees (12). Risk/compliance (13). Admin (14). Notifications delivery (15; Mailpit runs but nothing sends).
Reconciliation (17). Production infrastructure, IaC, secret manager and KMS (18). Retention jobs (blocked on
LR-012). Any real provider credential or any real personal data.

## 15. Prerequisites the owner must provide

| # | Prerequisite | Why |
|---|---|---|
| P1 | **Recorded acceptance of Stage 2** (and the still-unrecorded Stage 1 acceptance, prerequisite-assessment §2) | CLAUDE.md: no stage starts without explicit instruction. Stage 1 acceptance is NOT_VERIFIED. |
| P2 | **Docker socket permission** for the dev user (`sudo usermod -aG docker administrator` + re-login, or rootless Docker) | Compose and testcontainers are impossible without it ([local-environment-design.md §9](../development/local-environment-design.md)) |
| P3 | **Go installed** at the pinned version (official tarball, checksum verified) | Nothing compiles otherwise |
| P4 | `make` installed (`sudo apt install make`), optional | Convenience. The equivalents are documented. |
| P5 | **GitHub Actions enabled** on the repository, plus branch protection on `main` requiring the §10 checks and 2 reviewers for protected paths | CI gates (TESTING §5) |
| P6 | Go module path confirmed | `go.mod` |
| P7 | Git identity configured (DEVELOPMENT §9) | Commits |
| P8 | Decision on PostgreSQL major version (17 as briefed vs 18, LEC-1) and the MinIO substitute if needed (LEC-2) | Image pins |

## 16. Risks

| # | Risk | Likelihood / impact | Mitigation |
|---|---|---|---|
| R1 | Docker permission not fixed in time | Medium / High (blocks local integration tests) | CI runs them on GitHub runners, but Stage 3 acceptance still needs a working local `make dev` |
| R2 | A River version lacks a needed feature (custom schema, retention knobs) | Low–Medium / Medium | BPC-1 spike first. Fallbacks are documented (`search_path`). |
| R3 | The OpenAPI 3.1 validator library is immature | Medium / Low | TAC-2 fallback: JSON Schema 2020-12 validation of extracted components |
| R4 | Scope creep (auth or payments "while we're there") | Medium / High | §14 list. Reviewers reject out-of-stage code. |
| R5 | Drift between the `design/sql` drafts and the executable migrations | Medium / Medium | Ported DB cases + schema snapshot + migration-plan cross-check in review |
| R6 | MinIO distribution changes (LEC-2) | Medium / Low | S3-API-only usage, swappable local server |
| R7 | PGlite (PG 18) validated drafts vs PG 17 runtime | Low / Medium | The Stage 3 migration tests on PG 17 are the authority |
| R8 | Grants forgotten for new tables in later stages | Medium / High | Grants golden test fails on any table without its expected grants |
| R9 | The npm audit exception becomes permanent | Medium / Low | Exception file with an expiry date, enforced by CI |
