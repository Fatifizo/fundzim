# Stage 17 — Fee and revenue reconciliation

## 1. Two complementary checks

| Check | Owner | What |
|---|---|---|
| Recompute | fees (Stage 12, `fees.reconcile`, 15 min) | every assessment recomputes from its stored canonical inputs; amounts, currencies, policy versions and fee journals agree; no duplicates |
| Statement fee | reconciliation (Stage 17) | the fee a provider line reports against the provider fee FundZim assessed for that payment or payout |

## 2. Statement fees

`fees.ProviderFeeFacts(source type, ids)` returns the assessed PAYMENT_PROCESSING_FEE (payments) or
PAYOUT_PROCESSING_FEE (payouts) with its basis (QUOTED, ESTIMATED, PROVIDER_REPORTED, PROVIDER_CONFIRMED or UNKNOWN). Each
match records a fee status:

| Status | Meaning |
|---|---|
| MATCHED | the reported fee equals FundZim's assessment |
| MISMATCH | they differ → FEE_MISMATCH (MEDIUM) with both amounts and the basis |
| NOT_REPORTED | the line has no fee (the simulator never reports one) |
| NO_EXPECTATION | FundZim's fee is UNKNOWN or not assessed — **an unknown fee is never 0** |
| NOT_APPLICABLE | failed or non-fee lines |

Settlement batches use FundZim's assessed fee for their expected net where FundZim knows it (settlement-management.md
§5), so a fee difference also shows as SETTLEMENT_SHORT / OVER on the batch.

## 3. Limits

- Provider fee evidence stays SYNTHETIC (sandbox) or UNVERIFIED until a provider is selected (ADR-040, PCR-016); a
  statement never turns a fee VERIFIED and never changes an assessment, a schedule or a journal.
- No provider fee schedule, tax treatment or rate is invented. Standalone FEE lines are PROVIDER_ADJUSTMENT cases.
- **Revenue** is not recognised from reconciliation: production fees are disabled (`fees.ProductionFeesEnabled = false`);
  the fee view shows `BLOCKED`. Fee remittances to FundZim's bank account are matched as evidence only
  (bank-reconciliation.md); their posting (`FEE_REMITTANCE`) waits for the accounting review.
- Refund and chargeback fee adjustments remain fees' own (Stage 12).
