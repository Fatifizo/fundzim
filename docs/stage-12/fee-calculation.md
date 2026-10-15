# Stage 12 — Fee calculation

Code: `internal/fees/calc.go` (pure; `AlgorithmVersion = "fees-calc/1"`). Tests: `internal/fees/calc_test.go`.

## 1. Exactness

- Money is `int64` minor units. Products and quotients are computed on `math/big` integers (no overflow, no floats); the
  result must fit `int64` (`ErrCalcOverflow` otherwise, e.g. MaxInt64 + a fixed fee, or a donor charge above MaxInt64).
- Rates are integer basis points 0–10 000. A non-positive basis is refused (`ErrCalcAmount`).
- One rounding per component ([rounding.md](rounding.md)): `fee = round(basis × bps / 10 000 + fixed)`, then the minimum,
  then the maximum. TIERED: the band containing the whole amount applies to the whole amount (`fee = round(basis × tier_bps
  / 10 000 + tier_fixed)`).
- Every fee is computed on the donation (or payout, refund, disputed) amount — never grossed up on the donor charge
  (provider fee bases are UNKNOWN; KI-S12).

## 2. Responsibility split

`Split(fee, responsibility, donor_share_bps)`: DONOR_COVERED → donor; ORGANISER_COVERED → organiser; PLATFORM_ABSORBED →
platform; SHARED → largest-remainder allocation of the donor share and the rest (ties to the donor, MONEY.md §6). Parts
always sum to the fee. An unknown fee keeps the parts that its responsibility makes zero (e.g. ORGANISER_COVERED: donor 0,
platform 0, organiser UNKNOWN).

## 3. Donation breakdown

For donation *D*, platform fee *P* and provider fee *V*:

| Figure | Formula |
|---|---|
| total donor charge | `D + P_donor + V_donor` |
| campaign allocation | `D − P_organiser − V_organiser` |
| platform net | `P − V_platform` |
| provider fee liability | `V` |
| identity (checked) | `donor_charge = campaign_allocation + platform_net + V` |

Refused: an allocation ≤ 0 (`FEE_EXCEEDS_AMOUNT`), including an organiser-borne platform fee alone ≥ D. An UNKNOWN part
makes the figures that depend on it `null` (UNKNOWN), never 0. Worked synthetic example (test fixture, not FundZim's
rates): D = 10 000, platform 5 % organiser, provider 2 % organiser → charge 10 000, allocation 9 300, platform net 500,
provider 200. Donor-covered both: charge 10 700, allocation 10 000. Platform-absorbed provider fee with an organiser
platform fee: allocation 9 500, platform net 300.

## 4. Payout, refund and chargeback fees

- `PayoutFees(amount, policy, schedule)`: provider payout fee from evidence; organiser-borne → beneficiary receives
  `amount − fee` (refused if not positive); platform-absorbed → beneficiary receives `amount` and the fee is an expense.
  Donors never bear payout fees.
- Provider refund and chargeback fees: same pass-through component (`TxRefund`, `TxChargeback`).
- Platform-fee reversals: [refund-adjustments.md](refund-adjustments.md).

## 5. Determinism and storage

`Compute(Inputs)` is the single entry point. `Inputs` is the canonical document (algorithm version, transaction type,
currency, basis, method, provider, campaign, campaign category, the selection time, and the full terms of the policy
versions and provider schedule used). The calculation row stores the inputs, the result, SHA-256 of both and the time.
Reconciliation recomputes stored inputs and compares results (`ROUNDING_MISMATCH` if they differ).

## 6. Inputs never accepted from clients

Clients send campaign, amount, currency and optionally a payment method. Unknown JSON fields are refused, so a client
cannot supply a rate, a fee, a policy or a responsibility. The browser never computes fees.
