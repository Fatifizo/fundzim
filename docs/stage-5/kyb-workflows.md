# Stage 5 — Organisation Verification (KYB) Workflows

> Code: `internal/kyc/kyb.go`, `internal/kyc/review.go` (approval effects), `internal/verification`
> (`/organisations/{org_id}/kyb…`). Design inputs: [compliance/kyb-architecture.md](../compliance/kyb-architecture.md).
> Requirements are INTERNAL_POLICY rules in the versioned verification policy, not legal conclusions.
> PVO registration and fundraising-authority questions remain LEGAL_REVIEW_REQUIRED (see
> [open-legal-questions](../compliance/open-legal-questions.md), LR-046 and related).

## 1. Model

- `kyc.kyb_organisations` — the verified legal record of an `app.organisations` organisation (one per
  organisation; created on the first KYB case). Holds the level (`ORG_UNVERIFIED` → `ORG_KYB_VERIFIED`) and
  status, legal details and the encrypted registered address.
- `kyc.kyb_cases` — the case (same `verification_case` machine as KYC, [kyc-workflows.md](kyc-workflows.md) §2),
  with the details being verified (registered name, trading name, registration number, registry,
  country of registration, encrypted registered address).
- `kyc.organisation_persons` — declared persons with roles `DIRECTOR`, `TRUSTEE`, `OFFICE_BEARER`,
  `BENEFICIAL_OWNER`, `CONTROLLER`, `REPRESENTATIVE`; optional ownership in **basis points** (integer
  0–10000, never a float); identity details are encrypted as a KYB_PERSON identity (no uniqueness across
  accounts: a director may also hold a personal account).
- `kyc.beneficial_owners`, `kyc.representative_authorities` — written only by an approval.

Organisation types are the implemented vocabulary (`COMPANY`, `TRUST`, `PVO`, `FAITH_BASED`, `SCHOOL`,
`HEALTH_INSTITUTION`, `COMMUNITY_BASED`, `SPORTS_CLUB`, `OTHER`; ADR-034). The policy has rules for
`TRUST` (trust deed + authority letter/board resolution), `PVO` (PVO or registration certificate +
constitution + authority; **four eyes**) and a `default` rule for every other type.

## 2. Authorisation and isolation

| Caller | `GET …/kyb` | Create / edit / persons / documents / submit / withdraw |
|---|---|---|
| ORG_ADMIN | full case (details, persons with masked ID numbers, documents) | yes |
| ORG_MEMBER | level, status and case **status only** (persons and documents empty) | `403` |
| Non-member | `404 ORGANISATION_NOT_FOUND` (existence not revealed) | `404` |
| Staff | via `/admin/verification` with `kyc.case.review`; decisions need `org.verification.decide` + step-up | — |

Document uploads for `KYB_CASE`/`KYB_PERSON` subjects are pre-authorised from the form fields (before the
file is read) against the caller's ORG_ADMIN membership. A reviewer who is a member of the organisation,
created the case or submitted it is conflicted and cannot be assigned or decide (also when the conflict is
through the personal account linked to their staff account).

## 3. Flow

1. ORG_ADMIN with a verified email starts the case (`POST /organisations/{id}/kyb`).
2. Details (`PATCH`, `If-Match`), persons (`POST/DELETE …/persons`), documents.
3. **Submit** requires: registered name, registration number, country and address; at least
   `max(min_persons, 1)` declared persons; the policy's documents in CLEAN state; and the submitting
   ORG_ADMIN must personally be `IDENTITY_VERIFIED` and ACTIVE (`422 REPRESENTATIVE_NOT_VERIFIED`).
4. Review as for KYC. **A verified representative never verifies the organisation**: the organisation
   stays `ORG_UNVERIFIED` until a reviewer approves the KYB case (integration-tested).
5. **Approval re-checks at decision time** that the submitter is still an ORG_ADMIN
   (`409 REPRESENTATIVE_NO_LONGER_AUTHORISED`) and still identity-verified, and that an authority letter or
   board resolution is on the case (`409 AUTHORITY_EVIDENCE_MISSING`). It then supersedes earlier
   representative authorities, records the new one (bound to that evidence, valid until the KYB expiry),
   activates person identities, records persons with the `BENEFICIAL_OWNER` role as beneficial owners
   (determined by the approving reviewer; ownership as declared, no hard-coded ownership threshold), writes
   the KYB checks (screening `N_A` — not performed), copies the legal details to the organisation record
   (re-encrypting the address with the organisation-row AAD) and moves the organisation to
   `ORG_KYB_VERIFIED` (`expires_at` = 365 days by policy).

## 4. Not in Stage 5

- `ORG_REGISTERED_VERIFIED` and `ORG_PAYOUT_VERIFIED` levels exist in the vocabulary but nothing grants them
  yet (no registry lookup; no organisation payout-ownership upgrade) — KI-S5-16.
- Registry lookups (companies registry, PVO register) are manual document checks; no registry integration.
- PVO fundraising-authority records (`kyc.fundraising_authorities` in the Stage 1 design) are not built;
  campaigns (Stage 6) need them — see the handover.
