# FundZim API Error Model

**Status:** Stage 2 design. **Contract:** `ErrorResponse`, `Error`, `ErrorDetail` in
[`api/openapi/fundzim-v1.yaml`](../../api/openapi/fundzim-v1.yaml); per-operation codes in
`x-fundzim-error-codes`. **Companions:** [api-design.md](api-design.md) · [authorization-matrix.md](authorization-matrix.md).
**Sources:** [ARCHITECTURE.md §6](../ARCHITECTURE.md) (envelope, status codes), [MONEY.md §3.3](../MONEY.md)
(`INVALID_MONEY`), [PAYMENTS.md §10](../PAYMENTS.md) (idempotency codes),
[payout-eligibility-and-controls.md §3.1](../payments/payout-eligibility-and-controls.md) (`PAYOUT_NOT_ELIGIBLE`,
coarse reasons), [STAGE-1-TO-STAGE-2.md §10](../stage-handover/STAGE-1-TO-STAGE-2.md) (`APPROVER_CONFLICT`,
tipping-off), [compliance-case-management.md §6](../compliance/compliance-case-management.md),
[SECURITY.md §10, §15](../SECURITY.md), [AUDIT.md §6](../AUDIT.md).

---

## 1. Envelope

```json
{
  "error": {
    "code": "PAYOUT_NOT_ELIGIBLE",
    "message": "This payout can't be requested right now.",
    "retryable": false,
    "details": [
      { "code": "INSUFFICIENT_AVAILABLE_FUNDS", "meta": { "available": { "amount_minor": "250000", "currency": "USD" } } },
      { "code": "DESTINATION_COOLING_OFF", "meta": { "available_after": "2026-10-10T08:00:00Z" } }
    ]
  },
  "meta": { "request_id": "01JAB7Q9X4M2T8K5V3N6P0R1SZ" }
}
```

| Field | Rule |
|---|---|
| `error.code` | Stable `UPPER_SNAKE` code (pattern `^[A-Z][A-Z0-9_]*$`). Clients branch on it. Never renamed or reused; new codes are additive; unknown codes are handled by HTTP status. |
| `error.message` | Human-readable, safe to show to the caller, in plain language. It may vary by locale and over time; clients must not parse it. Never contains internals (§7). |
| `error.retryable` | `true` only when repeating the **identical** request later may succeed without any change (rate limits, in-progress idempotent request, unavailable dependency, upload still scanning). When `true`, honour `Retry-After` if present. `false` means change the request, the state or the actor first. |
| `error.details[]` | Optional. Field errors (`field` + `code`), or categorised reasons (`code` only, e.g. payout categories, SoD conflict categories). `meta` carries non-sensitive structured hints only (`required_level`, `minimum`/`maximum` as Money, `max_age_seconds`, `available_after`, the caller's **own** unresolved `payment_id`). |
| `meta.request_id` | Always present; matches the `X-Request-ID` response header. Support quotes it; it reveals nothing. |

Error responses are `Content-Type: application/json`, `Cache-Control: no-store`. `401` responses do not set
`WWW-Authenticate` challenges for cookie auth. `429` always, and `409 IDEMPOTENCY_REQUEST_IN_PROGRESS` /
`503` where known, carry `Retry-After` (seconds).

## 2. HTTP mapping

| HTTP | Category | Typical codes |
|---|---|---|
| 400 | Malformed request | `MALFORMED_REQUEST`, `IDEMPOTENCY_KEY_REQUIRED`, `IDEMPOTENCY_KEY_INVALID`, `INVALID_CURSOR`, `INVALID_FILTER`, `WEBHOOK_PAYLOAD_INVALID` |
| 401 | Not authenticated | `AUTHENTICATION_REQUIRED`, `SESSION_EXPIRED`, `INVALID_CREDENTIALS`, `OTP_INVALID`, `OTP_EXPIRED`, `WEBHOOK_SIGNATURE_INVALID` |
| 403 | Not permitted | `PERMISSION_DENIED`, `CSRF_TOKEN_INVALID`, `MFA_REQUIRED`, `STEP_UP_REQUIRED`, `VERIFICATION_REQUIRED`, `ACCOUNT_RESTRICTED`, `APPROVER_CONFLICT`, `STAFF_CONFLICT_OF_INTEREST`, `KYC_ACCESS_NOT_BOUND`, `CHALLENGE_REQUIRED`, `FEATURE_NOT_AVAILABLE` |
| 404 | Not found / not visible | `<RESOURCE>_NOT_FOUND`, `ROUTE_NOT_FOUND` (also the answer for objects the caller may not see — authorization-matrix §7) |
| 405 | Method | `METHOD_NOT_ALLOWED` |
| 409 | State / concurrency / idempotency conflict | `IDEMPOTENCY_KEY_REUSED`, `IDEMPOTENCY_REQUEST_IN_PROGRESS`, `VERSION_CONFLICT`, `*_INVALID_TRANSITION`, `PAYOUT_IN_FLIGHT_EXISTS`, `PAYOUT_ALREADY_SUBMITTED`, `PAYMENT_OUTCOME_UNKNOWN`, `APPROVAL_EXPIRED` |
| 413 / 415 | Body | `PAYLOAD_TOO_LARGE`, `UNSUPPORTED_MEDIA_TYPE` |
| 422 | Validation / business rule | `VALIDATION_FAILED`, `INVALID_MONEY`, `CURRENCY_*`, `AMOUNT_*`, `PAYOUT_NOT_ELIGIBLE`, `CAMPAIGN_INCOMPLETE`, `JUSTIFICATION_REQUIRED` |
| 429 | Rate / attempt limits | `RATE_LIMITED`, `OTP_ATTEMPTS_EXCEEDED`, `KYC_REVEAL_LIMIT_REACHED` |
| 500 | Server | `INTERNAL_ERROR`, `INVARIANT_CHECK_FAILED` |
| 503 | Unavailable | `SERVICE_UNAVAILABLE`, `PROVIDER_UNAVAILABLE` |

**Precedence** when several problems exist: authentication (401) → route visibility (404 for `/admin` and invisible
objects) → CSRF / MFA / permission (403) → idempotency header (400) → body syntax (400) → validation (422; money
format errors make the top-level code `INVALID_MONEY`, otherwise `VALIDATION_FAILED`, with every field in
`details[]`) → idempotency conflict (409) → state and business rules (409/422) → provider (503). Validation errors
are reported together, not one at a time.

## 3. Payment and payout outcome reporting is not error reporting

A payment that the provider declined, that expired, or whose outcome is unknown is **not** an HTTP error: the
donation and payment intent exist, so creation returns `201` and `GET /payments/{payment_id}` returns `200` with
`status` = `FAILED`, `EXPIRED` or `UNKNOWN`. In particular:

- `UNKNOWN` is reported as a status with `do_not_pay_again: true`, never as `FAILED` and never as a 5xx.
- `PAYMENT_OUTCOME_UNKNOWN` (409) is used only to stop a **new** payment by the same donor for the same campaign
  and amount while an earlier one is `UNKNOWN` (baseline §12 I-7, adopted), and on cancel attempts of an
  `UNKNOWN` payment. The donor can override it deliberately only after the configured delay
  (`meta.override_available_after`).
- `PROVIDER_UNAVAILABLE` (503) is returned only when **nothing was sent** to the provider (connection refused,
  circuit open, `PROVIDER_HOLD`); the response says nothing was charged. If anything may have been sent, the
  payment is `UNKNOWN` instead.
- Payout and refund executions likewise report `UNKNOWN` as a status; there is no error that tells a client to
  resubmit.

## 4. Error code catalogue

159 top-level codes, grouped by module. HTTP status is fixed per code. The OpenAPI operation lists the
codes it can return in `x-fundzim-error-codes`; codes common to a class of routes are described in
[api-design.md §15](api-design.md#15-endpoint-catalogue).

### 4.1 Request and validation

| Code | HTTP | Retryable | Meaning / client action |
|---|---|---|---|
| `MALFORMED_REQUEST` | 400 | no | Body is not valid JSON, wrong Content-Type, duplicate JSON keys, or a JSON number where a string is required. |
| `VALIDATION_FAILED` | 422 | no | One or more fields failed validation. `details[]` lists each field and a detail code (unknown fields included). |
| `INVALID_CURSOR` | 400 | no | Cursor is malformed, expired, or was issued for different filters/sort. |
| `INVALID_FILTER` | 400 | no | A filter or sort field is not on the endpoint's allow-list, or its value is malformed. |
| `PAYLOAD_TOO_LARGE` | 413 | no | Body exceeds the route's size limit. |
| `UNSUPPORTED_MEDIA_TYPE` | 415 | no | Content-Type is not `application/json` (or the provider's documented type on webhook routes). |
| `ROUTE_NOT_FOUND` | 404 | no | No such route for this actor. Also returned to non-staff sessions on every `/api/v1/admin/*` route. |
| `METHOD_NOT_ALLOWED` | 405 | no | Method not supported on this route. |
| `VERSION_CONFLICT` | 409 | no | Optimistic-concurrency check failed: the `version` sent does not match the current resource version. Re-read and retry. |
| `EVIDENCE_REQUIRED` | 422 | no | Action requires an evidence record reference that was not supplied. |
| `JUSTIFICATION_REQUIRED` | 422 | no | Staff action requires `justification` text (and a reason code where listed). |
| `FEATURE_NOT_AVAILABLE` | 403 | no | Feature is disabled by a feature flag or policy (e.g. `campaign.individual_for_others.enabled = false`). |

### 4.2 Server

| Code | HTTP | Retryable | Meaning / client action |
|---|---|---|---|
| `INTERNAL_ERROR` | 500 | yes | Unexpected server error. No internals in the message. Safe to retry only if the route is idempotent or an `Idempotency-Key` was sent. |
| `SERVICE_UNAVAILABLE` | 503 | yes | Dependency (database, queue) unavailable or the service is in maintenance. Honour `Retry-After`. |
| `INVARIANT_CHECK_FAILED` | 500 | no | A financial invariant check failed and the operation was refused (CLAUDE.md rule 13). Raises a SEV1; never retried automatically. |

### 4.3 Authentication and authorisation

| Code | HTTP | Retryable | Meaning / client action |
|---|---|---|---|
| `AUTHENTICATION_REQUIRED` | 401 | no | No valid session (missing, expired, revoked). |
| `SESSION_EXPIRED` | 401 | no | Session reached idle or absolute timeout. Sign in again. |
| `INVALID_CREDENTIALS` | 401 | no | Uniform login failure (enumeration-resistant): wrong code, wrong password, wrong second factor or unknown account. |
| `OTP_INVALID` | 401 | no | OTP code wrong for this challenge. Remaining attempts may be returned in `details`. |
| `OTP_EXPIRED` | 401 | no | Challenge expired, consumed, unknown, or bound to another purpose (uniform). |
| `OTP_ATTEMPTS_EXCEEDED` | 429 | yes | Maximum verification attempts for the challenge or the identifier reached. New challenge after `Retry-After`. |
| `CSRF_TOKEN_INVALID` | 403 | no | Unsafe method without a valid `X-CSRF-Token`, or Origin / `Sec-Fetch-Site` check failed. |
| `PERMISSION_DENIED` | 403 | no | Authenticated actor lacks the permission or object-level right, and the resource's existence is not sensitive (see 404-vs-403 rule). |
| `MFA_REQUIRED` | 403 | no | Staff session has not completed MFA (or MFA is not enrolled). |
| `STEP_UP_REQUIRED` | 403 | no | Action needs a fresh step-up (OTP for users, MFA for staff) within `STEP_UP_MAX_AGE`. `details[0].meta.max_age_seconds` is set. Retry after step-up. |
| `ACCOUNT_RESTRICTED` | 403 | no | The account may not perform this action at this time. Deliberately reason-free (covers account holds, verification SUSPENDED, compliance restrictions) — tipping-off safe. |
| `VERIFICATION_REQUIRED` | 403 | no | Verification level below the gate for this action. `details[0].meta.required_level` names the level (non-sensitive). |
| `CHALLENGE_REQUIRED` | 403 | no | Anti-abuse challenge (CAPTCHA / proof-of-work) required before this request is accepted. |
| `APPROVER_CONFLICT` | 403 | no | Segregation-of-duties conflict: checker is the maker/requester/initiator, already approved, changed the destination in the cooling-off window, or has a declared relationship. `details[0].code` gives the conflict category. |
| `STAFF_CONFLICT_OF_INTEREST` | 403 | no | Staff member has a declared or detected conflict with this object (claim, review, decision). |
| `SELF_GRANT_FORBIDDEN` | 403 | no | A SUPER_ADMIN cannot request or approve a role grant for themselves. |
| `ROLE_COMBINATION_FORBIDDEN` | 409 | no | Role grant would create a role-level SoD conflict (operational-controls.md §2, cells marked R). |
| `SESSION_NOT_FOUND` | 404 | no | No such session for this user. |
| `MFA_METHOD_NOT_FOUND` | 404 | no | No such MFA method. |
| `MFA_LAST_METHOD` | 409 | no | Cannot remove the last MFA method of a staff account. |

### 4.4 Idempotency

| Code | HTTP | Retryable | Meaning / client action |
|---|---|---|---|
| `IDEMPOTENCY_KEY_REQUIRED` | 400 | no | Route requires an `Idempotency-Key` header. |
| `IDEMPOTENCY_KEY_INVALID` | 400 | no | `Idempotency-Key` is not a UUID. |
| `IDEMPOTENCY_KEY_REUSED` | 409 | no | Same key was used with a different request (method + path + canonical body hash). Use a new key for a new request. |
| `IDEMPOTENCY_REQUEST_IN_PROGRESS` | 409 | yes | First request with this key is still processing. Retry the identical request after `Retry-After`; never with a new key. |

### 4.5 Money and currency

| Code | HTTP | Retryable | Meaning / client action |
|---|---|---|---|
| `INVALID_MONEY` | 422 | no | `amount_minor` is not a canonical digit string within int64 (no number, decimal point, exponent, sign, whitespace or leading zero) — MONEY.md §3.3. |
| `CURRENCY_NOT_SUPPORTED` | 422 | no | Currency code is not in the platform currency registry. |
| `CURRENCY_NOT_ACCEPTED` | 422 | no | Campaign (or rail) does not accept this currency. FundZim never converts. |
| `CURRENCY_MISMATCH` | 422 | no | Two amounts or an amount and a resource have different currencies (e.g. payout vs destination, refund vs payment). |
| `AMOUNT_BELOW_MINIMUM` | 422 | no | Amount below the configured minimum for this currency/rail. `details[0].meta.minimum` is a Money object. |
| `AMOUNT_ABOVE_MAXIMUM` | 422 | no | Amount above the configured maximum for this currency/rail/provider. `details[0].meta.maximum` is a Money object. |
| `AMOUNT_EXCEEDS_REFUNDABLE` | 422 | no | Requested refund exceeds the refundable remainder of the payment. |
| `DONATION_LIMIT_REACHED` | 422 | no | Donor's verification level does not allow this amount or frequency; sign in and verify to give more. Never reveals risk scoring. |

### 4.6 Campaigns

| Code | HTTP | Retryable | Meaning / client action |
|---|---|---|---|
| `CAMPAIGN_NOT_FOUND` | 404 | no | No such campaign visible to this actor. |
| `CAMPAIGN_NOT_ACCEPTING_DONATIONS` | 409 | no | Campaign is not ACTIVE (or an organisation suspension stops donations). |
| `CAMPAIGN_INVALID_TRANSITION` | 409 | no | Lifecycle transition not allowed from the current state (PRODUCT §6.2; DB transition guard). |
| `CAMPAIGN_NOT_EDITABLE` | 409 | no | Campaign state does not allow this edit. |
| `CAMPAIGN_INCOMPLETE` | 422 | no | Required content, beneficiary, acknowledgements or fundraising authority missing for submission. `details[]` lists the gaps. |
| `CAMPAIGN_RESUBMISSION_LIMIT` | 409 | no | Configured limit of resubmissions after rejection reached. |
| `CAMPAIGN_REVIEW_ALREADY_CLAIMED` | 409 | no | Another reviewer holds the review claim. |
| `FUNDRAISING_AUTHORITY_REQUIRED` | 422 | no | A valid fundraising authority is required for this campaign type or end date (PVO Act gate, LR-046–LR-050). |
| `CAMPAIGN_UPDATE_NOT_FOUND` | 404 | no | No such campaign update. |
| `MEDIA_NOT_FOUND` | 404 | no | No such media item on this campaign. |
| `CATEGORY_NOT_FOUND` | 404 | no | No such campaign category. |
| `ABUSE_REPORT_NOT_FOUND` | 404 | no | No such abuse report. |
| `MODERATION_ACTION_NOT_FOUND` | 404 | no | No such pending moderation action. |

### 4.7 Uploads

| Code | HTTP | Retryable | Meaning / client action |
|---|---|---|---|
| `UPLOAD_NOT_READY` | 409 | yes | Upload is still in quarantine / scanning. Retry later. |
| `UPLOAD_REJECTED` | 422 | no | Upload failed scanning, type sniffing or size limits. No scanner detail is returned. |
| `UPLOAD_EXPIRED` | 409 | no | Upload session expired before completion. |

### 4.8 Organisations

| Code | HTTP | Retryable | Meaning / client action |
|---|---|---|---|
| `ORGANISATION_NOT_FOUND` | 404 | no | No such organisation visible to this actor. |
| `ORG_LAST_ADMIN` | 409 | no | Operation would leave the organisation without an ORG_ADMIN. |
| `ORG_MEMBER_NOT_FOUND` | 404 | no | No such member. |
| `ORG_ALREADY_MEMBER` | 409 | no | Invitee is already a member. |
| `INVITATION_NOT_FOUND` | 404 | no | Invitation unknown, expired, revoked, or not addressed to the caller's verified contact (uniform). |

### 4.9 Beneficiaries

| Code | HTTP | Retryable | Meaning / client action |
|---|---|---|---|
| `BENEFICIARY_NOT_FOUND` | 404 | no | No such beneficiary visible to this actor. |
| `BENEFICIARY_LOCKED` | 409 | no | Beneficiary record cannot be edited while its verification is under review. |
| `BENEFICIARY_TYPE_NOT_ALLOWED` | 422 | no | Beneficiary type not allowed for this owner, category or policy. |
| `CONSENT_REQUIRED` | 422 | no | A required consent (beneficiary, guardian, health-data, vendor processing) is missing. |
| `INSTITUTION_PAYEE_NOT_FOUND` | 404 | no | No such verified institution payee. |
| `BENEFICIARY_VERIFICATION_NOT_FOUND` | 404 | no | No such beneficiary verification. |

### 4.10 Payments, refunds and disputes

| Code | HTTP | Retryable | Meaning / client action |
|---|---|---|---|
| `DONATION_NOT_FOUND` | 404 | no | No such donation visible to this actor. |
| `PAYMENT_NOT_FOUND` | 404 | no | No such payment visible to this actor. |
| `PAYMENT_METHOD_UNAVAILABLE` | 422 | no | No enabled provider/rail for this method, currency and campaign (capability routing; MERCHANT_SETTLEMENT custody refused). |
| `PAYMENT_OUTCOME_UNKNOWN` | 409 | no | An earlier payment by the same donor for the same campaign and amount is in UNKNOWN (baseline §12 I-7). Do not pay again. `details[0].meta.payment_id` points at the donor's own payment and `meta.override_available_after` gives the time after which the donor may deliberately pay again by resending with `acknowledged_unresolved_payment_id` (delay is an INTERNAL_RISK limit). |
| `PAYMENT_NOT_CANCELLABLE` | 409 | no | Payment is final, UNKNOWN, or the provider does not support cancellation at this point. |
| `PAYMENT_NOT_REFUNDABLE` | 409 | no | Payment is not SUCCEEDED/PARTIALLY_REFUNDED, is DISPUTED, or the refund window/policy does not allow it. |
| `REFUND_REQUEST_NOT_FOUND` | 404 | no | No such refund request visible to this actor. |
| `REFUND_REQUEST_EXISTS` | 409 | no | An open refund request already exists for this donation. |
| `REFUND_REQUEST_INVALID_TRANSITION` | 409 | no | Refund request state does not allow this action. |
| `REFUND_BATCH_NOT_FOUND` | 404 | no | No such bulk refund batch. |
| `DISPUTE_NOT_FOUND` | 404 | no | No such dispute. |
| `DISPUTE_INVALID_TRANSITION` | 409 | no | Dispute state does not allow this action, or the evidence deadline has passed. |
| `RECOVERY_CASE_NOT_FOUND` | 404 | no | No such recovery case. |
| `RECOVERY_CASE_INVALID_TRANSITION` | 409 | no | Recovery case state does not allow this action. |

### 4.11 Payouts

| Code | HTTP | Retryable | Meaning / client action |
|---|---|---|---|
| `PAYOUT_NOT_FOUND` | 404 | no | No such payout request visible to this actor. |
| `PAYOUT_NOT_ELIGIBLE` | 422 | no | Request-time eligibility failed. `details[]` carries coarse, non-sensitive reason categories only (error-model.md §6). No payout row is created; an eligibility decision record is. |
| `PAYOUT_IN_FLIGHT_EXISTS` | 409 | no | Another non-terminal payout exists for this campaign and currency (EC-11). |
| `PAYOUT_ALREADY_SUBMITTED` | 409 | no | Payout reached SUBMITTED (or later); it can no longer be cancelled or changed from the owner side. |
| `PAYOUT_INVALID_TRANSITION` | 409 | no | Payout state does not allow this staff action. |
| `APPROVAL_EXPIRED` | 409 | no | The pending approval object expired; it is closed EXPIRED, never auto-approved. |
| `APPROVAL_ALREADY_RECORDED` | 409 | no | This approver already recorded a decision on this object. |
| `PAYOUT_DESTINATION_NOT_FOUND` | 404 | no | No such payout destination visible to this actor. |
| `PAYOUT_DESTINATION_NOT_ALLOWED` | 422 | no | Holder is not the owner, the verified beneficiary, or a verified institution/organisation; or the rail does not fit the currency. |
| `PAYOUT_DESTINATION_INACTIVE` | 409 | no | Destination is deactivated or not verified for this use. |
| `DESTINATION_OVERRIDE_NOT_FOUND` | 404 | no | No such destination override request. |

### 4.12 KYC / KYB

| Code | HTTP | Retryable | Meaning / client action |
|---|---|---|---|
| `KYC_ACCESS_NOT_BOUND` | 403 | no | C3 access must be bound to an open case or review assigned to the caller. |
| `KYC_ATTEMPT_NOT_FOUND` | 404 | no | No such verification attempt for this subject. |
| `KYC_ATTEMPT_INVALID_STATE` | 409 | no | Attempt state does not allow this step (e.g. already submitted). |
| `KYC_RETRY_NOT_AVAILABLE` | 409 | no | A new attempt is not available now (cool-down, attempt limit, or staff decision). Reason-free by design. |
| `KYC_CONSENT_REQUIRED` | 422 | no | Required processing/biometric consent not recorded. |
| `KYC_DOCUMENT_REJECTED` | 422 | no | Document failed type/size/scan checks. No scanner detail is returned. |
| `KYC_CASE_NOT_FOUND` | 404 | no | No such KYC/KYB case. |
| `KYC_DOCUMENT_NOT_FOUND` | 404 | no | No such document on this case. |
| `KYC_DECISION_INVALID` | 409 | no | Decision not allowed in the case's current state, or a second approver is required/missing. |
| `KYC_REVEAL_LIMIT_REACHED` | 429 | yes | Per-staff daily reveal/view budget (`kyc.reveal_daily_max`) exhausted. |
| `KYB_ATTEMPT_NOT_FOUND` | 404 | no | No such organisation verification attempt. |
| `FUNDRAISING_AUTHORITY_INVALID` | 422 | no | Authority evidence missing, expired, or the validity window does not cover the requested dates. |

### 4.13 Compliance

| Code | HTTP | Retryable | Meaning / client action |
|---|---|---|---|
| `COMPLIANCE_CASE_NOT_FOUND` | 404 | no | No such case visible to this actor. RESTRICTED_STR cases are always 404 for actors without `str.prepare`. |
| `CASE_INVALID_TRANSITION` | 409 | no | Case state does not allow this transition. |
| `CASE_CLOSURE_INCOMPLETE` | 409 | no | Closure memo, reason code, hold resolution or action evidence missing. |
| `DECISION_PROPOSAL_NOT_FOUND` | 404 | no | No such decision proposal. |
| `SCREENING_HIT_NOT_FOUND` | 404 | no | No such screening hit. |
| `SANCTIONS_BLOCK_NOT_OVERRIDABLE` | 409 | no | Overrides and exemptions can never bypass a confirmed sanctions block or a legal hold. |
| `OVERRIDE_EXPIRY_REQUIRED` | 422 | no | Overrides/exemptions/restrictions need an expiry within the configured maximum. |
| `RESTRICTION_NOT_FOUND` | 404 | no | No such compliance restriction. |
| `OVERRIDE_NOT_FOUND` | 404 | no | No such compliance override. |

### 4.14 Risk, holds and limits

| Code | HTTP | Retryable | Meaning / client action |
|---|---|---|---|
| `HOLD_NOT_FOUND` | 404 | no | No such hold visible to this actor. |
| `HOLD_ALREADY_RELEASED` | 409 | no | Hold is already released. |
| `HOLD_TYPE_NOT_PERMITTED` | 403 | no | Actor may not place or release this hold type. |
| `LIMIT_NOT_FOUND` | 404 | no | No such limit key/version. |
| `LIMIT_SOURCE_REQUIRED` | 422 | no | Limit change lacks source citation, approval owner, effective date or review date. |
| `LIMIT_CHANGE_NOT_FOUND` | 404 | no | No such limit change request. |
| `ALERT_NOT_FOUND` | 404 | no | No such monitoring alert. |

### 4.15 Ledger

| Code | HTTP | Retryable | Meaning / client action |
|---|---|---|---|
| `LEDGER_ADJUSTMENT_NOT_FOUND` | 404 | no | No such ledger adjustment. |
| `LEDGER_ADJUSTMENT_UNBALANCED` | 422 | no | Proposed lines are not balanced per currency, or mix currencies in one journal. |
| `LEDGER_PERIOD_CLOSED` | 409 | no | Target ledger period is closed; post into the open period with a reference. |
| `LEDGER_TRANSACTION_NOT_FOUND` | 404 | no | No such ledger transaction. |
| `LEDGER_ACCOUNT_NOT_FOUND` | 404 | no | No such ledger account. |

### 4.16 Reconciliation

| Code | HTTP | Retryable | Meaning / client action |
|---|---|---|---|
| `RECONCILIATION_IMPORT_DUPLICATE` | 409 | no | A file with the same content hash was already imported for this source. |
| `RECONCILIATION_IMPORT_NOT_FOUND` | 404 | no | No such import. |
| `RECONCILIATION_RUN_NOT_FOUND` | 404 | no | No such run. |
| `DISCREPANCY_NOT_FOUND` | 404 | no | No such discrepancy. |
| `RESOLUTION_NOT_FOUND` | 404 | no | No such resolution. |
| `SETTLEMENT_BATCH_NOT_FOUND` | 404 | no | No such settlement batch. |

### 4.17 Reports

| Code | HTTP | Retryable | Meaning / client action |
|---|---|---|---|
| `REPORT_NOT_FOUND` | 404 | no | No such report job visible to this actor. |
| `REPORT_NOT_READY` | 409 | yes | Report is still generating. |

### 4.18 Users

| Code | HTTP | Retryable | Meaning / client action |
|---|---|---|---|
| `CONTACT_CHANGE_NOT_FOUND` | 404 | no | No such pending contact change. |
| `CONTACT_IN_USE` | 409 | no | The new contact cannot be used. Uniform message; does not confirm whether another account holds it. |

### 4.19 Admin

| Code | HTTP | Retryable | Meaning / client action |
|---|---|---|---|
| `USER_NOT_FOUND` | 404 | no | No such user. |
| `ROLE_REQUEST_NOT_FOUND` | 404 | no | No such role-assignment request. |
| `ROLE_ASSIGNMENT_NOT_FOUND` | 404 | no | No such role assignment. |
| `FEE_CHANGE_NOT_FOUND` | 404 | no | No such fee-schedule change request. |
| `FEE_CHANGE_NOT_FUTURE_DATED` | 422 | no | Fee changes must take effect in the future; never retroactive. |
| `KILLSWITCH_NOT_FOUND` | 404 | no | No such kill switch. |
| `FLAG_CHANGE_NOT_FOUND` | 404 | no | No such pending feature-flag change. |
| `DEAD_LETTER_NOT_FOUND` | 404 | no | No such dead-lettered webhook item. |

### 4.20 Notifications

| Code | HTTP | Retryable | Meaning / client action |
|---|---|---|---|
| `UNSUBSCRIBE_TOKEN_INVALID` | 400 | no | Unsubscribe token malformed or expired. |

### 4.21 Audit

| Code | HTTP | Retryable | Meaning / client action |
|---|---|---|---|
| `AUDIT_EVENT_NOT_FOUND` | 404 | no | No such audit event in the caller's scope. |
| `EVIDENCE_RECORD_NOT_FOUND` | 404 | no | No such evidence record in the caller's scope. |

### 4.22 Provider

| Code | HTTP | Retryable | Meaning / client action |
|---|---|---|---|
| `PROVIDER_UNAVAILABLE` | 503 | yes | Provider unreachable before anything was sent, circuit breaker open, or PROVIDER_HOLD. Nothing was charged. Honour `Retry-After`. |
| `PROVIDER_CAPABILITY_MISSING` | 422 | no | The provider/rail does not support the requested operation (e.g. refund); handled by the documented fallback, if any. |

### 4.23 Webhooks

| Code | HTTP | Retryable | Meaning / client action |
|---|---|---|---|
| `WEBHOOK_SIGNATURE_INVALID` | 401 | no | Signature, timestamp window or key id invalid. Nothing stored. Body is minimal. |
| `WEBHOOK_PROVIDER_UNKNOWN` | 404 | no | No such provider configured for this environment. |
| `WEBHOOK_PAYLOAD_INVALID` | 400 | no | Verified but unparseable payload (stored for investigation, alert raised). |

### 4.24 Rate limiting

| Code | HTTP | Retryable | Meaning / client action |
|---|---|---|---|
| `RATE_LIMITED` | 429 | yes | Rate limit exceeded for this route class and key. Honour `Retry-After`. |


## 5. Field detail codes (`details[].code`)

Used with `VALIDATION_FAILED` / `INVALID_MONEY` (with `field`) and as categories for `APPROVER_CONFLICT`
(without `field`).

| Detail code | Meaning |
|---|---|
| `REQUIRED` | Field is required. |
| `UNKNOWN_FIELD` | Field is not part of the input DTO (mass-assignment defence). |
| `INVALID_TYPE` | Wrong JSON type. |
| `INVALID_FORMAT` | Pattern/format mismatch (UUID, E.164 phone, email, date-time, public_code). |
| `TOO_SHORT` | Below minimum length. |
| `TOO_LONG` | Above maximum length. |
| `OUT_OF_RANGE` | Numeric/date value outside the allowed range. |
| `NOT_ALLOWED_VALUE` | Value not in the allowed enum or allow-list. |
| `MUST_BE_POSITIVE` | Amount must be greater than zero. |
| `INVALID_MONEY` | Money value not canonical (see top-level INVALID_MONEY). |
| `CURRENCY_NOT_SUPPORTED` | Currency not in the registry. |
| `IMMUTABLE_FIELD` | Field cannot change in the current state. |
| `REFERENCE_NOT_FOUND` | Referenced id does not exist or is not visible to the caller. |
| `DUPLICATE_VALUE` | Value must be unique within the request or the caller's own resources. |
| `CONFLICT_MAKER_IS_CHECKER` | APPROVER_CONFLICT category: approver is the maker/requester. |
| `CONFLICT_INITIATOR` | APPROVER_CONFLICT category: approver initiated the underlying action. |
| `CONFLICT_DESTINATION_CHANGER` | APPROVER_CONFLICT category: approver changed/verified the destination within the cooling-off period. |
| `CONFLICT_DUPLICATE_APPROVER` | APPROVER_CONFLICT category: DUAL approvals need two distinct people. |
| `CONFLICT_DECLARED_RELATIONSHIP` | APPROVER_CONFLICT category: declared or detected relationship with the subject. |

Field paths use dots and zero-based indexes: `amount.amount_minor`, `lines[2].amount.currency`.

## 6. `PAYOUT_NOT_ELIGIBLE` reason categories (owner-safe)

The eligibility engine records every check result (EC-01 … EC-22) in `payout_eligibility_decisions` for staff.
The owner receives **only** these coarse categories ([payout-eligibility-and-controls.md §3.1](../payments/payout-eligibility-and-controls.md)).
The same categories appear in `PayoutEligibilityPreview.blocking_categories` and `PayoutRequest.rejection_category`.

| Category (`details[].code`) | Covers checks | Owner-facing meaning |
|---|---|---|
| `CAMPAIGN_NOT_ELIGIBLE` | EC-01 | Campaign status does not allow payouts (e.g. not ACTIVE/COMPLETED). |
| `REQUESTER_NOT_AUTHORISED` | EC-02 | Caller is not the owner or an ORG_ADMIN with `org.payout.request`. |
| `VERIFICATION_REQUIRED` | EC-03 | Owner/organisation/representative verification level is insufficient. `meta.required_level` may be given. |
| `BENEFICIARY_NOT_VERIFIED` | EC-04 | Beneficiary verification is not VERIFIED. |
| `DESTINATION_NOT_VERIFIED` | EC-05 | Payout destination is not verified for this campaign. |
| `DESTINATION_COOLING_OFF` | EC-06 | Destination was added/changed recently; `meta.available_after` (timestamp) may be given. |
| `CURRENCY_OR_RAIL_UNSUPPORTED` | EC-08 | Destination rail or provider cannot pay out in this currency. |
| `INSUFFICIENT_AVAILABLE_FUNDS` | EC-09 | Amount exceeds the available (settled and released) balance in that currency. |
| `AMOUNT_OUT_OF_RANGE` | EC-10 | Below the minimum or above the maximum per payout; `meta.minimum`/`meta.maximum` (Money) may be given. |
| `FUNDRAISING_AUTHORITY_INVALID` | FA gate | Required fundraising-authority evidence missing or expired. |
| `PAYOUTS_PAUSED` | EC-12/13/15/16/19 (FAIL) | Payouts are paused for review. Single reason-free category for every hold, case, screening, risk or pool-integrity FAIL. Never refined (tipping-off, LR-008). |
| `TEMPORARILY_UNAVAILABLE` | EC-18/EC-20 (FAIL at request) | Payout rail temporarily unavailable; try later. |

Rules:

- `PAYOUTS_PAUSED` is the only category for holds (any type), open `blocks_payouts` cases, screening matches or
  staleness, risk signals and pool-integrity failures. It is never split, never accompanied by a hold type, case
  id, date the hold was placed, or reviewer name, and its message is the pre-approved neutral text ("Payouts are
  paused while we complete a review. Contact support if you have questions.").
- `REVIEW` and `DEFER` results are not errors: the payout is created and goes to `PENDING_REVIEW`
  (owner sees `UNDER_REVIEW`).
- `PAYOUT_IN_FLIGHT_EXISTS` (EC-11) is a separate 409 because the owner can act on it (wait or cancel the other
  payout).

## 7. What must never appear in an error (message, details or meta)

| Never | Instead |
|---|---|
| SQL, constraint or table names, stack traces, file paths, Go error strings, library or provider exception text | Stable code + neutral message; internal detail goes to the structured log under `request_id` (redacted, [SECURITY.md §15](../SECURITY.md)). |
| Internal ids of other users or objects the caller cannot see (other users' user ids, other campaigns, staff ids, case ids, hold ids, ledger account ids) | Nothing, or the caller's own ids only. |
| Hold types or reasons, compliance case existence, STR activity, screening hits, PEP status, risk scores or rule names, fraud indicators — to owners, donors, beneficiaries or support-facing text | `ACCOUNT_RESTRICTED`, `PAYOUTS_PAUSED`, `KYC_RETRY_NOT_AVAILABLE` — deliberately reason-free (LR-008, LR-072, MLPC Act s 31(2) as cited in compliance-case-management.md §6). |
| Whether an account, phone or email exists (login, OTP, recovery, invitations, contact change) | Uniform responses: `202` for OTP send, `INVALID_CREDENTIALS`, `OTP_EXPIRED`, `INVITATION_NOT_FOUND`, `CONTACT_IN_USE` without saying who uses it. |
| Secrets or C3 values: passwords, OTPs, tokens, keys, webhook secrets, card data, identity numbers, full payout account numbers, document text | Masked values only, and only to the owner of the data. |
| Malware scanner verdicts or rule names | `UPLOAD_REJECTED` / `KYC_DOCUMENT_REJECTED`. |
| Raw provider responses or provider error codes in donor-facing text | `failure_category` (coarse) on the payment status; provider raw status is staff-only. |
| Remaining attempts or thresholds that help an attacker tune abuse (beyond the caller's own OTP challenge) | `RATE_LIMITED` + `Retry-After`. |
| Values of limits that are internal risk controls (donor velocity limits, risk thresholds) | `DONATION_LIMIT_REACHED` with a "verify to give more" hint. Regulatory/provider minimum and maximum amounts **may** be shown (`AMOUNT_BELOW_MINIMUM` meta). |

Webhook error bodies are minimal (`{"error":{"code":"WEBHOOK_SIGNATURE_INVALID","message":"Unauthorized.","retryable":false}}`)
and never echo the payload or the expected signature.

Every error response is logged with `request_id`, code and actor (no body); denied staff actions additionally
emit audit events with `outcome = denied` ([AUDIT.md §5](../AUDIT.md), `payout.approval.denied_self`).

## 8. Localisation

- English first; Shona and Ndebele planned ([FRONTEND.md §9](../FRONTEND.md)).
- The **code** is the contract. The web app maps codes (and `details[].code`) to its own message catalogue and
  uses `message` only as a fallback. Server messages are English by default and may follow
  `Accept-Language` (`en`, `sn`, `nd`) once catalogues exist; the code never changes with locale.
- Messages are short, plain, action-oriented, and avoid jargon ("Enter the phone number in international format").
- Money in messages is formatted server-side with the currency always shown (ZWG as "ZiG"); structured amounts are
  in `meta` as Money objects for the client to format.
- Tipping-off-sensitive texts (holds, restrictions, verification outcomes) come from pre-approved templates and are
  the same in every language.

## 9. Retry guidance for clients

| Code(s) | Client behaviour |
|---|---|
| `RATE_LIMITED`, `OTP_ATTEMPTS_EXCEEDED`, `KYC_REVEAL_LIMIT_REACHED` | Wait `Retry-After`; do not retry in a loop. |
| `IDEMPOTENCY_REQUEST_IN_PROGRESS` | Retry the identical request with the **same** key after `Retry-After`. |
| `IDEMPOTENCY_KEY_REUSED` | Programming error: a new logical request needs a new key. Never auto-retry. |
| `SERVICE_UNAVAILABLE`, `PROVIDER_UNAVAILABLE`, `INTERNAL_ERROR` on an idempotent request | Retry with the same `Idempotency-Key` and exponential back-off. Without a key, do not retry non-GET requests automatically. |
| `UPLOAD_NOT_READY`, `REPORT_NOT_READY` | Poll later. |
| `STEP_UP_REQUIRED` | Run step-up (AUTH-05/06 or AUTH-11/12), then resend the same request (same key). |
| `VERSION_CONFLICT` | Re-read, re-apply the user's change, resend. |
| `PAYMENT_OUTCOME_UNKNOWN` | Show the existing payment's status; tell the donor not to pay again. Offer "pay again anyway" only after `override_available_after`. |
| Anything else with `retryable: false` | Show the message / fix input; do not retry. |
