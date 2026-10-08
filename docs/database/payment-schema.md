# Payment Schema (fees, psp, payments)

> **Status:** Stage 2 design. Nothing here is implemented. The DDL is a **non-executable draft**:
> [`design/sql/0007_fees_psp.sql`](../../design/sql/0007_fees_psp.sql) and
> [`design/sql/0015_payments.sql`](../../design/sql/0015_payments.sql), validated only in in-memory PGlite
> ([design/sql/README.md](../../design/sql/README.md)). Invariant tests:
> [`design/sql/tests/payments_test.sql`](../../design/sql/tests/payments_test.sql) (100 cases).
> Executable migrations are written in Stage 3+ (goose, ADR-028) from these drafts. **No provider is selected
> or integrated**; the only provider in fixtures is the sandbox.

Contract: [design-baseline.md](../stage-2/design-baseline.md) §5.7, §5.8, §5.16, §7, §8. State machines:
[payment-state-machine.md](../architecture/payment-state-machine.md). Provider interfaces:
[payment-provider-interfaces.md](../architecture/payment-provider-interfaces.md). Payout tables:
[payout-schema.md](payout-schema.md).

---

## 1. Ownership and boundaries

| Module | Tables (schema `app`) | Writes | Reads from other modules |
|---|---|---|---|
| `fees` | `fee_schedules`, `fee_schedule_versions`, `fee_schedule_change_requests` | fees only | — |
| `psp` | `payment_providers`, `provider_capabilities` (+ view `provider_capabilities_current`), `provider_accounts`, `provider_webhook_inbox`, `provider_health_events` | psp only | — |
| `payments` | `donations`, `payment_intents`, `payment_attempts`, `payment_transactions`, `payment_provider_references`, `payment_events`, `refund_requests`, `refund_transactions`, `payment_disputes`, `chargebacks` | payments only | campaigns, fees, risk, ledger, psp — **through their Go interfaces** |

**Foreign keys into other modules' tables are declared references, never queries.** They give referential
integrity (`app.campaigns`, `app.users`, `app.currencies`, psp tables, `ledger.ledger_transactions`,
`risk.holds`, `audit.evidence_records`). No trigger in 0015 reads a table owned by another module
(CLAUDE.md "a module's SQL may reference only tables it owns"). The Model A routing guard is therefore a
**composite FK** to the capability row (§3.3), not a trigger. `payments` never references `payouts` tables
and vice versa (baseline §3); `compliance_case_id` is stored by id only.

## 2. Fees

### 2.1 `fee_schedules`

| Column | Type | Constraint |
|---|---|---|
| `id` | uuid | PK |
| `code` | text | UNIQUE, `^[a-z][a-z0-9_.]{2,63}$` |
| `name` | text | NOT NULL |
| `fee_kind` | text | `PLATFORM_FEE` only (PSP fees are reported by providers, not configured) |
| `status` | text | `ACTIVE` \| `RETIRED` |
| `version`, `created_at`, `updated_at` | | `app.set_updated_at`, `app.keep_created_at` |

### 2.2 `fee_schedule_change_requests` (maker-checker; machine `fee_schedule_change_request`)

Proposal columns (`currency`, `proposed_rate_bps` 0–10000, `proposed_fixed_minor` ≥ 0, `proposed_min_fee_minor`,
`proposed_max_fee_minor` ≥ min, `proposed_rounding_mode` `HALF_UP|FLOOR|CEIL`, `proposed_effective_from`),
`justification`, `requested_by`, `expires_at` (7 d policy), `decided_by`, `decided_at`, `decision_reason`,
`decided_step_up_at`, `proposal_hash` (what the checker saw).

| Constraint | Rule |
|---|---|
| `ck_…_maker_checker` | `decided_by IS NULL OR decided_by <> requested_by` |
| `ck_…_future` | `proposed_effective_from > requested_at` (never retroactive) |
| `ck_…_decision` / `_step_up` | APPROVED/REJECTED carry decider, time, reason; APPROVED carries step-up time |
| Machine | `'' → PENDING → APPROVED \| REJECTED \| EXPIRED \| WITHDRAWN` |

### 2.3 `fee_schedule_versions` (immutable once effective)

`fee_schedule_id`, `version_no`, `currency`, `rate_bps` (integer bps), `fixed_minor`, `min_fee_minor`,
`max_fee_minor`, `rounding_mode`, `effective_from`, `change_request_id` (UNIQUE), `cancelled_at`, `cancelled_by`.

- Trigger `app.fee_schedule_versions_guard`: on INSERT `effective_from > now()`, the change request is
  `APPROVED` and **every value equals the approved proposal**; on UPDATE only a one-time cancellation of a
  version that is **not yet effective**; DELETE/TRUNCATE forbidden.
- `uq_fee_schedule_versions_effective (fee_schedule_id, currency, effective_from) WHERE cancelled_at IS NULL`.
- Resolution at time *t*: the non-cancelled version with the greatest `effective_from ≤ t`. Every intent stores
  the `fee_schedule_version_id` used (MONEY §5), so historical fees stay explainable.

## 3. PSP registry

### 3.1 `payment_providers`

`code` (adapter id, e.g. `sandbox`), `display_name`, `is_sandbox`, `selection_status` (`PENDING|SELECTED|REJECTED`,
default PENDING), `status` (`DISABLED|ENABLED|RETIRED`), `confirmation_mode`
(`WEBHOOK_SIGNED|WEBHOOK_UNSIGNED_VERIFY_BY_API|POLL_ONLY`), `selection_adr`.

- `ck_payment_providers_enabled_requires_selection`: a real provider is `ENABLED` only when
  `selection_status = SELECTED` with an ADR id (Stage 9). Candidate providers from
  [provider-comparison.md](../payments/provider-comparison.md) may be registered only as `PENDING`/`DISABLED`.
- The sandbox is never "selected" and can never have a PRODUCTION account (trigger on `provider_accounts`).

### 3.2 `provider_capabilities` (versioned, append-only)

One row = one evidence-backed statement about (provider, `method`, `currency`). A change is a new row with a higher
`capability_version`; the router reads view `provider_capabilities_current` (highest version per key).

| Column | Notes |
|---|---|
| `method` | `ECOCASH, ONEMONEY, INNBUCKS, OMARI, ZIMSWITCH, BANK_TRANSFER, CARD` |
| `currency`, `settlement_currency` | `ck_…_same_currency_settlement`: **must be equal** (ADR-018; a rail that cannot settle in the donation currency is simply not recorded, hence not offered) |
| `custody_model` | `PSP_POOL \| MERCHANT_SETTLEMENT \| SPLIT_DIRECT` (operating-model-decision §9) |
| flags | `separate_capture, refund_api, partial_refund, idempotent_create, idempotent_refund, supports_cancel, payout_api, payout_rails[], split_settlement, pool_balance_api, signed_webhooks, status_api, documented_expiry (+ seconds)` — all default **false = absent** |
| `statement_format` | `API, FILE_CSV, FILE_XLSX, FILE_PDF, DASHBOARD_EXPORT, SFTP, NONE, UNKNOWN` |
| `evidence_status` | `VERIFIED \| UNVERIFIED \| NOT_SUPPORTED \| REQUIRES_PROVIDER_CONFIRMATION` |
| `evidence_source`, `pcr_ref`, `flag_evidence` | VERIFIED needs a source; RPC needs `PCR-nnn`; `flag_evidence` (object) keeps per-flag claims visible but inert |
| `routable` | **GENERATED** `evidence_status = 'VERIFIED' AND custody_model IN ('PSP_POOL','SPLIT_DIRECT') AND (signed_webhooks OR status_api)` |

Other checks: `partial_refund ⇒ refund_api`; `payout_api ⇔ payout_rails non-empty`; NOT_SUPPORTED rows assert
no flags. `uq_provider_capabilities_routing_key (id, provider_id, method, currency, custody_model, routable)` and
`uq_provider_capabilities_payout_key (id, provider_id, currency, routable, payout_api)` are the targets of the
pinning FKs below. Append-only (`app.forbid_mutation`).

### 3.3 Model A routing guard in the database

`payment_intents` stores the routing facts and references the capability with a composite FK:

```sql
CONSTRAINT fk_payment_intents_capability
  FOREIGN KEY (capability_id, provider_id, method, currency, custody_model, capability_routable)
  REFERENCES app.provider_capabilities (id, provider_id, method, currency, custody_model, routable),
CONSTRAINT ck_payment_intents_routable       CHECK (capability_routable),
CONSTRAINT ck_payment_intents_custody_model  CHECK (custody_model IN ('PSP_POOL','SPLIT_DIRECT'))
```

So an intent can exist only on a routable capability whose provider, method and currency match — a
`MERCHANT_SETTLEMENT` or unverified capability can never be referenced (tests
`intent_on_merchant_settlement_capability_rejected`, `intent_on_unverified_capability_rejected`). `SPLIT_DIRECT`
is permitted only for `campaigns.settlement_model = MODEL_C` — an application rule in the router (the campaign
column belongs to `campaigns`).

### 3.4 `provider_accounts` — secret references only

`environment` (`SANDBOX|STAGING|PRODUCTION`), `account_label`, `merchant_reference` (non-secret),
`api_credentials_secret_ref`, `webhook_secret_ref`, `webhook_secret_ref_previous` (rotation overlap),
`webhook_replay_window_seconds`. Every `*_secret_ref` must match `^secretref://[a-z0-9][a-z0-9/_.-]{2,200}$`,
so a pasted key (`sk_live_…`) is rejected (test `provider_account_rejects_raw_secret`). Secret values live only in
the secret manager.

### 3.5 `provider_webhook_inbox`

One endpoint per provider receives payment, refund, dispute and payout events (baseline §5.8).

| Group | Columns | Mutability |
|---|---|---|
| Raw | `provider_id, provider_account_id, provider_event_id, event_class (PAYMENT\|REFUND\|DISPUTE\|PAYOUT\|OTHER), event_type_raw, provider_reference, our_reference, provider_occurred_at, received_at, signature_verified, verification_method (SIGNATURE\|STATUS_API_CONFIRM), signature_key_slot, payload_redacted, raw_body_sha256, raw_object_id, headers_subset, created_at` | **Immutable** (trigger `app.provider_webhook_inbox_raw_immutable`) |
| Processing | `status, dispatched_to, attempts, next_attempt_at, last_error_code, parked_subject_id, parked_until_status, parked_at, park_deadline_at, processed_at, version, updated_at` | Mutable; `status` guarded |

- `uq_provider_webhook_inbox_event (provider_id, provider_event_id)` — dedupe; duplicates get 2xx and are dropped.
- Only verified events are stored. Unsigned providers store `signature_verified=false` **only** with
  `verification_method = STATUS_API_CONFIRM` (`ck_…_verified`); the event changes nothing until an authenticated
  status query confirms it.
- Processing machine `provider_webhook_inbox`:
  `'' → RECEIVED → DISPATCHED → PROCESSED | PARKED | FAILED_RETRYING | UNRECOGNISED`;
  `RECEIVED → UNRECOGNISED | FAILED_RETRYING`; `PARKED → DISPATCHED`; `FAILED_RETRYING → DISPATCHED | DEAD_LETTERED`;
  `DEAD_LETTERED → DISPATCHED` (audited re-queue); `UNRECOGNISED → DISPATCHED` (after the mapping is extended).
  **No silent drop:** an `OTHER`/unmapped event type, or a raw status the adapter cannot map, is kept as
  `UNRECOGNISED` and raises an alert (ALR-W03; ADR-024). `PARKED` needs subject, awaited status and deadline.
  DELETE/TRUNCATE forbidden.
- Design choice: one table with an immutable raw column group (DATABASE §9 allows "processing-status columns
  may be updated") rather than a sibling table, to keep the baseline §5.8 catalogue unchanged. Stage 3 should
  narrow `0018` grants to column-level UPDATE on the processing group (today grants are table-level; the trigger
  enforces immutability).

### 3.6 `provider_health_events` (append-only)

`operation` (`CREATE_PAYMENT … STATEMENT, ALL`), `method`, `currency`, `event_kind` (`CALL_OK, CALL_ERROR, TIMEOUT,
CIRCUIT_OPENED, CIRCUIT_HALF_OPEN, CIRCUIT_CLOSED, OUTAGE_DECLARED, OUTAGE_RESOLVED, SETTLEMENT_RECONCILED, RECON_SEV1_OPENED, RECON_SEV1_CLOSED`),
`error_class`, `latency_ms`, `actor_id` (required for staff-declared outages), `observed_at`. Circuit state for
EC-18 = latest `CIRCUIT_*` row per (provider, operation, currency). **Settlement freshness for EC-20** = latest
`SETTLEMENT_RECONCILED` per (provider, currency) and no `RECON_SEV1_OPENED` without a later `RECON_SEV1_CLOSED`;
these rows are written by `reconciliation` through the psp interface (`ck_provider_health_events_recon`: currency
required, operation `STATEMENT`) and read by `payouts` through the same interface. Index
`ix_provider_health_events_latest`.

## 4. Payments tables

### 4.1 `payment_intents` (canonical payment; `payment_id = id`)

| Column | Type | Notes |
|---|---|---|
| `campaign_id`, `currency`, `amount_minor`, `method` | | **Immutable** (trigger); `amount_minor > 0` |
| `provider_id`, `provider_account_id`, `capability_id`, `custody_model`, `capability_routable`, `routing_reason` | | Pinned by composite FKs (§3.3). Re-binding allowed only while `CREATED` and no `CREATE` attempt other than `DEFINITELY_NOT_SENT` exists |
| `platform_fee_minor`, `fee_schedule_version_id` | bigint, uuid | `0 ≤ fee ≤ amount` |
| `status`, `status_rank` | text, smallint | 13 states; `ck_…_status_rank`: `status_rank = app.payment_status_rank(status)` |
| `provider_payment_ref` | text | `UNIQUE (provider_id, provider_payment_ref)` (NULLs distinct); set once |
| `last_authoritative_source` | text | `SYNC, WEBHOOK, POLL, RECON, INTERNAL, STAFF` |
| `unknown_since`, `unknown_reason` | | Required in UNKNOWN. Reasons: `TIMEOUT, CONN_RESET_AFTER_SEND, HTTP_5XX, MALFORMED_RESPONSE, SIGNATURE_INVALID_RESPONSE, CRASH_DURING_CREATE` |
| `next_poll_at`, `poll_attempts`, `poll_horizon_at`, `needs_reconciliation` | | Poll schedule (transaction-lifecycle §7.3) |
| `reversal_kind` | text | `CARD_CHARGEBACK \| PROVIDER_REVERSAL`; set **iff** `CHARGED_BACK` |
| `possible_duplicate_of` | uuid | self-FK (§8 duplicate rule) |
| `expires_at` | timestamptz | documented hard expiry only |
| `idempotency_scope`, `idempotency_key` | | `UNIQUE` backstop to `app.idempotency_keys` (which expires) |
| `version`, timestamps | | |

Other checks: `AUTHORISED ⇒ method = CARD` (separate capture). Uniques `(id, campaign_id)`, `(id, campaign_id,
currency)`, `(id, provider_id)` exist only as composite-FK targets. Indexes: `(campaign_id, status)`; partial poll
queue on `next_poll_at`; partial `needs_reconciliation`.

### 4.2 `donations` (1:1 with intent)

`payment_intent_id` UNIQUE, `campaign_id` (composite FK to the intent's campaign), `donor_user_id` or guest
contact (`guest_name`, `guest_email`, `guest_phone_e164` — **C2**), `is_anonymous`, `display_name` (≤ 80),
`message` (≤ 500) + `message_status`, `receipt_number` (random Crockford, UNIQUE), `terms_version`,
`privacy_notice_version`, `marketing_consent`, `access_token_hash` (baseline §12 **I-2**: SHA-256 of the guest
donation access token, shown once; UNIQUE; required for guests by `ck_donations_guest_token`; replaced — i.e. the previous token revoked — when an
idempotent guest replay re-issues it, I-8; grants
read-only access to this donation and receipt and the right to request a refund). Exactly one of user or guest
contact. No money columns (single
source: the intent). Identity, receipt and accepted terms immutable; DELETE forbidden (erasure = anonymisation,
DATABASE §9).

### 4.3 `payment_attempts`

Every outbound call (`CREATE, GET_STATUS, CANCEL, CAPTURE`), inserted **IN_FLIGHT and committed before the call**,
completed once (generic trigger `app.attempt_complete_once`). `our_reference = payment_intent_id` (CHECK — the
reference never changes). `classification`: `IN_FLIGHT, ACCEPTED, REJECTED, DEFINITELY_NOT_SENT, OUTCOME_UNKNOWN,
NOT_FOUND, AUTH_CONFIG_ERROR, NOT_SUPPORTED, RATE_LIMITED`; `unknown_reason` iff `OUTCOME_UNKNOWN`; an attempt
still `IN_FLIGHT` after a crash is resolved as `OUTCOME_UNKNOWN / CRASH_DURING_CREATE` by the recovery sweeper.
`UNIQUE (payment_intent_id, attempt_no)`.

### 4.4 `payment_transactions` (provider-side record)

`provider_transaction_ref` (`UNIQUE (provider_id, provider_transaction_ref)`), `raw_status` (**verbatim**),
`raw_status_at`, `mapped_status` (payment vocabulary), `mapping_version`, `last_source`, `reported_amount_minor` +
`reported_currency`, `provider_fee_minor`, `payer_instrument_fingerprint` (HMAC; duplicate rule and
same-instrument manual refunds), `payer_instrument_masked`. Not machine-guarded: it mirrors the latest provider
observation, anomalies included. Every observation is also a `payment_events` row with `raw_status`.

### 4.5 `payment_provider_references`

`UNIQUE (provider_id, ref_kind, reference)`; kinds `PAYMENT, TRANSACTION, CHECKOUT_SESSION, POLL_URL, AUTHORISATION,
CAPTURE, REFUND, DISPUTE, REVERSAL`. Append-only. Webhooks that carry only a provider reference are resolved
through this table.

### 4.6 `payment_events` (append-only history of every payments machine)

`subject_type` (`PAYMENT_INTENT | REFUND_REQUEST | REFUND_TRANSACTION | PAYMENT_DISPUTE`) with the matching subject
FK (deferrable), always `payment_intent_id`; `from_status` (NULL = initial), `to_status`, `applied`,
`not_applied_reason` (`LOWER_RANK, NOT_AN_EDGE, ANOMALY, DUPLICATE, PARKED`), `transition_code` (`P1`–`P20`, `P18b`),
`source`, `provider_event_id`, `inbox_event_id`, `payment_attempt_id`, `raw_status`, `actor_type`, `actor_id`,
`reason`, `evidence_record_id`, `ledger_transaction_ids uuid[]`, `correlation_id`, `occurred_at`, `recorded_at`.

| Check | Rule |
|---|---|
| `ck_payment_events_success_source` | Applied `SUCCEEDED` (intent or refund) only from `SYNC, WEBHOOK, POLL, RECON` — never INTERNAL/STAFF |
| `ck_payment_events_unknown_internal` | Applied `UNKNOWN` only from `INTERNAL` |
| `ck_payment_events_failed_source` | Applied `FAILED` (intent or refund) only from `WEBHOOK, POLL, RECON, SYNC`, `INTERNAL` from `CREATED` (pre-submit rejection, P4), or `STAFF` (with evidence, next row) — baseline §12 **I-21** |
| `ck_payment_events_staff_failed_evidence` | Staff `FAILED` needs `evidence_record_id` (second layer; STAFF is an allowed FAILED source only together with this evidence; I-21 as amended) |
| `ck_payment_events_staff` | STAFF source needs actor and reason |
| `ck_payment_events_not_applied` | Not-applied events carry a reason |

**Same-transaction rule:** deferred constraint triggers (`app.require_payment_event`) on `payment_intents`,
`refund_requests`, `refund_transactions`, `payment_disputes` reject a COMMIT in which a status changed without an
applied event with the same from/to (`recorded_at = transaction_timestamp()`). The current state is thus always
derivable from history (DATABASE §9).

### 4.7 `refund_requests` (machine `refund_request`)

`payment_intent_id, campaign_id, currency` (composite FK to the intent ⇒ **refund currency = payment currency**),
`amount_minor > 0`, `reason_code`, `reason_text`, `donor_reason_text` (C2), `channel`, `requested_by`,
`decided_by` (`ck_refund_requests_maker_checker: decided_by <> requested_by`), `decided_step_up_at`,
`second_approved_by` (DUAL for funding plan `PLATFORM_FUNDED_WITH_RECOVERY`, distinct from both), `funding_plan`,
`execution_method` (`PROVIDER_REFUND | MANUAL_PAYOUT`), `compliance_case_id`, `idempotency_key`
(`UNIQUE (requested_by, idempotency_key)`), `expires_at` (72 h).

**Over-refund guard** (`app.refund_requests_check_refundable`, AFTER INSERT/UPDATE OF status → APPROVED):

1. `SELECT … FROM payment_intents WHERE id = … FOR UPDATE` (serialises concurrent approvals);
2. payment must be `SUCCEEDED` or `PARTIALLY_REFUNDED` (a `DISPUTED` payment is not refunded);
3. Σ `refund_requests.amount_minor` in `APPROVED, EXECUTING, COMPLETED` (= pending reservations + settled) +
   Σ `chargebacks.amount_minor`, both filtered to the payment's currency, must be ≤ `amount_minor`.

Because composite FKs force every refund and chargeback into the payment's currency, the sums can never mix
currencies. A second check on `refund_transactions` → `SUCCEEDED` re-asserts Σ settled ≤ captured.

### 4.8 `refund_transactions` (machine `refund_transaction`; `id = refund_id`)

Created in the approval transaction together with `refund:{id}:reserved`. `refund_request_id` UNIQUE; FK
`(refund_request_id, payment_intent_id, currency, amount_minor)` ⇒ same payment, currency and amount as approved.
`provider_refund_ref` (`UNIQUE (provider_id, provider_refund_ref)`, set once), `raw_status`, `failure_code`
(required when FAILED), unknown/poll columns, `ledger_reservation_txn_id` **NOT NULL**,
`ledger_settlement_txn_id` (only when SUCCEEDED), `ledger_release_txn_id` (only when FAILED) — each FK is
`(txn_id, currency) → ledger.ledger_transactions (id, currency)` so journals are in the refund currency.

### 4.9 `payment_disputes` (machine `payment_dispute`)

`provider_dispute_ref` (`UNIQUE (provider_id, provider_dispute_ref)`), `kind` (`INQUIRY, CHARGEBACK,
PROVIDER_REVERSAL`), `reason_code_provider` (verbatim), `reason_category`, `amount_minor`, currency (composite FK),
`debit_timing` (`ON_OPEN, ON_LOSS, NONE`; INQUIRY ⇒ NONE), `evidence_due_at` (**stored as received, never
edited**), `hold_id → risk.holds`, `evidence_pack_record_id → audit.evidence_records`, `accept_requested_by` /
`accept_approved_by` (distinct; ACCEPTED requires the checker), `escalated_from_id`, `outcome_at`.

### 4.10 `chargebacks` (append-only)

One per completed involuntary loss: `reversal_kind`, `payment_dispute_id` (required **iff** `CARD_CHARGEBACK`,
UNIQUE), `provider_reversal_ref` (`UNIQUE (provider_id, …)`), amount + currency (composite FK),
`ledger_transaction_id` (`(id, currency)` FK; NULL when the provider debited at open and `dispute:{id}:opened`
already moved the money), `inbox_event_id`, `occurred_at`. Counted by the over-refund guard.

## 5. Uniqueness backing idempotency (summary)

| Constraint | Prevents |
|---|---|
| `uq_payment_intents_idempotency (idempotency_scope, idempotency_key)` | Two intents for one client request |
| `uq_payment_intents_provider_payment_ref` | Two intents bound to one provider payment |
| `uq_payment_transactions_provider_ref` | Two provider transaction records |
| `uq_payment_provider_references` | Reference collisions across kinds |
| `uq_provider_webhook_inbox_event` | Double ingestion of a webhook |
| `uq_refund_requests_idempotency`, `uq_refund_transactions_refund_request_id`, `uq_refund_transactions_provider_ref` | Double refunds |
| `uq_payment_disputes_provider_ref`, `uq_chargebacks_provider_ref`, `uq_chargebacks_payment_dispute_id` | Double dispute/chargeback |
| ledger `uq_ledger_transactions_idempotency_key` (`payment:{id}:capture`, `refund:{id}:reserved` …) | Double posting |

## 6. Concurrency control

| Operation | Lock order (always this order) | Isolation |
|---|---|---|
| Apply payment event (webhook/poll/recon) | `payment_intents` row `FOR UPDATE` → (ledger posts T1/T2 via ledger service, which locks balance rows in account-id order) | READ COMMITTED |
| Approve refund | `payment_intents FOR UPDATE` (taken by the guard trigger, or earlier by the service) → ledger balance rows (account-id order) | READ COMMITTED |
| Refund/dispute apply | `payment_intents FOR UPDATE` → subject row → ledger | READ COMMITTED |
| Inbox claim | River job `FOR UPDATE SKIP LOCKED`; inbox row locked; guarded status transition | READ COMMITTED |

`SERIALIZABLE` is not needed: every decision reads state that a single row lock covers (the payment row), and the
ledger's own non-negative projection CHECK (L8) is the last line for balances. Provider calls are never made inside
a DB transaction (DATABASE §12): attempt row committed → call → result in a new transaction.

## 7. Grants and append-only enforcement

Grants are derived by `app.apply_runtime_grants()` (0018): tables with `app.forbid_mutation` on UPDATE get no UPDATE
grant. Append-only here: `provider_capabilities`, `provider_health_events`, `payment_events`,
`payment_provider_references`, `chargebacks`; insert-once-then-complete: `payment_attempts`; delete-forbidden:
all other payments tables and `fee_schedule_versions`, `provider_webhook_inbox`.

## 8. Test requirements (implemented in `payments_test.sql`)

Registry: settlement ≠ currency rejected; MERCHANT_SETTLEMENT / UNVERIFIED not routable and unusable; method
mismatch rejected; capabilities append-only; raw secret rejected; sandbox never PRODUCTION; candidate provider
cannot be ENABLED. Inbox: dedupe; unsigned needs status-API confirmation; raw immutable; processing mutable;
illegal processing transition; parking needs subject; unrecognised kept (not dropped); no delete. Machine: no
event ⇒ rejected; must start CREATED; FAILED→PENDING, SUCCEEDED→FAILED, PENDING→CREATED, REFUNDED→*, CHARGED_BACK→*
rejected; late success FAILED→SUCCEEDED allowed; success never INTERNAL; UNKNOWN only INTERNAL; FAILED never INTERNAL except from CREATED, STAFF only with provider evidence (I-21); rank must match; amount/currency immutable; provider ref unique and set-once; events append-only;
attempts complete once; provider rebinding after possible send rejected; CRASH_DURING_CREATE accepted. Refunds:
self-approval, currency mismatch, over-refund (incl. one minor unit over), refund of unsucceeded payment, C2 needs
DUAL, amount mismatch, UNKNOWN→CREATED illegal. Disputes/chargebacks: currency mismatch, accept self-approval,
illegal OPENED→WON, `evidence_due_at` immutable, P18b dispute won on a partially refunded payment, card chargeback needs dispute, chargebacks append-only. Fees:
self-approval, needs APPROVED CR, never retroactive, must equal proposal, effective rate not editable, future
version cancellable, no delete.

**Stage 3+ (real PostgreSQL) must add** what PGlite cannot test: concurrent refund approvals on one payment
(exactly the affordable set succeeds), webhook + poll racing to SUCCEEDED (one T1/T2), crash between attempt
insert and result (sweeper → UNKNOWN), and grant tests under `SET ROLE fundzim_app`.

## 9. Open items

- PD-20/PD-21 (refund fees, windows) are policy, not schema; LR-018, LR-081, LR-083 unchanged.
- PCR-006/009/010/016: capability flags stay false until VERIFIED.
- Resolved: `DISPUTED → PARTIALLY_REFUNDED` (P18b, ADR-024) restores a partially refunded payment when its dispute
  is won.
- Resolved (lead, I-21 amended): `STAFF` may write payment `FAILED` **only** with a provider-evidence record
  (transaction-lifecycle §7.3 step 6, FINANCE maker-checker). `SYSTEM` and timeout handlers never can.
