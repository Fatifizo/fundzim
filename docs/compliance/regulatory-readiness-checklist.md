# FundZim Regulatory Readiness Checklist

> **Design completion is not legal approval.** A `DESIGN_COMPLETE` item means a design document exists in
> this repository. It does **not** mean counsel, a regulator, a payment provider, a bank or an auditor has
> reviewed or accepted it. FundZim may not accept real donor money or make real payouts until every item
> marked **Live-blocking** reaches the required status (Gate C in [ROADMAP.md](../ROADMAP.md): Stage 20).
> Passing a documentation stage is never permission to operate a live crowdfunding business.

Status: snapshot at Stage 1 (2026-10-08). Updated at the end of every stage; changes to a status require the
evidence listed.

Related: [regulatory-requirements-register.md](regulatory-requirements-register.md),
[open-legal-questions.md](open-legal-questions.md), [licensing-assessment.md](licensing-assessment.md),
[operating-model-decision.md](operating-model-decision.md),
[provider-due-diligence-checklist.md](../payments/provider-due-diligence-checklist.md),
[ROADMAP.md](../ROADMAP.md).

## 1. Status ladder

Statuses are cumulative and must be earned in order; each needs evidence.

| Status | Meaning | Evidence required to claim it |
|---|---|---|
| `NOT_STARTED` | No design yet | — |
| `DESIGN_COMPLETE` | A design or policy draft exists in the repo and has passed the stage's internal review | Document path; stage completion report |
| `IMPLEMENTED` | Built in code/config/process | Commit or PR references; configuration record |
| `TESTED` | Tested by FundZim (automated or documented manual test) | Test reports; test run ids |
| `EXTERNALLY_VERIFIED` | Reviewed by a qualified external party (counsel, auditor, provider, penetration tester) | Signed opinion, audit report, provider confirmation letter, pentest report |
| `APPROVED` | Accepted by the accountable authority (business owner/board, and the regulator or provider where their approval is required) | Signed approval, licence, registration certificate, executed contract |

"Required for live" = the minimum status before Stage 20 live pilot authorisation.

## 2. Checklist

| # | Item | Current status | Required for live | Evidence required | Owner | Blocking stage | Notes / dependencies |
|---|---|---|---|---|---|---|---|
| RR-01 | Operating model reviewed | DESIGN_COMPLETE ([operating-model-decision.md](operating-model-decision.md), ADR-013) | APPROVED | Counsel opinion on Model A; provider confirmation that it supports the model; business-owner sign-off | Business owner + counsel | Stage 9 (contract), Stage 20 | LR-001, LR-003, LR-004 |
| RR-02 | Legal entity requirements confirmed | NOT_STARTED | APPROVED | Entity registration documents; counsel confirmation of the entity type needed to contract with PSPs and banks | Business owner | Stage 9 | Also required for PSP onboarding |
| RR-03 | PSP agreement reviewed | NOT_STARTED (criteria in [provider-due-diligence-checklist.md](../payments/provider-due-diligence-checklist.md)) | APPROVED | Executed agreement; counsel review notes; confirmation that crowdfunding/donations are a permitted use | Business owner + counsel | Stage 9 | PCR items; LR-004 |
| RR-04 | Bank partnership requirements assessed | NOT_STARTED | EXTERNALLY_VERIFIED | Bank's onboarding requirements for FundZim's operating account (fee income only under Model A); confirmation that no client-money account is needed under Model A | FINANCE lead | Stage 9 | If Model B is ever considered, a separate ADR and licensing review are required |
| RR-05 | Applicable licensing confirmed | NOT_STARTED (analysis in [licensing-assessment.md](licensing-assessment.md)) | APPROVED | Written counsel opinion; any licence or registration certificate, or confirmation none is required | Counsel | Stage 20 | LR-004; NPS-related REQ items |
| RR-06 | Data protection registration / licensing and DPO | NOT_STARTED | APPROVED | Registration/licence with the data protection authority if required; DPO appointment record | Business owner + DPO | Stage 20 (before real personal data at scale; earlier if required for pilot users) | LR-010 |
| RR-07 | KYC policy approved | DESIGN_COMPLETE ([kyc-architecture.md](kyc-architecture.md), [kyb-architecture.md](kyb-architecture.md), [beneficiary-verification.md](beneficiary-verification.md)) | APPROVED | Approved policy document with thresholds; counsel review; KYC vendor contract | COMPLIANCE lead | Stage 5 (implementation), Stage 20 | LR-007 |
| RR-08 | AML / CFT policy approved | DESIGN_COMPLETE ([aml-risk-framework.md](aml-risk-framework.md), [transaction-monitoring.md](transaction-monitoring.md), [sanctions-screening.md](sanctions-screening.md)) | APPROVED | Approved AML policy, risk assessment, STR procedure; counsel confirmation of FundZim's AML status | COMPLIANCE lead + counsel | Stage 13, Stage 20 | LR-007, LR-008, LR-009 |
| RR-09 | Data protection reviewed (DPIA) | DESIGN_COMPLETE ([data-protection-assessment.md](data-protection-assessment.md), [PRIVACY.md](../PRIVACY.md)) | EXTERNALLY_VERIFIED | Completed DPIA reviewed by counsel/DPO; privacy notice; processor agreements | DPO | Stage 20 | LR-010, LR-011, LR-033–LR-035 |
| RR-10 | Consumer protection / donor protection policy reviewed | DESIGN_COMPLETE ([donor-protection-policy.md](donor-protection-policy.md)) | APPROVED | Counsel review; business decisions PD-30–PD-34 made; published refund terms | Business owner + counsel | Stage 20 | LR-018, LR-022, LR-085 (→ LR-080), LR-086, LR-088 |
| RR-11 | Terms of service, campaign terms, privacy notice | NOT_STARTED | APPROVED | Counsel-drafted documents; acceptance evidence design (terms versioning) | Counsel | Stage 7 (draft), Stage 20 | LR-022, LR-027 |
| RR-12 | Financial control design reviewed | DESIGN_COMPLETE ([settlement-and-custody-model.md](../ledger/settlement-and-custody-model.md), [LEDGER.md](../LEDGER.md), [operational-controls.md](operational-controls.md)) | EXTERNALLY_VERIFIED | Accountant/auditor review of ledger and custody accounting; maker-checker evidence | FINANCE lead | Stage 10, Stage 20 | LR-002 |
| RR-13 | Reconciliation design approved | DESIGN_COMPLETE (Stage 0 [LEDGER.md](../LEDGER.md) §10 + Stage 1 settlement model; full design Stage 17) | TESTED + EXTERNALLY_VERIFIED | Reconciliation against real provider test reports; auditor walkthrough | FINANCE lead | Stage 17 | PCR-007 — settlement report formats|
| RR-14 | Incident response defined | DESIGN_COMPLETE ([incident-response-workflows.md](incident-response-workflows.md)) | TESTED | Runbooks; tabletop exercise records; drill results (Stage 18) | SECURITY_ADMIN + COMPLIANCE lead | Stage 18 | LR-087 |
| RR-15 | Provider sandbox tested | NOT_STARTED | TESTED | Sandbox test reports for every rail in scope, including failure scenarios F1–F16 ([TESTING.md](../TESTING.md)) | Tech lead | Stage 9 | Needs sandbox access (PCR) |
| RR-16 | Live pilot authorisation confirmed where required | NOT_STARTED | APPROVED | Written confirmation from counsel (and regulator or provider where required) that a limited live pilot may run, with its conditions | Business owner + counsel | Stage 20 | All live-blocking items above |
| RR-17 | Payout controls and segregation of duties | DESIGN_COMPLETE ([payout-eligibility-and-controls.md](../payments/payout-eligibility-and-controls.md), [operational-controls.md](operational-controls.md)) | TESTED | Tests proving no self-approval, duplicate payout prevention, UNKNOWN handling; payout threshold values approved (LR-030) | FINANCE lead | Stage 11, Stage 19 | |
| RR-18 | Campaign approval policy and prohibited causes | DESIGN_COMPLETE ([campaign-approval-policy.md](campaign-approval-policy.md)) | APPROVED | Approved tier table and prohibited-cause list; counsel review of LR-013/LR-014/LR-015/LR-025 | COMPLIANCE lead | Stage 6, Stage 20 | |
| RR-19 | Exchange control position for international donations | NOT_STARTED (questions in [open-legal-questions.md](open-legal-questions.md)) | APPROVED | Counsel opinion; provider confirmation of the settlement currency for foreign cards | Counsel | Stage 20 (blocks international donations only) | LR-005 |
| RR-20 | ZiG acceptance and settlement position | NOT_STARTED | APPROVED | Counsel opinion; provider confirmation of ZWG support | Counsel + FINANCE lead | Stage 20 (blocks ZiG only) | LR-006; PD-11 |
| RR-21 | Tax registration and fee treatment | NOT_STARTED | EXTERNALLY_VERIFIED | Tax adviser opinion on VAT/income tax on fees and any transaction taxes; registrations | FINANCE lead | Stage 12, Stage 20 | LR-016, LR-017 |
| RR-22 | Charity / organisation fundraising rules | NOT_STARTED | APPROVED | Counsel opinion on which organisation types may fundraise and what registration evidence to require | Counsel | Stage 20 (blocks organisation campaigns) | LR-013 |
| RR-23 | Record retention schedule | NOT_STARTED | APPROVED | Approved retention schedule per retention class ([audit-evidence-model.md](audit-evidence-model.md) §7) | COMPLIANCE lead + counsel | Stage 17/18 | LR-012, LR-034 |
| RR-24 | Security assessment | NOT_STARTED | EXTERNALLY_VERIFIED | Independent penetration test; remediation evidence | SECURITY_ADMIN | Stage 18 | |
| RR-25 | Staff vetting and access review process | DESIGN_COMPLETE ([operational-controls.md](operational-controls.md) §6) | IMPLEMENTED + TESTED | First access review record; vetting per LR-089 | Business owner | Stage 14 | LR-089 |
| RR-26 | Complaints handling procedure | NOT_STARTED (outline in [donor-protection-policy.md](donor-protection-policy.md) §10) | APPROVED | Published procedure; SLA configuration | Business owner | Stage 20 | LR-086 |
| RR-27 | Regulator / law-enforcement request procedure | DESIGN_COMPLETE ([incident-response-workflows.md](incident-response-workflows.md) §13) | APPROVED | Counsel-approved procedure | COMPLIANCE lead | Stage 20 | LR-032 |
| RR-28 | KYC vendor due diligence | NOT_STARTED | EXTERNALLY_VERIFIED | Vendor contract, data processing agreement, data location, security review | COMPLIANCE lead | Stage 5 | LR-011, LR-033 |
| RR-29 | Business decisions for launch | NOT_STARTED | APPROVED | PD-01–PD-13 plus Stage 1 PDs decided and recorded | Business owner | Stage 20 (some earlier: PD-11 by Stage 9) | |

## 3. Live-blocking summary

Every item except RR-19, RR-20 and RR-22 is live-blocking for the whole platform. Those three block only the
feature they govern: international donations, ZiG, and organisation campaigns respectively. The pilot may launch
without those features if their items are unresolved.

## 4. Maintenance rules

- A status is raised only with its evidence linked in this table (or in a referenced evidence record).
- A status is **lowered** when its evidence lapses (contract expiry, policy past review date, legal change).
- Each stage completion report lists every status change in this checklist.
- Nobody may describe FundZim as licensed, approved, registered or compliant on the strength of this
  checklist ([CLAUDE.md](../../CLAUDE.md)).
