# Stage 12 — Provider fees

No production payment provider is selected or certified (ADR-040; KI-S9-02). FundZim does not know any provider
transaction fee, settlement fee, mobile-money charge, card fee, refund fee, chargeback fee, payout fee or FX margin
(PCR-005, PCR-007, PCR-013, PCR-016). Stage 12 therefore models provider fees as **evidence**, never as fact.

## 1. Evidence model (`app.fee_provider_schedules`)

| Field | Meaning |
|---|---|
| provider, contract reference, schedule version | where the terms come from; a contract reference is free text ("IT synthetic sandbox terms (not a contract)" in tests) |
| currency, payment method (optional), fee type | TRANSACTION, SETTLEMENT, REFUND, CHARGEBACK, PAYOUT (no FX margin: FundZim never converts) |
| model and terms | PERCENTAGE, FIXED, PERCENTAGE_PLUS_FIXED, TIERED, CAPPED, MINIMUM_FEE; integer bps and minor units |
| rounding mode | NULL = the provider's rounding is UNKNOWN (estimates use HALF_UP and say so) |
| effective window | selection at a time; a method-specific schedule beats a provider-wide one, then the latest start |
| evidence source, evidence reference | provenance |
| confirmation status | `SYNTHETIC` (sandbox simulator only, TEST) or `UNVERIFIED` (a documented claim not yet verified). `VERIFIED` is refused (`PROVIDER_FEE_VERIFICATION_UNAVAILABLE`; CHECK `ck_fee_provider_schedules_no_verified_stage12`) |
| status | PENDING_APPROVAL → ACTIVE (a second person, `fee.config.approve`, never the recorder or anyone linked) → RETIRED |

Terms are immutable once recorded (only status and approval columns change); activating or retiring a schedule
invalidates ACTIVE quotes in its currency.

## 2. Bases

| Basis | When |
|---|---|
| `ESTIMATED` | computed from SYNTHETIC or UNVERIFIED evidence |
| `UNKNOWN` | no evidence (amount null — never 0); or no method chosen, so no provider is known |
| `PROVIDER_REPORTED` | a provider reported the fee (only the TEST fixture `provider_fee_report` writes `fee_provider_reports`, evidence SIMULATED) |
| `PROVIDER_CONFIRMED` / `RECONCILED` | require verified provider evidence or settlement statements — none exist; reconciliation reports `UNRECONCILED_PROVIDER_FEE` |

An estimated fee is never treated as a confirmed provider charge, and revenue is never recognised from an estimate.

## 3. Who bears the provider fee

Decided by a FundZim policy of the provider-cost category (`PAYMENT_PROCESSING_FEE`, `PAYOUT_PROCESSING_FEE`,
`REFUND_FEE`, `CHARGEBACK_FEE`; model `PROVIDER_PASS_THROUGH`). The provider fee is never platform revenue. Without such a
policy the split is unknown and the totals that depend on it are UNKNOWN. PD-20 and PD-23 (who bears refund, dispute and
payout fees) remain business decisions; every option is configurable, none is assumed.

## 4. What a provider must confirm

Fee schedule per method and currency, rounding, whether fees are deducted at source or invoiced, whether the fee is
reported on the callback, status API or only on statements (PCR-016), refund and chargeback fees and whether a fee is
returned on refund, payout fees (PCR-013), settlement fees (PCR-007), whether passing a fee to the donor is permitted.
