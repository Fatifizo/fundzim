# Stage 12 — Chargeback fees (foundations)

No dispute source exists (no provider; KI-S11-08); LR-020 (who bears chargeback loss), LR-080 (recovery from campaign
owners) and PCR-005 (chargeback process and fees) are open. Stage 12 builds the calculation, the exposure record and TEST_ONLY
journals; no real chargeback is executed.

## 1. What is tracked

| Item | Where |
|---|---|
| Disputed amount | `basis_minor` of the CHARGEBACK adjustment |
| Platform-fee reversal | the adjustment (same treatment and rounding as refunds; PD-20's provisional "platform fee reversed" is configuration) |
| Provider chargeback fee | a `CHARGEBACK_FEE` assessment from CHARGEBACK provider evidence (UNKNOWN without it) |
| Platform exposure / campaign exposure | the fee's platform and organiser parts, by the `CHARGEBACK_FEE` policy (ORGANISER_COVERED or PLATFORM_ABSORBED) |
| Recovery status | `RECOVERED_FROM_CAMPAIGN_BALANCE`, `BORNE_BY_PLATFORM`, `UNRECOVERED_INSUFFICIENT_CAMPAIGN_BALANCE`, `UNKNOWN_FEE`, `RESPONSIBILITY_NOT_CONFIGURED`, `NOT_APPLICABLE` |
| Accounting adjustment | `DISPUTE_OPENED` (with the revenue reversal) and `DISPUTE_FEE` journals; references in `fee_accounting_references` |

## 2. Rules

- An organiser-borne chargeback fee is taken from the campaign's payable balance only if the ledger accepts it
  (`campaign_payable` is non-negative); otherwise the exposure is `UNRECOVERED…` and the assessment stays `PENDING_POSTING`
  (reconciliation reports it). It is never assumed recoverable from organisers.
- One chargeback-fee assessment per donation (a second chargeback on the same donation reuses it; KI-S12).
- `DISPUTE_WON` (re-credit) is not exercised: no dispute lifecycle exists.

## 3. Example (synthetic)

Donation 100.00 (fee 5.00, organiser-covered), chargeback 20.00 → reversal 1.00; `DISPUTE_OPENED` Dr payable 19.00 / Dr
revenue 1.00 / Cr settled 20.00; chargeback fee 15.00 (synthetic FIXED evidence, organiser-covered) `DISPUTE_FEE` Dr payable
15.00 / Cr settled 15.00 → `RECOVERED_FROM_CAMPAIGN_BALANCE`. A 10.00 donation with a 5.00 chargeback cannot cover the 15.00
fee → `UNRECOVERED_INSUFFICIENT_CAMPAIGN_BALANCE`.
