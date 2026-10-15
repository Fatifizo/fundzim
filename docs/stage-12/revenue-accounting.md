# Stage 12 — Revenue accounting

**ACCOUNTING_REVIEW_REQUIRED.** No accounting approval exists (KI-S10-02, LR-002); tax treatment is open (LR-016, LR-045,
LR-059). Everything below runs in the TEST ledger only; production revenue recognition is BLOCKED.

## 1. Accounts and rules (Stage 10, unchanged)

No new account class or posting rule is added. Fee journals use the existing versioned rules with `source_system = 'fees'`,
`SIMULATED` evidence, deterministic keys and the policy version in `ledger_transactions.fee_schedule_version_id`.

| Event (TEST) | Rule | Lines | Key | Trigger and evidence |
|---|---|---|---|---|
| Capture with platform fee | `DONATION_CAPTURED` | Dr `asset:psp_clearing` (D + P_donor + V_donor) · Cr `liability:campaign_unsettled` (D − P_organiser + V_donor) · Cr `revenue:platform_fees` (P) | `fee_fixture:{donation}:capture` | fixture (real capture would need verified provider evidence: production BLOCKED) |
| Provider fee at source | `PSP_FEE` | Dr `campaign_unsettled` (V_donor + V_organiser) · Dr `expense:psp_processing_fees` (V_platform) · Cr `psp_clearing` (V) | `…:psp_fee` | known (estimated) provider fee |
| Settlement / release | `SETTLEMENT_MATCHED`, `RELEASE` | settled ← clearing (charge − V); unsettled → payable (allocation) | `…:settlement`, `…:release` | fixture `settle: true` |
| Refund with fee reversal | `REFUND_RESERVED`, `REFUND_SETTLED` | Dr campaign (R − Fr_organiser) · Dr `revenue:platform_fees` (Fr) · Cr `liability:refund_payable` (R + Fr_donor); then payable → provider | `…:refund:{id}:reserved|settled` | [refund-adjustments.md](refund-adjustments.md) |
| Chargeback with fee reversal | `DISPUTE_OPENED` | Dr campaign (C − Fr_organiser) · Dr revenue (Fr) · Cr provider (C + Fr_donor) | `…:dispute:{id}:opened` | [chargeback-fees.md](chargeback-fees.md) |
| Chargeback fee | `DISPUTE_FEE` | Dr expense (platform part) · Dr `campaign_payable` (organiser part) · Cr `psp_settled` | `…:dispute:{id}:fee` | known chargeback fee |
| Payout fee (platform-absorbed) | `PAYOUT_FEE` | Dr expense · Cr `psp_settled` | `payout:{id}:fee` | TEST payout completed |

Every journal balances (posting engine), stays in one currency, is immutable and idempotent; corrections are new
journals. Real test payments are never posted (ADR-042): their assessments say `NOT_POSTED_TEST_PAYMENT`.

## 2. Revenue recognition (TEST)

The Stage 1 custody model credits `revenue:platform_fees` in the capture journal; Stage 12 keeps that trigger for synthetic
fixtures and calls it "recognised (TEST)". Whether production revenue is recognised at capture, at settlement or at fee
remittance (`FEE_REMITTANCE`) is an accounting decision (handover). Cash received is not reported: there is no settlement
evidence. A quote, an assessment or an estimate is never revenue.

## 3. Synthetic example (test fixture, not FundZim's commercial rates)

Donation USD 100.00, platform fee USD 5.00 (5 %, organiser-covered), provider fee USD 2.00 (2 %, organiser-covered):
capture Dr clearing 100.00 / Cr unsettled 95.00 / Cr revenue 5.00; provider fee Dr unsettled 2.00 / Cr clearing 2.00;
settlement 98.00; release 93.00 to `campaign_payable`. Partial refund USD 40.00: fee reversal 2.00; Dr payable 38.00 /
Dr revenue 2.00 / Cr refund payable 40.00. Verified line by line in `TestFeeAccountingFixturesRefundsAndChargebacks`.

## 4. Reporting

`GET /api/v1/admin/finance/revenue` ([reconciliation.md](reconciliation.md) §4): quoted, assessed (test payments vs
synthetic), refunded, chargeback-reversed, manual adjustments, provider fees estimated/unknown, payout fees, chargeback
fee exposure, recognised revenue, reversals, provider fee expense, net — per currency and month, by method and provider;
never a cross-currency total.
