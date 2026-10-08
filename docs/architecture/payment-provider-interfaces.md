# Payment Provider Interfaces (`psp` module)

> **Status:** Stage 2 design. Nothing here is implemented (abstraction + sandbox: Stage 8; real rails: Stage 9).
> **The Go code below is a SKETCH.** Go is not installed on the design machine; nothing here has been compiled or
> run. Names and shapes are the design intent for Stage 8 and may be adjusted there without changing the rules.
> **No provider is selected or integrated.** Candidate names (Paynow, Pesepay, Payonify, Linkwa, Smile&Pay,
> ContiPay) appear only as `selection_status = PENDING` candidates
> ([provider-comparison.md](../payments/provider-comparison.md)); none of their capabilities are asserted here.

Contract: [design-baseline.md](../stage-2/design-baseline.md) §2 (`psp` module), §5.8, ADR-007, ADR-020, ADR-024,
ADR-029. Registry DDL: [`0007_fees_psp.sql`](../../design/sql/0007_fees_psp.sql)
([payment-schema.md §3](../database/payment-schema.md)). Rules: [PAYMENTS.md](../PAYMENTS.md) §3–§5, §9–§10, §14;
[provider-capability-matrix.md §1](../payments/provider-capability-matrix.md) (only VERIFIED counts);
[operating-model-decision.md §9](../compliance/operating-model-decision.md) (refuse MERCHANT_SETTLEMENT);
[currency-and-fx-policy.md](../payments/currency-and-fx-policy.md) (settlement currency = donation currency).

---

## 1. Placement

```
internal/psp/                 public service: Registry, Router, Inbox, Health; interfaces below
internal/psp/adapters/sandbox Stage 8 — deterministic fake (non-production only)
internal/psp/adapters/<name>  Stage 9 — one per selected provider (after an ADR), none today
```

`payments` and `payouts` import `psp` (baseline §3); `psp` imports only `platform` and `audit`. Adapters
**translate only**: no fee logic, no eligibility, no state decisions, no ledger. They never log secrets, full
MSISDNs, card data or raw signed payloads.

## 2. Interface sketches

```go
// SKETCH — not compiled.
package psp

type ProviderID string            // payment_providers.code, e.g. "sandbox"
type Method string                // ECOCASH, ONEMONEY, INNBUCKS, OMARI, ZIMSWITCH, BANK_TRANSFER, CARD
type Rail string                  // payout rails (same codes minus CARD)

// Our reference: payment_id / refund_id / payout_id (UUIDv7). Sent as merchant reference AND provider
// idempotency key. Never contains an attempt counter; identical on every retry, forever.
type OurRef uuid.UUID

type CallMeta struct {
    CorrelationID string            // propagated to logs/traces; stored on *_attempts.correlation_id
    AttemptID     uuid.UUID         // the *_attempts row committed IN_FLIGHT before the call
    Deadline      time.Time         // explicit per-call deadline (ctx also carries it)
    Account       AccountConfig     // resolved from provider_accounts (secret REFS resolved at call time)
}

type PaymentProvider interface {
    ID() ProviderID
    CreatePayment(ctx context.Context, m CallMeta, r CreatePaymentRequest) (PaymentObservation, error)
    GetPayment(ctx context.Context, m CallMeta, ref OurRef) (PaymentObservation, error)   // status query by OUR ref
    CancelPayment(ctx context.Context, m CallMeta, ref OurRef) (PaymentObservation, error)
    CapturePayment(ctx context.Context, m CallMeta, ref OurRef, amt money.Money) (PaymentObservation, error) // separate_capture only
}

type RefundProvider interface {
    RefundPayment(ctx context.Context, m CallMeta, r RefundRequest) (RefundObservation, error) // r.RefundID = refund_id
    GetRefund(ctx context.Context, m CallMeta, ref OurRef) (RefundObservation, error)
}

type PayoutProvider interface {
    CreatePayout(ctx context.Context, m CallMeta, r PayoutRequest) (PayoutObservation, error)  // r.PayoutID = payout_id
    GetPayoutStatus(ctx context.Context, m CallMeta, ref OurRef) (PayoutObservation, error)
    CancelPayout(ctx context.Context, m CallMeta, ref OurRef) (PayoutObservation, error)       // only if supported (PCR-014)
}

type ProviderWebhookVerifier interface {
    // Verify exact raw bytes: signature (constant-time) with the CURRENT then PREVIOUS secret ref,
    // timestamp within replay window if the provider sends one. Error ⇒ 401, nothing stored.
    Verify(ctx context.Context, acct AccountConfig, raw InboundWebhook) (VerifiedBy, error)
    // Parse into normalised events; unknown types are returned with Class=OTHER, never dropped.
    Parse(ctx context.Context, raw InboundWebhook) ([]WebhookEvent, error)
}

type ProviderReconciliationSource interface {
    // Statement/settlement data per provider+currency+period (API, file or dashboard export; PCR-007).
    FetchStatement(ctx context.Context, m CallMeta, q StatementQuery) (StatementHandle, error)
    ParseStatement(ctx context.Context, h StatementHandle) (iter.Seq2[StatementLine, error], error)
    PoolBalance(ctx context.Context, m CallMeta, cur money.CurrencyCode) (money.Money, error) // only if pool_balance_api
}

// Every observation carries canonical AND raw; raw is never discarded.
type PaymentObservation struct {
    Class          OutcomeClass       // §5
    ProviderRef    string             // provider's id, if any
    RawStatus      string             // verbatim
    Canonical      CanonicalStatus    // mapped via the adapter's versioned table; UNKNOWN if unmapped
    Mapped         bool
    MappingVersion string
    Next           NextAction         // NONE | REDIRECT(url) | AWAIT_HANDSET_APPROVAL | SHOW_INSTRUCTIONS
    Reported       *money.Money       // amount as reported (mismatch ⇒ anomaly)
    Fee            *money.Money       // PSP fee if reported (T2 timing, PCR-016)
    PayerToken     []byte             // provider payer token → HMAC'd by payments, never stored raw
    OccurredAt     *time.Time
    Metadata       map[string]string  // provider-specific, allow-listed keys only; stored redacted
}
```

`RefundObservation` and `PayoutObservation` have the same shape (plus `Fee`/`Received` for payouts). Unsupported
operations return `ErrNotSupported` — never a silent no-op.

## 3. Capability discovery (DB-backed registry, verified-only routing)

Capabilities are **data**, not code constants: `app.provider_capabilities` rows (versioned, append-only), read
through the view `provider_capabilities_current`. The adapter declares what it *implements*; the registry row
declares what is *evidenced*; routing uses the intersection, and only rows where `routable = true`:

```
routable = evidence_status = 'VERIFIED'
       AND custody_model IN ('PSP_POOL','SPLIT_DIRECT')     -- never MERCHANT_SETTLEMENT (Model B in substance)
       AND (signed_webhooks OR status_api)                   -- an authoritative status path exists
CHECK settlement_currency = currency                         -- ADR-018: no FX, ever
```

Every flag defaults to false (= absent); UNVERIFIED and REQUIRES_PROVIDER_CONFIRMATION rows stay visible
(`flag_evidence`, `pcr_ref`) but inert (capability-matrix rule 2). A capability change is a new row with a source
(doc URL + access date or contract ref), recorded by staff, audited. The payment/payout row pins the capability by
composite FK, so the decision is reproducible later.

```go
// SKETCH
type Registry interface {
    Routable(ctx context.Context, q RouteQuery) ([]Capability, error)     // current, routable rows only
    Capability(ctx context.Context, id uuid.UUID) (Capability, error)
    Account(ctx context.Context, provider ProviderID, env Environment) (AccountConfig, error)
}
```

## 4. Provider configuration (secret references only)

`app.provider_accounts` holds `api_credentials_secret_ref`, `webhook_secret_ref`, `webhook_secret_ref_previous`
(rotation overlap) as `secretref://…` paths (CHECK rejects anything else), plus non-secret `merchant_reference`
and `webhook_replay_window_seconds`. The adapter resolves refs from the secret manager at call time and caches
them in memory with a short TTL; values never reach the DB, logs, traces or errors. The sandbox provider cannot
have a `PRODUCTION` account (DB trigger) and startup refuses it when `APP_ENV=production`. A real provider can be
`ENABLED` only after `selection_status = SELECTED` with an ADR (DB CHECK).

## 5. Error classification

The **adapter** classifies every call outcome into exactly one class (only it knows the provider's documented
semantics, PCR-009/010). Unclassifiable ⇒ `INDETERMINATE`.

| Class | Meaning | `*_attempts.classification` | Payment / payout effect |
|---|---|---|---|
| `ACCEPTED` | Provider acknowledged | `ACCEPTED` | → PENDING / REQUIRES_ACTION / PROCESSING (or final if authenticated) |
| `DEFINITE_FAILURE` | Documented "not created / declined" | `REJECTED` | → FAILED (payment P4/P10; payout Y11) |
| `RETRYABLE_BEFORE_SEND` | Nothing reached the provider (DNS, refused, TLS before body written) | `DEFINITELY_NOT_SENT` | stays CREATED/SUBMITTED; same-reference retry allowed |
| `INDETERMINATE` | Timeout after write, reset, 5xx, malformed/unverifiable response, crash | `OUTCOME_UNKNOWN` (+ `unknown_reason`) | → **UNKNOWN**; status query; never FAILED |
| `NOT_FOUND` | Status query: reference unknown to provider | `NOT_FOUND` | stays; FAILED only after documented propagation window **and** finality (PCR-010/026) |
| `AUTH_CONFIG_ERROR` | 401/403, bad credentials, config | `AUTH_CONFIG_ERROR` | create: treated as not sent only if the provider documents no processing on auth failure; otherwise INDETERMINATE. Alert, circuit opens |
| `RATE_LIMITED` | 429 with documented no-processing semantics | `RATE_LIMITED` | as RETRYABLE_BEFORE_SEND, with backoff |
| `NOT_SUPPORTED` | Operation not implemented/evidenced | `NOT_SUPPORTED` | caller chooses manual path (e.g. MANUAL_PAYOUT refund) |
| `INVALID_REQUEST` | Our bug (validation) | `REJECTED` | FAILED pre-submit + SEV3 |

`unknown_reason`: `TIMEOUT, CONN_RESET_AFTER_SEND, HTTP_5XX, MALFORMED_RESPONSE, SIGNATURE_INVALID_RESPONSE,
CRASH_DURING_CREATE` (attempt still IN_FLIGHT after a process crash, set by the sweeper).

## 6. Timeouts, retries, idempotency and correlation

1. **Record before call.** The `*_attempts` row is committed `IN_FLIGHT` before the HTTP call; no provider call
   ever runs inside a DB transaction (DATABASE §12).
2. **Deadlines.** Each operation has an explicit deadline from adapter config (e.g. create 20 s, status 10 s
   [default]); HTTP client timeouts ≤ deadline; no unbounded retries inside the adapter.
3. **Status-query first.** After any `INDETERMINATE` outcome the next action is always a status query by our
   reference, never a re-create.
4. **Same reference only.** A non-idempotent call is retried only (a) after `RETRYABLE_BEFORE_SEND`, or (b) for
   payments/refunds when the capability says `idempotent_create` / `idempotent_refund` (VERIFIED) — always with
   the same `payment_id` / `refund_id`. **Payouts:** never re-created after a possible send; enforced by the
   `payout_attempts` trigger (create allowed only while SUBMITTED and only if all earlier creates were
   `DEFINITELY_NOT_SENT`).
5. **Backoff:** exponential with jitter; poll schedules per [background-processing.md](background-processing.md)
   (`payments.status_poll`, `payouts.status_poll`).
6. **Correlation.** `correlation_id` from the inbound request/job flows to the adapter, the provider (where a
   header is supported), `*_attempts`, `*_events`, logs and traces. The provider reference is recorded in
   `*_provider_references` (UNIQUE per provider, kind, reference).
7. **Circuit breaker** per (provider, operation, currency) from `provider_health_events`; open circuit ⇒ router
   skips the provider for new payments (before binding only) and EC-18 DEFERs payouts.

## 7. Provider health and settlement freshness (EC-18, EC-20)

`app.provider_health_events` (append-only) is the single place for provider-level operational state:

| `event_kind` | Writer | Reader |
|---|---|---|
| `CALL_OK`, `CALL_ERROR`, `TIMEOUT`, `CIRCUIT_OPENED/HALF_OPEN/CLOSED` | psp (adapters' wrapper) | router, EC-18 |
| `OUTAGE_DECLARED`, `OUTAGE_RESOLVED` | staff via psp (actor required) | router, EC-18, incident tooling |
| `SETTLEMENT_RECONCILED`, `RECON_SEV1_OPENED`, `RECON_SEV1_CLOSED` | `reconciliation`, **through psp's interface** (provider + currency required, operation `STATEMENT`) | `payouts` EC-20, **through psp's interface** |

EC-20 passes when the latest `SETTLEMENT_RECONCILED` for (provider, currency) is within the freshness limit
(`settlement.freshness`, risk.limits) and there is no `RECON_SEV1_OPENED` without a later `RECON_SEV1_CLOSED`;
otherwise DEFER. This keeps `payouts` from depending on `reconciliation` (baseline §3: reconciliation imports
payouts, not the reverse).

```go
// SKETCH
type Health interface {
    Record(ctx context.Context, tx pgx.Tx, e HealthEvent) error
    Circuit(ctx context.Context, p ProviderID, op Operation, cur money.CurrencyCode) (CircuitState, error)
    SettlementFreshness(ctx context.Context, p ProviderID, cur money.CurrencyCode) (lastReconciled time.Time, openSEV1 bool, err error)
}
```

## 8. Routing algorithm

```text
Route(campaign, method, currency, amount):                         -- payments, before any provider call
  if campaign.status != ACTIVE                                    → 422 CAMPAIGN_NOT_ACCEPTING_DONATIONS
  if currency ∉ campaign.accepted_currencies                      → 422 CURRENCY_NOT_ACCEPTED
  rows := registry.Routable(method, currency)                     -- VERIFIED, not MERCHANT_SETTLEMENT,
                                                                  -- settlement_currency = currency (CHECK)
  rows := rows where provider.status = ENABLED and account(env).status = ENABLED
  rows := rows where custody_model = PSP_POOL                      (campaign.settlement_model = MODEL_A)
                   or custody_model = SPLIT_DIRECT                 (campaign.settlement_model = MODEL_C only)
  rows := rows where PROVIDER limits (risk.limits) contain amount  -- min/max per currency; unconfigured ⇒ excluded
  rows := rows where circuit(provider, CREATE_PAYMENT, currency) != OPEN and no PROVIDER_HOLD
  pick by configured priority (per method, currency)               → record provider, account, capability_id,
                                                                     custody_model, routing_reason on the intent
  none                                                             → 422 PAYMENT_METHOD_UNAVAILABLE
Fallback to the next provider only while the intent is CREATED and every CREATE attempt was DEFINITELY_NOT_SENT
(DB trigger enforces the binding).

RoutePayout(payout, destination):
  rows := registry.Routable(currency = payout.currency) with payout_api and destination.rail ∈ payout_rails,
          custody_model = PSP_POOL (the pool that holds the campaign's settled funds), provider enabled,
          circuit closed, settlement freshness ok
```

`MERCHANT_SETTLEMENT` refusal is layered: generated `routable` is false; the payment FK requires
`routable = true`; `ck_payment_intents_custody_model` excludes it; and the router never asks for it. Moving to
Model B needs a new ADR plus legal sign-off (ADR-013).

## 9. Currency, method and payout-method support

- One capability row per (provider, method, currency); `settlement_currency` must equal `currency`, so a rail
  that would settle USD donations in ZWG (or vice versa) cannot even be recorded. FundZim never converts.
- ZWG rows are inert until LR-043 enables the currency and the provider's ZWG behaviour is VERIFIED (PCR-018).
- Payout rails are listed per currency on the capability row (`payout_api`, `payout_rails`). A payout is routed only
  when the destination rail is listed and the currency matches (EC-08).
- Refund support: `refund_api`, `partial_refund`, `idempotent_refund`. Without `refund_api`, refunds use
  `execution_method = MANUAL_PAYOUT` to the same payer instrument (LR-081).

## 10. Provider-specific metadata

Adapters may return `Metadata` with allow-listed keys only (e.g. provider status reason code, poll URL id). It is
stored redacted in `provider_webhook_inbox.payload_redacted` / `payment_events` and never used for decisions unless
promoted to a typed field by a reviewed change. Raw status strings are always kept verbatim (`raw_status`).

## 11. Sandbox provider contract (Stage 8, automated tests)

`code = 'sandbox'`, `is_sandbox = true`, never in production. Implements every interface above. Signs webhooks
with a test secret through the **same** verification path as real providers. Has a controllable clock and a test
control API (deliver, duplicate, reorder, delay, drop, replay events).

### 11.1 Scenario triggers (deterministic)

Selected, in priority order, by: (1) `metadata["sandbox_scenario"]` (honoured only when `APP_ENV != production`),
(2) the **amount's last two minor-unit digits**, (3) default `success`.

| Amount suffix | Scenario | Behaviour |
|---|---|---|
| `…00` | `success` | Accepted → PENDING; signed webhook `paid` after 1 s (sandbox clock) |
| `…01` | `success_handset` | REQUIRES_ACTION, then `paid` after 5 s |
| `…02` | `decline` | Definitive rejection → FAILED (`DECLINED`) |
| `…03` | `timeout_then_success` | `CreatePayment` exceeds the deadline (`INDETERMINATE/TIMEOUT`) but the payment IS created; status query and a later webhook report `paid` |
| `…04` | `timeout_then_failure` | Timeout; payment created; later `failed` |
| `…05` | `timeout_not_created` | Timeout; nothing created; status query returns `NOT_FOUND` (sandbox documents finality after 60 s) |
| `…06` | `duplicate_webhook` | `paid` delivered 3× with the same `provider_event_id` |
| `…07` | `out_of_order_webhook` | `paid` delivered before `pending`; later `refunded` delivered before `paid` (parking) |
| `…08` | `late_success_after_expiry` | `expired` webhook, then authoritative `paid` 2 min later (P15) |
| `…09` | `refund_failure` | Payment succeeds; any refund ends `failed` |
| `…10` | `refund_timeout_then_success` | Refund call times out; `GetRefund` later `succeeded` |
| `…11` | `chargeback_lost` | Succeeds; `dispute.opened` (debit on open) then `dispute.lost` |
| `…12` | `chargeback_won` | As above, `dispute.won` |
| `…13` | `provider_reversal` | Succeeds; later `reversal` without dispute phase (P20) |
| `…14` | `bad_signature` | Webhook signed with a wrong key (must be rejected, nothing stored) |
| `…15` | `replayed_old` | Valid signature, timestamp outside window |
| `…16` | `webhook_never` | No webhooks; only status queries reveal state |
| `…17` | `unmapped_status` | Provider status `"limbo"` (unmapped ⇒ UNKNOWN + alert; inbox `UNRECOGNISED` for unknown event type) |
| `…18` | `amount_mismatch` | `paid` reports a different amount (anomaly, not applied) |

Payout scenarios by payout amount suffix:

| Suffix | Scenario | Behaviour |
|---|---|---|
| `…00` | `payout_paid` | Accepted → `completed` webhook |
| `…20` | `payout_unknown_then_completed` | `CreatePayout` times out (created); status query `processing`, then `completed` |
| `…21` | `payout_unknown_then_failed` | Timeout; later documented final `failed` |
| `…22` | `payout_not_sent` | Connection refused before write (`RETRYABLE_BEFORE_SEND`) twice, then accepted on the same reference |
| `…23` | `payout_rejected_bad_destination` | Definitive `invalid destination` |
| `…24` | `payout_reversed` | Completed, then `returned` after 1 h (Y14) |
| `…25` | `payout_late_completion_after_failed` | `failed`, then `completed` (SEV1 path) |
| `…26` | `payout_crash_window` | Sandbox accepts but test harness kills the worker before tx2 (`CRASH_DURING_CREATE`) |

### 11.2 Simulated custody models and capabilities

The sandbox registers several capability rows (all `evidence_source = 'sandbox contract'`, VERIFIED) so tests can
exercise every routing branch: `PSP_POOL` (default), `SPLIT_DIRECT` (Model C; organisation campaigns only),
`MERCHANT_SETTLEMENT` (must be refused: never routable), an `UNVERIFIED` row (must be refused), signed vs
unsigned webhooks (`WEBHOOK_UNSIGNED_VERIFY_BY_API` path requires a status query before applying), with/without
`refund_api`, `partial_refund`, `separate_capture`, `payout_api`, `idempotent_create`, `documented_expiry`, and
statement formats `API` and `FILE_CSV` for reconciliation tests (`settlement_report_mismatch` scenario via the
control API). A second sandbox (`sandbox_two`, `POLL_ONLY`) supports fallback-before-binding tests.

## 12. Test requirements

Stage 8: contract tests run every adapter (sandbox now, real ones in Stage 9) through the same suite — every
classification class, every scenario above, raw status preserved, mapping version recorded, secrets never logged
(log capture assertion), `ErrNotSupported` instead of no-ops, router refusals (MERCHANT_SETTLEMENT, UNVERIFIED,
currency mismatch, unconfigured PROVIDER limit, open circuit), fallback only before binding. DB-level invariants
for the registry are already in `payments_test.sql`.

## 13. Open questions

PCR-001–007 (licensing, contract, custody, statements), PCR-006 (refunds/idempotent refunds), PCR-009
(idempotent create), PCR-010 (expiry/finality), PCR-013/014/015/026 (payouts: idempotency, fees, cancellation,
returns, completion window), PCR-016 (fee timing), PCR-018 (ZWG); LR-001/003/004/005 (custody, licensing,
cross-border), LR-043 (ZWG enablement), LR-075 (FundZim as instructing party), LR-081 (refund to another instrument).
