# Stage 18 — Authentication, MFA and sessions

Reviewed 2026-10-23 against the code (file references are to `internal/auth` unless noted). Changes made in Stage 18 are
marked **Stage 18**.

## 1. Passwords and recovery

| Control | Implementation | Evidence |
|---|---|---|
| Hashing | Argon2id PHC, 16-byte salt, 64 MiB / t=3 / p=2, at most 4 concurrent hashes; < 64 MiB refused outside development; rehash on login | `passwords/passwords.go`, `config.go`; login p50 153 ms sequential, 401 ms at 8 concurrent (Stage 18 sample) |
| Policy | length and context checks (email, display name) | `passwords_test.go` |
| Reset / verification tokens | 32 random bytes, SHA-256 stored, single use (one atomic UPDATE), new token invalidates the old; reset 30 min, verification 24 h, staff invitation 48 h | `account.go`, `consumers.go` |
| After a reset | all sessions and pending MFA challenges revoked | `account.go:404` |
| Enumeration | dummy Argon2 verification for unknown accounts; identical `INVALID_CREDENTIALS`; forgot/resend always 202 | `TestIdentityLoginGenericErrorsAndCSRF` |
| Rate limits | login 20 / 5 min per IP and 5 / 15 min per email; MFA 5 / 5 min; step-up 5 / 15 min; reset 5 / h per IP, 3 / h per email; GCRA on Valkey with an in-process fallback that never fails open | `platform/ratelimit/policies.go`; DR exercise: Valkey stopped, login still answered 401 |
| Lockout | none by design (no lockout denial of service); limits refill | F-S18-10 |

## 2. Sessions

32-byte tokens stored as SHA-256; `__Host-` cookie, HttpOnly, Secure, SameSite=Lax (required outside development);
users 7 days idle / 30 days absolute, staff 15 minutes idle / 12 hours absolute, enforced server-side; a new session at
every login (the presented one is revoked as ROTATED); role grants revoke the target's sessions; password, MFA, email
changes and suspension revoke the other sessions; logout, logout-all and per-session revocation (owner-scoped). CSRF:
every non-safe method needs `Origin` = `APP_PUBLIC_URL` when present, refuses `Sec-Fetch-Site: cross-site`, and requires
`X-CSRF-Token` = HMAC(CSRF_SECRET, session id) with a live session. Concurrent sessions are not capped (F-S18-10).

## 3. MFA

TOTP (SHA-1, 6 digits, 30 s, ±1 step) sealed with AES-GCM bound to the method id; a code is accepted only for a step
after the last used one (replay protection); login challenge 5 minutes, 5 attempts; 10 recovery codes stored as keyed
HMAC, single use; enrolment, confirmation, disable and regeneration need a fresh step-up; **staff can never disable MFA
and a staff login without confirmed MFA is refused**. WebAuthn is not implemented (F-S18-12): production must not grant
privileged staff access until a phishing-resistant factor (or an SSO with one) is in place.

## 4. Step-up

Fresh for 10 minutes (`STEP_UP_MAX_AGE`); enforced by the router for every permission flagged `requires_step_up`
(fail-closed: if the flags cannot be loaded, every permission needs step-up) and by handlers whose route is not a
permission. Staff step up with TOTP only. **Stage 18 (F-S18-01):** a staff password change needs a fresh TOTP step-up and
never refreshes the step-up itself; before this fix, a staff member's password alone unlocked every step-up route for 10
minutes. Tested by `TestSecurityStaffPasswordChangeNeedsTOTPStepUp` (stale step-up refused; password-only step-up
refused; after a TOTP step-up the change succeeds and `step_up_at` is unchanged) and the web form test.

## 5. MFA reset and lost devices (procedure, F-S18-03)

There is no reset shortcut and none was added. A staff member who has lost both TOTP and recovery codes: a SECURITY_ADMIN
suspends the account (`staff.suspend`, audited), a second administrator invites a **new named identity** through the
dual-controlled invitation flow (which enrols MFA), and roles are granted again through maker-checker requests. The old
identity stays suspended for the audit trail. A user who loses MFA uses a recovery code; without one, support cannot
remove MFA (no support permission exists for it).

## 6. Administrative and service authentication

Staff accounts are separate from personal accounts (links are confirmed by both sides and used to exclude
self-decisions). Services authenticate with per-role PostgreSQL passwords (`verify-full` outside development),
`rediss://` passwords, per-bucket S3 key pairs (all distinct) and an OpenBao AppRole (production refuses static tokens).
The internal listeners (`/metrics`, readiness) have no authentication and must stay on private networks (KI-S18-16).

## 7. Tests run

identity ITs (registration, login errors and CSRF, email change, sessions/rotation/IDOR, reset and change, MFA, phone,
concurrency, RBAC maker-checker, organisation isolation, audit chains, session expiry), the Stage 18 step-up test, auth
unit tests (`TestAuthorizeMatrix`, `TestCSRFMiddleware`, `TestCookiesAreHardened`). Results: security-testing.md.
