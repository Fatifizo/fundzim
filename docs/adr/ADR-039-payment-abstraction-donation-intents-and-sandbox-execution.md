# ADR-039: Payment abstraction — donation intents, sandbox-only execution, deferred financial tables

- **Status:** Proposed (Stage 8, 2026-10-12)
- **Deciders:** Technical lead (Stage 8); owner acceptance pending
- **Related:** ADR-007, ADR-013, ADR-018, ADR-020, ADR-024, ADR-025, ADR-029, ADR-038; design-baseline §2–§8;
  [payment-state-machine.md](../architecture/payment-state-machine.md);
  [payment-provider-interfaces.md](../architecture/payment-provider-interfaces.md);
  [payment-schema.md](../database/payment-schema.md); Stage 8 docs in `docs/stage-8/`

## Context

Stage 2 designed the `psp` and `payments` modules in detail (schema drafts, 42-edge payment machine, provider
interfaces, sandbox scenarios). Stage 8 implements them. The Stage 8 brief adds requirements that the Stage 2
design does not literally contain (several payment attempts under one donation intent, payment sessions,
reconciliation references, recorded webhook rejections) and forbids ledger postings, real payments and real refunds.
No ledger, fees or reconciliation module exists yet, and no provider is selected.

## Decision

### 1. Modules

- **`psp`** (`internal/psp`, imports platform and audit only): provider registry and capabilities, provider
  accounts with secret **references**, `SecretResolver`, adapter interfaces, router, webhook ingestion (verify →
  inbox → dispatch job), rejection log, provider health, and the sandbox simulator (`internal/psp/sandbox`).
- **`payments`** (`internal/payments`, imports platform, audit, campaigns, compliance, risk, psp): donation intents,
  payments, provider calls, provider transactions, references, events, sessions, refund requests, reconciliation
  references, `applyEvent`, jobs and HTTP handlers.
- psp dispatches verified webhooks to a handler that `payments` registers at composition time (no import cycle;
  `psp` never imports `payments`).

### 2. Donation intents are the parent of payments

A **donation intent** (`app.donation_intents`) is the donor's validated request to give a fixed amount in one
currency to one campaign. It is not money received and changes no total. Each **payment** (`app.payment_intents`,
Stage 2's canonical payment, `payment_id` = provider reference and idempotency key, never reused) belongs to one
donation intent. In the API a payment is called a **payment attempt** (brief §11): a donor whose first attempt
FAILED can make a second attempt, which is a new payment with a new `payment_id` (Stage 2 §9 "a retry is always a new
payment"). Stage 2's `app.payment_attempts` keeps its meaning: **one row per outbound provider call** (create,
status query, cancel), committed `IN_FLIGHT` before the call. Rules:

- A new payment attempt is refused while another attempt of the same donation intent is `UNKNOWN`
  (`409 PAYMENT_OUTCOME_UNKNOWN`), in progress (`CREATED`, `PENDING`, `REQUIRES_ACTION`, `AUTHORISED`:
  `409 PAYMENT_IN_PROGRESS`) or succeeded (`409 DONATION_ALREADY_PAID`). The donation intent row is locked `FOR UPDATE`
  while deciding, so concurrent attempt requests serialise.
- A late authoritative success (P15) is always applied, even if another attempt also succeeded; the second success
  opens a `DUPLICATE_SUCCESS` reconciliation reference (refund-and-reversal-flows §3.1) instead of being hidden.
- Amount, currency and campaign of every payment equal its donation intent's (composite FK).
- Donation intent machine `donation_intent`: `'' → OPEN`; `OPEN → PAID | EXPIRED | CANCELLED`;
  `EXPIRED → PAID` and `CANCELLED → PAID` (late authoritative success wins). Status changes are recorded in
  `app.payment_events` (subject `DONATION_INTENT`).

### 3. State vocabulary

Stage 2 states are authoritative (baseline §8). The brief's suggested states map as follows:

| Brief | Stage 8 |
|---|---|
| `CREATED` | `CREATED` |
| `VALIDATED` | No state: validation (eligibility, currency, limits, routing) happens before any row is written; failure is a 4xx and nothing is stored. A stored payment is validated by construction |
| `PROVIDER_PENDING`, `PROCESSING` | `PENDING` (provider acknowledged, not final; ADR-020) |
| `REQUIRES_ACTION`, `SUCCEEDED`, `FAILED`, `CANCELLED`, `EXPIRED`, `UNKNOWN` | Same names |
| `RECONCILIATION_REQUIRED` | Not a state (it would break precedence): `payment_intents.needs_reconciliation = true` plus an open `payment_reconciliation_references` row |

Refund requests use the Stage 2 machine (`REQUESTED`, `PENDING_APPROVAL` = the brief's `UNDER_REVIEW`, `ON_HOLD`,
`APPROVED`, `REJECTED`, `WITHDRAWN`). The brief's `PROVIDER_PENDING … RECONCILIATION_REQUIRED` refund states belong
to `refund_transactions` (Stage 2), which are deferred (§4).

### 4. Deferred (not created in Stage 8)

| Stage 2 object | Why deferred | Stage 8 substitute |
|---|---|---|
| `fee_schedules*`, `platform_fee_minor`, `fee_schedule_version_id` | No fees module; fee model LR-028 open | None. Intents carry no fee |
| ledger postings (`ledger.Post` T1/T2) | No ledger (Stage 10); the brief forbids postings | `payments.LedgerPoster` interface, **nil at runtime**; a test-only recorder asserts exactly one posting request per key. Every success also writes outbox event `payments.payment_succeeded` with the future posting key |
| `refund_transactions` | Needs ledger reservation journals; brief: no refund execution | Refund requests stop at `APPROVED`: the `APPROVED → EXECUTING` edge is **not seeded**, so execution is impossible even in SQL |
| `payment_disputes`, `chargebacks` | Need ledger and holds; LR-020 | Payment machine keeps all 42 edges (Go and SQL identical) but nothing drives `DISPUTED`/`CHARGED_BACK` |
| `donations` (receipts, guest contact) | Guest donations not legally approved (LR-026, LR-035); receipts are a later stage | `donation_intents` holds the donor (authenticated user only) and the public anonymity choice |

### 5. Methods

Method codes stay Stage 2's (`ECOCASH, ONEMONEY, INNBUCKS, OMARI, ZIMSWITCH, BANK_TRANSFER, CARD`). The brief's
provider-neutral categories are derived in Go: `MOBILE_MONEY` (EcoCash, OneMoney, InnBucks, O'Mari),
`ACCOUNT_TO_ACCOUNT` (ZimSwitch), `BANK_TRANSFER`, `CARD`. ZIPIT gets a code only when a provider documents it.

### 6. Execution gates (defence in depth)

1. `psp.LiveExecutionEnabled` is a Go **constant `false`**: the router refuses every non-sandbox provider with
   `PAYMENT_EXECUTION_DISABLED`. Changing it needs a code change, an ADR and the gates below.
2. Kill switch `payments.live_execution.enabled` (`KILL_SWITCH`, seeded `false`, maker-checker) is checked as well.
3. A real provider can be `ENABLED` only with `selection_status = SELECTED` and an ADR (DB CHECK); no real provider
   row exists.
4. The sandbox runs only when `PAYMENTS_SANDBOX_ENABLED=true` **and** `APP_ENV` is not `production` (configuration
   refuses to load otherwise); the database refuses a `PRODUCTION` account for a sandbox provider.
5. Every simulated record is marked: `payment_providers.is_sandbox`, and `is_simulated` on donation intents and
   payments, pinned by composite FK to the provider's `is_sandbox`, so a simulated payment can never be confused with
   a real one.
6. The public campaign donation button stays disabled; there is no donor checkout UI.

### 7. Eligibility

`campaigns` gains action `ACCEPT_DONATION`: campaign `ACTIVE`, visibility `PUBLIC` or `UNLISTED`, an approved version,
currency = goal currency. **Any** compliance restriction (`RESTRICTED` or higher) on the owner, organisation or a
beneficiary blocks new donations, and so does any restriction on the donor. This is stricter than ADR-037's campaign
matrix (where `RESTRICTED` lets existing campaigns continue) because new money is involved. Messages never reveal the
reason (no tipping off).

### 8. Limits

Four kinds, kept separate (brief §27): **technical** (code constants: amount ≥ 1 minor unit and ≤ 10¹² minor
units), **provider** (the sandbox declares its own in its contract; a real provider's limits must come from
approved `PROVIDER` rows in `risk.limits`, and an unconfigured limit excludes the provider), **regulatory** and
**internal risk** (approved `risk.limits` rows `payments.donation.min_amount` / `max_amount`, scope `CURRENCY`;
none is seeded and no threshold is invented). Velocity limits are protective rate-limit policies
(`payments.intent.user`, `payments.intent.campaign`, `payments.attempt.user`).

### 9. Webhooks

Generic ingestion at `POST /api/v1/webhooks/payments/{provider}`: size limit, adapter verification (signature,
timestamp, key slot), one transaction that inserts the inbox row (`UNIQUE (provider_id, provider_event_id)`) and the
River dispatch job, then `200`. A rejected webhook stores **no payload**: only a row in
`app.provider_webhook_rejections` (provider, reason code, body hash, time) for operations, behind the per-IP rate
limit. Dispatch failures retry with backoff and end `DEAD_LETTERED` (staff re-queue only).

## Consequences

- Positive: the Stage 2 design is implemented with its database guarantees; real money cannot move; the
  financial tables that need the ledger arrive with it, unchanged in meaning.
- Negative: the deferred tables mean Stage 10/11 migrations must add fee columns, `refund_transactions`, disputes
  and chargebacks; the `payments.payment_succeeded` events of sandbox payments are never posted.
- The brief's names (`DonationIntent`, `PaymentAttempt`) and Stage 2's names (`payment_intents`, `payment_attempts`)
  differ; [docs/stage-8/payment-architecture.md](../stage-8/payment-architecture.md) has the glossary.
