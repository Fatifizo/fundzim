# Stage 6 — Campaign moderation

> `internal/campaigns/review.go`, `internal/campaigns/updates`, `internal/campaigns/media`; ADR-036, ADR-037 §4;
> campaign-approval-policy.md (Stage 1). Verified identity is a precondition for submission, never a reason to approve.

## 1. Review queue and assignment

- Submission (and a live edit) opens exactly one review (`app.campaign_reviews`, unique open review per campaign),
  pinned to the snapshot version, the campaign policy version and the risk tier.
- `GET /admin/campaigns/review` (`campaign.review`): open reviews, oldest first, filters `assigned=me|unassigned|any` and
  `category`, keyset cursor `<queued_at>|<review_id>`. (`?status=` is not supported; KI.)
- `GET /admin/campaigns/{campaign_id}/review`: working copy, reviewed and approved snapshots, owner (display name, KYC
  or KYB level), beneficiary (type, verification status, relationship, authority basis, disclosure), restriction level,
  media readiness, history, APPROVE/PUBLISH evaluations, policy version and screening status. Viewing is audited
  (`campaign.review_viewed`).
- `assign` (to self by default); assigning someone else requires that person to hold `campaign.review`
  (`422 ASSIGNEE_NOT_ELIGIBLE`). Only the assignee may start, request changes, approve, reject or escalate
  (`409 NOT_ASSIGNED`). Reassignment resets the review to ASSIGNED.

Roles: `REVIEWER` (review, decide, publish, suspend/unsuspend), `COMPLIANCE` (the same plus `campaign.decide.high`),
`ADMIN` (`content.moderate`, `campaign.category.manage`). SUPPORT, KYC_REVIEWER and SUPER_ADMIN have no campaign
decision rights.

## 2. Conflict of interest (self-review)

A staff member may not review, decide, escalate or second-approve a campaign whose owner, creator or organisation
member is themselves **or any account linked to them** (`app.actor_identities`). Enforced twice:

1. Go: `conflicted()` before every staff action → `403 SELF_DECISION_FORBIDDEN`.
2. Database: `app.campaign_reviews_not_self()` (SECURITY DEFINER) refuses any `assigned_to`, `pending_decided_by`,
   `decided_by`, `second_approver` or `escalated_by` that matches, even for a direct SQL write by the app role
   (`TestCampaignSelfReviewFourEyesAndPrivacy`).

Owners have no admin routes (404). A staff member who has not linked a personal account is not detected by this check
(KI); conflict declarations remain the procedural control.

## 3. Decisions and four eyes

Decision bodies: `{reason_code (UPPER_SNAKE), note (3–5000, internal), user_message? (shown to the owner)}`.
`campaign.decide` requires a fresh step-up (enforced by the route policy).

| Action | Effect |
|---|---|
| request-changes | `UNDER_REVIEW → CHANGES_REQUESTED` (or a re-review is declined); owner sees reason code + user message only |
| approve | APPROVE evaluation on the reviewed snapshot; STANDARD/ELEVATED: `→ APPROVED`. HIGH tier (MEDICAL, BUSINESS_SUPPORT, OTHER): records a pending first approval and returns `202` |
| second-approval | `campaign.decide.high` (COMPLIANCE), a different person from the first approver (Go + DB CHECK) → `APPROVED` |
| reject | `UNDER_REVIEW → REJECTED` (or the re-review is rejected and the live version stays) |
| escalate | flags the review; outbox `campaigns.review_escalated` opens a compliance case (case type `CAMPAIGN_REVIEW`, the campaign as primary subject, the owner as related subject); the campaign stays in review |
| reopen | `REJECTED → UNDER_REVIEW` with a new review assigned to the reopener |
| publish / suspend / reactivate / cancel / complete / archive | staff lifecycle actions (see campaign-lifecycle.md) |

No screening is performed (`SCREENING_PROVIDER_NOT_SELECTED`); the review detail shows it. Reviewer checklists remain
a manual procedure (campaign-approval-policy §8); per-check result rows are not built (KI).

## 4. Update moderation

Owner updates (`internal/campaigns/updates`) are plain text, only for `ACTIVE`/`PAUSED`/`COMPLETED` campaigns. They go
to `PENDING_MODERATION` when the category is `PRE_MODERATE_UPDATES` (MEDICAL), the tier policy says so, or the owner is
RESTRICTED; otherwise they publish directly. `content.moderate` staff approve or hide (`{reason_code, note}`); the
moderator cannot be the author or linked to the owner/creator/organisation (Go + `campaign_updates_not_self` trigger).
Hidden updates cannot be unhidden (KI). Suspended or restricted (SUSPENDED/OFFBOARDED) campaigns cannot post.

## 5. Media moderation

Media are approved automatically only after the scan **and** the re-encode succeed ([campaign-media.md](campaign-media.md));
reviewers see readiness in the review detail. `content.moderate` staff remove media (`POST …/media/{media_id}/remove`)
with a reason; removed media are never served and the objects are kept as evidence.
