# Stage 3 → Stage 4 Handover: Authentication, Identity and Authorization

**From:** Stage 3 — Core Platform Foundation (code on branch `stage-3/core-platform`).
**To:** Stage 4 — Authentication & Identity ([ROADMAP.md](../ROADMAP.md)).
**Date:** 2026-10-08. Stage 4 must not start until the project owner explicitly asks for it and the
prerequisites in §10 are met. This document is an index and checklist; where it differs from a linked
specification, **the specification wins**.

Inputs: [stage-3/implementation.md](../stage-3/implementation.md) (what exists) ·
[stage-3/security-review.md](../stage-3/security-review.md) (findings to close) ·
[security/authentication-authorization.md](../security/authentication-authorization.md) ·
[ADR-027](../adr/ADR-027-authentication-session-strategy.md) · [api/authorization-matrix.md](../api/authorization-matrix.md) ·
[database/identity-schema.md](../database/identity-schema.md) · [database/migration-plan.md](../database/migration-plan.md) Stage 4 ·
`design/sql/0004_users_auth.sql` · [design-baseline.md §12](../stage-2/design-baseline.md) (I-1, I-4, I-6, I-10, I-12, I-16) ·
[STAGE-2-TO-STAGE-3.md](STAGE-2-TO-STAGE-3.md) (items carried over, §6 below)

---

## 1. Stage 4 objective (from ROADMAP)

User accounts (phone/email), OTP login (hashed, short TTL, rate-limited), optional password (Argon2id),
server-side sessions (`__Host-` cookies, rotation, idle and absolute timeouts), CSRF protection, staff accounts
with mandatory MFA (WebAuthn, TOTP fallback; no SMS/email OTP for staff, I-6), the permission model (roles →
permissions, ownership checks), account recovery, device/session management and auth audit events. OTP
delivery uses only the `log` SMS fake and Mailpit. **Acceptance:** no endpoint reachable without an explicit
authorisation decision; staff cannot log in without MFA; security tests pass.

## 2. HTTP middleware: what exists and where auth slots in

Current chain (`internal/app/routes.go` `PublicHandler`, outermost first):

| # | Middleware | Status |
|---|---|---|
| 1 | Request ID (`httpx.RequestIDMiddleware`) | exists |
| 2 | Access log + metrics (`httpx.AccessLog`) | exists |
| 3 | Panic recovery (`httpx.Recover`) | exists |
| 4 | Security headers (`httpx.SecurityHeaders`) | exists |
| 5 | CORS (`httpx.CORS`, off unless configured) | exists |
| 6 | Body limit (`httpx.BodyLimit`) | exists |
| 7 | Timeout (`httpx.Timeout`) | exists |
| 8 | Coarse per-IP rate limit (`httpx.RateLimit`, in-memory) | exists |
| 9 | **Session authentication** — resolve the `__Host-fz_session` cookie (locally `fz_session`, see §5) to a principal; anonymous when absent; never 401 here | **Stage 4** |
| 10 | **CSRF** — for unsafe methods with a session cookie: Origin/Fetch-Metadata check + `X-CSRF-Token` | **Stage 4** |
| 11 | **Per-route rate limit** — keyed by principal and the route's rate-limit class (`x-fundzim-rate-limit-class` in the contract), Redis-backed with the in-memory fallback | **Stage 4** (OTP/login need it) |
| 12 | **Idempotency** — `Idempotency-Key` on routes flagged in the contract, backed by `app.idempotency_keys` | carried over (§6) |
| — | **Policy enforcement** at the router: `httpx.Router.Handle(pattern, policy, h)` already requires a policy and panics without one; only `PolicyPublic` exists. Add `Authenticated`, `Staff` (MFA complete), `Permission(<name>)`, `Webhook`, `Internal` and enforce them in the router wrapper before the handler; ownership (ABAC) checks stay in the service layer | **Stage 4** |

Keep the coarse per-IP limiter **before** session lookup (cheap rejection before database work). Note the
Stage 3 order differs from [ARCHITECTURE §5](../ARCHITECTURE.md) in one place: access log is outside panic
recovery so recovered panics are logged with status 500. Either keep this and update ARCHITECTURE §5, or
change the code — do not leave them silently different.

Before the auth middleware ships, close [security-review](../stage-3/security-review.md) **F-02**: the web
route-handler proxy strips `X-Forwarded-For`, so all browser traffic through it reaches the API from one
address and would share one login/OTP rate-limit bucket. Also align the request-ID formats (web accepts 1–128
characters, API 8–64).

## 3. Database infrastructure available

| Item | State |
|---|---|
| Roles | `fundzim_migrator` (DDL), `fundzim_app` (API runtime), `fundzim_worker` (= app + worker-only routines), `fundzim_kyc`, `fundzim_compliance`, `fundzim_readonly`; created with `LOGIN` locally by `deploy/docker/postgres/init/10-roles.sh`, with role-level timeouts |
| Pools | One pgx pool as `fundzim_app` (`DATABASE_URL`), lazy, max 20. No `WithTx` helper, no in-transaction context marker, no sqlc yet — add them before the first domain store |
| Schemas | `app queue ledger audit kyc risk compliance recon` exist |
| Shared SQL | `app.forbid_mutation`, `set_updated_at`, `keep_created_at`, `guard_transition` + `app.status_transitions`, `forbid_column_change`, `allow_only_column_changes`, `set_once_columns`, `bump_version`; `audit.chain_append`, `audit.verify_chain` |
| Tables | `app.currencies`, `app.markets`, `app.idempotency_keys`, `app.outbox_events`, `app.inbox_events`, `app.feature_flags`, `app.feature_flag_changes`, `audit.audit_events`, `audit.security_audit_events` |
| Grants | `app.apply_runtime_grants()` derives runtime privileges; **every Stage 4 migration that creates tables ends with `CALL app.apply_runtime_grants();`**. The procedure already lists `app.sessions` and `app.otp_challenges` as deletable ephemeral tables and `app.permissions` as reference data. Decide explicitly whether `app.roles` and `app.role_permissions` are reference-only (seeded by migration) and update the procedure in the same migration if so |
| Readiness | API is not ready until the DB version equals the embedded migration version |
| Guide | [development/migrations.md](../development/migrations.md) (naming, `SET LOCAL` timeouts inside the Up section, StatementBegin/End, staged FKs, review checklist) |

## 4. Required identity schema migrations

Port from `design/sql/0004_users_auth.sql` (and the notification subset of `0011`) in the split given by
[migration-plan.md](../database/migration-plan.md) Stage 4:

| # | Migration | Tables |
|---|---|---|
| 4.0 | `create_users` | `app.users` (+ `user_account` transition edges), `app.user_profiles` (avatar column **without** FK until Stage 5); FK `fk_feature_flag_changes_approved_by` → `app.users` added `NOT VALID`, then `VALIDATE` (I-16) |
| 4.1 | `create_user_contacts` | `app.user_emails`, `app.user_phone_numbers` |
| 4.2 | `create_auth_credentials` | `app.mfa_methods`, `app.authentication_identities`, `app.password_credentials`, `app.recovery_codes` |
| 4.3 | `create_auth_otp_sessions` | `app.otp_challenges`, `app.sessions` |
| 4.4 | `create_rbac` (two reviewers) | `app.roles`, `app.permissions`, `app.role_permissions` (seeds incl. `BUSINESS_APPROVER`), `app.role_assignment_requests` (+ edges), `app.role_assignments` |
| 4.5 | `bootstrap_super_admins` (two reviewers) | One-off ceremony (two named people, maker ≠ checker), recorded in `audit.security_audit_events` |
| 4.6 | `create_security_events` | `app.security_events` (partitioned monthly) |
| 4.7 | `create_notifications_core` | `app.notification_templates`, `app.notification_jobs` (+ edges), `app.notification_attempts`; seed OTP/security templates |

Foreign keys on Stage 3 columns:

- `app.feature_flag_changes.approved_by` → `app.users(id)`: add `NOT VALID`, then `VALIDATE` (no `NOT NULL`;
  approval is optional for flags without `requires_approval`).
- `app.feature_flag_changes.requested_by`: **no FK** in the design (`0004_users_auth.sql`): the migration seed
  row uses the all-zero system actor UUID, which is not a user. If Stage 4 wants this FK, it needs a design
  decision (e.g. a system-actor row in `app.users`) recorded in the migration plan first.
- `audit.audit_events.actor_id` / `on_behalf_of` and `audit.security_audit_events.actor_id`: **no FK by
  design** — audit rows accept system/provider/vendor actors and must outlive account anonymisation.

Also at the start of Stage 4, a Go `audit` module (`Recorder.Record(ctx, tx, Event)`) must exist before the
first auth event is written; no Go code writes audit rows yet.

## 5. Configuration already reserved

`.env.example` contains placeholders that **no code reads yet** — Stage 4 adds them to
`internal/platform/config` with validation and non-local refusals ([configuration.md §6](../development/configuration.md)):

| Group | Variables | Refusals to implement |
|---|---|---|
| AUTH | `SESSION_COOKIE_NAME` (`fz_session` locally), `SESSION_IDLE_TIMEOUT` (168h), `SESSION_ABSOLUTE_TIMEOUT` (720h), `STAFF_SESSION_IDLE_TIMEOUT` (15m), `STAFF_SESSION_ABSOLUTE_TIMEOUT` (12h), `STEP_UP_MAX_AGE` (10m), `OTP_TTL` (5m), `OTP_MAX_ATTEMPTS` (5), `CSRF_SECRET` (generated), `WEBAUTHN_RP_ID`, `WEBAUTHN_RP_ORIGIN` | Non-local: cookie name must carry the `__Host-` prefix; `CSRF_SECRET` present and long enough; `WEBAUTHN_RP_ORIGIN` https |
| EMAIL | `EMAIL_PROVIDER=smtp`, `SMTP_*` (Mailpit `localhost:1025`), `EMAIL_FROM` (`.invalid` domain) | Real providers arrive in Stage 15 |
| SMS | `SMS_PROVIDER=log`, `SMS_API_KEY`, `SMS_SENDER_ID` | `SMS_PROVIDER=log` refused in production (SECURITY §14) |
| SECURITY | `FIELD_ENCRYPTION_PROVIDER`, `FIELD_ENCRYPTION_LOCAL_KEY`, `BLIND_INDEX_KEY`, `AUDIT_HASH_CHAIN_ENABLED` | `local` provider and disabled hash chain refused outside local |

Timeout values are starting points; [SECURITY.md](../SECURITY.md) and
[authentication-authorization.md §2.2](../security/authentication-authorization.md) are the source of truth.
Mailpit runs with `docker compose --profile tools up -d fundzim-mail` (UI on `127.0.0.1:8025`).

## 6. Carried over from the Stage 2 → 3 handover (do early in Stage 4)

These were in the Stage 2 → 3 blueprint but are not implemented ([implementation.md §13](../stage-3/implementation.md)):

| Item | Why it matters for Stage 4 |
|---|---|
| **Root `.dockerignore`, golangci-lint (incl. gosec), architecture import-graph test, Trivy, CODEOWNERS** | Stage 4 adds the first security-critical modules; the module dependency rules must be enforced before `auth` and `users` exist |
| **`platform/db.WithTx`** (retry on serialisation/deadlock, in-tx marker) and sqlc setup | Every auth write is transactional with its audit event |
| **River job queue** (pinned version; default privileges for `queue` first, then River's migration SQL committed verbatim, run by goose) and `platform/jobs` | OTP delivery and security notifications are background jobs |
| **Worker binary** (`apps/api/cmd/worker`, `fundzim_worker` pool via `DATABASE_WORKER_URL`, internal listener, health/readiness) | Runs the jobs above |
| **Outbox writer + dispatcher, inbox `Consume`** | Auth events → notifications without dual writes |
| **Idempotency store + middleware** (`app.idempotency_keys` exists) | Four `/me` profile operations accept an optional `Idempotency-Key` (`x-fundzim-idempotency: optional`); 35 operations in later stages require it |
| **OpenTelemetry** tracing (exporter disabled when `OTEL_EXPORTER_OTLP_ENDPOINT` is empty) | Optional for Stage 4 acceptance; correlation is by `X-Request-ID` today |
| **Security-review findings** F-01, F-02, F-08, F-09, F-11 | See [security-review.md §4](../stage-3/security-review.md) |
| Mark `GET /healthz` and `GET /readyz` `x-fundzim-status: IMPLEMENTED` in the contract | They are implemented but unmarked |

## 7. Frontend

- Routes: `/login` and `/register` (Stage 4 "coming soon" pages) and `/dashboard` (Stages 4–6) exist as
  placeholders in `apps/web/src/app/`. Navigation is in `src/lib/navigation.ts`.
- API access: the browser calls same-origin `/api/v1/*`; `src/app/api/v1/[...path]/route.ts` forwards cookies,
  `Origin` and a validated `X-Request-ID`, passes multiple `Set-Cookie` headers and redirects through, and never
  retries. Server Components use `getServerApi()` (`src/lib/api/server.ts`, `server-only`).
- Client (`src/lib/api/client.ts`): envelope parsing, `ApiError` with `code`/`requestId`/`retryable`, retries
  only GET/HEAD. Stage 4 adds: forwarding the session cookie from Server Components when a call needs the
  user, sending `X-CSRF-Token` on unsafe methods, and handling `401`/`403` codes. Session cookies stay
  HttpOnly; no token is ever stored in JavaScript.
- Generate endpoint DTOs from the OpenAPI contract when Stage 4 needs them (approach in
  `apps/web/README.md`), keeping `types.ts` for the envelope.
- Next.js 16 renamed `middleware` to `proxy` (`proxy.ts`); read `apps/web/AGENTS.md` and the bundled docs before
  adding route protection. The CSP is static with `'unsafe-inline'` (security-review F-04); login forms must not
  rely on inline event handlers.

## 8. Testing infrastructure available

| Layer | How |
|---|---|
| Go unit | `go test -race ./...` (`make test-go`); `internal/app` tests run the real handler chain with an unreachable DB |
| Fuzz | `go test -fuzz=… ./internal/platform/money/` (CI runs 20 s smoke per target) |
| Integration | `//go:build integration` in `tests/integration`, against the compose services as the **real roles** (`fundzim_app`, `fundzim_worker`): migrations, grants, append-only, storage separation, optional black-box API/web checks (`make test-integration`) — extend it with users/sessions/RBAC invariants and role-boundary tests |
| Web | Vitest + Testing Library + axe (`npm test`), Playwright E2E against the standalone build (`npm run test:e2e`) |
| CI | `.github/workflows/ci.yml` jobs `go`, `web`, `e2e`, `contracts`, `integration`, `images`, `secrets` |

Stage 4 adds per ROADMAP: auth flow integration tests, rate-limit tests, CSRF tests, the authorisation matrix
(every role × permission, deny by default), session expiry tests (inject the clock — there is no
`platform/clock` package yet; create one).

## 9. Recommended implementation order

1. Owner prerequisites (§10).
2. Carried-over guards: root `.dockerignore`, golangci-lint, architecture test, CODEOWNERS, Trivy (§6).
3. `platform/clock`, `platform/db.WithTx`, sqlc, `audit` Recorder (Go), `platform/idempotency`.
4. River migrations + `platform/jobs` + worker binary + outbox/inbox; then OTel if in scope.
5. Config groups AUTH/EMAIL/SMS/SECURITY with refusals and tests.
6. Migrations 4.0 – 4.3 (users, contacts, credentials, OTP, sessions) with grants and integration tests.
7. Session middleware, CSRF, router policies, Redis-backed per-route rate limits; fix web proxy client IP (F-02).
8. OTP login (user), optional password (Argon2id parameters measured on target hardware), recovery.
9. Migrations 4.4 – 4.7 (RBAC, super-admin bootstrap ceremony, security events, notification core).
10. Staff login with mandatory WebAuthn/TOTP, step-up, `BUSINESS_APPROVER` separation (I-10).
11. Web login/register/session pages; session list and revocation.
12. Authorisation-matrix and deny-by-default tests; Stage 4 report; stop.

## 10. Prerequisites

| # | Prerequisite | State at handover |
|---|---|---|
| P1 | **Recorded acceptance** of Stage 1, Stage 2 and Stage 3 | None recorded (Stage 1 and 2 NOT_VERIFIED in [stage-3/prerequisite-assessment.md](../stage-3/prerequisite-assessment.md); Stage 3 awaiting acceptance) |
| P2 | Docker access for the dev user (`sudo usermod -aG docker $USER`, then re-login or `sg docker`) | Being set up by the owner |
| P3 | GitHub Actions confirmed running on the repository; branch protection on `main` | NOT_VERIFIED |
| P4 | Decisions listed in [authentication-authorization.md §9](../security/authentication-authorization.md): CAPTCHA/proof-of-work choice, Argon2id parameters, permission seed gaps; SMS prefix allow-list (LR-031) and staff vetting (LR-089) stay `LEGAL_REVIEW_REQUIRED` | Open |
| P5 | Risk-policy decision on SIM swap and phone-number recycling (ROADMAP Stage 4 security considerations) | Open |
| P6 | `make` installed (optional; every target has a documented equivalent) | Not installed |
