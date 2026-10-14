# Stage 11 — Available-funds model

## 1. Derivation from the ledger (no double subtraction)

`liability:campaign_payable:{campaign}` contains only funds that were captured, **settled** (`SETTLEMENT_MATCHED` against
settlement evidence) and **released** (`RELEASE`). Everything else already sits in other accounts and is therefore never
subtracted again:

| Conceptual term (brief §14) | Where it is in the ledger | Treatment |
|---|---|---|
| Verified settled funds | `campaign_payable` (after settlement + release) | the base |
| Unsettled donations | `campaign_unsettled` | not available (different account) |
| Active payout reservations | `campaign_payout_pending` (PAYOUT_RESERVED) | already out of payable |
| Payouts in flight / UNKNOWN | `payout_in_transit` | already out of payable |
| Completed payouts | left the pool (`psp_settled`) | already out of payable |
| Required reserves | `campaign_reserve` (RELEASE_WITH_RESERVE) | already out of payable (no reserve percentage invented) |
| Compliance holds / freeze | `campaign_held` (CAMPAIGN_FROZEN) or account FROZEN | already out of payable / refused (EC-09, EC-12) |
| Refund exposure | open refund requests (not yet in the ledger: refunds do not execute) | **subtracted**: available = payable − open refund exposure |
| Chargeback exposure | no dispute source exists (LR-020) | not modelled (EC-14 NOT_APPLICABLE) |

**available = campaign_payable − open refund exposure** (refund requests REQUESTED, PENDING_APPROVAL, ON_HOLD, APPROVED,
EXECUTING; payments module, read through an interface). The request checks it (EC-09); the ledger reservation enforces the
payable part under a row lock, so two requests can never reserve the same money.

## 2. Authorisation and provider success are not settlement

A captured or provider-reported payment never reaches `campaign_payable` (it debits `psp_clearing`). Only
`SETTLEMENT_MATCHED` + `RELEASE` do, and settlement reconciliation is **NOT_PERFORMED** (no statements). Consequently:

- **Production available funds: BLOCKED.** EC-20 fails for any non-test payout.
- In the TEST ledger, `campaign_payable` is reached only by the TEST_ONLY `fund_campaign` fixture (staff API, ADMIN,
  synthetic capture → settlement → release). Those amounts are synthetic; every owner and staff view labels them TEST_ONLY
  and shows production status BLOCKED. They are never donations and never withdrawable funds.

## 3. Owner preview

`GET /campaigns/{campaign_id}/payout-eligibility`: settled-and-released, refund exposure, reserved (pending + in transit),
available, `funds_label = TEST_ONLY`, `production_status = BLOCKED`, owner-safe blockers and (with a destination and
amount) the 22 check results with EC-13/EC-16 details masked. Nothing is recorded.
