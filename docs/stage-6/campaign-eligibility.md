# Stage 6 — Campaign eligibility

> `internal/campaigns/eligibility.go`; ADR-037 for age attestation, BASIC_VERIFIED and restrictions. There is no
> universal "eligible" flag: every action has its own gates. A publicly visible campaign is not thereby able to accept
> donations (donation eligibility belongs to the payment stages).

## 1. Actions and gates (campaign policy `campaign-v1`, INTERNAL_POLICY)

| Check | CREATE_DRAFT | EDIT_DRAFT / UPDATE_PUBLISHED | SUBMIT_FOR_REVIEW | APPROVE | PUBLISH | REACTIVATE (and resume) |
|---|---|---|---|---|---|---|
| Account active (personal account) | ✓ | ✓ | ✓ | ✓ | ✓ | ✓ |
| Age: current `ATTESTED` declaration or document-verified DOB; no verified DOB below `adult_age` | ✓ | ✓ | ✓ | ✓ | ✓ | ✓ |
| KYC level (status ACTIVE) | BASIC_VERIFIED | BASIC_VERIFIED | IDENTITY_VERIFIED | IDENTITY_VERIFIED (owner) | IDENTITY_VERIFIED (owner) | IDENTITY_VERIFIED (owner) |
| Organisation: actor is ORG_ADMIN | ✓ | ✓ | ✓ | — | — | — |
| Organisation: `ORG_KYB_VERIFIED` + representative authority `org.campaign.submit` | — | — | ✓ | ✓ | ✓ | ✓ |
| Compliance restriction (owner, organisation, beneficiary, campaign) | matrix §3 | matrix §3 | matrix §3 | matrix §3 | matrix §3 | matrix §3 |
| Category active; organisation-only categories need an organisation | ✓ | — | ✓ | ✓ | ✓ | ✓ |
| Content lengths (title 10–120, summary 20–300, story 100–20 000) | title/summary | edited fields | ✓ | ✓ (snapshot) | ✓ | ✓ |
| Goal currency usable + goal within policy range | ✓ | edited | ✓ | ✓ | ✓ | ✓ |
| Beneficiary linked, owned by the campaign owner, verified | — | — | ✓ | ✓ | ✓ | ✓ |
| Cover image approved, no media still scanning | — | — | ✓ | ✓ | ✓ | ✓ |
| Resubmission limit | — | — | ✓ | — | — | — |

The staff actions (APPROVE, PUBLISH, REACTIVATE) evaluate the **owner side**: a reviewer cannot approve or publish a
campaign whose owner lost identity verification, was restricted, or whose beneficiary is no longer verified
(`TestCampaignSecurityRechecks`). SUBMIT, APPROVE, PUBLISH and REACTIVATE evaluations — allowed or refused — are stored
in `app.campaign_eligibility_evaluations` with the policy version.

## 2. Reason codes

`ACCOUNT_NOT_ACTIVE`, `AGE_ATTESTATION_REQUIRED`, `AGE_REQUIREMENT_NOT_MET`, `BASIC_VERIFICATION_REQUIRED`,
`IDENTITY_VERIFICATION_REQUIRED`, `ORGANISATION_VERIFICATION_REQUIRED`, `NOT_ORGANISATION_ADMIN`,
`REPRESENTATIVE_AUTHORITY_REQUIRED`, `BENEFICIARY_REQUIRED`, `BENEFICIARY_NOT_VERIFIED`, `BENEFICIARY_NOT_AUTHORISED`,
`INDIVIDUAL_FOR_OTHERS_DISABLED`, `MINOR_DISCLOSURE_NOT_ALLOWED`, `ACCOUNT_RESTRICTED`, `CATEGORY_INACTIVE`,
`CATEGORY_REQUIRES_ORGANISATION`, `COVER_IMAGE_REQUIRED`, `MEDIA_NOT_READY`, `CURRENCY_NOT_AVAILABLE`,
`GOAL_OUT_OF_RANGE`, `MISSING_FIELD`, `INVALID_LENGTH`, `RESUBMISSION_LIMIT_REACHED`.

A refused action returns `422 NOT_ELIGIBLE` with `details: [{field, code}]`; `GET /campaigns/{id}/eligibility?action=`
returns `{action, allowed, reasons: [{code, field, message}], policy_version}` without side effects.

## 3. Compliance restriction matrix (ADR-037 §3)

| Restriction level (highest of owner, organisation, beneficiary, campaign) | Blocked | Allowed |
|---|---|---|
| none | — | everything else permitted |
| `RESTRICTED` (resolution RESTRICT) | create, submit, approve, publish, resume/reactivate, beneficiary change | editing drafts, editing a live campaign (still re-reviewed), updates (forced into moderation) |
| `SUSPENDED` (SUSPEND), `OFFBOARDED` (OFFBOARD, CONFIRMED_FRAUD) | every campaign action | — ; APPROVED/ACTIVE/PAUSED campaigns are suspended by the worker (`campaigns.restriction_enforcement`) |

The reason is never disclosed (`ACCOUNT_RESTRICTED`: "This action is not available for this account."), which also
protects STR-restricted cases from tipping off. A failure to read restrictions fails closed (the action errors, nothing
transitions; `TestCampaignFailureInjection`). Lifting a restriction never reactivates a campaign.

## 4. Age attestation and BASIC_VERIFIED

- Statement: "I am at least `adult_age` years old" (`adult_age` from the verification policy, seeded 18,
  INTERNAL_POLICY; **LEGAL_REVIEW_REQUIRED** — LR-021 age of users, LR-015 minors). Statement version
  `age-statement-v1`; only the current version is accepted.
- Recorded append-only with user, outcome (`ATTESTED`/`DECLINED`), statement version, `adult_age`, policy version,
  source (`REGISTRATION`, `DASHBOARD`, `CAMPAIGN_FLOW`), time, IP (not for registration) and `SELF_ATTESTED` assurance.
  A change is a new row (`kyc.age_attested` event, audit).
- BASIC_VERIFIED requires email verified, phone verified, the current attestation `ATTESTED` for at least the policy
  age, and an active account; it is withdrawn when one stops holding. The device/IP-risk input named in
  kyc-architecture.md is **not** used (no such signal exists; ADR-037 §2). Minors cannot organise or receive donations
  as account holders: a `DECLINED` declaration or a verified DOB below `adult_age` blocks every action.

## 5. Beneficiaries and fundraising basis

- The beneficiary must belong to the campaign owner (same user or same organisation); someone else's beneficiary is
  refused (`BENEFICIARY_NOT_AUTHORISED`). The owner is never assumed to be the payout recipient.
- `SELF` beneficiary of an identity-verified owner: no separate approval needed (not REJECTED/REVOKED/SUSPENDED/EXPIRED).
  Every other type must be `APPROVED` (Stage 5 verification; minors with four eyes).
- `fundraising_basis` is set at submission: `SELF_FUNDRAISING`, `INDIVIDUAL_FOR_OTHERS` or `ORGANISATION_OWN_CAUSE`.
  Fundraising-authority records (PVO registration, s8 authority) are **not built**: individuals raising for someone
  else are blocked while `campaign.individual_for_others.enabled` is false (seeded false; LR-046 – LR-048, PD-27,
  **LEGAL_REVIEW_REQUIRED**).
- Public disclosure of the beneficiary name: `DISPLAY_NAME` only with `consent_declared`, reviewed by staff, and never
  for minors (`MINOR_DISCLOSURE_NOT_ALLOWED`, LR-070). Default `NONE` ("details kept private").

## 6. Money

Goals are `amount_minor` (positive int64) + currency, never floats; no FX; no totals. Usable currencies are those
enabled with verified minor units: **USD** today. **ZWG is unavailable** (`minor_units_verified = false`, LR-043).
Goal limits per currency are INTERNAL_POLICY values in `campaign-v1`.
