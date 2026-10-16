# Stage 13 — Withdrawal, payout and destination risk

## 1. Signals

| Signal | Source | Facts |
|---|---|---|
| WITHDRAWAL_REQUESTED (requester) | `payouts.requested` | `payout_request_id`, `campaign_id`, `amount_minor`, `currency`, `destination_id` |
| PAYOUT_FAILED / PAYOUT_RETURNED (requester) | `payouts.status_changed` → FAILED / REVERSED | `payout_request_id`, `campaign_id` |
| DESTINATION_CHANGED / DESTINATION_REJECTED (owner) | Stage 5 destination events | `destination_id` |
| MFA_CHANGED, PASSWORD_CHANGED, PASSWORD_RESET_REQUESTED, SIGNED_IN (user) | identity events | account-takeover context |

Example rules (configuration): a new destination followed by a withdrawal within 24 h
(`sequence(DESTINATION_CHANGED, WITHDRAWAL_REQUESTED)`), several destinations in 30 days
(`distinct_count(WITHDRAWAL_REQUESTED.destination_id)`), repeated failures, MFA change shortly before a withdrawal. The
recommended automated action for these is TEMPORARY_HOLD with WITHDRAWAL_HOLD or PAYOUT_DESTINATION_HOLD (≤ 7 days) and a
case.

## 2. Eligibility engine v2 (`payout-eligibility-v2`)

The Stage 11 engine is unchanged except for three inputs read from risk (`riskFacts`, fail closed on error):

| Check | Stage 13 rule | Owner-facing category |
|---|---|---|
| EC-12 holds | FAIL when any enforced hold covers the campaign, owner user or organisation, destination or beneficiary (types CAMPAIGN, WITHDRAWAL, PAYOUT_DESTINATION, ACCOUNT_REVIEW, BENEFICIARY), besides the Stage 11 states | PAYOUTS_PAUSED |
| EC-15 screening (TEST payouts) | REVIEW for an open potential match or a requested screening without a result (PENDING/ERROR); FAIL for an analyst-confirmed match; otherwise NOT_APPLICABLE (no provider). Production payouts still FAIL (no provider) | PAYOUTS_UNAVAILABLE |
| EC-16 indicators | adds `RISK_CLASSIFICATION_HIGH|CRITICAL` (owner, organisation, campaign, destination or requester) → REVIEW, never an outright refusal | — |

EC-12 runs at REQUEST and again at PRE_SUBMIT (immediately before submission): a hold placed after approval stops the
submission (orchestration opens a COMPLIANCE_HOLD exception and returns the payout to review, invalidating approvals);
EC-15 failures are handled the same way. **Approval** (`ApprovePayout`) is refused with `409 PAYOUT_RISK_HOLD` while a hold
covers the payout. Every Stage 11 control (reservations, UNKNOWN never resubmitted, FINANCE maker-checker, simulator only)
is unchanged; holds never move money or touch the ledger.

## 3. Destination holds

A PAYOUT_DESTINATION_HOLD covers one destination (rules: the destination named by the triggering signal). Staff may place
one directly; release is maker-checker. Destination verification and changes remain the Stage 5/11 flows (step-up, cooling
-off, approval invalidation).
