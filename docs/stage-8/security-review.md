# Stage 8 — Security review

Scope: `internal/psp`, `internal/payments`, the two Stage 8 migrations, the payment routes and the staff payment UI.
Evidence names tests in `tests/integration/payments_test.go` (IT), unit tests (UT) and migrations (DB).

| # | Threat | Control | Evidence | Status |
|---|---|---|---|---|
| 1 | Unsigned webhook | Signature required before anything is stored | IT `TestPaymentsWebhookSecurity`; UT `TestVerify` | Mitigated |
| 2 | Forged signature / wrong key | HMAC-SHA256 recomputed, constant-time compare; kid selects the slot | IT, UT | Mitigated (sandbox scheme; real providers use their documented procedure — PCR-017) |
| 3 | Payload tampering | Signature covers the exact bytes; JSON parsed only after verification | IT (tampered amount → 401) | Mitigated |
| 4 | Replay / timestamp manipulation | Signed timestamp, ±replay window (300 s); dedupe on (provider, event id); precedence makes a replayed state a no-op | IT (stale, future, replay of valid bytes); UT | Mitigated |
| 5 | Provider spoofing (unknown provider path) | Provider must exist, be enabled and usable; sandbox only when enabled | IT (`nosuchpsp` → 404, rejection row) | Mitigated |
| 6 | Webhook flood / storage abuse | Per-IP limit `payments.webhook.ip` + `global.ip`; 64 KiB cap; rejections store no payload | IT (413); policy test | Mitigated |
| 7 | Simulated event treated as real (or vice versa) | `simulated` in payload must equal the provider's `is_sandbox`; `is_simulated` pinned by FK on inbox and payments | DB FKs; UT (livemode) | Mitigated |
| 8 | Browser redirect confirms a payment | Return endpoint only consumes a token and schedules a status query; `SUCCEEDED` only from SYNC/WEBHOOK/POLL/RECON (Go + `ck_payment_events_success_source`) | IT `TestPaymentsRedirectSessionNeverConfirms`; UT `TestSourceRules`; IT `TestPaymentsDatabaseInvariants` | Mitigated |
| 9 | Timeout recorded as failure (double charge on retry) | Indeterminate ⇒ `UNKNOWN` (INTERNAL only); FAILED never INTERNAL except pre-submit from CREATED (`ck_payment_events_failed_source`); new attempt refused while UNKNOWN | IT `TestPaymentsTimeoutIsUnknownNeverFailed`; UT | Mitigated |
| 10 | Duplicate payment from double submit | Required Idempotency-Key + DB uniques; donation-intent row lock | IT (replay, 100 concurrent, 20 concurrent attempts) | Mitigated |
| 11 | IDOR on donation intents / sessions | Donor id in every query; 404 for others; session bound to donor and single-use token | IT (other donor → 404; foreign session token → 404) | Mitigated |
| 12 | Unauthorised staff access | Permissions `payment.view` / `payment.operate` / `refund.*` / `payment.sandbox.control`; non-staff → 404 | IT `TestPaymentsStaffOperations` | Mitigated |
| 13 | Sensitive staff actions without fresh MFA | `payment.operate` and `refund.approve` require step-up (route policy) | permission seeds; mock E2E step-up flow | Mitigated |
| 14 | Self-approval of refunds | Go check + `ck_refund_requests_maker_checker` | IT | Mitigated |
| 15 | Manual payment-success override | No endpoint exists; staff can only request a status query | IT (`/succeed`, `/fail` → 404/405); OpenAPI | Mitigated |
| 16 | CSRF | Donor/staff routes behind the session CSRF middleware; webhooks exempt by path (signature-authenticated, no cookies) | existing CSRF tests | Mitigated |
| 17 | Open redirect via provider URL | `safeRedirect`: https, declared host, no userinfo/port; invalid URLs never shown | UT `TestSafeRedirect` | Mitigated |
| 18 | Secrets in database / logs / API | `secretref://` only (CHECK); env resolver; values never logged or returned; staff see reference names + `secrets_resolvable` | DB CHECK test (raw key refused); IT (responses scanned for secret values) | Mitigated; KMS needed before real providers |
| 19 | Card / PIN collection | No such fields anywhere; hosted redirect only; no checkout UI | code review; schema | Mitigated |
| 20 | Sensitive data in logs / metric labels | Logs carry ids and codes only; payer masked/HMAC'd; metric labels exclude ids | code review | Mitigated |
| 21 | Simulator in production | Config refuses `PAYMENTS_SANDBOX_ENABLED` in production; DB refuses a PRODUCTION sandbox account; `LiveExecutionEnabled` constant false | UT `TestPaymentsSandboxRefusedInProduction`, `TestLiveExecutionIsDisabled`; IT | Mitigated |
| 22 | Real provider enabled without selection | `ck_payment_providers_enabled_requires_selection` (SELECTED + ADR) | IT | Mitigated |
| 23 | Merchant-settlement / unverified routing (Model B) | Generated `routable`; composite FK; router filter | IT (methods list, attempts refused) | Mitigated |
| 24 | History tampering | `payment_events`, capabilities, health, rejections append-only (trigger + no UPDATE grant); inbox raw columns immutable; no deletes | IT `TestPaymentsDatabaseInvariants` | Mitigated |
| 25 | Over-refund / refund execution | DB guard; no execution edge | IT | Mitigated |
| 26 | Rate-limit bypass by a donor | `payments.intent.user`, `payments.intent.campaign`, `payments.attempt.user` (protective; Valkey with fallback) | policy test | Mitigated (shared-IP effects: KI-S7-02) |
| 27 | Restricted party receives donations | Any restriction level on campaign, owner, organisation, beneficiary or donor blocks; reason never disclosed | IT (restricted donor) | Mitigated |
| 28 | Lost events on crash | Inbox + outbox in one transaction; in-flight sweeper; dispatch retries and dead letter; never discarded | IT (crash recovery, dead letter) | Mitigated |
| 29 | Donor screening | Not performed (`SCREENING_PROVIDER_NOT_SELECTED`) | — | **Open** (production blocker) |
