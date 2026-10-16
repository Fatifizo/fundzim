# Stage acceptance register (PROPOSED)

> **Status: proposed template, created in Stage 8 (2026-10-12).** No stage has a recorded acceptance. This file
> records what is outstanding; it does **not** record any sign-off. Only the project owner may change a row to
> `ACCEPTED` or `ACCEPTED_WITH_CONDITIONS`, by adding the decision, the date, the evidence reference (for example a
> signed message or meeting minute) and the accepted conditions. Engineers and Claude Code sessions must never
> edit a row to an accepted state.

## How to record an acceptance

1. The owner reviews the stage's completion report and handover document.
2. The owner records, in the row: decision (`ACCEPTED`, `ACCEPTED_WITH_CONDITIONS`, `REJECTED`), date, name or
   role of the person accepting, evidence reference, and the conditions they accept.
3. The change is committed with the owner as author (or with a reference to the owner's written decision).

## Register

| Stage | Title | Completion report recommendation | Acceptance | Outstanding conditions (from each stage's report and known issues) |
|---|---|---|---|---|
| 0 | Foundation | see the stage report | **NOT_RECORDED** | — |
| 1 | Regulatory & compliance architecture | see the stage report | **NOT_RECORDED** | All `LEGAL_REVIEW_REQUIRED` items (open-legal-questions.md); provider confirmations (provider-questions.md) |
| 2 | System architecture & database design | see the stage report | **NOT_RECORDED** | Design decisions recorded in ADR-021 – ADR-031 |
| 3 | Core platform | see the stage report | **NOT_RECORDED** | See docs/stage-3 and STAGE-3-TO-STAGE-4.md |
| 4 | Authentication & identity | see the stage report | **NOT_RECORDED** | See docs/stage-4/known-issues.md |
| 5 | KYC/KYB, beneficiaries, payout destinations | see the stage report | **NOT_RECORDED** | Screening provider not selected (KI-S5-04); see docs/stage-5/known-issues.md |
| 6 | Campaign engine | PASS WITH CONDITIONS | **NOT_RECORDED** | LR-046 – LR-048, LR-070; see docs/stage-6/known-issues.md |
| 7 | Public fundraising experience | PASS WITH CONDITIONS | **NOT_RECORDED** | Manual WCAG 2.2 AA audit; link-preview/crawl verification; LR-012, LR-022, LR-035; field performance monitoring |
| 8 | Payment abstraction layer | PASS WITH CONDITIONS (sandbox only; production BLOCKED) | **NOT_RECORDED** | Production payment execution BLOCKED (see docs/stage-8/known-issues.md) |
| 9 | Zimbabwe payment provider integration readiness | see docs/stage-9 (provider integration and certification BLOCKED; no provider selected) | **NOT_RECORDED** | ADR-040 DECISION_DEFERRED; provider confirmations and legal items open; secret manager operation (Stage 18/20); see docs/stage-9/known-issues.md |
| 10 | Immutable double-entry ledger, posting engine, reconciliation foundation | see docs/stage-10 (TEST ledger only; production posting BLOCKED) | **NOT_RECORDED** | Accounting review of the chart and rules (KI-S10-02); production ledger database, backups and DR (KI-S10-06); settlement reconciliation BLOCKED (KI-S10-04); LR-001/002/003/012/076; see docs/stage-10/known-issues.md |
| 11 | Withdrawals & payouts | see docs/stage-11 (TEST payouts only; production payout execution BLOCKED) | **NOT_RECORDED** | No payout provider (ADR-040); settlement evidence (KI-S11-03); LR-020, LR-024, LR-030, LR-009 screening; hold registry; see docs/stage-11/known-issues.md |
| 12 | Fees & platform revenue | see docs/stage-12 (TEST fees only; production fee charging and revenue recognition BLOCKED) | **NOT_RECORDED** | Fee model PD-01/LR-028; tax LR-016/LR-045/LR-059; accounting review (KI-S10-02); provider fee evidence (ADR-040, PCR-016); PD-20, PD-23; see docs/stage-12/known-issues.md |
| 13 | Trust, fraud & risk engine | see docs/stage-13 (TEST data; production screening BLOCKED) | **NOT_RECORDED** | Screening provider and scope (LR-009/LR-053/LR-064/LR-065/LR-066); reporting duties (LR-008/LR-051/LR-060); automated decisions LR-071; retention LR-012/LR-034/LR-074; no approved rules; see docs/stage-13/known-issues.md |

## Effect of a missing acceptance

While acceptance is not recorded, work continues only as isolated, non-production engineering. Financial
execution stays disabled and the production gate stays **BLOCKED** (Stage 8 brief §3).
