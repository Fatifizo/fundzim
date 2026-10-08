# Stage 4 Implementation — Authentication, Identity, RBAC, Account Security & Infrastructure Remediation

> Branch `stage-4/identity-access` (from `stage-3/core-platform` `17c9d88`). This document records what was
> built, where it lives and how it maps to the Stage 4 brief. Evidence for every claim of "tested" is in
> [testing.md](testing.md). Nothing here moves money, posts to a ledger or pays anyone out: those stay out of
> scope (Stages 8–11).

Related: [authentication-architecture.md](authentication-architecture.md) ·
[session-management.md](session-management.md) · [rbac.md](rbac.md) · [mfa.md](mfa.md) ·
[security-review.md](security-review.md) · [known-issues.md](known-issues.md) ·
[interface-contracts.md](interface-contracts.md) · [ADR-032](../adr/ADR-032-email-password-totp-authentication.md) ·
[ADR-033](../adr/ADR-033-per-request-nonce-csp.md) · [handover](../stage-handover/STAGE-4-TO-STAGE-5.md)

---

## 1. Stage 3 remediation gate

| Stage 3 item | Resolution in Stage 4 | Where |
|---|---|---|
| KI-01 no worker / outbox / idempotency | **Done.** Worker binary on River v0.47.0 (Postgres queue in schema `queue`), outbox relay (`FOR UPDATE SKIP LOCKED`, one `outbox.deliver` job per subscriber), deliver job with inbox dedupe, outbox and idempotency-key purge jobs, Prometheus metrics, graceful drain | `apps/api/cmd/worker`, `internal/platform/{jobs,outbox}`, `internal/app/worker*.go`, migrations `20261008130000/130100` (River's own SQL, unedited) |
| KI-05 per-replica in-memory limiter | **Done.** GCRA in a Valkey Lua script (atomic), named policies (`ratelimit.DefaultPolicies`), protective in-process fallback when Valkey is unreachable (never allow-all), circuit breaker, metrics. Old `httpx.RateLimit`/`MemoryRateLimiter` removed | `internal/platform/ratelimit` |
| KI-04 web proxy replaces client IP | **Done.** The web server stamps each request with the real peer address under a per-process random header; the proxy forwards the peer (or the right-most untrusted `X-Forwarded-For` hop when the peer is a trusted proxy) and drops client-supplied `X-Forwarded-For`/`X-Real-IP`/`Forwarded`. The API trusts `X-Forwarded-For` only from the web container (fixed compose subnet, `172.28.0.10/32`) | `apps/web/src/lib/net/*`, `internal/platform/httpx/clientip.go`, `compose.yaml` |
| KI-06 CSP `'unsafe-inline'` | **Done.** Per-request nonce CSP with `'strict-dynamic'`, no `'unsafe-inline'` (scripts or styles); `'unsafe-eval'` only under `next dev`. Required turning off `cacheComponents` (ADR-033) | `apps/web/src/proxy.ts`, `apps/web/src/lib/security/csp.ts` |
| KI-13 no architecture tests | **Done.** Import-graph test (design-baseline §4) and SQL table-ownership test (§5), mutation-checked | `internal/archtest` |
| KI-14 OpenAPI types for the web client | Not done — hand-written types with tests remain (still OPEN, [known-issues.md](known-issues.md)) | — |
| Idempotency infrastructure | **Done.** `Idempotency-Key` store in PostgreSQL (`app.idempotency_keys`): per-principal scope, request fingerprint, in-progress lease, replay of stored responses (< 500, not 408/429), `Optional`/`Required` modes | `internal/platform/idempotency` |
| CI validation | Stage 3 and Stage 4 branches pushed; **GitHub lists no workflows and no runs** for the repository (Actions appears disabled). CI could not be verified — BLOCKED on an owner action ([prerequisite-assessment.md](prerequisite-assessment.md)) | `.github/workflows/ci.yml` (extended) |
| Dependencies | govulncheck: 0 vulnerabilities affecting the code (1 module-level advisory in an unused package, KI-S4-10); `npm audit --omit=dev`: 0 | [testing.md](testing.md) |

## 2. Modules and packages added

| Package | Module (design-baseline) | Responsibility |
|---|---|---|
| `internal/users` | users | Accounts, profiles, emails (`is_login` sign-in address), phone numbers. Public Go interface used by auth and organisations |
| `internal/auth` | auth | Registration, verification, login, MFA, sessions, CSRF, step-up, password reset/change, email change, phone verification, staff invitations, role requests, suspension, bootstrap ceremony, the request `Authorizer`, outbox email consumers |
| `internal/auth/passwords` | auth | Argon2id hasher (bounded concurrency, dummy hash), NFKC normalisation, password policy, embedded common-password list (SecLists, MIT) |
| `internal/organisations` | organisations | Organisations, membership, organisation roles, invitations; cross-organisation isolation |
| `internal/audit` | audit | `audit.Record` into the business or security hash chain; rejects secret-like metadata keys |
| `internal/notifications` | notifications | SMTP email sender (Mailpit locally), `dev_mailpit` SMS sender; refuse to send inside a DB transaction |
| `internal/platform/{authz,clock,crypto,jobs,outbox,ratelimit,idempotency}` | platform | Principal/context, injectable clock, AES-256-GCM field encryption + HMAC blind index + tokens, River clients, outbox, limiter, idempotency |
| `internal/archtest` | — | Architecture tests |

Module dependencies follow design-baseline §4 and are enforced by `internal/archtest`. Table ownership
follows §5 with Stage 4 additions: `app.auth_tokens`, `app.mfa_login_challenges`, `app.session_events`
(owned by **auth**) and `app.user_terms_acceptances` (owned by **users**).

## 3. Database (migrations `20261008140000`–`20261008140500`)

| Migration | Tables / changes |
|---|---|
| `140000_identity_users` | `users` (+ `is_system`, STAFF-only, immutable), `user_profiles`, `user_emails` (`is_login`; unique live sign-in address), `user_phone_numbers` (unique verified E.164); seeded non-login system actor `00000000-…-0001`; FK on `feature_flag_changes.approved_by` |
| `140100_identity_credentials` | `mfa_methods` (TOTP secret ciphertext + key id, `totp_last_used_step`), `authentication_identities`, `password_credentials` (Argon2id PHC CHECK, one current), `otp_challenges` (HMAC code + destination, ≤ 5 min, ≤ 5 attempts), `recovery_codes` (keyed hash), `auth_tokens` (SHA-256, ≤ 7 days, one live per user+purpose), `mfa_login_challenges` (≤ 15 min, ≤ 5 attempts) — guard triggers make them immutable except consumption/attempts |
| `140200_identity_sessions` | `sessions` (hash only; staff sessions require `mfa_verified_at` and a PASSWORD/WEBAUTHN first factor — CHECKs), `session_events` (append-only) |
| `140300_identity_rbac` | `roles`, `permissions` (+ `account.suspend`, `account.reactivate`, `staff.invite`), `role_permissions`, `role_assignment_requests` (maker-checker, no self-request/approval CHECKs, 24 h expiry, guarded state machine), `role_assignments` (only from an APPROVED GRANT request whose maker/checker are copied by composite FK; SoD conflicts rejected by trigger), `security_events`, `break_glass_grants`, `staff_conflict_declarations` |
| `140400_organisations` | `organisations`, `organisation_roles` (ORG_ADMIN, ORG_MEMBER), `organisation_members` (deferred "at least one active ORG_ADMIN" trigger; removal final), `organisation_invitations` |
| `140500_runtime_grants_v2` | `app.apply_runtime_grants()` v2: new reference tables, River routines/types for `fundzim_app` |
| `140600_terms_acceptances` | `user_terms_acceptances` (users module; append-only): document, version, time and IP of each acceptance — LR-022 acceptance versioning. Registration records `TERMS_OF_USE` version `placeholder-2026-10-08` (the text is a counsel-pending placeholder) |

All thirteen migrations go down to zero and up again cleanly (verified twice on a scratch database; the Down
sections of `users` and `rbac` lift the `status_transitions` append-only trigger only inside the rollback).

## 4. HTTP API (48 routes, all in `api/openapi/fundzim-v1.yaml`)

Policies (deny by default, `httpx.Router`): **Public**, **Authenticated** (any live session), **User**
(personal accounts only), **Staff**, **Permission(code)** (staff + permission + fresh step-up when the
permission requires it). Non-staff callers of staff routes get `404 ROUTE_NOT_FOUND`.

- Auth: `register`, `verify-email`, `resend-verification`, `login`, `mfa/verify`, `mfa/recovery`,
  `forgot-password`, `reset-password`, `staff-invitation/start|finish`, `session`, `logout`, `logout-all`,
  `step-up/verify`.
- Self service (`/me`): profile get/patch, password change, email change, security overview, sessions list and
  revoke, phone verify request/confirm, MFA enrol/confirm/disable, recovery-code regeneration.
- Organisations: create, list mine, get, members, change member role, remove member/leave, invitations
  create/list/revoke, my invitations accept/decline.
- Admin: invite staff, role-assignment requests list/create/approve/reject, user view (masked), suspend user,
  reactivate user, suspend staff.

Middleware order (public listener): request ID → trusted client IP → access log → recover → security headers →
CORS → body limit → timeout → global per-IP limit (Valkey) → session resolution → CSRF → router (authorization
per route, then per-route idempotency) → handler.

## 5. Frontend (`apps/web`)

Pages: `/register`, `/login` (+ `/login/mfa`), `/verify-email`, `/forgot-password`, `/reset-password`,
`/staff/accept-invitation`, `/dashboard`, `/settings/{profile,security,sessions,mfa}`. Protected pages check
the session server-side and redirect to `/login?next=…` (`next` validated against open redirects); they are
`private, no-store`. The browser never sees the session token (HttpOnly) and sends `X-CSRF-Token` from the
readable CSRF cookie. MFA QR codes are rendered locally (`qrcode-generator@2.0.4`, MIT) — the secret never
leaves the page.

## 6. Operations

- `fundzimctl bootstrap-admins --admin-a-email … --admin-a-name … --admin-b-email … --admin-b-name …
  --justification …` — one-time, audited creation of the first two SUPER_ADMINs ([rbac.md](rbac.md) §5).
- Worker: `apps/api/cmd/worker` (`fundzim-worker` in compose), internal `/healthz`, `/readyz`, `/metrics` on
  `WORKER_INTERNAL_HTTP_PORT` (9091).
- `docker compose --profile scale-test up -d fundzim-api-2` — second API replica on 127.0.0.1:8081 for
  distributed-limit tests.

## 7. Deviations from the brief / earlier designs

| Item | Deviation | Reason |
|---|---|---|
| ADR-027 OTP-first login | Email + password with TOTP (ADR-032) | Owner's Stage 4 brief; no SMS provider |
| Staff WebAuthn | TOTP only (WebAuthn deferred) | ADR-032 §3 |
| `cacheComponents` (Next.js) | Disabled | Nonce CSP needs per-request rendering (ADR-033) |
| Phone OTP confirm | Bound to user + number (no browser `flow_id` in the API contract) | Contract simplicity; the `flow_id` column is still filled |
| Staff reactivation | Not implemented | Should be a maker-checker decision; admin console stage |
| Organisation invitation | Accepted by the signed-in owner of the invited verified address; no token is emailed | A forwarded email grants nothing |
