# Stage 17 — Unknown outcomes

Unknown ≠ failed (CLAUDE.md financial rule 9). A timeout, a connection failure, a missing or conflicting callback, a
missing or delayed settlement leaves the payment or payout UNKNOWN (or unmatched) until authoritative evidence resolves it.

## 1. Who resolves what

| Evidence | Payment | Payout |
|---|---|---|
| webhook (verified) | `apply` source WEBHOOK (Stage 8) | `applyStatus` source WEBHOOK (Stage 11) |
| status query | source POLL / SYNC | source POLL |
| statement line (Stage 17, TEST) | **approved PROVIDER_OUTCOME_APPLIED resolution** → `payments.ApplyReconciliationOutcome` → `apply` source RECON | **approved resolution** → `payouts.ApplyReconciliationOutcome` → `applyStatus` source RECON |
| silence | stays UNKNOWN; payments opens UNKNOWN_HORIZON_EXCEEDED after 72 h | stays UNKNOWN; RECONCILIATION_MISMATCH after 15 min; never resubmitted |

## 2. Why statement evidence needs a second person

Stage 17 statements are staff-supplied files: one person could otherwise move money states with a document they made.
So a statement line for an UNKNOWN record opens UNKNOWN_PAYMENT_OUTCOME (HIGH) / UNKNOWN_PAYOUT_OUTCOME (CRITICAL) —
plus the outbox event `reconciliation.unknown_outcome_escalated` (in-app notice to holders of `reconciliation.resolve`) —
and **nothing is applied automatically**. The resolution requires: the source reference (the line and its import),
evidence type (the import's evidence record, SHA-256), evidence time (the line's booking time), a justification (≥ 20
characters), the proposer (maker), a different approver (checker, never a linked account, step-up), approval within
24 h against the proposal hash, and the audit trail (`reconciliation.resolution.*`, the owning module's events).

## 3. What the owning module does with it

- Payments: `apply` records the observation in the provider evidence log (contradictions escalated, never discarded),
  refuses an amount/currency difference as an anomaly, applies only its own edges (RECON may drive SUCCEEDED or FAILED),
  and applies a given evidence key once. On a terminal outcome it closes its own UNKNOWN references.
- Payouts: `applyStatus` with the payout locked; Y10 (completed) or Y11 (failed); the reservation follows the edge;
  journals follow (`syncLedger`); an amount/currency difference opens a SEV1 exception and is not applied; the same
  evidence key is applied once; **nothing is resubmitted**.

If the module does not apply the evidence (not an edge any more, an anomaly), the resolution is EXECUTED with the module's
answer recorded and the exception returns to INVESTIGATING.

## 4. Production

When a provider is selected, the authenticated provider statement API and settlement reports become sources with their
own authority; whether statement evidence may then resolve UNKNOWN outcomes without a checker is a decision for that
stage (it is not taken here).
