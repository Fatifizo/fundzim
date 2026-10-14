# Stage 11 → Stage 12 Handover: Fees & Platform Revenue

> Stage 11 is implemented on branch `stage-11/payouts` and **awaits acceptance** (Stages 1–10 too; Stage 9 PARTIAL;
> Stage 10 PASS WITH CONDITIONS). Do not start Stage 12 until the owner accepts and asks for it. This document prepares
> Stage 12; it implements nothing. No fee may be charged and no revenue recognised until a fee model (PD-01), the tax
> position (LR-016, LR-028) and provider fee evidence (PCR-016) exist.

Stage 11 docs: [implementation](../stage-11/implementation.md) · [payout architecture](../stage-11/payout-architecture.md)
· [ledger integration](../stage-11/ledger-integration.md) · [available funds](../stage-11/available-funds.md) ·
[known issues](../stage-11/known-issues.md). ADR-043 (Stage 10: ADR-042).

## Preparation topics (brief §58)

| Topic | What exists after Stage 11 | What Stage 12 must decide or obtain |
|---|---|---|
| Ledger account structure | `revenue:platform_fees`, `expense:psp_processing_fees`, `asset:fundzim_operating_bank`, `liability:campaign_unsettled/payable` ([chart](../stage-10/chart-of-accounts.md)) | Use these; a new class is a migration + ADR-014 amendment |
| Donation accounting | `DONATION_CAPTURED` designs an optional `revenue:platform_fees` credit line; fixtures carry none | Gross vs net capture per PD-01; whether the fee is deducted or added on (donor-covers-fees) |
| Provider fee evidence | `PSP_FEE` rule (campaign or FundZim bears); no provider fee data source | PCR-016 (fee on callback/status/statement); never infer fees |
| Platform fee configuration | none (no fee schedule) | Versioned, effective-dated fee schedules per currency/rail/category as configuration with maker-checker (CLAUDE.md: fee-configuration changes are maker-checker) |
| Currency-specific policies | USD only (ZWG unverified, LR-043) | Per-currency schedules; never cross-currency |
| Calculation boundaries | — | Integer basis-point arithmetic on minor units; inputs stored so a fee is reproducible; the schedule version in force at the time |
| Refund fee treatment | `REFUND_RESERVED` reverses the platform fee in the design; `REFUND_FEE_TRUE_UP*` rules (LR-076) | PD-20: whether platform fees are refunded; who bears unrecovered provider fees |
| Chargeback fee treatment | `DISPUTE_FEE`, `DISPUTE_FUNDING*` rules | LR-020, PD-20; no dispute source yet |
| Payout fee treatment | `PAYOUT_FEE` rule (FundZim bears) unused; `provider_fee_minor`, `beneficiary_received_minor` columns exist on payouts | PD-23 (who bears payout fees), PCR-013; if the campaign bears it, the beneficiary receives amount − fee |
| Rounding | — | Documented rounding rule (no float); allocation sums must equal the gross exactly; exhaustive rounding tests (ROADMAP) |
| Fee disclosure | Donor checkout disabled; no fee shown | Disclosure before payment and on receipts; consumer-protection items (LR-018) |
| Revenue recognition | `revenue:platform_fees` exists; FEE_REMITTANCE moves fees to FundZim's own bank (Model A: only platform-fee income may land in a FundZim account) | When revenue is recognised (capture vs settlement vs remittance) — accounting review (KI-S10-02) |
| Reconciliation | Payment, ledger and payout reconciliation foundations; settlement NOT_PERFORMED | Fee lines on provider statements; fee remittance matching (Stage 17) |
| Legal and tax | — | LR-016 (VAT/income tax on fees), LR-028, LR-076 (pool funding), PD-01 |
| Provider limitations | No provider (ADR-040) | Provider fee schedules and remittance mechanics (PCR-016) |

## Rules Stage 12 must keep

- No float anywhere; fees are integer minor units with a documented rounding rule and stored inputs.
- Fees post through ledger rules only (no balance edits); fee changes are maker-checker and audited.
- Payout and settlement controls of Stages 10–11 stay unchanged; TEST ledger and TEST payouts only until production gates
  are lifted by ADRs with legal sign-off.

## Open blockers carried

ADR-040; PCR-013/016; LR-016, LR-020, LR-024, LR-028, LR-030, LR-076; PD-01, PD-20, PD-23, PD-24; accounting review
(KI-S10-02); settlement evidence (KI-S11-03); stage acceptance not recorded.
