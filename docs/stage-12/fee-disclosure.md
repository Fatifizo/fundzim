# Stage 12 — Fee disclosure

Disclosure obligations are open (LR-018, LR-056 consumer protection; LR-059 IMTT disclosures; LR-028 fee model). Public
checkout stays disabled (the campaign page's donate button still says "Donations are not yet available."). Every figure
shown is labelled **TEST — synthetic configuration, not FundZim's commercial rates**; UNKNOWN is shown as "Unknown", never 0;
the browser never computes a fee (all amounts come from the API as digit strings).

## 1. Donor (development preview)

`/dashboard/donation-preview?campaign=<id>` (signed in only; linked from the organiser fees page). The donor enters an
amount and optionally a payment method; the page calls `POST /api/v1/fees/quotes` (Idempotency-Key) and shows:

| Shown | Source |
|---|---|
| Donation amount | the request |
| Platform fee (QUOTED, or "No platform fee policy is active") | quote component PLATFORM_FEE |
| Provider fee (ESTIMATED from SYNTHETIC/UNVERIFIED evidence, or UNKNOWN) | quote component PAYMENT_PROCESSING_FEE |
| Who bears each fee | responsibility and parts |
| Total donor charge | `donor_charge_minor` (UNKNOWN when it depends on an unknown provider fee) |
| Campaign allocation | `campaign_allocation_minor` |
| Expiry, status | `expires_at`, ACTIVE/EXPIRED/INVALIDATED/BOUND |
| Notice | "A fee quote is an estimate for a TEST donation. It is not a charge, and nothing has been paid." |

No unconfirmed rate is advertised: rates appear only as the terms of TEST policy versions on staff pages and in
`GET /api/v1/fees/policies/active`, labelled TEST.

## 2. Organiser

- `/dashboard/campaigns/[id]/fees` (owner or organisation member): the policies that would apply to the campaign (platform,
  provider processing, payout), who bears each, refund treatment, the assessed fees with their basis (QUOTED / ESTIMATED /
  UNKNOWN) under the page's TEST banner ("no fee is charged and nothing here is a real donation"), decided adjustments, and
  restrictions. The API
  projection carries no staff identities, notes, decision reasons or internal references (pending or rejected requests are
  not shown) — `OrganiserAssessment` / `OrganiserAdjustment`.
- Withdrawal pages: the request page lists the payout-fee policies; the withdrawal detail shows the fee estimate (or
  UNKNOWN), who bears it and what the beneficiary receives.
- Synthetic financial activity is never presented as real donations.

## 3. Staff

`/admin/finance/fees` (overview, production BLOCKED banner), policies and versions (terms, hash, approvals), provider fee
evidence, quotes, assessments (journals, adjustments, history), adjustments, reconciliation and `/admin/finance/revenue`
(one table per currency; no cross-currency total).
