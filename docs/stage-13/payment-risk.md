# Stage 13 — Donation and payment risk

## 1. Signals (donor = the signed-in user; intents always have a donor account)

| Signal | Source | Facts |
|---|---|---|
| DONATION_INTENT_CREATED | `payments.donation_intent_created` | `campaign_id`, `donation_intent_id`, `amount_minor`, `currency` |
| PAYMENT_FAILED | `payments.payment_status_changed` → FAILED (authoritative failure only; UNKNOWN is never a failure) | `payment_id`, `campaign_id` |
| PROVIDER_EVIDENCE_CONTRADICTION | `payments.evidence_contradiction` (Stage 9 evidence recording) | — |

No provider-reported risk signal is invented: no provider is selected (ADR-040); provider fraud/risk flags (PCR-005) will
become signals when a provider documents them.

## 2. Velocity controls (durable)

Velocity is computed from the signal store in PostgreSQL inside the evaluation transaction — never from in-memory counters
(which are lost on restart and differ per process). Examples (configuration, maker-checker):

| Control | Rule |
|---|---|
| intents per minute / hour / day | `signal_count(DONATION_INTENT_CREATED) window 60 / 3600 / 86400` on the donor |
| distinct campaigns per donor | `distinct_count(DONATION_INTENT_CREATED.campaign_id) in 1 day` |
| repeated failed attempts | `signal_count(PAYMENT_FAILED) in 1 hour` |
| amount pattern | `fact_compare(DONATION_INTENT_CREATED.amount_minor ≥ X)` combined with counts |

The HTTP rate limits of Stage 8 (`payments.intent.user`, `payments.attempt.user`) remain the first line of defence; risk
velocity rules route patterns to people and never silently refuse a donor.

## 3. Enforcement

`payments.CreateIntent` and `payments.CreateAttempt` refuse with `409 DONATIONS_ON_HOLD` ("Donations to this campaign are
temporarily unavailable while it is reviewed.") while an enforced CAMPAIGN_HOLD or DONATION_HOLD covers the campaign, or a
DONATION_HOLD covers the donor. A hold-registry error stops the request (fail closed, `TestCheckHoldsFailsClosed`). Holds
never change a payment's state: payments in flight follow the payment state machine; refunds remain maker-checker and
non-executable (Stage 8/9).
