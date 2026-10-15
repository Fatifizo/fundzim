# Stage 12 — Fee reconciliation and reporting

Code: `internal/fees/reconcile.go`, `reports.go`. Job `fees.reconcile` every 15 minutes; staff run it from
`/admin/finance/fees/reconciliation` (`fee.adjust.request`, step-up).

## 1. What a run compares (latest ≤ 5 000 assessments; KI-S12)

| Check | Detects |
|---|---|
| Recompute the stored calculation from its canonical inputs with its algorithm version | `ROUNDING_MISMATCH` (non-reproducible result or inputs) |
| Assessed fee vs its calculation component | `AMOUNT_MISMATCH` (SEV1) |
| Assessment currency vs calculation currency and journal currency | `CURRENCY_MISMATCH` (SEV1) |
| Each referenced journal exists (by key) | `MISSING_POSTING` (SEV1) |
| Journal fee lines vs the recorded fee (revenue credit for a capture, revenue debit for a refund/chargeback, clearing credit for a provider fee, expense debit for a payout fee, settled credit for a chargeback fee) | `AMOUNT_MISMATCH` (SEV1) |
| Journal `fee_schedule_version_id` vs the assessment's policy version | `POLICY_MISMATCH` (SEV1) |
| More than one journal of the same purpose for one assessment | `DUPLICATE_FEE` (SEV1) |
| PENDING_POSTING after the grace period | `MISSING_POSTING` (SEV2) |
| Every assessable component of a calculation has an assessment | `MISSING_FEE` (SEV2) |
| Provider-cost fee: reported fee vs estimate | `AMOUNT_MISMATCH` (SEV2) |
| Provider-cost fee with no report or confirmation after the grace period | `UNRECONCILED_PROVIDER_FEE` (SEV3) — always, in Stage 12: no provider evidence source exists |

Detections are immutable and idempotent (one OPEN item per kind, assessment and journal). Resolving records a note
(`fee.adjust.request`) and never changes a fee, an assessment or the ledger; corrections are adjustments. Settlement
reconciliation against provider statements is `NOT_PERFORMED` (PCR-007).

## 2. Reporting (`GET /api/v1/admin/finance/revenue`, `fee.view`)

Per currency (never combined — no approved FX policy): quoted platform fees (not revenue), assessed platform fees (split
test payments / synthetic donations), refunded and chargeback-reversed platform fees, net manual adjustments, provider fees
estimated and UNKNOWN count, payout fees, chargeback fee exposure, recognised revenue and reversals (TEST ledger
`revenue:platform_fees`), provider fee expense (`expense:psp_processing_fees`), net revenue, and `cash_received:
NOT_AVAILABLE`. Also by month (ledger movements), by payment method and by provider (assessed platform fees). Every report is
labelled "TEST — synthetic data, not FundZim's financial performance". Default window: the last 12 months; at most 3 years.
