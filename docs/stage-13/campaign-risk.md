# Stage 13 — Campaign risk

## 1. Signals

| Signal | Source | Notes |
|---|---|---|
| CAMPAIGN_CREATED (owner) | `campaigns.created` | carries `campaign_id` and `title_hash` = SHA-256 of the normalised (lower-case, whitespace-collapsed) public title, so rules can count near-identical titles per owner (`distinct_count` / `fact_compare` on `title_hash`) |
| CAMPAIGN_REPORTED (campaign) | `campaigns.report_escalated` (a public report that staff escalated) | confidence 60: **a report is an allegation**, never assumed true; the reason code is kept |
| CAMPAIGN_REVIEW_ESCALATED (campaign) | `campaigns.review_escalated` | confidence 80 |
| RESTRICTION_APPLIED (campaign or party) | `compliance.restriction_applied` | compliance's own decision, recorded as context |
| owner signals | identity, verification, destination, payout signals on the owner | rules on the USER/ORGANISATION subject |

## 2. Example rules (none is seeded; each needs maker-checker)

* Rapid creation: `signal_count(CAMPAIGN_CREATED) in 1 day ≥ N` on the owner → REVIEW_REQUIRED.
* Duplicate titles: `distinct_count(CAMPAIGN_CREATED.title_hash) in 30 days` compared with the count → REVIEW_REQUIRED.
* Reports: `signal_count(CAMPAIGN_REPORTED) in 1 day ≥ N` on the campaign → REVIEW_REQUIRED or TEMPORARY_HOLD
  (WITHDRAWAL_HOLD on the campaign, ≤ 7 days) — never a campaign suspension.
* Account takeover around a campaign: `sequence(MFA_CHANGED, DESTINATION_CHANGED, WITHDRAWAL_REQUESTED) in 1 day`.

N is configuration chosen by people under maker-checker; no value is presented as validated.

## 3. Enforcement

* **Publication** (`campaigns.publish`): refused with `409 CAMPAIGN_ON_HOLD` while an enforced CAMPAIGN_HOLD on the campaign
  or ACCOUNT_REVIEW_HOLD on its owner exists (checked inside the publication transaction, before eligibility).
* **Donations**: CAMPAIGN_HOLD or DONATION_HOLD on the campaign stops new donation intents and payment attempts
  ([payment-risk.md](payment-risk.md)).
* **Withdrawals**: CAMPAIGN_HOLD or WITHDRAWAL_HOLD on the campaign fails EC-12 ([payout-risk.md](payout-risk.md)).

Campaign status changes (pause, suspend, cancel) remain the campaigns module's and compliance's decisions; a hold never
changes a campaign's status and is never shown publicly. Owners see only coarse reasons ("cannot be published while a
review is in progress"; payout category PAYOUTS_PAUSED).
