# Stage 8 — Payment architecture

How the `psp` and `payments` modules fit together as built. Decisions: [ADR-039](../adr/ADR-039-payment-abstraction-donation-intents-and-sandbox-execution.md).
Contracts: [interface-contracts.md](interface-contracts.md). Stage 2 designs this implements:
[payment-provider-interfaces.md](../architecture/payment-provider-interfaces.md),
[payment-state-machine.md](../architecture/payment-state-machine.md), [payment-schema.md](../database/payment-schema.md).
Related Stage 8 documents: [provider-interface.md](provider-interface.md) ·
[payment-state-machine.md](payment-state-machine.md) · [webhook-security.md](webhook-security.md) ·
[idempotency.md](idempotency.md) · [donation-intents.md](donation-intents.md).

Everything below runs against the **sandbox simulator only**. No real provider is registered and real payment
execution is impossible (§9).

## 1. Modules and boundaries

| Module | Package | Imports (enforced by `internal/archtest`) | Owns (tables, all in schema `app`) | Does |
|---|---|---|---|---|
| `psp` | `internal/psp`, `internal/psp/sandbox` | platform, audit | `payment_providers`, `provider_capabilities` (+ view `provider_capabilities_current`), `provider_accounts`, `provider_webhook_inbox`, `provider_webhook_rejections`, `provider_health_events`, `sandbox_provider_payments`, `sandbox_scheduled_webhooks` | Registry and capabilities, accounts with secret **references**, `SecretResolver`, adapter interfaces, router, webhook ingestion (verify → inbox → dispatch), rejection log, health and circuit state, the sandbox simulator |
| `payments` | `internal/payments` | platform, audit, campaigns, risk, psp | `donation_intents`, `payment_intents`, `payment_attempts`, `payment_transactions`, `payment_provider_references`, `payment_sessions`, `payment_events`, `refund_requests`, `payment_reconciliation_references` | Donation intents, payments ("payment attempts" in the API), provider calls, `apply`, polling and sweep, sessions, refund **requests**, reconciliation references, donor and staff HTTP handlers |

Boundary rules:

- `psp` never imports `payments`. At composition time (`internal/app/payments.go`) `payments` registers its webhook
  handler with `psp.Service.Handle("PAYMENT", payments.ApplyWebhook)`; the handler finishes the inbox row through
  `psp.MarkProcessed` / `Park` / `MarkUnrecognised` in its own transaction.
- `payments` reads compliance restrictions through the `DonorRestrictions` interface, implemented in `internal/app`
  over `compliance.Service.Restrictions` (fail closed). ADR-039 §1 lists `compliance` among the imports; the code
  reaches it only through that interface, so the archtest graph has no `payments → compliance` edge.
- Campaign eligibility comes from `campaigns.Service.EvaluateDonation` (action `ACCEPT_DONATION`); campaigns still
  hold **no financial state**.
- Approved money limits come from `risk.Service.MoneyLimits` (`risk.limits`, approved rows only).
- No module writes another module's tables. No `ledger`, `fees` or `recon` tables exist.

## 2. Glossary (brief ↔ Stage 2 ↔ API ↔ code)

| Brief | Stage 2 / table | API resource | Go |
|---|---|---|---|
| DonationIntent | `donation_intents` (new in Stage 8, ADR-039 §2) | `/donation-intents` | `payments.DonationIntent`, `donationRow` |
| PaymentAttempt (donor retry) | `payment_intents` — the canonical payment; `id` = `payment_id` = our provider reference and idempotency key | `/donation-intents/{id}/attempts`; staff `/admin/payments/attempts` | `payments.PaymentAttempt`, `payRow` |
| Provider request (one outbound call) | `payment_attempts` (committed `IN_FLIGHT` before the call) | `provider_calls` in attempt detail | `insertCall`, `completeCall` |
| Provider-side record | `payment_transactions` (latest raw + mapped status) | `transaction` in attempt detail | `recordTransaction` |
| PaymentProvider / PaymentMethod | `payment_providers`, `provider_capabilities` | `/payment-providers/capabilities`, `/payment-methods` | `psp.Provider`, `psp.Capability`, `psp.Adapter` |
| PaymentSession | `payment_sessions` | `next_action` + `/payment-sessions/{id}/return` | `openSession`, `ReturnFromSession` |
| ProviderEvent | `provider_webhook_inbox` (+ `provider_webhook_rejections`) | `/admin/payments/webhooks`, `/webhook-rejections` | `psp.InboxEvent`, `psp.WebhookEvent` |
| PaymentEvent | `payment_events` (append-only; subjects `DONATION_INTENT`, `PAYMENT_INTENT`, `REFUND_REQUEST`) | `events` in detail views | `insertEvent`, `notApplied` |
| PaymentReconciliationReference | `payment_reconciliation_references` | `/admin/payments/reconciliation` | `openRecon` |
| RefundRequest | `refund_requests` | `/admin/payments/refund-requests` | `RequestRefund`, `DecideRefund` |
| PaymentAuditRecord | `audit.audit_events` (actions `payments.*`) | — | `audit.Record` |

"Payment attempt" (API) and `payment_attempts` (table) mean different things: the API word is a donor's payment,
the table row is one provider call. The column `payment_intents.attempt_seq` is the API's `attempt_number`.

## 3. Data model

Migrations: `20261012090000_psp_registry_and_inbox.sql` (psp) and `20261012090100_payments.sql` (payments). Both are
ports of the Stage 2 drafts (`design/sql/0007_fees_psp.sql`, `0015_payments.sql`) without the deferred objects
(fees, `donations`, `refund_transactions`, disputes, chargebacks — [implementation.md](implementation.md) §8).

| Table | Key guarantees |
|---|---|
| `payment_providers` | `code` unique and immutable; `is_sandbox` immutable and `= (code LIKE 'sandbox%')`; a non-sandbox row can be `ENABLED` only with `selection_status = 'SELECTED'` and `selection_adr ~ '^ADR-[0-9]{3}$'`; a sandbox is never `SELECTED`; no delete. Seeded: `sandbox` only |
| `provider_capabilities` | Append-only, versioned per (provider, method, currency); `routable` is **generated** (VERIFIED, not `MERCHANT_SETTLEMENT`, an authoritative status path); `settlement_currency = currency`; `UNIQUE (id, provider_id, method, currency, custody_model, routable)` is the FK target that pins every payment to a routable row |
| `provider_accounts` | Secret columns accept only `secretref://…` (CHECK); a sandbox provider cannot have a `PRODUCTION` account (trigger); replay window 0–3600 s; no delete |
| `provider_webhook_inbox` | `UNIQUE (provider_id, provider_event_id)`; verified-only (CHECK); raw hash and redacted payload immutable; status machine `provider_webhook_inbox`; no delete or truncate |
| `provider_webhook_rejections` | Append-only; no payload, no headers; reason code from a closed list |
| `provider_health_events` | Append-only call outcomes and circuit events |
| `sandbox_provider_payments`, `sandbox_scheduled_webhooks` | The simulator's "provider side" (CHECK `is_sandbox`) |
| `donation_intents` | `UNIQUE (donor_user_id, idempotency_key)`; amount, currency, campaign, donor, anonymity and simulation flag immutable; status machine `donation_intent`; every status change needs a `payment_events` row in the same transaction (deferred trigger); no delete |
| `payment_intents` | Composite FK to its donation intent (same campaign, currency, amount, `is_simulated`); FK `(provider_id, is_simulated) → payment_providers (id, is_sandbox)`; FK to a **routable** capability; `UNIQUE (donation_intent_id, idempotency_key)`, `UNIQUE (donation_intent_id, attempt_seq)`, `UNIQUE (provider_id, provider_payment_ref)`; status machine `payment_intent` (42 edges); `status_rank` kept equal to `app.payment_status_rank(status)`; event required per change; immutable money fields; no delete or truncate |
| `payment_attempts` | `UNIQUE (payment_intent_id, attempt_no)`; a completed call cannot change (`app.attempt_complete_once`); no delete |
| `payment_transactions` | One per payment; `UNIQUE (provider_id, provider_transaction_ref)`; raw status verbatim; payer instrument only as HMAC fingerprint and masked value |
| `payment_provider_references` | Append-only; `UNIQUE (provider_id, ref_kind, reference)` |
| `payment_sessions` | One per payment; return token stored as a hash only (`UNIQUE`); status machine `payment_session` |
| `payment_events` | Append-only (no update, delete, truncate); source/target CHECKs (success only from provider sources, `UNKNOWN` only `INTERNAL`, `FAILED` restricted, `PAID` only `INTERNAL`); partial `UNIQUE (payment_intent_id, provider_event_id) WHERE applied` |
| `refund_requests` | Maker ≠ checker (CHECK); over-refund trigger; status machine `refund_request` **without execution edges**; no delete |
| `payment_reconciliation_references` | One open reference per (payment, reason) (partial unique index); statuses `OPEN`, `AWAITING_SETTLEMENT_DATA`, `RESOLVED_BY_PROVIDER_STATUS`; no "reconciled" state |

Grants: `app.apply_runtime_grants()` gives `fundzim_app` `SELECT, INSERT` everywhere in `app`, `UPDATE` only on tables
without a `forbid_mutation` update trigger, and `DELETE` on none of these tables. New permissions `payment.view`,
`payment.operate` (step-up) and `payment.sandbox.control` (ADMIN only); `refund.request` and `refund.approve`
(step-up) exist since Stage 4.

## 4. One payment attempt: tx1 → provider call → tx2

`payments.CreateAttempt` (payment-provider-interfaces §6). Validation and routing happen **before** anything is
written; a refusal is a 4xx and stores nothing.

```mermaid
sequenceDiagram
    autonumber
    actor D as Donor (browser)
    participant API as payments (API)
    participant DB as PostgreSQL
    participant R as psp router
    participant A as adapter (sandbox)
    D->>API: POST /donation-intents/{id}/attempts (Idempotency-Key)
    API->>API: idempotency middleware (REQUIRED); key backstop lookup
    API->>API: donor restriction, ACCEPT_DONATION, currency, limits
    API->>R: Route(method, currency, amount, simulated)
    R-->>API: provider, account, routable PSP_POOL capability, adapter
    API->>DB: tx1: lock donation intent FOR UPDATE; refuse if PAID / not OPEN / attempt live, UNKNOWN or paid;<br/>INSERT payment CREATED + event P1 (DONOR) + provider call CREATE IN_FLIGHT + audit; COMMIT
    API->>A: CreatePayment(payment_id = our reference = idempotency key), deadline 20 s, outside any tx
    A-->>API: Observation (class, raw + canonical status, provider ref, next action)
    API->>DB: tx2 (runs even if the client went away): lock payment; complete the call (once);<br/>apply(observation); open a session if the donor must act; COMMIT
    API-->>D: 201 PaymentAttempt with the real status (PENDING, REQUIRES_ACTION, UNKNOWN, FAILED …)
```

- A crash between tx1 and tx2 leaves the call `IN_FLIGHT`; the sweeper marks it `OUTCOME_UNKNOWN /
  CRASH_DURING_CREATE` after deadline + 30 s and moves a `CREATED` payment to `UNKNOWN` (never `FAILED`).
- If tx2 itself fails, the response reports `UNKNOWN` and the same sweeper resolves it. A provider outcome is never
  a 5xx.
- The single-use return token and session id are generated before the call so the provider's return URL
  (`PUBLIC_SITE_URL/donations/return?session=…&token=…`) can carry them; only the token's hash is stored, and only
  when the provider asks the donor to act.

## 5. Webhook pipeline

| Step | Where | What |
|---|---|---|
| 1 | Route `POST /api/v1/webhooks/payments/{provider}` | Public, CSRF-exempt by path, per-IP rate limit `payments.webhook.ip` |
| 2 | `psp.HandleWebhook` | Provider known, `ENABLED` and usable (sandbox only when enabled); `application/json`; ≤ 64 KiB; account resolved |
| 3 | `Adapter.Verify` | Provider's documented procedure (sandbox: HMAC-SHA256, key slot, replay window). Failure → 401, a rejection row (no payload), nothing else |
| 4 | `Adapter.Parse` | Event id, class, raw type and status, references, reported amount; simulation flag must equal the provider's `is_sandbox` |
| 5 | One transaction | `INSERT … ON CONFLICT (provider_id, provider_event_id) DO NOTHING` into the inbox **and** outbox event `psp.webhook_received {inbox_id}` |
| 6 | Response | `200 {received: true, duplicate}` — "durably stored", not "applied" |
| 7 | Worker: outbox consumer `psp.webhook_dispatch` (`psp.Dispatch`) | Lock row; `RECEIVED/FAILED_RETRYING → DISPATCHED`; class `OTHER` → `UNRECOGNISED`; call the `PAYMENT` handler |
| 8 | `payments.ApplyWebhook` | One transaction: find the payment by our reference (or the provider reference), lock it, map the raw status, `apply`, then `MarkProcessed`, `Park` or `MarkUnrecognised` |
| 9 | Failure | Handler error → `FAILED_RETRYING`; the outbox delivery retries with backoff (5 s → 1 h, jittered); attempt 8 → `DEAD_LETTERED` (staff re-queue only, audited, step-up) |

ADR-039 §9 and interface-contracts §3/§6 describe step 5 as "inbox row + River job `psp.webhook_dispatch` in one
transaction". The implementation uses the transactional outbox instead (the API process has no River client); the
durability guarantee is the same (inbox row and work item commit together). Details:
[webhook-security.md](webhook-security.md).

## 6. Background work

| Kind | Type | Schedule | Does |
|---|---|---|---|
| `psp.webhook_dispatch` | Outbox consumer of `psp.webhook_received` | Per event; max 8 deliveries, backoff 5 s → 1 h | §5 steps 7–9 |
| `payments.sweep` | River periodic job (unique per 5 s) | Every 5 s | (1) in-flight calls past deadline + 30 s → `OUTCOME_UNKNOWN / CRASH_DURING_CREATE` (and `CREATED → UNKNOWN` for a create call); (2) up to 25 payments with `next_poll_at` due in `CREATED`, `UNKNOWN`, `PENDING`, `REQUIRES_ACTION`, `AUTHORISED` → `PollPayment`; (3) expire `OPEN` donation intents past `expires_at` with no live, unknown or paid payment; (4) parked inbox events past their deadline → reconciliation reference |
| `sandbox.deliver_webhooks` | River periodic job, **only when the sandbox runs** | Every 1 s, batch 50 | Signs and POSTs due simulated webhooks to `SANDBOX_WEBHOOK_URL` ([provider-interface.md](provider-interface.md) §6.6) |

There is no separate `payments.status_poll` job (contracts §6 lists one, and the sweep at 15 s): polls run inside
the 5 s sweep, driven by `payment_intents.next_poll_at`, and immediately for a staff status query or a donor return
(`next_poll_at = now`). Poll cadence: `UNKNOWN` first poll after 3 s; then 5 s doubling per poll, capped at 10 min;
each poll takes a 40 s lease on `next_poll_at` (status deadline 10 s + grace 30 s).

## 7. Outbox events

Payloads carry ids, statuses and flags only — never amounts or donor identity (contracts §7).

| Event | Emitted when | Payload |
|---|---|---|
| `payments.donation_intent_created` | Intent stored | `donation_intent_id, campaign_id, simulated` |
| `payments.payment_status_changed` | Every applied payment transition | `payment_id, donation_intent_id, from, to, source, simulated` |
| `payments.payment_succeeded` | Transition to `SUCCEEDED` (not `DISPUTED → SUCCEEDED`) | `payment_id, donation_intent_id, campaign_id, posting_key: "payment:{id}:capture", simulated` — the Stage 10 ledger consumer's input; **never posted in Stage 8** |
| `payments.late_success` | `FAILED`/`EXPIRED`/`CANCELLED → SUCCEEDED` (P15) | `payment_id, from, simulated` |
| `payments.reconciliation_required` | A new reconciliation reference opens | `payment_id, reason, simulated` |
| `payments.refund_requested` | Refund request stored | `refund_request_id, payment_id, simulated` |
| `payments.refund_decided` | Approved or rejected | `refund_request_id, payment_id, decision, execution: "DISABLED"` |
| `psp.webhook_received` | Verified webhook stored (or re-dispatched) | `inbox_id` |

No Stage 8 consumer subscribes to the `payments.*` events; they wait for Stage 10 (ledger) and later stages.

## 8. Model A guard (ADR-013)

Donor funds must never settle to a FundZim account. Enforced three times:

1. **Router** (`psp.Service.Route`): only capabilities with `routable = true` **and** `custody_model = 'PSP_POOL'`
   are candidates. `MERCHANT_SETTLEMENT` (Model B in substance) is never routable; `SPLIT_DIRECT` (Model C) is not
   routed in Stage 8 because no campaign has a Model C settlement model.
2. **Generated column**: `provider_capabilities.routable` is false for `MERCHANT_SETTLEMENT` and for any evidence
   other than `VERIFIED`.
3. **Composite FK**: a payment references `(capability_id, provider_id, method, currency, custody_model,
   capability_routable)` of a capability row, and the payment stores `capability_routable` — a payment on a
   non-routable row is refused by the database (`TestPaymentsDatabaseInvariants`).

The sandbox seeds a `MERCHANT_SETTLEMENT` row (`BANK_TRANSFER`) and an `UNVERIFIED` row (`INNBUCKS`) only so tests
prove both refusals ([provider-capability-register.md](provider-capability-register.md) §2).

## 9. Execution gates (ADR-039 §6)

| # | Gate | Where | Effect today |
|---|---|---|---|
| 1 | `psp.LiveExecutionEnabled` is a Go **constant `false`** | `internal/psp/psp.go` | `Usable` refuses every non-sandbox provider (`PAYMENT_EXECUTION_DISABLED`); changing it needs a code change and an ADR |
| 2 | Kill switch `payments.live_execution.enabled` (`KILL_SWITCH`, seeded off, maker-checker) | `app.feature_flags` | Checked only if gate 1 were true |
| 3 | A real provider can be `ENABLED` only when `SELECTED` under an ADR | DB CHECK | No real provider row exists |
| 4 | Sandbox only with `PAYMENTS_SANDBOX_ENABLED=true` **and** `APP_ENV ≠ production` | Config loading refuses it in production; the DB refuses a `PRODUCTION` account for a sandbox provider; the adapter is registered only when enabled | Without the sandbox, donation intents return `PAYMENTS_UNAVAILABLE` |
| 5 | Every simulated record is marked | `is_sandbox`, `is_simulated` pinned by composite FKs; webhook `simulated && !livemode` must match the provider | A simulated payment cannot be mistaken for a real one |
| 6 | No donor checkout | Public campaign button stays disabled; no donor payment pages | — |

The staff providers view shows the gate state (`live_execution: {enabled, constant, kill_switch}`), and
`TestLiveExecutionIsDisabled` fails if the constant changes.

## 10. Metrics (no ids in labels)

`payment_intents_created_total{simulated}`, `payment_attempts_total{method,outcome}`,
`payment_provider_calls_total{provider,operation,classification}`, `payment_provider_errors_total{provider,error_class}`,
`payment_webhooks_received_total{provider,duplicate}`, `payment_webhooks_rejected_total{provider,reason}`,
`payment_unknown_outcomes_total{provider,reason}` (here `provider` is `sandbox` or `real`, not the provider code),
`payment_reconciliation_required_total{reason}`, `payment_events_not_applied_total{reason}`,
`payment_processing_duration_seconds{stage}` (`create`, `webhook_ingest`, `webhook_apply`, `status_poll`). Alert
conditions are also logged as `WARN`/`ERROR` lines containing `ALERT` (unmapped status, amount mismatch, refused
redirect, unrecognised event, dead letter); no alert routing is configured in the repository.
