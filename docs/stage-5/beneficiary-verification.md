# Stage 5 — Beneficiary Verification

> Code: `internal/beneficiaries`, migration `20261009150300_beneficiaries.sql`, HTTP in
> `internal/verification`. Design inputs: [compliance/beneficiary-verification.md](../compliance/beneficiary-verification.md)
> (ADR-016). Consent and representation rules for minors and persons lacking capacity are
> **LEGAL_REVIEW_REQUIRED** (LR-015, LR-021, LR-058, LR-070); Stage 5 records declarations and evidence and
> routes them to human review — it does not decide what the law requires.

## 1. Model

A beneficiary is **who the money is for**, an entity independent of the owner who raises it
(`app.beneficiaries`). A verified owner does **not** make a beneficiary verified, and a relationship never
establishes authority on its own: the authority basis is declared separately and must be evidenced.

| Field | Values |
|---|---|
| `beneficiary_type` | `SELF`, `INDIVIDUAL`, `MINOR`, `INCAPACITATED_ADULT`, `ORGANISATION`, `COMMUNITY_GROUP` |
| relationship type | `SELF`, `PARENT_GUARDIAN`, `FAMILY_MEMBER`, `AUTHORIZED_REPRESENTATIVE`, `ORGANISATION_REPRESENTATIVE`, `THIRD_PARTY_ORGANISER`, `OTHER` (description required) |
| authority basis | `NOT_REQUIRED` (SELF only), `BENEFICIARY_CONSENT`, `PARENTAL_RESPONSIBILITY`, `GUARDIANSHIP_ORDER`, `LEGAL_REPRESENTATION`, `ORGANISATION_AUTHORITY`, `GROUP_MANDATE`, `INSTITUTION_CONFIRMATION` |
| owner | the calling user, or an organisation where the caller is ORG_ADMIN |

Name and date of birth for individual beneficiaries are encrypted in the `kyc` module (identity reference);
the owner's view shows them back to the owner only. Evidence documents are kyc documents with subject type
`BENEFICIARY`.

**Minors:** `MINOR` requires a date of birth below the policy's `adult_age` and an authority basis of
`PARENTAL_RESPONSIBILITY`, `GUARDIANSHIP_ORDER` or `LEGAL_REPRESENTATION`. An adult cannot be declared as a
`MINOR` (`NOT_A_MINOR`); a person under `adult_age` must use `MINOR` (`MINOR_MUST_USE_MINOR_TYPE`).
`MINOR` and `INCAPACITATED_ADULT` **always** need two reviewers (database CHECK, policy validation and code).
The age calculation compares (month, day); a leap-year off-by-one was found by a Stage 5 unit test and fixed.

## 2. Requirements (policy v1, INTERNAL_POLICY)

| Type | Documents | Four eyes |
|---|---|---|
| SELF | none | no |
| INDIVIDUAL | relationship evidence **or** consent form | no |
| MINOR | birth certificate; guardianship evidence or birth certificate; consent form | **yes** |
| INCAPACITATED_ADULT | guardianship evidence; medical evidence | **yes** |
| ORGANISATION | authority letter or registration certificate | no |
| COMMUNITY_GROUP | constitution or authority letter | no |

## 3. Workflow and access

Same `verification_case` vocabulary as KYC ([kyc-workflows.md](kyc-workflows.md) §2): DRAFT → SUBMITTED →
UNDER_REVIEW → (information request / escalation) → APPROVED | REJECTED, then EXPIRED / SUSPENDED / REVOKED.
Submission without the policy's CLEAN documents → `422 SUBMISSION_INCOMPLETE`. Edits and new evidence only
in editable states (`409 CASE_NOT_EDITABLE`); a decided beneficiary cannot be edited.

- Owner user / ORG_ADMIN of the owning organisation: edit; ORG_MEMBER: view; anyone else: `404
  BENEFICIARY_NOT_FOUND` (lists contain only own and own-organisation beneficiaries).
- Reviewers decide with `beneficiary.verification.decide` + step-up (held by KYC_REVIEWER; COMPLIANCE
  does not hold it). A reviewer who created or owns the beneficiary, is a member of the owning organisation,
  or whose linked personal account is any of those, is conflicted (`403 SELF_DECISION_FORBIDDEN`), and a
  trigger on `app.beneficiary_verifications` refuses conflicting rows in the database.
- Four-eyes: the first approval returns `202` and the status stays in review; only a second, different
  reviewer's `second-approval` approves (integration-tested: same reviewer → `403
  SECOND_APPROVER_MUST_DIFFER`).
- Information requests unanswered past the policy window are closed by the worker sweep.

Events: `beneficiaries.verification_{submitted,information_requested,approved,rejected,escalated,expired,
suspended,revoked}` (IDs and codes only) feed notifications, the risk module (rejected, escalated) and
compliance (escalated).
