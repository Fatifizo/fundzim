# Stage 10 — Posting rules

Rules live in `ledger.ledger_posting_rules` (reference data, migration-only) with their allowed account patterns in
`ledger_posting_rule_lines`. Each rule has `(code, version)`; journals record `posting_rule_version` and must use the
current (maximum) version. A change is a new version added by migration; old journals keep their version. All rules
are version 1 in Stage 10. Full list with idempotency key formats: section 2 of `migrations/20261013120000_ledger.sql`.

## 1. Brief event names → design rules

| Brief event | Rule(s) | Stage 10 state |
|---|---|---|
| DONATION_CONFIRMED | `DONATION_CAPTURED` (and `PSP_FEE` for provider fees) | Exercised by synthetic fixtures only; payments are NOT_ELIGIBLE / BLOCKED |
| SETTLEMENT_CONFIRMED | `SETTLEMENT_MATCHED`; differences `SETTLEMENT_DISCREPANCY` → suspense | Synthetic only; no settlement evidence exists |
| FUNDS_RELEASED | `RELEASE`, `RELEASE_WITH_RESERVE`, `RESERVE_RELEASE` | Synthetic only (`RELEASE`) |
| REFUND_APPROVED / REFUND_COMPLETED / REFUND_FAILED | `REFUND_RESERVED`, `REFUND_SETTLED`, `REFUND_FAILED` (+ fee true-up rules) | Synthetic only; no refund executes (KI-S9-07) |
| CHARGEBACK_* | `DISPUTE_OPENED`, `DISPUTE_HELD`, `DISPUTE_RELEASED`, `DISPUTE_WON`, `DISPUTE_LOST`, `DISPUTE_FEE`, `DISPUTE_FUNDING*` | Rules exist; no source (disputes are a later stage) |
| CAMPAIGN_FROZEN / UNFROZEN | `CAMPAIGN_FROZEN`, `CAMPAIGN_UNFROZEN` | Rules exist; not wired to campaigns (Stage 11+) |
| PAYOUT_* | `PAYOUT_RESERVED`, `PAYOUT_SUBMITTED`, `PAYOUT_COMPLETED`, `PAYOUT_FAILED`, `PAYOUT_RELEASED`, `PAYOUT_REVERSED`, `PAYOUT_FEE` | Rules exist; **not used** (Stage 11) |
| FEE_REMITTANCE, RECOVERY_RECEIVED | as named | Rules exist; no source |
| ADJUSTMENT, WRITE_OFF, REVERSAL | approval-requiring rules | Maker-checker only ([reversals.md](reversals.md)) |

## 2. Donation accounting (synthetic illustration)

Gross capture of USD 10.00 (fixture `donations`):

| Account | Dr | Cr |
|---|---:|---:|
| `asset:psp_clearing:<synthetic provider>` | 10.00 | |
| `liability:campaign_unsettled:<synthetic campaign>` | | 10.00 |

A provider fee of USD 0.50 borne by the campaign (`PSP_FEE`): Dr campaign_unsettled 0.50, Cr psp_clearing 0.50.
Settlement (`SETTLEMENT_MATCHED`, fixture `settlement`): Dr psp_settled, Cr psp_clearing. Release (`RELEASE`):
Dr campaign_unsettled, Cr campaign_payable. Platform fee lines are designed in `DONATION_CAPTURED` but **no fee is
configured** (fees are not a Stage 10 item; LR items open), so the fixtures carry none.

## 3. Constraints from the brief

- Payment providers and webhooks never write journals; only the ledger service posts.
- Provider-reported success is not settlement: `DONATION_CAPTURED` debits a *claim* (`psp_clearing`), never
  `psp_settled`; moving to `psp_settled` needs `SETTLEMENT_MATCHED` with settlement evidence.
- Evidence is never elevated automatically: a journal's `evidence_class` is fixed when posted.
