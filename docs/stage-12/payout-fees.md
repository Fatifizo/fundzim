# Stage 12 — Payout fees

Payouts remain TEST payouts through the simulator (ADR-043). PD-23 (who bears payout fees) and PCR-013 (payout fees) are
open: the bearer is configuration (`PAYOUT_PROCESSING_FEE` policy, ORGANISER_COVERED or PLATFORM_ABSORBED) and the amount
comes from PAYOUT provider evidence (UNKNOWN without it).

## 1. Request time

`payouts.RequestPayout` calls `fees.EstimatePayoutFee` inside its transaction: the calculation is stored and the payout row
gets `fee_calculation_id`, `fee_estimate_minor` (null = UNKNOWN), `fee_basis` (ESTIMATED / UNKNOWN) and
`fee_responsibility` (null = no policy). The columns are immutable (trigger). A known organiser-borne fee that is not below
the amount is refused (`FEE_EXCEEDS_AMOUNT`, also a CHECK). The owner view shows the estimate, who bears it and what the
beneficiary receives.

## 2. No double deduction

The reservation is always the requested amount (unchanged Stage 11 journals). An organiser-borne fee is carved out of it
(the beneficiary receives `amount − fee`; no extra journal); a platform-absorbed fee is a separate `PAYOUT_FEE` expense
journal. Tested: the campaign's payable falls by exactly the payout amount, once (`TestFeePayoutFeesNeverDeductTwice`).

## 3. Completion

`payouts.syncLedger` calls `fees.AssessPayoutFee` after `PAYOUT_COMPLETED`: one `PAYOUT_PROCESSING_FEE` assessment per
payout (idempotent). A known platform-absorbed fee is `PENDING_POSTING` until its `PAYOUT_FEE` journal (`payout:{id}:fee`) is
posted; a ledger refusal leaves it pending and `fees.maintenance` retries (several workers posting concurrently produce one
journal: deterministic key). An organiser-borne or UNKNOWN fee needs no journal (`NOT_APPLICABLE`).

## 4. Stale estimates

The estimate is captured once. A later policy change does not alter it; the assessment at completion uses the same
calculation. Whether a changed provider fee should re-quote an approved payout is a Stage 13+ decision (KI-S12).
