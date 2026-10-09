# Stage 5 Security Review — Verification, Restricted Data, Documents and Compliance Cases

> **Scope:** branch `stage-5/verification` — `internal/{storage,kyc,beneficiaries,payouts,risk,compliance,verification}`,
> `internal/platform/{storage/sse,verifcase,config}`, the audit/outbox gateways, migrations
> `20261009150000`–`150600`, `apps/web` verification pages, compose. **Method:** threat/abuse analysis
> against [SECURITY.md](../SECURITY.md), [THREAT-MODEL.md](../THREAT-MODEL.md),
> [identity-data-protection.md](../security/identity-data-protection.md) and the Stage 5 brief, plus the
> adversarial integration tests in [testing.md](testing.md). This is an engineering self-review by the
> implementing team — **not** an independent audit or penetration test, and it makes no compliance or
> certification claim.

## 1. Threats and controls

| Threat / abuse | Controls | Evidence |
|---|---|---|
| **IDOR** on cases, documents, beneficiaries, destinations, KYB | Every handler authorises the object server-side (owner, ORG_ADMIN, membership, assigned reviewer); unknown or foreign objects → `404` (existence not revealed); uploads are pre-authorised from form fields before the file is read | `TestVerificationKYCEndToEnd` (foreign PATCH/submit/upload/document/access → 404), `TestVerificationKYBRepresentativeAndIsolation`, beneficiary and destination tests |
| **Self-approval** | Reviewer cannot be assigned to or decide a case about themselves, their organisation, or what they created/submitted/changed; the check includes the **personal account linked to a staff account**; KYC decisions, beneficiary verifications and compliance decisions are also refused by database triggers; assignee must be eligible staff (`ASSIGNEE_NOT_ELIGIBLE`) | `TestVerificationSelfReviewAndDuplicateIdentity` (linked reviewer → `403 SELF_DECISION_FORBIDDEN`; assigning them → 403; no self-decision rows in the DB); compliance maker-checker tests |
| **Four-eyes bypass** | `requires_second_approval` cases and `FRAUD_*` rejections need a second, different reviewer (code + CHECK + trigger); policy validation refuses MINOR/INCAPACITATED_ADULT/RESTRICTED without four eyes; compliance RESTRICT/SUSPEND/OFFBOARD/CONFIRMED_FRAUD are maker-checker; policy versions and risk limits are maker-checker | beneficiary four-eyes test, duplicate-identity test, `TestComplianceMakerCheckerAndSubjectConflicts`, `TestRiskLimitsMakerCheckerAndBaseline`, `TestPolicyValidateRefusesUnsafeRules` |
| **Duplicate identities / account farming** | HMAC blind index over the normalised ID number; duplicates are routed to ENHANCED + four-eyes (never auto-rejected); a unique index allows only one ACTIVE identity per number | duplicate-identity test (`409 DUPLICATE_IDENTITY_ACTIVE` on second approval) |
| **Document access** | No public or direct URLs; reviewer access needs `kyc.document.view` + fresh step-up + assignment; 60 s tickets bound to object, user and session; re-authorisation at download; every content access audited | KYC end-to-end (other user, other session, no ticket, unassigned reviewer, support) and failure-injection (expired ticket) |
| **Untrusted filenames / content** | Filenames never stored or used; magic-byte allow-list; declared type must match; markup in the sniff window refused; downloads `attachment` + `nosniff` + `CSP: sandbox` | storage unit tests; KYC end-to-end (`text/html` → `422 UNSUPPORTED_FILE_TYPE`; traversal filename absent from responses) |
| **Malware** | Quarantine prefix; only CLEAN can be opened or satisfy a requirement; scanner errors → FAILED_SCAN (never CLEAN); interrupted scans fenced; dev scanner refused in production and labelled as not malware protection | `TestStoragePipelineEICARRejected`, `…ScannerOutageThenRecovery`, `…InterruptedScanIsFenced`, `TestVerificationFailureInjection` (EICAR via real ClamAV → REJECTED, submit refused) |
| **Mock-provider misuse** | The dev mock records `PROVIDER_CONFIRMATION_REQUIRED` with `non_production = true`; the database refuses a non-production OWNERSHIP PASS and refuses VERIFIED without production ownership + compliance PASS for the current details; `eligible_for_payout` is constant `false` | `TestVerificationPayoutDestinationNeverVerifiedByFormatOrMock` (direct SQL attempts refused) |
| **Format ≠ ownership** | Format validation is its own check; VERIFIED needs reviewed ownership evidence with a name match; any detail change bumps `details_version` and resets to UNVERIFIED | destination test |
| **Admin / support over-reach** | SUPPORT, ADMIN and SUPER_ADMIN hold none of the KYC, document, decision, reveal or compliance permissions; admin pages return 404 for them in the web app | KYC end-to-end (support → 403 queue and documents) |
| **Sensitive data exposure** | ID numbers encrypted (AES-256-GCM, row/field AAD) and masked in every view; full number only via the reveal endpoint (COMPLIANCE, step-up, ≥ 10-char justification, security audit); KYC drafts, notes and addresses encrypted; separate keys for KYC, compliance, blind index, tickets and SSE-C (config refuses reuse) | KYC end-to-end: ID number absent from case, queue, detail, audit metadata and outbox payloads |
| **Audit coverage** | Case events one-per-version (deferred trigger); audit for every transition, upload, removal, document access, reveal, first approval and decision; restricted roles write audit/outbox only through gateways with per-role prefix allow-lists and a deep key deny-list | audit-chain test; `TestComplianceRoleIsGatewayOnly`; gateway tests per role |
| **Log redaction** | No document content, filenames or identity values in logs; scan logs carry IDs and codes; compliance notes never logged | code review; redaction allow-list unchanged |
| **Cross-organisation isolation** | KYB, beneficiaries and destinations owned by an organisation are scoped by membership role; non-members get 404 | KYB isolation test |
| **Concurrency** | Row locks + status checks; exactly one of competing decisions wins; one open case per user (unique index); idempotent resubmit; attach-vs-submit serialised on the case lock; one compliance case per trigger under duplicate delivery | `TestVerificationConcurrentReviewActions`, `TestComplianceConcurrentDecisionsExactlyOneWins`, `TestComplianceDuplicateDeliveryOpensOneCase`, `TestStoragePipelineConcurrentScansPromoteOnce` |
| **Risk-score misuse** | Signal, score and decision are separate tables; decisions only route to humans; no rule or output is labelled fraud; thresholds are versioned INTERNAL_RISK limits; missing limits fail closed to manual review | risk unit and integration tests |
| **Policy drift** | Policies are versioned with maker-checker; cases and decisions keep their policy version; historical decisions are never rewritten | policy unit tests |
| **Restricted DB roles** | `fundzim_kyc` and `fundzim_compliance` own their schemas only; no DELETE; separate pools and URLs (config refuses sharing `DATABASE_URL`) | gateway/role integration tests, config tests |

## 2. Findings during Stage 5

| ID | Finding | Status |
|---|---|---|
| F-S5-01 | Case assignment accepted any user id as assignee (e.g. a SUPPORT account or the subject). | **Fixed** — assignee must be active staff with `kyc.case.review` (`422 ASSIGNEE_NOT_ELIGIBLE`), integration-tested |
| F-S5-02 | KYC/KYB, beneficiary and destination conflict checks compared only the raw staff id, so a reviewer could decide the verification of their own **personal** account. | **Fixed** in code (`kyc.ActorIdentities`), integration-tested. Residual: the KYC decision trigger compares direct ids only, and there is no API to create the staff ↔ personal link (KI-S5-02, KI-S5-03) |
| F-S5-03 | Reviewer queue cursor used `submitted_at` alone: items sharing a timestamp at a page boundary were skipped. | **Fixed** — keyset `(submitted_at, id)`; mutation-checked test |
| F-S5-04 | Beneficiary age used day-of-year: off by one after 28 February in leap years (a person on their 18th birthday counted as 17). | **Fixed**; unit test |
| F-S5-05 | Adult age (18) was hard-coded in KYC and beneficiaries. | **Fixed** — `adult_age` in the versioned policy (INTERNAL_POLICY; LR-021 open) |
| F-S5-06 | Reviewer case detail lacked the risk level and four-eyes state. | **Fixed** — `review` object |
| F-S5-07 | GO-2026-6617: reachable HTTP/2 HPACK race in `net/http` reported by govulncheck on Go 1.27.1. | **Fixed** — toolchain go1.27.2 (`go.mod` `toolchain go1.27.2`; API image `golang:1.27.2-alpine3.24`); govulncheck reports 0 reachable vulnerabilities |
| F-S5-08 | Identity-number reveal is not limited to the assigned reviewer. (Its self-check originally used the direct id only; it now also covers a linked personal account, with an integration test.) | OPEN (KI-S5-10) — mitigated by COMPLIANCE-only permission, step-up, justification and audit |
| F-S5-09 | `DOCUMENT_TICKET_TTL` accepts up to 10 min in config while the ticket issuer caps at 5 min. | OPEN, low (KI-S5-18) |
| F-S5-10 | Payout-destination `start-review` returns success without an effect. | ACCEPTED (assignment starts the review), KI-S5-09 |

## 3. Not covered / out of scope

Live identity, screening and ownership providers (none selected); KMS and key rotation (Stage 18);
production ClamAV deployment and signature-freshness monitoring (Stage 18); retention purging (LR-012,
LR-074); `-race` (no C compiler locally and CI not running); an independent penetration test (Stage 18).
