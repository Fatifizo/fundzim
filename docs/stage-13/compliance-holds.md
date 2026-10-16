# Stage 13 — Holds (central hold registry)

`risk.risk_holds` is the single registry of operational holds. Compliance restrictions (RESTRICTED / SUSPENDED /
OFFBOARDED, `compliance.subject_restrictions`) remain the **legal** restriction source; a hold never creates, lifts or
mirrors a restriction, and both are enforced independently (neither overrides the other). Holds never move money, never
touch the ledger and never change a payment, payout or campaign status.

## 1. Types and subjects (CHECK)

| Hold | Subjects | Stops | Takes effect |
|---|---|---|---|
| CAMPAIGN_HOLD | campaign | publication, donations, withdrawals | REQUESTED → second person activates |
| DONATION_HOLD | campaign, user (donor) | donation intents and payment attempts | REQUESTED → second person activates |
| WITHDRAWAL_HOLD | campaign, user, organisation | payout request, approval, submission (EC-12) | at once (automated allowed) |
| PAYOUT_DESTINATION_HOLD | payout destination | payouts to it (EC-12) | at once (automated allowed) |
| ACCOUNT_REVIEW_HOLD | user, organisation | publication, withdrawals | REQUESTED → second person activates |
| BENEFICIARY_HOLD | beneficiary | withdrawals for it (EC-12) | at once |

One open hold per type and subject (unique partial index); a second request returns `RISK_HOLD_EXISTS` (the engine links to
the existing hold).

## 2. Lifecycle (`risk_hold`, database-guarded)

```
REQUESTED ─activate (2nd person)─► ACTIVE ─review─► UNDER_REVIEW
    └─revoke─► REVOKED               │                  │
                                     ├─request-release──┴─► RELEASE_PENDING ─approve─► RELEASED
                                     │                          └─reject─► ACTIVE
                                     └─(expires_at)──► EXPIRED   (also from UNDER_REVIEW)
```

Enforced while ACTIVE, UNDER_REVIEW or RELEASE_PENDING. RELEASED, EXPIRED and REVOKED are terminal.

## 3. Release controls

* Release is **maker-checker**: `request-release` (`risk.hold.create`) then `approve-release` / `reject-release`
  (`risk.hold.release`, fresh step-up recorded in `release_step_up_at`).
* The checker is never the requester or anyone linked to the requester; nobody linked to a held **user** may request or
  approve its release — **no one releases their own hold** (Go and `risk_holds_guard`; CHECK `released_by <> release_requested_by`).
* Activation of a requested hold: never its requester or a linked account (`activated_by`, CHECK and guard).
* Automated holds (engine): payout-protective types only, `expires_at ≤ activated_at + 7 days`, linked evaluation and case
  (CHECK `ck_risk_holds_automated`). The `risk.maintenance` job expires them (monitoring event HOLD_EXPIRED, outbox
  `risk.hold_expired`); an expiry is not a decision — the case stays open for a human.
* Case outcomes apply to the case's holds ([risk-decisions.md](risk-decisions.md)).

## 4. Enforcement points

| Point | Holds checked | Refusal |
|---|---|---|
| `campaigns.publish` | CAMPAIGN_HOLD (campaign), ACCOUNT_REVIEW_HOLD (owner) | `409 CAMPAIGN_ON_HOLD` |
| `payments.CreateIntent`, `CreateAttempt` | CAMPAIGN_HOLD, DONATION_HOLD (campaign), DONATION_HOLD (donor) | `409 DONATIONS_ON_HOLD` |
| payout eligibility EC-12 (request and pre-submit) | CAMPAIGN, WITHDRAWAL, PAYOUT_DESTINATION, ACCOUNT_REVIEW, BENEFICIARY | `422 PAYOUT_NOT_ELIGIBLE` (category PAYOUTS_PAUSED) / back to review |
| `payouts.ApprovePayout` | as EC-12 (campaign, owner, organisation, destination) | `409 PAYOUT_RISK_HOLD` |

A registry read error stops the action (fail closed). Owners see only coarse messages; the hold, its reason and the case are
staff-only.

## 5. Events and history

Every change: `risk.risk_events`, `audit`, outbox `risk.hold_activated` / `risk.hold_released` / `risk.hold_expired`.
Reconciliation flags holds still open under a case resolved without action, holds past expiry and automated holds without a
case.
