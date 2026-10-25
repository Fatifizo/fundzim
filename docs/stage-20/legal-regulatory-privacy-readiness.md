# Stage 20 — Legal, regulatory and privacy readiness

**Legal/regulatory result: BLOCKED. Privacy result: BLOCKED.** This document states evidence only; it contains no legal
conclusion. Source: docs/compliance/open-legal-questions.md (authoritative `LR` register, last updated 2026-10-08),
which states: *"No item has been reviewed by counsel."* Counts verified on 2026-10-25: 101 rows, 90 unique IDs
(LR-001 … LR-090), 11 rows marked DUPLICATE, **31 P0 (launch-blocking) IDs**, none resolved.

| Topic | Evidence in the repository | Status | Decision required / responsible party | Required evidence |
|---|---|---|---|---|
| Operating and fundraising model (Model A, custody) | ADR-013; operating-model-decision.md; routing refuses settlement to FundZim accounts | NOT_VERIFIED legally — P0 LR-001, LR-003, LR-004, LR-036, LR-038, LR-039 | counsel; owner | written determination per register §6 |
| Who may fundraise (PVO Act) | LR-013, LR-046 – LR-048, LR-050 (P0); fundraising-authority evidence not built | BLOCKED | counsel; owner | determination; then implementation |
| AML status and controls | Stage 13 risk engine (TEST); LR-007 – LR-009, LR-051 (P0) | BLOCKED | counsel; compliance officer (not named, PD-28) | determination |
| Identity verification | Stage 5 KYC/KYB; LR-062 (P0) | BLOCKED | counsel | determination of required level |
| Payment-provider contractual requirements | no provider selected (ADR-040); 66 PCR OPEN | BLOCKED | owner; provider | contract, PCR answers |
| Customer and beneficiary terms | LR-015, LR-021, LR-022 (P0); no approved terms in the repository | NOT_PROVIDED | counsel; owner | approved terms |
| Donor consent | notification consent records (Stage 15); LR-010, LR-011, LR-023 | BLOCKED | counsel | consent basis |
| Privacy notices | no approved notice in the repository | NOT_PROVIDED | counsel; DPO (not named, LR-057) | approved notice |
| Data processing and storage | data classification, C3 KYC isolation (Stage 5/18); POTRAZ data-controller licence (LR-010, LR-057) not obtained | BLOCKED | owner; DPO | licence; register of processing |
| Data retention | `audit.retention_policies` CHECKs forbid invented periods; no periods exist (LR-012, LR-074; KI-S18-13) | BLOCKED | counsel | approved retention schedule |
| Data-subject requests | not implemented (KI-S18-14) | FAIL for production (no workflow) | owner, then engineering | workflow + tests |
| Campaign moderation and prohibited use | Stage 6/7 review, publication eligibility, compliance restrictions, public reports; LR-070 (minor/medical campaigns, P0) | PASS WITH CONDITIONS (engineering); legal scope BLOCKED | counsel | prohibited-use policy approval |
| Complaints and disputes | public reports (Stage 7); disputes deferred (ADR-039 §4); refund requests not executable | BLOCKED | owner; counsel; provider | complaints procedure; dispute process with the provider |
| Financial records and reporting | append-only ledger and audit; retention undetermined (LR-012) | BLOCKED | counsel; accountant | obligations and periods |
| Cross-border data / payments | LR-005 / LR-044 international rails, LR-043 ZiG (feature-gating P0); FundZim never converts currencies | BLOCKED | counsel; provider | determination per rail |
| Consumer protection | LR-056 (P0) | BLOCKED | counsel | determination |

Engineering keeps every dependency fail-closed (gates false, CHECK-limited TEST environments). Documentation existing
for a topic is not evidence that the requirement is met.
