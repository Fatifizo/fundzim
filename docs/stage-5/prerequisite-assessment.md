# Stage 5 Prerequisite Assessment

**Date:** 2026-10-09. **Branch:** `stage-5/verification`, created from `stage-4/identity-access` (`1adbb1e`).
Classification: SATISFIED · SATISFIED_WITH_CONDITIONS · BLOCKED · NOT_VERIFIED.

## 1. Read before implementation

CLAUDE.md, README, PRODUCT, ARCHITECTURE, SECURITY, ROADMAP; Stage 1 compliance specifications
(`kyc-architecture`, `kyb-architecture`, `beneficiary-verification`, `identity-data-protection`,
`operational-controls`, `aml-risk-framework`); Stage 2 design baseline and the SQL drafts `0003_audit`
(evidence), `0005_storage`, `0008_risk`, `0010_kyc`, `0012_beneficiaries`, `0013_compliance`,
`0016_payouts` (destinations), `0018_grants` (restricted roles, gateways, RLS); Stage 3 and Stage 4
implementation, security, testing and known-issues documents; `STAGE-4-TO-STAGE-5.md`.

## 2. Findings

| # | Prerequisite | Finding | Classification |
|---|---|---|---|
| 1 | Stage 4 deliverables | Present on `stage-4/identity-access` (`a3cd61b..1adbb1e`): auth, sessions, CSRF, MFA, RBAC with maker-checker, organisations, worker/outbox, distributed limits, nonce CSP. Local evidence: 113 unit, 47 integration, 184 web unit, 57 E2E tests passing at the end of Stage 4 | SATISFIED |
| 2 | **Stage 1–4 acceptance recorded** | No acceptance record exists for any stage. The Stage 5 prompt is an instruction to proceed, not an acceptance record. | **NOT_VERIFIED** — governance condition |
| 3 | **CI on GitHub** | Re-checked 2026-10-09: the public API reports `total_count: 0` workflows and runs for the repository. Stage 4 CI evidence does not exist. | **BLOCKED** (owner: enable Actions). Stage 5 proceeds on local evidence only and will say so |
| 4 | Authentication / sessions / step-up | Implemented and tested (Stage 4); staff sessions require TOTP (DB CHECK); `requires_step_up` on `kyc.document.view`, `kyc.identity_number.reveal`, `kyc.decision.record`, `beneficiary.verification.decide`, `org.verification.decide` | SATISFIED |
| 5 | Authorization | Deny-by-default policies, permission + step-up checks, staff 404 for non-staff; KYC_REVIEWER/COMPLIANCE hold the KYC permissions; SUPER_ADMIN, ADMIN and SUPPORT do **not** hold `kyc.document.view` | SATISFIED |
| 6 | Organisation membership + isolation | Implemented (Stage 4) with membership-scoped queries | SATISFIED |
| 7 | Audit infrastructure | Hash-chained business and security streams. **Evidence tables (`audit.evidence_records`, `audit.evidence_holds`) were deferred to Stage 5** (Stage 3 I-18) | SATISFIED_WITH_CONDITIONS — built first in Stage 5 |
| 8 | Restricted DB roles | `fundzim_kyc` and `fundzim_compliance` roles exist with generated passwords; config reserved `DATABASE_KYC_URL` / `DATABASE_COMPLIANCE_URL`; runtime grants v2 gives them only `app.status_transitions`. The Stage 2 gateway functions (`audit.append_event`, `audit.record_evidence`, `app.enqueue_outbox`) and RLS are not built | SATISFIED_WITH_CONDITIONS — built in Stage 5 |
| 9 | Object storage | Garage with three buckets and three credentials (`private-kyc` credential reserved for the kyc module); private buckets have no anonymous access (Stage 3 tests). No upload pipeline, metadata tables or malware scanning exist | SATISFIED_WITH_CONDITIONS — pipeline built in Stage 5 |
| 10 | Malware scanning | No scanner. Real scanning needs ClamAV (or equivalent) | Built in Stage 5 (ClamAV container where it runs; a development-only scanner refused in production) |
| 11 | Field encryption | AES-256-GCM + HMAC blind index exist (local keys); KMS is Stage 18 and production refuses to start | SATISFIED_WITH_CONDITIONS — Stage 5 uses **separate** C3 keys for KYC data |
| 12 | Worker / outbox | Implemented and tested (Stage 4) | SATISFIED |
| 13 | Identity-verification vendor | None selected (PD-25). The brief forbids automated provider integrations without approval | Manual review only; vendor adapter interface without a live implementation |
| 14 | Sanctions/PEP screening provider | None approved. The brief forbids live screening without an approved provider. Stage 1 requires `SANCTIONS_PEP_SCREEN` for IDENTITY_VERIFIED | **CONDITION:** the check is recorded as **not performed** (`N_A`, reason `SCREENING_PROVIDER_NOT_SELECTED`); approvals carry this as an explicit condition and it is a payout blocker for later stages. Never reported as CLEAR |
| 15 | Payout-destination ownership confirmation | No PSP or bank verification API is integrated (Stages 8–9) | Only evidence-based manual review or `PROVIDER_CONFIRMATION_REQUIRED`; a development mock is marked non-production and never makes a destination eligible |
| 16 | Payments / payouts / ledger | Not implemented; out of scope | SATISFIED (none will be built) |
| 17 | Stage 4 known issues relevant here | KI-S4-11 (organisation type vocabulary), KI-S4-08 (no organisation UI) | Addressed in Stage 5 where in scope |

## 3. Decision

Proceed with Stage 5. Nothing critical is missing for authentication, authorization or storage security;
the conditions above (evidence tables, gateways, scanning pipeline, separate keys) are built first. The
governance conditions (acceptance records, CI) remain open and are reported, not assumed.
