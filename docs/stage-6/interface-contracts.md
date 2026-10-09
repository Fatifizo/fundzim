# Stage 6 interface contracts (campaign engine)

Binding for every Stage 6 stream. Decisions: ADR-036 (lifecycle and publication) and ADR-037 (age attestation,
BASIC_VERIFIED, restrictions, staff links). Change this file before diverging from it.

## 1. Ownership and packages

| Package | Module | Owns (tables) | Stream |
|---|---|---|---|
| `internal/campaigns` | campaigns | `app.campaign_categories`, `app.campaign_policies`, `app.campaigns`, `app.campaign_goals`, `app.campaign_versions`, `app.campaign_beneficiaries`, `app.campaign_reviews`, `app.campaign_status_history`, `app.campaign_eligibility_evaluations` | lead |
| `internal/campaigns/media` | campaigns | `app.campaign_media`, `app.campaign_media_events` | M |
| `internal/campaigns/updates` | campaigns | `app.campaign_updates`, `app.campaign_update_events` | U |
| `internal/kyc` (additions) | kyc | `kyc.age_attestations` | R |
| `internal/compliance` (additions) | compliance | `compliance.subject_restrictions` | R |
| `internal/auth` (additions) | auth | `app.staff_link_requests` | R |
| `internal/platform/featureflags` | platform | reads `app.feature_flags` | R |

The campaigns module may import: platform, audit, users, organisations, kyc, beneficiaries, compliance, risk,
storage. The core `campaigns` package never imports `media` or `updates` (they import it); the core reaches
them through the interfaces in §4, wired in `internal/app`.

The campaigns module uses the **app pool** (`fundzim_app`). Grants for new app tables follow the runtime grant
derivation in place since Stage 5 (check `migrations/20261009150000_audit_evidence_gateways.sql`; add explicit
grants in your migration if the derivation does not cover a new table).

## 2. Migration slots (goose, `migrations/`)

| File | Stream | Content |
|---|---|---|
| `20261009170000_age_attestation.sql` | R | `kyc.age_attestations`, policy rules `basic` section (new APPROVED policy version v2 through the guarded transition, ADR-034 exemption pattern), grants |
| `20261009170100_staff_links.sql` | R | `app.staff_link_requests`, `app.actor_identities(uuid)`, KYC self-decision trigger uses it |
| `20261009170200_compliance_restrictions.sql` | R | `compliance.subject_restrictions`, grants, backfill from approved resolutions |
| `20261009171000_campaigns.sql` | lead | core campaign tables, state machine, categories, policy v1, permissions |
| `20261009171100_campaign_media.sql` | M | `app.campaign_media`, `stored_object_scan` additions if any |
| `20261009171200_campaign_updates.sql` | U | `app.campaign_updates` |

## 3. Core data model (lead; summary)

- `app.campaigns`: `id`, `public_code` (10 chars, Crockford base32, random, unique), `slug` (ASCII, `<base>-<public_code>`,
  unique, fixed at first publication), `owner_user_id` XOR `owner_organisation_id`, `created_by`, `category_code`,
  `title`, `summary`, `story` (plain text), `goal_amount_minor bigint > 0`, `goal_currency char(3)`, `status`,
  `visibility` (owner choice `PUBLIC` = listed and searchable, `UNLISTED` = link only; a campaign is exposed only while
  `ACTIVE`, `PAUSED` or `COMPLETED`),
  `risk_tier` (`STANDARD`, `ELEVATED`, `HIGH`), `policy_version`, `approved_version_id`, `pending_version_id`,
  `re_review_required`, `fundraising_basis`, timestamps `created_at`, `submitted_at`, `approved_at`,
  `published_at`, `paused_at`, `suspended_at`, `completed_at`, `completion_reason`, `cancelled_at`, `archived_at`,
  `resubmission_count`, `version`. **No raised, total or balance column.**
- Money: `goal_amount_minor` + `goal_currency`, `internal/platform/money`. A currency is usable only if
  `app.currencies.enabled AND minor_units_verified` (ZWG is not verified: LR-043 → `CURRENCY_NOT_AVAILABLE`).
- `campaign_status_history`: one row per version (`from_status`, `to_status`, `actor_type`, `actor_id`,
  `reason_code`, `note`, `correlation_id`).
- `campaign_versions`: content snapshots (submission, approval, material edit), with `beneficiary_id`,
  `media_ids`, `goal`, `disclosure`.
- `campaign_beneficiaries`: link to `app.beneficiaries` (one current primary), `disclosure` (`NONE`, `DISPLAY_NAME`),
  `consent_declared`, linked/unlinked by and reason.
- `campaign_reviews`: one per submission; pinned version and policy; assignment; decision; `pending_outcome`,
  `pending_decided_by` (four eyes for HIGH tier); `escalated` + compliance case reference.

## 4. Go interfaces between packages

```go
// package campaigns (core) — used by media and updates
type Access struct {
    CampaignID, Status, Category, Visibility string
    OwnerUserID, OwnerOrgID *string
    Public bool // ACTIVE/PAUSED/COMPLETED (PUBLIC or UNLISTED; only PUBLIC is listed/searchable)
}
// OwnerAccess authorises a personal user on a campaign: read = owner or org member; write = owner or ORG_ADMIN.
// Returns ErrNotFound (404 CAMPAIGN_NOT_FOUND) for anyone else.
func (s *Service) OwnerAccess(ctx context.Context, userID, campaignID string, write bool) (Access, error)
func (s *Service) Load(ctx context.Context, campaignID string) (Access, error)              // staff paths
func (s *Service) PublicBySlug(ctx context.Context, slug string) (Access, error)          // ErrNotFound unless Public
func (s *Service) Restricted(ctx context.Context, a Access) (level string, err error)     // "" | RESTRICTED | SUSPENDED | OFFBOARDED (owner, org, beneficiary, campaign)
func (s *Service) RecordMaterialChange(ctx context.Context, campaignID, kind, actorID string) error // sets re_review_required when ACTIVE/PAUSED

// implemented by package media, injected into the core (nil-safe: no media = not ready)
type MediaInfo interface {
    Readiness(ctx context.Context, campaignID string) (MediaReadiness, error)
    ApprovedIDs(ctx context.Context, campaignID string) ([]string, error) // ordered, for version snapshots
}
type MediaReadiness struct{ CoverApproved bool; Pending, Rejected int }
```

## 5. Eligibility (lead)

`Evaluate(ctx, action, actor, campaign) → Result{Action, Allowed bool, Reasons []Reason{Code, Field, Message}}`.
Actions: `CREATE_DRAFT`, `EDIT_DRAFT`, `SUBMIT_FOR_REVIEW`, `APPROVE`, `PUBLISH`, `UPDATE_PUBLISHED`,
`REACTIVATE` (also used for resume). Codes include: `NOT_AUTHENTICATED`, `ACCOUNT_NOT_ACTIVE`,
`AGE_ATTESTATION_REQUIRED`, `BASIC_VERIFICATION_REQUIRED`, `IDENTITY_VERIFICATION_REQUIRED`,
`ORGANISATION_VERIFICATION_REQUIRED`, `NOT_ORGANISATION_ADMIN`, `REPRESENTATIVE_AUTHORITY_REQUIRED`,
`BENEFICIARY_REQUIRED`, `BENEFICIARY_NOT_VERIFIED`, `BENEFICIARY_NOT_AUTHORISED`, `INDIVIDUAL_FOR_OTHERS_DISABLED`,
`ACCOUNT_RESTRICTED` (generic for any restriction, no reason disclosed), `CATEGORY_INACTIVE`,
`CATEGORY_REQUIRES_ORGANISATION`, `COVER_IMAGE_REQUIRED`, `MEDIA_NOT_READY`, `CURRENCY_NOT_AVAILABLE`,
`GOAL_OUT_OF_RANGE`, `MISSING_FIELD`, `RESUBMISSION_LIMIT_REACHED`, `SECOND_APPROVAL_REQUIRED`.
Gates (campaign policy v1): CREATE_DRAFT and EDIT_DRAFT need `BASIC_VERIFIED`; SUBMIT, PUBLISH, REACTIVATE
need the owner `IDENTITY_VERIFIED` (organisation: `ORG_KYB_VERIFIED` plus the submitter's representative
authority `org.campaign.submit`). Evaluations for SUBMIT, APPROVE, PUBLISH and REACTIVATE are persisted in
`campaign_eligibility_evaluations`.

## 6. HTTP API (all under `/api/v1`, standard envelope, CSRF on unsafe methods, `If-Match`/`ETag` = version)

Owner (personal session):

| Method and path | Notes |
|---|---|
| `GET /campaign-categories` | public; active categories |
| `POST /campaigns` | body `{title, summary, category, goal: {amount_minor: "digits", currency}, organisation_id?}`; optional `Idempotency-Key`; 201 campaign |
| `GET /campaigns/mine` | own and organisation campaigns (`?organisation_id=`) |
| `GET /campaigns/{id}` | owner view: working copy, status, review feedback (no reviewer identity), eligibility for next actions |
| `GET /campaigns/{id}/eligibility?action=` | structured reasons |
| `PATCH /campaigns/{id}` | `If-Match` required; editable in DRAFT and CHANGES_REQUESTED (and ACTIVE/PAUSED → material-edit rules) |
| `POST /campaigns/{id}/submit` · `/withdraw` · `/cancel` · `/publish` · `/pause` · `/resume` · `/complete` (`{reason}`) · `/archive` | lifecycle; 409 `INVALID_STATUS`; 422 `NOT_ELIGIBLE` with `details` = reasons |
| `POST /campaigns/{id}/beneficiaries` | `{beneficiary_id, disclosure, consent_declared, reason?}`; replaces the primary (change control) |
| `DELETE /campaigns/{id}/beneficiaries/{beneficiary_id}` | `{reason}` (draft only) |
| `POST /campaigns/{id}/media` (multipart: `kind`, `position?`, `alt_text`, `file`) · `GET /campaigns/{id}/media` · `PATCH /campaigns/{id}/media/{media_id}` (order, alt text) · `DELETE /campaigns/{id}/media/{media_id}` | M |
| `GET /campaigns/{id}/media/{media_id}/content` | owner preview of a processed image (M) |
| `POST /campaigns/{id}/updates` · `GET /campaigns/{id}/updates` · `PATCH`/`DELETE /campaigns/{id}/updates/{update_id}` | U |

Staff (`campaign.view` to read; permissions per action; step-up where the permission requires it):

| Method and path | Permission |
|---|---|
| `GET /admin/campaigns/review` (`?status=&assigned=me|unassigned|any&category=&cursor=`) · `GET /admin/campaigns/{id}/review` (detail; `review/{id}` would collide with `{id}/media`) | `campaign.review` |
| `GET /admin/campaigns` (all statuses, search) | `campaign.view` |
| `POST /admin/campaigns/{id}/assign` (`{assignee_id?}`) · `/start-review` · `/request-changes` · `/escalate` | `campaign.review` |
| `POST /admin/campaigns/{id}/approve` · `/reject` · `/reopen` | `campaign.decide` (step-up) |
| `POST /admin/campaigns/{id}/second-approval` | `campaign.decide.high` (step-up) |
| `POST /admin/campaigns/{id}/publish` | `campaign.publish` (step-up) |
| `POST /admin/campaigns/{id}/suspend` · `/cancel` | `campaign.suspend` (step-up) |
| `POST /admin/campaigns/{id}/reactivate` | `campaign.unsuspend` (step-up) |
| `GET /admin/campaigns/{id}/media/all` · `POST /admin/campaigns/{id}/media/{media_id}/remove` | `campaign.view` · `content.moderate` (M) |
| `GET /admin/campaigns/updates/moderation` · `POST /admin/campaigns/updates/{update_id}/approve` · `/hide` | `content.moderate` (U) |
| `GET /admin/campaign-categories` · `PATCH /admin/campaign-categories/{code}` | `campaign.category.manage` |

Decision bodies: `{reason_code (UPPER_SNAKE), note (3–5000), user_message?}`. Self-review (owner, organisation
member, linked personal account) → 403 `SELF_DECISION_FORBIDDEN`.

Public (no session; rate limited; cacheable):

| Method and path | Notes |
|---|---|
| `GET /public/campaigns` | `?q=&category=&sort=published|created&cursor=&limit=`; ACTIVE, PAUSED, COMPLETED with `visibility = PUBLIC` |
| `GET /public/campaigns/{slug}` | approved version only; 404 for anything not public |
| `GET /public/campaigns/{slug}/updates` | published, not hidden (U) |
| `GET /public/campaigns/{slug}/media/{media_id}` | image bytes of APPROVED media in the approved version (M) |

Age attestation and staff links (R):

| Method and path | Notes |
|---|---|
| `GET /me/age-attestation` · `POST /me/age-attestation` (`{outcome: "ATTESTED"|"DECLINED", statement_version}`) | personal session |
| `POST /auth/register` gains optional `age_attestation: true` | recorded with source `REGISTRATION` |
| `POST /admin/me/personal-account-link` (`{email}`) | staff session, step-up |
| `POST /me/staff-link/confirm` (`{token}`) | personal session; email token |
| `GET /admin/me/personal-account-link` | status (masked email) |

## 7. Events (outbox) and audit actions

Outbox (payloads carry ids, statuses and codes only): `campaigns.created`, `campaigns.submitted`,
`campaigns.changes_requested`, `campaigns.approved`, `campaigns.rejected`, `campaigns.published`,
`campaigns.paused`, `campaigns.resumed`, `campaigns.suspended`, `campaigns.reactivated`, `campaigns.completed`,
`campaigns.cancelled`, `campaigns.archived`, `campaigns.review_escalated` (opens a compliance case),
`campaigns.beneficiary_changed`, `campaigns.media_added`, `campaigns.media_removed`,
`campaigns.update_published`; `kyc.age_attested`, `kyc.level_changed` (existing);
`compliance.restriction_applied`, `compliance.restriction_lifted`; `auth.staff_link_confirmed`.

Audit actions mirror the brief (§34) as `campaign.draft_created`, `campaign.updated`, `campaign.submitted`,
`campaign.review_assigned`, `campaign.approved`, `campaign.rejected`, `campaign.published`, `campaign.paused`,
`campaign.suspended`, `campaign.reactivated`, `campaign.completed`, `campaign.archived`,
`campaign.beneficiary_changed`, `campaign.media_added`, `campaign.media_removed`, `campaign.update_published`,
each with `from_status`/`to_status`, reason and the request correlation id.

## 8. Permissions (new or changed, lead migration)

New: `campaign.decide.high` (COMPLIANCE, step-up), `campaign.publish` (REVIEWER, COMPLIANCE, step-up),
`campaign.category.manage` (ADMIN, step-up). Changed: `campaign.decide` requires step-up;
`campaign.review` also granted to COMPLIANCE. Existing: `campaign.view`, `campaign.suspend`,
`campaign.unsuspend`, `content.moderate`, `campaign.review_policy.request/approve`.

## 9. Content rules

Plain text only. Titles 10–120 characters, summary 20–300, story 100–20 000 (policy values). HTML tags are refused
(`HTML_NOT_ALLOWED`); `javascript:`/`data:`/`vbscript:` URLs are refused (`UNSAFE_LINK`); control characters
are stripped or refused. The web app renders text escaped, never with `dangerouslySetInnerHTML`, and never turns
text into links.

## 10. Changes during implementation

- `visibility` is `PUBLIC` | `UNLISTED` only (no `HIDDEN`); exposure needs a live status.
- `SUSPENDED → APPROVED` added for campaigns suspended before they were ever published (ADR-036 §2).
- Review detail moved to `GET /admin/campaigns/{campaign_id}/review` (`review/{id}` collided with `{id}/media`); the
  staff media list is `GET /admin/campaigns/{id}/media/all`.
- Added: `POST /campaigns/{campaign_id}/revise` (REJECTED → DRAFT), `GET /campaign-currencies` (`[{code, minor_units,
  display_symbol}]`), staff `complete` and `archive` (`campaign.suspend`); media upload requires `depicts_minor`
  (`true` → 422 `MINOR_MEDIA_NOT_SUPPORTED`).
- Eligibility is a separate endpoint (`GET /campaigns/{campaign_id}/eligibility?action=`), not embedded in the owner view.
  Codes also include `AGE_REQUIREMENT_NOT_MET`, `MINOR_DISCLOSURE_NOT_ALLOWED`, `INVALID_LENGTH`; `NOT_AUTHENTICATED`
  and `SECOND_APPROVAL_REQUIRED` are not emitted (401 / 202 instead).
- `CURRENCY_NOT_AVAILABLE` on create/PATCH is a `VALIDATION_FAILED` detail (field `goal.currency`).
- If-Match handling, path parameter names and the ignored queue `?status=` are inconsistent (KI-S6-08/09/10).
- Extra tables: `app.campaign_media_events`, `app.campaign_update_events`; staff link requests `app.staff_link_requests`.
- `GET /admin/campaign-categories` uses `campaign.view`; only PATCH needs `campaign.category.manage`.
- Completion reasons accepted from owners: `ORGANISER_COMPLETED`, `OTHER` (`GOAL_REACHED` refused, `EXPIRED` unused).

