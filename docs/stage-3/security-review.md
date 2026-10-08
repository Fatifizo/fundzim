# Stage 3 Security Baseline Review

> **Scope:** the code and configuration on branch `stage-3/core-platform` — the Go API foundation, migrations,
> local container environment, web app and CI. Method: reading the implementation (file references below)
> against [SECURITY.md](../SECURITY.md), [THREAT-MODEL.md](../THREAT-MODEL.md),
> [design-baseline.md §12](../stage-2/design-baseline.md) and the
> [Stage 2 → 3 handover](../stage-handover/STAGE-2-TO-STAGE-3.md). This is an engineering review, not an audit,
> penetration test or certification, and it makes no compliance claim. Stage 3 has no authentication, no
> personal data and no money movement, which bounds what can be assessed.

Related: [implementation.md](implementation.md) · [configuration.md](../development/configuration.md) ·
[STAGE-3-TO-STAGE-4.md](../stage-handover/STAGE-3-TO-STAGE-4.md)

---

## 1. Controls in place

### 1.1 HTTP layer (`internal/platform/httpx`, `internal/app`)

| Control | Implementation | Assessment |
|---|---|---|
| Security headers | Every API response: `X-Content-Type-Options: nosniff`, `X-Frame-Options: DENY`, `Referrer-Policy: no-referrer`, `Content-Security-Policy: default-src 'none'; frame-ancestors 'none'`, `Cross-Origin-Resource-Policy: same-origin`, `Permissions-Policy`, `Cache-Control: no-store`. HSTS (2 years, `includeSubDomains`) outside development/test | Appropriate for a JSON API |
| CORS | Off by default (no headers emitted). When configured: explicit origins only, `*` refused by config, `https` required outside local, no `Access-Control-Allow-Credentials` | Matches the same-origin design (ADR-027) |
| Body limit | `HTTP_MAX_BODY_BYTES` (default 1 MiB): declared length checked up front (`413 PAYLOAD_TOO_LARGE`), streamed bodies capped by `http.MaxBytesReader`. The web proxy caps at 1 MiB too | Adequate |
| Timeouts | `ReadHeaderTimeout 5s`, `ReadTimeout 15s`, `WriteTimeout REQUEST_TIMEOUT+5s`, `IdleTimeout 120s`, `MaxHeaderBytes 64 KiB`; per-request context deadline `REQUEST_TIMEOUT`; graceful shutdown `SHUTDOWN_TIMEOUT` | Slowloris and runaway requests bounded |
| Rate limiting | In-memory token bucket per client IP (20 rps, burst 40 by default), `429 RATE_LIMITED` + `Retry-After`; refused as disabled outside local; probes exempt | Baseline only — see F-01, F-02, F-05 |
| Client IP / request ID | `X-Forwarded-For` and inbound `X-Request-ID` honoured only from `TRUSTED_PROXY_CIDRS` peers; request ID format-checked (`^[A-Za-z0-9._:-]{8,64}$`) to prevent log-field forgery; otherwise UUIDv7 | Correct design; deployment values matter (F-03) |
| Panic handling | Recovery middleware returns a generic `500 INTERNAL_ERROR` envelope; panic value and stack go to the log only; counter metric | No stack or internals reach clients |
| Error redaction | `errs.As` maps unknown errors to `INTERNAL_ERROR` with a fixed message; causes are logged, never returned. Public readiness returns only `ok`/`unavailable` (or `SERVICE_UNAVAILABLE`); a unit test asserts no host, role, password, port or dependency name leaks | Good |
| Deny by default | Every route needs an explicit policy or the process panics at startup; only `PolicyPublic` exists (5 routes) | Framework ready for Stage 4 policies |
| Internal listener | Metrics and detailed readiness on a separate listener (default `127.0.0.1:9090`); in compose bound to `0.0.0.0` inside the container but published only on host `127.0.0.1` | See F-12 |

### 1.2 Logging (`internal/platform/logging`)

Structured `slog` with `service`, `env`, `version`. Request/response bodies are never logged. A `ReplaceAttr`
hook redacts the **value** of any attribute whose **key** matches
`pass(word|wd)?|secret|token|authorization|cookie|session|otp|api[_-]?key|private[_-]?key|credential|card|id_number|passport|account_number|dsn|database_url|redis_url|(^|[_-])(pan|cvv|pin)([_-]|$)`
(case-insensitive), at any group depth. Access logs use route templates, not raw paths, so IDs in URLs do not
reach logs or metric labels. Debug level is refused in production; JSON format required outside local.
Residual risk: F-08.

### 1.3 Secrets handling

| Control | Implementation |
|---|---|
| `config.Secret` | DB URLs, Redis URL and storage keys render `[REDACTED]` via `String`, `GoString`, `LogValue`, `MarshalJSON`, `MarshalText`; `Reveal()` only at the client-library boundary. Config errors list variable **names** only; `db.NewPool` and `cache.Open` return errors without the URL |
| `.env` generation | `scripts/dev-env-init.sh` generates random per-machine values (32-char passwords, 32-byte hex, Garage key IDs) from `/dev/urandom`, writes `.env` with `umask 077` (mode 600), never overwrites. `.env.example` holds placeholders/markers only |
| Git hygiene | `.gitignore` excludes `.env`, `.env.*` (except `.env.example`), `*.env`, key/cert files, `secrets/`; `apps/web/.dockerignore` excludes `.env*` and `*.pem` from the web build context |
| Scanning | `scripts/check-secrets.sh` (forbidden files + credential patterns) locally and in CI; gitleaks v8.30.1 over full history in CI, downloaded with SHA-256 verification |
| gitleaks config | `.gitleaks.toml` extends the default rules with narrow line allowlists: UUID-valued example `Idempotency-Key` headers in the OpenAPI contract, `valkey/valkey:<tag>` image references, and one prose line in `docs/architecture/observability.md`; `node_modules/` and `.next/` paths are ignored |
| Reviewed false positives | `.gitleaksignore` lists three fingerprints in commit `fe9cfa7`: the observability.md prose line, a deliberately fake provider-style key in `design/sql/tests/payments_test.sql` (rewritten in Stage 3 so it no longer matches) and an example UUID `Idempotency-Key` in the OpenAPI contract. Each was reviewed as not a credential |
| CI | No repository secrets; the integration job generates throwaway values on the runner |

### 1.4 Database least privilege

- **Roles** (`deploy/docker/postgres/init/10-roles.sh`): `fundzim_migrator` owns the database and the `public`
  schema and runs DDL; it is **not** a superuser and has no `CREATEDB`/`CREATEROLE`. Runtime roles
  `fundzim_app`, `fundzim_worker`, `fundzim_kyc`, `fundzim_compliance`, `fundzim_readonly` have `LOGIN` only,
  role-level `statement_timeout`/`lock_timeout`/`idle_in_transaction_session_timeout`, and `readonly` defaults
  to read-only transactions. `CONNECT` revoked from `PUBLIC`; `CREATE` on `public` revoked from `PUBLIC`.
  Nothing in the application connects as a superuser.
- **Grants procedure** (`app.apply_runtime_grants()`): privileges are derived from the catalogue and reset on
  every call. `fundzim_app`: no access to `kyc` or `compliance`; `SELECT` only on reference tables
  (`status_transitions`, `currencies`, `markets`); `SELECT, INSERT` only on `audit` and `ledger`; no
  `UPDATE` where an append-only trigger exists; `DELETE` only on an allow-list of ephemeral tables; no DDL;
  `SELECT` only on `goose_db_version`. `fundzim_worker` = `fundzim_app` + `EXECUTE audit.verify_chain`
  (worker-only). Routines are not executable by `PUBLIC` by default. New tables get nothing until the
  procedure is called again (fail closed).
- **Append-only enforcement in the database:** `audit.audit_events`, `audit.security_audit_events`,
  `app.feature_flag_changes` and `app.status_transitions` reject `UPDATE`/`DELETE` (and the first three
  `TRUNCATE`) through `app.forbid_mutation` triggers — independent of grants, so they also bind the table owner.
  Audit rows are SHA-256 hash-chained (`audit.chain_append`, verification by `audit.verify_chain`).
  `app.outbox_events` content is immutable and undispatched rows cannot be deleted; `app.idempotency_keys`
  identity is immutable and completed records are frozen; `app.currencies` rows are never deleted and verified
  minor units are frozen; feature-flag changes are maker ≠ checker by CHECK and deferred trigger.
- **TLS:** `sslmode=disable` only in development/test; `verify-full` required otherwise (config refusal).

### 1.5 Object storage

Three classes → three buckets → three credentials (I-19 as amended by I-26). Each Garage key is granted
read/write (not owner) on exactly one bucket by `deploy/docker/garage/init.sh`. The API builds one S3 client
per class from that class's credential only; keys are random UUIDv7 under the class prefix and a key outside
the prefix (or containing `..`) is refused. No public or presigned URLs exist in Stage 3. Config refuses shared
buckets or shared keys. Integration tests assert that each credential is denied write **and** list on the other
two buckets and that anonymous GETs on the private buckets do not return 200.

### 1.6 Containers and local network

- API image: distroless `static-debian12:nonroot` pinned by digest, uid `65532`, no shell or package manager,
  static binary (`CGO_ENABLED=0`, `-trimpath`), healthcheck through `fundzimctl`. CI asserts the user.
- Web image: official Node slim image, runs as `node` (uid 1000), application files root-owned and read-only
  except `.next/cache`; no build args or secrets; `API_BASE_URL` is a runtime, server-only variable.
- Every published port binds to `127.0.0.1` (web 3000, API 8080, internal 9090, Postgres 5432, Valkey 6379,
  Garage S3 3900, Mailpit 8025/1025). The Garage admin (3903) and RPC (3901) ports are not published.
- Valkey requires a password and persists nothing. Compose services have memory limits and log rotation.

### 1.7 Web app

Same-origin proxy with a fixed upstream origin (no SSRF), hop-by-hop and client forwarding headers stripped,
body cap, timeout, no automatic retry of mutations (client and proxy), validated request IDs. Server-only API
configuration (`server-only` import guard, no `NEXT_PUBLIC_*` used). Security headers on every route
(CSP, nosniff, `X-Frame-Options: DENY`, referrer policy, permissions policy, COOP), `X-Powered-By` removed.
Production refuses to start without a valid `API_BASE_URL`. Site-wide `noindex` and `robots.txt` disallow.
`/campaigns/[slug]` never renders the slug.

### 1.8 CI supply chain

`permissions: contents: read`; third-party actions pinned by commit SHA; `govulncheck` (pinned `v1.8.0`);
`npm audit --omit=dev --audit-level=high` blocking; full-history gitleaks; images built in CI.

## 2. Findings and residual risks

Severity is for the current state (local development only, no users, no money). "Follow-up" names the stage in
which the item must be closed at the latest.

| ID | Finding | Severity | Follow-up |
|---|---|---|---|
| F-01 | **Rate limiter is in-memory per process.** With N API replicas the effective limit is N × the configured rate, and it resets on restart. Redis is not used for rate limiting (no Redis-backed limiter exists) | Medium | Stage 4: per-route limits for OTP/login need a shared store (Redis-backed limiter with the in-memory fallback) before auth endpoints are exposed |
| F-02 | **Rate limiting through the web proxy collapses to one key.** The route-handler proxy strips client `X-Forwarded-For` (correctly — it must not trust the browser) but sets no forwarded address, so the API sees the web container's IP for every browser request; with the default limits all browser traffic via the proxy shares one 20 rps bucket. Related: the proxy accepts request IDs of 1–128 characters while the API accepts 8–64, so short or long IDs are replaced by the API and the two logs no longer correlate | Medium | Stage 4 (before login/OTP limits matter): the proxy must forward the real client address it received from a trusted hop (or the reverse proxy routes `/api/v1` directly to the API, ARCHITECTURE §3), and the request-ID patterns must be aligned |
| F-03 | **Compose trusts `172.16.0.0/12`.** Host-originated requests to the published API port arrive from the Docker gateway inside that range, so any local process can set `X-Forwarded-For`/`X-Request-ID`. Local only | Low | Stage 18: deployed environments set `TRUSTED_PROXY_CIDRS` to the reverse proxy's exact addresses |
| F-04 | **Web CSP allows `'unsafe-inline'` scripts and styles** (nonces conflict with Partial Prerendering under `cacheComponents`). No third-party origins, no `unsafe-eval` in production, `object-src`/`base-uri`/`frame-ancestors` locked. No user-generated content is rendered in Stage 3 | Medium | **Before Stage 7** renders owner-supplied content: nonce via `proxy.ts` on dynamic routes or script hashes |
| F-05 | **Readiness probes on the public listener.** `/readyz` is exempt from rate limiting. **Mitigated in Stage 3:** public readiness reuses one check result per second (`health.Checker.RunCached`), so probe traffic cannot multiply dependency load; `/internal/readiness` always runs fresh checks. | Low | Stage 18: keep probes off the public proxy. |
| F-06 | **npm audit reports high-severity advisories in development dependencies** (`eslint-config-next` chain). Production dependencies are gated (blocking); the full audit is warning-only in CI | Low | Tracked by the stage lead in the Stage 3 known-issues record; re-evaluate on each `eslint-config-next` update, at the latest Stage 18 |
| F-07 | **Integration tests write synthetic audit rows into the local development database** when run against the compose stack; append-only triggers make them undeletable. They contain no personal data | Informational | Reset volumes to clear (`make reset` / `docker compose down -v`); [seed-data.md §3](../development/seed-data.md) |
| F-08 | **Log redaction is key-based only.** A secret inside a message string, an `error` value (e.g. a driver error) or under an innocuous key is not redacted; readiness failures log raw error strings. Over-matching keys (e.g. anything containing `card` or `session`) is acceptable | Medium | Stage 4 (before passwords, OTPs and session tokens exist in the process): value-pattern tests for error strings, an explicit allow-list for logged fields per observability.md §2, and tests that auth secrets never reach logs |
| F-09 | **Repository-root `.dockerignore`. Fixed in Stage 3:** it excludes `.env*` (except `.env.example`), `.git`, `node_modules`, `apps/web`, `design` and `docs`, so local secrets never reach the Docker daemon for the API image. | Closed | — |
| F-10 | **Image pinning is partial.** API runtime pinned by digest; Go build image, Node image and all compose images pinned by exact tag only | Low | Stage 18: digest pinning plus automated update process |
| F-11 | **Security tooling proposed in the handover is not in CI:** golangci-lint (incl. gosec), SAST, Trivy image scanning, the architecture import-graph test, CODEOWNERS | Medium | Stage 4 (early, carried over — see the Stage 4 handover) |
| F-12 | **Internal listener exposure depends on deployment.** In compose it listens on `0.0.0.0` inside the container (reachable from other containers on the network); `/internal/readiness` returns raw error strings | Low | Stage 18: internal port reachable only from the monitoring network |
| F-13 | **Request timeout is cooperative.** The middleware sets a context deadline; a handler that ignores its context runs until the server `WriteTimeout` | Low | Ongoing review rule: every handler and dependency call honours `ctx` |
| F-14 | **Local object storage has no server-side encryption**, single-node Garage, admin token in `.env` | Informational (local data is synthetic) | Stage 18 / ADR-009: SSE-KMS per private bucket in deployed environments |
| F-15 | **Unused privileged credentials exist locally:** `fundzim_kyc`, `fundzim_compliance`, `fundzim_readonly` have `LOGIN` and generated passwords but no application uses them; `fundzim_readonly` has no grants yet | Informational | Their pools arrive with their stages (5, 13, 17) |
| F-16 | **No authentication or authorisation beyond `PolicyPublic`.** Expected for Stage 3 — every route is public by explicit decision | Informational | Stage 4 |

No finding involves real personal data, real credentials or money movement; none exists in Stage 3.

## 3. Items verified absent

- No secret, token or real credential in tracked files is known to this review beyond the reviewed false
  positives in §1.3 (the scans are the authority; their results are recorded by the stage lead).
- No `NEXT_PUBLIC_*` variable carries configuration.
- No code path builds public or presigned object URLs.
- No endpoint accepts a request body.

## 4. Follow-up summary by stage

| Stage | Items |
|---|---|
| 4 (early) | F-01 shared rate-limit store for auth routes · F-02 proxy client-IP forwarding (request-ID limits already aligned in Stage 3) · F-08 redaction hardening and tests · F-11 golangci-lint/gosec, Trivy, architecture test, CODEOWNERS |
| 7 | F-04 nonce- or hash-based CSP before owner content is rendered |
| 18 | F-03 exact trusted proxy CIDRs · F-05 probes not public · F-06 dependency audit exception review · F-10 digest pinning · F-12 internal port isolation · F-14 storage SSE-KMS |
