# Stage 5 Implementation — KYC, KYB, Beneficiary & Payout-Destination Verification, Documents, Compliance Cases

> Branch `stage-5/verification` (from `stage-4/identity-access` `1adbb1e`). This document records what was
> built, where it lives and how it maps to the Stage 5 brief. Evidence for every "tested" claim is in
> [testing.md](testing.md). Nothing here moves money: there is no payment processing, no payout execution, no
> ledger posting, no live identity-provider integration, no live sanctions screening and no regulatory
> reporting. A verified payout destination is **never** eligible for a payout in Stage 5
> (`eligible_for_payout` is always `false`).

Related: [kyc-workflows.md](kyc-workflows.md) · [kyb-workflows.md](kyb-workflows.md) ·
[beneficiary-verification.md](beneficiary-verification.md) ·
[payout-destination-verification.md](payout-destination-verification.md) ·
[document-security.md](document-security.md) · [compliance-review.md](compliance-review.md) ·
[security-review.md](security-review.md) · [testing.md](testing.md) · [known-issues.md](known-issues.md) ·
[interface-contracts.md](interface-contracts.md) · [prerequisite-assessment.md](prerequisite-assessment.md) ·
[ADR-034](../adr/ADR-034-verification-state-models.md) · [ADR-035](../adr/ADR-035-restricted-data-access-and-documents.md) ·
[handover](../stage-handover/STAGE-5-TO-STAGE-6.md)

---

## 1. Prerequisite gate

[prerequisite-assessment.md](prerequisite-assessment.md): Stages 1–4 are implemented but their acceptance is
**not recorded**; GitHub Actions lists no workflows and no runs (KI-S4-01, still BLOCKED). Stage 5 proceeded
on the user's explicit instruction. No sanctions/PEP screening provider and no identity-verification or
account-name-lookup provider is selected, so those capabilities are recorded honestly as *not performed*
(§6) instead of being simulated.

## 2. Modules and packages added

Module dependencies are enforced by `internal/archtest` (import graph and SQL table ownership,
mutation-checked).

| Package | Module | May import | Responsibility |
|---|---|---|---|
| `internal/storage` (+ `internal/platform/storage/sse.go`) | storage | platform, audit | Upload pipeline (size cap, SHA-256, magic-byte sniffing), quarantine, malware scanning (ClamAV `clamd` / EICAR-only dev scanner), CLEAN promotion, SSE-C encryption of private objects, download tickets, rescans and upload expiry jobs. Owns `app.stored_objects`, `app.upload_sessions` |
| `internal/kyc` | kyc | platform, audit, storage, users, organisations | Individual KYC and organisation KYB cases, identities (encrypted, blind-indexed), documents linkage, the review engine (assign/start/request-info/decide/second approval), versioned verification policy, expiry sweep, PAYOUT_VERIFIED upgrade. Uses **only** the `fundzim_kyc` pool; audit/evidence/outbox through gateways. Owns the `kyc` schema |
| `internal/beneficiaries` | beneficiaries | …, kyc | Beneficiaries as independent entities with relationship and authority basis, minors, review with four-eyes. Owns `app.beneficiar*` |
| `internal/payouts` | payouts | …, beneficiaries, compliance, risk, storage | Payout **destinations** only (no payouts): format validation, ownership/compliance checks bound to `details_version`, review, cooling-off timestamp. Owns `app.payout_destination*` |
| `internal/risk` | risk | platform, audit | Risk signals from events, versioned explainable model (`risk-model-v1`), assessments, routing decisions (NO_ACTION / MANUAL_REVIEW / ESCALATE), limits registry with maker-checker. Owns the `risk` schema |
| `internal/compliance` | compliance | platform, audit, users, organisations, kyc, risk, storage | Compliance cases (state machine, assignment, notes encrypted, maker-checker resolutions, RLS for STR-restricted cases). Uses **only** the `fundzim_compliance` pool. Owns the `compliance` schema |
| `internal/verification` | verification (orchestration; owns no tables) | all of the above | HTTP handlers for users, organisations and reviewers; document upload/access orchestration; merged reviewer queue |
| `internal/platform/verifcase` | platform | — | Shared `verification_case` state machine (edges compared with the migration by a unit test) |
| `internal/app/verification.go` | composition | — | Pools, module wiring, routes, outbox consumers (scan results, users KYC mirror, PAYOUT_VERIFIED upgrade, notifications), 15-minute sweep job |

Additions to existing modules: `audit.RecordGateway`, `audit.RecordEvidence(Gateway)`,
`outbox.WriteGateway`; `organisations` `MemberRole/IsAdmin/Summary/AdminIDs/OrganisationsOf`;
`users.DisplayNames/PersonalAccount/SetKYCMirror`; `auth.StaffHasPermission`; config group `Verification`
and new keys (§5); `httpx.BodyLimitWithExemptions` (upload route only).

## 3. Database (migrations `20261009150000`–`20261009150600`)

| Migration | Contents |
|---|---|
| `150000_audit_evidence_gateways` | `audit.evidence_records`, `audit.evidence_holds`; SECURITY DEFINER gateways `audit.append_event`, `audit.record_evidence`, `app.enqueue_outbox` with a per-role prefix allow-list (`audit.gateway_prefix_ok`, keyed on `session_user`) and a deep key deny-list (`audit.gateway_payload_ok`); runtime grants v3 deriving privileges for the `kyc` and `compliance` schemas |
| `150100_storage` | `app.stored_objects` (scan state machine `stored_object_scan`, bucket classes, purposes, SSE key id), `app.upload_sessions` |
| `150200_kyc` | `verification_profiles`, `kyb_organisations`, `identities` (AES-256-GCM ciphertext + HMAC blind index; one ACTIVE identity per number), `kyc_cases`, `kyb_cases`, `case_events` (exactly one event per case version — deferred constraint trigger), `profile_events`, `information_requests`, `review_notes` (encrypted), `organisation_persons`, `beneficial_owners`, `representative_authorities`, `consents` (+ `v_consents_current`), `kyc_documents` (composite FK to PRIVATE_KYC stored objects), `kyc_checks`, `kyb_checks`, `kyc_decisions` (self-decision trigger, second approver ≠ first), `verification_policies` (seeded v1); new permissions; `verification_case`, `kyc_verification_status`, `verification_policy` state machines |
| `150300_beneficiaries` | `beneficiaries` (four-eyes CHECK for MINOR / INCAPACITATED_ADULT), `beneficiary_relationships`, `beneficiary_events`, `beneficiary_information_requests`, `beneficiary_verifications` (conflict trigger) |
| `150400_payout_destinations` | `payout_destinations` (`details_version`; change guard resets to UNVERIFIED), `payout_destination_checks` (FORMAT / OWNERSHIP / COMPLIANCE; `DEV_MOCK_PROVIDER` must be `non_production`; a non-production OWNERSHIP PASS is refused), `payout_destination_events`; VERIFIED guard requiring a production OWNERSHIP PASS **and** COMPLIANCE PASS for the current `details_version` |
| `150500_risk` | `risk.limits`, `risk.limit_change_requests` (maker-checker), `risk.risk_signals`, `risk.risk_assessments`, `risk.risk_decisions`; `risk_limit` machine; INTERNAL_RISK baseline limits for model v1 |
| `150600_compliance` | `compliance.compliance_cases` (+ RLS for STR-restricted cases), `compliance_case_events`, `compliance_case_links`, `compliance_case_notes` (encrypted), `compliance_case_triggers`, `case_number_seq`; `compliance_case` machine |

All seven migrations were rolled down and re-applied cleanly on the development database ([testing.md](testing.md)).
Every state machine is guarded in the database (`app.guard_transition` / `app.status_transitions`; the
storage scan status by a dedicated trigger reading the same registry).

## 4. Brief coverage

| Brief area | Implemented | Doc |
|---|---|---|
| Individual KYC (statuses, workflow, LOW/STANDARD/ENHANCED/RESTRICTED, no hard-coded legal thresholds) | Yes | [kyc-workflows.md](kyc-workflows.md) |
| KYB (org types, persons, beneficial owners; verified representative ≠ verified organisation) | Yes | [kyb-workflows.md](kyb-workflows.md) |
| Organisation authorisation and cross-org isolation | Yes (ORG_ADMIN edits, members read status only, outsiders 404) | [kyb-workflows.md](kyb-workflows.md) |
| Beneficiaries (independent entities, relationship types, minors) | Yes | [beneficiary-verification.md](beneficiary-verification.md) |
| Payout destinations (statuses, separate FORMAT / OWNERSHIP / COMPLIANCE outcomes, PROVIDER_CONFIRMATION_REQUIRED, non-production mock) | Yes; never payout-eligible | [payout-destination-verification.md](payout-destination-verification.md) |
| Secure documents (states, scanner abstraction, access control, audited access, expiring access) | Yes | [document-security.md](document-security.md) |
| Review engine (ASSIGN_CASE … REOPEN) | Yes (+ RETURN, REINSTATE, REVOKE, SECOND_APPROVAL) | [kyc-workflows.md](kyc-workflows.md) §4 |
| Compliance cases (OPEN … CLOSED) | Yes | [compliance-review.md](compliance-review.md) |
| Risk foundation (signal ≠ score ≠ decision) | Yes | [compliance-review.md](compliance-review.md) §4 |
| Expiry and rechecks | Yes (sweep every 15 min; rescans every 30 s) | [kyc-workflows.md](kyc-workflows.md) §6 |
| Versioned, auditable policy engine | Yes (maker-checker; decisions record the policy version) | [kyc-workflows.md](kyc-workflows.md) §7 |
| Frontend (user, organisation, admin pages) | Yes — tested against a mock API; see known issues for the real-stack run | §7 |
| Notifications and audit events | Yes | §8 |
| OpenAPI | 51 operations added; route-coverage test passes | §9 |
| CI extension | See completion report (Actions not running) | [known-issues.md](known-issues.md) KI-S5-01 |

## 5. Configuration

New keys (`.env.example`; refusals in `internal/platform/config`): `DATABASE_KYC_URL`,
`DATABASE_COMPLIANCE_URL` (required, must differ from `DATABASE_URL`), `KYC_FIELD_ENCRYPTION_LOCAL_KEY`,
`KYC_BLIND_INDEX_KEY`, `COMPLIANCE_FIELD_ENCRYPTION_LOCAL_KEY`, `DOCUMENT_TICKET_KEY` (all distinct from
each other and from the Stage 4 keys), `STORAGE_SSE_C_KEY` (required outside development/test),
`MALWARE_SCANNER` (`clamd` | `dev`; `dev` refused outside development/test), `CLAMAV_ADDR`,
`MALWARE_SCAN_TIMEOUT` (60 s), `UPLOAD_MAX_BYTES` (10 MiB), `UPLOAD_TTL` (15 min),
`DOCUMENT_TICKET_TTL` (60 s). The local stack now defaults to the real ClamAV scanner and SSE-C encryption.
A process may omit the public-media storage credential entirely (the worker does, least privilege), but never
set it partially; the KYC and evidence credentials are required whenever storage is configured.

## 6. What is deliberately not done (honest status)

| Capability | Status in Stage 5 |
|---|---|
| Sanctions / PEP screening | **Not performed.** No provider selected; every approval records the condition `sanctions_pep_screening: NOT_PERFORMED_SCREENING_PROVIDER_NOT_SELECTED` and is `APPROVE_WITH_CONDITIONS`. LEGAL_REVIEW_REQUIRED (LR-009, LR-053, LR-064) |
| Automated identity verification (document authenticity, liveness) | **Not integrated.** Manual staff review only; no vendor adapter was approved. Remote identification standard: LR-062 |
| Account-name / ownership lookup for wallets and bank accounts | **Not available.** `PROVIDER_LOOKUP` is answered by a dev mock that records `PROVIDER_CONFIRMATION_REQUIRED` as a non-production check and can never confirm ownership (PROVIDER_CONFIRMATION_REQUIRED, PCR-013) |
| Payouts, payout eligibility | **Not built** (Stage 11). `eligible_for_payout` is always `false` |
| Regulatory reporting (STR etc.) | Not built. RLS for STR-restricted compliance cases exists |
| KMS / key rotation | Local keys only; production start-up is refused until KMS exists (Stage 18) |
| Production malware-scanning deployment | ClamAV runs in the local compose stack; signature-update and production deployment are not done (Stage 18) |

## 7. Frontend (`apps/web`)

User: `/dashboard/verification` (+ `identity`, `documents`, `beneficiaries`, `payout-destinations`).
Organisation: `/dashboard/organisations`, `/dashboard/organisations/[id]/verification` (ORG_ADMIN edits,
ORG_MEMBER reads status, others 404). Staff: `/admin/verification` (+ `kyc`, `kyb`, `beneficiaries`,
`payout-destinations`), `/admin/verification/cases/[caseId]`, `/admin/compliance/cases` (+ detail).
`requireStaff()` returns 404 for anonymous users, ordinary users and SUPPORT staff. The `/api/v1` web proxy
allows a larger body only for `POST /api/v1/verification/documents`. Details: `apps/web/README.md`.

## 8. Notifications and audit

Email notices (worker consumer `verification.notify`) for KYC submitted / information requested / approved /
rejected / expired / suspended / revoked, beneficiary submitted / information requested / approved / rejected /
expired, and payout destination verified / rejected / information requested / suspended / expired. Notices
carry no identity data. Every state change writes a case event (one per version) and an audit event; document
views write `kyc.document.accessed` (security stream); identity-number reveals write
`kyc.identity_number.revealed` with the justification; first approvals write `*.case.first_approval`.

## 9. OpenAPI

`api/openapi/fundzim-v1.yaml` gained 51 implemented operations (KYC, KYB, beneficiaries, payout destinations,
documents, reviewer queue/actions/policies, compliance actions) and the schemas they use.
`TestEveryRouteHasAPolicyAndIsInOpenAPI` passes; `@redocly/cli@2.54.3 lint` reports the file valid (2 pre-existing `/healthz` `/readyz` warnings).
