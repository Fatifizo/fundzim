# Configuration Reference (Stage 3)

> **Scope.** This document describes the configuration that the Stage 3 code **actually reads and validates**.
> The source of truth is [`internal/platform/config/config.go`](../../internal/platform/config/config.go)
> (Go API and `fundzimctl config check`), [`apps/api/cmd/fundzimctl/main.go`](../../apps/api/cmd/fundzimctl/main.go)
> (migrations) and [`apps/web/src/lib/api/config.ts`](../../apps/web/src/lib/api/config.ts) (web). If this page
> and the code disagree, the code wins and this page must be fixed.
>
> `.env.example` also lists variables for later stages (AUTH, EMAIL, SMS, PAYMENTS, OBSERVABILITY, SECURITY).
> **Nothing in Stage 3 reads them** (§6). Setting them has no effect yet.

Related: [DEVELOPMENT.md §4](../DEVELOPMENT.md) · [SECURITY.md §14](../SECURITY.md) ·
[local-environment-design.md §5](local-environment-design.md) · [stage-3/implementation.md](../stage-3/implementation.md)

---

## 1. How configuration is loaded

- The API reads **only process environment variables** (12-factor). It does **not** read `.env` itself.
  `.env` is consumed by `docker compose` (interpolation) and by your shell when you run
  `set -a; . ./.env; set +a` (the Makefile does this for host-side targets).
- `config.Load` parses everything once at startup. Values are trimmed; an empty value counts as unset.
- Every problem is collected and reported together, sorted, by **variable name only** (never the value).
  The API exits with status 1 and prints e.g.:

  ```
  fundzim-api: invalid configuration:
    - DATABASE_URL is required
    - LOG_FORMAT must be json outside development and test
  ```

- If `APP_ENV` is missing or unknown, that is an error **and** the rest of the configuration is validated
  with production strictness.
- "Local" in this document means `APP_ENV` is `development` or `test` (`Env.IsLocal()`). "Non-local" means
  `staging` or `production`.
- Secret-bearing values (database URLs, Redis URL, storage keys) are held in the `config.Secret` type, which
  renders as `[REDACTED]` through `String`, `GoString`, `slog.LogValue`, JSON and text marshalling.
  `Reveal()` is called only where a value is handed to a client library.
- `fundzimctl config check` runs the same `config.Load` and prints `configuration OK` or the problem list.

## 2. Go API variables (read by `internal/platform/config`)

### 2.1 App and HTTP

| Variable | Default | Validation | Non-local rule |
|---|---|---|---|
| `APP_ENV` | — (**required**) | `development`, `test`, `staging`, `production` | — |
| `APP_NAME` | `FundZim` | — | — |
| `APP_VERSION` | empty | Optional override of the build version reported by `/api/v1/version` and metrics | — |
| `HTTP_HOST` | `127.0.0.1` | — | Containers set `0.0.0.0` explicitly |
| `HTTP_PORT` | `8080` | integer 1–65535; must differ from `INTERNAL_HTTP_PORT` | — |
| `INTERNAL_HTTP_HOST` | `127.0.0.1` | — | Never expose publicly (metrics, detailed readiness) |
| `INTERNAL_HTTP_PORT` | `9090` | integer 1–65535 | — |
| `REQUEST_TIMEOUT` | `15s` | Go duration, 1s–2m. Request context deadline; the server `WriteTimeout` is this + 5 s | — |
| `SHUTDOWN_TIMEOUT` | `20s` | Go duration, 1s–5m. Graceful-shutdown budget | — |
| `HTTP_MAX_BODY_BYTES` | `1048576` (1 MiB) | integer 1024–67108864 | — |
| `TRUSTED_PROXY_CIDRS` | empty | Comma-separated CIDRs (each must parse) | Peers in these ranges may supply `X-Request-ID` and `X-Forwarded-For` |
| `CORS_ALLOWED_ORIGINS` | empty (no CORS headers at all) | Comma-separated `scheme://host[:port]`; `*` refused; path other than `/` refused | Must be `https` |

### 2.2 Database

| Variable | Default | Validation | Non-local rule |
|---|---|---|---|
| `DATABASE_URL` | — (**required**) | `postgres://` or `postgresql://` URL with a host. Runtime role `fundzim_app` | `sslmode=verify-full` required |
| `DATABASE_MIGRATION_URL` | empty | Same URL checks when set. Not used by the API process | `sslmode=verify-full` required when set |
| `DATABASE_MAX_CONNS` | `20` | integer 1–500 | — |
| `DATABASE_CONNECT_TIMEOUT` | `5s` | Go duration, 500ms–1m | — |

The pool is created **lazily**: a database that is down at startup does not stop the API; readiness reports
not-ready until it answers (§5 of [implementation.md](../stage-3/implementation.md)).

### 2.3 Redis (optional, never authoritative)

| Variable | Default | Validation | Non-local rule |
|---|---|---|---|
| `REDIS_URL` | empty (Redis disabled) | `redis://` or `rediss://` | `rediss://` (TLS) required |
| `REDIS_REQUIRED` | `false` | boolean (`strconv.ParseBool`: `true`/`false`/`1`/`0`…) | — |

`REDIS_REQUIRED=true` without `REDIS_URL` is a configuration error. With `REDIS_REQUIRED=true`, an unreachable
Redis at startup (2 s ping) is a **startup error**; with `false` the API logs a warning and continues. Stage 3
uses Redis only for a readiness check — the rate limiter is in-memory (see
[security-review.md](../stage-3/security-review.md)).

### 2.4 Object storage (three classes, three credentials, three buckets)

| Variable | Default | Validation |
|---|---|---|
| `STORAGE_ENDPOINT` | empty (storage disabled) | `http(s)` URL with a host. **Required outside local**; `https` required outside local |
| `STORAGE_REGION` | `us-east-1` | — (Garage locally uses `garage`) |
| `STORAGE_FORCE_PATH_STYLE` | `false` | boolean (Garage locally: `true`) |
| `STORAGE_PUBLIC_BUCKET` / `_ACCESS_KEY_ID` / `_SECRET_ACCESS_KEY` | empty | All three required when `STORAGE_ENDPOINT` is set. Class `PUBLIC_CAMPAIGN_MEDIA` |
| `STORAGE_KYC_BUCKET` / `_ACCESS_KEY_ID` / `_SECRET_ACCESS_KEY` | empty | All three required when `STORAGE_ENDPOINT` is set. Class `PRIVATE_IDENTITY_DOCUMENTS` |
| `STORAGE_EVIDENCE_BUCKET` / `_ACCESS_KEY_ID` / `_SECRET_ACCESS_KEY` | empty | All three required when `STORAGE_ENDPOINT` is set. Class `PRIVATE_COMPLIANCE_DOCUMENTS` |

Cross-checks when storage is enabled: the three buckets must be **three different buckets** (I-26) and the
three access key IDs must be **three different keys** (I-19). When storage is configured, all three classes are
readiness-critical.

### 2.5 Logging and rate limiting

| Variable | Default | Validation | Non-local rule |
|---|---|---|---|
| `LOG_FORMAT` | `json` | `json` or `text` | `json` required |
| `LOG_LEVEL` | `info` | `debug`, `info`, `warn`, `error` (case-insensitive) | `debug` refused in **production** (allowed in staging) |
| `RATE_LIMIT_ENABLED` | `true` | boolean | `false` refused |
| `RATE_LIMIT_RPS` | `20` | integer 1–10000 (tokens per second per client IP) | — |
| `RATE_LIMIT_BURST` | `40` | integer 1–100000 | — |

## 3. Production (non-local) refusals implemented in Stage 3

The API refuses to start in `staging` or `production` when any of these holds:

| Refused | Variable(s) |
|---|---|
| Database TLS not `verify-full` | `DATABASE_URL`, `DATABASE_MIGRATION_URL` (when set) |
| Plain-text Redis | `REDIS_URL` not `rediss://` (when set) |
| No object storage, or plain-HTTP storage | `STORAGE_ENDPOINT` empty or `http://` |
| Text logs | `LOG_FORMAT=text` |
| Rate limiting off | `RATE_LIMIT_ENABLED=false` |
| Non-HTTPS CORS origin | any `http://` entry in `CORS_ALLOWED_ORIGINS` |
| Debug logging (production only) | `LOG_LEVEL=debug` |

Always refused (every environment): missing/unknown `APP_ENV`, missing `DATABASE_URL`, `CORS_ALLOWED_ORIGINS=*`,
shared storage buckets or keys, `REDIS_REQUIRED=true` without `REDIS_URL`, `HTTP_PORT == INTERNAL_HTTP_PORT`,
out-of-range numbers and durations.

**Not yet implemented** (they arrive with the features they protect; SECURITY.md §14 remains the target list):
refusing `SMS_PROVIDER=log`, `MALWARE_SCAN_ENABLED=false`, `AUDIT_HASH_CHAIN_ENABLED=false`,
`FIELD_ENCRYPTION_PROVIDER=local`, the sandbox payment provider, and `__Host-` cookie enforcement. None of those
variables is read in Stage 3.

## 4. `fundzimctl` (migrations)

`fundzimctl migrate …` does **not** go through `config.Load`. It reads two variables directly:

| Variable | Use |
|---|---|
| `DATABASE_MIGRATION_URL` | **Required.** Connects as `fundzim_migrator` (the only role that runs DDL) |
| `APP_ENV` | `migrate down` is allowed only when `APP_ENV` is `development` or `test`; unset or any other value refuses it |

So `migrate up/status/version` work without `DATABASE_URL`. `fundzimctl healthcheck [url]` and
`fundzimctl version` read no configuration. Details: [migrations.md](migrations.md).

## 5. Web (`apps/web`) variables

| Variable | Where read | Default | Rule |
|---|---|---|---|
| `API_BASE_URL` | Server only, **per request** (route-handler proxy, Server Components) and at server start | Dev/test: `http://127.0.0.1:8080` | Origin only (no path, query, fragment or credentials), `http`/`https`. With `NODE_ENV=production` it is **required**: the server exits on start (`src/instrumentation.ts`) and the proxy answers `503 SERVICE_UNAVAILABLE` |
| `PORT`, `HOSTNAME` | Standalone server | `3000`, `0.0.0.0` in the image | — |
| `NODE_ENV` | Next.js | — | `production` in the image |
| `NEXT_PUBLIC_*` | Inlined into the browser bundle at build time | — | **None is used in Stage 3.** `.env.example` lists `NEXT_PUBLIC_APP_BASE_URL`, which no code reads yet. Never put a secret in a `NEXT_PUBLIC_` variable |

## 6. Variables in `.env.example` that Stage 3 does not read

| Group | Variables | Read from |
|---|---|---|
| Upload/scan | `UPLOAD_MAX_BYTES`, `MALWARE_SCAN_ENABLED` | Stage 5 |
| AUTH | `SESSION_*`, `STAFF_SESSION_*`, `STEP_UP_MAX_AGE`, `OTP_TTL`, `OTP_MAX_ATTEMPTS`, `CSRF_SECRET`, `WEBAUTHN_RP_ID`, `WEBAUTHN_RP_ORIGIN` | Stage 4 |
| EMAIL, SMS | `EMAIL_PROVIDER`, `SMTP_*`, `EMAIL_FROM`, `SMS_PROVIDER`, `SMS_API_KEY`, `SMS_SENDER_ID` | Stage 4 (fakes), Stage 15 (providers) |
| PAYMENTS | `PAYMENTS_*` | Stage 8+ |
| OBSERVABILITY | `OTEL_EXPORTER_OTLP_ENDPOINT`, `OTEL_SERVICE_NAME`, `ERROR_REPORTING_DSN` | Not wired in Stage 3 (no tracing exporter exists) |
| SECURITY | `FIELD_ENCRYPTION_*`, `BLIND_INDEX_KEY`, `AUDIT_HASH_CHAIN_ENABLED` | Stage 4/5 |
| Further pools | `DATABASE_WORKER_URL`, `DATABASE_KYC_URL`, `DATABASE_COMPLIANCE_URL`, `DATABASE_READONLY_URL` (commented) | Later stages; the API opens only the `fundzim_app` pool |

## 7. Development configuration

1. `./scripts/dev-env-init.sh` creates `.env` from `.env.example` (mode 600) and replaces every
   `<generate:password>`, `<generate:hex32>` and `<generate:garage-key-id>` marker with a fresh random value.
   `${NAME}` references (e.g. inside `DATABASE_URL`) are expanded from values generated earlier in the file.
   It **refuses to overwrite** an existing `.env`. `--print` writes the result to stdout instead (it contains
   secrets — local use only).
2. **Docker Compose** uses `.env` for interpolation only. `compose.yaml` builds the container-internal URLs
   itself and sets the API's environment explicitly (container hostnames, `APP_ENV=development`,
   `LOG_FORMAT=json`, `TRUSTED_PROXY_CIDRS=172.16.0.0/12`, `REDIS_REQUIRED=false`, Garage storage settings).
   Only the secrets, bucket names, `LOG_LEVEL` and host ports come from `.env`. Consequently, changing e.g.
   `RATE_LIMIT_RPS` in `.env` does **not** change the containerised API (it keeps the defaults).
3. **Host processes** (`go run ./apps/api/cmd/api`, `fundzimctl`, integration tests) need the variables in
   their environment: `set -a; . ./.env; set +a`. The `.env` defaults for host use are `HTTP_HOST=127.0.0.1`,
   `LOG_FORMAT=text`, `LOG_LEVEL=debug`, `TRUSTED_PROXY_CIDRS=127.0.0.1/32` and URLs pointing at the
   `127.0.0.1`-bound compose ports.
4. Compose-only variables (never read by FundZim code): `POSTGRES_SUPERUSER_PASSWORD`,
   `POSTGRES_{MIGRATOR,APP,WORKER,KYC,COMPLIANCE,READONLY}_PASSWORD` (used by
   `deploy/docker/postgres/init/10-roles.sh` on first start of an empty volume), `REDIS_PASSWORD`,
   `GARAGE_RPC_SECRET`, `GARAGE_ADMIN_TOKEN`, `*_HOST_PORT`, and the image build arguments `APP_VERSION`,
   `GIT_COMMIT`, `BUILD_TIME`.
5. If you change a `*_HOST_PORT`, also edit the matching host URL in `.env` (`DATABASE_URL`,
   `DATABASE_MIGRATION_URL`, `REDIS_URL`, `STORAGE_ENDPOINT`, `API_BASE_URL`): they contain the default port.
6. Database passwords are applied only when the Postgres volume is first initialised. Regenerating `.env`
   after that requires resetting the volumes (`make reset`, destructive).

## 8. Testing configuration

### 8.1 Unit tests

`go test ./...` and `npm --prefix apps/web test` need **no** environment. Config tests inject a map through
`config.Load(LookupFunc)`; the API tests build dependencies without a live database.

### 8.2 Integration tests (`-tags integration`)

`tests/integration` runs against the real local services. Load `.env` first
(`set -a; . ./.env; set +a`). Missing required variables **fail** the test (they are not skipped).

| Variable | Required by |
|---|---|
| `DATABASE_MIGRATION_URL` | Migration up / re-run / version tests |
| `DATABASE_URL` | Grants, append-only, outbox and readiness-query tests (as `fundzim_app`) |
| `POSTGRES_WORKER_PASSWORD` | Tests as `fundzim_worker` (URL derived from `DATABASE_URL` with the worker role) |
| `REDIS_URL` | Redis connectivity test |
| `STORAGE_ENDPOINT`, `STORAGE_REGION`, `STORAGE_{PUBLIC,KYC,EVIDENCE}_{BUCKET,ACCESS_KEY_ID,SECRET_ACCESS_KEY}` | Storage class, cross-bucket denial and anonymous-access tests (path-style addressing is forced) |
| `FUNDZIM_IT_API_URL` | Optional. Black-box API checks (e.g. `http://127.0.0.1:8080`); skipped when unset |
| `FUNDZIM_IT_WEB_URL` | Optional. Web → API proxy and homepage check (e.g. `http://127.0.0.1:3000`); skipped when unset |
| `FUNDZIM_IT_DESTRUCTIVE` | Optional. `1` runs a migration down-to-0 / up round trip. **Only on a throwaway database** (CI sets it) |

`make test-integration` defaults `FUNDZIM_IT_API_URL` and `FUNDZIM_IT_WEB_URL` to the local ports.

### 8.3 Web E2E (Playwright)

`playwright.config.ts` builds the standalone server and runs it with `PORT=${E2E_PORT:-3100}`,
`HOSTNAME=127.0.0.1`, `NODE_ENV=production` and `API_BASE_URL=http://127.0.0.1:1` (deliberately unreachable,
so the suite proves the pages degrade gracefully without the API).

### 8.4 CI

`.github/workflows/ci.yml` uses no repository secrets. The integration job runs `./scripts/dev-env-init.sh` to
generate throwaway secrets on the runner, starts the compose infrastructure, runs the API on the runner and
sets `FUNDZIM_IT_API_URL` and `FUNDZIM_IT_DESTRUCTIVE=1`. Go jobs set `GOTOOLCHAIN=local`.

## 9. Production configuration requirements

Stage 3 has no deployed environment. When one exists (Stages 18–20), the minimum the code enforces is §3.
In addition, by design (not code-enforced yet):

- Secrets come from a secret manager at runtime — never from a committed file, an image layer or a build
  argument. `dev-env-init.sh` and `.env` are local-only.
- `APP_ENV=production`; `HTTP_HOST=0.0.0.0` inside the container; the internal listener
  (`INTERNAL_HTTP_*`) reachable only from the monitoring network.
- `TRUSTED_PROXY_CIDRS` set to the reverse proxy's addresses only (not a whole private range).
- `/healthz` and `/readyz` are not routed publicly by the reverse proxy (OpenAPI `Health` tag).
- One credential per storage class from the secret manager / cloud IAM, each scoped to one bucket.
- `API_BASE_URL` for the web container set to the API's internal origin.

## 10. Brief names → implemented names

The Stage 3 brief and the Stage 2 design proposed some names that differ from the code:

| Proposed name | Implemented as | Why |
|---|---|---|
| `HTTP_HOST`, `HTTP_PORT` (brief) | `HTTP_HOST`, `HTTP_PORT` | Adopted as briefed |
| `API_LISTEN_ADDR` (Stage 2 design) | `HTTP_HOST` + `HTTP_PORT` | Brief governs (prerequisite-assessment §2) |
| `METRICS_LISTEN_ADDR` (Stage 2 handover §9) | `INTERNAL_HTTP_HOST` + `INTERNAL_HTTP_PORT` | Same host/port style as the public listener; the listener also serves detailed readiness |
| `SHUTDOWN_GRACE_PERIOD` (handover §8) | `SHUTDOWN_TIMEOUT` | Naming only |
| `S3_ENDPOINT` (brief) | `STORAGE_ENDPOINT` | `STORAGE_*` group per `.env.example` / ADR-008 |
| `S3_BUCKET` (brief, one bucket) | `STORAGE_PUBLIC_BUCKET`, `STORAGE_KYC_BUCKET`, `STORAGE_EVIDENCE_BUCKET` | **I-26:** three classes live in three buckets |
| `S3_ACCESS_KEY` / `S3_SECRET_KEY` (brief, one credential) | `STORAGE_{PUBLIC,KYC,EVIDENCE}_ACCESS_KEY_ID` / `_SECRET_ACCESS_KEY` | **I-19 (security control):** one shared key would reach both private classes. Each key reaches exactly one bucket |
| Other `S3_*` settings (region, path style) | `STORAGE_REGION`, `STORAGE_FORCE_PATH_STYLE` | Same group |
| `LOCAL_POSTGRES_SUPERUSER_PASSWORD` (Stage 2 proposal) | `POSTGRES_SUPERUSER_PASSWORD` | Compose-only |
| `LOCAL_MINIO_ROOT_USER/PASSWORD` (Stage 2 proposal) | `GARAGE_RPC_SECRET`, `GARAGE_ADMIN_TOKEN` | Garage replaced MinIO (MinIO community images are no longer published) |
| `WORKER_*`, `QUEUE_*`, `DATABASE_WORKER_URL` etc. (Stage 2 proposals) | Not implemented | No worker binary or job queue exists yet (carried to Stage 4) |
