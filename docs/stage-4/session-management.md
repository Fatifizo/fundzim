# Stage 4 Session Management

Code: `internal/auth/sessions.go`, `internal/auth/middleware.go`. Schema: `app.sessions`, `app.session_events`
(migration `20261008140200`). Decision: ADR-027 §sessions (unchanged by ADR-032).

## 1. Model

- **Opaque, server-side sessions.** The cookie holds 32 random bytes (base64url); the database stores only
  `SHA-256(token)` (`token_hash`, 32 bytes, unique). A database leak does not yield usable cookies.
- **Cookies** (all `Path=/`, no `Domain`):

| Cookie | Content | Flags |
|---|---|---|
| `__Host-fz_session` (`fz_session` on local http) | session token | HttpOnly, Secure (https), SameSite=Lax, `Max-Age` = absolute expiry |
| `__Host-fz_csrf` (`fz_csrf`) | `HMAC-SHA-256(CSRF_SECRET, "csrf:"+session_id)` | readable by JS (double submit), Secure, SameSite=Lax |
| `__Host-fz_mfa` (`fz_mfa`) | pending MFA challenge token | HttpOnly, Secure, SameSite=Strict, ≤ 15 min |

  Production refuses to start unless `APP_PUBLIC_URL` is https and the session cookie uses the `__Host-` prefix.
  Nothing session-related is stored in `localStorage` or `sessionStorage`.

## 2. Lifetimes

| | Users | Staff |
|---|---|---|
| Idle timeout | `SESSION_IDLE_TIMEOUT` 7 days | `STAFF_SESSION_IDLE_TIMEOUT` 15 min |
| Absolute timeout | `SESSION_ABSOLUTE_TIMEOUT` 30 days | `STAFF_SESSION_ABSOLUTE_TIMEOUT` 12 h |
| Step-up window | `STEP_UP_MAX_AGE` 10 min | same (TOTP only) |

The idle expiry slides on use, written at most once a minute per session (bounded write load), never past the
absolute expiry. Both are enforced in the lookup query and by a CHECK (`idle_expires_at <= absolute_expires_at`).

## 3. Lifecycle

| Event | Effect | Recorded |
|---|---|---|
| Login (password, or password + MFA) | New session; any session presented with the request is revoked `ROTATED` (fixation defence); `step_up_at = now` | `session_events CREATED`, audit `auth.login.succeeded`, security event |
| Step-up | `step_up_at = now` | `session_events STEP_UP` |
| Logout | Current session revoked `LOGOUT`; cookies cleared | event + audit |
| Logout all | Every session of the user revoked `LOGOUT_ALL` | event + audit |
| Revoke one (`DELETE /me/sessions/{id}`) | Own sessions only (scoped by user id; another user's id → 404) | `USER_REVOKED` |
| Password reset | All sessions + pending MFA challenges revoked `PASSWORD_RESET` | |
| Password change | Other sessions revoked `PASSWORD_CHANGED` | |
| Email change confirmed | All sessions revoked | |
| MFA enabled/disabled/codes regenerated | Other sessions revoked `MFA_CHANGED` | |
| Role granted/revoked | Target's sessions revoked `PRIVILEGE_CHANGE` (new permissions apply at the next sign-in with fresh MFA) | |
| Account suspended | All sessions revoked `ACCOUNT_SUSPENDED` | |

Revocation is immediate: every request re-reads the session row (no cached authentication), and the account
must be `ACTIVE`, not the system actor, and of the session's kind.

## 4. Request processing

1. `SessionMiddleware` reads the cookie and resolves it (session row, then the account through the `users`
   module, then staff permissions from role assignments and active break-glass grants). Unknown/expired cookies
   are cleared. **A database error is remembered**, and every non-public route then answers
   `503 SERVICE_UNAVAILABLE` instead of treating the caller as anonymous (fail closed).
2. `CSRFMiddleware` (unsafe methods): `Origin`, when present, must equal `APP_PUBLIC_URL`;
   `Sec-Fetch-Site: cross-site` is refused; with a session cookie, `X-CSRF-Token` must equal the session-bound
   token (constant-time compare). This also covers login CSRF (pre-session endpoints get the origin checks).
   Webhook paths are exempt (signature-authenticated, no cookies).
3. The router's `Authorizer` applies the route policy ([rbac.md](rbac.md)).

## 5. Listing

`GET /me/sessions` returns live sessions with `current`, created/last-seen times, the IP masked to /24 (IPv4)
or /48 (IPv6), a truncated user agent, the authentication method and whether MFA was used.

## 6. Measured cost

Authenticated `GET /me` (session lookup, account lookup, CSRF/authorisation, JSON): p50 ≈ 2–3 ms, p95 ≈ 4 ms
sequential; p95 ≈ 6.5 ms at 20 concurrent clients (~4 500 req/s, one in-process API on this machine)
([testing.md](testing.md) §4).
