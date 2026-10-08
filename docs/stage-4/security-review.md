# Stage 4 Security Review — Authentication, Identity, RBAC and Account Security

> **Scope:** branch `stage-4/identity-access` — `internal/{auth,users,organisations,audit,notifications}`,
> `internal/platform/{ratelimit,idempotency,outbox,jobs,crypto,authz}`, migrations `2026100813*`/`2026100814*`,
> `apps/web` auth pages/proxy/CSP, compose and CI. **Method:** design and code review against
> [SECURITY.md](../SECURITY.md), [THREAT-MODEL.md](../THREAT-MODEL.md), OWASP ASVS-style checks for
> authentication/session/access control, plus the adversarial integration tests listed in
> [testing.md](testing.md). This is an engineering self-review by the implementing team — **not** an independent
> audit or penetration test, and it makes no compliance or certification claim. An independent review is
> planned for Stage 18.

Related: [authentication-architecture.md](authentication-architecture.md) · [session-management.md](session-management.md)
· [rbac.md](rbac.md) · [mfa.md](mfa.md) · [known-issues.md](known-issues.md)

---

## 1. Controls verified (with evidence)

| Area | Control | Evidence |
|---|---|---|
| Credential storage | Argon2id PHC only (DB CHECK); NFKC; common-password list; bounded hashing concurrency | `passwords` tests; `TestIdentityPasswordResetAndChange` checks the stored format |
| Secrets at rest | Session/link tokens SHA-256; OTP/recovery codes keyed HMAC; TOTP secrets AES-256-GCM with row-ID AAD | integration tests assert the raw token/code/secret is absent from the DB |
| Enumeration | Generic register/forgot/resend responses; identical login error; dummy-hash timing; "already registered" goes to the owner by email | `TestIdentityRegistrationVerificationAndLogin`, `TestIdentityLoginGenericErrorsAndCSRF` |
| Brute force | Valkey GCRA per IP and per account; MFA challenge ≤ 5 attempts (DB CHECK); OTP ≤ 5 attempts; step-up throttle; shared across replicas | `TestIdentityLoginGenericErrorsAndCSRF` (429), `TestIdentityMFAEnrollLoginRecovery`, `TestDistributedLoginLimitAcrossReplicas` |
| Session fixation | New token at every login/MFA completion; presented session revoked `ROTATED` | `TestIdentitySessionsRotationLogoutAndIDOR` |
| Session revocation | Re-read per request; revoked on logout(-all), password reset/change, MFA change, email change, privilege change, suspension | sessions, password, RBAC integration tests |
| CSRF | Origin = `APP_PUBLIC_URL`, `Sec-Fetch-Site` cross-site refused, session-bound HMAC token (constant-time) | `TestCSRFMiddleware` (unit), integration CSRF cases incl. another session's token |
| Cookies | `__Host-` + Secure in production (config refuses otherwise); HttpOnly session; SameSite Lax/Strict | `TestCookiesAreHardened` |
| Access control | Deny-by-default policies, 404 for staff routes, per-request permission recomputation, step-up for sensitive permissions, fail-closed on DB error | `TestAuthorizeMatrix`, RBAC integration test |
| IDOR | Own-session and organisation queries scoped by caller; foreign IDs → 404 | sessions and organisation integration tests |
| Mass assignment | Strict JSON decoding (unknown fields → 422) — e.g. `account_kind` on register | registration integration test |
| Maker-checker | Role grants/revocations need a different checker; self-request/approval refused in app **and** DB; SoD role conflicts in DB; bootstrap via system actor + peer | RBAC integration test |
| Privilege boundaries | SUPER_ADMIN cannot suspend accounts; staff cannot use personal-account routes; staff without `user.view` cannot view users | RBAC integration test |
| Staff MFA | Invitation acceptance enrols TOTP before a password exists; DB CHECK requires MFA on staff sessions; staff step-up is TOTP-only | RBAC integration test |
| Audit | Security decisions in the hash-chained security stream; high-volume failures in `app.security_events`; metadata keys checked against the redaction list; chains verify | `TestIdentityAuditChainsIntact` |
| Logging | No tokens/codes/secrets logged (senders never log bodies or links; redaction list covers `token`, `otp`, `secret`, `password`, `session`, `cookie`) | code review; Stage 3 redaction tests |
| Client IP | Only the web container is a trusted proxy; the web proxy ignores client-supplied forwarding headers | web unit/E2E tests; manual check: spoofed `X-Forwarded-For` ignored, real peer recorded |
| XSS | Nonce CSP, `strict-dynamic`, no `unsafe-inline` | `e2e/csp.spec.ts` (injected script blocked, zero violations) |
| Module boundaries | auth/organisations reach users data only through `internal/users` | `internal/archtest` (mutation-checked) |

## 2. Findings

Severity is the reviewer's judgement for the current (pre-production) stage. **No finding is critical or high
for local/staging use; production is blocked anyway until KMS exists (F-01).**

| ID | Finding | Severity | Status |
|---|---|---|---|
| F-01 | Field-encryption and blind-index keys are local hex keys; KMS integration does not exist. Config refuses to start in production. | High for production, n/a locally | OPEN — Stage 18 (blocks production by design) |
| F-02 | The per-account login limit (5 per 15 min) counts every attempt, so anyone who knows an address can keep its owner from signing in (rolling 15-minute denial; no permanent lockout). Correct-password attempts are also refused while limited. | Medium | OPEN — Stage 13/18: add a per-(account, IP/device) budget with a higher account-wide ceiling, and/or a CAPTCHA step |
| F-03 | Argon2id concurrency is bounded per process (4); a distributed password-spraying flood from many IPs can make login answer 503 (availability, not confidentiality). | Medium | OPEN — Stage 18 (edge rate limiting/WAF, autoscaling, global login budget) |
| F-04 | Breached-password screening is the offline top-10 000 list only; no k-anonymity breach-corpus check. | Low | ACCEPTED for Stage 4 (no outbound dependency); revisit Stage 18 |
| F-05 | Any signed-in user can trigger SMS codes to arbitrary numbers (bounded: 5/h per user, 3/h per number); with many accounts this is an SMS-pumping vector once a paid provider exists. A request for a number invalidates another user's pending code for that number (nuisance). | Low now (dev provider), Medium with a paid provider | OPEN — Stage 15 (provider selection must add per-prefix/country budgets and fraud controls, SECURITY §10.3) |
| F-06 | Bootstrap ceremony records the checker step-up as the ceremony time (the new admins have not authenticated yet). | Low | ACCEPTED with procedure — run only under a documented two-person operator procedure (Stage 18/20) |
| F-07 | Handler-level throttles (`auth.*`) return `429 RATE_LIMITED` without a `Retry-After` header (the message states the wait); the global limiter sets it. | Low | OPEN — small fix |
| F-08 | Registration of an existing address skips the audit write a new registration performs (a few ms difference) — a weak timing signal; Argon2 hashing is performed in both paths. | Low | OPEN — equalise (e.g. record a security event in both paths) |
| F-09 | Email change revokes sessions with reason `PASSWORD_CHANGED` (no `EMAIL_CHANGED` reason in the CHECK list). Effect is correct; the label is imprecise. | Informational | OPEN — add the reason in a later migration |
| F-10 | The "someone tried to register with your email" notice can be triggered repeatedly from many IPs (inbox nuisance). | Low | OPEN — per-address notice budget (Stage 15) |
| F-11 | The organisation-side invitation list shows the invitee's full email (C2) to organisation admins (who typed it). | Informational | ACCEPTED |
| F-12 | The web server's peer-address stamp wraps Node's `http.Server` request emission (Next.js gives handlers no socket); a framework upgrade could break it. It fails closed (no IP forwarded → the API sees the web container). | Low | OPEN — re-verify on each Next.js upgrade (test exists) |
| F-13 | Security-chain audit writes are serialised (by design); under load, sensitive writes queue (PATCH `/me` p95 ≈ 75 ms at 10 concurrent clients). | Low now | OPEN — Stage 19 load tests |
| F-14 | `golang.org/x/crypto` v0.57.0 carries advisory GO-2026-5932 (`openpgp` package unmaintained); FundZim does not import that package (govulncheck: not called). No fixed version exists. | Informational | ACCEPTED — monitored by govulncheck in CI |
| F-15 | Integration tests create accounts, staff and audit rows in the shared development database and revoke existing SUPER_ADMIN assignments there to re-run the bootstrap ceremony. | Informational (dev only) | ACCEPTED — CI uses a throwaway database; `make reset` locally |
| F-16 | CI could not be verified: GitHub lists no workflows/runs for the repository. | Process | BLOCKED — owner to enable Actions |

## 3. Explicit non-findings checked

- No hard-coded administrator, default password or public staff registration; no `SUPER_ADMIN` granted
  implicitly (bootstrap refuses when one exists).
- No session or secret material in `localStorage`/`sessionStorage`; no tokens in URLs other than one-time email
  links (POSTed by the page, single use, short TTL; web pages send
  `Referrer-Policy: strict-origin-when-cross-origin`, so the query string never leaves the origin in a Referer).
- No plaintext passwords, OTPs, recovery codes, MFA secrets or link tokens in the database, logs, audit
  metadata or outbox payloads.
- No payment, ledger or payout code was added.
