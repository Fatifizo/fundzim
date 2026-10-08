# FundZim API Design (`/api/v1`)

**Status:** Stage 2 design. There is no server code yet; implementation starts in Stage 3/4.
**Contract:** [`api/openapi/fundzim-v1.yaml`](../../api/openapi/fundzim-v1.yaml) (OpenAPI 3.1.0). This document
explains the conventions behind that contract and catalogues every endpoint, including those not yet in the
OpenAPI file. Where this document and the YAML disagree, the YAML wins ([ADR-026](../adr/ADR-026-api-versioning-contract-first.md)),
and the difference is a defect to fix in both.

**Companion documents:** [authorization-matrix.md](authorization-matrix.md) (who may call what) ·
[error-model.md](error-model.md) (error envelope and code catalogue).

**Inputs:** [design-baseline.md](../stage-2/design-baseline.md) (module names, table names, state names; the baseline
wins on conflict) · [ARCHITECTURE.md §5–§6](../ARCHITECTURE.md) · [PAYMENTS.md §7, §9–§13](../PAYMENTS.md) ·
[SECURITY.md §4–§11](../SECURITY.md) · [MONEY.md §3.3](../MONEY.md) · [PRODUCT.md §4, §6, §8–§10](../PRODUCT.md) ·
[FRONTEND.md](../FRONTEND.md) · Stage 1 specifications ([transaction-lifecycle.md](../payments/transaction-lifecycle.md),
[payout-lifecycle.md](../payments/payout-lifecycle.md), [payout-eligibility-and-controls.md](../payments/payout-eligibility-and-controls.md),
[refund-and-dispute-architecture.md](../payments/refund-and-dispute-architecture.md),
[operational-controls.md](../compliance/operational-controls.md), [kyc-architecture.md](../compliance/kyc-architecture.md),
[kyb-architecture.md](../compliance/kyb-architecture.md), [beneficiary-verification.md](../compliance/beneficiary-verification.md),
[campaign-approval-policy.md](../compliance/campaign-approval-policy.md),
[compliance-case-management.md](../compliance/compliance-case-management.md),
[identity-data-protection.md](../security/identity-data-protection.md)) ·
[STAGE-1-TO-STAGE-2.md §10](../stage-handover/STAGE-1-TO-STAGE-2.md).

**Related Stage 2 documents (written in parallel; linked, not restated):** `docs/architecture/*.md`,
`docs/database/*.md`, [docs/security/authentication-authorization.md](../security/authentication-authorization.md).

---

## 1. Principles

1. **Contract first.** The OpenAPI file changes before the code, in the same pull request. A route-registry test
   (Stage 3) fails if a registered route is missing from the spec or has no authorization policy.
2. **Deny by default.** Every endpoint in §15 names its authentication requirement and its authorization rule:
   a staff permission, an ownership predicate, or both. A route without a policy is unreachable
   ([SECURITY.md §5.1](../SECURITY.md)).
3. **Per-audience DTOs.** Public, owner/self and staff responses are separate schemas (§11). Domain structs are
   never serialised directly.
4. **Money is exact, per currency, and never totalled across currencies** (§3.4).
5. **No client claim confirms money.** A browser redirect, a poll or a client-supplied status never changes a
   payment, refund or payout state. Only verified webhooks, authenticated provider status queries and
   reconciliation do that ([PAYMENTS.md §7](../PAYMENTS.md)).
6. **Financial mutations are idempotent** (§7) and are backed by database uniqueness, not only by the
   idempotency table.
7. **Errors never tip off.** Error codes and messages never reveal holds, compliance cases, screening results,
   STR activity or risk signals to owners, donors or beneficiaries ([error-model.md §6](error-model.md), LR-008).

## 2. Base URL, versioning and evolution

| Topic | Rule |
|---|---|
| Base | `https://<host>/api/v1/...`, same origin as the web app ([ARCHITECTURE.md §3](../ARCHITECTURE.md)). Health probes `/healthz` and `/readyz` sit **outside** `/api/v1`. Webhooks are `/api/v1/webhooks/{provider}`. |
| Major version | In the URL. A breaking change needs `/api/v2/` for the affected resources only; v1 and v2 run side by side during the deprecation period. |
| Additive changes | New endpoints, new optional request fields and new response fields stay in v1. Clients must ignore unknown response fields. |
| Enum evolution | Adding a value to a **response** enum (for example a payment status) is treated as potentially breaking (ADR-026). Clients must map unknown values to a safe default ("processing"; never "failed"). New values are announced in release notes before they are emitted. Adding a value to a **request** enum is additive. |
| Error codes | New codes are additive. Clients must fall back on the HTTP status for an unknown code. A code is never renamed or reused for a different meaning. |
| Deprecation | A deprecated operation is marked `deprecated: true` in the spec and responds with `Deprecation: <date>` and `Sunset: <date>` headers plus a `Link: <...>; rel="successor-version"`. The deprecation period is a product decision announced with the change; nothing is removed from v1 without a sunset date. |
| Request strictness | Requests are strict: unknown JSON fields are rejected with `422 VALIDATION_FAILED` / `UNKNOWN_FIELD` (mass-assignment defence; request schemas set `additionalProperties: false`). Responses are lenient for clients. |

## 3. Data format

### 3.1 JSON and naming

- `application/json; charset=utf-8` for requests and responses. Other content types return
  `415 UNSUPPORTED_MEDIA_TYPE`. Uploads never go through the JSON API; they use presigned URLs (§13).
- Field names are `snake_case`. Path segments are lowercase and hyphenated (`/payout-requests`). Enum values are
  `UPPER_SNAKE` and match the baseline state names exactly ([design-baseline.md §8](../stage-2/design-baseline.md)).
- Timestamps are RFC 3339 UTC with `Z` (`"2026-10-08T07:30:00Z"`). Dates without time (`valid_until`) are
  `YYYY-MM-DD`. Display in Africa/Harare is a client concern.
- A field that may be unknown is present with `null` in responses (schemas list it as required + nullable).
  Optional request fields may be omitted. `PATCH` bodies are partial: an absent field is unchanged; `null`
  clears a field only where the schema allows null.
- Body size limits are per route class (default 64 KiB; campaign story 256 KiB). Oversize → `413 PAYLOAD_TOO_LARGE`.

### 3.2 Identifiers

| Identifier | Format | Where |
|---|---|---|
| Resource id | UUIDv7 string | Every resource (`campaign_id`, `payment_id`, `payout_request_id`, …). Not a secret: authorization never depends on it being unguessable. |
| `public_code` | 10 chars, Crockford base32, random | Public campaign URLs `/c/{slug}-{public_code}`; public read endpoints use `/public/campaigns/{public_code}`. The slug is cosmetic; the web app redirects to the canonical slug. |
| `receipt_number` | Random, unique, not enumerable | Donor receipts. Never accepted as an authorization token. |
| `payment_id`, `payout_request_id` (`payout_id`), `refund_id` | UUIDv7 | Also the provider reference and provider idempotency key for every attempt, forever ([PAYMENTS.md §10.2](../PAYMENTS.md)). Created server-side before any provider call. |

### 3.3 Optimistic concurrency

Mutable aggregates expose `version` (integer). Update requests send the `version` they read; a mismatch returns
`409 VERSION_CONFLICT`. Financial state transitions are guarded by the database transition triggers in addition
to the version check.

### 3.4 Money

- Always `{"amount_minor": "<digits>", "currency": "USD"|"ZWG"}` ([MONEY.md §3.3](../MONEY.md)). `amount_minor`
  is a **string** matching `^(0|[1-9][0-9]{0,18})$` (non-negative), `^[1-9][0-9]{0,18}$` (request amounts that
  must be positive) or `^(0|-?[1-9][0-9]{0,18})$` (signed statement/ledger lines only), and must fit in int64.
  JSON numbers, decimal points, exponents, signs on unsigned fields, whitespace and leading zeros are rejected
  with `422 INVALID_MONEY`.
- A response may add a display-only `formatted` string (`"US$100.00"`, ZiG shown as `"ZiG"`). Clients never
  parse it.
- **No cross-currency totals anywhere.** Raised amounts, financial summaries, statements and reports are arrays
  keyed by currency. There is no "total balance" field in any schema. The progress bar uses the goal currency
  only. There is no FX endpoint: FundZim never converts ([ADR-018](../adr/ADR-018-currency-isolation-and-fx.md)).
- Rates and percentages are integer basis points (`platform_fee_basis_points`).
- Fee previews are display-only. Authoritative fees are computed at capture.

### 3.5 Money states in owner views

The owner financial summary (`GET /campaigns/{campaign_id}/financial-summary`, schema
`CampaignFinancialSummary`) returns, **per currency**, the separate ledger-derived states of
[settlement-and-custody-model.md §2](../ledger/settlement-and-custody-model.md): `raised_gross`, `fees`,
`refunded`, `reversed`, `awaiting_settlement`, `available`, `reserved`, `pending_payout`, `in_transit`,
`paid_out` and `held`. They are never added together. `held` and `payouts_paused` are shown without any
reason. Only `available` can be requested as a payout (EC-09).

## 4. Envelope

```json
{ "data": { "...": "..." }, "meta": { "request_id": "01JAB7Q9X4M2T8K5V3N6P0R1SZ" } }
```

Lists:

```json
{ "data": [ { "...": "..." } ], "meta": { "request_id": "01JAB7...", "limit": 20, "next_cursor": "eyJr..." } }
```

Errors use `{ "error": { "code", "message", "retryable", "details[]" }, "meta": { "request_id" } }`; see
[error-model.md](error-model.md). `X-Request-ID` is echoed on every response (accepted inbound only from trusted
proxies, [OBSERVABILITY.md](../OBSERVABILITY.md)). `204 No Content` responses carry the header only.

## 5. HTTP status codes

| Status | Use |
|---|---|
| 200 | Read or update succeeded. |
| 201 | Resource created (including a donation whose payment ended in `UNKNOWN` or `FAILED` — the payment intent exists). |
| 202 | Accepted for asynchronous processing (report jobs, status queries, data-rights requests, abuse reports). |
| 204 | Success with no body (logout, revoke, delete). |
| 400 | Malformed JSON, missing/invalid `Idempotency-Key`, invalid cursor or filter. |
| 401 | Not authenticated, session expired, wrong OTP, webhook signature invalid. |
| 403 | Authenticated but not permitted where existence is not sensitive; CSRF failure; MFA or step-up required; SoD conflict. |
| 404 | Not found, **and** the response for any object the caller may not see (IDOR hardening, §10.3). Also every `/api/v1/admin/*` route for non-staff sessions. |
| 405 | Method not allowed. |
| 409 | State conflict (invalid transition, in-flight payout, version conflict) and idempotency conflicts. |
| 413 / 415 | Payload too large / unsupported media type. |
| 422 | Validation or business-rule failure (`VALIDATION_FAILED`, `INVALID_MONEY`, `PAYOUT_NOT_ELIGIBLE`, …). |
| 429 | Rate limited; always with `Retry-After`. |
| 500 | Unexpected error (no internals). `INVARIANT_CHECK_FAILED` is also 500 and raises a SEV1. |
| 503 | Dependency or provider unavailable; `Retry-After` when known. |

## 6. Pagination, filtering and sorting

### 6.1 Cursor pagination

- `?limit=20&cursor=<opaque>` → `meta.next_cursor` (null on the last page). Default `limit` 20, maximum 100.
  Offset pagination is not offered ([ARCHITECTURE.md §6](../ARCHITECTURE.md)).
- The cursor is opaque base64url of a server-signed (HMAC) structure holding the sort key value(s), the last
  `id` as tiebreaker, a hash of the filter/sort parameters and an expiry. Clients never construct or edit it.
  A tampered, expired or mismatched cursor (filters changed between pages) → `400 INVALID_CURSOR`.
- Ordering is always total: the requested sort key plus `id` (UUIDv7, time-ordered) as tiebreaker.
- No `total_count` on large or financial tables. Counts that matter (for example `donation_count`) are explicit
  fields of the parent resource.

### 6.2 Filters

- Filters are plain query parameters whose names are **allow-listed per endpoint** (the "List" column of the
  catalogue and the `parameters` of each operation). Any other parameter → `400 INVALID_FILTER`.
- Within one parameter, comma-separated values are OR (`status=PENDING_REVIEW,APPROVED`). Different parameters
  are AND.
- Time ranges use `from` / `to` (RFC 3339), half-open `[from, to)`. Monetary filters always carry `currency`.
- Free-text `q` exists only on public endpoints over public fields. Staff PII search (`/admin/users`) is
  exact-match on a normalised phone or email (blind index); there is no partial or wildcard search over personal
  data.
- Filters map to SQL through allow-lists, never by string concatenation ([SECURITY.md §8](../SECURITY.md)).

### 6.3 Sorting

`sort=<key>` with an optional `-` prefix for descending, one key per request, chosen from the endpoint's
allow-list (OpenAPI enum). The default sort is documented per endpoint (usually `-created_at`).

## 7. Idempotency

### 7.1 Where it is required

`Idempotency-Key: <uuid>` is **required** on every financial mutation (marked `required` in the catalogue,
`x-fundzim-idempotency: required` in the spec):

- donation / payment creation and payment cancellation;
- donor and staff refund requests, refund approval/rejection/withdrawal;
- payout requests, payout cancellation, payout approval/rejection/cancellation (staff);
- payout destination creation and campaign destination selection;
- campaign freeze, unfreeze approval, hold placement and release (they post ledger journals);
- ledger adjustments and their approval; dispute evidence submission and acceptance; recovery write-off;
- reconciliation imports, runs and resolutions.

Missing → `400 IDEMPOTENCY_KEY_REQUIRED`; not a UUID → `400 IDEMPOTENCY_KEY_INVALID`. Other mutating endpoints
accept the header optionally with the same semantics.

### 7.2 Semantics ([PAYMENTS.md §10.1](../PAYMENTS.md))

| Situation | Response |
|---|---|
| First request with this key | Row inserted `IN_PROGRESS` in its own short transaction, request processed, final response stored `COMPLETED`. |
| Same key, same request hash, `COMPLETED` | The stored status code and body are **replayed**, with `Idempotent-Replayed: true`. |
| Same key, same request hash, `IN_PROGRESS` | `409 IDEMPOTENCY_REQUEST_IN_PROGRESS`, `retryable: true`, `Retry-After`. Retry with the **same** key. |
| Same key, **different** request hash | `409 IDEMPOTENCY_KEY_REUSED`, `retryable: false`. |

- **Scope:** `(actor scope, route template, key)`. Actor scope is the user id or the staff user id. Guest
  checkouts have no session (baseline §12 I-2), so their scope is the anonymous scope for that route; a replay
  there is only served when the request hash (which includes payer phone, amount and donor details) matches
  exactly, so a stranger cannot obtain someone else's stored response without the full original request.
- **Request hash:** SHA-256 over the method, route template, path parameters and the canonicalised JSON body
  (sorted keys, no insignificant whitespace). Headers other than the key do not participate.
- **What is stored:** final 2xx responses and deterministic 4xx business/validation responses (so a replay of a
  rejected request is rejected identically). Not stored: `409 IN_PROGRESS`, `429`, `503` and unexpected `500`.
  For those, the key is released and the same key may be retried; financial uniqueness constraints in the
  database (`payment_intents`, `payout_requests`, `refund_requests` unique idempotency columns, provider
  reference uniques) make the retry safe ([PAYMENTS.md §10.3](../PAYMENTS.md)).
- **Secrets in replays:** stored bodies contain no secrets. The one-time `donation_access_token` (§9.4) is not
  stored; on a guest replay the server mints a fresh token, stores its SHA-256 in place of the old hash (the old
  token stops working) and returns it with the replayed body, so a guest whose first response was lost can still
  reach the donation.
- **Provider outcome unknown:** if the provider call times out, the payment is committed as `UNKNOWN` and the
  `201` response (status `UNKNOWN`) is what is stored and replayed. A client retry never creates a second
  provider call with a new reference.
- **Retention:** at least 24 hours, configurable, purged by a job ([PAYMENTS.md §10.1](../PAYMENTS.md)). After
  expiry, a resend with the old key is a new request — which is why clients must create a new key only for a new
  logical operation and the database uniques remain the last line of defence.
- **Client rule:** generate one key per user intent (for example when the donation form is submitted), persist it
  until a final answer is shown, and reuse it on every retry.

## 8. Rate limiting

Limits are applied per **route class** (the "RL class" column) with token-bucket / sliding-window counters in
Redis, keyed by the dimensions below ([SECURITY.md §10](../SECURITY.md)). Exceeding a limit returns
`429 RATE_LIMITED` with `Retry-After` (seconds). Values below are **initial proposals** held as configuration and
tuned in Stage 4/18; they are not legal thresholds and are never hard-coded.

| Class | Applies to | Keys | Initial proposal | Redis unavailable |
|---|---|---|---|---|
| `RL-PUBLIC-READ` | Public GETs, session bootstrap | IP (+ /24 or ASN aggregation) | 300/min per IP; edge cache in front | fail open (edge limits remain) |
| `RL-PUBLIC-WRITE` | Abuse reports, unsubscribe | IP, campaign | 5/h per IP per campaign; `CHALLENGE_REQUIRED` after 3 | fail closed |
| `RL-OTP-SEND` | Login OTP, step-up challenges, contact change | destination phone/email, IP, global budget, prefix allow-list | 3 per 15 min per destination; 10/h per IP; global SMS budget with alert (SMS-pumping, SECURITY §10.3) | fail closed |
| `RL-OTP-VERIFY` | OTP verify, staff login/MFA | challenge (max 5 attempts), account, IP | 5 per challenge; 20 per 15 min per IP; progressive back-off | fail closed |
| `RL-USER-READ` | Authenticated user reads; donation-token reads | user / donation | 300/min | fail open |
| `RL-USER-WRITE` | Non-financial user writes | user | 60/min | fail open with in-process fallback limiter |
| `RL-SENSITIVE-USER` | Step-up-gated and financial user actions (payout request/cancel, destinations, refund request, contact change, data rights, KYC identity data) | user, campaign | 10/h per user; payout requests 5/day per campaign | fail closed |
| `RL-DONATE` | Donation create, payment cancel | IP, user (if signed in), device fingerprint (where lawful), campaign, payer phone | 10/min per IP or user; 30/h per payer phone; campaign velocity alerts (card testing) | fail closed |
| `RL-STATUS-POLL` | Payment/payout status | resource id + session | 60/min per resource; server sends `poll_after_seconds` | fail open |
| `RL-UPLOAD` | Upload slots and finalisation | user | 30/h | fail closed |
| `RL-STAFF-READ` | Staff reads | staff user | 600/min | fail open |
| `RL-STAFF-WRITE` | Staff mutations | staff user | 120/min | fail closed |
| `RL-STAFF-SENSITIVE` | KYC document view sessions, identity reveals, donor unmasking, audit export, break-glass | staff user, subject | 30 view sessions/h; reveals capped by `kyc.reveal_daily_max` (`KYC_REVEAL_LIMIT_REACHED`) | fail closed |
| `RL-WEBHOOK` | Provider webhooks | provider | high ceiling (for example 1,000/min per provider); verification failures counted separately and alerted | fail open (inbox dedupe protects) |
| `RL-HEALTH` | `/healthz`, `/readyz` | — | not rate-limited in the app; internal network only | — |

OTP send endpoints respond identically whether or not the identifier exists (enumeration resistance).

## 9. Authentication, sessions and CSRF

Summary of [ADR-027](../adr/ADR-027-authentication-session-strategy.md) as it shows on the API; the full model
is in [authentication-authorization.md](../security/authentication-authorization.md).

### 9.1 Auth requirement labels used in the catalogue

| Label | Meaning |
|---|---|
| `none` | No session needed (public reads, health, login steps, unsubscribe token in body). Unsafe methods still pass the Origin / `Sec-Fetch-Site` checks. |
| `none or user session` | Guest (no cookie) or a signed-in USER. Used for donation creation and abuse reports. There are **no guest sessions** (baseline §12 I-2). |
| `session` | USER session (signed in). Staff accounts cannot use user routes. |
| `session + step-up` | USER session with a step-up completed within `STEP_UP_MAX_AGE` (10 min starting point); otherwise `403 STEP_UP_REQUIRED`. Used for payout requests/cancellation, destination changes, contact changes, closing/cancelling with funds, data export ([SECURITY.md §4.1](../SECURITY.md)). |
| `session or donation token` | Either the creating USER session or `X-Donation-Access-Token` for that donation (guest capability, §9.4). |
| `staff + MFA` | STAFF account session created by password (Argon2id) **plus** WebAuthn or TOTP (baseline §12 I-6; never SMS or email OTP). All `/api/v1/admin/*` routes except the two staff login steps. Non-staff sessions receive `404 ROUTE_NOT_FOUND`; no session receives `401`. |
| `staff + MFA + step-up` | Additionally a fresh MFA step-up (approvals, freezes, C3 access, role grants) ([operational-controls.md §3](../compliance/operational-controls.md)). |
| `provider signature` | Webhooks only. No cookies, no CSRF; signature + replay window + dedupe ([PAYMENTS.md §9](../PAYMENTS.md)). |

**Financial admin endpoints are never reachable by user sessions:** the router mounts `/api/v1/admin/*` behind a
staff-session middleware that rejects USER sessions and cookie-less requests before any handler or permission check runs, and the
service layer re-checks the staff permission.

### 9.2 CSRF

Every `POST`, `PUT`, `PATCH` and `DELETE` that relies on the session cookie requires all of
([SECURITY.md §7.1](../SECURITY.md)): the `SameSite=Lax` cookie; an `Origin` (or `Referer`) equal to the FundZim
origin and no `Sec-Fetch-Site: cross-site`; and `X-CSRF-Token` equal to the session-bound token from
`GET /auth/session`. Failure → `403 CSRF_TOKEN_INVALID`. The token rotates with the session. Exempt: webhooks
(signature-authenticated, no cookies) and `POST /notifications/unsubscribe` (signed token in the body, does not
use the session). Guest requests without a cookie (donation creation, token-authenticated refund requests) are not
cookie-authenticated, so they are not CSRF-exposed; they still pass the Origin / `Sec-Fetch-Site` checks. If a
session cookie is present, the CSRF token is required. `GET` never changes state.

### 9.3 Session lifetime

Users: idle 7 days / absolute 30 days. Staff: idle 15 minutes / absolute 12 hours. Step-up freshness 10 minutes.
Rotation on login, step-up, privilege change and contact change ([SECURITY.md §6](../SECURITY.md)).

### 9.4 Guest donors

Guests may donate without an account (PRODUCT §8) and **without any session** (baseline §12 I-2). After
`POST /campaigns/{campaign_id}/donations` a guest receives a one-time `donation_access_token` (≥ 256-bit random,
shown once, expiring; only its SHA-256 is stored in `app.donations.access_token_hash`). The token grants:

- read-only access to that one donation (`GET /donations/{id}`), its payment status (`GET /payments/{id}`) and its
  receipt;
- a refund request for that donation (`POST /donations/{id}/refund-requests`) and reading that refund request.

It does not allow visibility changes, payment cancellation, refund withdrawal or anything else; those need a
signed-in account (a guest can attach the donation to an account later, DON-07). The token travels only in the
`X-Donation-Access-Token` header, never in a URL query; receipt links carry it in the URL fragment so it never
reaches server logs. Losing the token means contacting SUPPORT.

## 10. Authorization (summary)

The full matrix, permission catalogue and SoD rules are in [authorization-matrix.md](authorization-matrix.md).

### 10.1 Layers

1. Route class: public, user, guest donation token, staff (MFA), webhook.
2. Staff RBAC permission (for example `payout.approve`), evaluated in the service layer.
3. Object-level predicate (ownership or organisation role), applied **in the query** (`WHERE owner_user_id =
   $actor` or organisation membership join), not by filtering after load ([SECURITY.md §5.4](../SECURITY.md)).
4. State and policy gates (verification level, campaign state, holds) → specific error codes.
5. Segregation of duties for checker actions → `403 APPROVER_CONFLICT`.

### 10.2 Staff routes

All under `/api/v1/admin/...`. Approvals and other money-moving or C3-touching actions require step-up, a
`justification` (and usually a `reason_code`), and record the actor as maker or checker. Approvals bind to the
object **as requested** (for payouts: `expected_amount` and `expected_destination_version` in the body);
any change invalidates them.

### 10.3 404 versus 403

- **User-facing resources** (campaigns, donations, payments, refund requests, payout requests, destinations,
  beneficiaries, organisations, KYC attempts): an object that does not exist **or** that the caller has no
  relationship with returns `404 <RESOURCE>_NOT_FOUND`. A caller who can see the object but may not perform the
  action (for example an `ORG_MEMBER` requesting a payout on their organisation's campaign) receives
  `403 PERMISSION_DENIED`.
- **Staff routes:** missing permission → `403 PERMISSION_DENIED` (the route is known to staff). Objects whose
  existence is itself restricted — `RESTRICTED_STR` compliance cases and their links — return `404` to anyone
  without `str.prepare`, and are excluded from list results.
- **Non-staff on `/admin`** → `404 ROUTE_NOT_FOUND`.

## 11. Per-audience DTOs and field exposure

| Resource | Public | Owner / self | Staff | Never in any DTO |
|---|---|---|---|---|
| Campaign | `PublicCampaign`, `PublicCampaignSummary` | `OwnerCampaign` (incl. `payouts_paused`, owner-safe `review_feedback`) | `StaffCampaign` (risk tier, policy version, hold refs, case ids) | Internal risk scores in public/owner views; hold or case reasons for owners |
| Financial summary | `RaisedAmounts` per currency | `CampaignFinancialSummary` (separate money states) | `StaffCampaignFinancialSummary` (+ holds, ledger account ids, dispute exposure) | A single combined balance; cross-currency totals |
| Donation | `PublicDonation` (anonymity applied) | `Donation` (donor), `OwnerDonationView` (owner; contact only with consent) | `StaffDonation` (masked); `DonorUnmaskResult` via audited action | Anonymous donor identity to public/owner; donor contact without consent |
| Payment | — | `PaymentIntent`, `PaymentStatusView` | `StaffPaymentIntent`, `PaymentEvent` | Card data (never held), raw provider payloads, full payer MSISDN outside the payer's own view |
| Refund | — | `RefundRequest` | `StaffRefundRequest`, `RefundTransaction` | Internal funding plan detail in donor view |
| Payout request | — | `PayoutRequest`, `PayoutStatusView`, `PayoutTimelineItem`, `PayoutEligibilityPreview` | `StaffPayoutRequest` (tier, approvals, eligibility decisions) | Eligibility check ids, approver identities, hold/case/screening reasons in owner views |
| Payout destination | — | `PayoutDestination` (masked) | `PayoutDestination` (masked) | Full account number, ciphertext, blind index, key ids (reveal only through the audited, case-bound flow) |
| User | `PublicUser` (display name + badge) | `User` (`/me`, full own contacts) | `StaffUserView` (masked) | Password/OTP/session/recovery hashes, MFA secrets |
| Verification | badge booleans | `KYCStatus`, `KYBStatus`, `KycAttempt` | `StaffKycCase` (masked identity summary), `DocumentViewSession`, `IdentityReveal` | Document content or URLs outside view sessions; full identity numbers outside reveal; vendor raw results |
| Organisation | `PublicOrganisation` | `Organisation` | `StaffOrganisationView` | Persons' identity data |
| Beneficiary | `PublicBeneficiary` (minimised; minors and health data hidden by default, PD-39) | `Beneficiary` | `StaffBeneficiaryVerification` | Evidence content |
| Compliance / risk | — | (only `payouts_paused`, `ACCOUNT_RESTRICTED`, `PAYOUTS_PAUSED`) | `ComplianceCase`, `Hold`, `RiskAssessment`, `ScreeningHit` | STR existence or records outside `str.prepare`; screening hits and notes outside COMPLIANCE |

C3 inputs (identity numbers, dates of birth, payout account numbers) are `writeOnly` in request schemas and are
never echoed. Responses that contain C2/C3 data or one-time values are sent with `Cache-Control: no-store`
(all authenticated responses are `no-store`, [SECURITY.md §11](../SECURITY.md)).

## 12. Audit requirements per endpoint class

Audit events are written in the **same database transaction** as the domain change
([ARCHITECTURE.md §5](../ARCHITECTURE.md)). They reference evidence and never embed C3 values, secrets or raw
payloads ([AUDIT.md §6](../AUDIT.md), [audit-evidence-model.md](../compliance/audit-evidence-model.md)).

| Class | Endpoints | Requirement |
|---|---|---|
| A0 — none | Public reads, own non-sensitive reads, status polling | Metrics only (not audit). |
| A1 — security chain | Login/OTP outcomes, logout/revocation, step-up, MFA enrolment, role grants/revocations, staff suspension, account recovery, audit queries, KYC document views, identity reveals, donor unmasking, break-glass | `security_audit_events` (separate hash chain, readable by SECURITY_ADMIN). Failures recorded with `outcome = failure/denied`. |
| A2 — domain change | Every user mutation of a domain object (campaign lifecycle, beneficiary, destination, payout request/cancel, refund request, donation visibility) | `audit_events` with actor, target, before/after (redacted allow-list), `request_id`. |
| A3 — justified staff action (J) | Every staff mutation | `reason_code` + `justification` required (`422 JUSTIFICATION_REQUIRED`), maker/checker roles recorded, evidence ids linked. Denied checker attempts are audited (`payout.approval.denied_self` and equivalents). |
| A4 — sensitive read | C3 access and unmasking | One event per access with justification, case/review id, IP/device; never the value. `kyc.status.viewed` is sampled. |

Audit action names in the catalogue come from [AUDIT.md §5](../AUDIT.md). Names marked **(new)** are proposed by
this document and must be added to the closed action catalogue in Stage 3 (unknown actions fail tests).

## 13. Asynchronous work and uploads

- Long-running work returns `202` with a job or resource id (`Accepted`, `ReportJob`, `DataRightsRequest`) and is
  polled. No endpoint blocks on a provider call longer than the provider timeout; payment creation returns the
  committed intent with whatever state the synchronous provider response allowed (`PENDING`,
  `REQUIRES_ACTION`, `FAILED` or `UNKNOWN`).
- Uploads use a two-step presigned pattern: request an `UploadSession` (presigned PUT, ≤ 5 min, size and type
  bound, random key, quarantine prefix), upload directly to object storage, then attach by `upload_session_id`.
  Nothing is readable until scanned; `409 UPLOAD_NOT_READY` while scanning, `422 UPLOAD_REJECTED` on failure
  (no scanner detail). KYC objects go to `private-kyc` through the `kyc` module only
  ([identity-data-protection.md §6](../security/identity-data-protection.md)).
- Staff document viewing is a **view session**, never a download: `POST .../view-sessions` returns a short-lived
  (≤ `STORAGE_KYC_PRESIGN_TTL`, 5 min) watermarked viewer URL, requires `kyc.document.view`, an open case
  assigned to the caller, a justification and fresh step-up, and emits `kyc.document.viewed`.

## 14. Payment and payout semantics on the API

- **Payment status.** `GET /payments/{payment_id}` returns `PaymentStatusView`. `UNKNOWN` is surfaced as its own
  status with `do_not_pay_again: true` and `donor_message_code: CONFIRMING_DO_NOT_PAY_AGAIN` ("We're confirming
  your payment. Don't pay again"). It is never shown as failed. `FAILED` carries a coarse `failure_category` and
  allows a **new** donation. Polling never triggers confirmation. **Duplicate guard (baseline §12 I-7,
  adopted):** creating a payment for the same donor (user, or guest identified by payer phone/email), campaign
  and amount while an earlier one is `UNKNOWN` returns `409 PAYMENT_OUTCOME_UNKNOWN` ("don't pay again"). The
  donor may override deliberately with `acknowledged_unresolved_payment_id` only after a configurable delay
  (INTERNAL_RISK limit; `meta.override_available_after`).
- **Payouts — owner side.** Owners can request (`POST /campaigns/{campaign_id}/payout-requests`), cancel
  **before** `SUBMITTED` (`POST /payout-requests/{id}/cancel`) and read. There is **no owner endpoint that changes
  a payout's status after submission**; a cancel attempt at or after `SUBMITTED` returns
  `409 PAYOUT_ALREADY_SUBMITTED`. `UNKNOWN` appears to the owner as `display_status: CONFIRMING`; it is never
  resubmitted and is resolved only by status query, webhook or reconciliation.
- **Eligibility failures** return `422 PAYOUT_NOT_ELIGIBLE` with coarse categories (error-model §6). Every hold,
  compliance case, screening, risk or pool-integrity failure maps to the single category `PAYOUTS_PAUSED`.
- **Staff resolution of UNKNOWN** is never a free-form status edit. It is a provider status query
  (`status-checks`, which only enqueues an authoritative query) or a reconciliation resolution with evidence and
  maker-checker (`REC-09`/`REC-10`).
- **Refunds** go to the original instrument only; there is no destination field on any refund request.

## 15. Endpoint catalogue

Conventions for the tables:

- Paths are shown with their full prefix. **Auth** uses the labels of §9.1. **Authorization** names the staff
  permission and/or the ownership predicate; **MC** marks the maker or checker role in a maker-checker pair.
- **Request → response** names schemas in `components/schemas` of the OpenAPI file; `Page<X>` is the list
  envelope; `—` means no body or not yet specified.
- **Specific errors** omit codes common to a whole class of routes: all routes may return `MALFORMED_REQUEST`,
  `RATE_LIMITED`, `INTERNAL_ERROR`, `SERVICE_UNAVAILABLE`; session routes `AUTHENTICATION_REQUIRED`,
  `SESSION_EXPIRED`; unsafe cookie routes `CSRF_TOKEN_INVALID`; staff routes `MFA_REQUIRED`,
  `PERMISSION_DENIED`, and `JUSTIFICATION_REQUIRED` on actions; step-up routes `STEP_UP_REQUIRED`; body routes
  `VALIDATION_FAILED`; idempotent routes the four `IDEMPOTENCY_*` codes; list routes `INVALID_CURSOR`,
  `INVALID_FILTER`. The OpenAPI operation lists the full set in `x-fundzim-error-codes`.
- **List** shows `cursor` for paginated endpoints, then allow-listed filters, then (after `·`) sort keys.
- **Idem.**: `required` / `optional` / `n/a`. **RL class**: §8. **Audit event**: §12; `(J)` = justification
  required; `(new)` = proposed action name.
- `ⁿ` after an ID = not yet in the OpenAPI file (§16).

### Authentication

Sessions, OTP login, step-up, staff MFA.

| ID | Method & path | Auth | Authorization (permission / ownership) | Request → response | Specific errors | List: filters · sort | Idem. | RL class | Audit event |
|---|---|---|---|---|---|---|---|---|---|
| AUTH-01 | `GET /api/v1/auth/session` | session | Session holder (user or staff). No session → 401; there are no guest sessions. | — → 200 SessionInfo | — | — | n/a | RL-USER-READ | — |
| AUTH-02 | `POST /api/v1/auth/otp` | none | Anyone; enumeration-resistant uniform 202. | OtpSendRequest → 202 OtpChallenge | `CHALLENGE_REQUIRED` | — | n/a | RL-OTP-SEND | auth.otp.sent (security) |
| AUTH-03 | `POST /api/v1/auth/otp/verify` | none | Holder of the challenge. | OtpVerifyRequest → 200 SessionInfo | `INVALID_CREDENTIALS`, `OTP_ATTEMPTS_EXCEEDED`, `OTP_EXPIRED`, `OTP_INVALID` | — | n/a | RL-OTP-VERIFY | auth.login.succeeded / auth.login.failed / auth.otp.failed_max_attempts (security) |
| AUTH-04 | `POST /api/v1/auth/logout` | session | Session holder. | — → 204 (no body) | — | — | n/a | RL-USER-WRITE | auth.session.revoked (security) |
| AUTH-05 | `POST /api/v1/auth/step-up/challenges` | session | Signed-in user. | StepUpChallengeRequest → 201 OtpChallenge | — | — | n/a | RL-OTP-SEND | auth.otp.sent (security) |
| AUTH-06 | `POST /api/v1/auth/step-up/verify` | session | Signed-in user. | StepUpVerifyRequest → 200 SessionInfo | `OTP_ATTEMPTS_EXCEEDED`, `OTP_EXPIRED`, `OTP_INVALID` | — | n/a | RL-OTP-VERIFY | auth.step_up.succeeded (security) |
| AUTH-07 | `GET /api/v1/me/sessions` | session | Own sessions only. | — → 200 Page<SessionSummary> | — | cursor | n/a | RL-USER-READ | — |
| AUTH-08 | `DELETE /api/v1/me/sessions/{session_id}` | session | Own sessions only (others → 404). | — → 204 (no body) | `SESSION_NOT_FOUND` | — | n/a | RL-USER-WRITE | auth.session.revoked (security) |
| AUTH-09 | `POST /api/v1/admin/auth/login` | none | STAFF accounts only (password, Argon2id; baseline §12 I-6); uniform failure. | StaffLoginRequest → 200 StaffMfaChallenge | `CHALLENGE_REQUIRED`, `INVALID_CREDENTIALS` | — | n/a | RL-OTP-VERIFY | auth.login.failed on failure (security) |
| AUTH-10 | `POST /api/v1/admin/auth/mfa/verify` | none | Holder of the MFA challenge. | StaffMfaVerifyRequest → 200 SessionInfo | `INVALID_CREDENTIALS`, `OTP_ATTEMPTS_EXCEEDED` | — | n/a | RL-OTP-VERIFY | auth.login.succeeded / auth.login.failed (security) |
| AUTH-11 | `POST /api/v1/admin/auth/step-up` | staff + MFA | Any staff. | — → 201 StaffMfaChallenge | — | — | n/a | RL-STAFF-WRITE | — |
| AUTH-12 | `POST /api/v1/admin/auth/step-up/verify` | staff + MFA | Any staff. | StaffMfaVerifyRequest → 200 SessionInfo | `INVALID_CREDENTIALS`, `OTP_ATTEMPTS_EXCEEDED` | — | n/a | RL-OTP-VERIFY | auth.step_up.succeeded (security) |
| AUTH-13 | `GET /api/v1/admin/me` | staff + MFA | Any staff. | — → 200 StaffSelf | — | — | n/a | RL-STAFF-READ | — |
| AUTH-14 | `POST /api/v1/admin/me/mfa-methods` | staff + MFA + step-up | Self. | MfaEnrolmentRequest → 201 MfaMethod | — | — | n/a | RL-STAFF-WRITE | auth.mfa.enrolled (security) |
| AUTH-15 | `DELETE /api/v1/admin/me/mfa-methods/{method_id}` | staff + MFA + step-up | Self. | — → 204 (no body) | `MFA_LAST_METHOD`, `MFA_METHOD_NOT_FOUND` | — | n/a | RL-STAFF-WRITE | auth.mfa.removed (security) |
| AUTH-16 ⁿ | `POST /api/v1/auth/password/login` | none | Anyone; uniform failure. | — → 200 SessionInfo | `INVALID_CREDENTIALS` | — | n/a | RL-OTP-VERIFY | auth.login.* (security) |

### User profiles

Signed-in user's own account (`/me`).

| ID | Method & path | Auth | Authorization (permission / ownership) | Request → response | Specific errors | List: filters · sort | Idem. | RL class | Audit event |
|---|---|---|---|---|---|---|---|---|---|
| USR-01 | `GET /api/v1/me` | session | Self. | — → 200 User | — | — | n/a | RL-USER-READ | — |
| USR-02 | `PATCH /api/v1/me/profile` | session | Self. | UserProfileUpdate → 200 User | `VERSION_CONFLICT` | — | optional | RL-USER-WRITE | user.profile.updated (new) |
| USR-03 | `POST /api/v1/me/contact-changes` | session + step-up | Self. | ContactChangeRequest → 201 ContactChange | `ACCOUNT_RESTRICTED`, `CONTACT_IN_USE` | — | optional | RL-SENSITIVE-USER | auth.otp.sent (security) |
| USR-04 | `POST /api/v1/me/contact-changes/{contact_change_id}/verify` | session + step-up | Self. | ContactChangeVerifyRequest → 200 User | `CONTACT_CHANGE_NOT_FOUND`, `OTP_ATTEMPTS_EXCEEDED`, `OTP_EXPIRED`, `OTP_INVALID` | — | n/a | RL-OTP-VERIFY | user.phone.changed / user.email.changed + payout.hold.applied (system) |
| USR-05 | `GET /api/v1/me/activity` | session | Self. | — → 200 Page<ActivityItem> | — | cursor; kind, from, to | n/a | RL-USER-READ | — |
| USR-06 | `POST /api/v1/me/data-export-requests` | session + step-up | Self. | — → 202 DataRightsRequest | — | — | optional | RL-SENSITIVE-USER | user.data_export.requested |
| USR-07 | `POST /api/v1/me/deletion-requests` | session + step-up | Self. Blocked while funds/payouts/cases exist (reason-free). | — → 202 DataRightsRequest | `ACCOUNT_RESTRICTED` | — | optional | RL-SENSITIVE-USER | user.deletion.requested |

### Organisations

Organisations, membership and invitations (ORG_ADMIN / ORG_MEMBER).

| ID | Method & path | Auth | Authorization (permission / ownership) | Request → response | Specific errors | List: filters · sort | Idem. | RL class | Audit event |
|---|---|---|---|---|---|---|---|---|---|
| ORG-01 | `POST /api/v1/organisations` | session | IDENTITY_VERIFIED user. | OrganisationCreateRequest → 201 Organisation | `ACCOUNT_RESTRICTED`, `VERIFICATION_REQUIRED` | — | optional | RL-USER-WRITE | organisation.created |
| ORG-02 | `GET /api/v1/me/organisations` | session | Own memberships. | — → 200 Page<OrganisationMembershipSummary> | — | cursor | n/a | RL-USER-READ | — |
| ORG-03 | `GET /api/v1/organisations/{org_id}` | session | `org.view` (member); non-members → 404. | — → 200 Organisation | `ORGANISATION_NOT_FOUND` | — | n/a | RL-USER-READ | — |
| ORG-04 | `PATCH /api/v1/organisations/{org_id}` | session | `org.manage` (ORG_ADMIN). Members → 403. | OrganisationUpdateRequest → 200 Organisation | `ORGANISATION_NOT_FOUND`, `VERSION_CONFLICT` | — | optional | RL-USER-WRITE | organisation.updated (new) |
| ORG-05 | `GET /api/v1/organisations/{org_id}/members` | session | `org.view`. | — → 200 Page<OrganisationMember> | `ORGANISATION_NOT_FOUND` | cursor | n/a | RL-USER-READ | — |
| ORG-06 | `PATCH /api/v1/organisations/{org_id}/members/{member_id}` | session + step-up | `org.manage`; cannot remove last ORG_ADMIN. | OrganisationMemberUpdate → 200 OrganisationMember | `ORGANISATION_NOT_FOUND`, `ORG_LAST_ADMIN`, `ORG_MEMBER_NOT_FOUND`, `VERSION_CONFLICT` | — | optional | RL-USER-WRITE | organisation.member.role_changed (new) |
| ORG-07 | `DELETE /api/v1/organisations/{org_id}/members/{member_id}` | session | `org.manage`, or the member themself (leave). | — → 204 (no body) | `ORGANISATION_NOT_FOUND`, `ORG_LAST_ADMIN`, `ORG_MEMBER_NOT_FOUND` | — | n/a | RL-USER-WRITE | organisation.member.removed |
| ORG-08 | `POST /api/v1/organisations/{org_id}/invitations` | session | `org.manage`. | OrganisationInvitationCreate → 201 OrganisationInvitation | `ORGANISATION_NOT_FOUND`, `ORG_ALREADY_MEMBER` | — | optional | RL-USER-WRITE | organisation.invitation.created (new) |
| ORG-09 | `GET /api/v1/organisations/{org_id}/invitations` | session | `org.manage`. | — → 200 Page<OrganisationInvitation> | `ORGANISATION_NOT_FOUND` | cursor; status | n/a | RL-USER-READ | — |
| ORG-10 | `DELETE /api/v1/organisations/{org_id}/invitations/{invitation_id}` | session | `org.manage`. | — → 204 (no body) | `INVITATION_NOT_FOUND`, `ORGANISATION_NOT_FOUND` | — | n/a | RL-USER-WRITE | organisation.invitation.revoked (new) |
| ORG-11 ⁿ | `POST /api/v1/organisation-invitations/accept` | session | Invitee whose verified contact matches. | — → 200 OrganisationMember | `INVITATION_NOT_FOUND` | — | n/a | RL-USER-WRITE | organisation.member.added |

### Beneficiaries

Beneficiaries, evidence, consents, institution payees; staff verification decisions.

| ID | Method & path | Auth | Authorization (permission / ownership) | Request → response | Specific errors | List: filters · sort | Idem. | RL class | Audit event |
|---|---|---|---|---|---|---|---|---|---|
| BEN-01 | `POST /api/v1/beneficiaries` | session | Owner (self) or `org.beneficiary.manage`. `INDIVIDUAL_OTHER` gated by feature flag. | BeneficiaryCreateRequest → 201 Beneficiary | `BENEFICIARY_TYPE_NOT_ALLOWED`, `FEATURE_NOT_AVAILABLE`, `INSTITUTION_PAYEE_NOT_FOUND`, `VERIFICATION_REQUIRED` | — | optional | RL-USER-WRITE | beneficiary.declared (new) |
| BEN-02 | `GET /api/v1/beneficiaries` | session | Owner-scoped query. | — → 200 Page<Beneficiary> | — | cursor; organisation_id, verification_status | n/a | RL-USER-READ | — |
| BEN-03 | `GET /api/v1/beneficiaries/{beneficiary_id}` | session | Owner or org member (`org.beneficiary.manage`); else 404. | — → 200 Beneficiary | `BENEFICIARY_NOT_FOUND` | — | n/a | RL-USER-READ | — |
| BEN-04 | `PATCH /api/v1/beneficiaries/{beneficiary_id}` | session | As BEN-03. | BeneficiaryUpdateRequest → 200 Beneficiary | `BENEFICIARY_LOCKED`, `BENEFICIARY_NOT_FOUND`, `VERSION_CONFLICT` | — | optional | RL-USER-WRITE | beneficiary.updated (new); campaign.beneficiary.changed when linked |
| BEN-05 | `POST /api/v1/beneficiaries/{beneficiary_id}/evidence/upload-sessions` | session | As BEN-03. | UploadSessionRequest → 201 UploadSession | `BENEFICIARY_NOT_FOUND` | — | n/a | RL-UPLOAD | — |
| BEN-06 | `POST /api/v1/beneficiaries/{beneficiary_id}/evidence` | session | As BEN-03. | BeneficiaryEvidenceCreate → 201 BeneficiaryEvidence | `BENEFICIARY_NOT_FOUND`, `UPLOAD_EXPIRED`, `UPLOAD_NOT_READY`, `UPLOAD_REJECTED` | — | optional | RL-UPLOAD | beneficiary.evidence.uploaded (new) |
| BEN-07 | `POST /api/v1/beneficiaries/{beneficiary_id}/consents` | session | As BEN-03. | ConsentCreate → 201 ConsentSummary | `BENEFICIARY_NOT_FOUND`, `UPLOAD_NOT_READY` | — | optional | RL-USER-WRITE | beneficiary.consent.recorded (new) |
| BEN-08 | `POST /api/v1/beneficiaries/{beneficiary_id}/verification-submissions` | session | As BEN-03. | — → 200 Beneficiary | `BENEFICIARY_LOCKED`, `BENEFICIARY_NOT_FOUND`, `CONSENT_REQUIRED` | — | optional | RL-USER-WRITE | beneficiary.verification.submitted (new) |
| BEN-09 | `PUT /api/v1/campaigns/{campaign_id}/beneficiary` | session | Campaign editor (owner / `org.campaign.edit`); beneficiary must belong to the same owner. | CampaignBeneficiaryLink → 200 OwnerCampaign | `BENEFICIARY_NOT_FOUND`, `CAMPAIGN_NOT_EDITABLE`, `CAMPAIGN_NOT_FOUND`, `VERSION_CONFLICT` | — | optional | RL-USER-WRITE | campaign.beneficiary.changed |
| BEN-10 | `GET /api/v1/institution-payees` | session | Signed-in users. | — → 200 Page<InstitutionPayee> | — | cursor; q, institution_type, city | n/a | RL-USER-READ | — |
| BEN-11 | `POST /api/v1/institution-payees/proposals` | session | Signed-in users. | InstitutionPayeeProposal → 202 InstitutionPayee | — | — | optional | RL-USER-WRITE | institution_payee.proposed (new) |
| BEN-12 | `GET /api/v1/admin/beneficiary-verifications` | staff + MFA | `beneficiary.verification.decide`. | — → 200 Page<StaffBeneficiaryVerification> | — | cursor; status, risk_tier, assigned_to · created_at, -created_at | n/a | RL-STAFF-READ | — |
| BEN-13 | `POST /api/v1/admin/beneficiary-verifications/{verification_id}/decisions` | staff + MFA + step-up | `beneficiary.verification.decide`; not own/linked; HIGH tier needs second approver (returns PENDING). **MC:** HIGH tier: second, different approver. | BeneficiaryVerificationDecisionRequest → 200 StaffBeneficiaryVerification | `APPROVER_CONFLICT`, `BENEFICIARY_VERIFICATION_NOT_FOUND`, `KYC_DECISION_INVALID`, `STAFF_CONFLICT_OF_INTEREST` | — | optional | RL-STAFF-WRITE | beneficiary.verification.decided (J) |

### Campaign creation

Owner campaign authoring and lifecycle (owner / organisation members).

| ID | Method & path | Auth | Authorization (permission / ownership) | Request → response | Specific errors | List: filters · sort | Idem. | RL class | Audit event |
|---|---|---|---|---|---|---|---|---|---|
| CMP-01 | `POST /api/v1/campaigns` | session | BASIC_VERIFIED owner; organisation campaigns need `org.campaign.create`. | CampaignCreateRequest → 201 OwnerCampaign | `ACCOUNT_RESTRICTED`, `CATEGORY_NOT_FOUND`, `CURRENCY_NOT_SUPPORTED`, `FEATURE_NOT_AVAILABLE`, `INVALID_MONEY`, `ORGANISATION_NOT_FOUND`, `VERIFICATION_REQUIRED` | — | optional | RL-USER-WRITE | campaign.created |
| CMP-02 | `GET /api/v1/me/campaigns` | session | Owner-scoped query. | — → 200 Page<OwnerCampaign> | — | cursor; status, organisation_id · -updated_at, -created_at | n/a | RL-USER-READ | — |
| CMP-03 | `GET /api/v1/campaigns/{campaign_id}` | session | Owner or org member; else 404. | — → 200 OwnerCampaign | `CAMPAIGN_NOT_FOUND` | — | n/a | RL-USER-READ | — |
| CMP-04 | `PATCH /api/v1/campaigns/{campaign_id}` | session | Owner / `org.campaign.edit`. | CampaignUpdateRequest → 200 OwnerCampaign | `CAMPAIGN_NOT_EDITABLE`, `CAMPAIGN_NOT_FOUND`, `CURRENCY_MISMATCH`, `FUNDRAISING_AUTHORITY_INVALID`, `INVALID_MONEY`, `VERSION_CONFLICT` | — | optional | RL-USER-WRITE | campaign.edited (new) / campaign.material_edit.flagged |
| CMP-05 | `POST /api/v1/campaigns/{campaign_id}/media/upload-sessions` | session | As CMP-04. | UploadSessionRequest → 201 UploadSession | `CAMPAIGN_NOT_EDITABLE`, `CAMPAIGN_NOT_FOUND` | — | n/a | RL-UPLOAD | — |
| CMP-06 | `POST /api/v1/campaigns/{campaign_id}/media` | session | As CMP-04. | CampaignMediaAttachRequest → 201 CampaignMedia | `CAMPAIGN_NOT_FOUND`, `UPLOAD_EXPIRED`, `UPLOAD_NOT_READY`, `UPLOAD_REJECTED` | — | optional | RL-UPLOAD | campaign.media.added (new) |
| CMP-07 | `DELETE /api/v1/campaigns/{campaign_id}/media/{media_id}` | session | As CMP-04. | — → 204 (no body) | `CAMPAIGN_NOT_EDITABLE`, `CAMPAIGN_NOT_FOUND`, `MEDIA_NOT_FOUND` | — | n/a | RL-USER-WRITE | campaign.media.removed (new) |
| CMP-08 | `POST /api/v1/campaigns/{campaign_id}/submit` | session | Owner IDENTITY_VERIFIED (org: KYB level per policy + `org.campaign.edit`). | CampaignSubmitRequest → 200 CampaignTransitionResult | `ACCOUNT_RESTRICTED`, `CAMPAIGN_INCOMPLETE`, `CAMPAIGN_INVALID_TRANSITION`, `CAMPAIGN_NOT_FOUND`, `CAMPAIGN_RESUBMISSION_LIMIT`, `FUNDRAISING_AUTHORITY_REQUIRED`, `VERIFICATION_REQUIRED`, `VERSION_CONFLICT` | — | optional | RL-USER-WRITE | campaign.submitted |
| CMP-09 | `POST /api/v1/campaigns/{campaign_id}/withdraw-submission` | session | As CMP-04. | CampaignOwnerTransitionRequest → 200 CampaignTransitionResult | `CAMPAIGN_INVALID_TRANSITION`, `CAMPAIGN_NOT_FOUND`, `VERSION_CONFLICT` | — | optional | RL-USER-WRITE | campaign.state.changed |
| CMP-10 | `POST /api/v1/campaigns/{campaign_id}/publish` | session | As CMP-04; owner still IDENTITY_VERIFIED. | CampaignOwnerTransitionRequest → 200 CampaignTransitionResult | `ACCOUNT_RESTRICTED`, `CAMPAIGN_INVALID_TRANSITION`, `CAMPAIGN_NOT_FOUND`, `VERIFICATION_REQUIRED`, `VERSION_CONFLICT` | — | optional | RL-USER-WRITE | campaign.state.changed |
| CMP-11 | `POST /api/v1/campaigns/{campaign_id}/close` | session + step-up | Owner / `org.campaign.edit`. | CampaignOwnerTransitionRequest → 200 CampaignTransitionResult | `CAMPAIGN_INVALID_TRANSITION`, `CAMPAIGN_NOT_FOUND`, `VERSION_CONFLICT` | — | optional | RL-SENSITIVE-USER | campaign.state.changed |
| CMP-12 | `POST /api/v1/campaigns/{campaign_id}/cancel` | session + step-up | Owner / `org.campaign.cancel` (ORG_ADMIN). | CampaignOwnerTransitionRequest → 200 CampaignTransitionResult | `CAMPAIGN_INVALID_TRANSITION`, `CAMPAIGN_NOT_FOUND`, `VERSION_CONFLICT` | — | optional | RL-SENSITIVE-USER | campaign.state.changed / campaign.cancellation.requested (new) |
| CMP-13 | `GET /api/v1/campaigns/{campaign_id}/financial-summary` | session | Owner / `org.finance.view` (ORG_ADMIN). ORG_MEMBER → 403. | — → 200 CampaignFinancialSummary | `CAMPAIGN_NOT_FOUND` | — | n/a | RL-USER-READ | — |
| CMP-14 | `GET /api/v1/campaigns/{campaign_id}/donations` | session | Owner / org member. | — → 200 Page<OwnerDonationView> | `CAMPAIGN_NOT_FOUND` | cursor; currency, from, to · -donated_at, -amount | n/a | RL-USER-READ | — |
| CMP-15 | `GET /api/v1/campaigns/{campaign_id}/statement` | session | As CMP-13. | — → 200 CampaignStatement | `CAMPAIGN_NOT_FOUND` | query: from, to, currency | n/a | RL-USER-READ | — |
| CMP-16 ⁿ | `GET /api/v1/campaigns/{campaign_id}/share-stats` | session | As CMP-03. | — → 200 — | `CAMPAIGN_NOT_FOUND` | — | n/a | RL-USER-READ | — |
| CMP-17 ⁿ | `GET /api/v1/campaigns/{campaign_id}/donations/export` | session + step-up | As CMP-13. | — → 200 text/csv | `CAMPAIGN_NOT_FOUND` | — | n/a | RL-SENSITIVE-USER | campaign.donor_export (new) |

### Campaign updates

Owner posts and public update feed.

| ID | Method & path | Auth | Authorization (permission / ownership) | Request → response | Specific errors | List: filters · sort | Idem. | RL class | Audit event |
|---|---|---|---|---|---|---|---|---|---|
| UPD-01 | `POST /api/v1/campaigns/{campaign_id}/updates` | session | Owner / `org.campaign.edit`; campaign ACTIVE or COMPLETED. | CampaignUpdateCreate → 201 CampaignUpdate | `ACCOUNT_RESTRICTED`, `CAMPAIGN_NOT_EDITABLE`, `CAMPAIGN_NOT_FOUND` | — | optional | RL-USER-WRITE | campaign.update.posted (new) |
| UPD-02 | `GET /api/v1/campaigns/{campaign_id}/updates` | session | As CMP-03. | — → 200 Page<CampaignUpdate> | `CAMPAIGN_NOT_FOUND` | cursor | n/a | RL-USER-READ | — |
| UPD-03 | `PATCH /api/v1/campaigns/{campaign_id}/updates/{update_id}` | session | As UPD-01. | CampaignUpdateCreate → 200 CampaignUpdate | `CAMPAIGN_NOT_FOUND`, `CAMPAIGN_UPDATE_NOT_FOUND`, `VERSION_CONFLICT` | — | optional | RL-USER-WRITE | campaign.update.edited (new) |
| UPD-04 | `DELETE /api/v1/campaigns/{campaign_id}/updates/{update_id}` | session | As UPD-01. | — → 204 (no body) | `CAMPAIGN_NOT_FOUND`, `CAMPAIGN_UPDATE_NOT_FOUND` | — | n/a | RL-USER-WRITE | campaign.update.withdrawn (new) |
| UPD-05 | `GET /api/v1/public/campaigns/{public_code}/updates` | none | Public campaigns only. | — → 200 Page<PublicCampaignUpdate> | `CAMPAIGN_NOT_FOUND` | cursor | n/a | RL-PUBLIC-READ | — |

### Campaign discovery

Public, cacheable reads. Only ACTIVE/COMPLETED (and noticed SUSPENDED/FROZEN/CANCELLED) campaigns with PUBLIC visibility; UNLISTED by direct link only.

| ID | Method & path | Auth | Authorization (permission / ownership) | Request → response | Specific errors | List: filters · sort | Idem. | RL class | Audit event |
|---|---|---|---|---|---|---|---|---|---|
| DIS-01 | `GET /api/v1/public/campaigns` | none | Public. | — → 200 Page<PublicCampaignSummary> | — | cursor; q, category, currency, status, organisation_id · -published_at, -raised, ends_at, trending | n/a | RL-PUBLIC-READ | — |
| DIS-02 | `GET /api/v1/public/campaigns/{public_code}` | none | Public; HIDDEN/non-public states → 404. | — → 200 PublicCampaign | `CAMPAIGN_NOT_FOUND` | — | n/a | RL-PUBLIC-READ | — |
| DIS-03 | `GET /api/v1/public/campaigns/{public_code}/donations` | none | Public. | — → 200 Page<PublicDonation> | `CAMPAIGN_NOT_FOUND` | cursor · -donated_at | n/a | RL-PUBLIC-READ | — |
| DIS-04 | `GET /api/v1/public/campaign-categories` | none | Public. | — → 200 Page<Category> | — | cursor | n/a | RL-PUBLIC-READ | — |
| DIS-05 | `GET /api/v1/public/currencies` | none | Public. | — → 200 Page<CurrencyInfo> | — | cursor | n/a | RL-PUBLIC-READ | — |
| DIS-06 | `GET /api/v1/public/organisations/{org_id}` | none | Public; only organisations with a public campaign or verified badge. | — → 200 PublicOrganisation | `ORGANISATION_NOT_FOUND` | — | n/a | RL-PUBLIC-READ | — |
| DIS-07 | `POST /api/v1/public/campaigns/{public_code}/abuse-reports` | none or user session | Anyone; Origin checks (and CSRF token if a session cookie is sent); challenge after threshold. | AbuseReportCreate → 202 AbuseReportAck | `CAMPAIGN_NOT_FOUND`, `CHALLENGE_REQUIRED` | — | n/a | RL-PUBLIC-WRITE | campaign.reported |

### Campaign moderation (staff)

Review pipeline, suspension, freeze, visibility, abuse reports.

| ID | Method & path | Auth | Authorization (permission / ownership) | Request → response | Specific errors | List: filters · sort | Idem. | RL class | Audit event |
|---|---|---|---|---|---|---|---|---|---|
| MOD-01 | `GET /api/v1/admin/campaigns` | staff + MFA | `campaign.view`. | — → 200 Page<StaffCampaign> | — | cursor; status, queue, risk_tier, category, claimed_by, organisation_id, q · submitted_at, -submitted_at, -updated_at, sla_due_at | n/a | RL-STAFF-READ | — |
| MOD-02 | `GET /api/v1/admin/campaigns/{campaign_id}` | staff + MFA | `campaign.view`. | — → 200 StaffCampaign | `CAMPAIGN_NOT_FOUND` | — | n/a | RL-STAFF-READ | — |
| MOD-03 | `POST /api/v1/admin/campaigns/{campaign_id}/review/claim` | staff + MFA | `campaign.review`; conflict attestation. | ReviewClaimRequest → 200 StaffCampaign | `CAMPAIGN_INVALID_TRANSITION`, `CAMPAIGN_NOT_FOUND`, `CAMPAIGN_REVIEW_ALREADY_CLAIMED`, `STAFF_CONFLICT_OF_INTEREST`, `VERSION_CONFLICT` | — | optional | RL-STAFF-WRITE | campaign.review.claimed |
| MOD-04 | `POST /api/v1/admin/campaigns/{campaign_id}/review/checks` | staff + MFA | `campaign.review`; claimant only. | ReviewCheckRecord → 200 StaffCampaign | `CAMPAIGN_INVALID_TRANSITION`, `CAMPAIGN_NOT_FOUND` | — | optional | RL-STAFF-WRITE | campaign.review.check_recorded |
| MOD-05 | `POST /api/v1/admin/campaigns/{campaign_id}/review/decision` | staff + MFA | `campaign.decide`; claimant; HIGH tier sign-offs; KYC decider ≠ campaign decider for HIGH. | ReviewDecisionRequest → 200 StaffCampaign | `CAMPAIGN_INVALID_TRANSITION`, `CAMPAIGN_NOT_FOUND`, `STAFF_CONFLICT_OF_INTEREST`, `VERSION_CONFLICT` | — | optional | RL-STAFF-WRITE | campaign.state.changed (J) |
| MOD-06 | `POST /api/v1/admin/campaigns/{campaign_id}/suspend` | staff + MFA | `campaign.suspend`. | StaffActionRequest → 200 ModerationAction | `CAMPAIGN_INVALID_TRANSITION`, `CAMPAIGN_NOT_FOUND` | — | optional | RL-STAFF-WRITE | campaign.state.changed (J) + payout.hold.applied (system) |
| MOD-07 | `POST /api/v1/admin/campaigns/{campaign_id}/unsuspend` | staff + MFA | `campaign.unsuspend`. | StaffActionRequest → 200 ModerationAction | `CAMPAIGN_INVALID_TRANSITION`, `CAMPAIGN_NOT_FOUND` | — | optional | RL-STAFF-WRITE | campaign.state.changed (J) |
| MOD-08 | `POST /api/v1/admin/campaigns/{campaign_id}/freeze` | staff + MFA + step-up | `campaign.freeze`. | StaffActionRequest → 200 ModerationAction | `CAMPAIGN_INVALID_TRANSITION`, `CAMPAIGN_NOT_FOUND` | — | required | RL-STAFF-WRITE | campaign.state.changed (J) + payout.hold.applied (J) |
| MOD-09 | `POST /api/v1/admin/campaigns/{campaign_id}/unfreeze-requests` | staff + MFA + step-up | `campaign.unfreeze.request`. **MC:** maker. | StaffActionRequest → 201 ModerationAction | `CAMPAIGN_INVALID_TRANSITION`, `CAMPAIGN_NOT_FOUND` | — | optional | RL-STAFF-WRITE | campaign.unfreeze.requested (new, J) |
| MOD-10 | `POST /api/v1/admin/campaigns/{campaign_id}/cancel` | staff + MFA + step-up | `campaign.cancel`. **MC:** maker when FROZEN. | StaffActionRequest → 200 ModerationAction | `CAMPAIGN_INVALID_TRANSITION`, `CAMPAIGN_NOT_FOUND` | — | optional | RL-STAFF-WRITE | campaign.state.changed (J) |
| MOD-11 | `POST /api/v1/admin/campaign-moderation-actions/{action_id}/approve` | staff + MFA + step-up | `campaign.unfreeze.approve` (unfreeze) or `campaign.cancel` (cancel); checker ≠ maker ≠ freezer. **MC:** checker. | StaffActionRequest → 200 ModerationAction | `APPROVAL_EXPIRED`, `APPROVER_CONFLICT`, `CAMPAIGN_INVALID_TRANSITION`, `MODERATION_ACTION_NOT_FOUND` | — | required | RL-STAFF-WRITE | campaign.state.changed (J) + payout.hold.released (J) |
| MOD-12 | `POST /api/v1/admin/campaign-moderation-actions/{action_id}/reject` | staff + MFA | As MOD-11. **MC:** checker. | StaffActionRequest → 200 ModerationAction | `APPROVAL_EXPIRED`, `APPROVER_CONFLICT`, `MODERATION_ACTION_NOT_FOUND` | — | optional | RL-STAFF-WRITE | campaign.moderation.rejected (new, J) |
| MOD-13 | `PATCH /api/v1/admin/campaigns/{campaign_id}/visibility` | staff + MFA | `content.moderate`. | VisibilityChangeRequest → 200 ModerationAction | `CAMPAIGN_NOT_FOUND`, `VERSION_CONFLICT` | — | optional | RL-STAFF-WRITE | campaign.visibility.changed (new, J) |
| MOD-14 | `POST /api/v1/admin/campaigns/{campaign_id}/updates/{update_id}/hide` | staff + MFA | `content.moderate`. | StaffActionRequest → 200 ModerationAction | `CAMPAIGN_NOT_FOUND`, `CAMPAIGN_UPDATE_NOT_FOUND` | — | optional | RL-STAFF-WRITE | campaign.update.hidden (new, J) |
| MOD-15 | `GET /api/v1/admin/abuse-reports` | staff + MFA | `campaign.review`. | — → 200 Page<StaffAbuseReport> | — | cursor; status, campaign_id, reason_category · -received_at | n/a | RL-STAFF-READ | — |
| MOD-16 | `POST /api/v1/admin/abuse-reports/{abuse_report_id}/resolve` | staff + MFA | `campaign.review`. | StaffActionRequest → 200 StaffAbuseReport | `ABUSE_REPORT_NOT_FOUND` | — | optional | RL-STAFF-WRITE | campaign.report.resolved (new, J) |
| MOD-17 | `GET /api/v1/admin/campaigns/{campaign_id}/status-history` | staff + MFA | `campaign.view`. | — → 200 Page<CampaignStatusHistoryItem> | `CAMPAIGN_NOT_FOUND` | cursor | n/a | RL-STAFF-READ | — |
| MOD-18 | `GET /api/v1/admin/campaigns/{campaign_id}/financial-summary` | staff + MFA | `ledger.view` or `hold.view` (FINANCE, COMPLIANCE). | — → 200 StaffCampaignFinancialSummary | `CAMPAIGN_NOT_FOUND` | — | n/a | RL-STAFF-READ | — |

### Donations

Donation creation (with payment intent), donor views, receipts, staff donation views.

| ID | Method & path | Auth | Authorization (permission / ownership) | Request → response | Specific errors | List: filters · sort | Idem. | RL class | Audit event |
|---|---|---|---|---|---|---|---|---|---|
| DON-01 | `GET /api/v1/public/campaigns/{public_code}/donation-options` | none | Public. | — → 200 DonationOptions | `CAMPAIGN_NOT_FOUND` | query: currency, amount_minor | n/a | RL-PUBLIC-READ | — |
| DON-02 | `POST /api/v1/campaigns/{campaign_id}/donations` | none or user session | Guest (no session; receives a one-time donation access token) or signed-in user; campaign ACTIVE; donor limits by verification level; restriction check (reason-free); duplicate-UNKNOWN guard (I-7). | DonationCreateRequest → 201 DonationCreated | `ACCOUNT_RESTRICTED`, `AMOUNT_ABOVE_MAXIMUM`, `AMOUNT_BELOW_MINIMUM`, `CAMPAIGN_NOT_ACCEPTING_DONATIONS`, `CAMPAIGN_NOT_FOUND`, `CHALLENGE_REQUIRED`, `CURRENCY_NOT_ACCEPTED`, `CURRENCY_NOT_SUPPORTED`, `DONATION_LIMIT_REACHED`, `INVALID_MONEY`, `PAYMENT_METHOD_UNAVAILABLE`, `PAYMENT_OUTCOME_UNKNOWN`, `PROVIDER_UNAVAILABLE` | — | required | RL-DONATE | payment.created; payment.provider.called |
| DON-03 | `GET /api/v1/me/donations` | session | Own donations. | — → 200 Page<Donation> | — | cursor; status, campaign_id, currency · -created_at | n/a | RL-USER-READ | — |
| DON-04 | `GET /api/v1/donations/{donation_id}` | session or donation token | Creating user session, or `X-Donation-Access-Token` for this donation; else 404. | — → 200 Donation | `DONATION_NOT_FOUND` | — | n/a | RL-USER-READ | — |
| DON-05 | `GET /api/v1/donations/{donation_id}/receipt` | session or donation token | As DON-04. | — → 200 Receipt | `DONATION_NOT_FOUND`, `PAYMENT_NOT_FOUND` | — | n/a | RL-USER-READ | — |
| DON-06 | `PATCH /api/v1/donations/{donation_id}/visibility` | session | Creating user only (the guest token is read-only, I-2). | DonationVisibilityUpdate → 200 Donation | `DONATION_NOT_FOUND` | — | optional | RL-USER-WRITE | donation.visibility.changed (new) |
| DON-07 ⁿ | `POST /api/v1/donations/{donation_id}/claim` | session | Signed-in user presenting the access token. | — → 200 Donation | `DONATION_NOT_FOUND` | — | n/a | RL-USER-WRITE | donation.claimed (new) |
| DON-08 | `GET /api/v1/admin/donations` | staff + MFA | `payment.view`. | — → 200 Page<StaffDonation> | — | cursor; campaign_id, status, currency, provider, receipt_number, from, to · -created_at | n/a | RL-STAFF-READ | — |
| DON-09 | `GET /api/v1/admin/donations/{donation_id}` | staff + MFA | `payment.view`. | — → 200 StaffDonation | `DONATION_NOT_FOUND` | — | n/a | RL-STAFF-READ | — |
| DON-10 | `POST /api/v1/admin/donations/{donation_id}/donor-unmask` | staff + MFA + step-up | `donor.identity.unmask`. | StaffActionRequest → 200 DonorUnmaskResult | `DONATION_NOT_FOUND` | — | n/a | RL-STAFF-SENSITIVE | donor.identity.unmasked (new, J; security chain) |

### Payment intents

Donor cancel; staff payment inspection and status queries.

| ID | Method & path | Auth | Authorization (permission / ownership) | Request → response | Specific errors | List: filters · sort | Idem. | RL class | Audit event |
|---|---|---|---|---|---|---|---|---|---|
| PAY-01 | `POST /api/v1/payments/{payment_id}/cancel` | session | Creating user only (guest token is read-only, I-2). Never from UNKNOWN. | — → 200 PaymentStatusView | `PAYMENT_NOT_CANCELLABLE`, `PAYMENT_NOT_FOUND`, `PAYMENT_OUTCOME_UNKNOWN`, `PROVIDER_UNAVAILABLE` | — | required | RL-DONATE | payment.state.changed |
| PAY-02 | `GET /api/v1/admin/payments` | staff + MFA | `payment.view`. | — → 200 Page<StaffPaymentIntent> | — | cursor; status, provider, currency, campaign_id, needs_reconciliation, from, to · -created_at, unknown_since | n/a | RL-STAFF-READ | — |
| PAY-03 | `GET /api/v1/admin/payments/{payment_id}` | staff + MFA | `payment.view`. | — → 200 StaffPaymentIntent | `PAYMENT_NOT_FOUND` | — | n/a | RL-STAFF-READ | — |
| PAY-04 | `GET /api/v1/admin/payments/{payment_id}/events` | staff + MFA | `payment.view`. | — → 200 Page<PaymentEvent> | `PAYMENT_NOT_FOUND` | cursor | n/a | RL-STAFF-READ | — |
| PAY-05 | `POST /api/v1/admin/payments/{payment_id}/status-checks` | staff + MFA | `payment.status.query`. Never changes state directly. | StaffActionRequest → 202 Accepted | `PAYMENT_NOT_FOUND` | — | optional | RL-STAFF-WRITE | payment.status_query.requested (new, J) |

### Payment status

Donor-facing status; UNKNOWN is surfaced distinctly ("don't pay again").

| ID | Method & path | Auth | Authorization (permission / ownership) | Request → response | Specific errors | List: filters · sort | Idem. | RL class | Audit event |
|---|---|---|---|---|---|---|---|---|---|
| PST-01 | `GET /api/v1/payments/{payment_id}` | session or donation token | Payment's donor (creating user session or the donation access token); else 404. | — → 200 PaymentStatusView | `PAYMENT_NOT_FOUND` | — | n/a | RL-STATUS-POLL | — |
| PST-02 ⁿ | `GET /api/v1/payments/{payment_id}/stream` | session or donation token | As PST-01. | — → 200 text/event-stream | `PAYMENT_NOT_FOUND` | — | n/a | RL-STATUS-POLL | — |

### Refund requests

Donor refund requests; staff refund maker-checker; disputes and recovery (staff).

| ID | Method & path | Auth | Authorization (permission / ownership) | Request → response | Specific errors | List: filters · sort | Idem. | RL class | Audit event |
|---|---|---|---|---|---|---|---|---|---|
| REF-01 | `POST /api/v1/donations/{donation_id}/refund-requests` | session or donation token | Donation's signed-in donor, or a guest holding the donation access token (I-2). | DonorRefundRequestCreate → 201 RefundRequest | `ACCOUNT_RESTRICTED`, `AMOUNT_EXCEEDS_REFUNDABLE`, `CURRENCY_MISMATCH`, `DONATION_NOT_FOUND`, `INVALID_MONEY`, `PAYMENT_NOT_REFUNDABLE`, `REFUND_REQUEST_EXISTS` | — | required | RL-SENSITIVE-USER | payment.refund.requested |
| REF-02 | `GET /api/v1/me/refund-requests` | session | Own. | — → 200 Page<RefundRequest> | — | cursor; status | n/a | RL-USER-READ | — |
| REF-03 | `GET /api/v1/refund-requests/{refund_request_id}` | session or donation token | Own; or the access token of the donation the request belongs to (others → 404). | — → 200 RefundRequest | `REFUND_REQUEST_NOT_FOUND` | — | n/a | RL-USER-READ | — |
| REF-04 | `POST /api/v1/refund-requests/{refund_request_id}/withdraw` | session | Own (signed-in requester; guests ask SUPPORT — token is not a write credential beyond the refund request itself). | — → 200 RefundRequest | `REFUND_REQUEST_INVALID_TRANSITION`, `REFUND_REQUEST_NOT_FOUND` | — | required | RL-USER-WRITE | payment.refund.withdrawn (new) |
| REF-05 | `POST /api/v1/admin/refund-requests` | staff + MFA | `refund.request`. **MC:** maker. | StaffRefundRequestCreate → 201 StaffRefundRequest | `AMOUNT_EXCEEDS_REFUNDABLE`, `CURRENCY_MISMATCH`, `INVALID_MONEY`, `PAYMENT_NOT_FOUND`, `PAYMENT_NOT_REFUNDABLE` | — | required | RL-STAFF-WRITE | payment.refund.requested (J) |
| REF-06 | `GET /api/v1/admin/refund-requests` | staff + MFA | `refund.view`. | — → 200 Page<StaffRefundRequest> | — | cursor; status, campaign_id, currency, channel · -created_at, created_at | n/a | RL-STAFF-READ | — |
| REF-07 | `GET /api/v1/admin/refund-requests/{refund_request_id}` | staff + MFA | `refund.view`. | — → 200 StaffRefundRequest | `REFUND_REQUEST_NOT_FOUND` | — | n/a | RL-STAFF-READ | — |
| REF-08 | `POST /api/v1/admin/refund-requests/{refund_request_id}/submit-for-approval` | staff + MFA | `refund.request`. | StaffActionRequest → 200 StaffRefundRequest | `REFUND_REQUEST_INVALID_TRANSITION`, `REFUND_REQUEST_NOT_FOUND` | — | optional | RL-STAFF-WRITE | payment.refund.triaged (new, J) |
| REF-09 | `POST /api/v1/admin/refund-requests/{refund_request_id}/approve` | staff + MFA + step-up | `refund.approve`; approver ≠ requester; C2 funding plan needs DUAL. **MC:** checker ≠ requester. | RefundDecisionRequest → 200 StaffRefundRequest | `AMOUNT_EXCEEDS_REFUNDABLE`, `APPROVAL_EXPIRED`, `APPROVER_CONFLICT`, `PROVIDER_CAPABILITY_MISSING`, `REFUND_REQUEST_INVALID_TRANSITION`, `REFUND_REQUEST_NOT_FOUND`, `VERSION_CONFLICT` | — | required | RL-STAFF-WRITE | payment.refund.approved (J); ledger.transaction.posted |
| REF-10 | `POST /api/v1/admin/refund-requests/{refund_request_id}/reject` | staff + MFA + step-up | `refund.approve`; ≠ requester. **MC:** checker. | RefundDecisionRequest → 200 StaffRefundRequest | `APPROVER_CONFLICT`, `REFUND_REQUEST_INVALID_TRANSITION`, `REFUND_REQUEST_NOT_FOUND`, `VERSION_CONFLICT` | — | required | RL-STAFF-WRITE | refund.rejected (J) |
| REF-11 | `POST /api/v1/admin/refund-requests/{refund_request_id}/hold` | staff + MFA | `refund.approve`. | RefundDecisionRequest → 200 StaffRefundRequest | `REFUND_REQUEST_INVALID_TRANSITION`, `REFUND_REQUEST_NOT_FOUND`, `VERSION_CONFLICT` | — | optional | RL-STAFF-WRITE | refund.on_hold (J) |
| REF-12 | `POST /api/v1/admin/refund-requests/{refund_request_id}/resume` | staff + MFA | `refund.approve`. | RefundDecisionRequest → 200 StaffRefundRequest | `REFUND_REQUEST_INVALID_TRANSITION`, `REFUND_REQUEST_NOT_FOUND` | — | optional | RL-STAFF-WRITE | refund.resumed (new, J) |
| REF-22 | `POST /api/v1/admin/refund-requests/bulk` | staff + MFA + step-up | `refund.bulk.request` (COMPLIANCE). Creates one refund request per payment plus a batch with a manifest hash; each refund stays individually idempotent (I-24). **MC:** maker. | BulkRefundCreate → 201 BulkRefundBatch | `CAMPAIGN_INVALID_TRANSITION`, `CAMPAIGN_NOT_FOUND` | — | required | RL-STAFF-WRITE | payment.refund.bulk_requested (new, J) |
| REF-23 | `POST /api/v1/admin/refund-batches/{batch_id}/approve` | staff + MFA + step-up | `refund.approve`; ≠ maker; binds to the manifest hash. **MC:** checker. | BulkRefundDecisionRequest → 200 BulkRefundBatch | `APPROVAL_EXPIRED`, `APPROVER_CONFLICT`, `REFUND_BATCH_NOT_FOUND`, `VERSION_CONFLICT` | — | required | RL-STAFF-WRITE | payment.refund.bulk_approved (new, J) |
| REF-24 | `POST /api/v1/admin/refund-batches/{batch_id}/business-approve` | staff + MFA + step-up | `refund.bulk.approve` (BUSINESS_APPROVER) when any currency total exceeds the configured limit; ≠ maker and ≠ FINANCE checker. **MC:** additional checker. | BulkRefundDecisionRequest → 200 BulkRefundBatch | `APPROVAL_EXPIRED`, `APPROVER_CONFLICT`, `REFUND_BATCH_NOT_FOUND`, `VERSION_CONFLICT` | — | required | RL-STAFF-WRITE | payment.refund.bulk_business_approved (new, J) |
| REF-13 | `GET /api/v1/admin/disputes` | staff + MFA | `dispute.view`. | — → 200 Page<Dispute> | — | cursor; status, provider, campaign_id, evidence_due_before · evidence_due_at, -created_at | n/a | RL-STAFF-READ | — |
| REF-14 | `GET /api/v1/admin/disputes/{dispute_id}` | staff + MFA | `dispute.view`. | — → 200 Dispute | `DISPUTE_NOT_FOUND` | — | n/a | RL-STAFF-READ | — |
| REF-15 | `POST /api/v1/admin/disputes/{dispute_id}/evidence-packs` | staff + MFA + step-up | `dispute.evidence.submit`. | EvidencePackCreate → 201 EvidencePack | `DISPUTE_INVALID_TRANSITION`, `DISPUTE_NOT_FOUND`, `EVIDENCE_REQUIRED`, `PROVIDER_CAPABILITY_MISSING`, `PROVIDER_UNAVAILABLE` | — | required | RL-STAFF-WRITE | dispute.evidence.submitted (new, J); evidence.exported |
| REF-16 | `POST /api/v1/admin/disputes/{dispute_id}/accept` | staff + MFA + step-up | `dispute.accept`. **MC:** maker. | StaffActionRequest → 200 Dispute | `DISPUTE_INVALID_TRANSITION`, `DISPUTE_NOT_FOUND` | — | required | RL-STAFF-WRITE | dispute.accept.requested (new, J) |
| REF-17 | `POST /api/v1/admin/disputes/{dispute_id}/accept/approve` | staff + MFA + step-up | `dispute.accept`; ≠ maker. **MC:** checker. | StaffActionRequest → 200 Dispute | `APPROVAL_EXPIRED`, `APPROVER_CONFLICT`, `DISPUTE_NOT_FOUND`, `PROVIDER_UNAVAILABLE` | — | required | RL-STAFF-WRITE | dispute.accepted (new, J) |
| REF-18 | `GET /api/v1/admin/recovery-cases` | staff + MFA | `recovery.view`. | — → 200 Page<RecoveryCase> | — | cursor; status, subject_type, currency | n/a | RL-STAFF-READ | — |
| REF-19 | `GET /api/v1/admin/recovery-cases/{recovery_case_id}` | staff + MFA | `recovery.view`. | — → 200 RecoveryCase | `RECOVERY_CASE_NOT_FOUND` | — | n/a | RL-STAFF-READ | — |
| REF-20 | `POST /api/v1/admin/recovery-cases/{recovery_case_id}/write-off` | staff + MFA + step-up | `recovery.write_off`. **MC:** maker. | StaffActionRequest → 200 RecoveryCase | `RECOVERY_CASE_INVALID_TRANSITION`, `RECOVERY_CASE_NOT_FOUND` | — | required | RL-STAFF-WRITE | recovery.write_off.requested (new, J) |
| REF-21 | `POST /api/v1/admin/recovery-cases/{recovery_case_id}/write-off/approve` | staff + MFA + step-up | `recovery.write_off.approve`: second FINANCE (DUAL) up to the threshold; BUSINESS_APPROVER required above it (I-4); distinct from maker. **MC:** checker (DUAL; BUSINESS_APPROVER above threshold). | StaffActionRequest → 200 RecoveryCase | `APPROVAL_EXPIRED`, `APPROVER_CONFLICT`, `RECOVERY_CASE_NOT_FOUND` | — | required | RL-STAFF-WRITE | recovery.written_off (new, J); ledger.transaction.posted |

### Payout destinations

Masked payout destinations; owner add/deactivate (step-up); staff overrides (maker-checker).

| ID | Method & path | Auth | Authorization (permission / ownership) | Request → response | Specific errors | List: filters · sort | Idem. | RL class | Audit event |
|---|---|---|---|---|---|---|---|---|---|
| DST-01 | `GET /api/v1/payout-destinations` | session | Own; org destinations need `org.payout_destination.manage` or `org.finance.view`. | — → 200 Page<PayoutDestination> | — | cursor; organisation_id, status, currency | n/a | RL-USER-READ | — |
| DST-02 | `POST /api/v1/payout-destinations` | session + step-up | Owner (self) or `org.payout_destination.manage` (ORG_ADMIN). Holder must be owner / verified beneficiary / verified institution / organisation. | PayoutDestinationCreate → 201 PayoutDestination | `ACCOUNT_RESTRICTED`, `CURRENCY_NOT_SUPPORTED`, `PAYOUT_DESTINATION_NOT_ALLOWED`, `VERIFICATION_REQUIRED` | — | required | RL-SENSITIVE-USER | payout.destination.added; payout.hold.applied (system) |
| DST-03 | `GET /api/v1/payout-destinations/{destination_id}` | session | As DST-01; else 404. | — → 200 PayoutDestination | `PAYOUT_DESTINATION_NOT_FOUND` | — | n/a | RL-USER-READ | — |
| DST-04 | `POST /api/v1/payout-destinations/{destination_id}/deactivate` | session + step-up | As DST-02. In-flight payouts keep their snapshot. | — → 200 PayoutDestination | `PAYOUT_DESTINATION_NOT_FOUND` | — | optional | RL-SENSITIVE-USER | payout.destination.changed |
| DST-05 | `PUT /api/v1/campaigns/{campaign_id}/payout-destination` | session + step-up | Owner / `org.payout_destination.manage`. | CampaignPayoutDestinationSet → 200 OwnerCampaign | `CAMPAIGN_NOT_FOUND`, `CURRENCY_MISMATCH`, `PAYOUT_DESTINATION_INACTIVE`, `PAYOUT_DESTINATION_NOT_ALLOWED`, `PAYOUT_DESTINATION_NOT_FOUND`, `VERSION_CONFLICT` | — | required | RL-SENSITIVE-USER | payout.destination.changed; payout.hold.applied (system) |
| DST-06 | `GET /api/v1/admin/payout-destinations/{destination_id}` | staff + MFA | `payout.view`. | — → 200 PayoutDestination | `PAYOUT_DESTINATION_NOT_FOUND` | — | n/a | RL-STAFF-READ | — |
| DST-07 | `POST /api/v1/admin/payout-destination-override-requests` | staff + MFA + step-up | `payout.destination.override.request`; evidence of user request. **MC:** maker. | DestinationOverrideCreate → 201 DestinationOverride | `CAMPAIGN_NOT_FOUND`, `EVIDENCE_REQUIRED`, `PAYOUT_DESTINATION_NOT_ALLOWED` | — | required | RL-STAFF-WRITE | payout.destination.override.requested (new, J) |
| DST-08 | `POST /api/v1/admin/payout-destination-override-requests/{override_request_id}/approve` | staff + MFA + step-up | `payout.destination.override.approve`; ≠ maker. **MC:** checker. | StaffActionRequest → 200 DestinationOverride | `APPROVAL_EXPIRED`, `APPROVER_CONFLICT`, `DESTINATION_OVERRIDE_NOT_FOUND` | — | required | RL-STAFF-WRITE | payout.destination.changed (J); payout.hold.applied |
| DST-09 | `POST /api/v1/admin/payout-destination-override-requests/{override_request_id}/reject` | staff + MFA | As DST-08. **MC:** checker. | StaffActionRequest → 200 DestinationOverride | `APPROVER_CONFLICT`, `DESTINATION_OVERRIDE_NOT_FOUND` | — | optional | RL-STAFF-WRITE | payout.destination.override.rejected (new, J) |
| DST-10 ⁿ | `POST /api/v1/admin/payout-destinations/{destination_id}/account-reveal` | staff + MFA + step-up | `kyc.identity_number.reveal`; case-bound; daily cap. | IdentityRevealRequest → 200 IdentityReveal | `KYC_ACCESS_NOT_BOUND`, `KYC_REVEAL_LIMIT_REACHED` | — | n/a | RL-STAFF-SENSITIVE | kyc.identity_number.revealed (J; security chain) |

### Payout requests

Owner payout requests (request, cancel before SUBMITTED, view) and staff approval.

| ID | Method & path | Auth | Authorization (permission / ownership) | Request → response | Specific errors | List: filters · sort | Idem. | RL class | Audit event |
|---|---|---|---|---|---|---|---|---|---|
| POR-01 | `GET /api/v1/campaigns/{campaign_id}/payout-eligibility` | session | Owner / ORG_ADMIN (`org.payout.request`). | — → 200 PayoutEligibilityPreview | `CAMPAIGN_NOT_FOUND` | query: currency | n/a | RL-USER-READ | — |
| POR-02 | `POST /api/v1/campaigns/{campaign_id}/payout-requests` | session + step-up | Owner or ORG_ADMIN with `org.payout.request` (EC-02); all request-time checks (EC-01..EC-22). | PayoutRequestCreate → 201 PayoutRequest | `ACCOUNT_RESTRICTED`, `CAMPAIGN_NOT_FOUND`, `CURRENCY_MISMATCH`, `INVALID_MONEY`, `PAYOUT_DESTINATION_NOT_FOUND`, `PAYOUT_IN_FLIGHT_EXISTS`, `PAYOUT_NOT_ELIGIBLE` | — | required | RL-SENSITIVE-USER | payout.requested; payout.eligibility.evaluated; ledger.transaction.posted |
| POR-03 | `GET /api/v1/campaigns/{campaign_id}/payout-requests` | session | Owner / `org.finance.view`. | — → 200 Page<PayoutRequest> | `CAMPAIGN_NOT_FOUND` | cursor; status, currency · -requested_at | n/a | RL-USER-READ | — |
| POR-04 | `GET /api/v1/payout-requests/{payout_request_id}` | session | Owner / `org.finance.view`; else 404. | — → 200 PayoutRequest | `PAYOUT_NOT_FOUND` | — | n/a | RL-USER-READ | — |
| POR-05 | `POST /api/v1/payout-requests/{payout_request_id}/cancel` | session + step-up | Owner / ORG_ADMIN. After SUBMITTED → 409 PAYOUT_ALREADY_SUBMITTED. | — → 200 PayoutRequest | `PAYOUT_ALREADY_SUBMITTED`, `PAYOUT_INVALID_TRANSITION`, `PAYOUT_NOT_FOUND` | — | required | RL-SENSITIVE-USER | payout.state.changed; ledger.transaction.posted |
| POR-06 | `GET /api/v1/admin/payout-requests` | staff + MFA | `payout.view`. | — → 200 Page<StaffPayoutRequest> | — | cursor; status, approval_tier, currency, provider, campaign_id, needs_reconciliation, assigned_to · requested_at, -requested_at, approval_expires_at, unknown_since | n/a | RL-STAFF-READ | — |
| POR-07 | `GET /api/v1/admin/payout-requests/{payout_request_id}` | staff + MFA | `payout.view`. | — → 200 StaffPayoutRequest | `PAYOUT_NOT_FOUND` | — | n/a | RL-STAFF-READ | — |
| POR-08 | `POST /api/v1/admin/payout-requests/{payout_request_id}/approve` | staff + MFA + step-up | `payout.approve` (FINANCE only; COMPLIANCE never approves payouts, I-20); approver ≠ requester/initiator/destination-changer; DUAL = two distinct FINANCE approvers; no declared conflict. **MC:** checker. | PayoutDecisionRequest → 200 StaffPayoutRequest | `APPROVAL_ALREADY_RECORDED`, `APPROVAL_EXPIRED`, `APPROVER_CONFLICT`, `PAYOUT_INVALID_TRANSITION`, `PAYOUT_NOT_FOUND`, `VERSION_CONFLICT` | — | required | RL-STAFF-WRITE | payout.approved (J) / payout.approval.denied_self; payout.eligibility.rechecked |
| POR-09 | `POST /api/v1/admin/payout-requests/{payout_request_id}/reject` | staff + MFA + step-up | `payout.reject`; ≠ requester. | PayoutDecisionRequest → 200 StaffPayoutRequest | `APPROVER_CONFLICT`, `PAYOUT_INVALID_TRANSITION`, `PAYOUT_NOT_FOUND` | — | required | RL-STAFF-WRITE | payout.rejected (J); ledger.transaction.posted |
| POR-10 | `POST /api/v1/admin/payout-requests/{payout_request_id}/cancel` | staff + MFA + step-up | `payout.cancel`. | StaffActionRequest → 200 StaffPayoutRequest | `PAYOUT_ALREADY_SUBMITTED`, `PAYOUT_INVALID_TRANSITION`, `PAYOUT_NOT_FOUND` | — | required | RL-STAFF-WRITE | payout.state.changed (J) |
| POR-11 | `POST /api/v1/admin/payout-requests/{payout_request_id}/eligibility-checks` | staff + MFA | `payout.view`. | — → 200 StaffPayoutRequest | `PAYOUT_NOT_FOUND` | — | optional | RL-STAFF-WRITE | payout.eligibility.rechecked |
| POR-12 ⁿ | `POST /api/v1/admin/manual-payouts` | staff + MFA + step-up | FINANCE maker; DUAL checker. **MC:** maker; DUAL. | — → 200 StaffPayoutRequest | — | — | required | RL-STAFF-WRITE | payout.requested (J) |

### Payout status

Owner polling/timeline (read-only) and staff provider status queries.

| ID | Method & path | Auth | Authorization (permission / ownership) | Request → response | Specific errors | List: filters · sort | Idem. | RL class | Audit event |
|---|---|---|---|---|---|---|---|---|---|
| PSS-01 | `GET /api/v1/payout-requests/{payout_request_id}/status` | session | As POR-04. | — → 200 PayoutStatusView | `PAYOUT_NOT_FOUND` | — | n/a | RL-STATUS-POLL | — |
| PSS-02 | `GET /api/v1/payout-requests/{payout_request_id}/timeline` | session | As POR-04. | — → 200 Page<PayoutTimelineItem> | `PAYOUT_NOT_FOUND` | cursor | n/a | RL-USER-READ | — |
| PSS-03 | `GET /api/v1/admin/payout-requests/{payout_request_id}/events` | staff + MFA | `payout.view`. | — → 200 Page<PayoutEvent> | `PAYOUT_NOT_FOUND` | cursor | n/a | RL-STAFF-READ | — |
| PSS-04 | `POST /api/v1/admin/payout-requests/{payout_request_id}/status-checks` | staff + MFA | `payout.status.query`. | StaffActionRequest → 202 Accepted | `PAYOUT_INVALID_TRANSITION`, `PAYOUT_NOT_FOUND` | — | optional | RL-STAFF-WRITE | payout.status_query.requested (new, J) |

### KYC

Individual verification (user) and KYC review (staff). Never returns document contents or ID numbers except via audited view/reveal.

| ID | Method & path | Auth | Authorization (permission / ownership) | Request → response | Specific errors | List: filters · sort | Idem. | RL class | Audit event |
|---|---|---|---|---|---|---|---|---|---|
| KYC-01 | `GET /api/v1/me/verification` | session | Self. | — → 200 KYCStatus | — | — | n/a | RL-USER-READ | — |
| KYC-02 | `POST /api/v1/me/verification/consents` | session | Self. | KycConsentCreate → 201 ConsentSummary | — | — | optional | RL-USER-WRITE | kyc.consent.recorded (new) |
| KYC-03 | `POST /api/v1/me/verification/attempts` | session | Self; cool-down and attempt limits. | KycAttemptCreate → 201 KycAttempt | `KYC_CONSENT_REQUIRED`, `KYC_RETRY_NOT_AVAILABLE` | — | optional | RL-USER-WRITE | kyc.attempt.started |
| KYC-04 | `GET /api/v1/me/verification/attempts/{attempt_id}` | session | Own. | — → 200 KycAttempt | `KYC_ATTEMPT_NOT_FOUND` | — | n/a | RL-USER-READ | — |
| KYC-05 | `PUT /api/v1/me/verification/attempts/{attempt_id}/identity-data` | session | Own; attempt OPEN. | IdentityDataSubmission → 204 (no body) | `KYC_ATTEMPT_INVALID_STATE`, `KYC_ATTEMPT_NOT_FOUND` | — | optional | RL-SENSITIVE-USER | kyc.submission.created |
| KYC-06 | `POST /api/v1/me/verification/attempts/{attempt_id}/document-uploads` | session | Own; attempt OPEN. | UploadSessionRequest → 201 UploadSession | `KYC_ATTEMPT_INVALID_STATE`, `KYC_ATTEMPT_NOT_FOUND` | — | n/a | RL-UPLOAD | — |
| KYC-07 | `POST /api/v1/me/verification/attempts/{attempt_id}/documents` | session | Own. | KycDocumentFinalize → 201 KycDocumentMeta | `KYC_ATTEMPT_INVALID_STATE`, `KYC_ATTEMPT_NOT_FOUND`, `KYC_DOCUMENT_REJECTED`, `UPLOAD_EXPIRED`, `UPLOAD_NOT_READY` | — | optional | RL-UPLOAD | kyc.document.uploaded |
| KYC-08 | `POST /api/v1/me/verification/attempts/{attempt_id}/submit` | session | Own. | — → 200 KycAttempt | `KYC_ATTEMPT_INVALID_STATE`, `KYC_ATTEMPT_NOT_FOUND` | — | optional | RL-USER-WRITE | kyc.attempt.submitted (new) |
| KYC-09 ⁿ | `POST /api/v1/me/verification/attempts/{attempt_id}/liveness-sessions` | session | Own. | — → 200 — | — | — | n/a | RL-USER-WRITE | — |
| KYC-10 | `GET /api/v1/admin/kyc/cases` | staff + MFA | `kyc.case.review`. | — → 200 Page<StaffKycCase> | — | cursor; status, target_level, assigned_to, risk_tier · created_at, -created_at, sla_due_at | n/a | RL-STAFF-READ | — |
| KYC-11 | `GET /api/v1/admin/kyc/cases/{case_id}` | staff + MFA | `kyc.case.review`; assigned or queue member. | — → 200 StaffKycCase | `KYC_CASE_NOT_FOUND` | — | n/a | RL-STAFF-READ | kyc.status.viewed (sampled) |
| KYC-12 | `POST /api/v1/admin/kyc/cases/{case_id}/claim` | staff + MFA | `kyc.case.review`; not own/linked account. | — → 200 StaffKycCase | `KYC_CASE_NOT_FOUND`, `STAFF_CONFLICT_OF_INTEREST` | — | optional | RL-STAFF-WRITE | kyc.case.claimed (new) |
| KYC-13 | `POST /api/v1/admin/kyc/cases/{case_id}/decisions` | staff + MFA + step-up | `kyc.decision.record`; fraud REJECT / ESCALATE resolution with funds → second approver. **MC:** maker when second approval required. | KycDecisionRequest → 201 KycDecision | `KYC_CASE_NOT_FOUND`, `KYC_DECISION_INVALID`, `STAFF_CONFLICT_OF_INTEREST` | — | optional | RL-STAFF-WRITE | kyc.decision.recorded (J); kyc.level.changed |
| KYC-14 | `POST /api/v1/admin/kyc/decisions/{decision_id}/approve` | staff + MFA + step-up | `kyc.decision.record` (COMPLIANCE for fraud/escalations); ≠ decider. **MC:** checker. | StaffActionRequest → 200 KycDecision | `APPROVAL_EXPIRED`, `APPROVER_CONFLICT`, `KYC_DECISION_INVALID` | — | optional | RL-STAFF-WRITE | kyc.decision.recorded (J) |
| KYC-15 | `POST /api/v1/admin/kyc/cases/{case_id}/documents/{document_id}/view-sessions` | staff + MFA + step-up | `kyc.document.view`; case open and assigned to caller; justification; daily/volume caps. | DocumentViewSessionRequest → 201 DocumentViewSession | `KYC_ACCESS_NOT_BOUND`, `KYC_CASE_NOT_FOUND`, `KYC_DOCUMENT_NOT_FOUND`, `KYC_REVEAL_LIMIT_REACHED` | — | n/a | RL-STAFF-SENSITIVE | kyc.document.viewed (J; security chain) |
| KYC-16 | `POST /api/v1/admin/kyc/cases/{case_id}/identity-reveals` | staff + MFA + step-up | `kyc.identity_number.reveal`. | IdentityRevealRequest → 201 IdentityReveal | `KYC_ACCESS_NOT_BOUND`, `KYC_CASE_NOT_FOUND`, `KYC_REVEAL_LIMIT_REACHED` | — | n/a | RL-STAFF-SENSITIVE | kyc.identity_number.revealed (J; security chain) |
| KYC-17 | `GET /api/v1/admin/users/{user_id}/verification` | staff + MFA | `kyc.status.view` (SUPPORT: level only). | — → 200 KYCStatus | `USER_NOT_FOUND` | — | n/a | RL-STAFF-READ | kyc.status.viewed (sampled) |

### KYB

Organisation verification and fundraising authority; KYB review (staff).

| ID | Method & path | Auth | Authorization (permission / ownership) | Request → response | Specific errors | List: filters · sort | Idem. | RL class | Audit event |
|---|---|---|---|---|---|---|---|---|---|
| KYB-01 | `GET /api/v1/organisations/{org_id}/verification` | session | `org.view`. | — → 200 KYBStatus | `ORGANISATION_NOT_FOUND` | — | n/a | RL-USER-READ | — |
| KYB-02 | `POST /api/v1/organisations/{org_id}/verification/attempts` | session | `org.verification.submit` (ORG_ADMIN, IDENTITY_VERIFIED). | KybAttemptCreate → 201 KybAttempt | `KYC_RETRY_NOT_AVAILABLE`, `ORGANISATION_NOT_FOUND`, `VERIFICATION_REQUIRED` | — | optional | RL-USER-WRITE | kyb.attempt.started (new) |
| KYB-03 | `POST /api/v1/organisations/{org_id}/verification/attempts/{attempt_id}/document-uploads` | session | As KYB-02. | UploadSessionRequest → 201 UploadSession | `KYB_ATTEMPT_NOT_FOUND`, `ORGANISATION_NOT_FOUND` | — | n/a | RL-UPLOAD | — |
| KYB-04 | `POST /api/v1/organisations/{org_id}/verification/attempts/{attempt_id}/documents` | session | As KYB-02. | KycDocumentFinalize → 201 KycDocumentMeta | `KYB_ATTEMPT_NOT_FOUND`, `KYC_DOCUMENT_REJECTED`, `ORGANISATION_NOT_FOUND`, `UPLOAD_NOT_READY` | — | optional | RL-UPLOAD | kyc.document.uploaded |
| KYB-05 | `POST /api/v1/organisations/{org_id}/verification/attempts/{attempt_id}/persons` | session | As KYB-02. | OrganisationPersonSubmission → 201 OrganisationPersonRef | `KYB_ATTEMPT_NOT_FOUND`, `KYC_ATTEMPT_INVALID_STATE`, `ORGANISATION_NOT_FOUND` | — | optional | RL-SENSITIVE-USER | kyb.person.declared (new) |
| KYB-06 | `POST /api/v1/organisations/{org_id}/verification/attempts/{attempt_id}/submit` | session | As KYB-02. | — → 200 KybAttempt | `KYB_ATTEMPT_NOT_FOUND`, `KYC_ATTEMPT_INVALID_STATE`, `ORGANISATION_NOT_FOUND` | — | optional | RL-USER-WRITE | kyb.attempt.submitted (new) |
| KYB-07 | `POST /api/v1/organisations/{org_id}/fundraising-authorities` | session | `org.verification.submit`. | FundraisingAuthorityCreate → 201 FundraisingAuthority | `FUNDRAISING_AUTHORITY_INVALID`, `ORGANISATION_NOT_FOUND`, `UPLOAD_NOT_READY` | — | optional | RL-USER-WRITE | kyb.fundraising_authority.submitted (new) |
| KYB-08 | `GET /api/v1/organisations/{org_id}/fundraising-authorities` | session | `org.view`. | — → 200 Page<FundraisingAuthority> | `ORGANISATION_NOT_FOUND` | cursor | n/a | RL-USER-READ | — |
| KYB-09 | `GET /api/v1/admin/kyb/cases` | staff + MFA | `kyc.case.review`. | — → 200 Page<StaffKycCase> | — | cursor; status, target_level, assigned_to | n/a | RL-STAFF-READ | — |
| KYB-10 | `GET /api/v1/admin/kyb/cases/{case_id}` | staff + MFA | `kyc.case.review`. | — → 200 StaffKycCase | `KYC_CASE_NOT_FOUND` | — | n/a | RL-STAFF-READ | — |
| KYB-11 | `POST /api/v1/admin/kyb/cases/{case_id}/decisions` | staff + MFA + step-up | `org.verification.decide`; HIGH risk: KYC_REVIEWER non-final, COMPLIANCE finalises. **MC:** second approver for HIGH. | KybDecisionRequest → 201 KycDecision | `KYC_CASE_NOT_FOUND`, `KYC_DECISION_INVALID`, `STAFF_CONFLICT_OF_INTEREST` | — | optional | RL-STAFF-WRITE | organisation.verification.decided (J) |
| KYB-12 | `POST /api/v1/admin/kyb/cases/{case_id}/documents/{document_id}/view-sessions` | staff + MFA + step-up | `kyc.document.view` (as KYC-15). | DocumentViewSessionRequest → 201 DocumentViewSession | `KYC_ACCESS_NOT_BOUND`, `KYC_CASE_NOT_FOUND`, `KYC_DOCUMENT_NOT_FOUND`, `KYC_REVEAL_LIMIT_REACHED` | — | n/a | RL-STAFF-SENSITIVE | kyc.document.viewed (J; security chain) |

### Compliance (staff)

Cases, notes, decisions (maker-checker), screening, restrictions, overrides. STR-restricted cases invisible without `str.prepare`.

| ID | Method & path | Auth | Authorization (permission / ownership) | Request → response | Specific errors | List: filters · sort | Idem. | RL class | Audit event |
|---|---|---|---|---|---|---|---|---|---|
| CMPL-01 | `GET /api/v1/admin/compliance/cases` | staff + MFA | `case.manage`. | — → 200 Page<ComplianceCase> | — | cursor; state, case_type, severity, assigned_to, subject_type, subject_id, blocks_payouts · sla_due_at, -opened_at | n/a | RL-STAFF-READ | — |
| CMPL-02 | `POST /api/v1/admin/compliance/cases` | staff + MFA | `case.create`. | ComplianceCaseCreate → 201 ComplianceCase | — | — | optional | RL-STAFF-WRITE | case.opened (risk.case.opened) |
| CMPL-03 | `GET /api/v1/admin/compliance/cases/{case_id}` | staff + MFA | `case.manage`. | — → 200 ComplianceCase | `COMPLIANCE_CASE_NOT_FOUND` | — | n/a | RL-STAFF-READ | — |
| CMPL-04 | `POST /api/v1/admin/compliance/cases/{case_id}/assignment` | staff + MFA | `case.manage`. | CaseAssignRequest → 200 ComplianceCase | `COMPLIANCE_CASE_NOT_FOUND`, `STAFF_CONFLICT_OF_INTEREST` | — | optional | RL-STAFF-WRITE | case.assigned |
| CMPL-05 | `POST /api/v1/admin/compliance/cases/{case_id}/notes` | staff + MFA | `case.manage`. | CaseNoteCreate → 201 CaseNote | `COMPLIANCE_CASE_NOT_FOUND` | — | optional | RL-STAFF-WRITE | case.note.added |
| CMPL-06 | `GET /api/v1/admin/compliance/cases/{case_id}/notes` | staff + MFA | `case.manage`. | — → 200 Page<CaseNote> | `COMPLIANCE_CASE_NOT_FOUND` | cursor | n/a | RL-STAFF-READ | — |
| CMPL-07 | `POST /api/v1/admin/compliance/cases/{case_id}/links` | staff + MFA | `case.manage`. | CaseLinkCreate → 201 ComplianceCase | `COMPLIANCE_CASE_NOT_FOUND` | — | optional | RL-STAFF-WRITE | case.linked |
| CMPL-08 | `POST /api/v1/admin/compliance/cases/{case_id}/transitions` | staff + MFA | `case.manage`. | CaseTransitionRequest → 200 ComplianceCase | `CASE_CLOSURE_INCOMPLETE`, `CASE_INVALID_TRANSITION`, `COMPLIANCE_CASE_NOT_FOUND` | — | optional | RL-STAFF-WRITE | case.state.changed (J) |
| CMPL-09 | `POST /api/v1/admin/compliance/cases/{case_id}/decision-proposals` | staff + MFA + step-up | `case.manage`. **MC:** maker. | DecisionProposalCreate → 201 DecisionProposal | `CASE_INVALID_TRANSITION`, `COMPLIANCE_CASE_NOT_FOUND` | — | optional | RL-STAFF-WRITE | case.decision.proposed (J) |
| CMPL-10 | `POST /api/v1/admin/compliance/decision-proposals/{proposal_id}/approve` | staff + MFA + step-up | `case.manage`; ≠ maker; no conflict. **MC:** checker. | StaffActionRequest → 200 DecisionProposal | `APPROVAL_EXPIRED`, `APPROVER_CONFLICT`, `DECISION_PROPOSAL_NOT_FOUND` | — | optional | RL-STAFF-WRITE | case.decision.approved (J) |
| CMPL-11 | `POST /api/v1/admin/compliance/decision-proposals/{proposal_id}/reject` | staff + MFA | As CMPL-10. **MC:** checker. | StaffActionRequest → 200 DecisionProposal | `APPROVER_CONFLICT`, `DECISION_PROPOSAL_NOT_FOUND` | — | optional | RL-STAFF-WRITE | case.decision.rejected (J) |
| CMPL-12 | `GET /api/v1/admin/compliance/screening-hits` | staff + MFA | `screening.review`. | — → 200 Page<ScreeningHit> | — | cursor; status, subject_type, match_score_band | n/a | RL-STAFF-READ | — |
| CMPL-13 | `POST /api/v1/admin/compliance/screening-hits/{hit_id}/disposition` | staff + MFA + step-up | `screening.review`. **MC:** checker at S1. | ScreeningDispositionRequest → 200 ScreeningHit | `APPROVER_CONFLICT`, `SCREENING_HIT_NOT_FOUND` | — | optional | RL-STAFF-WRITE | sanctions.screening.disposition (new, J) |
| CMPL-14 | `POST /api/v1/admin/compliance/restrictions` | staff + MFA + step-up | `compliance.restriction.request`. **MC:** maker. | ComplianceRestrictionCreate → 201 ComplianceRestriction | `OVERRIDE_EXPIRY_REQUIRED` | — | optional | RL-STAFF-WRITE | compliance.restriction.requested (new, J) |
| CMPL-15 | `POST /api/v1/admin/compliance/restrictions/{restriction_id}/approve` | staff + MFA + step-up | `compliance.restriction.approve`; ≠ maker. **MC:** checker. | StaffActionRequest → 200 ComplianceRestriction | `APPROVAL_EXPIRED`, `APPROVER_CONFLICT`, `RESTRICTION_NOT_FOUND` | — | optional | RL-STAFF-WRITE | compliance.restriction.applied (new, J); payout.hold.applied |
| CMPL-16 | `POST /api/v1/admin/compliance/restrictions/{restriction_id}/lift` | staff + MFA + step-up | `compliance.restriction.request` → approve via CMPL-15 pattern. **MC:** maker. | StaffActionRequest → 200 ComplianceRestriction | `RESTRICTION_NOT_FOUND` | — | optional | RL-STAFF-WRITE | compliance.restriction.lift_requested (new, J) |
| CMPL-17 | `POST /api/v1/admin/compliance/overrides` | staff + MFA + step-up | `compliance.override.request`; single subject; expiry; never sanctions block/legal hold. **MC:** maker. | ComplianceOverrideCreate → 201 ComplianceOverride | `OVERRIDE_EXPIRY_REQUIRED`, `SANCTIONS_BLOCK_NOT_OVERRIDABLE` | — | optional | RL-STAFF-WRITE | compliance.override.requested (J) |
| CMPL-18 | `POST /api/v1/admin/compliance/overrides/{override_id}/approve` | staff + MFA + step-up | `compliance.override.approve`; ≠ maker. **MC:** checker. | StaffActionRequest → 200 ComplianceOverride | `APPROVAL_EXPIRED`, `APPROVER_CONFLICT`, `OVERRIDE_NOT_FOUND`, `SANCTIONS_BLOCK_NOT_OVERRIDABLE` | — | optional | RL-STAFF-WRITE | compliance.override.approved (J) |
| CMPL-19 ⁿ | `POST /api/v1/admin/compliance/cases/{case_id}/confidentiality` | staff + MFA + step-up | `str.prepare`. | — → 200 ComplianceCase | `COMPLIANCE_CASE_NOT_FOUND` | — | n/a | RL-STAFF-WRITE | case.confidentiality.restricted (new, J) |
| CMPL-20 ⁿ | `POST /api/v1/admin/compliance/str-reports` | staff + MFA + step-up | `str.prepare`. **MC:** maker. | — → 200 — | — | — | n/a | RL-STAFF-WRITE | compliance.report.filed (J) on filing |
| CMPL-21 ⁿ | `POST /api/v1/admin/compliance/str-reports/{str_id}/approve` | staff + MFA + step-up | `str.approve`; ≠ maker. **MC:** checker. | — → 200 — | `APPROVER_CONFLICT` | — | n/a | RL-STAFF-WRITE | compliance.report.filed (J) |
| CMPL-22 ⁿ | `POST /api/v1/admin/evidence-holds` | staff + MFA + step-up | `evidence.hold`. **MC:** two-person. | — → 200 — | — | — | n/a | RL-STAFF-WRITE | evidence.hold.requested (new, J) |

### Risk (staff)

Holds, limits registry, monitoring alerts, risk assessments.

| ID | Method & path | Auth | Authorization (permission / ownership) | Request → response | Specific errors | List: filters · sort | Idem. | RL class | Audit event |
|---|---|---|---|---|---|---|---|---|---|
| RSK-01 | `GET /api/v1/admin/risk/holds` | staff + MFA | `hold.view`. STR-linked hold reasons shown as COMPLIANCE_REVIEW without str.prepare. | — → 200 Page<Hold> | — | cursor; hold_type, scope_type, scope_id, active, case_id · -placed_at, review_by | n/a | RL-STAFF-READ | — |
| RSK-02 | `GET /api/v1/admin/risk/holds/{hold_id}` | staff + MFA | `hold.view`. | — → 200 Hold | `HOLD_NOT_FOUND` | — | n/a | RL-STAFF-READ | — |
| RSK-03 | `POST /api/v1/admin/risk/holds` | staff + MFA + step-up | `payout.hold` scoped by hold type (authorization-matrix §5). | HoldCreate → 201 Hold | `HOLD_TYPE_NOT_PERMITTED` | — | required | RL-STAFF-WRITE | payout.hold.applied (J); ledger.transaction.posted |
| RSK-04 | `POST /api/v1/admin/risk/holds/{hold_id}/release` | staff + MFA + step-up | `hold.release` scoped by type; maker when maker-checker applies. **MC:** maker (typed). | HoldReleaseRequestCreate → 201 HoldReleaseRequest | `HOLD_ALREADY_RELEASED`, `HOLD_NOT_FOUND`, `HOLD_TYPE_NOT_PERMITTED` | — | required | RL-STAFF-WRITE | payout.hold.released (J) or payout.hold.release_requested (new, J) |
| RSK-05 | `POST /api/v1/admin/risk/hold-release-requests/{release_request_id}/approve` | staff + MFA + step-up | `hold.release` for the type; ≠ maker; ≠ placer for CAMPAIGN_FREEZE. **MC:** checker. | StaffActionRequest → 200 HoldReleaseRequest | `APPROVAL_EXPIRED`, `APPROVER_CONFLICT`, `HOLD_NOT_FOUND` | — | required | RL-STAFF-WRITE | payout.hold.released (J); ledger.transaction.posted |
| RSK-06 | `GET /api/v1/admin/risk/limits` | staff + MFA | `limit.view`. | — → 200 Page<Limit> | — | cursor; limit_key, limit_type, scope, currency, review_overdue | n/a | RL-STAFF-READ | — |
| RSK-07 | `POST /api/v1/admin/risk/limit-change-requests` | staff + MFA + step-up | `limit.change.request`. **MC:** maker. | LimitChangeRequestCreate → 201 LimitChangeRequest | `INVALID_MONEY`, `LIMIT_SOURCE_REQUIRED` | — | optional | RL-STAFF-WRITE | policy.change.requested (J) |
| RSK-08 | `POST /api/v1/admin/risk/limit-change-requests/{limit_change_id}/approve` | staff + MFA + step-up | `limit.change.approve`; caller's role must match the limit's approval owner (COMPLIANCE / FINANCE; BUSINESS_APPROVER for business-owned limits); ≠ maker. **MC:** checker. | StaffActionRequest → 200 LimitChangeRequest | `APPROVAL_EXPIRED`, `APPROVER_CONFLICT`, `LIMIT_CHANGE_NOT_FOUND` | — | optional | RL-STAFF-WRITE | policy.change.approved (J) |
| RSK-09 | `POST /api/v1/admin/risk/limit-change-requests/{limit_change_id}/reject` | staff + MFA | As RSK-08. **MC:** checker. | StaffActionRequest → 200 LimitChangeRequest | `APPROVER_CONFLICT`, `LIMIT_CHANGE_NOT_FOUND` | — | optional | RL-STAFF-WRITE | policy.change.rejected (new, J) |
| RSK-10 | `GET /api/v1/admin/risk/alerts` | staff + MFA | `risk.alert.view`. | — → 200 Page<MonitoringAlert> | — | cursor; status, severity, rule_code, subject_type · -created_at | n/a | RL-STAFF-READ | — |
| RSK-11 | `POST /api/v1/admin/risk/alerts/{alert_id}/disposition` | staff + MFA | `risk.alert.manage` (REVIEWER: low severity only). | AlertDisposition → 200 MonitoringAlert | `ALERT_NOT_FOUND` | — | optional | RL-STAFF-WRITE | risk.alert.dispositioned (new, J) |
| RSK-12 | `GET /api/v1/admin/risk/assessments` | staff + MFA | `risk.assessment.view`. | — → 200 Page<RiskAssessment> | — | cursor; subject_type, subject_id | n/a | RL-STAFF-READ | — |

### Admin operations

Users, staff and roles, fees, config, kill switches, providers, webhook dead letters.

| ID | Method & path | Auth | Authorization (permission / ownership) | Request → response | Specific errors | List: filters · sort | Idem. | RL class | Audit event |
|---|---|---|---|---|---|---|---|---|---|
| ADM-01 | `GET /api/v1/admin/users` | staff + MFA | `user.view`. Exact-match search on phone/email via blind index; no partial PII search. | — → 200 Page<StaffUserView> | — | cursor; phone, email, user_id, account_kind | n/a | RL-STAFF-READ | user.searched (new, sampled) |
| ADM-02 | `GET /api/v1/admin/users/{user_id}` | staff + MFA | `user.view`. | — → 200 StaffUserView | `USER_NOT_FOUND` | — | n/a | RL-STAFF-READ | — |
| ADM-03 | `POST /api/v1/admin/users/{user_id}/suspend` | staff + MFA + step-up | `user.suspend`. | StaffActionRequest → 200 StaffUserView | `USER_NOT_FOUND` | — | optional | RL-STAFF-WRITE | user.suspended (J) |
| ADM-04 | `POST /api/v1/admin/users/{user_id}/sessions/revoke` | staff + MFA | `session.revoke`. | StaffActionRequest → 204 (no body) | `USER_NOT_FOUND` | — | n/a | RL-STAFF-WRITE | auth.session.revoked (J; security chain) |
| ADM-05 | `POST /api/v1/admin/users/{user_id}/account-recovery` | staff + MFA + step-up | `account.recovery.assist`; places ACCOUNT/PAYOUT hold + cooling-off. | AccountRecoveryAssistRequest → 202 Accepted | `EVIDENCE_REQUIRED`, `USER_NOT_FOUND` | — | optional | RL-STAFF-WRITE | auth.recovery.started (J; security chain) |
| ADM-06 | `GET /api/v1/admin/organisations/{org_id}` | staff + MFA | `organisation.view`. | — → 200 StaffOrganisationView | `ORGANISATION_NOT_FOUND` | — | n/a | RL-STAFF-READ | — |
| ADM-07 | `GET /api/v1/admin/staff` | staff + MFA | `role.view`. | — → 200 Page<StaffMember> | — | cursor; role, status | n/a | RL-STAFF-READ | — |
| ADM-08 | `POST /api/v1/admin/role-assignment-requests` | staff + MFA + step-up | `role.assign.request`; no self-grant; R-conflicts rejected. **MC:** maker. | RoleAssignmentRequestCreate → 201 RoleAssignmentRequest | `ROLE_COMBINATION_FORBIDDEN`, `SELF_GRANT_FORBIDDEN`, `USER_NOT_FOUND` | — | optional | RL-STAFF-WRITE | role.grant.requested (J; security chain) |
| ADM-09 | `POST /api/v1/admin/role-assignment-requests/{role_request_id}/approve` | staff + MFA + step-up | `role.assign.approve`; ≠ maker; ≠ grantee. **MC:** checker. | StaffActionRequest → 200 RoleAssignmentRequest | `APPROVAL_EXPIRED`, `APPROVER_CONFLICT`, `ROLE_COMBINATION_FORBIDDEN`, `ROLE_REQUEST_NOT_FOUND`, `SELF_GRANT_FORBIDDEN` | — | optional | RL-STAFF-WRITE | role.grant.approved (J; security chain; alert) |
| ADM-10 | `POST /api/v1/admin/role-assignment-requests/{role_request_id}/reject` | staff + MFA | As ADM-09. **MC:** checker. | StaffActionRequest → 200 RoleAssignmentRequest | `APPROVER_CONFLICT`, `ROLE_REQUEST_NOT_FOUND` | — | optional | RL-STAFF-WRITE | role.grant.rejected (new, J; security chain) |
| ADM-11 | `POST /api/v1/admin/role-assignments/{role_assignment_id}/revoke` | staff + MFA + step-up | `role.revoke`. | StaffActionRequest → 204 (no body) | `ROLE_ASSIGNMENT_NOT_FOUND` | — | n/a | RL-STAFF-WRITE | role.revoked (J; security chain; alert) |
| ADM-12 | `POST /api/v1/admin/staff/{user_id}/suspend` | staff + MFA + step-up | `staff.suspend`. | StaffActionRequest → 200 StaffMember | `USER_NOT_FOUND` | — | optional | RL-STAFF-WRITE | staff.suspended (new, J; security chain) |
| ADM-13 | `GET /api/v1/admin/fee-schedules` | staff + MFA | `fee.config.request` or `ledger.view`. | — → 200 Page<FeeSchedule> | — | cursor; currency, effective_at | n/a | RL-STAFF-READ | — |
| ADM-14 | `POST /api/v1/admin/fee-schedule-change-requests` | staff + MFA + step-up | `fee.config.request`. **MC:** maker. | FeeScheduleChangeCreate → 201 FeeScheduleChange | `FEE_CHANGE_NOT_FUTURE_DATED` | — | optional | RL-STAFF-WRITE | fee.config.change.requested (J) |
| ADM-15 | `POST /api/v1/admin/fee-schedule-change-requests/{fee_change_id}/approve` | staff + MFA + step-up | `fee.config.approve` (BUSINESS_APPROVER, I-4); ≠ maker. **MC:** checker. | StaffActionRequest → 200 FeeScheduleChange | `APPROVAL_EXPIRED`, `APPROVER_CONFLICT`, `FEE_CHANGE_NOT_FOUND` | — | optional | RL-STAFF-WRITE | fee.config.change.approved (J) |
| ADM-16 | `GET /api/v1/admin/killswitches` | staff + MFA | `killswitch.activate`. | — → 200 Page<KillSwitch> | — | cursor | n/a | RL-STAFF-READ | — |
| ADM-17 | `POST /api/v1/admin/killswitches/{killswitch_name}/activate` | staff + MFA + step-up | `killswitch.activate` (scope by role). | StaffActionRequest → 200 KillSwitch | `KILLSWITCH_NOT_FOUND` | — | optional | RL-STAFF-WRITE | killswitch.activated (J; alert) |
| ADM-18 | `POST /api/v1/admin/killswitches/{killswitch_name}/deactivation-requests` | staff + MFA + step-up | `killswitch.deactivate.request`. **MC:** maker. | StaffActionRequest → 201 FeatureFlagChange | `KILLSWITCH_NOT_FOUND` | — | optional | RL-STAFF-WRITE | killswitch.deactivation.requested (new, J) |
| ADM-19 | `POST /api/v1/admin/feature-flag-changes/{flag_change_id}/approve` | staff + MFA + step-up | `killswitch.deactivate.approve` for kill switches, `config.change.approve` for other flags marked `requires_approval`; ≠ maker (DB CHECK). **MC:** checker. | StaffActionRequest → 200 FeatureFlagChange | `APPROVAL_EXPIRED`, `APPROVER_CONFLICT`, `FLAG_CHANGE_NOT_FOUND` | — | optional | RL-STAFF-WRITE | killswitch.deactivated (J) / config.changed (J) |
| ADM-20 | `GET /api/v1/admin/providers` | staff + MFA | `provider.view`. | — → 200 Page<Provider> | — | cursor | n/a | RL-STAFF-READ | — |
| ADM-21 | `GET /api/v1/admin/webhook-dead-letters` | staff + MFA | `webhook.deadletter.manage`. | — → 200 Page<WebhookDeadLetter> | — | cursor; provider, status | n/a | RL-STAFF-READ | — |
| ADM-22 | `POST /api/v1/admin/webhook-dead-letters/{dead_letter_id}/requeue` | staff + MFA | `webhook.deadletter.manage`. | StaffActionRequest → 200 WebhookDeadLetter | `DEAD_LETTER_NOT_FOUND` | — | optional | RL-STAFF-WRITE | webhook.requeued (new, J) |
| ADM-23 ⁿ | `GET /api/v1/admin/feature-flags` | staff + MFA | `config.change.request`. | — → 200 — | — | — | n/a | RL-STAFF-READ | — |
| ADM-24 ⁿ | `POST /api/v1/admin/feature-flag-changes` | staff + MFA + step-up | `config.change.request`; approved via ADM-19 (`config.change.approve`) when the flag is marked `requires_approval`. **MC:** maker. | — → 200 — | — | — | n/a | RL-STAFF-WRITE | config.changed (J) on approval |
| ADM-25 ⁿ | `POST /api/v1/admin/breakglass-sessions` | staff + MFA + step-up | On-call SECURITY_ADMIN / FINANCE lead / COMPLIANCE lead with incident ref. | — → 200 — | — | — | n/a | RL-STAFF-SENSITIVE | breakglass.started (J; alert) |
| ADM-27 ⁿ | `POST /api/v1/admin/review-policy-change-requests` | staff + MFA + step-up | `campaign.review_policy.request` (COMPLIANCE). **MC:** maker. | — → 200 — | — | — | n/a | RL-STAFF-WRITE | policy.change.requested (J) |
| ADM-28 ⁿ | `POST /api/v1/admin/review-policy-change-requests/{policy_change_id}/approve` | staff + MFA + step-up | `campaign.review_policy.approve` (BUSINESS_APPROVER, I-4); ≠ maker. **MC:** checker. | — → 200 — | `APPROVER_CONFLICT` | — | n/a | RL-STAFF-WRITE | policy.change.approved (J) |
| ADM-26 ⁿ | `POST /api/v1/admin/access-reviews` | staff + MFA | `access.review.run`. | — → 200 — | — | — | n/a | RL-STAFF-WRITE | access.review.recorded (new) |

### Notifications

Preferences and one-click unsubscribe. Transactional/security notifications cannot be disabled.

| ID | Method & path | Auth | Authorization (permission / ownership) | Request → response | Specific errors | List: filters · sort | Idem. | RL class | Audit event |
|---|---|---|---|---|---|---|---|---|---|
| NTF-01 | `GET /api/v1/me/notification-preferences` | session | Self. | — → 200 NotificationPreferences | — | — | n/a | RL-USER-READ | — |
| NTF-02 | `PUT /api/v1/me/notification-preferences` | session | Self. | NotificationPreferencesUpdate → 200 NotificationPreferences | `VERSION_CONFLICT` | — | optional | RL-USER-WRITE | notification.preferences.changed (new) |
| NTF-03 | `POST /api/v1/notifications/unsubscribe` | none | Bearer of a signed unsubscribe token. CSRF-exempt (no cookies used). | UnsubscribeRequest → 204 (no body) | `UNSUBSCRIBE_TOKEN_INVALID` | — | n/a | RL-PUBLIC-WRITE | notification.suppression.added (new) |
| NTF-04 ⁿ | `GET /api/v1/admin/notification-templates` | staff + MFA | ADMIN (COMPLIANCE approves tipping-off templates). | — → 200 — | — | — | n/a | RL-STAFF-READ | — |
| NTF-05 ⁿ | `GET /api/v1/admin/users/{user_id}/notification-attempts` | staff + MFA | `user.view`. | — → 200 — | — | — | n/a | RL-STAFF-READ | — |

### Reports

Asynchronous staff reports (owner statements are CMP-15).

| ID | Method & path | Auth | Authorization (permission / ownership) | Request → response | Specific errors | List: filters · sort | Idem. | RL class | Audit event |
|---|---|---|---|---|---|---|---|---|---|
| RPT-01 | `POST /api/v1/admin/reports` | staff + MFA | `report.generate`; report types scoped by role (FINANCE financial, COMPLIANCE compliance). | ReportRequestCreate → 202 ReportJob | — | — | optional | RL-STAFF-WRITE | report.requested (new, J) |
| RPT-02 | `GET /api/v1/admin/reports` | staff + MFA | `report.generate`; own jobs. | — → 200 Page<ReportJob> | — | cursor; status, report_type | n/a | RL-STAFF-READ | — |
| RPT-03 | `GET /api/v1/admin/reports/{report_id}` | staff + MFA | `report.generate`; requester only. | — → 200 ReportJob | `REPORT_NOT_FOUND`, `REPORT_NOT_READY` | — | n/a | RL-STAFF-READ | audit.exported (J) when a download URL is issued |

### Reconciliation (staff)

Settlement imports, runs, discrepancies and resolutions; ledger inspection and adjustments (FINANCE).

| ID | Method & path | Auth | Authorization (permission / ownership) | Request → response | Specific errors | List: filters · sort | Idem. | RL class | Audit event |
|---|---|---|---|---|---|---|---|---|---|
| REC-01 | `GET /api/v1/admin/reconciliation/sources` | staff + MFA | `reconciliation.run`. | — → 200 Page<ReconciliationSource> | — | cursor | n/a | RL-STAFF-READ | — |
| REC-02 | `POST /api/v1/admin/reconciliation/imports` | staff + MFA | `reconciliation.run`. | ReconciliationImportCreate → 201 ReconciliationImport | `RECONCILIATION_IMPORT_DUPLICATE`, `UPLOAD_NOT_READY`, `UPLOAD_REJECTED` | — | required | RL-STAFF-WRITE | reconciliation.import.created (new) |
| REC-03 | `GET /api/v1/admin/reconciliation/imports/{import_id}` | staff + MFA | `reconciliation.run`. | — → 200 ReconciliationImport | `RECONCILIATION_IMPORT_NOT_FOUND` | — | n/a | RL-STAFF-READ | — |
| REC-04 | `POST /api/v1/admin/reconciliation/runs` | staff + MFA | `reconciliation.run`. | ReconciliationRunCreate → 202 ReconciliationRun | — | — | required | RL-STAFF-WRITE | reconciliation.run.started (new) |
| REC-05 | `GET /api/v1/admin/reconciliation/runs` | staff + MFA | `reconciliation.run`. | — → 200 Page<ReconciliationRun> | — | cursor; provider, currency, status · -started_at | n/a | RL-STAFF-READ | — |
| REC-06 | `GET /api/v1/admin/reconciliation/runs/{run_id}` | staff + MFA | `reconciliation.run`. | — → 200 ReconciliationRun | `RECONCILIATION_RUN_NOT_FOUND` | — | n/a | RL-STAFF-READ | — |
| REC-07 | `GET /api/v1/admin/reconciliation/discrepancies` | staff + MFA | `reconciliation.resolve`. | — → 200 Page<Discrepancy> | — | cursor; status, severity, kind, provider, currency, run_id · -detected_at, severity | n/a | RL-STAFF-READ | — |
| REC-08 | `GET /api/v1/admin/reconciliation/discrepancies/{discrepancy_id}` | staff + MFA | `reconciliation.resolve`. | — → 200 Discrepancy | `DISCREPANCY_NOT_FOUND` | — | n/a | RL-STAFF-READ | — |
| REC-09 | `POST /api/v1/admin/reconciliation/discrepancies/{discrepancy_id}/resolutions` | staff + MFA + step-up | `reconciliation.resolve`; evidence required. **MC:** maker when state/money changes. | ResolutionCreate → 201 Resolution | `DISCREPANCY_NOT_FOUND`, `EVIDENCE_REQUIRED` | — | required | RL-STAFF-WRITE | reconciliation.mismatch.resolved (J) / resolution.proposed (new, J) |
| REC-10 | `POST /api/v1/admin/reconciliation/resolutions/{resolution_id}/approve` | staff + MFA + step-up | `reconciliation.resolve`; ≠ maker. **MC:** checker. | StaffActionRequest → 200 Resolution | `APPROVAL_EXPIRED`, `APPROVER_CONFLICT`, `INVARIANT_CHECK_FAILED`, `RESOLUTION_NOT_FOUND` | — | required | RL-STAFF-WRITE | reconciliation.mismatch.resolved (J); payment/payout.state.changed (RECON) |
| REC-11 | `GET /api/v1/admin/settlement-batches` | staff + MFA | `reconciliation.run`. | — → 200 Page<SettlementBatch> | — | cursor; provider, currency, status, from, to | n/a | RL-STAFF-READ | — |
| REC-12 | `GET /api/v1/admin/settlement-batches/{batch_id}` | staff + MFA | `reconciliation.run`. | — → 200 SettlementBatch | `SETTLEMENT_BATCH_NOT_FOUND` | — | n/a | RL-STAFF-READ | — |
| REC-13 | `GET /api/v1/admin/ledger/accounts/{account_id}/balance` | staff + MFA | `ledger.view`. | — → 200 LedgerBalance | `LEDGER_ACCOUNT_NOT_FOUND` | — | n/a | RL-STAFF-READ | — |
| REC-14 | `GET /api/v1/admin/ledger/transactions/{transaction_id}` | staff + MFA | `ledger.view`. | — → 200 LedgerTransaction | `LEDGER_TRANSACTION_NOT_FOUND` | — | n/a | RL-STAFF-READ | — |
| REC-15 | `GET /api/v1/admin/ledger/invariant-runs` | staff + MFA | `ledger.view`. | — → 200 Page<InvariantRun> | — | cursor; result | n/a | RL-STAFF-READ | — |
| REC-16 | `POST /api/v1/admin/ledger/adjustments` | staff + MFA + step-up | `ledger.adjustment.create`. **MC:** maker. | LedgerAdjustmentCreate → 201 LedgerAdjustment | `CURRENCY_MISMATCH`, `INVALID_MONEY`, `LEDGER_ADJUSTMENT_UNBALANCED`, `LEDGER_PERIOD_CLOSED` | — | required | RL-STAFF-WRITE | ledger.adjustment.requested (J) |
| REC-17 | `POST /api/v1/admin/ledger/adjustments/{adjustment_id}/approve` | staff + MFA + step-up | `ledger.adjustment.approve`; ≠ maker. **MC:** checker. | StaffActionRequest → 200 LedgerAdjustment | `APPROVAL_EXPIRED`, `APPROVER_CONFLICT`, `INVARIANT_CHECK_FAILED`, `LEDGER_ADJUSTMENT_NOT_FOUND`, `LEDGER_PERIOD_CLOSED` | — | required | RL-STAFF-WRITE | ledger.adjustment.approved (J); ledger.transaction.posted |
| REC-18 | `GET /api/v1/admin/ledger/adjustments` | staff + MFA | `ledger.view`. | — → 200 Page<LedgerAdjustment> | — | cursor; status, currency | n/a | RL-STAFF-READ | — |

### Audit access (staff)

Scoped, audited read access to audit and security-audit chains; evidence metadata.

| ID | Method & path | Auth | Authorization (permission / ownership) | Request → response | Specific errors | List: filters · sort | Idem. | RL class | Audit event |
|---|---|---|---|---|---|---|---|---|---|
| AUD-01 | `GET /api/v1/admin/audit/events` | staff + MFA | `audit.read` (COMPLIANCE broad; FINANCE financial domains; SUPER_ADMIN role/admin domains). | — → 200 Page<AuditEvent> | — | cursor; action, actor_id, target_type, target_id, from, to, outcome · -occurred_at | n/a | RL-STAFF-READ | audit.queried (new; security chain) |
| AUD-02 | `GET /api/v1/admin/audit/events/{event_id}` | staff + MFA | `audit.read` (scoped). | — → 200 AuditEvent | `AUDIT_EVENT_NOT_FOUND` | — | n/a | RL-STAFF-READ | — |
| AUD-03 | `GET /api/v1/admin/audit/security-events` | staff + MFA | `security_audit.read` (SECURITY_ADMIN, SUPER_ADMIN; I-23). | — → 200 Page<AuditEvent> | — | cursor; action, actor_id, from, to | n/a | RL-STAFF-READ | audit.queried (new; security chain) |
| AUD-04 | `GET /api/v1/admin/audit/chain-verifications` | staff + MFA | `audit.read`. | — → 200 Page<ChainVerification> | — | cursor; chain, result | n/a | RL-STAFF-READ | — |
| AUD-05 | `GET /api/v1/admin/evidence-records/{evidence_record_id}` | staff + MFA | `evidence.view`. | — → 200 EvidenceRecord | `EVIDENCE_RECORD_NOT_FOUND` | — | n/a | RL-STAFF-READ | — |
| AUD-06 ⁿ | `POST /api/v1/admin/audit/exports` | staff + MFA + step-up | `audit.export`. **MC:** maker-checker. | — → 200 — | — | — | n/a | RL-STAFF-SENSITIVE | audit.exported (J) |

### Webhooks

Provider callbacks: signature-authenticated, CSRF/session-exempt, inbox + 2xx fast.

| ID | Method & path | Auth | Authorization (permission / ownership) | Request → response | Specific errors | List: filters · sort | Idem. | RL class | Audit event |
|---|---|---|---|---|---|---|---|---|---|
| WHK-01 | `POST /api/v1/webhooks/{provider}` | provider signature | Valid provider signature + replay window; dedupe on (provider, provider_event_id). | raw → 200 WebhookAck | `PAYLOAD_TOO_LARGE`, `UNSUPPORTED_MEDIA_TYPE`, `WEBHOOK_PAYLOAD_INVALID`, `WEBHOOK_PROVIDER_UNKNOWN`, `WEBHOOK_SIGNATURE_INVALID` | — | n/a | RL-WEBHOOK | webhook.received / webhook.signature.failed |
| WHK-03 | `POST /api/v1/webhooks/screening/{vendor}` | provider signature | Vendor signature + replay window; stored in `compliance.screening_callback_inbox`, dedupe on (vendor, vendor_event_id) (I-25). | raw → 200 WebhookAck | `PAYLOAD_TOO_LARGE`, `UNSUPPORTED_MEDIA_TYPE`, `WEBHOOK_PAYLOAD_INVALID`, `WEBHOOK_PROVIDER_UNKNOWN`, `WEBHOOK_SIGNATURE_INVALID` | — | n/a | RL-WEBHOOK | webhook.received / webhook.signature.failed |
| WHK-02 ⁿ | `POST /api/v1/webhooks/kyc-vendors/{vendor}` | provider signature | Vendor signature. | raw → 200 WebhookAck | `WEBHOOK_SIGNATURE_INVALID` | — | n/a | RL-WEBHOOK | kyc.vendor.result.received |

### Health

Outside `/api/v1`. Liveness and readiness for the platform; not routed publicly in production.

| ID | Method & path | Auth | Authorization (permission / ownership) | Request → response | Specific errors | List: filters · sort | Idem. | RL class | Audit event |
|---|---|---|---|---|---|---|---|---|---|
| HLT-01 | `GET /healthz` | none | Infrastructure. | — → 200 HealthStatus | — | — | n/a | RL-HEALTH | — |
| HLT-02 | `GET /readyz` | none | Infrastructure (internal network). | — → 200 HealthStatus | — | — | n/a | RL-HEALTH | — |


## 16. Not yet in OpenAPI

These endpoints are specified in the catalogue only. They are low-value admin/ops endpoints, depend on an
unselected vendor, or wait for a legal answer. Each must be added to the YAML (spec first) before it is built.

| ID | Endpoint | Why not yet |
|---|---|---|
| AUTH-16 | `POST /api/v1/auth/password/login` | SECURITY §4.1 optional password; not in MVP. |
| ORG-11 | `POST /api/v1/organisation-invitations/accept` | Body `{token}`; low priority for OpenAPI. |
| CMP-16 | `GET /api/v1/campaigns/{campaign_id}/share-stats` | Depends on LR-023 consent model; Stage 16. |
| CMP-17 | `GET /api/v1/campaigns/{campaign_id}/donations/export` | Export of personal data; decide in Stage 7 with PRIVACY.md. |
| DON-07 | `POST /api/v1/donations/{donation_id}/claim` | Convenience; Stage 10. |
| PST-02 | `GET /api/v1/payments/{payment_id}/stream` | PAYMENTS §7 'later'; polling first. |
| DST-10 | `POST /api/v1/admin/payout-destinations/{destination_id}/account-reveal` | Only if FINANCE investigations need it; prefer masked + provider reference. |
| POR-12 | `POST /api/v1/admin/manual-payouts` | Design with refund MANUAL_PAYOUT flow (Stage 11/12). |
| KYC-09 | `POST /api/v1/me/verification/attempts/{attempt_id}/liveness-sessions` | Vendor-specific; vendor not selected. |
| CMPL-19 | `POST /api/v1/admin/compliance/cases/{case_id}/confidentiality` | STR surface kept out of the general contract until LR-060/LR-008 are answered. |
| CMPL-20 | `POST /api/v1/admin/compliance/str-reports` | LR-060. |
| CMPL-21 | `POST /api/v1/admin/compliance/str-reports/{str_id}/approve` | LR-060. |
| CMPL-22 | `POST /api/v1/admin/evidence-holds` | Low-value admin/ops endpoint; specified in this catalogue only until its stage. |
| ADM-23 | `GET /api/v1/admin/feature-flags` | Low-value admin/ops endpoint; specified in this catalogue only until its stage. |
| ADM-24 | `POST /api/v1/admin/feature-flag-changes` | Low-value admin/ops endpoint; specified in this catalogue only until its stage. |
| ADM-25 | `POST /api/v1/admin/breakglass-sessions` | Operational detail Stage 14. |
| ADM-27 | `POST /api/v1/admin/review-policy-change-requests` | Stage 6 (campaign_review_policies). |
| ADM-28 | `POST /api/v1/admin/review-policy-change-requests/{policy_change_id}/approve` | Stage 6. |
| ADM-26 | `POST /api/v1/admin/access-reviews` | Low-value admin/ops endpoint; specified in this catalogue only until its stage. |
| NTF-04 | `GET /api/v1/admin/notification-templates` | Stage 15. |
| NTF-05 | `GET /api/v1/admin/users/{user_id}/notification-attempts` | Stage 15. |
| AUD-06 | `POST /api/v1/admin/audit/exports` | Low-value admin/ops endpoint; specified in this catalogue only until its stage. |
| WHK-02 | `POST /api/v1/webhooks/kyc-vendors/{vendor}` | Vendor not selected. |

## 17. Endpoint counts

| Area | Catalogue endpoints | In OpenAPI |
|---|---|---|
| Authentication | 16 | 15 |
| User profiles | 7 | 7 |
| Organisations | 11 | 10 |
| Beneficiaries | 13 | 13 |
| Campaign creation | 17 | 15 |
| Campaign updates | 5 | 5 |
| Campaign discovery | 7 | 7 |
| Campaign moderation (staff) | 18 | 18 |
| Donations | 10 | 9 |
| Payment intents | 5 | 5 |
| Payment status | 2 | 1 |
| Refund requests | 24 | 24 |
| Payout destinations | 10 | 9 |
| Payout requests | 12 | 11 |
| Payout status | 4 | 4 |
| KYC | 17 | 16 |
| KYB | 12 | 12 |
| Compliance (staff) | 22 | 18 |
| Risk (staff) | 12 | 12 |
| Admin operations | 28 | 22 |
| Notifications | 5 | 3 |
| Reports | 3 | 3 |
| Reconciliation (staff) | 18 | 18 |
| Audit access (staff) | 6 | 5 |
| Webhooks | 3 | 2 |
| Health | 2 | 2 |
| **Total** | **289** | **266** operations on 246 paths |

Catalogue: **289** endpoints. OpenAPI: **266** operations on **246** paths.

## 18. Decisions applied and remaining open points

Decisions taken by the Stage 2 lead in [design-baseline.md §12](../stage-2/design-baseline.md) and applied here:

| # | Decision | How the API reflects it |
|---|---|---|
| I-1 | Organisation roles are `ORG_ADMIN` and `ORG_MEMBER` only | Organisation payouts, destinations, financial summary and statements are `ORG_ADMIN`-only (`org.payout.request`, `org.payout_destination.manage`, `org.finance.view`). |
| I-2 | Guest donors: no guest session, only a per-donation access token | `X-Donation-Access-Token` (SHA-256 in `app.donations.access_token_hash`): read donation, payment status, receipt; request a refund and read it (§9.4). `GET /auth/session` needs a real session. Guest idempotency scope and token rotation on replay: §7.2. |
| I-3 | Per-module maker-checker request tables | Hold releases: `RSK-04/05` on `risk.hold_release_requests` (`/admin/risk/hold-release-requests/{id}/approve`). Destination overrides: `DST-07/08/09` on `app.payout_destination_override_requests`. Dispute acceptance: `REF-16/17` set `payment_disputes.accept_requested_by` / `accept_approved_by`. Kill-switch deactivation and flag changes: `ADM-18/19/24` on `app.feature_flag_changes` (`/admin/feature-flag-changes/{id}/approve`). |
| I-4 | New staff role `BUSINESS_APPROVER` | Holds approval-only permissions `fee.config.approve`, `recovery.write_off.approve` (above threshold), `campaign.review_policy.approve`, `limit.change.approve` (business-owned limits). Added to the authorization matrix and to `RoleAssignmentRequestCreate.role`. |
| I-5 | identity-data-protection.md wins on KYC status visibility | `kyc.status.view` excludes ADMIN and SUPER_ADMIN. |
| I-6 | Staff login = password + mandatory WebAuthn/TOTP | `StaffLoginRequest` is email + password; `StaffMfaVerifyRequest` accepts WebAuthn or TOTP only. |
| I-7 | Duplicate-payment guard adopted | `409 PAYMENT_OUTCOME_UNKNOWN` on DON-02 with an explicit, delayed override (§14). |
| I-20 | Only FINANCE approves payouts | `payout.approve` is FINANCE-only; DUAL = two distinct FINANCE approvers, neither the requester (POR-08). COMPLIANCE never approves payouts. |
| I-23 | Permission names | `recovery.write_off.approve` (REF-21); separate `security_audit.read` for the security audit chain (AUD-03). |
| I-24 | Bulk refunds | `POST /api/v1/admin/refund-requests/bulk` (REF-22, COMPLIANCE `refund.bulk.request`), FINANCE checker `refund.approve` (REF-23), BUSINESS_APPROVER `refund.bulk.approve` above the configured total (REF-24); manifest hash bound to approvals. |
| I-25 | Screening vendor callbacks | `POST /api/v1/webhooks/screening/{vendor}` (WHK-03) → `compliance.screening_callback_inbox`. |

Remaining open points:

| # | Topic | Position taken here | Needs |
|---|---|---|---|
| 1 | Refund API shape vs Stage 1 §7 | Donor path is `POST /donations/{donation_id}/refund-requests` (donation id, not payment id); withdrawal is `POST /refund-requests/{id}/withdraw` (a state transition to `WITHDRAWN`, not a `DELETE`); approve/reject/hold are separate POSTs as in Stage 1. | Accept as a refinement of [refund-and-dispute-architecture.md §7](../payments/refund-and-dispute-architecture.md). |
| 2 | Other pending maker-checker objects | Not covered by I-3: campaign unfreeze / cancel-from-FROZEN (proposed home `campaign_moderation_actions`), recovery write-off (`recovery_cases` state `WRITE_OFF_PENDING`), KYC/KYB second approval (`kyc_decisions`), case decision proposals (`compliance_case_events`), review-policy versions. | Database design to confirm columns. |
| 3 | New permissions | Permissions marked NEW in [authorization-matrix.md §2](authorization-matrix.md). | Stage 4 permission seed; review against operational-controls §1. |
| 4 | New audit actions | Marked `(new)` in the catalogue. | Add to the AUDIT §5 closed catalogue in Stage 3. |
| 5 | Guest replay token rotation | A guest idempotent replay mints a new access token and invalidates the old one (§7.2). | Confirm with authentication-authorization.md. |
| 6 | Money pattern | Stricter than the brief's `^-?[0-9]+$`: no leading zeros and ≤ 19 digits, as MONEY.md §3.3 requires. | None (MONEY.md wins). |
