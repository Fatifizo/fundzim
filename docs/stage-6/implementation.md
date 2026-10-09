# Stage 6 — Campaign engine: what was implemented

> Branch `stage-6/campaigns` (from `stage-5/verification` at `d4b4734`). Decisions: ADR-036 (lifecycle, versions,
> controlled publication) and ADR-037 (age attestation and BASIC_VERIFIED, compliance restriction enforcement,
> staff ↔ personal links). Contracts: [interface-contracts.md](interface-contracts.md). Prerequisites:
> [prerequisite-assessment.md](prerequisite-assessment.md).
>
> **No financial functionality.** There are no donations, payments, payouts, ledger postings, raised totals or
> balances anywhere in Stage 6. Every campaign view says "Donations are not yet available."

## 1. Stage 5 remediation (brief §2)

| Item | Result | Where |
|---|---|---|
| A. Age attestation and BASIC_VERIFIED | `kyc.age_attestations` (append-only); `POST/GET /me/age-attestation`; optional `age_attestation` at registration (recorded by a worker consumer, source `REGISTRATION`). BASIC_VERIFIED is granted from email verified + phone verified + current `ATTESTED` declaration + active account; withdrawn when a condition stops holding. A self-attestation is never treated as proof of age (`assurance: SELF_ATTESTED`); a document-verified date of birth (`DOCUMENT_VERIFIED`) wins over it, and one below `adult_age` overrides any attestation. `adult_age` is INTERNAL_POLICY (LR-021, LEGAL_REVIEW_REQUIRED) | `internal/kyc/age.go`, `internal/verification/age.go`, migration `20261009170000`; verification policy v2 (v1 retired, not edited) |
| B. Compliance enforcement | `compliance.subject_restrictions` maintained in the same transaction as resolution approval (`RESTRICT` → RESTRICTED, `SUSPEND` → SUSPENDED, `OFFBOARD`/`CONFIRMED_FRAUD` → OFFBOARDED; lifted by an approved `CLEARED`/`EDD_CONDITIONS` re-resolution). Events `compliance.restriction_applied` / `_lifted` (ids and levels only). Campaigns enforce the matrix at every gated action and suspend affected campaigns on SUSPENDED/OFFBOARDED; lifting never reactivates | `internal/compliance/restrictions.go`, migration `20261009170200`, `internal/campaigns/eligibility.go`, `internal/app/campaigns.go` |
| C. Staff ↔ personal links | Staff (fresh step-up) requests a link by email; the personal account confirms a single-use emailed token from its own session; the link is immutable through the API. `app.actor_identities(uuid)` (links in both directions) is used by the KYC decision trigger and the campaign review, campaign update and Go self-review checks | `internal/auth/stafflink.go`, migration `20261009170100` |
| D. Remote CI | Root cause found and fixed (`branches: [main, "stage-*"]` never matched `stage-N/name`; now `stage-**`). The first runs found three real problems, all fixed: web typecheck on a clean checkout (`next typegen`), a data race in a notifications test helper, and govulncheck running on Go 1.27.1 (`go.mod` now `go 1.27.2`) | `.github/workflows/ci.yml`, `go.mod` |
| E. Provider/legal dependencies | Preserved: `SCREENING_PROVIDER_NOT_SELECTED` (campaign policy), PROVIDER_CONFIRMATION_REQUIRED (payout ownership, unchanged), LEGAL_REVIEW_REQUIRED (LR-015, LR-019, LR-021, LR-043, LR-046 – LR-048, LR-070) | — |

## 2. Campaign domain (`internal/campaigns`, module `campaigns`)

| Package | Owns | Responsibility |
|---|---|---|
| `campaigns` (core) | `campaign_categories`, `campaign_policies`, `campaigns`, `campaign_goals`, `campaign_versions`, `campaign_beneficiaries`, `campaign_reviews`, `campaign_status_history`, `campaign_eligibility_evaluations` | ownership and access, content and goal, lifecycle, eligibility, beneficiary association and change control, review and moderation, publication, public read and search, categories, restriction-driven suspension |
| `campaigns/media` | `campaign_media`, `campaign_media_events` | upload, scan, image processing, moderation removal, serving ([campaign-media.md](campaign-media.md)) |
| `campaigns/updates` | `campaign_updates`, `campaign_update_events` | owner progress updates, pre-moderation, public listing |

The core never imports `media` or `updates`; it reaches media through the `MediaInfo` interface injected in
`internal/app`. Restrictions, age attestation and feature flags are read through small adapters
(`internal/app/campaigns.go`) over `compliance.Service.Restrictions`, `kyc.Service.AgeStatus` and
`platform/featureflags`. Allowed imports (archtest): platform, audit, users, organisations, kyc, beneficiaries,
compliance, risk, storage.

Separation the brief asks for: **ownership** (`owner_user_id` XOR `owner_organisation_id`, immutable), **content**
(working copy on `campaigns`; what is public is an approved `campaign_versions` snapshot), **beneficiaries**
(`campaign_beneficiaries` → `app.beneficiaries`, verified in Stage 5), **verification** (eligibility evaluations
against kyc/kyb/beneficiary state), **moderation** (`campaign_reviews`, update and media moderation),
**publication** (status + visibility), **financial activity** (none; reserved for later stages).

## 3. Data model highlights (migration `20261009171000_campaigns.sql`)

- Goal: `goal_amount_minor bigint > 0` + `goal_currency char(3)`; history in append-only `campaign_goals`. JSON money is
  `{amount_minor: "digits", currency}`; parsing rejects decimals, signs, leading zeros and overflow. Only currencies that
  are enabled **and** have verified minor units are usable (`GET /campaign-currencies`); ZWG is refused (LR-043).
- No raised, total or balance column on any campaign table.
- Identity: `public_code` (10 random Crockford base32 characters) and `slug` (`<title>-<code>`, fixed at first
  publication). Unpublished, suspended, archived or unknown slugs are all 404.
- Lifecycle guarded by `app.guard_transition('campaign')`; one `campaign_status_history` row per version (deferred
  constraint); content frozen in SUBMITTED, UNDER_REVIEW, APPROVED, REJECTED, CANCELLED, ARCHIVED, SUSPENDED; goal
  currency fixed after the first submission; slug fixed once published.
- Reviews: one open review per campaign; decided reviews are final; four-eyes CHECK for tiers that require it; a
  SECURITY DEFINER trigger refuses any review actor who is (or is linked to) the owner, creator or an organisation member.
- Campaign policy `campaign-v1` (INTERNAL_POLICY): gates, content limits, goal limits per currency, resubmission limit,
  owner pause, cover required, tier rules (HIGH = four eyes), screening `SCREENING_PROVIDER_NOT_SELECTED`.
- Categories: 11 seeded (MEDICAL, EDUCATION, EMERGENCY, FUNERAL, DISASTER_RELIEF, COMMUNITY, CHARITY, RELIGIOUS,
  BUSINESS_SUPPORT, ANIMAL_WELFARE, OTHER) with provisional tiers from campaign-approval-policy §4.

## 4. API

All routes are in `api/openapi/fundzim-v1.yaml` (60 Stage 6 operations, IMPLEMENTED; 12 Stage 2 campaign design
operations SUPERSEDED). Summary in [interface-contracts.md §6](interface-contracts.md). Staff permissions:
`campaign.view`, `campaign.review`, `campaign.decide` (now step-up), `campaign.decide.high` (new, COMPLIANCE),
`campaign.publish` (new), `campaign.suspend`, `campaign.unsuspend`, `campaign.category.manage` (new, ADMIN),
`content.moderate`.

## 5. Frontend

Creator dashboard and 8-step wizard (`/dashboard/campaigns…`), staff review (`/admin/campaigns…`), update moderation,
age declaration, staff link pages, public `/campaigns` and `/campaigns/[slug]` with a disabled "Donations are not yet
available." button. Details: `apps/web/README.md` and the completion report.

## 6. Notifications and audit

Owner emails (worker, outbox): created, submitted, changes requested, approved, rejected, published, paused,
suspended, reactivated, completed — with no compliance reasons, reviewer identities or notes. Audit actions
`campaign.draft_created`, `.updated`, `.submitted`, `.review_assigned`, `.approved`, `.rejected`, `.published`,
`.paused`, `.suspended`, `.reactivated`, `.completed`, `.archived`, `.beneficiary_changed`, `.media_added`,
`.media_removed`, `.update_published` (and others), each with from/to status, reason code and the request
correlation id; `campaign_status_history` keeps the immutable lifecycle record.
