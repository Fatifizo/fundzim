# Stage 8 — Donation intents

A **donation intent** (`app.donation_intents`) is a signed-in donor's validated request to give a fixed amount in one
currency to one campaign. It is **not** money received and changes no total; campaigns still hold no financial state.
Each payment ("payment attempt" in the API, `app.payment_intents`) belongs to one donation intent
([ADR-039](../adr/ADR-039-payment-abstraction-donation-intents-and-sandbox-execution.md) §2). Code:
`internal/payments/intents.go`, `attempts.go`. Contract: [interface-contracts.md](interface-contracts.md) §2.

In Stage 8 every donation intent is a **simulation** (`is_simulated = true`): real execution is disabled, so intents
can be created only while the sandbox runs; otherwise `POST /donation-intents` returns 503 `PAYMENTS_UNAVAILABLE`.
There is no donor checkout page; the API is exercised by tests and the staff UI shows the records.

## 1. Lifecycle

```text
'' ──► OPEN ──► PAID        (a payment SUCCEEDED; event source INTERNAL)
        │  ├──► CANCELLED   (donor, only with no live / unknown / paid payment)
        │  └──► EXPIRED     (sweep: expires_at passed and no live / unknown / paid payment)
EXPIRED ──► PAID, CANCELLED ──► PAID   (late authoritative success wins)
```

| Rule | Implementation |
|---|---|
| Window | `expires_at = created_at + 24 h` (`DonationIntentTTL`); new attempts only while `OPEN` and not past `expires_at` |
| History | Every status change has a `payment_events` row (subject `DONATION_INTENT`) in the same transaction (deferred trigger) |
| Immutable | Campaign, donor, amount, currency, simulation flag, anonymity choice, idempotency key, correlation id (trigger) |
| No delete | `forbid_mutation` on delete; no grant |
| `PAID` stays `PAID` | Even if the payment is later refunded; `PAID` only from source `INTERNAL` (DB CHECK) |

## 2. Statuses shown to the donor

`DonationIntent.status` is `OPEN`, `PAID`, `EXPIRED` or `CANCELLED`. Each `PaymentAttempt` carries its real payment
status and a donor-safe `message` that never says money was received before `SUCCEEDED`
(`TestDonorMessagesNeverClaimMoneyBeforeSuccess`); `UNKNOWN` reads "We're confirming this payment with the provider.
Please don't pay again." `GET /donation-intents/{id}/status` returns the intent status, the latest attempt and its
message.

## 3. Eligibility gates (checked on intent creation **and** again on every attempt)

| # | Gate | Source | Refusal |
|---|---|---|---|
| 1 | Signed-in personal account (`httpx.User()` policy) | auth | 401; staff sessions 403 `PERMISSION_DENIED` (the contract names `ACCOUNT_KIND_NOT_ALLOWED`, KI-S8-22) |
| 2 | Donor has **no** compliance restriction of any level | `compliance.Service.Restrictions` through `DonorRestrictions` (fail closed) | 403 `DONATION_NOT_AVAILABLE` (no reason given) |
| 3 | Campaign passes `ACCEPT_DONATION`: status `ACTIVE`, visibility `PUBLIC` or `UNLISTED`, an approved version, and **no** restriction of any level on the campaign, its owner, its organisation or its beneficiary | `campaigns.Service.EvaluateDonation` | 422 `CAMPAIGN_NOT_ACCEPTING_DONATIONS` (also for an unknown campaign; reasons never disclosed) |
| 4 | Currency usable and equal to the campaign's goal currency | §4 | 422 `CURRENCY_NOT_AVAILABLE` / `CURRENCY_NOT_ACCEPTED` |
| 5 | Limits | §5 | 422 `AMOUNT_OUT_OF_RANGE` |
| 6 | Payments available: sandbox running (real execution disabled) | `psp` gates | 503 `PAYMENTS_UNAVAILABLE` |
| 7 | At least one method routes for this currency (intent) / the chosen method routes for this amount (attempt) | `psp.AvailableMethods`, `psp.Route` | 422 `PAYMENT_METHOD_UNAVAILABLE`, `AMOUNT_OUT_OF_RANGE`; 503 `PAYMENT_EXECUTION_DISABLED`, `PAYMENT_PROVIDER_UNAVAILABLE` |

The restriction rule is **stricter** than ADR-037's campaign matrix (where `RESTRICTED` lets existing campaigns
continue) because new money is involved (ADR-039 §7). Gates 2–5 are evaluated before anything is written: a refused
request stores nothing (the brief's `VALIDATED` is not a state).

## 4. Currency rules

- Money is `amount_minor` (a string of digits `^[1-9][0-9]{0,17}$`, never a float) plus an upper-case ISO code.
- The currency must be in `money.UsableCurrencies` (enabled **and** `minor_units_verified`). **USD** qualifies.
  **ZWG** is refused with `CURRENCY_NOT_AVAILABLE` because its minor unit is unverified (LR-043, PCR-018).
- The currency must equal the campaign's goal currency (`CURRENCY_NOT_ACCEPTED`). FundZim never converts.
- Every payment of an intent has the intent's amount, currency and campaign (composite FK).

## 5. Limits (four kinds, kept separate — ADR-039 §8)

| Kind | Source | Applied by | Stage 8 values |
|---|---|---|---|
| Technical | Code constants `TechMinAmountMinor = 1`, `TechMaxAmountMinor = 10¹²` | `payments.checkLimits` | 1 – 1 000 000 000 000 minor units |
| Provider | `Adapter.Limits(method, currency)` | `psp.Route` | Sandbox contract: USD 1.00 – 10 000.00 ([provider-interface.md](provider-interface.md) §6.2); a provider without a known limit is excluded |
| Regulatory | Approved `risk.limits` rows `payments.donation.min_amount` / `max_amount`, type `REGULATORY`, scope `GLOBAL` or `CURRENCY` | `payments.checkLimits` (via `risk.Service.MoneyLimits`) | **None seeded**; no threshold is invented |
| Internal risk | Same keys, type `INTERNAL_RISK` | `payments.checkLimits` | **None seeded** |

`PROVIDER`-type rows under these keys are ignored by `checkLimits` (provider limits belong to routing; KI-S8-26).
Velocity is limited by protective rate-limit policies, not money limits:

| Policy | Key | Limit |
|---|---|---|
| `payments.intent.user` | donor | 20 / hour |
| `payments.intent.campaign` | campaign | 600 / hour |
| `payments.attempt.user` | donor | 30 / hour |

Rate-limit errors are 429 with the limiter's real `Retry-After` (`RetryAfter()`).

## 6. Idempotency and retries

- `POST /donation-intents` and `POST /donation-intents/{id}/attempts` require `Idempotency-Key` (UUID) — the
  platform middleware in **REQUIRED** mode, plus database backstops ([idempotency.md](idempotency.md)).
- A **retry** of the same request (same key) returns the same intent or payment; no second provider call.
- A **new attempt** (new key) after a definite outcome is a new payment with a new `payment_id` and
  `attempt_number + 1` (Stage 2 §9: a retry is always a new payment). It is refused while another attempt of the
  intent is `UNKNOWN` (409 `PAYMENT_OUTCOME_UNKNOWN`), in progress — `CREATED`, `PENDING`, `REQUIRES_ACTION`,
  `AUTHORISED` (409 `PAYMENT_IN_PROGRESS`) — or paid (409 `DONATION_ALREADY_PAID`). The intent row is locked
  `FOR UPDATE` while deciding, so concurrent attempts serialise (`TestPaymentsConcurrentAttemptsYieldOnePayment`).
- A late success on a second attempt is applied and opens a `DUPLICATE_SUCCESS` reconciliation reference instead of
  being hidden.

## 7. Donor API

| Method & path | Notes |
|---|---|
| `POST /api/v1/donation-intents` **IK** | `{campaign_id, amount_minor, currency, is_anonymous?}` → 201 `DonationIntent` |
| `GET /api/v1/donation-intents` | Own intents, newest first, `{items, next_cursor}` |
| `GET /api/v1/donation-intents/{donation_intent_id}` | Own only (404 otherwise), with attempts |
| `POST …/{donation_intent_id}/attempts` **IK** | `{method}` → 201 `PaymentAttempt` with the real status after the provider call (never a 5xx for a provider outcome) |
| `GET …/{donation_intent_id}/attempts` | `{items, next_cursor: null}` |
| `GET …/{donation_intent_id}/status` | Poll-friendly status |
| `POST …/{donation_intent_id}/cancel` | Only `OPEN` with no live, unknown or paid attempt |
| `POST /api/v1/payment-sessions/{session_id}/return` | `{token}` (single use, bound to the donor) → marks the session `RETURNED`, schedules a status query, returns `{attempt}`. **Never confirms a payment** |
| `GET /api/v1/payment-methods?campaign_id=&currency=` | Public; methods the router would accept now; empty when nothing routes or the campaign is not eligible |
| `GET /api/v1/payment-providers/capabilities` | Public; current capability rows of usable providers; no accounts or secrets; `simulated` flag |

Every response is `Cache-Control: no-store`. Donor-scoped reads are filtered by `donor_user_id` in SQL (no IDOR).
`is_anonymous` is a public-display choice only: the payment is always tied to the authenticated donor (LR-026).

## 8. Data stored about the donor

Campaign, donor user id, amount, currency, chosen method, anonymity choice, correlation id. No device data, no
payer PII, no marketing consent, no terms version (no donation terms exist yet, LR-022). A payer instrument reported
by a provider is stored only as an HMAC fingerprint and a masked value (LR-035).
