# Stage 4 Testing Evidence

All results below were produced in this session on 2026-10-08 on the development machine (Linux, Xeon
Platinum 8160 × 16, Go 1.27.1, Node 24.21.0, Docker 29.7.2; PostgreSQL 17.11, Valkey 9.0.6, Garage v2.4.1,
Mailpit in containers). Nothing is reported as passing that was not run. **GitHub Actions CI has not run**
(the repository lists no workflows or runs — [prerequisite-assessment.md](prerequisite-assessment.md)), so the
`-race` detector (CI only; no C compiler locally) has **not** been executed for Stage 4 code.

## 1. Summary

| Suite | Command | Result |
|---|---|---|
| Go format / vet | `gofmt -l apps internal migrations tests`; `go vet ./apps/api/... ./internal/... ./migrations/...`; `go vet -tags integration ./tests/...` | clean |
| Go build | `CGO_ENABLED=0 go build -trimpath ./apps/api/cmd/...` (api, worker, fundzimctl) | OK |
| Go unit + architecture tests | `go test -count=1 ./apps/api/... ./internal/... ./migrations/...` | **113 top-level tests passed, 0 failed, 0 skipped** (18 packages with tests, including `internal/archtest`) |
| Integration (real stack) | `set -a; . ./.env; set +a; FUNDZIM_IT_WEB_URL=http://127.0.0.1:3000 FUNDZIM_IT_API_URL=http://127.0.0.1:8080 FUNDZIM_IT_STOP_VALKEY=1 FUNDZIM_IT_PERF=1 sg docker -c "go test -tags integration -count=1 -v ./tests/integration/"` | **45 passed, 0 failed, 0 skipped** (60.8 s) |
| Two-replica limit | `docker compose --profile scale-test up -d fundzim-api-2`; `FUNDZIM_IT_API_URL=… FUNDZIM_IT_API2_URL=http://127.0.0.1:8081 go test -tags integration -run TestDistributedLoginLimitAcrossReplicas …` | **passed** (status sequence `[401 401 401 401 401 429 429 429]` alternating replicas) |
| Session expiry | `go test -tags integration -run TestIdentitySessionExpiry …` (added after the full run) | **passed** (idle expiry, absolute expiry with cookie `Max-Age`, sliding idle expiry) |
| Web lint / types | `npm --prefix apps/web run lint`, `run typecheck` | clean |
| Web unit (Vitest) | `npm --prefix apps/web test` | **184 passed** (14 files) |
| Web E2E (Playwright, Chromium) | `npm --prefix apps/web run test:e2e` | **57 passed, 1 skipped** (the pre-existing mobile-only test on the desktop project) |
| OpenAPI lint | `npx --yes @redocly/cli@2.54.3 lint api/openapi/fundzim-v1.yaml --config api/openapi/redocly.yaml` | valid, 2 warnings (pre-existing, `/healthz` and `/readyz` 4xx) |
| Route ↔ contract | `TestEveryRouteHasAPolicyAndIsInOpenAPI` + a script checking all 48 identity (method, path) operations, security, permission and step-up markers | pass / "48 routes checked, 0 problems" |
| Migrations | `fundzimctl migrate down` × 12 then `up` (twice, scratch DB `fundzim_lead`); identity migrations down/up twice on the dev DB | clean both directions |
| Vulnerabilities | `go run golang.org/x/vuln/cmd/govulncheck@v1.8.0 ./apps/api/... ./internal/... ./migrations/...`; `npm audit --omit=dev` | 0 affecting code (1 module advisory in unused `x/crypto/openpgp`, F-14); npm 0 |
| Secrets | `./scripts/check-secrets.sh`; `gitleaks git .` (13 commits); `gitleaks dir .` | clean; no leaks in history; working-tree hits only in the git-ignored local `.env` |
| Real-stack smoke via web | register → Mailpit link → verify → login → `/me` → `/dashboard` through `http://localhost:3000` (Next proxy) with a spoofed `X-Forwarded-For: 6.6.6.6` | 202/200/200/200/200; anonymous `/dashboard` → 307 `/login?next=%2Fdashboard`; API recorded the real peer `172.28.0.1`, not the spoofed value; CSP header nonce-based |

## 2. Integration tests (47)

**Identity, end to end** (`tests/integration/identity_test.go`): the API runs in-process on the shared
database and Valkey; emails come from the **running worker container** through the outbox and SMTP and are
read from Mailpit.

| Test | Proves |
|---|---|
| `TestIdentityRegistrationVerificationAndLogin` | policy 422, unknown-field 422 (no mass assignment), duplicate registration indistinguishable + owner notified, unverified login allowed, `/me` not cacheable, single-use verification token, raw token absent from DB |
| `TestIdentityLoginGenericErrorsAndCSRF` | identical errors for wrong password/unknown account, cookies set, raw session token absent from DB, CSRF: missing/forged/foreign-origin/cross-site/other-session token rejected, per-account throttle → 429 even with the right password |
| `TestIdentityEmailChangeAndResend` | resend invalidates the old link, generic resend, wrong password refused, change to a taken address gives the same 202 and sends nothing, old address notified, address changes only on confirmation, sessions revoked, old address can no longer sign in |
| `TestIdentitySessionsRotationLogoutAndIDOR` | rotation on re-login (old token dead), listing with masked IPs, another user's session → 404, revoke one / logout-all / logout |
| `TestIdentityPasswordResetAndChange` | generic forgot, policy on reset, single-use reset token, all sessions revoked, notification, old password dead, change needs current password and revokes other sessions only, Argon2id PHC stored |
| `TestIdentityMFAEnrollLoginRecovery` | enrol/confirm, alert email, password-only login yields challenge and no session, wrong/valid TOTP, single-use recovery code, challenge dead after 5 failures even for a correct code, TOTP secret encrypted and recovery codes hashed at rest, disable with step-up + code |
| `TestIdentityPhoneVerification` | invalid number 422, masked response, dev SMS via Mailpit, wrong code, single use, masked phone in `/me`, plaintext OTP absent |
| `TestIdentityConcurrency` | 10 parallel registrations of one address → exactly 1 account; 8 parallel uses of one verification token → exactly 1 success; one recovery code used by 2 concurrent challenges → exactly 1 success |
| `TestIdentityRBACStaffMakerChecker` | bootstrap ceremony (and refusal of a second one), staff invitation with TOTP, staff login needs MFA, staff refused on user routes, users/anonymous get 404 on admin routes, staff password-only step-up refused, self-request refused, duplicate pending refused, maker cannot approve, user cannot approve, peer approval, double decision refused, target sessions revoked and role active after re-login, stale step-up → `STEP_UP_REQUIRED`, SUPER_ADMIN lacks suspension, COMPLIANCE officer granted via maker-checker suspends (all sessions revoked, login 403, owner emailed) and reactivates a user, staff target via user route 404, REVOKE via maker-checker removes the permission, approval audited, security chain verifies |
| `TestIdentityOrganisationIsolation` | unverified users cannot create, outsiders get 404 on read/members/invite, invitation visible/acceptable only by the invited verified address, single accept, member can view but not manage, last ORG_ADMIN cannot leave or be demoted, promotion then demotion works, foreign member IDs via another organisation → 404, list shows only own organisations |
| `TestIdentityAuditChainsIntact` | both audit hash chains verify (worker role); no secret-like keys in security audit metadata |
| `TestIdentitySessionExpiry` | idle expiry and absolute expiry enforced server-side (short config timeouts), idle clamped to absolute, cookie `Max-Age` ≤ absolute lifetime, idle expiry slides after > 1 min of inactivity and never past the absolute expiry |
| `TestIdentityPerformanceSample` | latency numbers (§4) |
| `TestDistributedLoginLimitAcrossReplicas` | the per-account limit is shared by two real API containers |

**Platform (streams A and B) and Stage 3:** migrations repeatable; runtime-role readiness; audit append-only and
chain verification; reference-data privileges; outbox immutability; Redis; storage isolation (3 tests);
running API; web → API; rate limiting (atomic under concurrency, two replicas share one limit, hashed keys with
TTL, spoofed `X-Forwarded-For` cannot change the key, fallback when Valkey is unreachable and when the real
container is stopped); idempotency (concurrent requests run the handler once, key reuse with a different body,
5xx retryable, expired lease taken over, required header and expiry); outbox exactly-once per subscriber,
concurrent relays, rollback delivers nothing, retry until success, always-failing handler discarded; worker job
cancelled mid-run retried elsewhere, crashed job rescued; email and dev SMS to Mailpit; SMTP outage retried not
lost; worker container ready.

## 3. Unit and architecture tests added in Stage 4 (selection)

`internal/auth` (authorizer matrix incl. fail-closed cases, CSRF middleware, hardened cookies, phone
normalisation, masking/justification, email links), `internal/auth/passwords` (hash/verify, rehash, policy
codes, normalisation, busy semaphore), `internal/platform/crypto` (AEAD with AAD, keyed hashes, tokens, codes),
`internal/organisations` (slugs, role/type allow-lists), `internal/platform/ratelimit`, `idempotency`, `outbox`,
`jobs`, `notifications`, `httpx` (client IP, strict JSON decoding, router policies), `config` (auth/email/SMS/
security refusals), `internal/archtest` (import graph and table ownership; a deliberate violation was added and
caught for both rules, then removed).

## 4. Performance (measured, single machine — indicative only)

| Measurement | Result |
|---|---|
| Argon2id hash, default parameters (64 MiB, t=3, p=2) | 141 ms/op (`go test -bench . ./internal/auth/passwords/`) |
| Argon2id verify, parallel with the default bound of 4 | 63 ms/op aggregate (~16 verifications/s per API process) |
| `GET /me` sequential, 300 requests | p50 2.7 ms, p95 3.9 ms, p99 5.2 ms (~373 req/s single client) |
| `GET /me` 20 concurrent clients, 1 000 requests | p50 3.5 ms, p95 6.5 ms, p99 40.7 ms, ~4 509 req/s |
| `PATCH /me` 10 concurrent clients, 300 requests (each writes a business audit event) | p50 19 ms, p95 75.7 ms, p99 122 ms, ~382 req/s (audit chain serialisation, F-13) |

These are not capacity claims; production load testing is Stage 19.

## 5. Not tested / limitations

- `-race` (CI only) and the CI pipeline itself (not running).
- WebAuthn, real SMS, KMS, production TLS/cookie behaviour behind a real reverse proxy (config refusals are
  unit-tested).
- Staff reactivation (not implemented), break-glass grants (schema only; no API yet).
- Playwright auth tests use an in-memory mock API (server-side session checks cannot be intercepted in the
  browser); the real API path is covered by the Go integration tests and the manual web smoke above.
