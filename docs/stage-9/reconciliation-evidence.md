# Stage 9 — Reconciliation evidence capture

## 1. Provider evidence log

`app.payment_provider_evidence` (append-only; UPDATE/DELETE/TRUNCATE refused) holds one row per provider observation
of a payment: synchronous create/status responses (`SYNC`, `POLL`) and webhooks (`WEBHOOK`). Columns: environment,
source, call or inbox reference, provider event id, provider reference, **raw status verbatim**, mapped status and
mapping version (unmapped ⇒ `UNKNOWN`, `mapped = false`), reported amount and currency, provider timestamp, observed
time, consistency, and the contradicted row. `payment_transactions` still keeps only the latest observation.

| Consistency | Meaning |
|---|---|
| `FIRST` | First evidence for the payment |
| `CONSISTENT` | Same mapped status as the latest evidence |
| `PROGRESSION` | A different status that does not contradict (PENDING → SUCCEEDED; FAILED → SUCCEEDED is a late success handled by the state machine's own `LATE_SUCCESS` reference) |
| `CONTRADICTS` | A failure-family status (FAILED, EXPIRED, CANCELLED) after success-family evidence, or a reported amount/currency different from an earlier report |

## 2. Escalation of late contradictory evidence

A `CONTRADICTS` row is never discarded and never changes the payment by itself (the state machine records the
observation as not applied, e.g. `LOWER_RANK`). It:

1. opens a reconciliation reference `CONTRADICTORY_EVIDENCE` (one open per payment and reason) and flags the payment
   `needs_reconciliation`;
2. writes outbox `payments.evidence_contradiction` and `payments.reconciliation_required`;
3. logs an `ALERT` line (no payload).

Staff see it on the payment page (evidence table) and in the reconciliation queue. Resolution needs provider
statements (PCR-007/016, Stage 17).

## 3. What is not evidence

Settlement amounts, fees and balances are not recorded (no source; PCR-007, PCR-016). Provider events are never
treated as ledger balances. Simulator evidence is always `LOCAL_SIMULATOR` (DB trigger: evidence environment equals
the payment's).
