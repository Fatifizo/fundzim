# ADR-034: Verification, document and compliance state models (Stage 5)

- **Status:** Accepted
- **Date:** 2026-10-09
- **Deciders:** Project owner (Stage 5 brief), technical lead
- **Stage:** 5

## Context

The Stage 2 SQL drafts (`0010_kyc`, `0012_beneficiaries`, `0013_compliance`, `0016_payouts`, `0005_storage`)
were validated in memory but never executed. The Stage 5 brief, issued later by the owner, names explicit
status vocabularies for KYC cases, documents, payout destinations and compliance cases, and asks for one
reviewer workflow (assign, start review, request information, approve, reject, escalate, suspend, reopen).
The drafts use different names (e.g. KYC case `OPEN/IN_REVIEW/AWAITING_SUBMISSION/DECIDED`, compliance
`OPEN/IN_PROGRESS/AWAITING_INFO/PENDING_APPROVAL/DECIDED`). Two vocabularies for one concept would confuse
reviewers, users and later stages.

## Decision

The Stage 5 brief's vocabularies are canonical. The Stage 2 table designs (columns, append-only histories,
DB guards, encryption, maker-checker CHECKs) are kept; only the state names and transitions change. Every
machine is guarded in the database by `app.guard_transition` + `app.status_transitions`.

### 1. Verification cases (KYC, KYB, beneficiary) — machine `verification_case`

`NOT_STARTED` is not stored: it means "no case exists".

| From | To | Actor |
|---|---|---|
| (new) | `DRAFT` | subject |
| `DRAFT` | `SUBMITTED`, `WITHDRAWN` | subject |
| `SUBMITTED` | `UNDER_REVIEW` (start review, assigned reviewer), `WITHDRAWN` | reviewer / subject |
| `UNDER_REVIEW` | `ADDITIONAL_INFORMATION_REQUIRED`, `ESCALATED`, `APPROVED`, `REJECTED` | reviewer |
| `ADDITIONAL_INFORMATION_REQUIRED` | `SUBMITTED` (resubmitted), `WITHDRAWN`, `EXPIRED` (no response) | subject / system |
| `ESCALATED` | `UNDER_REVIEW` (returned), `APPROVED`, `REJECTED` | reviewer / compliance |
| `APPROVED` | `EXPIRED`, `SUSPENDED`, `REVOKED` | system / reviewer |
| `SUSPENDED` | `APPROVED` (reinstated), `UNDER_REVIEW` (reopened), `REVOKED` | reviewer |

`REJECTED`, `EXPIRED`, `REVOKED`, `WITHDRAWN` are final: re-verification is a new case. Decisions are
append-only rows; a case's decision is never edited. Mapping from the drafts: OPEN→DRAFT/SUBMITTED,
IN_REVIEW→UNDER_REVIEW, AWAITING_SUBMISSION→ADDITIONAL_INFORMATION_REQUIRED, DECIDED→APPROVED/REJECTED,
ABANDONED→WITHDRAWN/EXPIRED.

The KYC **profile** keeps the Stage 1 orthogonal model: `level` (UNVERIFIED, BASIC_VERIFIED,
IDENTITY_VERIFIED, PAYOUT_VERIFIED; organisations ORG_*) and `status` (ACTIVE, PENDING_REVIEW, REJECTED,
SUSPENDED). Case outcomes move the profile in the same transaction with an append-only `profile_events` row.

### 2. Internal risk levels

`LOW`, `STANDARD`, `ENHANCED`, `RESTRICTED` (brief §6) replace the drafts' `LOW/STANDARD/HIGH` (and
`ELEVATED`) for verification requirements and risk assessments. They are internal categories, not legal ones.

### 3. Documents (stored objects) — column `scan_status`

`UPLOADED` (row created, bytes arriving) → `QUARANTINED` (complete, hash and size verified, in the quarantine
prefix, scan pending) → `SCANNING` → `CLEAN` | `REJECTED` (malware or content that fails validation) |
`FAILED_SCAN` (scanner error/outage; retried, never treated as clean). `FAILED_SCAN` → `SCANNING` (retry).
`UPLOADED` → `REJECTED` when the content fails upload validation before quarantine (empty, over the size
limit, type not allowed, or sniffed type different from the declared one) — *amendment 2026-10-09, see
below*. `UPLOADED` never completed → `DELETED` by the expiry job. Any non-final or final state → `DELETED` (soft;
the row stays as proof). Only `CLEAN` objects can be opened.

### 4. Payout destinations

Status: `UNVERIFIED` → `PENDING_VERIFICATION` → `VERIFIED` | `REJECTED`; `VERIFIED` → `SUSPENDED` | `EXPIRED`
| `PENDING_VERIFICATION` (details changed); `SUSPENDED` → `PENDING_VERIFICATION` | `VERIFIED`;
`REJECTED`/`EXPIRED` → `PENDING_VERIFICATION` (new evidence). Plus `RETIRED` (owner removed it).
Three separate outcomes are recorded as append-only checks and never inferred from each other:
`FORMAT_VALIDATED` (syntax only), `OWNERSHIP_CONFIRMED` (evidence of ownership: provider confirmation, bank
letter, reviewed statement) and `COMPLIANCE_APPROVED`. `VERIFIED` requires the latter two. Where a provider
check is required but unavailable the ownership outcome is `PROVIDER_CONFIRMATION_REQUIRED`. A development
mock provider result is tagged `non_production` and can never satisfy `OWNERSHIP_CONFIRMED`.

### 5. Compliance cases — machine `compliance_case`

`OPEN` → `ASSIGNED` → `IN_REVIEW` ↔ `AWAITING_INFORMATION`; `IN_REVIEW` → `ESCALATED` → `IN_REVIEW`;
`IN_REVIEW`/`ESCALATED` → `RESOLVED` (decision recorded; maker-checker for listed decisions) → `CLOSED`;
`RESOLVED`/`CLOSED` → `IN_REVIEW` (reopen, recorded). Mapping: IN_PROGRESS→IN_REVIEW, AWAITING_INFO→
AWAITING_INFORMATION, PENDING_APPROVAL/DECIDED→RESOLVED (with `resolution_status` PROPOSED/APPROVED),
REOPENED→IN_REVIEW with a reopen event.

### 6. Beneficiary relationships

`SELF`, `PARENT_GUARDIAN`, `FAMILY_MEMBER`, `AUTHORIZED_REPRESENTATIVE`, `ORGANISATION_REPRESENTATIVE`,
`THIRD_PARTY_ORGANISER`, `OTHER` (brief §10) replace the drafts' finer list. The **authority basis** (consent,
guardianship evidence, mandate…) stays a separate field: a relationship type never establishes authority.

### 7. Organisation types

The implemented `app.organisations.org_type` vocabulary (COMPANY, TRUST, PVO, FAITH_BASED, SCHOOL,
HEALTH_INSTITUTION, COMMUNITY_BASED, SPORTS_CLUB, OTHER; Stage 4) is canonical. The brief's list is mapped:
REGISTERED_BUSINESS→COMPANY, REGISTERED_CHARITY→TRUST or PVO, NONPROFIT→PVO, COMMUNITY_ORGANISATION→
COMMUNITY_BASED, RELIGIOUS_ORGANISATION→FAITH_BASED, EDUCATIONAL_INSTITUTION→SCHOOL. KYB requirements are
per type in the verification policy, not assumed identical. Resolves Stage 4 KI-S4-11 for new schemas.

## Consequences

- One reviewer workflow and one status vocabulary across KYC, KYB and beneficiaries.
- The Stage 2 drafts remain historical design input; `docs/database/*` note the superseded names.
- The OpenAPI contract uses these names.

## Related

ADR-015, ADR-016, ADR-017, ADR-024, ADR-035; Stage 2 drafts listed above; docs/stage-5/*.

## Amendments

- **2026-10-09 (Stage 5 implementation):** added the `stored_object_scan` edge `UPLOADED → REJECTED` (§3). Content
  refused by upload validation is recorded as REJECTED without ever reaching quarantine or the scanner, so the
  refusal stays on record (the object row is kept as proof) and nothing from it can be opened. The Go and SQL edge
  lists were changed together (`migrations/20261009150100_storage.sql`).
- **2026-10-09:** the verification policy carries `adult_age` (INTERNAL_POLICY, seeded 18) instead of a
  hard-coded age in code. It drives the KYC self-verification minimum and the MINOR beneficiary boundary. It is
  not a legal conclusion: LR-021 (age of users) and LR-015 (minors and representation) remain OPEN.

