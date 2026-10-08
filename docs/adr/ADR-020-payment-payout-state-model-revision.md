# ADR-020: Payment and payout state model revision (UNKNOWN, AUTHORISED, payout states)

- **Status:** Accepted
- **Date:** 2026-10-08
- **Deciders:** Technical lead
- **Stage:** 1 (design). Amends [ADR-007](ADR-007-payment-provider-abstraction.md) and
  [PAYMENTS.md](../PAYMENTS.md). Implemented in Stages 8, 10 and 11.

## Context

Stage 0 represented an indeterminate provider outcome as `PENDING` with an `outcome_unknown` flag
([PAYMENTS.md](../PAYMENTS.md) §8) and had no state for card authorisation without capture. Its payout states
were `REQUESTED, UNDER_REVIEW, APPROVED, PROCESSING, PAID, FAILED, CANCELLED, RETURNED` (§13). The Stage 1
brief requires FAILED, PENDING, UNKNOWN, EXPIRED and CANCELLED to be distinct, and specifies the payout state
names below. A flag is easy to forget in a query ("show pending payments") and in metrics; a distinct state
forces every consumer to handle the case.

## Decision

### Payments (supersedes PAYMENTS.md §6 diagram and §6.1 rank table, and §8 handling of unknown outcomes)

1. **States:** `CREATED, PENDING, REQUIRES_ACTION, AUTHORISED, UNKNOWN, SUCCEEDED, FAILED, CANCELLED, EXPIRED,
   PARTIALLY_REFUNDED, REFUNDED, DISPUTED, CHARGED_BACK`.
2. **`UNKNOWN` replaces the `outcome_unknown` flag.** It means FundZim cannot determine whether the provider
   received or processed a request (timeout, connection reset after send, 5xx, malformed or unverifiable
   response). It is resolved **only** by an authoritative status query, verified webhook or reconciliation.
   Edges: `CREATED → UNKNOWN`; `PENDING → UNKNOWN` (internal source only); `UNKNOWN → PENDING |
   REQUIRES_ACTION | AUTHORISED | SUCCEEDED | FAILED | EXPIRED | CANCELLED`. **A timeout never produces
   FAILED.**
3. **`AUTHORISED`** is used only where a provider separates authorisation from capture (capability
   `separate_capture`); it posts nothing to the ledger. `SUCCEEDED` means captured/confirmed.
4. **Settlement is not a payment state.** It is tracked on settlement records and in the ledger
   ([ADR-014](ADR-014-accounting-separated-from-custody.md)).
5. **Rank table:** CREATED 0, UNKNOWN 5, PENDING 10, REQUIRES_ACTION 20, AUTHORISED 30,
   FAILED/CANCELLED/EXPIRED 50, SUCCEEDED 60, PARTIALLY_REFUNDED 70, DISPUTED 80, REFUNDED 90,
   CHARGED_BACK 95. The Stage 0 rule stays: a move needs higher rank **and** an allowed edge, with listed
   exceptions; early events are parked.
6. **Late authoritative success** may move `FAILED | EXPIRED | CANCELLED → SUCCEEDED` (risk signal raised);
   `SUCCEEDED → CHARGED_BACK` is a direct edge for involuntary reversals. `CHARGED_BACK` carries
   `reversal_kind ∈ {CARD_CHARGEBACK, PROVIDER_REVERSAL}` and is never recorded as `REFUNDED`.

Full transition table (P1–P20): [transaction-lifecycle.md](../payments/transaction-lifecycle.md).

### Payouts (supersedes PAYMENTS.md §13 state names and diagram)

7. **States:** `PAYOUT_REQUESTED, PENDING_REVIEW, APPROVED, SUBMITTED, PROCESSING, COMPLETED, FAILED,
   REJECTED, CANCELLED, UNKNOWN, REVERSED`.

   | Stage 0 | Stage 1 |
   |---|---|
   | `REQUESTED` | `PAYOUT_REQUESTED` |
   | `UNDER_REVIEW` | `PENDING_REVIEW` |
   | `PAID` | `COMPLETED` |
   | `RETURNED` | `REVERSED` |
   | — | `SUBMITTED` (new: instruction committed / call in progress) |
   | — | `UNKNOWN` (new: outcome indeterminate) |
   | — | `REJECTED` (new: FundZim refused; distinct from owner/staff `CANCELLED`) |

8. **Rules:** provider reference = `payout_id`, reused on every retry; a payout in `UNKNOWN` is never
   resubmitted, only resolved by status query or reconciliation; retry after authoritative `FAILED` is a new
   payout request with a new id; holds can stop a payout up to `APPROVED`; eligibility is re-checked
   immediately before `SUBMITTED`. Ledger effects per state follow ADR-014
   (`campaign_payout_pending` → `payout_in_transit:{p}` → out of `psp_settled:{p}`).

Full transition table (Y1–Y14): [payout-lifecycle.md](../payments/payout-lifecycle.md).

### ADR-007

9. ADR-007's state summary ("an unknown outcome after a timeout stays `PENDING`") is **amended** by this ADR.
   ADR-007's other decisions (layering, capabilities, webhooks, idempotency, hosted flows) are unchanged.

## Consequences

### Positive
- Indeterminate outcomes are visible in every query, dashboard and alert.
- Payout states distinguish "we refused" from "cancelled" and "submitted" from "provider acknowledged",
  which is what operators need during incidents.

### Negative / costs
- Stage 0 text and diagrams must be updated to the new names (lead task).
- More states to test (Stage 19 failure-injection suites).

### Follow-up work
- Update [PAYMENTS.md](../PAYMENTS.md) §6, §6.1, §8, §13; [LEDGER.md](../LEDGER.md) §6.5–6.7 names;
  [TESTING.md](../TESTING.md) scenarios referencing `PAID`/`RETURNED`/`outcome_unknown`;
  [PRODUCT.md](../PRODUCT.md) and [ROADMAP.md](../ROADMAP.md) mentions.

## Alternatives considered

| Alternative | Why rejected |
|---|---|
| Keep `outcome_unknown` flag on PENDING | Easy to miss in queries and metrics; brief requires distinct states. |
| Treat timeouts as FAILED after N seconds | Causes double payments when the provider actually succeeded. |
| Model AUTHORISED as SUCCEEDED | Posts uncaptured funds to the ledger; authorisations can lapse. |

## Security implications
Only authoritative sources can resolve UNKNOWN; external events cannot move a payment back into UNKNOWN.
Staff-driven FAILED for a payout requires provider evidence and is audited.

## Financial implications
No posting at AUTHORISED or UNKNOWN; payout money stays in `payout_in_transit` while UNKNOWN, so it can be
neither re-paid nor released until resolved. Duplicate payout prevention relies on idempotency keys, unique
constraints and the single in-flight payout rule.

## Related
[transaction-lifecycle.md](../payments/transaction-lifecycle.md), [payout-lifecycle.md](../payments/payout-lifecycle.md),
[payout-eligibility-and-controls.md](../payments/payout-eligibility-and-controls.md), [PAYMENTS.md](../PAYMENTS.md),
ADR-007, ADR-014.
