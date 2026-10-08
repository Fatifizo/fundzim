# Local Development Environment Design (for Stage 3)

> **Status:** Stage 2 design. **Nothing described here exists yet.** Stage 3 creates the compose file, init
> scripts, Make targets and `fundzimctl` commands described below. Image tags are **pinned by digest at Stage 3
> start**. The versions named here are the intended major/minor lines, not verified digests. Values marked
> **[default]** are starting points.

Related: [ADR-012](../adr/ADR-012-container-first.md) (container-first, compose under `deploy/`) ·
[DEVELOPMENT.md](../DEVELOPMENT.md) · [ARCHITECTURE.md §3, §8, §9](../ARCHITECTURE.md) ·
[SECURITY.md §14](../SECURITY.md) · [`.env.example`](../../.env.example) ·
[prerequisite-assessment.md](../stage-2/prerequisite-assessment.md) ·
[background-processing.md](../architecture/background-processing.md) ·
[observability.md](../architecture/observability.md) · [test-architecture.md](../testing/test-architecture.md) ·
[STAGE-2-TO-STAGE-3.md](../stage-handover/STAGE-2-TO-STAGE-3.md) ·
[migration-strategy.md](../database/migration-strategy.md)

---

## 1. Goals and non-goals

**Goals:** one command brings up every dependency. The Go api/worker and Next.js run either natively (fast
iteration, the default) or in containers (parity). The environment enforces the same **role separation,
bucket separation and fail-closed defaults** as production, at local scale.

**Non-goals:** production topology (Stage 18), any real PSP, SMS or email provider, any real personal data,
and Kubernetes.

## 2. Services

The compose file is `deploy/local/compose.yaml` (ADR-012: "a compose file under `deploy/`"), with init
assets under `deploy/local/`. Compose project name: `fundzim`.

| Service | Image (pin digest in Stage 3) | Profile | Purpose | Data volume |
|---|---|---|---|---|
| `postgres` | `postgres:17.x` (latest 17 minor at Stage 3; Debian-based) | *(default)* | Authoritative store. Roles and DB created by the init script (§3.1). | `fundzim_pg` |
| `minio` | MinIO server (see concern LEC-2) | *(default)* | S3-compatible object storage | `fundzim_minio` |
| `minio-init` | MinIO client `mc` (pinned) | *(default)*, one-shot | Creates two buckets, three users and three policies (§3.2, baseline §12 I-19). Exits 0. | — |
| `mailpit` | `axllent/mailpit` (pinned) | *(default)* | Catches all email, including Stage 4 OTP email | — (in-memory) |
| `redis` | `redis:7.x-alpine` (pinned; see LEC-3) | `redis` | Optional cache/rate-limit. The app runs without it (`REDIS_REQUIRED=false`). | none (ephemeral, `--save "" --appendonly no`) |
| `clamav` | `clamav/clamav` 1.4 LTS line (pinned) | `scan` | Malware scanning (from Stage 5/6). It needs about 3 GB RAM and several minutes to load signatures. | `fundzim_clamav` (signature DB) |
| `api` | Built from `apps/api/Dockerfile` (multi-stage, distroless, non-root) | `app` | Go binary, HTTP mode. In dev mode it also serves the **sandbox PSP** listener (§2.1, Stage 8+). | — |
| `worker` | Same image, `worker` entrypoint | `app` | River workers | — |
| `web` | `apps/web/Dockerfile` (node 24 LTS, non-root) | `app` | Next.js | — |
| `jaeger` (or `otel-collector` + Jaeger) | `jaegertracing/all-in-one` line (pinned) | `observability` | Local trace viewing. Off by default. | — |

**Default workflow:** `docker compose --profile redis up -d` for the dependencies, then run `api`, `worker` and
`web` **natively** (hot reload, debugger). The `app` profile exists for parity checks and the E2E job in CI.

### 2.1 Decision: the sandbox payment provider is served by the api binary in dev mode

| Option | Pros | Cons |
|---|---|---|
| A. In-memory fake adapter only | Fastest, deterministic | No real HTTP. Cannot produce the failure classes that matter (timeout **after** the request was written, connection reset, malformed response), so it cannot exercise webhook signature verification end to end. |
| **B. Sandbox "remote side" served by the api binary on a separate internal listener (chosen)** | Real HTTP round trips through the real adapter and HTTP client. Real signed webhooks back to `/api/v1/webhooks/sandbox`. Scriptable faults per test (TESTING §3.9). No new entrypoint, image or codebase (baseline §1 lists only `api`, `worker`, `fundzimctl`). | Shares a process with the api, so killing the api also kills the sandbox. Acceptable, because crash tests target the worker and api, not the provider. |
| C. Separate `sandbox-psp` container/entrypoint | Independent lifecycle | A fourth entrypoint and image to maintain. It is a pseudo-PSP that could drift into "looking like" a real deployable. |

Rules for option B (implemented in Stage 8, reserved now):

- Enabled only when `PAYMENTS_SANDBOX_ENABLED=true` **and** `APP_ENV ∈ {development, test}`. The config loader
  **refuses to boot** with `APP_ENV=staging|production` if it is true, or if `sandbox` appears in
  `PAYMENTS_ENABLED_PROVIDERS` (SECURITY §14 already requires this for production; extended here to staging
  so that staging behaves like production).
- Listens on `PAYMENTS_SANDBOX_LISTEN_ADDR` (`127.0.0.1:8091`), never on the public API port and never routed
  by the reverse proxy.
- The sandbox adapter talks to it via `PAYMENTS_SANDBOX_BASE_URL`, exactly as a real adapter talks to a PSP.
- Unit tests continue to use an in-memory fake implementing the same `psp` interfaces (option A) where no
  HTTP behaviour is under test.
- The sandbox code lives in `internal/psp/sandbox`. It is compiled into the binary but inert unless enabled.
  A build tag was rejected because CI would have to build and test two binaries.

## 3. Initialisation

### 3.1 PostgreSQL

Init script `deploy/local/postgres/init/10-roles.sh`, run once by the official image's
`/docker-entrypoint-initdb.d` mechanism as the bootstrap superuser:

1. It **derives role names and passwords from the URL variables** in `.env` (`DATABASE_MIGRATION_URL`,
   `DATABASE_URL`, `DATABASE_WORKER_URL`, `DATABASE_KYC_URL`, `DATABASE_COMPLIANCE_URL`, `DATABASE_READONLY_URL`). There is one source
   of truth, so passwords cannot drift between the init script and the app.
2. It creates `LOGIN` roles `fundzim_migrator`, `fundzim_app`, `fundzim_worker` (baseline §12 I-15: app
   privileges plus worker-only routines, granted by migrations), `fundzim_kyc`, `fundzim_compliance` and
   `fundzim_readonly` with those passwords, `NOSUPERUSER NOCREATEDB NOCREATEROLE`. Migration `0001` uses
   `IF NOT EXISTS` and therefore keeps them (in real environments, roles come from infrastructure;
   `design/sql/0001_foundation.sql` creates `NOLOGIN` placeholders only when they are missing).
3. It creates database `fundzim` (and `fundzim_test` for compose-based integration tests, §7), owned by the
   bootstrap superuser, with `REVOKE ALL ON DATABASE … FROM PUBLIC`, `GRANT CONNECT` to the six roles and
   `GRANT CREATE ON DATABASE` to `fundzim_migrator` only.
4. It sets role defaults: `ALTER ROLE fundzim_app SET statement_timeout = '15s'`, `lock_timeout = '3s'`,
   `idle_in_transaction_session_timeout = '30s'`; the same for `fundzim_worker`, `fundzim_kyc` and `fundzim_compliance`; and
   `fundzim_readonly` `statement_timeout = '5min'`
   ([performance-capacity.md §7](../architecture/performance-capacity.md)).
5. It does **not** create schemas or tables. That is goose's job, run as `fundzim_migrator` (§6).

Server flags (`command:`): `-c max_connections=150 -c shared_preload_libraries=pg_stat_statements
-c log_min_duration_statement=250ms -c log_statement=none -c log_parameter_max_length=0
-c log_parameter_max_length_on_error=0 -c log_timezone=UTC -c timezone=UTC`. The parameter-length settings keep
bind values (which could contain PII) out of server logs.

The bootstrap superuser password is `LOCAL_POSTGRES_SUPERUSER_PASSWORD` (new, local-only). It is used by
nothing except the image bootstrap and `make db-reset`.

### 3.2 MinIO

`minio-init` (one-shot, after `minio` is healthy):

| Step | Detail |
|---|---|
| Buckets | `${STORAGE_PUBLIC_BUCKET}` (`fundzim-public-media`), `${STORAGE_KYC_BUCKET}` (`fundzim-private-kyc`) |
| Users (three credentials, baseline §12 I-19) | `${STORAGE_PUBLIC_ACCESS_KEY_ID}` with policy `fundzim-public-rw`: `s3:GetObject/PutObject/DeleteObject` on `arn:aws:s3:::<public>/*` and `ListBucket` on the public bucket only. `${STORAGE_KYC_ACCESS_KEY_ID}` with policy `fundzim-kyc-rw`: the same actions on `<private>/kyc/*` only, with `ListBucket` conditioned on `s3:prefix` = `kyc/`. Used only by the `kyc` module. `${STORAGE_EVIDENCE_ACCESS_KEY_ID}` with policy `fundzim-evidence-rw`: the same actions on `<private>/compliance/*` and `<private>/reports/*` only, with `ListBucket` conditioned on those prefixes. Used only by `storage` for evidence and statement files. **No user can touch another user's bucket or prefix.** The KYC and evidence credentials cannot read each other's prefixes (tested in Stage 3, §8). |
| Anonymous access | Anonymous `GetObject` **only** on the `media/` prefix of the public bucket (served as the local "CDN" via `STORAGE_PUBLIC_CDN_BASE_URL`). The `quarantine/` prefix is never anonymous. The private bucket has **no** anonymous policy. |
| Encryption | Production uses SSE-KMS with a dedicated key (ADR-009). Locally there is no KMS. If the pinned MinIO supports a static dev KMS key, enable auto-encryption for the private bucket. Otherwise document "no SSE locally". Local data is synthetic (§9). |
| Root credentials | `LOCAL_MINIO_ROOT_USER` / `LOCAL_MINIO_ROOT_PASSWORD` (new, local-only). Used only by `minio-init`, **never** by the app. |

Prefix convention. Public bucket: `quarantine/` (uploads) and `media/` (promoted). Private bucket: every object
path starts with its credential's prefix, with quarantine **inside** it (`kyc/quarantine/…` → `kyc/documents/…`;
`compliance/quarantine/…` → `compliance/evidence/…`; `reports/…`), so prefix-scoped credentials cover the whole
lifecycle. The exact sub-prefixes follow the `storage` module design (go-module-design.md, ARCHITECTURE §8.3).

## 4. Ports and health checks

All published ports bind to **`127.0.0.1`** (never `0.0.0.0`), so nothing is reachable from the LAN.

| Service | Container port | Host bind | Purpose | Health check |
|---|---|---|---|---|
| web | 3000 | `127.0.0.1:3000` | Next.js | `node -e "fetch('http://127.0.0.1:3000/').then(r=>process.exit(r.ok?0:1)).catch(()=>process.exit(1))"` |
| api | 8080 | `127.0.0.1:8080` | `/api/v1`, `/healthz`, `/readyz` | `fundzimctl health --url http://127.0.0.1:8080/readyz` (distroless has no curl) |
| api internal | 9090 | `127.0.0.1:9090` | `/metrics`, `/internal/health` (`METRICS_LISTEN_ADDR`) | — |
| worker internal | 9091 | `127.0.0.1:9091` | `/metrics`, `/healthz`, `/readyz` (`WORKER_METRICS_LISTEN_ADDR`) | `fundzimctl health --url http://127.0.0.1:9091/readyz` |
| sandbox PSP (Stage 8+) | 8091 | `127.0.0.1:8091` | Sandbox provider remote side (§2.1) | Covered by api health |
| postgres | 5432 | `127.0.0.1:5432` | DB | `pg_isready -U postgres -d fundzim` (interval 5 s, retries 20) |
| redis | 6379 | `127.0.0.1:6379` | Cache | `redis-cli ping` |
| minio API | 9000 | `127.0.0.1:9000` | S3 | `mc ready local` (or the image's live endpoint `/minio/health/live`) |
| minio console | 9001 | `127.0.0.1:9001` | Admin UI (may be reduced in recent community builds, LEC-2) | — |
| mailpit UI | 8025 | `127.0.0.1:8025` | Read caught mail | Image built-in readiness |
| mailpit SMTP | 1025 | `127.0.0.1:1025` | SMTP (`SMTP_HOST/PORT`) | — |
| clamav | 3310 | `127.0.0.1:3310` | clamd (`CLAMAV_ADDR`) | Image built-in check (verify on pinned image); `start_period` 5 min |
| jaeger UI | 16686 | `127.0.0.1:16686` | Traces | — |
| OTLP gRPC / HTTP | 4317 / 4318 | `127.0.0.1:4317/4318` | `OTEL_EXPORTER_OTLP_ENDPOINT=http://127.0.0.1:4318` when the profile is on | — |

Dependency ordering: `api` and `worker` use `depends_on: postgres: condition: service_healthy` and
`minio-init: condition: service_completed_successfully`. They do **not** depend on `redis` or `clamav`.

## 5. Environment variables

`.env.example` stays the template (DEVELOPMENT §4). Stage 2 **does not edit it**. The variables below are
**proposals for Stage 3** to add, each with a placeholder or a safe local default.

### 5.1 Existing groups → consumers

| Group (`.env.example`) | api | worker | web | compose init | Notes |
|---|---|---|---|---|---|
| APP | ✔ | ✔ | `NEXT_PUBLIC_*` only + `API_BASE_URL` (server-side) | — | |
| DATABASE | ✔ (app, kyc, compliance) | ✔ (worker instead of app, + kyc, compliance, readonly) | ✗ (web has no DB access) | ✔ (role creation from URLs) | |
| REDIS | ✔ | ✔ | ✗ | — | |
| STORAGE | ✔ | ✔ | ✗ | ✔ (bucket/user creation) | Three credential sets (I-19). A process receives only the sets its modules need. |
| AUTH | ✔ | — | ✗ | — | |
| EMAIL | — | ✔ (sends) | ✗ | — | |
| SMS | ✔ (OTP send path) | ✔ | ✗ | — | |
| PAYMENTS | ✔ | ✔ | ✗ | — | |
| OBSERVABILITY | ✔ | ✔ | ✔ (OTel in Next server) | — | |
| SECURITY | ✔ | ✔ | ✗ | — | |

### 5.2 Proposed new variables (Stage 3 adds them to `.env.example`)

| Variable | Group | Local value / placeholder | Purpose | Production rule |
|---|---|---|---|---|
| `DATABASE_COMPLIANCE_URL` | DATABASE | `postgres://fundzim_compliance:<set-locally>@localhost:5432/fundzim?sslmode=disable` | Third pool (ADR-022) | `verify-full` |
| `DATABASE_WORKER_URL` | DATABASE | `postgres://fundzim_worker:<set-locally>@localhost:5432/fundzim?sslmode=disable` | Worker pool, role `fundzim_worker` (I-15). The worker uses it instead of `DATABASE_URL`. | `verify-full` |
| `STORAGE_EVIDENCE_ACCESS_KEY_ID` / `STORAGE_EVIDENCE_SECRET_ACCESS_KEY` | STORAGE | `<set-locally>` | Third credential: private bucket, `compliance/` and `reports/` prefixes, `storage` only (I-19) | Secret manager. Distinct from the KYC credential. |
| `STORAGE_EVIDENCE_KMS_KEY_ID` | STORAGE | empty | SSE key for evidence (may equal or differ from the KYC key; Stage 18 decision) | Required |
| `DATABASE_READONLY_URL` | DATABASE | `postgres://fundzim_readonly:<set-locally>@…` | Reporting pool (worker only) | `verify-full`; replica later |
| `DATABASE_KYC_MAX_CONNS` / `DATABASE_COMPLIANCE_MAX_CONNS` / `DATABASE_READONLY_MAX_CONNS` | DATABASE | `5` / `5` / `5` | Pool sizes | |
| `DATABASE_LOCK_TIMEOUT` | DATABASE | `3s` | Mirrors the role default for `SET LOCAL` | |
| `DATABASE_IDLE_IN_TX_TIMEOUT` | DATABASE | `30s` | | |
| `WORKER_DB_MAX_CONNS` | DATABASE | `25` | Worker pool (`fundzim_worker`) | |
| `LOCAL_POSTGRES_SUPERUSER_PASSWORD` | LOCAL (new group) | `<set-locally>` | Compose bootstrap only | Must not exist outside local |
| `LOCAL_MINIO_ROOT_USER` / `LOCAL_MINIO_ROOT_PASSWORD` | LOCAL | `<set-locally>` | MinIO bootstrap only | Must not exist outside local |
| `WORKER_QUEUES` | WORKER (new group) | `*` | Which River queues this process works (comma list) | |
| `WORKER_METRICS_LISTEN_ADDR` | WORKER | `:9091` | Worker internal listener | Internal only |
| `QUEUE_RESCUE_STUCK_AFTER` | WORKER | `15m` | River rescue ([background-processing.md §2.1](../architecture/background-processing.md)) | |
| `QUEUE_DISCARDED_RETENTION` | WORKER | `2160h` (90 d) | | |
| `SHUTDOWN_GRACE_PERIOD` | APP | `30s` | Graceful stop | |
| `HTTP_READ_HEADER_TIMEOUT` / `HTTP_READ_TIMEOUT` / `HTTP_WRITE_TIMEOUT` / `HTTP_IDLE_TIMEOUT` | APP | `5s` / `15s` / `30s` / `120s` | `http.Server` hardening | |
| `HTTP_MAX_BODY_BYTES` | APP | `1048576` | Default body limit (uploads use presigned PUT, not the API body) | |
| `PAYMENTS_SANDBOX_ENABLED` | PAYMENTS | `true` (dev), `false` (template default) | §2.1 | **Boot refused if true outside development/test** |
| `PAYMENTS_SANDBOX_LISTEN_ADDR` | PAYMENTS | `127.0.0.1:8091` | §2.1 | — |
| `PAYMENTS_SANDBOX_BASE_URL` | PAYMENTS | `http://127.0.0.1:8091` | Adapter target | — |
| `PAYMENTS_PROVIDER_HTTP_TIMEOUT` | PAYMENTS | `15s` | Upper bound per provider call → UNKNOWN beyond | |
| `PAYOUTS_ENABLED` | PAYMENTS | `false` | Global payout kill switch default (Stage 11) | Changing it is an audited flag change |
| `PAYOUT_DESTINATION_ENCRYPTION_LOCAL_KEY` | SECURITY | `<set-locally>` | Key class `payout-destination` (baseline §5.17), separate from the KYC field key | `kms` provider only |
| `PAYOUT_DESTINATION_KMS_KEY_ID` | SECURITY | empty | | Required |
| `PAYOUT_DESTINATION_BIDX_KEY` | SECURITY | `<set-locally>` | HMAC blind index for destinations | Secret manager |
| `KYC_FIELD_ENCRYPTION_LOCAL_KEY` | SECURITY | `<set-locally>` | **Proposal:** split the existing `FIELD_ENCRYPTION_LOCAL_KEY` per key class (KYC vs payout destination) so one key compromise does not expose both | `kms` only |
| `KYC_VENDOR` | KYC (new group, Stage 5) | `fake` | Verification vendor adapter | `fake` refused in production |
| `PAGINATION_CURSOR_HMAC_KEY` | SECURITY | `<set-locally>` | Signs opaque cursors ([performance-capacity.md §9](../architecture/performance-capacity.md)) | Secret manager |
| `OTEL_TRACES_SAMPLER_ARG` | OBSERVABILITY | `1.0` locally | Base ratio for public reads (0.05 in deployed envs) | |
| `MALWARE_SCAN_ENABLED` (existing) | STORAGE | `true` | Clarification: when `false` (allowed **only** in `development`), objects are promoted with `scan_status=SKIPPED_DEV`, which production code paths and staging refuse. When `true` and clamav is not running, uploads stay quarantined (fail closed). | Refused if false |

`fundzimctl config check` (Stage 3) loads the configuration exactly as the api does and prints the redacted
result, including which variables are missing or unsafe for the current `APP_ENV`.

## 6. Commands

`make` is **not installed** on the current machine (`sudo apt install make`). Every target below has a direct
equivalent. Stage 3 updates the `Makefile`. Targets that cannot work yet keep printing why (DEVELOPMENT §3
rule: never fake output).

| Make target (Stage 3) | Equivalent command | Notes |
|---|---|---|
| `make up` | `docker compose -f deploy/local/compose.yaml --env-file .env --profile redis up -d` | Add `--profile scan` for ClamAV, `--profile observability` for Jaeger |
| `make down` | `docker compose -f deploy/local/compose.yaml down` | Keeps volumes |
| `make db-reset` | `docker compose -f deploy/local/compose.yaml down -v postgres && make up && make migrate && make seed` | **Destroys local data.** Asks for confirmation. Refuses if `APP_ENV` ≠ `development`. |
| `make migrate` | `go run ./apps/api/cmd/fundzimctl migrate up` | Uses `DATABASE_MIGRATION_URL`. Applies the embedded goose migrations, which include the pinned River schema migration for `queue` (ADR-025). |
| `make migrate-status` | `go run ./apps/api/cmd/fundzimctl migrate status` | |
| `make migrate-down` | `go run ./apps/api/cmd/fundzimctl migrate down-to <version>` | **Local only.** Refuses unless `APP_ENV=development`. Production is forward-only (ADR-028). |
| `make seed` | `go run ./apps/api/cmd/fundzimctl seed --profile dev` | §7 |
| `make dev` | `make up`, then three terminals: `go run ./apps/api/cmd/api`, `go run ./apps/api/cmd/worker`, `npm --prefix apps/web run dev` | A process runner (for example a `Procfile` + `overmind`/`goreman`) is optional and not required |
| `make test` | `go test -race ./...` + `npm --prefix apps/web run test` (once Vitest exists) | Integration tests need Docker (testcontainers) |
| `make test-unit` | `go test -race -short ./...` | `-short` skips container-backed tests |
| `make test-integration` | `go test -race -run Integration -tags integration ./...` | See [test-architecture.md §5](../testing/test-architecture.md) |
| `make lint` | `gofmt -l . && go vet ./... && golangci-lint run && npm --prefix apps/web run lint && npx --prefix apps/web tsc --noEmit` | |
| `make build` | `go build -trimpath -o bin/ ./apps/api/cmd/...` + `npm --prefix apps/web run build` | |
| `make sqlc` | `go tool sqlc generate` (sqlc pinned via the `tool` directive in `go.mod`) | Generated code is committed and checked in CI (diff must be empty) |
| `make openapi-lint` | `npx @redocly/cli@<pinned> lint api/openapi/fundzim-v1.yaml` | |
| `make arch-test` | `go test ./tests/architecture/...` | Import graph, query ownership, route policies, event contracts |
| `make security` | `./scripts/check-secrets.sh && gitleaks detect --source . --redact && npm --prefix apps/web audit --audit-level=high && govulncheck ./...` | |
| `make compose-app` | `docker compose -f deploy/local/compose.yaml --profile app up -d --build` | Containerised api/worker/web |

`fundzimctl` subcommands that Stage 3 introduces: `migrate {up,status,down-to,version}`, `seed`,
`health --url`, `config check`, `jobs {list,retry}` (retry is audited). Later stages add `ledger verify`
(Stage 10) and `ledger rebuild-projection` (Stage 10, maker-checker).

## 7. Seed data strategy

| Kind | Mechanism | Examples | Rule |
|---|---|---|---|
| **Reference data** | goose migrations (same in every environment) | `app.currencies` (USD 2; ZWG 2 with `minor_units_verified = false`, ADR-018), `app.markets` (ZW), `app.status_transitions` edges, roles and permissions, campaign categories | Changing it is a migration. No environment-specific data. |
| **Synthetic development fixtures** | `fundzimctl seed --profile dev [--seed N]` (Stage 3 adds the command; each stage adds fixtures for its domain) | Users "Test Donor 01", staff with each role, campaigns in each state, sandbox payments in each state | Refuses unless `APP_ENV ∈ {development, test}` **and** the target DB is local (host `localhost`/`127.0.0.1`/compose service name). Deterministic for a given `--seed`. Idempotent (natural keys). |
| **Test fixtures** | Builders inside test packages (`internal/<module>/testfixtures`) | Per-test data | Never shared mutable state between tests |
| **Forbidden** | — | Production copies, real names + IDs, real phone numbers, real national ID/passport numbers, real account numbers, real document images (TESTING §6) | KYC sample documents are generated placeholder images watermarked "TEST" |

Synthetic identifiers: emails under the reserved `example.test` domain. Phone numbers from a fixture list that
Stage 4 confirms as non-routable test numbers (while unconfirmed, `SMS_PROVIDER=log` ensures nothing is sent).
National ID values in an obviously invalid test format. Amounts in both USD and ZWG, including edge values
(TESTING §6).

## 8. Local security considerations

| # | Control | How |
|---|---|---|
| LS-1 | No real credentials, ever | `.env` contains only locally generated values (`openssl rand -base64 32`). Stage 3 adds `scripts/dev-env-init.sh`, which copies `.env.example` to `.env` and fills every `<set-locally>` with fresh random values (never overwrites an existing `.env`). |
| LS-2 | Local-only binds | All ports on `127.0.0.1` (§4). The sandbox listener is loopback-only. |
| LS-3 | Separate DB roles locally too | Six LOGIN roles (§3.1). The api connects as `fundzim_app` and the worker as `fundzim_worker`, never as the superuser or migrator. A Stage 3 test proves `fundzim_app` cannot `UPDATE`/`DELETE` audit rows and cannot read `kyc` ([test-architecture.md §3.3](../testing/test-architecture.md)). |
| LS-4 | Separate storage credentials locally too | Three MinIO users with bucket- and prefix-scoped policies (§3.2), plus a Stage 3 test that each credential is denied outside its scope (other bucket and other prefix). The root credential is used only by `minio-init`. |
| LS-5 | Never point the local env at a real PSP | `PAYMENTS_ENABLED_PROVIDERS=sandbox` locally. From Stage 9, real adapters carry `environment = SANDBOX \| LIVE`. The config loader refuses `LIVE` provider endpoints unless `APP_ENV=production`, and refuses any provider base URL not in that adapter's allow-listed sandbox hosts when `APP_ENV ∈ {development, test}`. |
| LS-6 | No real messages | `SMS_PROVIDER=log` (refused in production), email to Mailpit only |
| LS-7 | Fail-closed defaults stay on | `MALWARE_SCAN_ENABLED`, `AUDIT_HASH_CHAIN_ENABLED` and `RATE_LIMIT_ENABLED` default to `true` locally as well. Turning one off is a deliberate developer action and is refused outside development. |
| LS-8 | Images | Non-root users, pinned digests, `.dockerignore` excludes `.env*`, `bin/`, `node_modules`, `.git` (ADR-012) |
| LS-9 | Server logs | PostgreSQL parameter logging disabled (§3.1). The api's `LOG_LEVEL=debug` still never logs bodies (OBSERVABILITY §3.1, lint rule). |
| LS-10 | Volumes | Local volumes hold synthetic data only. `make db-reset` and `docker compose down -v` are the cleanup path. |

## 9. Known blockers on the current machine

| Blocker | Effect | Fix (owner action) |
|---|---|---|
| **The user lacks Docker socket permission** (`docker` installed, but `/var/run/docker.sock` is not accessible to `administrator`) | Compose cannot start, and **testcontainers-go cannot start**, so integration, DB, concurrency and migration tests cannot run locally. Stage 3 acceptance (`make dev`, `make test`) is blocked until this is fixed. | Owner runs `sudo usermod -aG docker administrator`, then logs out and in again (or `newgrp docker`), then verifies with `docker run --rm hello-world`. **Security note:** membership of the `docker` group is root-equivalent on this host. The alternative is **rootless Docker** (`dockerd-rootless-setuptool.sh install`) with `DOCKER_HOST=unix:///run/user/$UID/docker.sock`. Testcontainers supports both. The owner chooses. |
| Go not installed | Nothing in Go can be built | Install the pinned Go release from go.dev (verify the SHA-256 checksum), see [STAGE-2-TO-STAGE-3.md §2](../stage-handover/STAGE-2-TO-STAGE-3.md) |
| make not installed | Make targets are unavailable. The equivalents in §6 work. | `sudo apt install make` |
| gitleaks, golangci-lint, govulncheck not installed | Local security/lint gates are incomplete. CI is unaffected. | Go tools via `go tool` directives at pinned versions. gitleaks from official releases (checksum verified). |

## 10. Concerns

| # | Concern | Handling |
|---|---|---|
| LEC-1 | The SQL drafts are validated in PGlite on PostgreSQL **18.3**, while the target is **17** | Stage 3 runs the drafts' test cases (ported to Go) against real `postgres:17`. Any 18-only syntax is a defect. The 17 vs 18 choice is the lead's: 17 is what the brief asks for, and 18 adds nothing the design needs (IDs are generated in Go). |
| LEC-2 | MinIO community distribution changed during 2025: the admin console was reduced, and official community images/binaries stopped being published, as I understand it. **Verify at Stage 3.** | For local-only use, pin the last suitable image by digest. Alternatively use another S3-compatible server (for example Garage, SeaweedFS or versitygw). The `storage` module uses only the S3 API, so a swap changes only compose. Record the choice in the Stage 3 report. Production uses a managed S3-compatible service (ADR-008). |
| LEC-3 | Redis licensing changed from 7.4 onward (no longer BSD) | Local dev use is unaffected. For production (Stage 18), evaluate Valkey (BSD) or a managed service. Redis is optional either way. |
| LEC-4 | ClamAV memory (~3 GB) may exceed small dev machines | Optional `scan` profile. Without it, uploads stay quarantined, or `MALWARE_SCAN_ENABLED=false` in development only (§5.2). CI runs the scanner in the upload test job only. |
| LEC-5 | ADR-012 lists Redis and ClamAV as compose services without profiles | Profiles keep them in the compose file but optional. This is compatible with ADR-012, which does not require them to run by default. |
