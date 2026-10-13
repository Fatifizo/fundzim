# Stage 8 — Reconciliation foundation

Stage 17 implements reconciliation against settlement data ([ADR-030](../adr/ADR-030-reconciliation-architecture.md),
[reconciliation-schema.md](../database/reconciliation-schema.md)). Stage 8 records **why** a payment needs it and
**what the provider reported**, and never marks anything reconciled: no settlement data exists.

## 1. Data

`payment_intents.needs_reconciliation` (flag, indexed) plus `app.payment_reconciliation_references`: payment, provider,
provider call, inbox event, provider reference, reason, expected amount + currency, reported amount + currency,
provider raw status, evidence time, status, resolution. Evidence columns are immutable (trigger); at most one open
reference per (payment, reason) (partial unique index). Outbox `payments.reconciliation_required`, metric
`payment_reconciliation_required_total{reason}`.

| Reason | Raised when |
|---|---|
| `UNKNOWN_HORIZON_EXCEEDED` | A payment is still `UNKNOWN` after the horizon (72 h default; PCR-010); automatic polling stops |
| `NOT_FOUND_AFTER_HORIZON` | As above, and the last status query said not found |
| `AMOUNT_MISMATCH` / `CURRENCY_MISMATCH` | A provider reports success with a different amount or currency (never applied) |
| `ANOMALY` | A provider observation that is not an edge from a higher state (e.g. a forbidden jump) |
| `UNMAPPED_STATUS` | The adapter cannot map the provider status (also inbox `UNRECOGNISED`) |
| `LATE_SUCCESS` | Authoritative success after FAILED/EXPIRED/CANCELLED (P15, applied) |
| `DUPLICATE_SUCCESS` | A second payment of the same donation intent succeeded (refund rule; applied, never hidden) |
| `PARKED_DEADLINE_EXCEEDED` | An event parked for its prerequisite past 72 h (never discarded) |

## 2. Statuses (machine `payment_reconciliation_reference`)

`OPEN → AWAITING_SETTLEMENT_DATA` (staff, `payment.operate` + step-up, audited) and `OPEN | AWAITING_SETTLEMENT_DATA →
RESOLVED_BY_PROVIDER_STATUS` (system only: when an authoritative provider status resolves an `UNKNOWN_HORIZON_EXCEEDED`
/ `NOT_FOUND_AFTER_HORIZON` reference; the note says "settlement not reconciled (Stage 17)"). There is no "reconciled"
status and no staff action that changes a payment's status.

## 3. What Stage 17 adds

Statement imports per provider and currency, matching by provider reference and our reference, settlement batches,
discrepancy cases with maker-checker resolution and correcting journals, and `SETTLEMENT_RECONCILED` health events for
payout eligibility (EC-20).
