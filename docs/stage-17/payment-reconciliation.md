# Stage 17 — Payment reconciliation

Payments reconcile four ways where evidence exists: **payment record ↔ provider transaction line ↔ provider settlement
line ↔ ledger journal**. In Stage 17 the provider lines are TEST evidence and test payments have no journal (ADR-042).

## 1. What is read

`payments.ReconFacts(provider, ids, provider refs)` returns id, status, amount, currency, execution environment, every
PAYMENT reference the provider gave (`payment_provider_references`), creation time and first SUCCEEDED time.
`payments.ReconSucceededInWindow(provider, currency, from, to)` lists payments that succeeded in a window (for
MISSING_SETTLEMENT). Reconciliation never queries payment tables.

## 2. Matching keys

| Key | Source | Strength |
|---|---|---|
| merchant reference = our payment id | statement line `merchant_ref` | strong (we generated it) |
| provider reference | statement line `provider_ref` vs `payment_provider_references` | strong (unique per provider) |
| amount, currency | verification only | never a key |
| booking date | verification of one-reference matches (rule set window) | weak |
| idempotency key | not exposed to providers by FundZim's contract | not used |

## 3. Outcomes

See matching-engine.md §3. Summary for payments: exact or rule-based matches; AMOUNT_MISMATCH; CURRENCY_MISMATCH;
STATUS_MISMATCH (provider and FundZim disagree on success/failure or a refund/chargeback FundZim has not recorded);
DUPLICATE_TRANSACTION (a second line for one payment, or repeated evidence from overlapping reports is deduplicated at
import); MISSING_PAYMENT (a line naming no payment — unknown funds); MISSING_SETTLEMENT (a succeeded payment absent from a
statement covering its time after the horizon); UNKNOWN_PAYMENT_OUTCOME.

## 4. Duplicates

| Duplicate | Detection |
|---|---|
| the same file | content hash unique (refused) |
| the same line in two statements | line identity + content hash (recorded once) |
| the same line with other content | next revision, CONFLICTING_EVIDENCE |
| two lines for one payment | DUPLICATE_TRANSACTION |
| repeated webhook evidence | payments' own provider evidence log (unique per provider event) — unchanged |
| two postings | ledger idempotency keys (unique); the ledger's own reconciliation reports duplicates |

Nothing is deleted: duplicates are cases with evidence.

## 5. Payments' own reconciliation references

Payments keeps opening `payment_reconciliation_references` (UNKNOWN past horizon, amount mismatch, contradictions, …).
When an approved PROVIDER_OUTCOME_APPLIED resolution moves an UNKNOWN payment to a final state, payments auto-resolves
its UNKNOWN_HORIZON_EXCEEDED / NOT_FOUND_AFTER_HORIZON references (existing behaviour in `apply`). Other reference kinds
stay with payments; the reconciliation exception and the payments reference are cross-referenced by payment id.

## 6. Production

Production payments cannot exist (`ck_*_no_production_stage9`); when they can, a matched production payment must have
its `payment:{id}:capture` journal, and settlement postings (`SETTLEMENT_MATCHED`) wait for the accounting review and a
new ADR (ADR-049 §8).
