# Stage 8 — Interface contracts

Binding contract between the backend (`internal/psp`, `internal/payments`), the web app (`apps/web`) and the OpenAPI
document. Decisions: [ADR-039](../adr/ADR-039-payment-abstraction-donation-intents-and-sandbox-execution.md). Tables:
migrations `20261012090000_psp_registry_and_inbox.sql`, `20261012090100_payments.sql`.

Conventions (unchanged, ADR-038 §7): `/api/v1`, envelope `{data, meta}` / `{error:{code,message,details,request_id}}`,
`UPPER_SNAKE` codes, UUIDv7 ids, money as `amount_minor` **string of digits** + `currency`, lists
`{items, next_cursor}` (keyset, newest first unless stated), every payment response `Cache-Control: no-store`.
Mutations require `Idempotency-Key` (UUID) where marked **IK**. Staff actions marked **step-up** need a fresh MFA
(the existing `STEP_UP_REQUIRED` flow).

## 1. Glossary (brief ↔ Stage 2 ↔ API)

| Brief | Table | API resource |
|---|---|---|
| DonationIntent | `donation_intents` | `donation-intents` |
| PaymentAttempt (donor retry) | `payment_intents` (canonical payment, `payment_id`) | `attempts` (`/donation-intents/{id}/attempts`), staff: `/admin/payments/attempts` |
| Provider request (call) | `payment_attempts` | `provider_calls` inside attempt detail |
| PaymentProvider / PaymentMethod | `payment_providers`, `provider_capabilities` | `payment-providers/capabilities`, `payment-methods` |
| PaymentSession | `payment_sessions` | `next_action` + `payment-sessions/{id}/return` |
| ProviderEvent | `provider_webhook_inbox` (+ `provider_webhook_rejections`) | `/admin/payments/webhooks` |
| PaymentEvent | `payment_events` | `events` inside detail views |
| PaymentReconciliationReference | `payment_reconciliation_references` | `/admin/payments/reconciliation` |
| RefundRequest | `refund_requests` | `/admin/payments/refund-requests` |
| PaymentAuditRecord | `audit.audit_events` (actions `payments.*`) | — |

## 2. Donor API (policy: signed-in USER; staff accounts get 403 `ACCOUNT_KIND_NOT_ALLOWED`)

| Method & path | Purpose | Notes |
|---|---|---|
| `POST /donation-intents` **IK** | Create a donation intent | Body `{campaign_id, amount_minor, currency, is_anonymous?}`. 201 `DonationIntent`. Eligibility, currency, limits and availability are checked before anything is stored |
| `GET /donation-intents` | My donation intents | `?cursor`; `{items, next_cursor}` |
| `GET /donation-intents/{donation_intent_id}` | One intent with its attempts | Own only (404 otherwise) |
| `POST /donation-intents/{donation_intent_id}/attempts` **IK** | Start a payment attempt | Body `{method}`. 201 `PaymentAttempt` whose `status` is the payment's real state after the provider call (`PENDING`, `REQUIRES_ACTION`, `UNKNOWN`, `FAILED` …). Never 5xx for a provider outcome |
| `GET /donation-intents/{donation_intent_id}/attempts` | Attempts of an intent | `{items, next_cursor: null}` |
| `GET /donation-intents/{donation_intent_id}/status` | Poll-friendly status | `{donation_intent_status, attempt: PaymentAttempt|null, message}` |
| `POST /donation-intents/{donation_intent_id}/cancel` | Cancel an OPEN intent | 409 `PAYMENT_IN_PROGRESS` / `PAYMENT_OUTCOME_UNKNOWN` if an attempt is live |
| `POST /payment-sessions/{session_id}/return` | Donor came back from a hosted page | Body `{token}` (single use). **Never confirms a payment**: marks the session `RETURNED`, schedules a status query, returns `{attempt}` |
| `GET /payment-methods?campaign_id=&currency=` | Methods currently available (public) | `{items:[{method, category, currency, simulated, label}]}`; empty when nothing is routable |
| `GET /payment-providers/capabilities` | Current capability rows of enabled providers (public) | No secrets, no account data; includes `simulated` |

`DonationIntent`: `{id, campaign_id, amount_minor, currency, status: OPEN|PAID|EXPIRED|CANCELLED, is_anonymous,
simulated, expires_at, created_at, updated_at, attempts?: PaymentAttempt[]}`.

`PaymentAttempt`: `{id, donation_intent_id, attempt_number, method, method_category, status, provider: {code,
display_name, simulated}, failure_code|null, message, next_action: {type: NONE|REDIRECT|AWAIT_HANDSET_APPROVAL|
SHOW_INSTRUCTIONS, session_id|null, redirect_url|null, instructions_code|null, expires_at|null}, created_at,
updated_at}`. `message` is donor-safe text (e.g. UNKNOWN: "We're confirming this payment with the provider. Please
don't pay again.").

## 3. Webhook ingestion (public, no session, no CSRF)

`POST /webhooks/payments/{provider}` — `{provider}` matches `^[a-z][a-z0-9_]{1,31}$`.

| Step | Rule | Failure |
|---|---|---|
| Rate limit | `payments.webhook.ip` (protective) | 429 + `Retry-After` |
| Provider | known and `ENABLED`; a sandbox provider only when the sandbox is enabled | 404 `WEBHOOK_PROVIDER_UNKNOWN` (rejection row) |
| Size / type | ≤ 64 KiB, `Content-Type: application/json` | 413 / 415 (rejection row) |
| Verify | adapter's documented procedure; sandbox: `FundZim-Sandbox-Signature: t=<unix>,kid=<current|previous>,v1=<hex HMAC-SHA256(secret, t + "." + body)>`, constant-time compare, replay window from the account (300 s) | 401 `WEBHOOK_SIGNATURE_INVALID` / `WEBHOOK_TIMESTAMP_INVALID` (rejection row, no payload stored) |
| Parse | adapter → events (`provider_event_id`, class, raw type, references) | 400 `WEBHOOK_MALFORMED` (rejection row) |
| Store | one tx: inbox row `ON CONFLICT (provider_id, provider_event_id) DO NOTHING` + River `psp.webhook_dispatch` | — |
| Ack | `200 {data:{received:true, duplicate:bool}}` | — |

Acknowledgement means "durably stored", not "applied". Duplicates are acknowledged and not reprocessed. Processing
failures retry with backoff (River) and end `DEAD_LETTERED` after 8 attempts.

## 4. Staff API (prefix `/admin/payments`)

| Method & path | Permission | Notes |
|---|---|---|
| `GET /overview` | `payment.view` | Counts only (no money totals): attempts by status, unknown, needs reconciliation, inbox by status, rejections (24 h), open refund requests, provider circuit states |
| `GET /intents` | `payment.view` | Donation intents; `?status&campaign_id&cursor` |
| `GET /intents/{donation_intent_id}` | `payment.view` | Intent + attempts + events |
| `GET /attempts` | `payment.view` | Payments; `?status&method&needs_reconciliation=true&cursor` |
| `GET /attempts/{payment_id}` | `payment.view` | Detail: payment, provider calls, provider transaction (raw + mapped), references, session (no token), webhooks (by our reference), events, reconciliation references, refund requests |
| `GET /unknown` | `payment.view` | `UNKNOWN` queue, oldest first |
| `POST /attempts/{payment_id}/status-query` | `payment.operate` **step-up** | Body `{reason}`; schedules an authenticated status query now. Never sets a status itself |
| `GET /webhooks` | `payment.view` | Inbox; `?status&cursor` |
| `GET /webhooks/{inbox_id}` | `payment.view` | Redacted payload, hash, verification facts, processing state |
| `POST /webhooks/{inbox_id}/requeue` | `payment.operate` **step-up** | Only `DEAD_LETTERED` or `UNRECOGNISED`; body `{reason}` |
| `GET /webhook-rejections` | `payment.view` | Rejections (no payloads) |
| `GET /providers` | `payment.view` | Providers, accounts (secret **reference names** + whether they resolve; never values), current capabilities (`routable`), circuit state, live-execution gate state |
| `GET /reconciliation` | `payment.view` | `?status&cursor` |
| `POST /reconciliation/{reference_id}/await-settlement` | `payment.operate` **step-up** | `OPEN → AWAITING_SETTLEMENT_DATA`, body `{note}` |
| `POST /attempts/{payment_id}/refund-requests` **IK** | `refund.request` | Body `{amount_minor, reason_code, reason_text}`; payment must be SUCCEEDED/PARTIALLY_REFUNDED; over-refund impossible (DB) |
| `GET /refund-requests` | `payment.view` | `?status&cursor` |
| `POST /refund-requests/{refund_request_id}/approve` | `refund.approve` **step-up** | maker ≠ checker (DB); body `{reason}`; result `APPROVED` with `execution: "DISABLED"` |
| `POST /refund-requests/{refund_request_id}/reject` | `refund.approve` **step-up** | body `{reason}` |
| `POST /refund-requests/{refund_request_id}/hold` / `resume` | `refund.approve` **step-up** | body `{reason}` |
| `POST /refund-requests/{refund_request_id}/withdraw` | `refund.request` (requester only) | body `{reason}` |
| `GET /sandbox/attempts/{payment_id}` | `payment.sandbox.control` | **Development only** (404 unless the sandbox is enabled). Simulator-side state |
| `POST /sandbox/attempts/{payment_id}/webhooks` | `payment.sandbox.control` | **Development only.** Body `{status: pending|action_required|paid|failed|expired|cancelled|limbo, delay_seconds?, copies?, amount_minor?, sign_with?: current|previous|wrong, timestamp_offset_seconds?}`; the simulator sends signed webhooks through the real endpoint |

There is **no** endpoint that sets a payment to `SUCCEEDED` or `FAILED` manually (brief §37).

## 5. Error codes (new)

`CAMPAIGN_NOT_ACCEPTING_DONATIONS` (422), `DONATION_NOT_AVAILABLE` (403, donor restricted; no reason given),
`CURRENCY_NOT_ACCEPTED` (422), `CURRENCY_NOT_AVAILABLE` (422, e.g. ZWG), `AMOUNT_OUT_OF_RANGE` (422),
`PAYMENT_METHOD_UNAVAILABLE` (422), `PAYMENT_EXECUTION_DISABLED` (503), `PAYMENTS_UNAVAILABLE` (503, sandbox off and
no live provider), `DONATION_INTENT_NOT_FOUND` (404), `DONATION_INTENT_NOT_OPEN` (409), `DONATION_ALREADY_PAID` (409),
`PAYMENT_IN_PROGRESS` (409), `PAYMENT_OUTCOME_UNKNOWN` (409), `PAYMENT_NOT_FOUND` (404), `PAYMENT_SESSION_INVALID`
(404/410), `ACCOUNT_KIND_NOT_ALLOWED` (403), `WEBHOOK_PROVIDER_UNKNOWN` (404), `WEBHOOK_SIGNATURE_INVALID` (401),
`WEBHOOK_TIMESTAMP_INVALID` (401), `WEBHOOK_MALFORMED` (400), `INBOX_EVENT_NOT_REQUEUEABLE` (409),
`REFUND_NOT_ALLOWED` (409), `REFUND_EXCEEDS_REFUNDABLE` (422), `REFUND_REQUEST_NOT_FOUND` (404),
`REFUND_SELF_APPROVAL_FORBIDDEN` (403), `RECONCILIATION_REFERENCE_NOT_FOUND` (404), `SANDBOX_DISABLED` (404).

## 6. Jobs (River, queue `default`)

| Kind | Inserted by | Does |
|---|---|---|
| `psp.webhook_dispatch` `{inbox_id}` | webhook ingestion (same tx) | lock inbox row, `→ DISPATCHED`, call the handler registered for the event class; on error `FAILED_RETRYING` (River retries, max 8), then `DEAD_LETTERED` |
| `payments.status_poll` `{payment_id}` | UNKNOWN entry, session return, staff request, sweep | `GET_STATUS` provider call (attempt row first), apply result through `applyEvent` (source `POLL`) |
| `payments.sweep` (periodic 15 s) | worker | in-flight call sweeper (deadline + 30 s ⇒ `OUTCOME_UNKNOWN / CRASH_DURING_CREATE`, payment `CREATED → UNKNOWN`), due polls, poll horizon ⇒ reconciliation reference, donation-intent expiry, parked-event deadline |
| `sandbox.deliver_webhook` `{…}` | sandbox simulator | signs and POSTs a webhook to the real endpoint (`SANDBOX_WEBHOOK_URL`) |

## 7. Outbox events (ids only, never amounts or donor identity)

`payments.donation_intent_created`, `payments.payment_status_changed {payment_id, donation_intent_id, from, to,
source, simulated}`, `payments.payment_succeeded {payment_id, donation_intent_id, campaign_id, posting_key:
"payment:{id}:capture", simulated}` (Stage 10 ledger consumer; never posted in Stage 8),
`payments.late_success`, `payments.reconciliation_required {payment_id, reason}`, `payments.refund_requested`,
`payments.refund_decided`.

## 8. Metrics (no ids in labels)

`payment_intents_created_total{simulated}`, `payment_attempts_total{method,outcome}`,
`payment_provider_calls_total{provider,operation,classification}`, `payment_webhooks_received_total{provider,duplicate}`,
`payment_webhooks_rejected_total{provider,reason}`, `payment_unknown_outcomes_total{provider,reason}`,
`payment_provider_errors_total{provider,error_class}`, `payment_reconciliation_required_total{reason}`,
`payment_processing_duration_seconds{stage}` (`create`, `webhook_ingest`, `webhook_apply`, `status_poll`),
`payment_events_not_applied_total{reason}`.

## 9. Web routes (staff UI, `apps/web`)

`/admin/payments` (overview), `/admin/payments/intents`, `/admin/payments/intents/[id]`, `/admin/payments/attempts`,
`/admin/payments/attempts/[id]`, `/admin/payments/unknown`, `/admin/payments/webhooks`, `/admin/payments/webhooks/[id]`,
`/admin/payments/providers`, `/admin/payments/reconciliation`, `/admin/payments/refunds`. Every page `noindex`, staff
only (404 otherwise), amounts formatted from minor-unit strings without floating point, simulated records labelled
**SIMULATED — DEVELOPMENT ONLY**. No donor checkout page; the public campaign button stays disabled.

## 10. Staff JSON shapes (binding for the web app and OpenAPI)

All money fields are `amount_minor` strings + `currency`. Timestamps RFC 3339. `provider` objects are
`{code, display_name, simulated}`.

- **Overview** `{attempts_by_status: {STATUS: n}, unknown_count, needs_reconciliation_count, inbox_by_status: {STATUS: n},
  rejections_24h, open_refund_requests, providers: [{code, display_name, simulated, status, circuit: CLOSED|OPEN|HALF_OPEN}],
  live_execution: {enabled: false, constant: false, kill_switch: false}, sandbox_enabled}` — counts only, never sums of money.
- **AdminIntent** `{id, campaign_id, donor_user_id, amount_minor, currency, status, is_anonymous, simulated, expires_at,
  created_at, updated_at}`; detail adds `attempts: AdminAttempt[]`, `events: Event[]`.
- **AdminAttempt** `{id, donation_intent_id, campaign_id, attempt_number, method, method_category, status, amount_minor,
  currency, provider, simulated, needs_reconciliation, unknown_since, unknown_reason, failure_code, created_at, updated_at}`;
  detail adds `provider_payment_ref, last_authoritative_source, poll_attempts, next_poll_at, poll_horizon_at,
  provider_calls: [{id, attempt_no, operation, classification, error_class, unknown_reason, http_status,
  provider_status_raw, provider_reference_returned, latency_ms, correlation_id, started_at, deadline_at, completed_at}],
  transaction: {provider_transaction_ref, raw_status, raw_status_at, mapped_status, mapping_version, last_source,
  reported_amount_minor, reported_currency, payer_instrument_masked, updated_at} | null, references: [{ref_kind, reference,
  source, recorded_at}], session: {id, kind, status, redirect_url, expires_at, returned_at} | null, webhooks: InboxItem[],
  events: Event[], reconciliation: Recon[], refund_requests: Refund[]`.
- **Event** `{id, subject_type, from_status, to_status, applied, not_applied_reason, transition_code, source,
  provider_event_id, raw_status, actor_type, reason, correlation_id, occurred_at, recorded_at}`.
- **InboxItem** `{id, provider, provider_event_id, event_class, event_type_raw, our_reference, provider_reference, status,
  attempts, last_error_code, simulated, signature_key_slot, verification_method, received_at, processed_at}`; detail adds
  `payload_redacted (object), raw_body_sha256 (hex), dispatched_to, parked_subject_id, parked_until_status, park_deadline_at,
  next_attempt_at, headers_subset (object)`.
- **Rejection** `{id, provider_code, reason_code, body_bytes, body_sha256 (hex|null), received_at}`.
- **Providers** `{items: [{code, display_name, simulated, status, selection_status, confirmation_mode, circuit,
  accounts: [{id, environment, label, status, api_credentials_secret_ref, webhook_secret_ref, webhook_secret_ref_previous,
  secrets_resolvable, webhook_replay_window_seconds}], capabilities: [{id, method, method_category, currency, custody_model,
  evidence_status, evidence_source, pcr_ref, routable, capability_version, flags: {refund_api, partial_refund,
  idempotent_create, supports_cancel, signed_webhooks, status_api, separate_capture, documented_expiry}}]}],
  live_execution: {enabled, constant, kill_switch}, sandbox_enabled}`.
- **Recon** `{id, payment_id, reason, status, expected_amount_minor, expected_currency, reported_amount_minor,
  reported_currency, provider_status_raw, provider_reference, evidence_at, created_at, resolved_at, resolution_note}`.
- **Refund** `{id, payment_id, amount_minor, currency, reason_code, reason_text, status, requested_by, requested_at,
  decided_by, decided_at, decision_reason, expires_at, execution: "DISABLED"}`.
- **SandboxState** `{provider_payment_ref, our_reference, scenario, raw_status, amount_minor, currency, method,
  created_at, updated_at, simulated: true}`.
