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

## Effect of a missing acceptance

While acceptance is not recorded, work continues only as isolated, non-production engineering. Financial
execution stays disabled and the production gate stays **BLOCKED** (Stage 8 brief §3).
