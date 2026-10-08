# Stage 4 Interface Contracts

**Purpose:** the fixed interfaces between the Stage 4 work streams (platform worker/outbox/notifications,
platform rate limiting/client IP/idempotency, identity backend, frontend). They were agreed before
implementation; anything implemented must match them. A change needs the lead's agreement and an update here.

Everything is in the root Go module `github.com/Fatifizo/fundzim`. Go 1.27.1. New third-party dependencies are
pinned to exact released versions at least two weeks old, and each one is justified in the Stage 4 report.

---

## 1. Shared primitives (exist now)

| Package | API |
|---|---|
| `internal/platform/authz` | `Principal{UserID, SessionID, Kind (USER/STAFF), MFAVerifiedAt, StepUpAt, Permissions, EmailVerified}`, `WithPrincipal/PrincipalFrom`, `WithClientIP/ClientIPFrom` |
| `internal/platform/httpx` | `Router.Handle(pattern, policy, h, opts...)`; policies `Public()`, `Authenticated()`, `User()`, `Staff()`, `Permission(code)`; `Router.SetAuthorizer(Authorizer)`; route option `With(mws ...Middleware)` (runs after authorization, before the handler) |
| `internal/platform/clock` | `Clock` interface, `System`, `Fake` |
| `internal/platform/db` | `NewPool`, `Open`, `WithTx(ctx, pool, TxOptions, fn(ctx, tx))` (retries 40001/40P01), `InTx(ctx)`, `IsUniqueViolation(err, constraint)` |
| `internal/platform/config` | groups `Auth`, `Email`, `SMS`, `Security` added (see `config.go`); new env vars are listed in `.env.example` |

`db.InTx(ctx)` is true inside `WithTx`. **Any outbound network call (SMTP, SMS, HTTP) must refuse to run when
`db.InTx(ctx)` is true** and return an error.

---

## 2. Worker, River and outbox (work stream A)

### 2.1 River
- River (pinned version) in schema `queue`. Its migration SQL is committed verbatim as goose migration(s) in
  `migrations/` (timestamps after `20261008120300`), each ending with `CALL app.apply_runtime_grants();` if new
  tables need grants (the `queue` default privileges from the Stage 3 grants migration already cover tables the
  migrator creates there).
- `internal/platform/jobs`: builds an insert-only River client for the API process and a working client for the
  worker. Queues: `default`, `outbox`, `notifications`, `maintenance`. Max attempts, backoff, timeouts per job kind.
  Jobs that exhaust attempts are **discarded** by River (River's dead-letter state), counted in a metric, and
  logged at error level.

### 2.2 Outbox

```go
package outbox

type Event struct {
    AggregateType string      // e.g. "user"
    AggregateID   string      // UUID
    EventType     string      // e.g. "identity.email_verification_requested"
    Payload       any         // marshalled to a JSON object; IDs and minimal fields only, never secrets
    CorrelationID string      // request ID
    OccurredAt    time.Time
}

// Write inserts the event in the caller's transaction (app.outbox_events). Returns the event ID.
func Write(ctx context.Context, tx pgx.Tx, e Event) (string, error)

type Delivery struct {
    EventID, EventType, AggregateType, AggregateID, CorrelationID string
    Payload    json.RawMessage
    OccurredAt time.Time
    Attempt    int
}

// Handler processes one delivery. It runs OUTSIDE any database transaction, so it may call external
// services; it opens its own transactions as needed. Delivery is at least once: handlers must be idempotent
// or tolerate a duplicate.
type Handler func(ctx context.Context, d Delivery) error

type Registry struct{ /* ... */ }
func NewRegistry() *Registry
// Subscribe registers a consumer ("<module>.<purpose>", lower snake, dotted) for one event type.
func (r *Registry) Subscribe(consumer, eventType string, h Handler)
```

- **Relay** (River periodic job, every second): in one transaction, claim up to N undispatched rows with
  `FOR UPDATE SKIP LOCKED`, insert one River job `outbox.deliver{consumer, event_id}` per subscriber with
  `InsertManyTx` (River uniqueness on `(consumer, event_id)`), set `dispatched_at`. Events with no subscriber are
  marked dispatched.
- **Deliver job**: if `app.inbox_events (consumer, event_id)` already exists, finish; else load the event, run
  the handler, then insert the inbox row. A failing handler is retried by River with backoff; after the last
  attempt the job is discarded (alert).
- The worker purges dispatched outbox rows older than a retention window and `app.idempotency_keys` past
  `expires_at` (periodic maintenance jobs).

### 2.3 Worker binary
`apps/api/cmd/worker` (composition in `internal/app/worker.go`): config, `fundzim_worker` pool
(`DATABASE_WORKER_URL`), River client with the queues above, internal listener (`WORKER_INTERNAL_HTTP_PORT`,
default 9091) with `/healthz`, `/readyz` (DB, schema version, River running) and `/metrics`, graceful shutdown
(stop fetching, let running jobs finish within `SHUTDOWN_TIMEOUT`). Consumers are registered by
`func registerConsumers(d *WorkerDeps, reg *outbox.Registry)` in `internal/app/consumers.go`, which the identity
work stream fills.

### 2.4 Notifications (`internal/notifications`)

```go
type Email struct{ To, Subject, Text, HTML string } // one recipient; HTML optional
type EmailSender interface{ Send(ctx context.Context, m Email) error }
type SMS struct{ To, Body string }                  // To is E.164
type SMSSender interface{ Send(ctx context.Context, m SMS) error }

func NewEmailSender(cfg config.Email) (EmailSender, error)              // smtp | disabled
func NewSMSSender(cfg config.SMS, email EmailSender) (SMSSender, error) // dev_mailpit | disabled
```

- SMTP with timeouts (dial 5 s, total 15 s) and TLS modes `none|starttls|tls`. Never logs bodies, links or codes.
- `dev_mailpit` SMS delivers an email to `<e164-without-plus>@sms.dev.invalid` with subject `SMS to +263…`
  (masked) and the SMS body as text. It exists so codes never appear in logs.
- Both senders return an error if `db.InTx(ctx)`.
- Mailpit is a default compose service (`fundzim-mail`), SMTP `fundzim-mail:1025` in containers and
  `127.0.0.1:1025` on the host; its HTTP API on `127.0.0.1:8025` is used by integration tests to read messages
  (`GET /api/v1/messages`, `GET /api/v1/message/{ID}`).

---

## 3. Rate limiting, client IP, idempotency (work stream B)

### 3.1 Client IP
- `httpx.ClientIPMiddleware(trusted []netip.Prefix)`: resolves the client address once and stores it with
  `authz.WithClientIP`. Only `X-Forwarded-For` is considered, and only when the direct peer is in `trusted`.
  Walk the list right to left, skipping addresses inside `trusted`, and take the first untrusted one. Malformed
  entries stop the walk (the last trusted hop's view wins). `X-Real-IP` and `Forwarded` are ignored.
- Every component that needs the client IP (rate limits, audit, sessions) reads `authz.ClientIPFrom(ctx)`.
- Logs record IPs as before. Audit/session rows store the IP (C2) per PRIVACY.

### 3.2 Distributed rate limiting (`internal/platform/ratelimit`)

```go
type Policy struct {
    Name       string        // e.g. "auth.login.account"
    Limit      int           // events per Window
    Window     time.Duration
    Protective bool          // true for authentication throttles: on Valkey failure use the local fallback, never allow-all
}
type Decision struct {
    Allowed    bool
    Remaining  int
    RetryAfter time.Duration
    Degraded   bool // decided by the local fallback because Valkey was unavailable
}
type Limiter interface {
    Allow(ctx context.Context, p Policy, key string) (Decision, error)
}

// KeyFunc extracts the limiter key from a request (e.g. client IP, principal, principal-or-IP).
type KeyFunc func(r *http.Request) (key string, ok bool)
func ByClientIP(r *http.Request) (string, bool)
func ByPrincipal(r *http.Request) (string, bool)
func ByPrincipalOrIP(r *http.Request) (string, bool)
// Middleware applies p to the route; on deny it writes 429 RATE_LIMITED with Retry-After.
func Middleware(l Limiter, p Policy, key KeyFunc, logger *slog.Logger) httpx.Middleware
```

- Valkey implementation is atomic (a single Lua script, e.g. GCRA or a sliding window), keyed
  `rl:<policy>:<hex sha256(key)[:32]>` (no raw emails or IPs in Valkey), with TTLs.
- **Valkey unavailable:** `Protective` policies fall back to an in-process limiter with the same parameters
  (per-replica), and `Decision.Degraded = true`; the metric `fundzim_ratelimit_degraded_total{policy}` is incremented.
  Non-protective policies also use the fallback. Nothing ever fails open to "allow all".
- The coarse global per-IP limit stays (now on the distributed limiter, policy `global.ip`).
- Tests: atomicity under concurrency, **two limiter instances (two "replicas") sharing one Valkey enforce one
  global limit**, fallback when Valkey is stopped, keys hashed, spoofed `X-Forwarded-For` from an untrusted
  peer cannot change the key.

### 3.3 Idempotency (`internal/platform/idempotency`)

```go
type Mode int
const ( Optional Mode = iota; Required )
// Middleware: Idempotency-Key (UUID) scoped by scope + principal user ID (or "anon:"+client IP).
func Middleware(store *Store, mode Mode, scope string, logger *slog.Logger) httpx.Middleware
func NewStore(pool *pgxpool.Pool, clk clock.Clock, ttl time.Duration) *Store
func (s *Store) DeleteExpired(ctx context.Context) (int64, error)
```

Semantics (api-design idempotency section): the request fingerprint is SHA-256 of method, path and body.
- New key: insert `IN_PROGRESS` with a lease (`locked_until`), run the handler, store status and body
  (non-5xx responses only), mark `COMPLETED`. A 5xx deletes the record so the client can retry.
- Same key, same fingerprint, `COMPLETED`: replay the stored response with `Idempotent-Replayed: true`.
- Same key, `IN_PROGRESS` (lease not expired): `409 IDEMPOTENCY_REQUEST_IN_PROGRESS` with `Retry-After`.
- Same key, different fingerprint: `409 IDEMPOTENCY_KEY_REUSED`.
- `Required` mode without the header: `400 IDEMPOTENCY_KEY_REQUIRED`. Malformed key: `400 IDEMPOTENCY_KEY_INVALID`.
- Database uniqueness (`uq_idempotency_keys_scope_key`) is the guard. Tests include N concurrent requests with
  one key: exactly one handler execution.

---

## 4. HTTP API for authentication (identity stream implements; frontend consumes)

All under `/api/v1`, JSON, standard envelope. Same-origin through the web proxy.

### 4.1 Cookies and CSRF
| Cookie | Attributes | Meaning |
|---|---|---|
| `fz_session` locally, `__Host-fz_session` with https | HttpOnly, SameSite=Lax, Path=/, Secure with https | Opaque session token (SHA-256 stored server-side) |
| `fz_csrf` locally, `__Host-fz_csrf` with https | **not** HttpOnly, SameSite=Lax, Path=/, Secure with https | CSRF token bound to the session (HMAC of the session ID) |
| `fz_mfa` / `__Host-fz_mfa` | HttpOnly, SameSite=Strict, Path=/, Max-Age = MFA challenge TTL | Pending MFA challenge after a correct password (no session yet) |

- Every unsafe request (POST/PUT/PATCH/DELETE) whose `Origin` header is present must have
  `Origin == APP_PUBLIC_URL`, else `403 CSRF_ORIGIN_MISMATCH`. `Sec-Fetch-Site: cross-site` is rejected the same way.
- Every unsafe request that carries a session cookie must send `X-CSRF-Token` equal to the CSRF cookie and valid
  for that session, else `403 CSRF_TOKEN_INVALID`.
- The browser client reads the CSRF cookie and sends `X-CSRF-Token` on unsafe requests. Session secrets are
  never readable from JavaScript.

### 4.2 Endpoints

| Method & path | Policy | Body → response (`data`) | Notes |
|---|---|---|---|
| `GET /auth/session` | public | → `{authenticated: bool, user?: Me, mfa_pending?: bool}` | Never 401 |
| `POST /auth/register` | public | `{email, password, display_name, accept_terms: true}` → **202** `{status:"verification_sent"}` | Identical response whether or not the email exists |
| `POST /auth/verify-email` | public | `{token}` → `{status:"verified"}` | `400 TOKEN_INVALID` for unknown/expired/used tokens |
| `POST /auth/resend-verification` | public | `{email}` → **202** `{status:"verification_sent"}` | Generic |
| `POST /auth/login` | public | `{email, password}` → `{status:"authenticated", user: Me}` + cookies, or `{status:"mfa_required", methods:["totp","recovery_code"]}` + `fz_mfa` | `401 INVALID_CREDENTIALS` (generic), `403 ACCOUNT_SUSPENDED` (only after a correct password), `429 RATE_LIMITED` |
| `POST /auth/mfa/verify` | public (needs `fz_mfa`) | `{code}` → as successful login | `401 MFA_CODE_INVALID`, `401 MFA_CHALLENGE_EXPIRED` |
| `POST /auth/mfa/recovery` | public (needs `fz_mfa`) | `{recovery_code}` → as successful login | One-time codes |
| `POST /auth/logout` | authenticated | → **204** | Clears cookies |
| `POST /auth/logout-all` | authenticated | → **204** | Revokes every session of the user |
| `POST /auth/forgot-password` | public | `{email}` → **202** `{status:"reset_requested"}` | Generic |
| `POST /auth/reset-password` | public | `{token, new_password}` → `{status:"password_reset"}` | Revokes all sessions; `400 TOKEN_INVALID`; `422 PASSWORD_POLICY_VIOLATION` with `details` |
| `POST /auth/step-up/verify` | authenticated | `{password}` or `{code}` → `{step_up_expires_at}` | Refreshes step-up freshness |
| `GET /me` | authenticated | → `Me` | |
| `PATCH /me` | authenticated | `{display_name}` → `Me` | Optional `Idempotency-Key` |
| `POST /me/password` | authenticated | `{current_password, new_password}` → **204** | Revokes the user's other sessions |
| `POST /me/email-change` | user | `{new_email, password}` → **202** | Verification sent to the new address; old address notified |
| `GET /me/security` | authenticated | → `{email_verified, phone_verified, mfa_enabled, recovery_codes_remaining, password_changed_at, recent_events:[{type, occurred_at}]}` | |
| `GET /me/sessions` | authenticated | → `[{id, current, created_at, last_seen_at, ip_masked, user_agent, auth_method, mfa}]` | |
| `DELETE /me/sessions/{session_id}` | authenticated | → **204** | Own sessions only; others → 404 |
| `POST /me/mfa/enroll` | authenticated + step-up | → `{enrollment_id, secret, otpauth_uri}` | Secret returned once, never again |
| `POST /me/mfa/confirm` | authenticated + step-up | `{enrollment_id, code}` → `{recovery_codes: [10]}` | Codes returned once |
| `POST /me/mfa/disable` | user + step-up | `{code}` (TOTP or recovery code) → **204** | Staff cannot disable MFA (403 MFA_REQUIRED_FOR_ROLE) |
| `POST /me/mfa/recovery-codes` | authenticated + step-up | → `{recovery_codes}` | Regenerates (old batch superseded) |
| `POST /me/phone/verify-request` | user | `{phone}` → **202** `{expires_at}` | Zimbabwe default region; E.164 |
| `POST /me/phone/verify-confirm` | user | `{phone, code}` → `{phone_verified: true}` | |

`Me = {id, account_kind, email, email_verified, display_name, phone_masked, phone_verified, mfa_enabled, roles: [..] (staff only), created_at}`.

Errors use the standard envelope; `STEP_UP_REQUIRED` (403) tells the client to call `/auth/step-up/verify` and
retry. `AUTHENTICATION_REQUIRED` (401) means no valid session.

Organisation and staff/admin endpoints are listed in [rbac.md](rbac.md) and the OpenAPI contract.

---

## 5. Compose additions
- `fundzim-mail` (Mailpit) becomes a default service; `fundzim-worker` (same API image, entrypoint `worker`,
  `DATABASE_WORKER_URL`) depends on migrate.
- The compose network gets a fixed subnet so the API can trust exactly the web container's address for
  `X-Forwarded-For` (`TRUSTED_PROXY_CIDRS=<web IP>/32`). Host-originated requests reach the API from the
  Docker gateway, which is **not** trusted.
- The API may run as two replicas in tests (`fundzim-api-2`, profile `scale-test`) to prove the distributed limits.
