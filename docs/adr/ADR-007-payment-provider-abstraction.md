# ADR-007: Payment provider abstraction

- **Status:** Accepted
- **Date:** 2026-10-08
- **Stage:** 0 (design). Abstraction is built in Stage 8. Real PSPs arrive in Stage 9.

## Context

FundZim targets these rails:

- EcoCash, OneMoney, InnBucks and O'Mari;
- ZimSwitch-supported methods;
- Zimbabwean bank payments;
- Visa and Mastercard.

**No payment service provider (PSP) has been selected.** Providers differ in:

- supported methods and currencies;
- webhook versus polling;
- signature schemes;
- refund support;
- payout support;
- settlement currency.

Coupling the platform to one PSP would make switching or adding providers expensive and risky. Regulatory
constraints mean funds should flow through licensed providers (LEGAL_REVIEW_REQUIRED, LR-001, LR-004).

## Decision

Payments are layered:

**Domain services → `payments.Service` → `Provider` interface → adapters (one per PSP) + a `sandbox` fake provider.**

- **Interface concepts:** `Capabilities()`, `CreatePayment`, `GetPayment`/`GetTransactionStatus`,
  `CancelPayment`, `RefundPayment`, `VerifyWebhook`, `ParseWebhook`, `CreatePayout`, `GetPayoutStatus`.
- **Capabilities** are declared per provider and configurable:
  - methods (`ECOCASH`, `ONEMONEY`, `INNBUCKS`, `OMARI`, `ZIMSWITCH`, `BANK_TRANSFER`, `CARD`);
  - currencies;
  - refunds (full or partial);
  - payouts;
  - webhooks versus polling;
  - minimum and maximum amounts;
  - settlement currency.
  Unsupported operations return an explicit `ErrNotSupported`.
- **Routing** selects a provider by method, currency, capability and configuration.
- **Payment intent states:**
  - main flow: `CREATED → PENDING → REQUIRES_ACTION → SUCCEEDED | FAILED | CANCELLED | EXPIRED`;
  - after success: `PARTIALLY_REFUNDED`, `REFUNDED`, `DISPUTED`, `CHARGED_BACK` (dispute lost).
  Transitions are monotonic: a move requires a higher precedence rank **and** an allowed edge in the state
  graph; events whose prerequisite state has not been reached are parked and reprocessed. Late or out-of-order events cannot regress
  a payment, e.g. from `SUCCEEDED` back to `PENDING`. An unknown outcome after a timeout stays `PENDING` until
  status polling or reconciliation resolves it. It is never assumed failed.
- **Webhooks:** `/api/v1/webhooks/{provider}`. Processing order:
  1. Verify the signature.
  2. Check the timestamp tolerance (replay window, e.g. 5 minutes, where supported).
  3. Store the redacted raw payload in `webhook_inbox` with `UNIQUE(provider, provider_event_id)`.
  4. Return 2xx quickly.
  5. Process asynchronously, with retries and backoff.
  6. Send poison messages to a dead-letter table and raise an alert.
  Where a provider's signatures are weak or missing, confirm via its authenticated server-to-server status API
  before acting.
- **Idempotency:**
  - Mutating financial endpoints require an `Idempotency-Key` header. Reusing a key with a different body
    returns `409 IDEMPOTENCY_KEY_REUSED`.
  - Outbound provider calls use our own reference as the idempotency key (`payment_id`; `refund_id` and
    `payout_id` for refunds and payouts), never with an attempt counter, so retries never double-charge.
  - DB uniqueness (`UNIQUE(provider, provider_transaction_id)`) is the last line of defence.
- **Confirmation** comes only from authoritative provider state. **Browser redirects are never payment
  confirmation.**
- Only **hosted or tokenised flows** are used. FundZim never receives raw PAN or CVV. The aim is to stay out of
  card-data scope, but **no PCI DSS compliance is claimed**.
- The `sandbox` provider is deterministic and scriptable. It can simulate:
  - decline;
  - timeout;
  - duplicate webhook;
  - out-of-order events;
  - delayed success after a client timeout;
  - refund and payout failures.

## Consequences

### Positive
- Providers can be added or replaced without touching campaigns, ledger or payouts.
- The full failure matrix can be tested without a real PSP.
- Capability flags make provider limitations explicit instead of implicit.

### Negative / costs
- The abstraction may not fit every provider perfectly. Provider-specific quirks are contained in adapters
  but still cost work.
- Lowest-common-denominator risk: advanced provider features need a capability flag, not leakage into the
  domain.

### Follow-up work
- Stage 1: PSP licensing and settlement research (LEGAL_REVIEW_REQUIRED, LR-004).
- Stage 8: interface, sandbox, inbox and outbox.
- Stage 9: real adapters.

## Alternatives considered

| Alternative | Why rejected |
|---|---|
| Integrate one PSP directly | Vendor lock-in. Its quirks would spread through the codebase, and switching would be costly. |
| Third-party payment orchestration SaaS only | An additional intermediary and cost, uncertain Zimbabwe-rail coverage, and data residency questions. It could still sit behind the interface as one adapter. |
| Synchronous webhook processing | Slow responses cause provider retries and duplicates, and lose events on errors. |

## Security implications
- Every inbound callback is authenticated.
- Provider credentials are classified C4 SECRET and kept in a secret manager, scoped per provider and
  environment.
- Webhook payloads are redacted before storage.
- The webhook endpoint is rate limited and does not reveal processing errors to callers.
- SSRF is avoided: provider base URLs come from configuration, never from request data.

## Financial implications
Prevents double charges and duplicate ledger postings through idempotency and uniqueness, and prevents false
confirmations by never trusting redirects. Unresolved outcomes are handled through reconciliation (Stage 17).

## Related
ADR-005, ADR-006, ADR-010, [PAYMENTS.md](../PAYMENTS.md), [LEDGER.md](../LEDGER.md), [COMPLIANCE.md](../COMPLIANCE.md).
