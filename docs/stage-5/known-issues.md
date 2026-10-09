# Stage 5 Known Issues

Status: **OPEN** (owner stage named), **ACCEPTED** (deliberate, reason given), **BLOCKED** (needs an owner
action), **ENVIRONMENT** (property of the development machine). Security findings: [security-review.md](security-review.md) (F-S5-xx).

## 1. Earlier issues — status after Stage 5

| ID | Status now |
|---|---|
| KI-S4-01 CI not running | **BLOCKED** (unchanged) — see KI-S5-01 |
| KI-S4-02 production needs KMS / real SMS | OPEN (unchanged); Stage 5 adds more local keys (KI-S5-06) |
| KI-S4-08 no organisation UI | **RESOLVED** — organisation list/create and organisation verification pages |
| KI-S4-11 OpenAPI org_type enum | **RESOLVED for Stage 5 schemas** (implemented vocabulary, ADR-034); the Stage 2 design operations on shared paths were rewritten to the implemented handlers, and the 38 replaced design operations are marked `SUPERSEDED`; `/beneficiaries/{id}/consents` and `/organisations/{org_id}/fundraising-authorities` remain design-only (no Stage 5 replacement) |
| KI-S4-14 Playwright against a mock API | OPEN — a real-stack smoke of the user verification flow was run; staff pages still mock-only (KI-S5-08) |
| KI-15 acceptance records | OPEN — Stages 1–4 acceptance still not recorded |
| KI-02 no local `-race` | ENVIRONMENT (unchanged) |

## 2. Stage 5 issues

| ID | Issue | Impact | Status / owner |
|---|---|---|---|
| KI-S5-01 | **CI not verified** (carries KI-S4-01): GitHub lists no workflows or runs; `-race`, the CI integration job and container builds have not run on Stage 5 code. | No independent CI evidence | **BLOCKED** — owner: enable Actions; record results |
| KI-S5-02 | No API or ceremony links a staff account to the same person's personal account (`app.users.staff_personal_user_id`). Self-review checks honour the link when present; integration tests set it with a fixture. | Without the link, a person holding both accounts is only caught by role separation and organisation checks | OPEN — Stage 14 (staff onboarding declares the personal account, maker-checker) |
| KI-S5-03 | The KYC decision trigger compares direct ids only (the application layer also checks the linked personal account; the compliance trigger already checks it). | Defence in depth is one layer for the linked-account case | OPEN — next kyc migration |
| KI-S5-04 | **Screening not performed:** no sanctions/PEP provider; approvals are `APPROVE_WITH_CONDITIONS` with `NOT_PERFORMED_SCREENING_PROVIDER_NOT_SELECTED`. | Verified ≠ screened | OPEN — LEGAL_REVIEW_REQUIRED (LR-009, LR-053, LR-064); provider selection |
| KI-S5-05 | **No ownership/account-name lookup provider:** the dev mock is non-production and never PASS; ownership is confirmed only from reviewed documents. | Manual effort per destination | OPEN — PROVIDER_CONFIRMATION_REQUIRED (PCR-013) |
| KI-S5-06 | Local keys only (KYC field, KYC blind index, compliance notes, ticket, SSE-C); no rotation for SSE-C objects. Production start-up is refused until KMS. | By design for Stage 5 | OPEN — Stage 18 |
| KI-S5-07 | ClamAV runs in the local compose stack only; production deployment, signature-freshness monitoring and alerting on exhausted FAILED_SCAN retries are not done. | — | OPEN — Stage 18 |
| KI-S5-08 | Frontend: user verification flow smoke-tested against the real stack; **staff/admin pages tested against the mock API only**; no automated Playwright run against the real API. | UI ↔ API drift possible on admin pages | OPEN — real-stack E2E job when CI runs |
| KI-S5-09 | Payout destination `start-review` is accepted and does nothing (assignment starts the review). | API oddity | ACCEPTED (documented in OpenAPI) |
| KI-S5-10 | Identity-number reveal is limited by permission (COMPLIANCE), step-up, justification and audit, but not by case assignment. Its self-check covers a linked staff/personal account (fixed in Stage 5). | Broader than strictly needed | OPEN — Stage 14 review tooling (F-S5-08) |
| KI-S5-11 | `KYB_PERSON` documents can be uploaded but `GET /verification/documents` does not list that subject type (they appear in the KYB case view). | Minor API gap | OPEN — small fix |
| KI-S5-12 | Prerequisite errors differ: KYB start without a verified email → `403 EMAIL_NOT_VERIFIED`; KYC start without verified email/phone → `422 PREREQUISITES_NOT_MET`. | Client mapping | OPEN — align in Stage 6 |
| KI-S5-13 | `If-Match` parsing differs: verification handlers ignore an unparsable value; compliance rejects it (`422 INVALID_VERSION`). | Inconsistent contract | OPEN — align |
| KI-S5-14 | No backend reason-code catalogue; the web app ships its own UPPER_SNAKE lists. | Codes can drift | OPEN — Stage 14 |
| KI-S5-15 | Web proxy upload cap is a fixed number (10 MiB + 256 KiB) that must follow `UPLOAD_MAX_BYTES`. | Config drift risk | OPEN |
| KI-S5-16 | `ORG_REGISTERED_VERIFIED` and `ORG_PAYOUT_VERIFIED` are never granted (no registry lookup; no organisation payout-ownership upgrade). PVO fundraising-authority records not built. | Organisation payout readiness not modelled yet | OPEN — Stage 6 (fundraising authority) / Stage 11 |
| KI-S5-17 | **`BASIC_VERIFIED` is never granted:** kyc-architecture requires EMAIL_OTP + PHONE_OTP + AGE_ATTESTATION + device/IP risk not BLOCK; there is no age attestation and no device-risk check, so granting it would fabricate verification. `/kyc/status` shows `UNVERIFIED` until identity approval and `create_draft` is false for non-`IDENTITY_VERIFIED` users. | Campaign drafts (Stage 6) gated on identity | OPEN — Stage 6: add age attestation (LR-021) and decide the device-risk input, or change the gate by ADR |
| KI-S5-18 | `DOCUMENT_TICKET_TTL` accepts up to 10 min but tickets are capped at 5 min. | Config misleads | OPEN, low |
| KI-S5-19 | No policy-management UI (API only); no inline document viewer (downloads only, by design of `attachment`). | Admin usability | OPEN — Stage 14 |
| KI-S5-20 | `docs/api/` (Stage 2 endpoint catalogue) is not synced with the implemented Stage 5 endpoints; `api/openapi/fundzim-v1.yaml` is the source of truth. | Doc drift | ACCEPTED (as in Stage 4) |
| KI-S5-21 | Compliance resolutions (RESTRICT, SUSPEND, OFFBOARD…) are recorded and emitted but not enforced on accounts, campaigns or funds. | No automatic effect yet | OPEN — Stages 6/11/13 |
| KI-S5-22 | Retention purging of identity documents and evidence is not implemented (soft delete only). | Data kept longer than necessary | OPEN — LEGAL_REVIEW_REQUIRED (LR-012, LR-074) |
| KI-S5-23 | Integration tests create users, staff, cases and documents in the shared development database and re-run the bootstrap ceremony (revoking existing SUPER_ADMIN assignments). | Dev data only | ACCEPTED — `make reset` locally |
| KI-S5-24 | Stage 1–4 acceptance not recorded. | Process gap | OPEN — owner |
