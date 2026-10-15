# Stage 12 — Refund fee adjustments

Refunds still cannot be executed (KI-S9-07: no refund sandbox; refund requests stop at APPROVED). Refund fee treatment is
therefore implemented as calculation, adjustment records and TEST_ONLY fixture journals. PD-20 (is the platform fee
refunded? who bears unrecovered provider fees?) and LR-018/LR-056 (consumer refund rights) remain open: the treatment is
configuration on each platform-fee policy version, never assumed.

## 1. Treatments (`refund_treatment` on PLATFORM_FEE versions)

| Treatment | Reversal of the platform fee for a refund of R out of D |
|---|---|
| `PROPORTIONAL` | `round(fee × cumulative_refunded / D) − reversed_before` (a full refund reverses the whole fee) |
| `PROPORTIONAL_EXCEPT_FIXED` | the same on `fee − fixed_part` (the fixed part is retained) |
| `NON_REFUNDABLE` | 0 — still recorded (amount 0) so later refunds are bounded by D |

Rounding: the version's mode, applied to the cumulative value (no drift across partial refunds). Refunds above the
original amount are refused. The provider's fee on a refund is UNKNOWN and never assumed refundable; it is recorded in the
adjustment's calculation document.

## 2. Who gets the reversal

The reversal Fr is split like the fee: the donor's share goes back to the donor (refund payable `R + Fr_donor`), the
organiser's share reduces what the campaign returns (`R − Fr_organiser`), and FundZim returns Fr (Dr `revenue:platform_fees`).

## 3. Records

- `app.fee_adjustments`: reason `REFUND`, direction DECREASE, amount Fr, `basis_minor` R, source `refund:{id}`, status
  APPLIED (SYSTEM), the calculation (treatment, rounding, exact value, before/after, parts) — immutable; unique per
  (assessment, reason, source), so a replay never duplicates.
- Journals are posted **before** the adjustment is recorded: a ledger refusal (e.g. the campaign holds less than it must
  return) records nothing (`TestFeeFailureInjection`). A client `reversal_id` makes a refund replayable.
- `app.fee_accounting_references` links the adjustment to `REFUND_RESERVED`.
- The assessment keeps its original fee; its net fee is computed (`net_fee_minor`).

## 4. Example (synthetic)

Donation 100.00, fee 5.00 PROPORTIONAL HALF_UP: refund 40.00 → reversal 2.00 (retained 3.00); a second refund 50.00 →
cumulative `round(5.00 × 90 / 100) = 4.50` → reversal 2.50; net fee 0.50; a further refund above the remaining 10.00 is
refused.

## 5. Manual corrections

`PROVIDER_CORRECTION`, `POLICY_CORRECTION`, `ACCOUNTING_ADJUSTMENT`: requested by `fee.adjust.request`, decided by
`fee.adjust.approve` (different person, linked accounts included, fresh step-up), never below zero (Go and trigger), never
posted — a ledger correction is a Stage 10 maker-checker adjustment referenced by `ledger_adjustment_id`.
