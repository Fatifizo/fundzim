# FundZim Security Architecture

Status: Stage 0 baseline (design). Nothing in this document is implemented yet unless stated.
Owners: Security Architect / Technical Lead.
Related: [THREAT-MODEL.md](THREAT-MODEL.md), [AUDIT.md](AUDIT.md), [DATA-CLASSIFICATION.md](DATA-CLASSIFICATION.md),
[PRIVACY.md](PRIVACY.md), [PAYMENTS.md](PAYMENTS.md), [LEDGER.md](LEDGER.md), [ARCHITECTURE.md](ARCHITECTURE.md),
ADR-007, ADR-008, ADR-009.

> FundZim makes **no claim** of PCI DSS compliance, data-protection compliance, security certification or
> regulatory approval. This document defines engineering controls the platform is designed to have. Stage 18
> (hardening) and Stage 20 (pilot readiness) produce internal evidence and independent test results; they do
> not by themselves certify compliance. Any compliance claim requires an independent assessment by a
> qualified party and explicit approval to make it.

---

## 1. Security objectives

In priority order:

1. **Integrity of money movement.** No actor (external, insider, or compromised component) can create,
   alter, redirect, duplicate or erase a financial record or payout without detection and authorisation.
2. **Confidentiality of identity data.** KYC documents, identity numbers and payout account details are
   exposed only to the minimum set of people and code paths that need them, and every access is audited.
3. **Accountability.** Every privileged or financial action is attributable to an authenticated actor with
   a recorded reason ([AUDIT.md](AUDIT.md)).
4. **Availability of donation and payout flows** under ordinary failure (provider outage, Redis loss,
   network partitions) without compromising 1–3. When in doubt, fail closed for money and fail open only for
   read-only public content.

## 2. Architectural security boundaries

```
Internet ──TLS──▶ Edge (CDN/WAF/reverse proxy)
                    ├─▶ Next.js (apps/web)        presentation only, no DB, no secrets beyond public config
                    └─▶ /api/v1/ Go API           system of record, all authz decisions
                           ├─▶ PostgreSQL         authoritative (app schema, ledger, audit, kyc schema)
                           ├─▶ Redis              non-authoritative (rate limits, cache, ephemeral locks)
                           ├─▶ Object storage     public-media bucket | private-kyc bucket (separate creds/keys)
                           ├─▶ KMS / secret mgr   envelope keys, signing keys, provider credentials
                           └─▶ PSP adapters       outbound calls; inbound webhooks at /api/v1/webhooks/{provider}
Staff ──TLS + MFA──▶ admin surface (Stage 14) ──▶ same Go API, staff-only routes and permissions
```

Rules that follow from the boundaries:

- **The Go API is the only policy enforcement point.** Next.js may hide UI elements, but every authorisation
  decision is made (again) in the API. A request that bypasses the frontend must get exactly the same answer.
- **Next.js holds no secrets** other than public configuration. It never talks to PostgreSQL, Redis,
  object storage or a PSP directly.
- **Module boundaries are security boundaries.** Only the `ledger` module writes ledger tables; only `kyc`
  touches the `kyc` schema and the `private-kyc` bucket; only `payments`/`payouts` call provider adapters.
  These are enforced by package structure, an architecture test (Stage 3), and database roles (§17).
- **Redis loss must be safe.** Losing Redis can degrade rate limiting or caching, never financial correctness.
  When the rate limiter is unavailable on sensitive endpoints (OTP send, login, payment creation), the
  endpoint fails closed or falls back to a conservative in-process limit.

## 3. OWASP coverage map

Controls are mapped against the OWASP Top 10 (2021) and the OWASP API Security Top 10 (2023). The ASVS
(Application Security Verification Standard) Level 2 is the target verification baseline, with Level 3
controls for payment, payout, ledger and KYC paths. Final mapping is re-checked in Stage 18.

| Risk | Primary controls (section) |
|---|---|
| A01 Broken Access Control / API1 BOLA / API5 BFLA | Central authz, ownership checks on every object, deny by default (§5, §9) |
| A02 Cryptographic Failures | TLS 1.2+, KMS envelope encryption, Argon2id, no home-grown crypto (§12, §13) |
| A03 Injection | Parameterised SQL only, output encoding, CSP (§7, §8) |
| A04 Insecure Design | Threat model, maker-checker, idempotency, ledger invariants (THREAT-MODEL, LEDGER) |
| A05 Security Misconfiguration | Secure headers, hardened containers, config validation at boot (§11, §19) |
| A06 Vulnerable Components | npm audit, govulncheck, Trivy, Dependabot/Renovate, pinned versions (§19) |
| A07 Identification & Auth Failures | OTP + optional Argon2id passwords, MFA for staff, rate limits (§4, §10) |
| A08 Software & Data Integrity Failures | Signed webhooks, lockfiles, CI provenance, append-only ledger/audit (§16, §19) |
| A09 Logging & Monitoring Failures | Structured logs, audit events, financial alerts (§14, AUDIT, OBSERVABILITY) |
| A10 SSRF / API7 SSRF | No arbitrary URL fetch; allow-listed fetcher (§9) |
| API2 Broken Authentication | Session design, OTP abuse defence (§4, §10) |
| API3 Broken Object Property Level Authz | Explicit response DTOs per audience; no mass assignment (§5.4) |
| API4 Unrestricted Resource Consumption | Rate limits, body size limits, pagination caps, upload limits (§10, §15) |
| API6 Unrestricted Access to Sensitive Business Flows | Velocity limits on donation/payout/OTP, risk engine (§10, Stage 13) |
| API8 Security Misconfiguration | As A05 |
| API9 Improper Inventory Management | Versioned `/api/v1/`, route inventory generated from code, no undocumented debug routes |
| API10 Unsafe Consumption of APIs | Provider responses validated, never trusted blindly; status confirmed server-to-server (§16, PAYMENTS) |

## 4. Authentication

Implemented in Stage 4. Design:

> **Stage 4 as built ([ADR-032](adr/ADR-032-email-password-totp-authentication.md) amends §4.1):** users sign in
> with **email + password** (Argon2id, 12+ characters, offline common-password list, no composition rules) and
> optional TOTP MFA with recovery codes; OTP **login** is deferred until an SMS provider exists. Phone numbers
> are verified by SMS code (development provider) as contact data only. Staff: password + **mandatory TOTP**
> (WebAuthn deferred), invitation-only accounts, TOTP-only step-up. Step-up uses `/auth/step-up/verify`.
> Details: [stage-4/authentication-architecture.md](stage-4/authentication-architecture.md),
> [stage-4/mfa.md](stage-4/mfa.md). The OTP rules below apply to phone verification codes.

### 4.1 Users (donors, campaign owners, organisation members)

- **Primary factor:** one-time code to a verified phone number (SMS; WhatsApp or other channels later if a
  provider is selected) or email. Mobile-first and low-literacy-friendly.
- **Optional password:** if enabled, hashed with **Argon2id** (parameters set at Stage 4 against target
  hardware, starting point memory ≥ 64 MiB, iterations ≥ 3, parallelism 1–4), unique salt, never logged.
  Passwords checked against a breached-password list (k-anonymity range query or local list) and a minimum
  length of 12; no composition rules.
- **OTP rules:** 6+ digits from a CSPRNG, stored only as a hash (HMAC-SHA-256 with server key), single use,
  TTL ≤ 5 minutes, max 5 verification attempts per code, max resend rate per phone/email/IP (§10).
  Codes are bound to purpose (`login`, `verify_phone`, `payout_destination_change`) so a code for one purpose
  cannot be replayed for another.
- **Step-up authentication:** fresh OTP (or MFA factor) required within the last N minutes for sensitive
  actions: adding/changing payout destinations, requesting a payout, changing phone/email, closing a campaign
  with funds, exporting personal data.
- **Account recovery** is a privileged flow: changing the phone number (the primary factor) requires the old
  factor, or a support-mediated recovery with identity re-verification; it triggers a payout hold and
  cooling-off (§10.4, [THREAT-MODEL.md](THREAT-MODEL.md) T-05, F-04).

### 4.2 Staff (REVIEWER, SUPPORT, COMPLIANCE, FINANCE, ADMIN, SUPER_ADMIN)

- Separate staff accounts; a staff identity is never the same account used to run personal campaigns.
- **Mandatory MFA**: WebAuthn/passkeys preferred (phishing resistant), TOTP fallback. SMS is **not** an
  accepted staff second factor (SIM-swap risk).
- Shorter session lifetime (§6), re-authentication for privileged actions, optional IP/device restrictions
  for FINANCE and SUPER_ADMIN (decided Stage 14).
- SSO via an identity provider may replace local staff auth later; the permission model is unchanged.

### 4.3 Service-to-service

- Provider webhooks authenticate by provider signature (§16), not by FundZim sessions.
- Internal jobs run with a distinct actor identity (`actor_type=system`, named job) so audit trails show
  which job performed an action.
- No shared “god” API keys. Any future machine API key is scoped, hashed at rest, rotatable and audited.

## 5. Authorisation

### 5.1 Model

- **RBAC for staff capabilities**, expressed as granular permissions (e.g. `campaign.review`,
  `campaign.suspend`, `kyc.document.view`, `kyc.decision.record`, `payout.approve`, `refund.approve`,
  `ledger.adjustment.create`, `ledger.adjustment.approve`, `role.assign`, `audit.read`).
- **ABAC / ownership** for user resources: a campaign owner may act on their campaigns; organisation members
  act according to their org role (ORG_ADMIN, ORG_MEMBER) on that organisation’s campaigns.
- **Deny by default.** A route without an explicit policy is unreachable (enforced by a test that enumerates
  routes and fails if any route lacks a policy declaration).
- Authorisation is evaluated in the service layer (not only in HTTP middleware), so jobs and internal calls
  are also subject to it.

### 5.2 Least privilege for administrators

`ADMIN` and `SUPER_ADMIN` do **not** implicitly hold sensitive permissions. In particular, neither role
includes `kyc.document.view`, `kyc.identity_number.reveal`, `ledger.adjustment.create` or `payout.approve`
by default. `SUPER_ADMIN` can assign roles, but:

- role grants are themselves maker-checker (one SUPER_ADMIN proposes, a different one approves);
- a SUPER_ADMIN cannot grant permissions to themselves;
- every grant/revoke is an audit event and triggers an alert to the security owner.

Indicative role → permission matrix (final matrix in Stage 4/14):

Stage 1 added two roles ([ADR-017](adr/ADR-017-payout-approval-segregation-of-duties.md)):
`KYC_REVIEWER` (identity-verification decisions) and `SECURITY_ADMIN` (access reviews, key rotation, incident
tooling, with no access to financial records, KYC documents or donor identities). The full
separation-of-duties matrix and maker-checker policy are in
[compliance/operational-controls.md](compliance/operational-controls.md).

| Permission | REVIEWER | KYC_REVIEWER | SUPPORT | COMPLIANCE | FINANCE | ADMIN | SECURITY_ADMIN | SUPER_ADMIN |
|---|---|---|---|---|---|---|---|---|
| View campaign + review queue | ✓ | | read | ✓ | read | ✓ | | ✓ |
| Approve/reject campaign | ✓ | | | ✓ | | | | |
| Suspend / unsuspend campaign | ✓ | | | ✓ | | | | |
| Freeze campaign / payouts | | | | ✓ | ✓ | | | |
| View KYC status | ✓ | ✓ | ✓ | ✓ | ✓ | ✓ | | ✓ |
| View KYC documents / reveal ID numbers | | ✓ (justified) | | ✓ (justified) | | | | |
| Record KYC decision | | ✓ | | ✓ | | | | |
| Approve payout (checker) | | | | | ✓ | | | |
| Create ledger adjustment (maker) | | | | | ✓ | | | |
| Approve ledger adjustment (checker) | | | | | ✓ (different person) | | | |
| Request refund (maker) | | | ✓ | ✓ | | | | |
| Approve refund (checker) | | | | | ✓ (not the requester) | | | |
| Access reviews, key/secret rotation, incident tooling | | | | | | | ✓ | |
| Assign roles | | | | | | | | ✓ (maker-checker) |
| Read audit log | | | | ✓ (scoped) | ✓ (financial) | | ✓ (security events) | ✓ |

### 5.3 Separation of duties (maker-checker)

Required, enforced in code and by database check (`approved_by <> requested_by`):

- payouts above a configured threshold or flagged by the risk hook (threshold value: business decision,
  `LEGAL_REVIEW_REQUIRED`, LR-030); every payout, approved or auto-approved, passes the automated policy
  checks in [PAYMENTS.md](PAYMENTS.md) §13;
- every refund (requested by SUPPORT/COMPLIANCE, approved by FINANCE);
- every manual ledger adjustment or reversal;
- payout destination override by staff;
- role and permission grants;
- unfreezing a FROZEN campaign;
- changes to fee configuration.

### 5.4 Object-level and property-level authorisation

- Every handler that loads a resource by ID checks the actor’s right to that specific resource (IDOR
  defence). UUIDv7 IDs are not treated as secrets.
- Queries are scoped at the repository layer (`WHERE owner_id = $actor` or org membership join), not
  filtered after loading.
- Responses use explicit per-audience DTOs (`PublicCampaign`, `OwnerCampaign`, `StaffCampaign`). Domain
  structs are never serialised directly. This prevents leaking fields such as donor identity on anonymous
  donations or KYC status details.
- Requests bind to explicit input DTOs; unknown JSON fields are rejected (no mass assignment of `status`,
  `role`, `owner_id`, `amount_minor`, etc.).

### 5.5 Break-glass access

For emergencies where the normal permission path is unavailable:

- time-boxed (default ≤ 1 hour), requires written justification and an incident/ticket reference;
- grants only the specific permission needed, never blanket SUPER_ADMIN;
- immediately alerts the security owner and a second senior staff member;
- every action under break-glass is tagged in the audit log and reviewed afterwards.

## 6. Session management

> **Stage 4 as built:** as described below, with the named timeouts as config defaults; rotation at login and
> MFA completion; revocation on logout(-all), password reset/change, email change, MFA change, privilege change
> and suspension; step-up refreshes `step_up_at` on the existing session rather than rotating it.
> [stage-4/session-management.md](stage-4/session-management.md).

- Opaque, random session token (≥ 256 bits from a CSPRNG). Only `SHA-256(token)` is stored server-side in
  PostgreSQL, so a database read does not yield usable sessions.
- Cookie: `__Host-fz_session`, `HttpOnly`, `Secure`, `SameSite=Lax`, `Path=/`, no `Domain` attribute.
- No JWTs for browser sessions (revocation and size problems). Short-lived signed tokens may be used for
  narrow purposes (e.g. presigned object URLs) where they are scoped and expire quickly.
- Timeouts (starting points, tuned in Stage 4; mirrored in `.env.example`): users idle 7 days / absolute
  30 days; staff idle 15 minutes / absolute 12 hours; step-up freshness 10 minutes.
- Rotation on login, on privilege change (role grant, step-up) and on phone/email change. Fixation prevented
  by never accepting a client-supplied session ID.
- Users can see and revoke active sessions. Changing the primary factor or a detected ATO signal revokes all
  sessions.
- Logout deletes the server-side session; it does not rely on cookie deletion alone.

## 7. CSRF and XSS

### 7.1 CSRF

Defence in depth for every state-changing request (`POST`, `PUT`, `PATCH`, `DELETE`):

1. `SameSite=Lax` session cookie.
2. Origin check: `Origin` (or `Referer` fallback) must match the configured FundZim origin; requests with
   `Sec-Fetch-Site: cross-site` are rejected.
3. Synchroniser/double-submit CSRF token bound to the session, sent in a header (`X-CSRF-Token`).

Webhook endpoints are exempt from CSRF (no cookies) and are authenticated by signature instead.

**Stage 4 as built:** `SameSite=Lax`, Origin check and session-bound HMAC token (`fz_csrf` cookie →
`X-CSRF-Token`). Deviation: there is no `Referer` fallback — a request **without** `Origin` (non-browser
clients, very old browsers) passes the origin step and, when it carries a session cookie, still needs the
token; pre-session endpoints (login, register) without `Origin` rely on `SameSite` and `Sec-Fetch-Site`.
`GET` handlers never change state.

### 7.2 XSS

- React’s default escaping; `dangerouslySetInnerHTML` is banned by lint rule except in a reviewed sanitiser
  component.
- Campaign stories are stored as a constrained format (Markdown subset or structured blocks, decided Stage 6)
  and rendered through an allow-list sanitiser. Raw HTML from users is never rendered.
- **Content Security Policy** with per-request nonces: `default-src 'self'`; `script-src 'self' 'nonce-…'
  'strict-dynamic'`; `object-src 'none'`; `base-uri 'none'`; `frame-ancestors 'none'`; `form-action 'self'`
  plus explicitly listed PSP hosted-payment origins. `connect-src` lists only the API origin and approved
  telemetry. Start in report-only in Stage 7, enforce by Stage 18.
- User-supplied text in OpenGraph tags, emails and SMS is escaped for each sink.

## 8. SQL injection and data access

- All SQL is parameterised (sqlc-generated or a query builder with bind parameters; final choice Stage 2/3).
  String concatenation of user input into SQL is forbidden and checked in code review and by `gosec`.
- Dynamic `ORDER BY` / filter fields map through allow-lists.
- The application connects with a least-privilege role (§17); even a successful injection cannot `UPDATE`
  or `DELETE` ledger or audit rows, or read the `kyc` schema from the general app connection.

## 9. SSRF and IDOR

### 9.1 SSRF

- The API does not fetch arbitrary user-supplied URLs. Features that would (link previews, remote image
  import) go through a single allow-listed fetcher that resolves DNS once, rejects private/link-local/
  loopback/metadata ranges (including `169.254.169.254` and IPv6 equivalents), disallows redirects to such
  ranges, enforces timeouts and size limits, and runs with no credentials.
- PSP endpoints are configured per provider, never derived from request or webhook data.

### 9.2 IDOR

See §5.4. Additionally: integration tests for every resource route include a “different user’s ID”
case that must return `404` (not `403`, to avoid confirming existence) for user-facing resources.

## 10. Rate limiting, brute force and abuse

### 10.1 Mechanism

Token-bucket/sliding-window limits keyed by multiple dimensions: IP (and /24 or ASN aggregation for abuse),
account, phone number, email, device fingerprint where lawful, and campaign. Stored in Redis with a
conservative fail-closed fallback for sensitive endpoints (§2).

**Stage 4 as built:** GCRA in Valkey (atomic Lua) with named policies keyed by IP (IPv6 /64), normalised
email, user, E.164 number, MFA challenge; on Valkey failure every policy keeps limiting per replica with an
in-process fallback (never allow-all). Device fingerprint, /24 and ASN aggregation are not implemented.
Values: [stage-4/authentication-architecture.md](stage-4/authentication-architecture.md) §5.

### 10.2 Brute force

- OTP verification: max 5 attempts per code; per-account and per-IP lockout with exponential back-off.
- Password login (if enabled): progressive delays; account-level soft lock that does not allow an attacker
  to permanently lock out a victim (unlock via OTP).
- Uniform responses for “user exists / does not exist” on login and recovery (enumeration resistance).

### 10.3 SMS pumping / OTP toll fraud

OTP sends cost money and can be abused to generate traffic to premium numbers:

- per-phone, per-IP and global send budgets with alerting on anomalies;
- country/prefix allow-list (Zimbabwe +263 by default; other prefixes opt-in for diaspora as product
  decides) — LEGAL_REVIEW_REQUIRED (LR-031) only insofar as provider terms apply;
- CAPTCHA or proof-of-work challenge after thresholds (privacy-respecting option chosen in Stage 4);
- OTP send endpoints never reveal whether a number is registered.

### 10.4 Business-flow abuse

- **Card testing:** velocity limits on donation attempts per IP/device/card fingerprint (as exposed by the
  PSP), minimum donation amount, risk scoring (Stage 13), PSP-side fraud tools where available.
- **Payout abuse:** payout destination change triggers payout hold + cooling-off period (duration set in
  Stage 11), notification to the old and new contact channels, and step-up auth.
- **Refund abuse:** refunds only to the original payment instrument via the PSP; never to a different
  destination.

## 11. Secure HTTP headers

Set at the edge and verified by an automated test (Stage 7/18):

| Header | Value (baseline) |
|---|---|
| `Strict-Transport-Security` | `max-age=63072000; includeSubDomains; preload` (preload after domain is stable) |
| `Content-Security-Policy` | Nonce-based policy (§7.2) |
| `X-Content-Type-Options` | `nosniff` |
| `Referrer-Policy` | `strict-origin-when-cross-origin` |
| `Permissions-Policy` | deny camera, microphone, geolocation, payment etc. except where a feature needs it (e.g. camera for KYC capture on the KYC route only) |
| `Cross-Origin-Opener-Policy` | `same-origin` |
| `Cross-Origin-Resource-Policy` | `same-site` for API responses |
| `Cache-Control` | `no-store` on all authenticated API responses and any response containing C2+ data |

`frame-ancestors 'none'` in CSP replaces `X-Frame-Options` (both may be sent).

## 12. Transport security

- TLS 1.2 minimum, TLS 1.3 preferred, modern cipher suites only, at every external edge.
- Internal traffic (API ↔ PostgreSQL, Redis, object storage, KMS) also encrypted in production; PostgreSQL
  with `sslmode=verify-full`.
- Outbound PSP calls verify certificates; no `InsecureSkipVerify`, enforced by lint.
- Certificates managed automatically (ACME or managed cloud certs); expiry monitored.

## 13. Encryption at rest and key management

| Layer | Control |
|---|---|
| Database volumes, backups, object storage | Provider/disk encryption at rest (baseline) |
| `private-kyc` bucket | Server-side encryption with a **dedicated** KMS key, separate from `public-media` |
| C3 fields (national ID / passport numbers, payout account numbers, DOB) | Application-level envelope encryption: per-record data key encrypted by KMS key-encryption key; ciphertext + key version stored |
| Dedupe on encrypted identifiers | HMAC-SHA-256 blind index with a separate key (equality search without decryption) |
| Secrets (C4) | Secret manager only; never in DB, repo, images or logs |

- No home-grown cryptography. Use Go standard library / vetted libraries (e.g. `crypto/aes` GCM, Tink-style
  AEAD) and the cloud KMS.
- Key rotation: KEKs rotated on a schedule (period decided Stage 5/18) with re-wrap of data keys; blind-index
  key rotation requires re-indexing and is planned as a migration.
- Decryption is a logged event (who, which record, why) for C3 identity data.

## 14. Secrets management

- `.env` files are for local development only and are git-ignored; `.env.example` contains placeholders only.
- Production secrets are injected at runtime from a managed secret store (choice in Stage 18). Containers
  receive only the secrets they need (e.g. the web container receives none of the PSP or DB credentials).
- Each PSP credential is per environment; sandbox credentials never work against production, and
  production credentials are never present in development or CI.
- Rotation runbooks for: DB passwords, PSP API keys and webhook secrets, KMS keys, session-signing/HMAC keys.
- Secret scanning: `scripts/check-secrets.sh` locally (Stage 0), `gitleaks` in CI and as a pre-commit hook
  (Stage 3). A leaked secret is rotated first, then removed from history — removal alone is not remediation.
- Config is validated at boot: the API refuses to start in production mode with placeholder or empty
  secrets, debug flags, the sandbox payment provider enabled, `SMS_PROVIDER=log`, `sslmode` other than
  `verify-full`, local field-encryption keys, or any security control switched off
  (`MALWARE_SCAN_ENABLED=false`, `AUDIT_HASH_CHAIN_ENABLED=false`, `RATE_LIMIT_ENABLED=false`).

## 15. Sensitive logging rules

Logging uses an **allow-list** model: structured fields are logged only if they are declared loggable.
Request/response bodies are not logged by default.

**Never log** (applies to application logs, traces, error reports, audit metadata and analytics):

- passwords, OTP codes, password-reset or magic-link tokens;
- session tokens, CSRF tokens, cookies, `Authorization` headers;
- API keys, webhook secrets, private keys, KMS data keys, HMAC keys;
- raw card data (PAN), CVV/CVC, expiry, PINs, mobile-money PINs;
- full national ID / passport numbers, identity document images or their extracted text, liveness media;
- full payout bank account numbers or full wallet numbers beyond what is needed (mask to last 4);
- raw webhook payloads (stored redacted in `webhook_inbox`, never in logs);
- free-text medical details from campaigns in error contexts.

Allowed with care: internal UUIDs, payment and payout IDs, provider name, provider transaction reference,
masked phone (`+26377****123`), masked account (`****1234`), request/correlation IDs.

Error reporting (Sentry-compatible) uses server-side scrubbing plus client-side `beforeSend` scrubbing.
A test suite (Stage 3) asserts that known sensitive fields are redacted by the logger.

## 16. Webhook authentication and replay protection

Detailed in [PAYMENTS.md](PAYMENTS.md). Security requirements:

1. Endpoint per provider: `/api/v1/webhooks/{provider}`. Unknown provider → `404`.
2. **Verify before parsing business content**: HMAC or asymmetric signature over the raw body using the
   provider’s scheme; constant-time comparison; reject on failure with no detail in the response.
3. **Replay protection**: timestamp tolerance (e.g. 5 minutes) where the provider signs a timestamp;
   `UNIQUE(provider, provider_event_id)` in `webhook_inbox`; processing is idempotent regardless.
4. **Never trust the webhook alone where the provider’s signing is weak or absent** — confirm via an
   authenticated server-to-server status call before changing payment state or posting to the ledger.
5. Never trust browser redirects or client-reported payment status.
6. Optional IP allow-listing of provider source ranges as defence in depth, never as the only control.
7. Body size limits and strict parsing; unknown event types are stored and ignored, not errored in a loop.
8. Webhook secrets rotatable with an overlap window (accept old + new during rotation).

## 17. Database permissions

Separate PostgreSQL roles (created in Stage 3 migrations, documented in [DATABASE.md](DATABASE.md)):

| Role | Purpose | Key restrictions |
|---|---|---|
| `fundzim_migrator` | Owns schemas; runs migrations only (CI/deploy step) | Never used by the running app |
| `fundzim_app` | Running API and workers | No DDL. `INSERT`+`SELECT` only on `ledger_transactions`, `ledger_entries`, `audit_events`; `UPDATE` only on the `ledger_balances` projection (ledger module); no `UPDATE`/`DELETE`/`TRUNCATE` on them. No access to `kyc` schema |
| `fundzim_kyc` | Used only by the `kyc` module’s connection pool | DML on `kyc` tables only; does not own the schema |
| `fundzim_readonly` | Reporting/analytics, read replica where available | `SELECT` on approved views; C3 columns excluded |

- Append-only tables are additionally protected by triggers that raise on `UPDATE`/`DELETE`/`TRUNCATE`, so even a
  mis-granted privilege is caught.
- Row-level security (RLS) may be applied to multi-tenant organisation data in Stage 2 if it reduces risk
  without hiding logic.
- Production database access by humans is via a bastion/session-recorded tool, read-only by default, with
  justification; write access is break-glass (§5.5).

## 18. File upload security and malware scanning

Two upload classes with different treatment ([ARCHITECTURE.md](ARCHITECTURE.md), ADR-008, ADR-009):

| | Campaign media (`public-media`) | KYC/compliance docs (`private-kyc`) |
|---|---|---|
| Classification | C0 once published, C1 before | C3 |
| Upload path | Presigned PUT to `quarantine/` prefix, size- and content-type-constrained | Presigned PUT to `quarantine/` in the private bucket, dedicated credentials |
| Validation | Magic-byte type check (JPEG/PNG/WebP; no SVG), dimension limits, decompression-bomb checks | Allow-listed types (JPEG/PNG/PDF), size limits, PDF active-content checks |
| Malware scan | ClamAV (or equivalent) before promotion | ClamAV before promotion; failed scans quarantined and alerted |
| Transform | Re-encode, strip EXIF/GPS, generate sizes | Stored as-is (evidentiary); no public derivatives |
| Access | Served via CDN after promotion | No public access; ≤ 5-minute presigned GET issued by `kyc` module to authorised staff; every issuance audited |
| Filenames | Server-generated keys only; user filename kept as metadata, escaped | Same |

Uploads never reach application servers’ local disks in production. The scanning worker has read access to
quarantine and write access to the promoted prefix only.

> **Stage 5 as built (private documents; [ADR-035](adr/ADR-035-restricted-data-access-and-documents.md),
> [stage-5/document-security.md](stage-5/document-security.md)):** uploads go **through the API** (multipart,
> fields first) instead of presigned PUTs, so the subject is authorised before any byte is stored and the
> content is size-capped, hashed and magic-byte sniffed in one pass; the user filename is **not** kept at all.
> Bytes land in the private bucket's `quarantine/` prefix (SSE-C encrypted), are scanned by ClamAV in the
> worker, and only CLEAN objects are promoted and can be opened; scanner errors become FAILED_SCAN and are
> retried, never treated as clean. Access is a 60-second HMAC ticket bound to the object, user **and session**,
> re-authorised and audited at download and served as an attachment with `nosniff` and `CSP: sandbox` — no
> presigned URL ever reaches a browser. PDF active-content checks are not implemented (PDFs are never rendered
> inline). Campaign-media uploads (public class) arrive with campaigns (Stage 6/7).

## 19. Dependency, supply-chain and container security

- Lockfiles committed (`package-lock.json`, `go.sum`). Exact versions for runtime dependencies.
- Automated update PRs (Dependabot or Renovate) with CI gate.
- `npm audit` and `govulncheck` in CI; high/critical findings in production dependencies block merge unless
  an exception is recorded with expiry. (Stage 0 note: `npm audit` reports high-severity findings in the
  `braces` package via `eslint-config-next`, a development-only dependency; tracked as known issue.)
- `gosec` and ESLint security rules in CI; CodeQL or equivalent SAST from Stage 3.
- Containers: minimal/distroless base images, non-root user, read-only root filesystem, no shell in
  production images where practical, pinned base image digests, Trivy scan in CI, SBOM generated per build.
- CI hardening: least-privilege CI tokens, protected `main` branch, required reviews, signed tags for
  releases (provenance attestation later).
- `make security` runs the locally available subset (secret scan, `npm audit`, `govulncheck` once Go exists).

## 20. Backup security

- Automated encrypted PostgreSQL backups with point-in-time recovery; encryption keys separate from the
  backup storage credentials.
- Backups stored in a separate account/project or with write-once (object lock) retention so a compromised
  application credential cannot delete them.
- `private-kyc` backups inherit C3 handling; no copies to developer machines.
- **Restore tests** on a schedule (quarterly at minimum, decided Stage 18) into an isolated environment,
  including a ledger invariant check (debits = credits per currency) after restore.
- Production data is never copied into development or staging. Test data is synthetic.
- Backup retention vs deletion obligations: see [PRIVACY.md](PRIVACY.md) (LEGAL_REVIEW_REQUIRED, LR-012, LR-034).

## 21. KYC document security (summary)

KYC data is the highest-impact confidentiality asset. Controls (Stage 5 implementation):

- separate Postgres schema `kyc`, separate DB role, separate connection pool;
- separate bucket, separate KMS key, separate credentials;
- application-level encryption of identity numbers with blind index;
- access only through the `kyc` module API; no other module can read documents or reveal numbers;
- staff access requires `kyc.document.view`, a justification, and produces an audit event; bulk export of
  KYC data is not a product feature;
- verification vendor (if any) receives data under contract; vendor choice and cross-border transfer are
  LEGAL_REVIEW_REQUIRED (LR-011, LR-033; [COMPLIANCE.md](COMPLIANCE.md), [PRIVACY.md](PRIVACY.md));
- KYC media is never used for training, analytics or marketing.

> **Stage 5 as built:** `kyc` schema owned by the `fundzim_kyc` role with its own pool; audit, evidence and
> outbox only through SECURITY DEFINER gateways with per-role prefix allow-lists. Identity numbers, KYC drafts,
> addresses and reviewer notes are AES-256-GCM encrypted (row/field AAD) with KYC-specific local keys and an
> HMAC blind index; KMS is still Stage 18. Document access needs `kyc.document.view`, a fresh step-up **and
> case assignment**; the full identity number needs `kyc.identity_number.reveal` (COMPLIANCE), step-up and a
> justification; both are security-audited. SUPPORT, ADMIN and SUPER_ADMIN hold none of these permissions.
> Reviewers cannot act on their own verification, including through a linked personal account. No verification
> vendor is integrated. Review: [stage-5/security-review.md](stage-5/security-review.md).

## 22. Incident response outline

Full runbooks in Stage 18. Outline:

1. **Detect** — alerts (ledger imbalance, reconciliation mismatch, webhook signature failure spikes,
   DLQ growth, unusual payout volume, mass KYC document access, role grants), user reports, PSP notices.
2. **Triage** — severity: SEV1 = possible loss or misdirection of funds, KYC data exposure, or ledger
   invariant failure; SEV2 = degraded donations/payouts; SEV3 = other.
3. **Contain** — kill switches available from Stage 11/14: global payout freeze, per-campaign freeze,
   per-provider disable, new-campaign submission pause, staff account suspension, credential rotation.
   Kill-switch use is itself audited.
4. **Preserve evidence** — snapshot logs, audit events, DB state; do not “fix” financial records in place
   (corrections are new ledger transactions).
5. **Eradicate & recover** — rotate secrets, patch, restore from verified backups if needed, reconcile
   with PSPs before resuming payouts.
6. **Notify** — PSPs, affected users, and authorities as legally required. Notification duties and
   timelines (data protection authority, FIU, RBZ) are LEGAL_REVIEW_REQUIRED (LR-008, LR-010) (see PRIVACY §12,
   COMPLIANCE).
7. **Post-incident review** — blameless write-up, threat model update, ADR if architecture changes.

Any unexpected financial invariant failure **stops the affected flow** and is escalated as SEV1 — engineers
must not patch around it (CLAUDE.md rule).

## 23. Security review gates per stage

| Stage | Security gate before the stage is accepted |
|---|---|
| 0 Foundation | This document, threat model, secret scan clean, no credentials in repo |
| 1 Regulatory & Compliance | Legal review register triaged; data-protection and AML obligations mapped to controls |
| 2 System Architecture & DB | DB role design, schema review for C3 separation, ledger constraint design reviewed |
| 3 Core Platform | Logger redaction tests, secure config loading, request IDs, CI with gitleaks/audit/govulncheck, container hardening |
| 4 Authentication & Identity | Session/CSRF/OTP design tested; brute-force and SMS-pumping tests; staff MFA |
| 5 KYC & Verification | Encryption + blind index reviewed; KYC access audit verified; vendor DPA (LEGAL_REVIEW_REQUIRED, LR-033) |
| 6 Campaign Engine | State machine authz tests; IDOR tests; story sanitiser tests |
| 7 Public Fundraising Experience | CSP (report-only), headers test, XSS tests, accessible error handling without data leakage |
| 8 Payment Abstraction | Sandbox provider failure scenarios; idempotency enforced; no raw card data path exists |
| 9 Zimbabwe Payment Integrations | Per-provider webhook verification review; credential handling; sandbox/prod separation |
| 10 Ledger | DB-level append-only and balance constraints proven by tests; app role cannot mutate history |
| 11 Withdrawals & Payouts | Maker-checker, cooling-off, step-up auth, destination-ownership verification tested |
| 12 Fees | Fee config changes maker-checker; rounding tests |
| 13 Trust, Fraud & Risk | Abuse cases from threat model covered; false-positive handling reviewed |
| 14 Admin & Ops Portal | Permission matrix review, break-glass flow, staff session hardening |
| 15 Notifications | Template injection tests; no sensitive data in SMS/email; anti-spoofing (SPF/DKIM/DMARC) |
| 16 Discovery & Sharing | OpenGraph escaping; referral attribution privacy review |
| 17 Reconciliation & Reporting | Report access controls; export redaction |
| 18 Security Hardening | External penetration test, CSP enforced, ASVS review, DR/restore test |
| 19 Financial Validation | Concurrency/failure-injection suites pass; no invariant violations |
| 20 Pilot Certification | Open findings risk-accepted by named owners; incident runbooks exercised |
