# Stage 17 — Ledger reconciliation

## 1. Responsibilities

| Check | Where |
|---|---|
| Every journal balances per currency (L1), entries reference active accounts (L2, L4), projections equal a recompute (L10), … | the ledger's own integrity runs (`ledger.integrity`, hourly) — read by reconciliation |
| Duplicate / orphan / mismatched DONATION_CAPTURED journals, missing postings, suspense balances | the ledger's own reconciliation (`ledger.reconcile`, 15 min) — open exceptions shown |
| Trial balance per currency: total debits = total credits | `ledger.TrialBalance`, shown per currency, never summed |
| A matched record has its expected journal (`payout:{id}:completed`, `payment:{id}:capture` for production payments), in the record's currency and not reversed | reconciliation runs → `ledger_status` POSTED / MISSING (+ MISSING_LEDGER_ENTRY, HIGH) / NOT_POSTED_TEST_PAYMENT / NOT_APPLICABLE |
| The journal a match points at exists, in the same currency | composite FK `recon.matches (ledger_transaction_id, currency) → ledger.ledger_transactions (id, currency)` |

## 2. Immutability

Reconciliation never writes ledger tables (`fundzim_app` has no ledger privilege; archtest). Corrections are
LEDGER_ADJUSTMENT resolutions: the maker's proposal files a `ledger_adjustments` request (MANUAL, named rule
`SETTLEMENT_DISCREPANCY_RESOLVED`, `ADJUSTMENT` or `WRITE_OFF`, reason `RECON_DISCREPANCY`, linked to the exception);
the checker's approval calls `ledger.ApproveAdjustment` (the ledger checks the rule's lines, balance per currency,
approved = proposed lines hash, maker ≠ checker) which posts a new journal. Posted journals are never edited or deleted.
See adjustments.md.

## 3. No settlement postings in Stage 17

`SettlementPostingEnabled = false` (ADR-049 §8). `SETTLEMENT_MATCHED` would debit `psp_settled` and credit
`psp_clearing`, but test payments have no capture journal (ADR-042) — the posting would create balances that mean
nothing. When enabled (accounting review, PRODUCTION ledger, ADR), a settlement posting is made first through
`ledger.PostOnce` with key `settlement:{batch}:{subject}` and the match recorded after, so a crash between the two is
repaired by an idempotent replay.

## 4. Discrepancies detected

Missing journal (MISSING_LEDGER_ENTRY), duplicate journal (ledger reconciliation + idempotency keys), unbalanced journal
(L1), incorrect currency (journal currency vs record, FK), incorrect amount (ledger reconciliation for captures; payouts'
own reconciliation for payout journals), incorrect account mapping (posting rules; L-checks), unexpected posting and
orphan references (ledger reconciliation). Any imbalance is shown as "No — stop and report" on the ledger page and is
never fixed by editing data (CLAUDE.md financial rule 13).
