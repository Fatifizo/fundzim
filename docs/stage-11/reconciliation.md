# Stage 11 — Payout reconciliation and exception queue

**Settlement of disbursements against provider statements is NOT_PERFORMED** (no provider, no statements: PCR-007,
PCR-016; Stage 17). Every run reports so.

## 1. Run (`payouts.reconcile` every 15 min; `POST /admin/payouts/reconciliation/run`, payout.operate)

For every payout changed more than the grace period ago (5 min; configurable for tests):

| Comparison | Mismatch type |
|---|---|
| reservation state vs payout state (e.g. FAILED but reservation not RELEASED; missing reservation) | RESERVATION_MISMATCH |
| journals the state requires, by deterministic key and amount/currency | LEDGER_MISMATCH |
| UNKNOWN past the horizon (15 min, simulator contract) | UNKNOWN_OUTCOME |
| SUBMITTED/PROCESSING for more than 1 h | MISSING_PROVIDER_CONFIRMATION |
| more than one accepted CREATE_PAYOUT, or different provider references | DUPLICATE_PAYOUT_REFERENCE |
| the provider's status for our reference (status API) vs ours; reported amount; reported currency | PROVIDER_STATUS_MISMATCH, AMOUNT_MISMATCH, CURRENCY_MISMATCH |

Each mismatch is an immutable `app.payout_reconciliation_references` row; a payout with mismatches gets one
RECONCILIATION_MISMATCH exception (SEV1 for ledger, amount, duplicate or provider-status mismatches, else SEV2) and a
RECONCILIATION_REQUIRED event. A rerun does not duplicate while that exception is unresolved.

## 2. Exception queue (`app.payout_exceptions`)

Types: UNKNOWN_PROVIDER_OUTCOME, DESTINATION_MISMATCH, INSUFFICIENT_ELIGIBLE_FUNDS, COMPLIANCE_HOLD, APPROVAL_EXPIRED,
PROVIDER_UNAVAILABLE, RECONCILIATION_MISMATCH, DUPLICATE_SUBMISSION_ATTEMPT, LEDGER_POSTING_FAILED, SUBMISSION_RETRIES_EXHAUSTED (KI-S11-20),
LATE_COMPLETION_AFTER_FAILURE (SEV1: the provider paid a payout we released as FAILED), PAYOUT_RETURNED. Each has
severity, source, detection time, owner, status, evidence (ids and codes, no personal data) and an append-only resolution
history (`payout_exception_events`). One unresolved exception per (type, payout); detection fields are immutable.
Workflow: OPEN → INVESTIGATING (owner = the staff member) → RESOLVED (code CONFIRMED_NO_ACTION, CORRECTED_BY_ADJUSTMENT,
PROVIDER_CONFIRMED, DUPLICATE_DETECTION, ESCALATED_INCIDENT, OTHER; note). **Resolving never moves money or changes a
payout**: corrections are ledger adjustments (maker-checker).

Tested: `TestPayoutReconciliationAndExceptionWorkflow` (simulated provider state edited to `failed` after our COMPLETED ⇒
PROVIDER_STATUS_MISMATCH SEV1; rerun no duplicate; investigate/resolve over HTTP; detection fields immutable; the payout
stays COMPLETED).
