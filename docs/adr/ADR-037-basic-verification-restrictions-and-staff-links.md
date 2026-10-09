# ADR-037: Age attestation and BASIC_VERIFIED, compliance restriction enforcement, staff ↔ personal links

- **Status:** Accepted
- **Date:** 2026-10-09
- **Deciders:** Technical lead (Stage 6)
- **Stage:** 6

## Context

Stage 5 left three conditions that block campaigns (KI-S5-02, KI-S5-17, KI-S5-21):

- `BASIC_VERIFIED` was never granted. kyc-architecture.md requires email OTP, phone OTP, an age attestation
  and "device and IP risk is not BLOCK". There was no age attestation, and the risk model has no device or IP
  input (its decisions are `NO_ACTION`, `MANUAL_REVIEW`, `ESCALATE`; there is no `BLOCK`).
- Compliance resolutions (`RESTRICT`, `SUSPEND`, `OFFBOARD`, `CONFIRMED_FRAUD`) were recorded but not enforced.
- Nothing linked a staff account to the same person's personal account, so self-review checks could not see
  it.

## Decision

1. **Age attestation** (`kyc.age_attestations`, append-only). A signed-in user declares "I am at least
   `adult_age` years old", where `adult_age` comes from the current verification policy (INTERNAL_POLICY,
   seeded 18; LR-021 OPEN — LEGAL_REVIEW_REQUIRED). Each row records the user, the outcome (`ATTESTED` or
   `DECLINED`), the statement version, `adult_age` and the policy version, the source (`REGISTRATION`,
   `DASHBOARD`, `CAMPAIGN_FLOW`), the time and the assurance `SELF_ATTESTED`. A change is a new row, never an
   update. A self-attestation is **never** documentary proof of age. "Verified age" exists only when an
   approved KYC identity carries a date of birth at or above `adult_age` (assurance `DOCUMENT_VERIFIED`). A
   verified date of birth below `adult_age`, or a later `DECLINED` row, overrides any attestation.

2. **BASIC_VERIFIED** is granted (a `profile_events` row and a profile level change in the kyc schema) when
   all of these hold: the login email is verified; a phone number is verified; the current attestation is
   `ATTESTED` for an `adult_age` at least the current policy's; the account is `ACTIVE`. It is withdrawn when
   any condition stops holding (only if the level is still `BASIC_VERIFIED`; higher levels have their own
   rules). **Deviation from kyc-architecture.md:** the device/IP-risk input is dropped until a risk model that
   produces such a signal exists. Compliance restrictions are enforced separately at every gated action (§3),
   so dropping the risk input does not let a restricted account act.

3. **Compliance restrictions** are a projection in the compliance schema
   (`compliance.subject_restrictions`), maintained in the same transaction as the case change:

   | Approved resolution | Restriction level |
   |---|---|
   | `RESTRICT` | `RESTRICTED` |
   | `SUSPEND` | `SUSPENDED` |
   | `OFFBOARD`, `CONFIRMED_FRAUD` | `OFFBOARDED` |

   A restriction applies to every party linked to the case as `PRIMARY_SUBJECT` (user, organisation,
   beneficiary, campaign). It is lifted only when the same case is reopened and resolved again with
   `CLEARED` or `EDD_CONDITIONS` (approved). Applying and lifting emit `compliance.restriction_applied` and
   `compliance.restriction_lifted` (ids and levels only, no reasons: STR cases must not tip off). Other modules
   read restrictions through `compliance.Service.Restrictions(...)`, never the table. Messages to users say
   only that the action is not available on their account.

   Enforcement matrix for campaign actions: `RESTRICTED` blocks new fundraising (create, submit, approve,
   publish, resume, reactivate, beneficiary change) but lets existing campaigns continue; `SUSPENDED` and
   `OFFBOARDED` block every campaign action and suspend affected campaigns (ADR-036 §6).

4. **Staff ↔ personal links.** A staff member (staff session, fresh step-up) requests a link to a personal
   account by email. The personal account must have that verified login email and confirms with a single-use
   token mailed to it, from its own session. The link (`app.users.staff_personal_user_id`) is then set,
   audited on both sides, and **cannot be removed** through the API in Stage 6 (unlinking would weaken
   conflict checks; it needs a future maker-checker procedure). A database function
   `app.actor_identities(uuid)` returns the actor and any linked account, and the KYC and campaign
   self-decision triggers use it, so linked identities are refused by the database, not only in Go.

## Consequences

- Campaign drafts can be created once BASIC_VERIFIED is granted; submission still needs `IDENTITY_VERIFIED`
  (PRODUCT §6.2).
- The device/IP-risk input is an open item (Stage 6 known issues).
- A staff member who never links a personal account is not detected by these checks. Conflict declarations
  (`app.staff_conflict_declarations`) remain the procedural control.

## Related

ADR-034, ADR-035, ADR-036; kyc-architecture.md; operational-controls.md; LR-021, LR-015.
